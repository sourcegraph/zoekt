package index

import (
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
)

// A chunk that mixes symbol matches with plain content matches must still
// return one SymbolInfo entry per Range, in the same order.
func TestScoreChunkSymbolInfoAlignment(t *testing.T) {
	// "target" appears on lines 1, 3 and 5. Only the one on line 3 is a symbol.
	content := "aa target\nbb\ncc target\ndd\nee target\n"
	symStart := uint32(len("aa target\nbb\ncc "))

	b := testShardBuilder(t, nil, Document{
		Name:            "f1",
		Content:         []byte(content),
		Symbols:         []DocumentSection{{symStart, symStart + 6}},
		SymbolsMetaData: []*zoekt.Symbol{{Kind: "function", Sym: "target"}},
	})

	q := query.NewOr(
		&query.Symbol{Expr: &query.Substring{Pattern: "target", CaseSensitive: true, Content: true}},
		&query.Substring{Pattern: "target", CaseSensitive: true, Content: true},
	)
	res := searchForTest(t, b, q, zoekt.SearchOptions{ChunkMatches: true, NumContextLines: 1})
	if len(res.Files) != 1 || len(res.Files[0].ChunkMatches) != 1 {
		t.Fatalf("want one file with one chunk, got %d files", len(res.Files))
	}
	cm := res.Files[0].ChunkMatches[0]
	if len(cm.Ranges) != 3 {
		t.Fatalf("want 3 ranges, got %d", len(cm.Ranges))
	}
	if cm.SymbolInfo == nil {
		t.Fatal("want SymbolInfo for the symbol match on line 3, got nil")
	}
	if len(cm.SymbolInfo) != len(cm.Ranges) {
		t.Fatalf("len(SymbolInfo) = %d, want %d to match Ranges", len(cm.SymbolInfo), len(cm.Ranges))
	}
	// Ranges are ordered by offset, so the symbol belongs to the middle one.
	for i, r := range cm.Ranges {
		wantSymbol := r.Start.LineNumber == 3
		if got := cm.SymbolInfo[i] != nil; got != wantSymbol {
			t.Errorf("range %d on line %d: SymbolInfo set = %v, want %v",
				i, r.Start.LineNumber, got, wantSymbol)
		}
	}

	// A short SymbolInfo also makes the display limit slice out of bounds.
	files := SortAndTruncateFiles(res.Files, &zoekt.SearchOptions{
		ChunkMatches:         true,
		NumContextLines:      1,
		MaxMatchDisplayCount: 2,
	})
	got := files[0].ChunkMatches[0]
	if len(got.SymbolInfo) != len(got.Ranges) {
		t.Errorf("after truncation len(SymbolInfo) = %d, want %d",
			len(got.SymbolInfo), len(got.Ranges))
	}
}
