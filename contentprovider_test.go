package zoekt

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func getNewlines(data []byte) newlines {
	var locs []uint32
	for i, c := range data {
		if c == '\n' {
			locs = append(locs, uint32(i))
		}
	}
	return newlines{
		locs:     locs,
		fileSize: uint32(len(data)),
	}
}

func TestGetLines(t *testing.T) {
	contents := [][]byte{
		[]byte("one\ntwo\nthree\nfour"),
		[]byte("one\ntwo\nthree\nfour\n"),
		[]byte("one"),
		[]byte(""),
	}

	for _, content := range contents {
		t.Run("", func(t *testing.T) {
			newLines := getNewlines(content)
			lines := bytes.Split(content, []byte{'\n'}) // TODO does split group consecutive sep?
			wantGetLines := func(low, high int) []byte {
				low--
				high--
				if low < 0 {
					low = 0
				}
				if low >= len(lines) {
					return nil
				}
				if high <= 0 {
					return nil
				}
				if high > len(lines) {
					high = len(lines)
				}
				return bytes.Join(lines[low:high], []byte{'\n'})
			}

			for low := -1; low <= len(lines)+2; low++ {
				for high := low; high <= len(lines)+2; high++ {
					want := wantGetLines(low, high)
					got := newLines.getLines(content, low, high)
					if d := cmp.Diff(string(want), string(got)); d != "" {
						t.Fatal(d)
					}
				}
			}
		})
	}
}

func TestAtOffset(t *testing.T) {
	cases := []struct {
		data       []byte
		offset     uint32
		lineNumber int
		lineStart  int
		lineEnd    int
	}{{
		data:       []byte("0.2.4.\n7.9.11.\n"),
		offset:     0,
		lineNumber: 1, lineStart: 0, lineEnd: 6,
	}, {
		data:       []byte("0.2.4.\n7.9.11.\n"),
		offset:     6,
		lineNumber: 1, lineStart: 0, lineEnd: 6,
	}, {
		data:       []byte("0.2.4.\n7.9.11.\n"),
		offset:     2,
		lineNumber: 1, lineStart: 0, lineEnd: 6,
	}, {
		data:       []byte("0.2.4.\n7.9.11.\n"),
		offset:     2,
		lineNumber: 1, lineStart: 0, lineEnd: 6,
	}, {
		data:       []byte("0.2.4.\n7.9.11.\n"),
		offset:     7,
		lineNumber: 2, lineStart: 7, lineEnd: 14,
	}, {
		data:       []byte("0.2.4.\n7.9.11.\n"),
		offset:     11,
		lineNumber: 2, lineStart: 7, lineEnd: 14,
	}, {
		data:       []byte("0.2.4.\n7.9.11.\n"),
		offset:     15,
		lineNumber: 3, lineStart: 15, lineEnd: 15,
	}, {
		data:       []byte("0.2.4.\n7.9.11."),
		offset:     7,
		lineNumber: 2, lineStart: 7, lineEnd: 14,
	}, {
		data:       []byte("\n\n"),
		offset:     0,
		lineNumber: 1, lineStart: 0, lineEnd: 0,
	}, {
		data:       []byte("\n\n"),
		offset:     1,
		lineNumber: 2, lineStart: 1, lineEnd: 1,
	}, {
		data:       []byte("\n\n"),
		offset:     3,
		lineNumber: 3, lineStart: 2, lineEnd: 2,
	}, {
		data:       []byte("line with no newlines"),
		offset:     3,
		lineNumber: 1, lineStart: 0, lineEnd: 21,
	}}

	for _, tt := range cases {
		t.Run("", func(t *testing.T) {
			nls := getNewlines(tt.data)
			gotLineNumber, gotLineStart, gotLineEnd := nls.atOffset(tt.offset)
			if gotLineNumber != tt.lineNumber {
				t.Fatalf("expected line number %d, got %d", tt.lineNumber, gotLineNumber)
			}
			if gotLineStart != tt.lineStart {
				t.Fatalf("expected line start %d, got %d", tt.lineStart, gotLineStart)
			}
			if gotLineEnd != tt.lineEnd {
				t.Fatalf("expected line end %d, got %d", tt.lineEnd, gotLineEnd)
			}
		})
	}
}

func TestLineBounds(t *testing.T) {
	cases := []struct {
		data       []byte
		lineNumber int
		start      uint32
		end        uint32
	}{{
		data:       []byte("0.2.4.\n7.9.11.\n"),
		lineNumber: 1,
		start:      0, end: 6,
	}, {
		data:       []byte("0.2.4.\n7.9.11.\n"),
		lineNumber: 2,
		start:      7, end: 14,
	}, {
		data:       []byte("0.2.4.\n7.9.11.\n"),
		lineNumber: 0,
		start:      0, end: 0,
	}, {
		data:       []byte("0.2.4.\n7.9.11.\n"),
		lineNumber: -1,
		start:      0, end: 0,
	}, {
		data:       []byte("0.2.4.\n7.9.11.\n"),
		lineNumber: 202002,
		start:      15, end: 15,
	}, {
		data:       []byte("\n\n"),
		lineNumber: 1,
		start:      0, end: 0,
	}, {
		data:       []byte("\n\n"),
		lineNumber: 2,
		start:      1, end: 1,
	}, {
		data:       []byte("\n\n"),
		lineNumber: 3,
		start:      2, end: 2,
	}}

	for _, tt := range cases {
		t.Run("", func(t *testing.T) {
			nls := getNewlines(tt.data)
			gotStart, gotEnd := nls.lineBounds(tt.lineNumber)
			if gotStart != tt.start {
				t.Fatalf("expected line start %d, got %d", tt.start, gotStart)
			}
			if gotEnd != tt.end {
				t.Fatalf("expected line end %d, got %d", tt.end, gotEnd)
			}
		})
	}
}

func TestChunkMatches(t *testing.T) {
	content := []byte(`0.2.4.6.8.10.
13.16.19.22.
26.29.32.35.
39.42.45.48.
52.55.58.61.
65.68.71.74.
78.81.84.87.
`)
	match_0_2 := &candidateMatch{byteOffset: 0, byteMatchSz: 2}
	match_6_10 := &candidateMatch{byteOffset: 6, byteMatchSz: 4}
	match_10_16 := &candidateMatch{byteOffset: 10, byteMatchSz: 6}
	match_19_42 := &candidateMatch{byteOffset: 19, byteMatchSz: 23}
	match_45_48 := &candidateMatch{byteOffset: 45, byteMatchSz: 3}
	match_71_72 := &candidateMatch{byteOffset: 71, byteMatchSz: 1}

	cases := []struct {
		candidateMatches []*candidateMatch
		numContextLines  int
		want             []candidateChunk
	}{{
		candidateMatches: []*candidateMatch{match_0_2},
		numContextLines:  0,
		want: []candidateChunk{{
			firstLine:  1,
			minOffset:  0,
			lastLine:   1,
			maxOffset:  2,
			candidates: []*candidateMatch{match_0_2},
		}},
	}, {
		candidateMatches: []*candidateMatch{match_0_2},
		numContextLines:  5,
		want: []candidateChunk{{
			firstLine:  1,
			minOffset:  0,
			lastLine:   1,
			maxOffset:  2,
			candidates: []*candidateMatch{match_0_2},
		}},
	}, {
		candidateMatches: []*candidateMatch{match_0_2, match_6_10},
		numContextLines:  0,
		want: []candidateChunk{{
			firstLine:  1,
			minOffset:  0,
			lastLine:   1,
			maxOffset:  10,
			candidates: []*candidateMatch{match_0_2, match_6_10},
		}},
	}, {
		candidateMatches: []*candidateMatch{match_0_2, match_10_16},
		numContextLines:  0,
		want: []candidateChunk{{
			firstLine:  1,
			minOffset:  0,
			lastLine:   2,
			maxOffset:  16,
			candidates: []*candidateMatch{match_0_2, match_10_16},
		}},
	}, {
		candidateMatches: []*candidateMatch{match_0_2, match_19_42},
		numContextLines:  0,
		want: []candidateChunk{{
			firstLine:  1,
			minOffset:  0,
			lastLine:   1,
			maxOffset:  2,
			candidates: []*candidateMatch{match_0_2},
		}, {
			firstLine:  2,
			minOffset:  19,
			lastLine:   4,
			maxOffset:  42,
			candidates: []*candidateMatch{match_19_42},
		}},
	}, {
		candidateMatches: []*candidateMatch{match_0_2, match_19_42},
		numContextLines:  1,
		want: []candidateChunk{{
			firstLine:  1,
			minOffset:  0,
			lastLine:   4,
			maxOffset:  42,
			candidates: []*candidateMatch{match_0_2, match_19_42},
		}},
	}, {
		candidateMatches: []*candidateMatch{
			match_0_2, match_19_42, match_45_48, match_71_72,
		},
		numContextLines: 0,
		want: []candidateChunk{{
			firstLine:  1,
			minOffset:  0,
			lastLine:   1,
			maxOffset:  2,
			candidates: []*candidateMatch{match_0_2},
		}, {
			firstLine:  2,
			minOffset:  19,
			lastLine:   4,
			maxOffset:  48,
			candidates: []*candidateMatch{match_19_42, match_45_48},
		}, {
			firstLine:  6,
			minOffset:  71,
			lastLine:   6,
			maxOffset:  72,
			candidates: []*candidateMatch{match_71_72},
		}},
	}, {
		candidateMatches: []*candidateMatch{
			match_0_2, match_19_42, match_45_48, match_71_72,
		},
		numContextLines: 100,
		want: []candidateChunk{{
			firstLine:  1,
			minOffset:  0,
			lastLine:   6,
			maxOffset:  72,
			candidates: []*candidateMatch{match_0_2, match_19_42, match_45_48, match_71_72},
		}},
	}}

	newlines := getNewlines(content)
	for _, tt := range cases {
		t.Run("", func(t *testing.T) {
			got := chunkCandidates(tt.candidateMatches, newlines, tt.numContextLines)
			if diff := cmp.Diff(fmt.Sprintf("%#v\n", tt.want), fmt.Sprintf("%#v\n", got)); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestSortFiles(t *testing.T) {
	in := []FileMatch{
		{FileName: "d1", Score: 2, Ranks: []float64{0.75}},
		{FileName: "d2", Score: 4, Ranks: []float64{0.25}},
		{FileName: "d3", Score: 3, Ranks: []float64{1.0}},
		{FileName: "d4", Score: 1, Ranks: []float64{0.5}},
	}

	// Document  RRF(Score) RFF(Ranks) SUM                  Rank
	// d3        1/(60+1)   1/(60+0)   0,0330601092896175   0
	// d2        1/(60+0)   1/(60+3)   0,0325396825396826   1
	// d1        1/(60+2)   1/(60+1)   0,0325224748810153   2
	// d4        1/(60+3)   1/(60+2)   0,0320020481310804   3

	SortFiles(in, true)

	wantOrder := []string{"d3", "d2", "d1", "d4"}

	var haveOrder = []string{}
	for _, f := range in {
		haveOrder = append(haveOrder, f.FileName)
	}

	if d := cmp.Diff(wantOrder, haveOrder); d != "" {
		t.Fatalf("-want, +got\n%s\n", d)
	}
}
