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
	name         string
	panicSearch  atomic.Bool
	panicList    atomic.Bool
	panicCorrupt atomic.Bool
	panicNil     atomic.Bool
	closed       atomic.Bool
}

type testMemoryFault struct{}

func (testMemoryFault) Error() string { return "mapped shard fault" }
func (testMemoryFault) Addr() uintptr { return 0x1234 }
func (testMemoryFault) RuntimeError() {}

func (s *repairTestSearcher) Search(context.Context, query.Q, *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	if s.panicSearch.Load() {
		if s.panicNil.Load() {
			var pointer *byte
			return &zoekt.SearchResult{Stats: zoekt.Stats{Crashes: int(*pointer)}}, nil
		}
		if s.panicCorrupt.Load() {
			panic(errors.New("corrupt shard"))
		}
		panic(testMemoryFault{})
	}
	return &zoekt.SearchResult{}, nil
}

func (s *repairTestSearcher) List(context.Context, query.Q, *zoekt.ListOptions) (*zoekt.RepoList, error) {
	if s.panicList.Load() {
		if s.panicCorrupt.Load() {
			panic(errors.New("corrupt shard"))
		}
		panic(testMemoryFault{})
	}
	return &zoekt.RepoList{}, nil
}

func (s *repairTestSearcher) Stats() (*zoekt.RepoStats, error) {
	return &zoekt.RepoStats{}, nil
}

func (s *repairTestSearcher) Close() { s.closed.Store(true) }

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

	waitForRepair(t, "search-triggered repair", ss.shardRepairs.idle)
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

	waitForRepair(t, "list-triggered repair", ss.shardRepairs.idle)
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
	if queue.idle() {
		t.Fatal("queue reported idle with repairs pending")
	}

	close(release)
	waitForRepair(t, "queued repairs", queue.idle)
	if got := maximum.Load(); got > shardRepairConcurrency {
		t.Fatalf("maximum concurrent repairs = %d, exceeds %d", got, shardRepairConcurrency)
	}
}

func TestFailedShardRepairDoesNotWithdrawReadinessAndCanRetry(t *testing.T) {
	repairer := &recordingRepairer{err: errors.New("stale file handle")}
	ss, _, shard := testSearcherWithRepairer(repairer)
	ss.markReady()

	ss.shardRepairs.schedule(shard)
	waitForRepair(t, "failed repair", ss.shardRepairs.idle)
	if !ss.Ready() {
		t.Fatal("failed repair withdrew readiness and prevented traffic-driven retry")
	}

	repairer.mu.Lock()
	repairer.err = nil
	repairer.mu.Unlock()
	if !ss.shardRepairs.schedule(shard) {
		t.Fatal("failed repair retained its single-flight slot")
	}
	waitForRepair(t, "successful repair retry", ss.shardRepairs.idle)
}

func TestShardRepairPanicIsContainedAndCanRetry(t *testing.T) {
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
	waitForRepair(t, "contained repair panic", queue.idle)

	panicOnLoad.Store(false)
	if !queue.schedule(searcher) {
		t.Fatal("repair panic retained its single-flight slot")
	}
	waitForRepair(t, "repair retry after panic", queue.idle)
}

func TestWatcherReplacementCanScheduleSamePathWhileOldRepairRuns(t *testing.T) {
	const key = "same-path.zoekt"
	oldRelease := make(chan struct{})
	var oldRuns atomic.Int32
	var newRuns atomic.Int32
	old := &repairTestSearcher{name: "old"}
	newer := &repairTestSearcher{name: "new"}
	queue := newShardRepairQueue(func(_ string, faulted zoekt.Searcher) error {
		switch faulted {
		case old:
			oldRuns.Add(1)
			<-oldRelease
		case newer:
			newRuns.Add(1)
		}
		return nil
	})

	queue.register(old, key)
	if !queue.schedule(old) {
		t.Fatal("old generation was not scheduled")
	}
	waitForRepair(t, "old generation repair start", func() bool { return oldRuns.Load() == 1 })

	queue.unregister(old)
	queue.register(newer, key)
	if !queue.schedule(newer) {
		t.Fatal("new generation was deduplicated against the old generation")
	}
	waitForRepair(t, "new generation repair", func() bool { return newRuns.Load() == 1 })

	close(oldRelease)
	waitForRepair(t, "both generation repairs", queue.idle)
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

func TestCorruptShardPanicIsContainedWithoutRepair(t *testing.T) {
	repairer := &recordingRepairer{}
	ss, searcher, shard := testSearcherWithRepairer(repairer)
	searcher.panicCorrupt.Store(true)
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
	if got := repairer.count(searcher.name); got != 0 {
		t.Fatalf("repair count = %d, want 0", got)
	}
}

func TestNilPointerPanicIsContainedWithoutRepair(t *testing.T) {
	repairer := &recordingRepairer{}
	ss, searcher, shard := testSearcherWithRepairer(repairer)
	searcher.panicNil.Store(true)
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
	if got := repairer.count(searcher.name); got != 0 {
		t.Fatalf("repair count = %d, want 0", got)
	}
}

type panicIndexFile struct {
	closed atomic.Bool
}

func (*panicIndexFile) Read(uint32, uint32) ([]byte, error) {
	panic(errors.New("fault while reading index"))
}
func (*panicIndexFile) Size() (uint32, error) { return 4096, nil }
func (f *panicIndexFile) Close()              { f.closed.Store(true) }
func (*panicIndexFile) Name() string          { return "panic.zoekt" }

func TestNewSearcherPanicClosesIndexFile(t *testing.T) {
	indexFile := &panicIndexFile{}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = newSearcherFromIndexFile(indexFile.Name(), indexFile)
	}()
	if recovered == nil {
		t.Fatal("newSearcherFromIndexFile did not panic")
	}
	if !indexFile.closed.Load() {
		t.Fatal("index file remained open after NewSearcher panic")
	}
}

func TestReloadInstallationPanicClosesReplacement(t *testing.T) {
	ss := newShardedSearcher(1)
	const key = "install-panic.zoekt"
	original := &repairTestSearcher{name: "original"}
	ss.replace(map[string]zoekt.Searcher{key: original})
	faulted := ss.getLoaded().shards[0]

	replacement := &repairTestSearcher{name: "replacement"}
	replacement.panicList.Store(true)
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		ss.installReloadedShard(key, faulted, replacement)
	}()
	if recovered == nil {
		t.Fatal("installReloadedShard did not panic")
	}
	if !replacement.closed.Load() {
		t.Fatal("replacement remained open after installation panic")
	}
}

func TestSupersededReloadClosesReplacement(t *testing.T) {
	ss := newShardedSearcher(1)
	const key = "superseded.zoekt"
	original := &repairTestSearcher{name: "original"}
	newer := &repairTestSearcher{name: "newer"}
	replacement := &repairTestSearcher{name: "replacement"}

	ss.replace(map[string]zoekt.Searcher{key: original})
	faulted := ss.getLoaded().shards[0]
	ss.replace(map[string]zoekt.Searcher{key: newer})

	if ss.installReloadedShard(key, faulted, replacement) {
		t.Fatal("superseded replacement was installed")
	}
	if !replacement.closed.Load() {
		t.Fatal("superseded replacement remained open")
	}
}
