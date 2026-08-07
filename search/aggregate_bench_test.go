package search

import (
	"context"
	"fmt"
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
)

const (
	benchmarkChunkRepositories = 128
	benchmarkChunkFiles        = 64
	benchmarkMatchesPerFile    = 4
)

// benchmarkResultSearcher returns one prebuilt result per repository. Each
// benchmark repository has its own result, so the search path can sort it
// without sharing mutable state between shards.
type benchmarkResultSearcher struct {
	repo   zoekt.Repository
	result *zoekt.SearchResult
}

func (s *benchmarkResultSearcher) Search(context.Context, query.Q, *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	return s.result, nil
}

func (s *benchmarkResultSearcher) List(context.Context, query.Q, *zoekt.ListOptions) (*zoekt.RepoList, error) {
	return &zoekt.RepoList{Repos: []*zoekt.RepoListEntry{{Repository: s.repo}}}, nil
}

func (s *benchmarkResultSearcher) Close() {}

func (s *benchmarkResultSearcher) String() string { return s.repo.Name }

func BenchmarkShardedSearchManyChunks(b *testing.B) {
	ss := newShardedSearcher(1)
	shards := make(map[string]zoekt.Searcher, benchmarkChunkRepositories)

	for repository := range benchmarkChunkRepositories {
		repo := zoekt.Repository{
			ID:   uint32(repository + 1),
			Name: fmt.Sprintf("benchmark-repository-%03d", repository),
		}
		files := make([]zoekt.FileMatch, benchmarkChunkFiles)
		for file := range files {
			// Each result chunk is sorted by score, but chunks interleave in the
			// final ordering. This makes the collector merge work representative
			// of results from many repositories.
			files[file] = zoekt.FileMatch{
				FileName:     fmt.Sprintf("%s/file-%03d.go", repo.Name, file),
				Repository:   repo.Name,
				RepositoryID: repo.ID,
				Score: float64((benchmarkChunkFiles-file)*benchmarkChunkRepositories +
					benchmarkChunkRepositories - repository),
				LineMatches: []zoekt.LineMatch{{
					LineFragments: make([]zoekt.LineFragmentMatch, benchmarkMatchesPerFile),
				}},
			}
		}

		shards[repo.Name] = &benchmarkResultSearcher{
			repo: repo,
			result: &zoekt.SearchResult{
				Files:         files,
				RepoURLs:      map[string]string{repo.Name: ""},
				LineFragments: map[string]string{},
				Stats: zoekt.Stats{
					MatchCount: benchmarkChunkFiles * benchmarkMatchesPerFile,
				},
			},
		}
	}
	ss.replace(shards)
	ss.markReady()

	benchmarks := []struct {
		name      string
		opts      zoekt.SearchOptions
		wantFiles int
	}{
		{name: "unlimited", wantFiles: benchmarkChunkRepositories * benchmarkChunkFiles},
		{name: "max-docs-8", opts: zoekt.SearchOptions{MaxDocDisplayCount: 8}, wantFiles: 8},
		{name: "max-docs-512", opts: zoekt.SearchOptions{MaxDocDisplayCount: 512}, wantFiles: 512},
		{name: "max-matches-1000", opts: zoekt.SearchOptions{MaxMatchDisplayCount: 1000}, wantFiles: 250},
	}

	ctx := context.Background()
	q := &query.Const{Value: true}
	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			var fileSlots int
			for b.Loop() {
				result, err := ss.Search(ctx, q, &benchmark.opts)
				if err != nil {
					b.Fatal(err)
				}
				if got := len(result.Files); got != benchmark.wantFiles {
					b.Fatalf("got %d files, want %d", got, benchmark.wantFiles)
				}
				fileSlots = cap(result.Files)
			}
			b.ReportMetric(float64(fileSlots), "file-slots/op")
		})
	}
}
