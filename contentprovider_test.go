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
	data := []byte(`one
two
three
four`)

	newLines := getNewlines(data)
	lines := bytes.Split(data, []byte{'\n'}) // TODO does split group consecutive sep?
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
			got := newLines.getLines(data, low, high)
			if d := cmp.Diff(string(want), string(got)); d != "" {
				t.Fatal(d)
			}
		}
	}
}
