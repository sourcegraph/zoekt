package zoekt

import (
	"bytes"
	"fmt"
	"testing"
	"testing/quick"
	"unicode/utf8"

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
			lines := bytes.SplitAfter(content, []byte{'\n'})
			if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
				// A trailing newline does not delimit an empty line at the end of a file
				lines = lines[:len(lines)-1]
			}
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
				return bytes.Join(lines[low:high], nil)
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
		lineStart  uint32
		lineEnd    uint32
	}{{
		data:       []byte("0.2.4.\n7.9.11.\n"),
		offset:     0,
		lineNumber: 1, lineStart: 0, lineEnd: 7,
	}, {
		data:       []byte("0.2.4.\n7.9.11.\n"),
		offset:     6,
		lineNumber: 1, lineStart: 0, lineEnd: 7,
	}, {
		data:       []byte("0.2.4.\n7.9.11.\n"),
		offset:     2,
		lineNumber: 1, lineStart: 0, lineEnd: 7,
	}, {
		data:       []byte("0.2.4.\n7.9.11.\n"),
		offset:     2,
		lineNumber: 1, lineStart: 0, lineEnd: 7,
	}, {
		data:       []byte("0.2.4.\n7.9.11.\n"),
		offset:     7,
		lineNumber: 2, lineStart: 7, lineEnd: 15,
	}, {
		data:       []byte("0.2.4.\n7.9.11.\n"),
		offset:     11,
		lineNumber: 2, lineStart: 7, lineEnd: 15,
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
		lineNumber: 1, lineStart: 0, lineEnd: 1,
	}, {
		data:       []byte("\n\n"),
		offset:     1,
		lineNumber: 2, lineStart: 1, lineEnd: 2,
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
			gotLineNumber := nls.atOffset(tt.offset)
			if gotLineNumber != tt.lineNumber {
				t.Fatalf("expected line number %d, got %d", tt.lineNumber, gotLineNumber)
			}
			if gotLineStart := nls.lineStart(gotLineNumber); gotLineStart != tt.lineStart {
				t.Fatalf("expected line start %d, got %d", tt.lineStart, gotLineStart)
			}
			if gotLineEnd := nls.lineEnd(gotLineNumber, false); gotLineEnd != tt.lineEnd {
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
		start:      0, end: 7,
	}, {
		data:       []byte("0.2.4.\n7.9.11.\n"),
		lineNumber: 2,
		start:      7, end: 15,
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
		start:      0, end: 1,
	}, {
		data:       []byte("\n\n"),
		lineNumber: 2,
		start:      1, end: 2,
	}, {
		data:       []byte("\n\n"),
		lineNumber: 3,
		start:      2, end: 2,
	}}

	for _, tt := range cases {
		t.Run("", func(t *testing.T) {
			nls := getNewlines(tt.data)
			gotStart := nls.lineStart(tt.lineNumber)
			if gotStart != tt.start {
				t.Fatalf("expected line start %d, got %d", tt.start, gotStart)
			}
			gotEnd := nls.lineEnd(tt.lineNumber, false)
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

func BenchmarkColumnHelper(b *testing.B) {
	// We simulate looking up columns of evenly spaced matches
	const matches = 10_000
	const match = "match"
	const space = "         "
	const dist = uint32(len(match) + len(space))
	data := bytes.Repeat([]byte(match+space), matches)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		columnHelper := columnHelper{data: data}

		lineOffset := 0
		offset := uint32(0)
		for offset < uint32(len(data)) {
			col := columnHelper.get(lineOffset, offset)
			if col != offset+1 {
				b.Fatal("column is not offset even though data is ASCII")
			}
			offset += dist
		}
	}
}

func TestColumnHelper(t *testing.T) {
	f := func(line0, line1 string) bool {
		data := []byte(line0 + line1)
		lineOffset := len(line0)

		columnHelper := columnHelper{data: data}

		// We check every second rune returns the correct answer
		offset := lineOffset
		column := 1
		for offset < len(data) {
			if column%2 == 0 {
				got := columnHelper.get(lineOffset, uint32(offset))
				if got != uint32(column) {
					return false
				}
			}
			_, size := utf8.DecodeRune(data[offset:])
			offset += size
			column++
		}

		return true
	}

	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}

	// Corner cases

	// empty data, shouldn't happen but just in case it slips through
	ch := columnHelper{data: nil}
	if got := ch.get(0, 0); got != 1 {
		t.Fatal("empty data didn't return 1", got)
	}

	// Repeating a call to get should return the same value
	// empty data, shouldn't happen but just in case it slips through
	ch = columnHelper{data: []byte("hello\nworld")}
	if got := ch.get(6, 8); got != 3 {
		t.Fatal("unexpected value for third column on second line", got)
	}
	if got := ch.get(6, 8); got != 3 {
		t.Fatal("unexpected value for repeated call for third column on second line", got)
	}

	// Now make sure if we go backwards we do not incorrectly use the cache
	if got := ch.get(6, 6); got != 1 {
		t.Fatal("unexpected value for backwards call for first column on second line", got)
	}
}

func TestFindMaxOverlappingSection(t *testing.T) {
	secs := []DocumentSection{
		{Start: 0, End: 5},
		{Start: 8, End: 19},
		{Start: 22, End: 26},
	}
	// 012345678901234567890123456
	// [....[
	//         [..........[
	//                       [...[

	testcases := []struct {
		name        string
		off         uint32
		sz          uint32
		wantSecIdx  uint32
		wantOverlap bool
	}{
		{off: 0, sz: 1, wantSecIdx: 0, wantOverlap: true},
		{off: 0, sz: 5, wantSecIdx: 0, wantOverlap: true},
		{off: 2, sz: 5, wantSecIdx: 0, wantOverlap: true},
		{off: 2, sz: 50, wantSecIdx: 1, wantOverlap: true},
		{off: 4, sz: 10, wantSecIdx: 1, wantOverlap: true},
		{off: 5, sz: 15, wantSecIdx: 1, wantOverlap: true},
		{off: 18, sz: 10, wantSecIdx: 2, wantOverlap: true},

		// Prefer full overlap, break ties by preferring the earlier section
		{off: 10, sz: 20, wantSecIdx: 2, wantOverlap: true},
		{off: 0, sz: 100, wantSecIdx: 0, wantOverlap: true},
		{off: 8, sz: 100, wantSecIdx: 1, wantOverlap: true},
		{off: 0, sz: 10, wantSecIdx: 0, wantOverlap: true},
		{off: 16, sz: 10, wantSecIdx: 2, wantOverlap: true},

		// No overlap
		{off: 5, sz: 2, wantOverlap: false},
		{off: 20, sz: 1, wantOverlap: false},
		{off: 99, sz: 1, wantOverlap: false},
		{off: 0, sz: 0, wantOverlap: false},
	}

	for _, tt := range testcases {
		t.Run(fmt.Sprintf("off=%d size=%d", tt.off, tt.sz), func(t *testing.T) {
			got, haveOverlap := findMaxOverlappingSection(secs, tt.off, tt.sz)
			if haveOverlap != tt.wantOverlap {
				t.Fatalf("expected overlap %v, got %v", tt.wantOverlap, haveOverlap)
			}
			if got != tt.wantSecIdx {
				t.Fatalf("expected section %d, got %d", tt.wantSecIdx, got)
			}
		})
	}
}
