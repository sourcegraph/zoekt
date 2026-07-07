package index

import (
	"testing"
)

func TestCaseFoldingEqualsRunes(t *testing.T) {
	for _, tc := range []struct {
		lower     string
		mixed     string
		wantMatch bool
		wantSz    int
	}{
		{"", "", true, 0},
		{"abc", "abc", true, 3},
		{"abc", "ABC", true, 3},
		{"abc", "AbC", true, 3},
		{"abc", "abcd", true, 3},
		{"abc", "AB", false, 2},
		{"abc", "xyz", false, 0},
		// Unicode (Kelvin symbol U+212A 'K' -> lowercase 'k')
		{"k", "\u212a", true, 3}, // Kelvin symbol UTF-8 size is 3 bytes
		{"k", "\u212ad", true, 3},
		// Non-ASCII
		{"äbč", "ÄBČ", true, 5}, // 'ä' (2 bytes), 'b' (1 byte), 'č' (2 bytes)
		{"äbč", "ÄBX", false, 0},
	} {
		sz, ok := caseFoldingEqualsRunes([]byte(tc.lower), []byte(tc.mixed))
		if ok != tc.wantMatch || sz != tc.wantSz {
			t.Errorf("caseFoldingEqualsRunes(%q, %q): got (%d, %t), want (%d, %t)",
				tc.lower, tc.mixed, sz, ok, tc.wantSz, tc.wantMatch)
		}
	}
}

func BenchmarkCaseFoldingEqualsRunes(b *testing.B) {
	// 1. ASCII matching
	asciiLower := []byte("the quick brown fox jumps over the lazy dog")
	asciiMixed := []byte("The Quick Brown Fox Jumps Over The Lazy Dog")

	// 2. Non-ASCII matching (Unicode)
	unicodeLower := []byte("the quïck bröwn fôx jûmps ovër the lâzy dôg")
	unicodeMixed := []byte("The QuÏck BrÖwn FÔx JÛmps OvËr The LÂzy DÔg")

	b.Run("ASCII", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			sz, ok := caseFoldingEqualsRunes(asciiLower, asciiMixed)
			if !ok || sz != len(asciiMixed) {
				b.Fatalf("bad match: %d, %t", sz, ok)
			}
		}
	})

	b.Run("Unicode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			sz, ok := caseFoldingEqualsRunes(unicodeLower, unicodeMixed)
			if !ok || sz != len(unicodeMixed) {
				b.Fatalf("bad match: %d, %t", sz, ok)
			}
		}
	})
}
