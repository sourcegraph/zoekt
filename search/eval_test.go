package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

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

// typeRepoQuery is the shape Sourcegraph sends for repo:has.content.
func typeRepoQuery() query.Q {
	return query.NewAnd(
		&query.Type{Type: query.TypeRepo, Child: &query.Substring{Pattern: "needle"}},
		&query.Substring{Pattern: "file"})
}

// TestSearchTypeRepoMaxWallTime checks that MaxWallTime also bounds the List
// calls that evaluate type:repo sub-queries. Otherwise a search runs for as
// long as its sub-queries take, plus MaxWallTime.
func TestSearchTypeRepoMaxWallTime(t *testing.T) {
	const maxWallTime = time.Minute
	opts := &zoekt.SearchOptions{MaxWallTime: maxWallTime}

	for _, tc := range []struct {
		name   string
		search func(*typeRepoSearcher) error
	}{
		{"Search", func(s *typeRepoSearcher) error {
			_, err := s.Search(context.Background(), typeRepoQuery(), opts)
			return err
		}},
		{"StreamSearch", func(s *typeRepoSearcher) error {
			return s.StreamSearch(context.Background(), typeRepoQuery(), opts, zoekt.SenderFunc(func(*zoekt.SearchResult) {}))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shard := &deadlineSearcher{}
			ss := newShardedSearcher(1)
			ss.ranked.Store([]*rankedShard{{Searcher: shard}})

			if err := tc.search(&typeRepoSearcher{ss}); err != nil {
				t.Fatal(err)
			}

			if latest := time.Now().Add(maxWallTime); shard.listDeadline.IsZero() || shard.listDeadline.After(latest) {
				t.Errorf("type:repo sub-query listed the shard with deadline %v, want at most %v", shard.listDeadline, latest)
			}
		})
	}
}

// TestSearchTypeRepoShedding checks that a search is rejected with
// ErrOverloaded when its type:repo sub-query can't get a scheduler slot in
// time, like the search itself would be.
func TestSearchTypeRepoShedding(t *testing.T) {
	ss := newShardedSearcher(1)
	ss.ranked.Store([]*rankedShard{{Searcher: &deadlineSearcher{}}})
	for _, class := range []zoekt.SchedulingClass{zoekt.SchedulingClassInteractive, zoekt.SchedulingClassBatch} {
		proc, err := ss.sched.Acquire(context.Background(), class)
		if err != nil {
			t.Fatal(err)
		}
		defer proc.Release()
	}
	searcher := &typeRepoSearcher{ss}
	opts := &zoekt.SearchOptions{MaxWallTime: 50 * time.Millisecond}

	// The client would wait much longer than the admission deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := searcher.Search(ctx, typeRepoQuery(), opts); !errors.Is(err, zoekt.ErrOverloaded) {
		t.Errorf("Search: got %v, want ErrOverloaded", err)
	}
	if err := searcher.StreamSearch(ctx, typeRepoQuery(), opts, zoekt.SenderFunc(func(*zoekt.SearchResult) {})); !errors.Is(err, zoekt.ErrOverloaded) {
		t.Errorf("StreamSearch: got %v, want ErrOverloaded", err)
	}
}

// TestSearchTypeRepoSchedulingClass checks that the type:repo sub-queries of
// a batch search are scheduled as batch too. A List on its own stays
// interactive.
func TestSearchTypeRepoSchedulingClass(t *testing.T) {
	sched := &recordingScheduler{}
	ss := newShardedSearcher(1)
	ss.sched = sched
	ss.ranked.Store([]*rankedShard{{Searcher: &deadlineSearcher{}}})
	searcher := &typeRepoSearcher{ss}
	opts := &zoekt.SearchOptions{SchedulingClass: zoekt.SchedulingClassBatch}

	_, err := searcher.Search(context.Background(), typeRepoQuery(), opts)
	require.NoError(t, err)
	err = searcher.StreamSearch(context.Background(), typeRepoQuery(), opts, zoekt.SenderFunc(func(*zoekt.SearchResult) {}))
	require.NoError(t, err)
	_, err = searcher.List(context.Background(), typeRepoQuery(), nil)
	require.NoError(t, err)

	batch, interactive := zoekt.SchedulingClassBatch, zoekt.SchedulingClassInteractive
	want := []zoekt.SchedulingClass{
		batch, batch, // Search: sub-query, then the search
		batch, batch, // StreamSearch: sub-query, then the search
		interactive, interactive, // List: sub-query, then the list
	}
	assert.Equal(t, want, sched.classes)
}
