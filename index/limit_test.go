package index

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sourcegraph/zoekt"
)

func TestLimitMatches(t *testing.T) {
	cases := []struct {
		// Represents a SearchResult with three dimensions:
		// 1. outer slice is `Files`
		// 2. inner slice is `{Chunk,Line}Matches`
		// 3. value is the length of `Ranges`/`LineFragments`
		in       [][]int
		limit    int
		expected [][]int
	}{{
		in:       [][]int{{1, 1, 1}},
		limit:    1,
		expected: [][]int{{1}},
	}, {
		in:       [][]int{{1, 1, 1}},
		limit:    3,
		expected: [][]int{{1, 1, 1}},
	}, {
		in:       [][]int{{1, 1, 1}},
		limit:    4,
		expected: [][]int{{1, 1, 1}},
	}, {
		in:       [][]int{{2, 2, 2}},
		limit:    4,
		expected: [][]int{{2, 2}},
	}, {
		in:       [][]int{{2, 2, 2}},
		limit:    3,
		expected: [][]int{{2, 1}},
	}, {
		in:       [][]int{{2, 2, 2}},
		limit:    1,
		expected: [][]int{{1}},
	}, {
		in:       [][]int{{1}, {1}},
		limit:    2,
		expected: [][]int{{1}, {1}},
	}, {
		in:       [][]int{{1}, {1}},
		limit:    1,
		expected: [][]int{{1}},
	}, {
		in:       [][]int{{1}, {1, 3}},
		limit:    4,
		expected: [][]int{{1}, {1, 2}},
	}, {
		in:       [][]int{{1}, {2, 2}, {3, 3, 3}},
		limit:    4,
		expected: [][]int{{1}, {2, 1}},
	}}

	for _, tc := range cases {
		t.Run("ChunkMatches", func(t *testing.T) {
			// Generate a ChunkMatch suitable for testing `LimitChunkMatches`.
			generateChunkMatch := func(numRanges, lineNumber int) (zoekt.ChunkMatch, int) {
				cm := zoekt.ChunkMatch{SymbolInfo: make([]*zoekt.Symbol, numRanges)}

				// To simplify testing, we generate Content and the associated
				// Ranges with fixed logic: each ChunkMatch has 1 line of
				// context, and each Range spans two lines. It'd probably be
				// better to do some kind of property-based testing, but this is
				// alright.

				// 1 line of context.
				cm.Content = append(cm.Content, []byte("context\n")...)
				for i := 0; i < numRanges; i += 1 {
					cm.Ranges = append(cm.Ranges, zoekt.Range{
						// We only provide LineNumber as that's all that's
						// relevant.
						Start: zoekt.Location{LineNumber: uint32(lineNumber + (2 * i) + 1)},
						End:   zoekt.Location{LineNumber: uint32(lineNumber + (2 * i) + 2)},
					})
					cm.Content = append(cm.Content, []byte(fmt.Sprintf("range%dStart\nrange%dEnd\n", i, i))...)
				}
				// 1 line of context. Content in zoekt notably just does not
				// contain a trailing newline.
				cm.Content = append(cm.Content, []byte("context")...)

				// Next Chunk starts two lines past the number of lines we just
				// added.
				return cm, lineNumber + (2 * numRanges) + 4
			}

			res := zoekt.SearchResult{}
			for _, file := range tc.in {
				fm := zoekt.FileMatch{}
				lineNumber := 0
				for _, numRanges := range file {
					var cm zoekt.ChunkMatch
					cm, lineNumber = generateChunkMatch(numRanges, lineNumber)
					fm.ChunkMatches = append(fm.ChunkMatches, cm)
				}
				res.Files = append(res.Files, fm)
			}

			res.Files = SortAndTruncateFiles(res.Files, &zoekt.SearchOptions{
				MaxMatchDisplayCount: tc.limit,
				ChunkMatches:         true,
			})

			var got [][]int
			for _, fm := range res.Files {
				var matches []int
				for _, cm := range fm.ChunkMatches {
					if len(cm.Ranges) != len(cm.SymbolInfo) {
						t.Errorf("Expected Ranges and SymbolInfo to be the same size, but got %d and %d", len(cm.Ranges), len(cm.SymbolInfo))
					}

					// Using the logic from generateChunkMatch.
					expectedNewlines := 1 + (len(cm.Ranges) * 2)
					actualNewlines := bytes.Count(cm.Content, []byte("\n"))
					if actualNewlines != expectedNewlines {
						t.Errorf("Expected Content to have %d newlines but got %d", expectedNewlines, actualNewlines)
					}

					matches = append(matches, len(cm.Ranges))
				}
				got = append(got, matches)
			}
			if !cmp.Equal(tc.expected, got) {
				t.Errorf("Expected %v but got %v", tc.expected, got)
			}
		})

		t.Run("LineMatches", func(t *testing.T) {
			res := zoekt.SearchResult{}
			for _, file := range tc.in {
				fm := zoekt.FileMatch{}
				for _, numFragments := range file {
					fm.LineMatches = append(fm.LineMatches, zoekt.LineMatch{LineFragments: make([]zoekt.LineFragmentMatch, numFragments)})
				}
				res.Files = append(res.Files, fm)
			}

			res.Files = SortAndTruncateFiles(res.Files, &zoekt.SearchOptions{
				MaxMatchDisplayCount: tc.limit,
				ChunkMatches:         false,
			})

			var got [][]int
			for _, fm := range res.Files {
				var matches []int
				for _, lm := range fm.LineMatches {
					matches = append(matches, len(lm.LineFragments))
				}
				got = append(got, matches)
			}
			if !cmp.Equal(tc.expected, got) {
				t.Errorf("Expected %v but got %v", tc.expected, got)
			}
		})
	}
}
