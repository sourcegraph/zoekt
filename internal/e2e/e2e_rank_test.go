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
		// The commit before ranking e2e tests were added to avoid matching
		// content inside our golden files.
		"https://github.com/sourcegraph/zoekt/commit/ef907c2371176aa3f97713d5bf182983ef090c6a",
	}
	q := func(query, target string) rankingQuery {
		return rankingQuery{Query: query, Target: target}
	}
	queries := []rankingQuery{
		// golang/go
		q("test server", "github.com/golang/go/src/net/http/httptest/server.go"),
		q("bytes buffer", "github.com/golang/go/src/bytes/buffer.go"),
		q("bufio buffer", "github.com/golang/go/src/bufio/scan.go"),
		q("time compare\\(", "github.com/golang/go/src/time/time.go"),

		// sourcegraph/sourcegraph
		q("graphql type User", "github.com/sourcegraph/sourcegraph/cmd/frontend/graphqlbackend/schema.graphql"),
		q("Get database/user", "github.com/sourcegraph/sourcegraph/internal/database/users.go"),
		q("InternalDoer", "github.com/sourcegraph/sourcegraph/internal/httpcli/client.go"),
		q("Repository metadata Write rbac", "github.com/sourcegraph/sourcegraph/internal/rbac/constants.go"), // unsure if this is the best doc?

		// cody
		q("generate unit test", "github.com/sourcegraph/cody/lib/shared/src/chat/recipes/generate-test.ts"),
		q("r:cody sourcegraph url", "github.com/sourcegraph/cody/lib/shared/src/sourcegraph-api/graphql/client.ts"),

		// zoekt
		q("zoekt searcher", "github.com/sourcegraph/zoekt/api.go"),

		// exact phrases
		q("assets are not configured for this binary", "github.com/sourcegraph/sourcegraph/ui/assets/assets.go"),
		q("sourcegraph/server docker image build", "github.com/sourcegraph/sourcegraph/dev/tools.go"),

		// symbols split up
		q("bufio flush writer", "github.com/golang/go/src/net/http/transfer.go"),                        // bufioFlushWriter
		q("coverage data writer", "github.com/golang/go/src/internal/coverage/encodecounter/encode.go"), // CoverageDataWriter
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

	var ranks []int
	for _, rq := range queries {
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
		}, rq.Query)

		t.Run(name, func(t *testing.T) {
			q, err := query.Parse(rq.Query)
			if err != nil {
				t.Fatal(err)
			}

			// q is marshalled as part of the test, so avoid our rewrites for
			// ranking.
			qSearch := query.ExpirementalPhraseBoost(q, rq.Query, query.ExperimentalPhraseBoostOptions{})

			sOpts := zoekt.SearchOptions{
				// Use the same options sourcegraph has by default
				ChunkMatches:       true,
				MaxWallTime:        20 * time.Second,
				ShardMaxMatchCount: 10_000 * 10,
				TotalMaxMatchCount: 100_000 * 10,
				MaxDocDisplayCount: 500,

				DebugScore: *debugScore,
			}
			result, err := ss.Search(context.Background(), qSearch, &sOpts)
			if err != nil {
				t.Fatal(err)
			}

			ranks = append(ranks, targetRank(rq, result.Files))

			var gotBuf bytes.Buffer
			marshalMatches(&gotBuf, rq, q, result.Files)
			assertGolden(t, name, gotBuf.Bytes())
		})
	}

	t.Run("rank_stats", func(t *testing.T) {
		if len(ranks) != len(queries) {
			t.Skip("not computing rank stats since not all query cases ran")
		}

		var gotBuf bytes.Buffer
		printf := func(format string, a ...any) {
			_, _ = fmt.Fprintf(&gotBuf, format, a...)
		}

		printf("queries: %d\n", len(ranks))

		for _, recallThreshold := range []int{1, 5} {
			count := 0
			for _, rank := range ranks {
				if rank <= recallThreshold && rank > 0 {
					count++
				}
			}
			countp := float64(count) * 100 / float64(len(ranks))
			printf("recall@%d: %d (%.0f%%)\n", recallThreshold, count, countp)
		}

		// Mean reciprocal rank
		mrr := float64(0)
		for _, rank := range ranks {
			if rank > 0 {
				mrr += 1 / float64(rank)
			}
		}
		mrr /= float64(len(ranks))
		printf("mrr: %f\n", mrr)

		assertGolden(t, "rank_stats", gotBuf.Bytes())
	})
}

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()

	wantPath := filepath.Join("testdata", name+".txt")
	if *update {
		if err := os.WriteFile(wantPath, got, 0o600); err != nil {
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
}

type rankingQuery struct {
	Query  string
	Target string
}

var (
	tarballCache = "/tmp/zoekt-test-ranking-tarballs-" + os.Getenv("USER")
	shardCache   = "/tmp/zoekt-test-ranking-shards-" + os.Getenv("USER")
)

func indexURL(indexDir, u string) error {
	if err := os.MkdirAll(tarballCache, 0o700); err != nil {
		return err
	}

	opts := archive.Options{
		Archive:     u,
		Incremental: true,
	}
	opts.SetDefaults() // sets metadata like Name and the codeload URL
	u = opts.Archive

	// if opts.Commit is set but opts.Branch is not, then we just need to give
	// the commit a name for testing.
	if opts.Commit != "" && opts.Branch == "" {
		opts.Branch = "test"
	}

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
	//	languageMap[lang] = ctags.ScipCTags
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

	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
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

func docName(f zoekt.FileMatch) string {
	return f.Repository + "/" + f.FileName
}

func marshalMatches(w io.Writer, rq rankingQuery, q query.Q, files []zoekt.FileMatch) {
	_, _ = fmt.Fprintf(w, "queryString: %s\n", rq.Query)
	_, _ = fmt.Fprintf(w, "query: %s\n", q)
	_, _ = fmt.Fprintf(w, "targetRank: %d\n\n", targetRank(rq, files))

	files, hiddenFiles := splitAtIndex(files, fileMatchesPerSearch)
	for _, f := range files {
		doc := docName(f)
		if doc == rq.Target {
			doc = "**" + doc + "**"
		}
		_, _ = fmt.Fprintf(w, "%s%s\n", doc, addTabIfNonEmpty(f.Debug))

		chunks, hidden := splitAtIndex(f.ChunkMatches, chunkMatchesPerFile)

		for _, m := range chunks {
			_, _ = fmt.Fprintf(w, "%d:%s%s", m.ContentStart.LineNumber, string(m.Content), addTabIfNonEmpty(m.DebugScore))
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

func targetRank(rq rankingQuery, files []zoekt.FileMatch) int {
	for i, f := range files {
		if docName(f) == rq.Target {
			return i + 1
		}
	}
	return -1
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
