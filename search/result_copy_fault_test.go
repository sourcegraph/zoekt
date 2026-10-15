package search

import (
	"testing"

	"github.com/sourcegraph/zoekt"
)

func TestCopyFilesCopiesHealthyMatches(t *testing.T) {
	content := []byte("content")
	checksum := []byte("checksum")
	line := []byte("line")
	chunk := []byte("chunk")
	result := &zoekt.SearchResult{Files: []zoekt.FileMatch{{
		Content:  content,
		Checksum: checksum,
		LineMatches: []zoekt.LineMatch{{
			Line: line,
		}},
		ChunkMatches: []zoekt.ChunkMatch{{
			Content: chunk,
		}},
	}}}

	faults := copyFiles(result, &fileMatchOrigins{})
	if len(faults) != 0 {
		t.Fatalf("copy faults = %d, want 0", len(faults))
	}
	if result.Stats.Crashes != 0 {
		t.Fatalf("crashes = %d, want 0", result.Stats.Crashes)
	}

	content[0] = 'X'
	checksum[0] = 'X'
	line[0] = 'X'
	chunk[0] = 'X'
	file := result.Files[0]
	if string(file.Content) != "content" ||
		string(file.Checksum) != "checksum" ||
		string(file.LineMatches[0].Line) != "line" ||
		string(file.ChunkMatches[0].Content) != "chunk" {
		t.Fatal("copied match still aliases an mmap-backed source slice")
	}
}

func TestFileMatchOriginsRejectAmbiguousBackingData(t *testing.T) {
	shared := []byte("shared")
	first := &repairTestSearcher{name: "first.zoekt"}
	second := &repairTestSearcher{name: "second.zoekt"}
	origins := &fileMatchOrigins{}
	origins.register(first, &zoekt.SearchResult{
		Files: []zoekt.FileMatch{{Content: shared}},
	})
	origins.register(second, &zoekt.SearchResult{
		Files: []zoekt.FileMatch{{Content: shared}},
	})

	file := &zoekt.FileMatch{Content: shared}
	if shard, record := origins.recordFault(fileMatchDataPointer(file), file); shard != nil || !record {
		t.Fatalf("ambiguous backing data resolved to %v, record = %v", shard, record)
	}
}
