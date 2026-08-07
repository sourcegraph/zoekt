package search

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
)

type repairTestSearcher struct {
	name        string
	panicSearch atomic.Bool
	panicList   atomic.Bool
}

func (s *repairTestSearcher) Search(context.Context, query.Q, *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	if s.panicSearch.Load() {
		panic(errors.New("mapped shard fault"))
	}
	return &zoekt.SearchResult{}, nil
}

func (s *repairTestSearcher) List(context.Context, query.Q, *zoekt.ListOptions) (*zoekt.RepoList, error) {
	if s.panicList.Load() {
		panic(errors.New("mapped shard fault"))
	}
	return &zoekt.RepoList{}, nil
}

func (s *repairTestSearcher) Stats() (*zoekt.RepoStats, error) {
	return &zoekt.RepoStats{}, nil
}

func (s *repairTestSearcher) Close() {}

func (s *repairTestSearcher) String() string { return s.name }

type recordingRepairer struct {
	mu     sync.Mutex
	counts map[string]int
	err    error
	onLoad func(string)
}

func (r *recordingRepairer) reload(key string, _ zoekt.Searcher) error {
	r.mu.Lock()
	if r.counts == nil {
		r.counts = make(map[string]int)
	}
	r.counts[key]++
	onLoad, err := r.onLoad, r.err
	r.mu.Unlock()

	if onLoad != nil {
		onLoad(key)
	}
	return err
}

func (r *recordingRepairer) count(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[key]
}

func waitForRepair(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func testSearcherWithRepairer(repairer *recordingRepairer) (*shardedSearcher, *repairTestSearcher, *rankedShard) {
	ss := newShardedSearcher(1)
	ss.shardRepairs = newShardRepairQueue(repairer.reload)
	searcher := &repairTestSearcher{name: "faulted.zoekt"}
	ss.replace(map[string]zoekt.Searcher{searcher.name: searcher})
	return ss, searcher, ss.getLoaded().shards[0]
}

func TestRecoveredSearchSchedulesShardRepairAndStaysIncomplete(t *testing.T) {
	repairer := &recordingRepairer{}
	ss, searcher, shard := testSearcherWithRepairer(repairer)
	searcher.panicSearch.Store(true)

	result, err := ss.searchOneShard(
		context.Background(),
		shard,
		&query.Const{Value: true},
		&zoekt.SearchOptions{},
	)
	if err != nil {
		t.Fatalf("search returned an error: %v", err)
	}
	if result.Stats.Crashes != 1 {
		t.Fatalf("crashes = %d, want 1", result.Stats.Crashes)
	}

	waitForRepair(t, "search-triggered repair", ss.shardRepairs.ready)
	if got := repairer.count(searcher.name); got != 1 {
		t.Fatalf("repair count = %d, want 1", got)
	}
}

func TestRecoveredListSchedulesShardRepair(t *testing.T) {
	repairer := &recordingRepairer{}
	ss, searcher, shard := testSearcherWithRepairer(repairer)
	searcher.panicList.Store(true)
	results := make(chan shardListResult, 1)

	ss.listOneShard(context.Background(), shard, &query.Const{Value: true}, nil, results)
	result := <-results
	if result.err != nil {
		t.Fatalf("list returned an error: %v", result.err)
	}
	if result.rl.Crashes != 1 {
		t.Fatalf("crashes = %d, want 1", result.rl.Crashes)
	}

	waitForRepair(t, "list-triggered repair", ss.shardRepairs.ready)
	if got := repairer.count(searcher.name); got != 1 {
		t.Fatalf("repair count = %d, want 1", got)
	}
}

func TestShardRepairsAreDeduplicatedAndConcurrencyBounded(t *testing.T) {
	const shardCount = shardRepairConcurrency * 3

	release := make(chan struct{})
	started := make(chan struct{}, shardCount)
	var running atomic.Int32
	var maximum atomic.Int32
	queue := newShardRepairQueue(func(string, zoekt.Searcher) error {
		current := running.Add(1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		running.Add(-1)
		return nil
	})

	searchers := make([]*repairTestSearcher, 0, shardCount)
	for i := range shardCount {
		searcher := &repairTestSearcher{name: fmt.Sprintf("queued-%02d.zoekt", i)}
		searchers = append(searchers, searcher)
		queue.register(searcher, searcher.name)
		if !queue.schedule(searcher) {
			t.Fatalf("repair %d was not scheduled", i)
		}
		if queue.schedule(searcher) {
			t.Fatalf("duplicate repair %d was scheduled", i)
		}
	}

	for range shardRepairConcurrency {
		<-started
	}
	if got := maximum.Load(); got != shardRepairConcurrency {
		t.Fatalf("maximum concurrent repairs = %d, want %d", got, shardRepairConcurrency)
	}
	if queue.ready() {
		t.Fatal("queue reported ready with repairs pending")
	}

	close(release)
	waitForRepair(t, "queued repairs", queue.ready)
	if got := maximum.Load(); got > shardRepairConcurrency {
		t.Fatalf("maximum concurrent repairs = %d, exceeds %d", got, shardRepairConcurrency)
	}
}

func TestFailedShardRepairKeepsReadinessFalseUntilRetry(t *testing.T) {
	repairer := &recordingRepairer{err: errors.New("stale file handle")}
	ss, searcher, shard := testSearcherWithRepairer(repairer)
	ss.markReady()

	ss.shardRepairs.schedule(shard)
	waitForRepair(t, "failed repair", func() bool {
		ss.shardRepairs.mu.Lock()
		defer ss.shardRepairs.mu.Unlock()
		_, unresolved := ss.shardRepairs.unresolved[searcher.name]
		return unresolved && ss.shardRepairs.running == 0
	})
	if ss.Ready() {
		t.Fatal("searcher reported ready after an unresolved repair failure")
	}

	repairer.mu.Lock()
	repairer.err = nil
	repairer.mu.Unlock()
	ss.shardRepairs.schedule(shard)
	waitForRepair(t, "successful repair retry", ss.shardRepairs.ready)
	if !ss.Ready() {
		t.Fatal("searcher did not become ready after a successful repair retry")
	}
}

func TestShardRepairPanicIsContainedUntilRetry(t *testing.T) {
	panicOnLoad := atomic.Bool{}
	panicOnLoad.Store(true)
	queue := newShardRepairQueue(func(string, zoekt.Searcher) error {
		if panicOnLoad.Load() {
			panic(errors.New("mapped shard fault during reload"))
		}
		return nil
	})
	searcher := &repairTestSearcher{name: "reload-fault.zoekt"}
	queue.register(searcher, searcher.name)

	queue.schedule(searcher)
	waitForRepair(t, "contained repair panic", func() bool {
		queue.mu.Lock()
		defer queue.mu.Unlock()
		_, unresolved := queue.unresolved[searcher.name]
		return unresolved && queue.running == 0
	})
	if queue.ready() {
		t.Fatal("queue reported ready after a repair panic")
	}

	panicOnLoad.Store(false)
	queue.schedule(searcher)
	waitForRepair(t, "repair retry after panic", queue.ready)
}

func TestWatcherUpdateClearsUnresolvedShardRepair(t *testing.T) {
	queue := newShardRepairQueue(func(string, zoekt.Searcher) error {
		return errors.New("stale file handle")
	})
	searcher := &repairTestSearcher{name: "removed.zoekt"}
	queue.register(searcher, searcher.name)
	queue.schedule(searcher)

	waitForRepair(t, "unresolved repair", func() bool {
		queue.mu.Lock()
		defer queue.mu.Unlock()
		_, unresolved := queue.unresolved[searcher.name]
		return unresolved
	})
	queue.unregister(searcher)
	if !queue.ready() {
		t.Fatal("queue stayed unready after the watcher removed the shard")
	}
}

func TestShardRepairDoesNotReplaceNewerOrRemovedShard(t *testing.T) {
	ss := newShardedSearcher(1)
	const key = "race.zoekt"
	original := &repairTestSearcher{name: "original"}
	newer := &repairTestSearcher{name: "newer"}
	replacement := &repairTestSearcher{name: "replacement"}

	ss.replace(map[string]zoekt.Searcher{key: original})
	faulted := ss.getLoaded().shards[0]
	ss.replace(map[string]zoekt.Searcher{key: newer})
	if ss.swapIfCurrent(key, faulted, replacement) {
		t.Fatal("repair replaced a newer shard")
	}

	ss.replace(map[string]zoekt.Searcher{key: nil})
	if ss.swapIfCurrent(key, faulted, replacement) {
		t.Fatal("repair resurrected a removed shard")
	}
}

func TestShardRepairReplacesCurrentShard(t *testing.T) {
	ss := newShardedSearcher(1)
	const key = "current.zoekt"
	original := &repairTestSearcher{name: "original"}
	replacement := &repairTestSearcher{name: "replacement"}

	ss.replace(map[string]zoekt.Searcher{key: original})
	faulted := ss.getLoaded().shards[0]
	if !ss.swapIfCurrent(key, faulted, replacement) {
		t.Fatal("repair did not replace the current faulted shard")
	}

	ss.mu.Lock()
	got := ss.shards[key]
	ss.mu.Unlock()
	if got == nil || got.Searcher != zoekt.Searcher(replacement) {
		t.Fatalf("installed shard = %v, want replacement", got)
	}
}
