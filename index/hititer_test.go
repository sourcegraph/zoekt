// Copyright 2019 Google Inc. All rights reserved.
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

package index

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"

	"slices"

	"github.com/google/go-cmp/cmp"
	"github.com/sourcegraph/zoekt"
)

func TestCompressedPostingIterator_limit(t *testing.T) {
	f := func(nums, limits []uint32) bool {
		if len(nums) == 0 || len(limits) == 0 {
			return true
		}

		nums = sortedUnique(nums)
		slices.Sort(limits)

		want := doHitIterator(&inMemoryIterator{postings: nums}, limits)

		it := newCompressedPostingIterator(toDeltas(nums), stringToNGram("abc"))
		got := doHitIterator(it, limits)
		if !reflect.DeepEqual(want, got) {
			t.Log(cmp.Diff(want, got))
			return false
		}
		return true
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

func doHitIterator(it hitIterator, limits []uint32) []uint32 {
	var nums []uint32
	for _, limit := range limits {
		it.next(limit)
		nums = append(nums, it.first())
	}
	return nums
}

func BenchmarkCompressedPostingIterator(b *testing.B) {
	cases := []struct{ size, limitSize int }{
		{100, 50},
		{10000, 100},
		{10000, 1000},
		{10000, 10000},
		{100000, 100},
		{100000, 1000},
		{100000, 10000},
		{100000, 100000},
	}
	for _, tt := range cases {
		b.Run(fmt.Sprintf("%d_%d", tt.size, tt.limitSize), func(b *testing.B) {
			benchmarkCompressedPostingIterator(b, tt.size, tt.limitSize)
		})
	}
}

func benchmarkCompressedPostingIterator(b *testing.B, size, limitsSize int) {
	nums := genUints32(size)
	limits := genUints32(limitsSize)

	nums = sortedUnique(nums)
	slices.Sort(limits)

	ng := stringToNGram("abc")
	deltas := toDeltas(nums)

	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		it := newCompressedPostingIterator(deltas, ng)
		for _, limit := range limits {
			it.next(limit)
			_ = it.first()
		}
		var s zoekt.Stats
		it.updateStats(&s)
		b.SetBytes(s.IndexBytesLoaded)
	}
}

func genUints32(size int) []uint32 {
	// Deterministic for benchmarks
	r := rand.New(rand.NewSource(int64(size)))
	nums := make([]uint32, size)
	for i := range nums {
		nums[i] = r.Uint32()
	}
	return nums
}
