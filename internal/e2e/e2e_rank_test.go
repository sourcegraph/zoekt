package e2e

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/build"
	"github.com/sourcegraph/zoekt/internal/archive"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/shards"
)

var update = flag.Bool("update", false, "update golden file")

var useShardCache = flag.Bool("shard_cache", false, "cache computed shards for faster test runs")

// debugScore can be set to include much more output. Do not commit the
// updated golden files, this is purely used for debugging in a local
// environment.
var debugScore = flag.Bool("debug_score", false, "include debug output in golden files.")

func TestRanking(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping due to short flag")
	}

	requireCTags(t)

	archiveURLs := []string{
		"https://github.com/sourcegraph/sourcegraph/tree/v5.2.2",
		"https://github.com/golang/go/tree/go1.21.4",
		"https://github.com/sourcegraph/cody/tree/vscode-v0.14.5",
	}
	queries := []string{
		// golang/go
		"test server",
		"bytes buffer",
		"bufio buffer",

		// sourcegraph/sourcegraph
		"graphql type User",
		"Get database/user",
		"InternalDoer",
		"Repository metadata Write rbac",

		// cody
		"generate unit test",
		"r:cody sourcegraph url",
	}

	var indexDir string
	if *useShardCache {
		t.Logf("reusing index dir to speed up testing. If you have unexpected results remove %s", shardCache)
		indexDir = shardCache
	} else {
		indexDir = t.TempDir()
	}

	for _, u := range archiveURLs {
		if err := indexURL(indexDir, u); err != nil {
			t.Fatal(err)
		}
	}

	ss, err := shards.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %v", indexDir, err)
	}
	defer ss.Close()

	for _, queryStr := range queries {
		// normalise queryStr for writing to fs
		name := strings.Map(func(r rune) rune {
			if strings.ContainsRune(" :", r) {
				return '_'
			}
			if '0' <= r && r <= '9' ||
				'a' <= r && r <= 'z' ||
				'A' <= r && r <= 'Z' {
				return r
			}
			return -1
		}, queryStr)

		t.Run(name, func(t *testing.T) {
			q, err := query.Parse(queryStr)
			if err != nil {
				t.Fatal(err)
			}

			sOpts := zoekt.SearchOptions{
				// Use the same options sourcegraph has by default
				ChunkMatches:       true,
				MaxWallTime:        20 * time.Second,
				ShardMaxMatchCount: 10_000 * 10,
				TotalMaxMatchCount: 100_000 * 10,
				MaxDocDisplayCount: 500,

				DebugScore: *debugScore,
			}
			result, err := ss.Search(context.Background(), q, &sOpts)
			if err != nil {
				t.Fatal(err)
			}

			var gotBuf bytes.Buffer
			marshalMatches(&gotBuf, queryStr, q, result.Files)
			got := gotBuf.Bytes()

			wantPath := filepath.Join("testdata", name+".txt")
			if *update {
				if err := os.WriteFile(wantPath, got, 0600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(wantPath)
			if err != nil {
				t.Fatal(err)
			}

			if d := cmp.Diff(string(want), string(got)); d != "" {
				t.Fatalf("unexpected (-want, +got):\n%s", d)
			}
		})
	}
}

var tarballCache = "/tmp/zoekt-test-ranking-tarballs-" + os.Getenv("USER")
var shardCache = "/tmp/zoekt-test-ranking-shards-" + os.Getenv("USER")

func indexURL(indexDir, u string) error {
	if err := os.MkdirAll(tarballCache, 0700); err != nil {
		return err
	}

	opts := archive.Options{
		Archive:     u,
		Incremental: true,
	}
	opts.SetDefaults() // sets metadata like Name and the codeload URL
	u = opts.Archive

	// update Archive location to cached location
	cacheBase := fmt.Sprintf("%s-%s%s.tar.gz", url.QueryEscape(opts.Name), opts.Branch, opts.Commit) // assume .tar.gz
	path := filepath.Join(tarballCache, cacheBase)
	opts.Archive = path

	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := download(u, path); err != nil {
			return err
		}
	}

	// TODO scip
	// languageMap := make(ctags.LanguageMap)
	// for _, lang := range []string{"kotlin", "rust", "ruby", "go", "python", "javascript", "c_sharp", "scala", "typescript", "zig"} {
	// 	languageMap[lang] = ctags.ScipCTags
	// }

	err := archive.Index(opts, build.Options{
		IndexDir:         indexDir,
		CTagsMustSucceed: true,
	})
	if err != nil {
		return fmt.Errorf("failed to index %s: %w", opts.Archive, err)
	}

	return nil
}

func download(url, dst string) error {
	tmpPath := dst + ".part"

	rc, err := archive.OpenReader(url)
	if err != nil {
		return err
	}
	defer rc.Close()

	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, rc)
	if err != nil {
		return err
	}

	err = f.Close()
	if err != nil {
		return err
	}

	return os.Rename(tmpPath, dst)
}

const (
	chunkMatchesPerFile  = 3
	fileMatchesPerSearch = 6
)

func marshalMatches(w io.Writer, queryStr string, q query.Q, files []zoekt.FileMatch) {
	_, _ = fmt.Fprintf(w, "queryString: %s\n", queryStr)
	_, _ = fmt.Fprintf(w, "query: %s\n\n", q)

	files, hiddenFiles := splitAtIndex(files, fileMatchesPerSearch)
	for _, f := range files {
		_, _ = fmt.Fprintf(w, "%s/%s%s\n", f.Repository, f.FileName, addTabIfNonEmpty(f.Debug))

		chunks, hidden := splitAtIndex(f.ChunkMatches, chunkMatchesPerFile)

		for _, m := range chunks {
			_, _ = fmt.Fprintf(w, "%d:%s%s\n", m.ContentStart.LineNumber, string(m.Content), addTabIfNonEmpty(m.DebugScore))
		}

		if len(hidden) > 0 {
			_, _ = fmt.Fprintf(w, "hidden %d more line matches\n", len(hidden))
		}
		_, _ = fmt.Fprintln(w)
	}

	if len(hiddenFiles) > 0 {
		fmt.Fprintf(w, "hidden %d more file matches\n", len(hiddenFiles))
	}
}

func splitAtIndex[E any](s []E, idx int) ([]E, []E) {
	if idx < len(s) {
		return s[:idx], s[idx:]
	}
	return s, nil
}

func addTabIfNonEmpty(s string) string {
	if s != "" {
		return "\t" + s
	}
	return s
}

func requireCTags(tb testing.TB) {
	tb.Helper()

	if os.Getenv("CTAGS_COMMAND") != "" {
		return
	}
	if _, err := exec.LookPath("universal-ctags"); err == nil {
		return
	}

	// On CI we require ctags to be available. Otherwise we skip
	if os.Getenv("CI") != "" {
		tb.Fatal("universal-ctags is missing")
	} else {
		tb.Skip("universal-ctags is missing")
	}
}
