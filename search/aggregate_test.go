package search

import (
	"slices"
	"testing"

	"github.com/sourcegraph/zoekt"
)

func TestCollectSenderUnlimitedSortsOnDone(t *testing.T) {
	collector := newCollectSender(&zoekt.SearchOptions{})
	collector.Send(&zoekt.SearchResult{Files: []zoekt.FileMatch{
		collectTestFile("lower.go", 1, 1),
	}})
	collector.Send(&zoekt.SearchResult{Files: []zoekt.FileMatch{
		collectTestFile("higher.go", 2, 1),
	}})

	if got, want := collectTestFileNames(collector.aggregate.Files), []string{"lower.go", "higher.go"}; !slices.Equal(got, want) {
		t.Fatalf("collector sorted before Done: got %v, want %v", got, want)
	}

	result, ok := collector.Done()
	if !ok {
		t.Fatal("Done returned no result")
	}
	if got, want := collectTestFileNames(result.Files), []string{"higher.go", "lower.go"}; !slices.Equal(got, want) {
		t.Fatalf("wrong final order: got %v, want %v", got, want)
	}
	if _, ok := collector.Done(); ok {
		t.Fatal("second Done returned a result")
	}
}

func TestCollectSenderDocumentLimitKeepsNovelExtension(t *testing.T) {
	collector := newCollectSender(&zoekt.SearchOptions{MaxDocDisplayCount: 3})
	collector.Send(&zoekt.SearchResult{Files: []zoekt.FileMatch{
		collectTestFile("first.go", 100, 1),
		collectTestFile("second.go", 99, 1),
		collectTestFile("third.go", 98, 1),
	}})
	collector.Send(&zoekt.SearchResult{Files: []zoekt.FileMatch{
		collectTestFile("novel.rs", 97, 1),
	}})

	if got := len(collector.aggregate.Files); got != 3 {
		t.Fatalf("got %d buffered files, want 3", got)
	}
	result, ok := collector.Done()
	if !ok {
		t.Fatal("Done returned no result")
	}
	if got, want := collectTestFileNames(result.Files), []string{"first.go", "second.go", "novel.rs"}; !slices.Equal(got, want) {
		t.Fatalf("wrong document-limited files: got %v, want %v", got, want)
	}
}

func TestCollectSenderMatchLimit(t *testing.T) {
	collector := newCollectSender(&zoekt.SearchOptions{MaxMatchDisplayCount: 3})
	collector.Send(&zoekt.SearchResult{Files: []zoekt.FileMatch{
		collectTestFile("first.go", 100, 2),
		collectTestFile("second.go", 99, 2),
	}})
	collector.Send(&zoekt.SearchResult{Files: []zoekt.FileMatch{
		collectTestFile("later.go", 101, 1),
	}})

	if got := collectTestLineFragmentCount(collector.aggregate.Files); got != 3 {
		t.Fatalf("got %d buffered line fragments, want 3", got)
	}
	result, ok := collector.Done()
	if !ok {
		t.Fatal("Done returned no result")
	}
	if got, want := collectTestFileNames(result.Files), []string{"later.go", "first.go"}; !slices.Equal(got, want) {
		t.Fatalf("wrong match-limited files: got %v, want %v", got, want)
	}
	if got := collectTestLineFragmentCount(result.Files); got != 3 {
		t.Fatalf("got %d final line fragments, want 3", got)
	}
}

func TestCollectSenderMatchLimitBoundsAggregate(t *testing.T) {
	collector := newCollectSender(&zoekt.SearchOptions{MaxMatchDisplayCount: 4})
	for chunk := range 32 {
		files := make([]zoekt.FileMatch, 8)
		for file := range files {
			files[file] = collectTestFile("match.go", float64(chunk*len(files)+file), 1)
		}
		collector.Send(&zoekt.SearchResult{Files: files})

		if got := len(collector.aggregate.Files); got != 4 {
			t.Fatalf("chunk %d: got %d buffered files, want 4", chunk, got)
		}
		if got := collectTestLineFragmentCount(collector.aggregate.Files); got != 4 {
			t.Fatalf("chunk %d: got %d buffered line fragments, want 4", chunk, got)
		}
	}

	result, ok := collector.Done()
	if !ok {
		t.Fatal("Done returned no result")
	}
	if got := len(result.Files); got != 4 {
		t.Fatalf("got %d final files, want 4", got)
	}
	if got := collectTestLineFragmentCount(result.Files); got != 4 {
		t.Fatalf("got %d final line fragments, want 4", got)
	}
}

func collectTestFile(name string, score float64, lineFragments int) zoekt.FileMatch {
	return zoekt.FileMatch{
		FileName: name,
		Score:    score,
		LineMatches: []zoekt.LineMatch{{
			LineFragments: make([]zoekt.LineFragmentMatch, lineFragments),
		}},
	}
}

func collectTestFileNames(files []zoekt.FileMatch) []string {
	names := make([]string, len(files))
	for i := range files {
		names[i] = files[i].FileName
	}
	return names
}

func collectTestLineFragmentCount(files []zoekt.FileMatch) int {
	var count int
	for i := range files {
		for j := range files[i].LineMatches {
			count += len(files[i].LineMatches[j].LineFragments)
		}
	}
	return count
}
