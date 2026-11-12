package index

import (
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
)

// A chunk that mixes symbol matches with plain content matches must still
// return one SymbolInfo entry per Range, in the same order.
func TestScoreChunkSymbolInfoAlignment(t *testing.T) {
	// "target" appears on lines 1, 3 (twice) and 5. Only the second one on
	// line 3 is a symbol, so the line has a plain match before the symbol.
	content := "aa target\nbb\ncc target target\ndd\nee target\n"
	symStart := uint32(len("aa target\nbb\ncc target "))

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

	for _, bm25 := range []bool{false, true} {
		opts := zoekt.SearchOptions{ChunkMatches: true, NumContextLines: 1, UseBM25Scoring: bm25}
		res := searchForTest(t, b, q, opts)
		if len(res.Files) != 1 || len(res.Files[0].ChunkMatches) != 1 {
			t.Fatalf("bm25=%v: want one file with one chunk, got %d files", bm25, len(res.Files))
		}
		cm := res.Files[0].ChunkMatches[0]
		if len(cm.Ranges) != 4 {
			t.Fatalf("bm25=%v: want 4 ranges, got %d", bm25, len(cm.Ranges))
		}
		if len(cm.SymbolInfo) != len(cm.Ranges) {
			t.Fatalf("bm25=%v: len(SymbolInfo) = %d, want %d to match Ranges", bm25, len(cm.SymbolInfo), len(cm.Ranges))
		}
		// Ranges are ordered by offset, so the symbol belongs to the third one.
		for i, r := range cm.Ranges {
			wantSymbol := r.Start.ByteOffset == symStart
			if got := cm.SymbolInfo[i] != nil; got != wantSymbol {
				t.Errorf("bm25=%v: range %d at offset %d: SymbolInfo set = %v, want %v",
					bm25, i, r.Start.ByteOffset, got, wantSymbol)
			}
		}

		// A short SymbolInfo also makes the display limit slice out of bounds.
		opts.MaxMatchDisplayCount = 2
		files := SortAndTruncateFiles(res.Files, &opts)
		got := files[0].ChunkMatches[0]
		if len(got.SymbolInfo) != len(got.Ranges) {
			t.Errorf("bm25=%v: after truncation len(SymbolInfo) = %d, want %d",
				bm25, len(got.SymbolInfo), len(got.Ranges))
		}
	}
}
