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
	faulted := truncatedMappedPage(t)
	result := &zoekt.SearchResult{
		Files: []zoekt.FileMatch{
			{RepositoryID: 11, Repository: "healthy/before", Content: before},
			{RepositoryID: 22, Repository: "faulted/repo", Content: faulted},
			{RepositoryID: 22, Repository: "faulted/repo", Content: faulted},
			{RepositoryID: 33, Repository: "healthy/after", Content: after},
		},
	}

	origins := &fileMatchOrigins{}
	origins.register(&repairTestSearcher{name: "faulted.zoekt"}, result)
	faults := copyFiles(result, origins)
	if len(faults) != 1 {
		t.Fatalf("copy faults = %d, want 1", len(faults))
	}
	if faults[0].recovered == nil {
		t.Fatal("copyFiles reported a nil fault")
	}
	if len(faults[0].stack) == 0 {
		t.Fatal("copyFiles reported no stack")
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
	name       string
	page       []byte
	allFaulted bool
}

func (s *copyFaultWiringSearcher) Search(context.Context, query.Q, *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	before := []byte("before")
	after := []byte("after")
	if s.allFaulted {
		before = s.page
		after = s.page
	}
	return &zoekt.SearchResult{
		Files: []zoekt.FileMatch{
			{
				RepositoryID: 22,
				Repository:   "faulted/repository",
				FileName:     "healthy-before",
				Content:      before,
				Score:        40,
			},
			{
				RepositoryID: 22,
				Repository:   "faulted/repository",
				FileName:     "faulted",
				Content:      s.page,
				Score:        100,
			},
			{
				RepositoryID: 22,
				Repository:   "faulted/repository",
				FileName:     "also-faulted",
				Content:      s.page,
				Score:        90,
			},
			{
				RepositoryID: 22,
				Repository:   "faulted/repository",
				FileName:     "healthy-after",
				Content:      after,
				Score:        30,
			},
		},
		Stats: zoekt.Stats{
			FileCount:  4,
			MatchCount: 4,
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

func (s *copyFaultWiringSearcher) String() string { return s.name }

func newCopyFaultWiringSearcher(t *testing.T) (*shardedSearcher, *recordingRepairer) {
	t.Helper()
	repairer := &recordingRepairer{}
	ss := newShardedSearcher(1)
	ss.shardRepairs = newShardRepairQueue(repairer.reload)
	searcher := &copyFaultWiringSearcher{name: "copy-fault.zoekt", page: truncatedMappedPage(t)}
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

func TestSearchContainsFinalResultCopyFaultWithDisplayLimit(t *testing.T) {
	ss, repairer := newCopyFaultWiringSearcher(t)
	result, err := ss.Search(
		context.Background(),
		&query.Const{Value: true},
		&zoekt.SearchOptions{MaxDocDisplayCount: 3},
	)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Stats.Crashes != 1 {
		t.Fatalf("crashes = %d, want 1", result.Stats.Crashes)
	}
	for _, file := range result.Files {
		if file.FileName == "faulted" || file.FileName == "also-faulted" {
			t.Fatalf("faulted match %q escaped the limited final copy", file.FileName)
		}
	}
	waitForRepair(t, "limited copy-fault repair", func() bool {
		return repairer.count("copy-fault.zoekt") == 1
	})
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

func TestCopyFaultRepairsExactShardWhenRepositorySpansShards(t *testing.T) {
	repairer := &recordingRepairer{}
	ss := newShardedSearcher(2)
	ss.shardRepairs = newShardRepairQueue(repairer.reload)

	healthy := &copyFaultWiringSearcher{name: "healthy.zoekt", page: []byte("healthy")}
	faulted := &copyFaultWiringSearcher{
		name:       "faulted.zoekt",
		page:       truncatedMappedPage(t),
		allFaulted: true,
	}
	ss.replace(map[string]zoekt.Searcher{
		healthy.name: healthy,
		faulted.name: faulted,
	})
	ss.markReady()

	result, err := ss.Search(
		context.Background(),
		&query.Const{Value: true},
		&zoekt.SearchOptions{},
	)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Stats.Crashes != 1 {
		t.Fatalf("crashes = %d, want 1", result.Stats.Crashes)
	}
	waitForRepair(t, "faulted shard repair", ss.shardRepairs.idle)
	if got := repairer.count(faulted.name); got != 1 {
		t.Fatalf("faulted shard repair count = %d, want 1", got)
	}
	if got := repairer.count(healthy.name); got != 0 {
		t.Fatalf("healthy shard repair count = %d, want 0", got)
	}
}

type multiRepoCopyFaultSearcher struct {
	name string
	page []byte
}

func (s *multiRepoCopyFaultSearcher) Search(context.Context, query.Q, *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	return &zoekt.SearchResult{
		Files: []zoekt.FileMatch{
			{RepositoryID: 1, Repository: "org/one", FileName: "one.go", Content: s.page},
			{RepositoryID: 2, Repository: "org/two", FileName: "two.go", Content: s.page},
		},
		RepoURLs: map[string]string{
			"org/one": "",
			"org/two": "",
		},
		LineFragments: map[string]string{},
		Stats: zoekt.Stats{
			FileCount:  2,
			MatchCount: 2,
		},
	}, nil
}

func (s *multiRepoCopyFaultSearcher) List(context.Context, query.Q, *zoekt.ListOptions) (*zoekt.RepoList, error) {
	return &zoekt.RepoList{Repos: []*zoekt.RepoListEntry{
		{Repository: zoekt.Repository{ID: 1, Name: "org/one"}},
		{Repository: zoekt.Repository{ID: 2, Name: "org/two"}},
	}}, nil
}

func (*multiRepoCopyFaultSearcher) Stats() (*zoekt.RepoStats, error) {
	return &zoekt.RepoStats{}, nil
}
func (*multiRepoCopyFaultSearcher) Close()           {}
func (s *multiRepoCopyFaultSearcher) String() string { return s.name }

func TestStreamSearchCountsFaultedShardOnceAcrossRepositoryEvents(t *testing.T) {
	repairer := &recordingRepairer{}
	ss := newShardedSearcher(1)
	ss.shardRepairs = newShardRepairQueue(repairer.reload)
	searcher := &multiRepoCopyFaultSearcher{
		name: "multi-repo.zoekt",
		page: truncatedMappedPage(t),
	}
	ss.replace(map[string]zoekt.Searcher{searcher.name: searcher})
	ss.markReady()

	var crashes int
	sender := zoekt.SenderFunc(func(result *zoekt.SearchResult) {
		crashes += result.Stats.Crashes
	})
	if err := ss.StreamSearch(
		context.Background(),
		&query.Const{Value: true},
		&zoekt.SearchOptions{},
		sender,
	); err != nil {
		t.Fatalf("StreamSearch: %v", err)
	}
	if crashes != 1 {
		t.Fatalf("aggregate crashes = %d, want 1", crashes)
	}
	waitForRepair(t, "multi-repository copy-fault repair", ss.shardRepairs.idle)
	if got := repairer.count(searcher.name); got != 1 {
		t.Fatalf("repair count = %d, want 1", got)
	}
}

func TestAmbiguousCopyFaultDoesNotRepairEitherShard(t *testing.T) {
	repairer := &recordingRepairer{}
	ss := newShardedSearcher(2)
	ss.shardRepairs = newShardRepairQueue(repairer.reload)
	page := truncatedMappedPage(t)
	first := &copyFaultWiringSearcher{name: "first.zoekt", page: page, allFaulted: true}
	second := &copyFaultWiringSearcher{name: "second.zoekt", page: page, allFaulted: true}
	ss.replace(map[string]zoekt.Searcher{
		first.name:  first,
		second.name: second,
	})
	ss.markReady()

	result, err := ss.Search(
		context.Background(),
		&query.Const{Value: true},
		&zoekt.SearchOptions{},
	)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Stats.Crashes == 0 {
		t.Fatal("ambiguous copy fault was not reported")
	}
	waitForRepair(t, "ambiguous copy-fault handling", ss.shardRepairs.idle)
	if got := repairer.count(first.name); got != 0 {
		t.Fatalf("first shard repair count = %d, want 0", got)
	}
	if got := repairer.count(second.name); got != 0 {
		t.Fatalf("second shard repair count = %d, want 0", got)
	}
}
