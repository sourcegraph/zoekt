// Copyright 2021 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package zoekt

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

func TestMakeArrayNgramOffset(t *testing.T) {
	for n, tc := range []struct {
		ngrams  []string
		offsets []uint32
	}{
		{nil, nil},
		{[]string{"ant", "any", "awl", "big", "bin", "bit", "can", "con"}, []uint32{0, 2, 5, 8, 10, 14, 18, 25, 30}},
	} {
		ngrams := []ngram{}
		for _, s := range tc.ngrams {
			ngrams = append(ngrams, stringToNGram(s))
		}
		m := makeArrayNgramOffset(ngrams, tc.offsets)
		t.Logf("makeNgramOffset(%v, %v) => %s", tc.ngrams, tc.offsets, &m)
		failn := stringToNGram("foo")
		if getFail := m.Get(failn); getFail != (simpleSection{}) {
			t.Errorf("#%d: Get(%q) got %v, want zero", n, failn, getFail)
		}
		for i := 0; i < len(tc.offsets)-1; i++ {
			want := simpleSection{tc.offsets[i], tc.offsets[i+1] - tc.offsets[i]}
			got := m.Get(ngrams[i])
			if want != got {
				t.Errorf("#%d.%d: Get(%q) got %v, want %v", n, i, tc.ngrams[i], got, want)
			}
			failn := ngram(uint64(ngrams[i] - 1))
			if getFail := m.Get(failn); getFail != (simpleSection{}) {
				t.Errorf("#%d.%d: Get(%q) got %v, want zero", n, i, failn, getFail)
			}
			failn = ngram(uint64(ngrams[i] + 1))
			if getFail := m.Get(failn); getFail != (simpleSection{}) {
				t.Errorf("#%d.%d: Get(%q) got %v, want zero", n, i, failn, getFail)
			}
		}
	}
}

func TestMakeCombinedNgramOffset(t *testing.T) {
	// The ascii / unicode ngram offset splitting is significantly
	// more complicated. Exercise it with a more comprehensive test!
	unicodeProbability := 0.2
	ngramCount := 1000
	ngramMap := map[ngram]bool{}

	rng := rand.New(rand.NewSource(42))

	randRune := func() rune {
		if rng.Float64() < unicodeProbability {
			return rune(0x100 + rand.Intn(0x80)) // Emoji
		}
		return rune('A' + rng.Intn('Z'-'A')) // A letter
	}

	for len(ngramMap) < ngramCount {
		ngramMap[runesToNGram([3]rune{randRune(), randRune(), randRune()})] = true
	}

	ngrams := []ngram{}
	for ng := range ngramMap {
		ngrams = append(ngrams, ng)
	}
	sort.Slice(ngrams, func(i, j int) bool { return ngrams[i] < ngrams[j] })

	offset := uint32(0)
	offsets := []uint32{0}

	for i := 0; i < len(ngrams); i++ {
		// vary
		offset += uint32(ngramAsciiMaxSectionLength/2 + rand.Intn(ngramAsciiMaxSectionLength))
		offsets = append(offsets, offset)
	}

	m := makeCombinedNgramOffset(ngrams, offsets)

	for i, ng := range ngrams {
		want := simpleSection{offsets[i], offsets[i+1] - offsets[i]}
		got := m.Get(ng)
		if want != got {
			t.Errorf("#%d: Get(%q) got %v, want %v", i, ng, got, want)
		}
		failn := ngram(uint64(ng - 1))
		if getFail := m.Get(failn); !ngramMap[failn] && getFail != (simpleSection{}) {
			t.Errorf("#%d: Get(%q) got %v, want zero", i, failn, getFail)
		}
		failn = ngram(uint64(ng + 1))
		if getFail := m.Get(failn); !ngramMap[failn] && getFail != (simpleSection{}) {
			t.Errorf("#%d: Get(%q) got %v, want zero", i, failn, getFail)
		}
	}

	if t.Failed() || true {
		t.Log(ngrams)
		t.Log(offsets)
		t.Log(m)
	}
}

func (a combinedNgramOffset) String() string {
	return fmt.Sprintf("combinedNgramOffset{\n  asc: %s,\n  uni: %s,\n}", a.asc, a.uni)
}

func (a *arrayNgramOffset) String() string {
	o := "arrayNgramOffset{tops:{"
	for i, p := range a.tops {
		if i > 0 {
			o += ", "
		}
		if p.top&1023 == 0 {
			// only one rune is represented here
			o += fmt.Sprintf("%s: %d", string(rune(p.top>>10)), p.off)
		} else {
			o += fmt.Sprintf("0x%x: %d", p.top>>10, p.off)
		}
	}
	o += "}, bots: {"
	for i, p := range a.bots {
		if i > 0 {
			o += ", "
		}
		if p < (256 << 21) {
			// two ascii-ish runes (probably)
			o += fmt.Sprintf("%s%s", string(rune(p>>21)), string(rune(p&runeMask)))
		} else {
			o += fmt.Sprintf("0x%x", p)
		}
	}
	o += fmt.Sprintf("}, offsets: %v}", a.offsets)
	return o
}

func (a *asciiNgramOffset) String() string {
	o := "asciiNgramOffset{entries:{"
	for i, e := range a.entries {
		ng := ngramAsciiPackedToNgram(ngramAscii(uint32(e) >> 11))
		length := e & ngramAsciiMaxSectionLength
		if i > 0 {
			o += ", "
		}
		o += fmt.Sprintf("%s: %d", ng, length)
	}
	o += fmt.Sprintf("}, chunkOffsets: %v}", a.chunkOffsets)
	return o

}
