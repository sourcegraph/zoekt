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
			o += fmt.Sprintf("%x: %d", p.top>>10, p.off)
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
			o += fmt.Sprintf("%x", p)
		}
	}
	o += fmt.Sprintf("}, offsets: %v}", a.offsets)
	return o
}
