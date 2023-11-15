package main

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

	"github.com/google/go-cmp/cmp"
	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/build"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/shards"
)

var update = flag.Bool("update", false, "update golden file")
var noShardCache = flag.Bool("noShardCache", os.Getenv("CI") != "", "by default we reuse the shard cache to speed up testing")

func TestRanking(t *testing.T) {
	requireCTags(t)

	archiveURLs := []string{
		"https://github.com/sourcegraph/sourcegraph/tree/v5.2.2",
	}
	queries := []string{
		"graphql type User",
	}

	var indexDir string
	if *noShardCache {
		indexDir = t.TempDir()
	} else {
		t.Logf("reusing index dir to speed up testing. If you have unexpected results try go test -no_shard_cache or remove %s", shardCache)
		indexDir = shardCache
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
				DebugScore: true,
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

	opts := Options{
		Incremental: true,
		Archive:     u,
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

	err := do(opts, build.Options{
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

	rc, err := openReader(url)
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
	lineMatchesPerFile   = 3
	fileMatchesPerSearch = 6
)

func marshalMatches(w io.Writer, queryStr string, q query.Q, files []zoekt.FileMatch) {
	_, _ = fmt.Fprintf(w, "queryString: %s\n", queryStr)
	_, _ = fmt.Fprintf(w, "query: %s\n\n", q)

	files, hiddenFiles := splitAtIndex(files, fileMatchesPerSearch)
	for _, f := range files {
		_, _ = fmt.Fprintf(w, "%s/%s\t%s\n", f.Repository, f.FileName, f.Debug)

		lines, hidden := splitAtIndex(f.LineMatches, lineMatchesPerFile)

		for _, m := range lines {
			_, _ = fmt.Fprintf(w, "%d:%s\t%s\n", m.LineNumber, m.Line, m.DebugScore)
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
