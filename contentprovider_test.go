package zoekt

import (
	"bytes"
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
