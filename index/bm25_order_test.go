package index

import (
	"context"
	"strings"
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
)

// BM25 gives lines of the same length and term frequency the same score.
// Matches that tie must stay in file order, like they do with the default
// scoring, which adds a small bonus for earlier matches.
func TestSearchBM25KeepsTiedMatchesInFileOrder(t *testing.T) {
	var b strings.Builder
	for range 40 {
		b.WriteString("foo x\nfoo yyyyyyyyyyyyyyyyyyyy\n")
	}
	searcher := searcherForTest(t, testShardBuilder(t, nil, Document{Name: "a.txt", Content: []byte(b.String())}))
	defer searcher.Close()

	for _, chunks := range []bool{false, true} {
		res, err := searcher.Search(context.Background(), &query.Substring{Pattern: "foo", Content: true},
			&zoekt.SearchOptions{UseBM25Scoring: true, ChunkMatches: chunks})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Files) != 1 {
			t.Fatalf("got %d files, want 1", len(res.Files))
		}

		type match struct {
			line  int
			score float64
		}
		var ms []match
		for _, lm := range res.Files[0].LineMatches {
			ms = append(ms, match{lm.LineNumber, lm.Score})
		}
		for _, cm := range res.Files[0].ChunkMatches {
			ms = append(ms, match{int(cm.ContentStart.LineNumber), cm.Score})
		}
		if len(ms) != 80 {
			t.Fatalf("ChunkMatches=%v: got %d matches, want 80", chunks, len(ms))
		}

		var lines []int
		for _, m := range ms {
			lines = append(lines, m.line)
		}
		for i := 1; i < len(ms); i++ {
			if ms[i].score == ms[i-1].score && ms[i].line < ms[i-1].line {
				t.Fatalf("ChunkMatches=%v: line %d comes after line %d with the same score %.4f\nlines in result order: %v",
					chunks, ms[i].line, ms[i-1].line, ms[i].score, lines)
			}
		}
	}
}
