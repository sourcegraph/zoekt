package search

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/internal/shardtest"
	"github.com/sourcegraph/zoekt/query"
)

func TestSearchTypeRepo(t *testing.T) {
	ss := newShardedSearcher(2)
	nextShardNum := 1
	addShard := func(docs ...index.Document) {
		b := shardtest.Builder(t, &zoekt.Repository{ID: 1, Name: "reponame"}, docs...)
		shard := shardtest.Searcher(t, b)
		ss.replace(map[string]zoekt.Searcher{fmt.Sprintf("key-%d", nextShardNum): shard})
		nextShardNum++
	}
	addShard(
		index.Document{Name: "f1", Content: []byte("bla the needle")},
		index.Document{Name: "f2", Content: []byte("another file another needle")})
	addShard(
		index.Document{Name: "f3", Content: []byte("another shard")})

	searcher := &typeRepoSearcher{ss}
	search := func(q query.Q, o ...zoekt.SearchOptions) *zoekt.SearchResult {
		t.Helper()
		var opts zoekt.SearchOptions
		if len(o) > 0 {
			opts = o[0]
		}
		res, err := searcher.Search(context.Background(), q, &opts)
		if err != nil {
			t.Fatalf("Search(%s): %v", q, err)
		}
		return res
	}
	wantSingleMatch := func(res *zoekt.SearchResult, want string) {
		t.Helper()
		fmatches := res.Files
		if len(fmatches) != 1 || len(fmatches[0].LineMatches) != 1 {
			t.Fatalf("got %v, want 1 matches", fmatches)
		}
		got := fmt.Sprintf("%s:%d", fmatches[0].FileName, fmatches[0].LineMatches[0].LineFragments[0].Offset)
		if got != want {
			t.Errorf("1: got %s, want %s", got, want)
		}
	}

	// type filter matches in different file
	res := search(query.NewAnd(
		&query.Type{
			Type:  query.TypeRepo,
			Child: &query.Substring{Pattern: "bla"},
		},
		&query.Substring{Pattern: "file"}))
	wantSingleMatch(res, "f2:8")

	// type filter matches in same file. Do not include that result
	res = search(query.NewAnd(
		&query.Type{
			Type:  query.TypeRepo,
			Child: &query.Substring{Pattern: "needle"},
		},
		&query.Substring{Pattern: "file"}))
	wantSingleMatch(res, "f2:8")

	// type filter matches path in different file
	res = search(query.NewAnd(
		&query.Type{
			Type:  query.TypeRepo,
			Child: &query.Substring{Pattern: "f1", FileName: true},
		},
		&query.Substring{Pattern: "file"}))
	wantSingleMatch(res, "f2:8")

	// type filter matches path in same file
	res = search(query.NewAnd(
		&query.Type{
			Type:  query.TypeRepo,
			Child: &query.Substring{Pattern: "f2", FileName: true},
		},
		&query.Substring{Pattern: "file"}))
	wantSingleMatch(res, "f2:8")

	// no match by content
	res = search(query.NewAnd(
		&query.Type{
			Type:  query.TypeRepo,
			Child: &query.Substring{Pattern: "nope"},
		},
		&query.Substring{Pattern: "file"}))
	if len(res.Files) != 0 {
		t.Fatalf("got %v, want 0 matches", len(res.Files))
	}

	// no index by path
	res = search(query.NewAnd(
		&query.Type{
			Type:  query.TypeRepo,
			Child: &query.Substring{Pattern: "nope", FileName: true},
		},
		&query.Substring{Pattern: "file"}))
	if len(res.Files) != 0 {
		t.Fatalf("got %v, want 0 matches", len(res.Files))
	}

	// type filter matches in a different shard
	res = search(query.NewAnd(
		&query.Type{
			Type:  query.TypeRepo,
			Child: &query.Substring{Pattern: "another shard"},
		},
		&query.Substring{Pattern: "file"}))
	wantSingleMatch(res, "f2:8")
}

// TestSearchTypeRepoReportsCrashes checks that a shard which crashes while
// evaluating a type:repo sub-query is reported. Its repositories are missing
// from the repo set, so the main search skips the shard and would not report
// the crash itself.
func TestSearchTypeRepoReportsCrashes(t *testing.T) {
	oldShardRecoveryLogger := shardRecoveryLogger
	shardRecoveryLogger = func() *slog.Logger { return slog.New(slog.DiscardHandler) }
	defer func() { shardRecoveryLogger = oldShardRecoveryLogger }()

	ss := newShardedSearcher(2)
	b := shardtest.Builder(t, &zoekt.Repository{ID: 1, Name: "reponame"},
		index.Document{Name: "f1", Content: []byte("needle in a file")})
	ss.replace(map[string]zoekt.Searcher{"key-1": shardtest.Searcher(t, b)})
	// replace calls List on the shard, which crashSearcher does not survive,
	// so add it to the ranked shards directly.
	ss.ranked.Store([]*rankedShard{
		ss.getLoaded().shards[0],
		{
			Searcher: &crashSearcher{},
			repos:    []*zoekt.Repository{{ID: 2, Name: "crashrepo"}},
		},
	})
	ss.markReady()

	searcher := &typeRepoSearcher{ss}
	q := query.NewAnd(
		&query.Type{Type: query.TypeRepo, Child: &query.Substring{Pattern: "needle"}},
		&query.Substring{Pattern: "file"})

	t.Run("Search", func(t *testing.T) {
		res, err := searcher.Search(context.Background(), q, &zoekt.SearchOptions{})
		require.NoError(t, err)
		assert.Len(t, res.Files, 1)
		assert.Equal(t, 1, res.Crashes)
	})

	t.Run("StreamSearch", func(t *testing.T) {
		var files int
		var stats zoekt.Stats
		err := searcher.StreamSearch(context.Background(), q, &zoekt.SearchOptions{}, zoekt.SenderFunc(func(sr *zoekt.SearchResult) {
			files += len(sr.Files)
			stats.Add(sr.Stats)
		}))
		require.NoError(t, err)
		assert.Equal(t, 1, files)
		assert.Equal(t, 1, stats.Crashes)
	})

	t.Run("List", func(t *testing.T) {
		rl, err := searcher.List(context.Background(), q, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, rl.Crashes)
	})
}
