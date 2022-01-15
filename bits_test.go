// Copyright 2016 Google Inc. All rights reserved.
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
	"encoding/binary"
	"log"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"testing/quick"

	"github.com/google/go-cmp/cmp"
)

var _ = log.Println

func TestNgram(t *testing.T) {
	in := "abc"
	n := stringToNGram(in)
	if n.String() != "abc" {
		t.Errorf("got %q, want %q", n, "abc")
	}

	f := func(b ngramRunes) bool {
		n := runesToNGram(b)
		got := ngramRunes(ngramToRunes(n))
		if !reflect.DeepEqual(b, got) {
			t.Log(cmp.Diff(b, got))
			return false
		}
		return true
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

type ngramRunes [ngramSize]rune

func (ngramRunes) Generate(rand *rand.Rand, size int) reflect.Value {
	// Same implementation used by testing/quick to generate strings. But we
	// force it to ngramSize runes.
	var b ngramRunes
	for i := range b {
		b[i] = rune(rand.Intn(0x10ffff))
	}
	return reflect.ValueOf(b)
}

func TestDocSection(t *testing.T) {
	in := []DocumentSection{{1, 2}, {3, 4}}
	serialized := marshalDocSections(in)
	roundtrip := unmarshalDocSections(serialized, nil)
	if !reflect.DeepEqual(in, roundtrip) {
		t.Errorf("got %v, want %v", roundtrip, in)
	}
}

func TestGenerateCaseNgrams(t *testing.T) {
	ng := stringToNGram("aB1")
	gotNG := generateCaseNgrams(ng)

	got := map[string]bool{}
	for _, n := range gotNG {
		got[string(ngramToBytes(n))] = true
	}

	want := map[string]bool{
		"aB1": true,
		"AB1": true,
		"ab1": true,
		"Ab1": true,
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNextFileIndex(t *testing.T) {
	for _, tc := range []struct {
		off, curFile uint32
		ends         []uint32
		want         uint32
	}{
		{maxUInt32, 0, []uint32{34}, 1},
		{9, 0, []uint32{34}, 0},
		{450, 0, []uint32{100, 200, 300, 400, 500, 600}, 4},
	} {
		got := nextFileIndex(tc.off, tc.curFile, tc.ends)
		if got != tc.want {
			t.Errorf("%v: got %d, want %d", tc, got, tc.want)
		}
	}
}

func TestSizedDeltas(t *testing.T) {
	encode := func(nums []uint32) []byte {
		return toSizedDeltas(nums)
	}
	decode := func(data []byte) []uint32 {
		if len(data) == 0 {
			return nil
		}
		return fromSizedDeltas(data, nil)
	}
	testIncreasingIntCoder(t, encode, decode)
}

func TestFromDeltas(t *testing.T) {
	decode := func(data []byte) []uint32 {
		if len(data) == 0 {
			return nil
		}
		return fromDeltas(data, nil)
	}
	testIncreasingIntCoder(t, toDeltas, decode)
}

func TestCompressedPostingIterator(t *testing.T) {
	decode := func(data []byte) []uint32 {
		if len(data) == 0 {
			return nil
		}

		var nums []uint32
		i := newCompressedPostingIterator(data, stringToNGram("abc"))
		for i.first() != maxUInt32 {
			nums = append(nums, i.first())
			i.next(i.first())
		}
		return nums
	}
	testIncreasingIntCoder(t, toDeltas, decode)
}

func toDeltas(offsets []uint32) []byte {
	var enc [8]byte

	deltas := make([]byte, 0, len(offsets)*2)

	var last uint32
	for _, p := range offsets {
		delta := p - last
		last = p

		m := binary.PutUvarint(enc[:], uint64(delta))
		deltas = append(deltas, enc[:m]...)
	}
	return deltas
}

func testIncreasingIntCoder(t *testing.T, encode func([]uint32) []byte, decode func([]byte) []uint32) {
	f := func(nums []uint32) bool {
		nums = sortedUnique(nums)
		b := encode(nums)
		got := decode(b)
		if len(nums) == len(got) && len(nums) == 0 {
			return true
		}
		if !reflect.DeepEqual(got, nums) {
			t.Log(cmp.Diff(nums, got))
			return false
		}
		return true
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

func sortedUnique(nums []uint32) []uint32 {
	if len(nums) == 0 {
		return nums
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })
	filtered := nums[:1]
	for _, n := range nums[1:] {
		if filtered[len(filtered)-1] != n {
			filtered = append(filtered, n)
		}
	}
	return filtered
}

func TestCondenseRuneOffsets(t *testing.T) {
	for i, tc := range []struct {
		arr  []uint32
		want []runeOffsetCorrection
	}{
		{[]uint32{}, []runeOffsetCorrection{}},
		{[]uint32{0, 100, 200, 300, 400, 500}, []runeOffsetCorrection{}},
		{[]uint32{0, 105, 205, 310, 420}, []runeOffsetCorrection{{100, 105}, {300, 310}, {400, 420}}},
	} {
		got := makeRuneOffsetMap(tc.arr)
		if !reflect.DeepEqual(got, runeOffsetMap(tc.want)) {
			t.Errorf("#%d: got %v, want %v", i, got, tc.want)
		}
		for j, byteOffset := range tc.arr {
			runeOffset := uint32(j * runeOffsetFrequency)
			gotByteOffset, _ := got.lookup(runeOffset)
			if gotByteOffset != byteOffset {
				t.Errorf("#%d: lookup(%v) got %v, want %v", i, runeOffset, gotByteOffset, byteOffset)
			}
		}
	}
}

func TestRuneOffsetLookup(t *testing.T) {
	for i, tc := range []struct {
		r                 uint32
		wantOff, wantLeft uint32
		offsets           []runeOffsetCorrection
	}{
		{0, 0, 0, nil},
		{1234, 1200, 34, nil},
		{5, 0, 5, []runeOffsetCorrection{{100, 105}, {400, 430}}},
		{120, 105, 20, []runeOffsetCorrection{{100, 105}, {400, 430}}},
		{1234, 1230, 34, []runeOffsetCorrection{{100, 105}, {400, 430}}},
	} {
		gotOff, gotLeft := runeOffsetMap(tc.offsets).lookup(tc.r)
		if gotLeft != tc.wantLeft {
			t.Errorf("#%d: got left=%v, want left=%v", i, gotLeft, tc.wantLeft)
		}
		if gotOff != tc.wantOff {
			t.Errorf("#%d: got off=%v, want off=%v", i, gotOff, tc.wantOff)
		}
	}

	m := runeOffsetMap([]runeOffsetCorrection{{100, 105}, {200, 210}, {400, 430}})
	inputs := []uint32{0, 1, 99, 100, 101, 199, 200, 201, 300, 399, 400, 401, 510, 610}
	wanted := []uint32{0, 0, 0, 105, 105, 105, 210, 210, 310, 310, 430, 430, 530, 630}
	for i, v := range inputs {
		got, _ := m.lookup(v)
		if got != wanted[i] {
			t.Errorf("got off=%v, want off=%v for map=%v", got, wanted[i], m)
		}
	}
}
