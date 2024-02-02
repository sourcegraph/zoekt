package zoekt

import (
	"math/rand"
	"reflect"
	"slices"
	"testing"
	"testing/quick"
)

const exampleQuery = "const data: Event = { ...JSON.parse(message.data), type: message.event }"

func genFrequencies(ngramOffs []runeNgramOff, max int) []uint32 {
	seen := map[ngram]uint32{}
	var frequencies []uint32
	for _, n := range ngramOffs {
		freq, ok := seen[n.ngram]
		if !ok {
			freq = uint32(rand.Intn(max))
			seen[n.ngram] = freq
		}
		frequencies = append(frequencies, freq)
	}
	return frequencies
}

func BenchmarkMinFrequencyNgramOffsets(b *testing.B) {
	ngramOffs := splitNGrams([]byte(exampleQuery))
	slices.SortFunc(ngramOffs, runeNgramOff.Compare)
	frequencies := genFrequencies(ngramOffs, 100)
	for i := 0; i < b.N; i++ {
		x0, x1 := minFrequencyNgramOffsets(ngramOffs, frequencies)
		if x0 == x1 {
			b.Fatal("should not be the same")
		}
	}
}

func TestMinFrequencyNgramOffsets(t *testing.T) {
	// Our implementation has ill-defined tie breaks when the 2nd smallest
	// frequency can be tied with others. Fixing that would make the CPU perf
	// worse, so what we do instead is just validate that what we get back is
	// acceptable.
	if err := quick.Check(func(s string, maxFreq uint16) bool {
		ngramOffs := splitNGrams([]byte(s))
		if len(ngramOffs) == 0 {
			return true
		}

		slices.SortFunc(ngramOffs, runeNgramOff.Compare)
		frequencies := genFrequencies(ngramOffs, int(maxFreq))
		x0, x1 := minFrequencyNgramOffsets(ngramOffs, frequencies)

		if x0.index > x1.index {
			t.Log("x0 should be before x1")
			return false
		}

		if len(ngramOffs) <= 1 {
			return true
		}

		// Now we just assert that we found two items with the smallest
		// frequencies.
		idx0 := slices.IndexFunc(ngramOffs, func(a runeNgramOff) bool { return a == x0 })
		idx1 := slices.IndexFunc(ngramOffs, func(a runeNgramOff) bool { return a == x1 })
		start := []uint32{frequencies[idx0], frequencies[idx1]}
		slices.Sort(start)
		slices.Sort(frequencies)
		return reflect.DeepEqual(start, frequencies[:2])
	}, nil); err != nil {
		t.Fatal(err)
	}
}
