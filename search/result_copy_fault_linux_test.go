//go:build linux

package search

import (
	"context"
	"os"
	"runtime/debug"
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
	"golang.org/x/sys/unix"
)

func truncatedMappedPage(t *testing.T) []byte {
	t.Helper()

	file, err := os.CreateTemp(t.TempDir(), "zoekt-copy-fault-*")
	if err != nil {
		t.Fatalf("create mapped file: %v", err)
	}
	pageSize := os.Getpagesize()
	if err := file.Truncate(int64(pageSize)); err != nil {
		t.Fatalf("size mapped file: %v", err)
	}
	data, err := unix.Mmap(
		int(file.Fd()),
		0,
		pageSize,
		unix.PROT_READ,
		unix.MAP_SHARED,
	)
	if err != nil {
		t.Fatalf("mmap file: %v", err)
	}
	t.Cleanup(func() {
		if err := unix.Munmap(data); err != nil {
			t.Errorf("munmap file: %v", err)
		}
	})
	name := file.Name()
	if err := file.Close(); err != nil {
		t.Fatalf("close mapped file: %v", err)
	}
	if err := os.Truncate(name, 0); err != nil {
		t.Fatalf("truncate mapped file: %v", err)
	}
	return data
}

func TestCopyFilesContainsMmapFaultAndDropsOnlyUnsafeMatch(t *testing.T) {
	before := []byte("before")
	after := []byte("after")
	result := &zoekt.SearchResult{
		Files: []zoekt.FileMatch{
			{RepositoryID: 11, Repository: "healthy/before", Content: before},
			{RepositoryID: 22, Repository: "faulted/repo", Content: truncatedMappedPage(t)},
			{RepositoryID: 33, Repository: "healthy/after", Content: after},
		},
	}

	var faultedRepositoryID uint32
	copyFiles(result, func(file *zoekt.FileMatch, recovered any, stack []byte) {
		if recovered == nil {
			t.Fatal("fault callback received a nil panic")
		}
		if len(stack) == 0 {
			t.Fatal("fault callback received no stack")
		}
		faultedRepositoryID = file.RepositoryID
	})

	if faultedRepositoryID != 22 {
		t.Fatalf("faulted repository ID = %d, want 22", faultedRepositoryID)
	}
	if result.Stats.Crashes != 1 {
		t.Fatalf("crashes = %d, want 1", result.Stats.Crashes)
	}
	if len(result.Files) != 2 {
		t.Fatalf("kept matches = %d, want 2", len(result.Files))
	}
	if result.Files[0].RepositoryID != 11 || result.Files[1].RepositoryID != 33 {
		t.Fatalf(
			"kept repository IDs = [%d %d], want [11 33]",
			result.Files[0].RepositoryID,
			result.Files[1].RepositoryID,
		)
	}

	before[0] = 'X'
	after[0] = 'Y'
	if string(result.Files[0].Content) != "before" || string(result.Files[1].Content) != "after" {
		t.Fatal("healthy match content still aliases its source slices")
	}
}

func TestCopyFileRestoresPanicOnFault(t *testing.T) {
	before := debug.SetPanicOnFault(false)
	defer debug.SetPanicOnFault(before)

	file := &zoekt.FileMatch{Content: truncatedMappedPage(t)}
	recovered, _ := copyFile(file)
	if recovered == nil {
		t.Fatal("copy of a truncated mapping did not fault")
	}
	if restored := debug.SetPanicOnFault(false); restored {
		t.Fatal("copyFile leaked panic-on-fault mode onto its caller")
	}
}

type copyFaultWiringSearcher struct {
	page []byte
}

func (s *copyFaultWiringSearcher) Search(context.Context, query.Q, *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	return &zoekt.SearchResult{
		Files: []zoekt.FileMatch{
			{
				RepositoryID: 22,
				Repository:   "faulted/repository",
				FileName:     "healthy-before",
				Content:      []byte("before"),
			},
			{
				RepositoryID: 22,
				Repository:   "faulted/repository",
				FileName:     "faulted",
				Content:      s.page,
			},
			{
				RepositoryID: 22,
				Repository:   "faulted/repository",
				FileName:     "healthy-after",
				Content:      []byte("after"),
			},
		},
		Stats: zoekt.Stats{
			FileCount:  3,
			MatchCount: 3,
		},
	}, nil
}

func (s *copyFaultWiringSearcher) List(context.Context, query.Q, *zoekt.ListOptions) (*zoekt.RepoList, error) {
	return &zoekt.RepoList{
		Repos: []*zoekt.RepoListEntry{{
			Repository: zoekt.Repository{
				ID:   22,
				Name: "faulted/repository",
			},
		}},
	}, nil
}

func (s *copyFaultWiringSearcher) Stats() (*zoekt.RepoStats, error) {
	return &zoekt.RepoStats{}, nil
}

func (s *copyFaultWiringSearcher) Close() {}

func (s *copyFaultWiringSearcher) String() string { return "copy-fault.zoekt" }

func newCopyFaultWiringSearcher(t *testing.T) (*shardedSearcher, *recordingRepairer) {
	t.Helper()
	repairer := &recordingRepairer{}
	ss := newShardedSearcher(1)
	ss.shardRepairs = newShardRepairQueue(repairer.reload)
	searcher := &copyFaultWiringSearcher{page: truncatedMappedPage(t)}
	ss.replace(map[string]zoekt.Searcher{searcher.String(): searcher})
	ss.markReady()
	return ss, repairer
}

func assertContainedCopyFault(t *testing.T, result *zoekt.SearchResult, repairer *recordingRepairer) {
	t.Helper()
	if result.Stats.Crashes != 1 {
		t.Fatalf("crashes = %d, want 1", result.Stats.Crashes)
	}
	if len(result.Files) != 2 {
		t.Fatalf("kept matches = %d, want 2", len(result.Files))
	}
	for _, file := range result.Files {
		if file.FileName == "faulted" {
			t.Fatal("faulted match escaped the final copy boundary")
		}
	}
	waitForRepair(t, "copy-fault repair", func() bool {
		return repairer.count("copy-fault.zoekt") == 1
	})
}

func TestSearchContainsFinalResultCopyFault(t *testing.T) {
	ss, repairer := newCopyFaultWiringSearcher(t)
	result, err := ss.Search(
		context.Background(),
		&query.Const{Value: true},
		&zoekt.SearchOptions{},
	)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	assertContainedCopyFault(t, result, repairer)
}

func TestStreamSearchContainsFinalResultCopyFault(t *testing.T) {
	ss, repairer := newCopyFaultWiringSearcher(t)
	var copied *zoekt.SearchResult
	sender := zoekt.SenderFunc(func(result *zoekt.SearchResult) {
		if len(result.Files) > 0 {
			copied = result
		}
	})
	if err := ss.StreamSearch(
		context.Background(),
		&query.Const{Value: true},
		&zoekt.SearchOptions{},
		sender,
	); err != nil {
		t.Fatalf("StreamSearch: %v", err)
	}
	if copied == nil {
		t.Fatal("StreamSearch sent no file results")
	}
	assertContainedCopyFault(t, copied, repairer)
}
