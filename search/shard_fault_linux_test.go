//go:build linux

package search

import (
	"context"
	"runtime/debug"
	"sync/atomic"
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
	"golang.org/x/sys/unix"
)

var faultReadSink byte

type mmapFaultSearcher struct {
	page      []byte
	faultList atomic.Bool
}

func (s *mmapFaultSearcher) Search(context.Context, query.Q, *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	faultReadSink = s.page[0]
	return &zoekt.SearchResult{}, nil
}

func (s *mmapFaultSearcher) List(context.Context, query.Q, *zoekt.ListOptions) (*zoekt.RepoList, error) {
	if s.faultList.Load() {
		faultReadSink = s.page[0]
	}
	return &zoekt.RepoList{}, nil
}

func (s *mmapFaultSearcher) Stats() (*zoekt.RepoStats, error) {
	return &zoekt.RepoStats{}, nil
}

func (s *mmapFaultSearcher) Close() {}

func (s *mmapFaultSearcher) String() string { return "mmap-fault.zoekt" }

func inaccessibleMappedPage(t *testing.T) []byte {
	t.Helper()
	data, err := unix.Mmap(
		-1,
		0,
		unix.Getpagesize(),
		unix.PROT_READ|unix.PROT_WRITE,
		unix.MAP_PRIVATE|unix.MAP_ANONYMOUS,
	)
	if err != nil {
		t.Fatalf("mmap: %v", err)
	}
	t.Cleanup(func() {
		if err := unix.Munmap(data); err != nil {
			t.Errorf("munmap: %v", err)
		}
	})
	if err := unix.Mprotect(data, unix.PROT_NONE); err != nil {
		t.Fatalf("mprotect: %v", err)
	}
	return data
}

func TestShardMmapFaultsAreRecoveredAndPanicModeIsRestored(t *testing.T) {
	before := debug.SetPanicOnFault(false)
	defer debug.SetPanicOnFault(before)

	ss := newShardedSearcher(1)
	ss.shardRepairs = newShardRepairQueue(func(string, zoekt.Searcher) error { return nil })
	searcher := &mmapFaultSearcher{page: inaccessibleMappedPage(t)}
	ss.replace(map[string]zoekt.Searcher{searcher.String(): searcher})
	shard := ss.getLoaded().shards[0]

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
		t.Fatalf("search crashes = %d, want 1", result.Stats.Crashes)
	}

	searcher.faultList.Store(true)
	listResults := make(chan shardListResult, 1)
	ss.listOneShard(context.Background(), shard, &query.Const{Value: true}, nil, listResults)
	if result := <-listResults; result.rl.Crashes != 1 {
		t.Fatalf("list crashes = %d, want 1", result.rl.Crashes)
	}

	if restored := debug.SetPanicOnFault(false); restored {
		t.Fatal("per-goroutine panic-on-fault mode was not restored")
	}
}
