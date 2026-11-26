package index

import (
	"context"
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
)

// BM25 sums a saturating score per term, so a file matching two different
// terms once each beats a file of the same length matching one term three
// times. Terms shorter than an ngram and regexp terms are matched by regexps
// instead of substrings, and must still count as separate terms.
func TestSearchBM25CountsRegexpTermsSeparately(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		one   string // matches one term three times
		both  string // matches both terms once
	}{
		{"substrings", "foo or bar", "foo foo foo\n", "foo bar zzz\n"},
		{"short substrings", "fo or ba", "fo fo fo zz\n", "fo ba zz zz\n"},
		{"regexps", "f.o or b.r", "foo foo foo\n", "foo bar zzz\n"},
		{"case sensitive regexps", "case:yes F.o or f.o", "foo foo foo\n", "Foo foo zzz\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			searcher := searcherForTest(t, testShardBuilder(t, nil,
				Document{Name: "one.txt", Content: []byte(tc.one)},
				Document{Name: "both.txt", Content: []byte(tc.both)},
			))
			defer searcher.Close()

			q, err := query.Parse(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			for _, chunks := range []bool{false, true} {
				res, err := searcher.Search(context.Background(), q, &zoekt.SearchOptions{UseBM25Scoring: true, ChunkMatches: chunks, DebugScore: true})
				if err != nil {
					t.Fatal(err)
				}
				scores := map[string]float64{}
				debug := map[string]string{}
				for _, f := range res.Files {
					scores[f.FileName] = f.Score
					debug[f.FileName] = f.Debug
				}
				if len(scores) != 2 {
					t.Fatalf("got %d files, want 2", len(scores))
				}
				if scores["both.txt"] <= scores["one.txt"] {
					t.Errorf("ChunkMatches=%v: both.txt scores %.4f, one.txt scores %.4f, want both.txt first\nboth.txt: %s\none.txt:  %s",
						chunks, scores["both.txt"], scores["one.txt"], debug["both.txt"], debug["one.txt"])
				}
			}
		})
	}
}
