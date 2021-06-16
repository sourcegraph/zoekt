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
	"sort"
)

type topOffset struct {
	top, off uint32
}

// arrayNgramOffset splits ngrams into two 32-bit parts and uses binary search
// to satisfy requests. A three-level trie (over the runes of an ngram) uses 20%
// more memory than this simple two-level split.
type arrayNgramOffset struct {
	// tops specify where the bottom halves of ngrams with the 32-bit top half begin.
	// The offset of the next value is used to find where the bottom section ends.
	tops []topOffset

	// bots are bottom halves of an ngram, referenced by tops
	bots []uint32

	// offsets is values from simpleSection.off, simpleSection.sz is computed by subtracting
	// adjacent offsets.
	offsets []uint32
}

func makeArrayNgramOffset(ngrams []ngram, offsets []uint32) arrayNgramOffset {
	arr := arrayNgramOffset{
		bots:    make([]uint32, 0, len(ngrams)),
		offsets: make([]uint32, len(offsets)),
	}
	copy(arr.offsets, offsets) // copy to ensure offsets is minimally sized

	lastTop := uint32(0xffffffff)
	lastStart := uint32(0)
	for i, v := range ngrams {
		curTop := uint32(v >> 32)
		if curTop != lastTop {
			if lastTop != 0xffffffff {
				arr.tops = append(arr.tops, topOffset{lastTop, lastStart})
			}
			lastTop = curTop
			lastStart = uint32(i)
		}
		arr.bots = append(arr.bots, uint32(v))
	}
	// add a sentinel value to make it simple to compute sizes
	arr.tops = append(arr.tops, topOffset{lastTop, lastStart}, topOffset{0xffffffff, uint32(len(arr.bots))})

	// shrink arr.tops to minimal size
	tops := make([]topOffset, len(arr.tops))
	copy(tops, arr.tops)
	arr.tops = tops

	return arr
}

func (a *arrayNgramOffset) Get(gram ngram) simpleSection {
	if a.tops == nil {
		return simpleSection{}
	}

	top, bot := uint32(uint64(gram)>>32), uint32(gram)

	topIdx := sort.Search(len(a.tops)-1, func(i int) bool { return a.tops[i].top >= top })
	if topIdx == len(a.tops)-1 || a.tops[topIdx].top != top {
		return simpleSection{}
	}

	botsSec := a.bots[a.tops[topIdx].off:a.tops[topIdx+1].off]
	botIdx := sort.Search(len(botsSec), func(i int) bool { return botsSec[i] >= bot })
	if botIdx == len(botsSec) || botsSec[botIdx] != bot {
		return simpleSection{}
	}
	idx := botIdx + int(a.tops[topIdx].off)
	return simpleSection{
		off: a.offsets[idx],
		sz:  a.offsets[idx+1] - a.offsets[idx],
	}
}

func (a *arrayNgramOffset) DumpMap() map[ngram]simpleSection {
	m := make(map[ngram]simpleSection, len(a.offsets)-1)
	for i := 0; i < len(a.tops)-1; i++ {
		top, botStart, botEnd := a.tops[i].top, a.tops[i].off, a.tops[i+1].off
		botSec := a.bots[botStart:botEnd]
		for j, bot := range botSec {
			idx := int(botStart) + j
			m[ngram(uint64(top)<<32|uint64(bot))] = simpleSection{
				off: a.offsets[idx],
				sz:  a.offsets[idx+1] - a.offsets[idx],
			}
		}
	}
	return m
}

func (a *arrayNgramOffset) SizeBytes() int {
	return 8*len(a.tops) + 4*len(a.bots) + 4*len(a.offsets)
}
