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

// shrinkUint32Slice copies slices with excess capacity to precisely sized ones
// to avoid wasting memory. It should be used on slices with long static durations.
func shrinkUint32Slice(a []uint32) []uint32 {
	if cap(a)-len(a) < 32 {
		return a
	}
	out := make([]uint32, len(a))
	copy(out, a)
	return out
}

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
		bots: make([]uint32, 0, len(ngrams)),
	}
	arr.offsets = shrinkUint32Slice(offsets)

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

// combinedNgramOffset combines an ascii ngram mapping with a unicode ngram mapping,
// falling back on unicode for unicode ngrams or ascii ngrams with excessive lengths.
type combinedNgramOffset struct {
	asc *asciiNgramOffset
	uni *arrayNgramOffset
}

func makeCombinedNgramOffset(ngrams []ngram, offsets []uint32) combinedNgramOffset {
	// split ngrams & offsets into ascii ngrams and unicode ngrams,
	// since ascii ngrams can be represented much more compactly (21b instead of 63b)

	// allocate these arrays based off of rough measurements of what their typical
	// sizes are-- source code is mostly ASCII with a little bit of Unicode.
	// Allocating 101% of the total number of ngrams gives a little space for the
	// duplicate entries used to mark section ends.
	ngramsAscii := make([]ngramAscii, 0, len(ngrams)*101/100)
	offsetsAscii := make([]uint32, 0, len(ngrams)*101/100)

	ngramsUnicode := make([]ngram, 0, len(ngrams)*11/100)
	offsetsUnicode := make([]uint32, 0, len(ngrams)*11/100)

	for i, ng := range ngrams {
		if ng&ngramAsciiMask == ng { // is ngram ascii-only?
			ngp := ngramAsciiToPacked(ng)
			if i == len(ngrams)-1 || ngrams[i+1]&ngramAsciiMask != ngrams[i+1] {
				// at the end of a section we insert an extra offset with the same ngram,
				// so the size of the segment can be calculated properly
				ngramsAscii = append(ngramsAscii, ngp, ngp)
				offsetsAscii = append(offsetsAscii, offsets[i], offsets[i+1])
			} else {
				ngramsAscii = append(ngramsAscii, ngp)
				offsetsAscii = append(offsetsAscii, offsets[i])
			}
			// note: len(offsets) == len(ngrams) + 1
			if offsets[i+1]-offsets[i] >= ngramAsciiMaxSectionLength {
				// max-length ascii sections can't be represented properly in the ascii mapping,
				// and are duplicated in the normal unicode entries.
				ngramsUnicode = append(ngramsUnicode, ng, ng)
				offsetsUnicode = append(offsetsUnicode, offsets[i], offsets[i+1])
			}
		} else {
			if i == len(ngrams)-1 || ngrams[i+1]&ngramAsciiMask == ngrams[i+1] {
				ngramsUnicode = append(ngramsUnicode, ng, ng)
				offsetsUnicode = append(offsetsUnicode, offsets[i], offsets[i+1])
			} else {
				ngramsUnicode = append(ngramsUnicode, ng)
				offsetsUnicode = append(offsetsUnicode, offsets[i])
			}
		}
	}

	// The last segment always has an extra trailing ngram entry that we don't need, and
	// is only present for spacing and alignment. Trim it.
	if len(ngramsAscii) > 0 {
		ngramsAscii = ngramsAscii[:len(ngramsAscii)-1]
	}
	if len(ngramsUnicode) > 0 {
		ngramsUnicode = ngramsUnicode[:len(ngramsUnicode)-1]
	}

	asc := makeAsciiNgramOffset(ngramsAscii, offsetsAscii)
	uni := makeArrayNgramOffset(ngramsUnicode, offsetsUnicode)

	return combinedNgramOffset{asc, &uni}
}

// Get returns a simpleSection with sz=0 if no entry, otherwise the appropriate
// offset based on the underlying ASCII or Unicode offset index.
func (a combinedNgramOffset) Get(gram ngram) (simpleSection, ngramIndexGetStats) {
	if a.asc == nil {
		return simpleSection{}, ngramIndexGetStats{}
	}

	var sec simpleSection
	if gram&ngramAsciiMask == gram {
		sec = a.asc.Get(gram)
		if sec.sz == ngramAsciiMaxSectionLength {
			// Fallback: this section's length was too long to store in the
			// ASCII map, find it in the Unicode map.
			sec = a.uni.Get(gram)
		}
	} else {
		sec = a.uni.Get(gram)
	}

	// TODO consider populating stats, although we will likely delete non-btree
	// code paths soon.

	return sec, ngramIndexGetStats{}
}

func (a combinedNgramOffset) DumpMap() map[ngram]simpleSection {
	m := a.asc.DumpMap()
	for k, v := range a.uni.DumpMap() {
		m[k] = v
	}
	return m
}

func (a combinedNgramOffset) SizeBytes() int {
	return a.asc.SizeBytes() + a.uni.SizeBytes()
}

const ngramAsciiMask = 127 | 127<<21 | 127<<42

// Ascii mapping packs 3*7b chars and 11 bits of lengths, with this as the set maximum.
// We could save another ~3% of total RAM / 5% of combinedNgramOffset RAM by switching to
// a 40b packing with 19-bit lengths, but the code would be significantly uglier so it doesn't
// seem worth it.
const ngramAsciiMaxSectionLength = (1 << 11) - 1

type ngramAscii uint32

func ngramAsciiToPacked(ng ngram) ngramAscii {
	return ngramAscii(uint32(ng&127) | uint32((ng>>(21-7))&(127<<7)) | uint32((ng>>(42-14))&(127<<14)))
}

func ngramAsciiPackedToNgram(ng ngramAscii) ngram {
	return ngram(ng&127) | ngram(ng&(127<<7))<<(21-7) | ngram(ng&(127<<14))<<(42-14)
}

// asciiNgramOffset stores ascii trigrams packed together with short lengths,
// using offsets for a chunk of entries to limit the number of lengths that must
// be summed to compute a section's offset.
type asciiNgramOffset struct {
	entries      []uint32 // (chara << 25 | charb << 18 | charc << 11 | length)
	chunkOffsets []uint32 // offset for entries[i*asciiNgramOffsetChunkLength]
}

// asciiNgramOffsetChunkLength specifies how many entries share one initial offset.
// It must be a power of 2, and was chosen empirically by measuring RAM usage:
// 8: 4132MB, 16: 4047MB, 32: 4006MB, 64: 3992MB, 128: 3990MB
const asciiNgramOffsetChunkLength = 32

func makeAsciiNgramOffset(ngrams []ngramAscii, offsets []uint32) *asciiNgramOffset {
	ao := &asciiNgramOffset{
		entries:      make([]uint32, 0, len(ngrams)),
		chunkOffsets: make([]uint32, 0, len(ngrams)/asciiNgramOffsetChunkLength),
	}

	for i, ng := range ngrams {
		if len(ao.entries)%asciiNgramOffsetChunkLength == 0 {
			ao.chunkOffsets = append(ao.chunkOffsets, offsets[i])
		}
		length := offsets[i+1] - offsets[i]

		for {
			if length < ngramAsciiMaxSectionLength {
				ao.entries = append(ao.entries, uint32(ng)<<11|length)
				break
			} else {
				// entries with lengths that are too long can't be represented fully in this
				// map, but we repeatedly insert offsets to make the next entry's offset computable
				// by summing the offsets in the preceding entries in the chunk, including
				// this invalid one.
				ao.entries = append(ao.entries, uint32(ng)<<11|ngramAsciiMaxSectionLength)
				length -= ngramAsciiMaxSectionLength
				if len(ao.entries)%asciiNgramOffsetChunkLength == 0 {
					// We reached the end of the chunk, so there's no need to reach the
					// offset for the next entry.
					break
				}
			}
		}
	}

	ao.entries = shrinkUint32Slice(ao.entries)
	ao.chunkOffsets = shrinkUint32Slice(ao.chunkOffsets)

	return ao
}

// Get returns a simpleSection with sz=0 if no entry, or sz=ngramAsciiMaxSectionLength
// if the length of the ngram is too large for this type and it should cascade to the next entry.
func (a *asciiNgramOffset) Get(gram ngram) simpleSection {
	if gram&ngramAsciiMask != gram {
		return simpleSection{}
	}
	g := uint32(ngramAsciiToPacked(gram) << 11)

	idx := sort.Search(len(a.entries), func(i int) bool {
		return a.entries[i] >= g
	})

	if idx == len(a.entries) || a.entries[idx]>>11 != g>>11 {
		return simpleSection{}
	}

	length := a.entries[idx] & ngramAsciiMaxSectionLength
	if length == ngramAsciiMaxSectionLength {
		// this ascii ngram's section length is too large to be represented;
		// repeate the Get() on the unicode map to get the correct result.
		return simpleSection{
			off: 0,
			sz:  ngramAsciiMaxSectionLength,
		}
	}

	chunkNum := idx / asciiNgramOffsetChunkLength
	chunkBase := chunkNum * asciiNgramOffsetChunkLength
	offset := a.chunkOffsets[chunkNum]
	for i := chunkBase; i < idx; i++ {
		offset += a.entries[i] & ngramAsciiMaxSectionLength
	}

	return simpleSection{
		off: offset,
		sz:  length,
	}
}

func (a *asciiNgramOffset) DumpMap() map[ngram]simpleSection {
	m := make(map[ngram]simpleSection, len(a.entries))
	off := uint32(0)
	for i, ent := range a.entries {
		if i%asciiNgramOffsetChunkLength == 0 {
			off = a.chunkOffsets[i/asciiNgramOffsetChunkLength]
		}
		length := ent & ngramAsciiMaxSectionLength
		if length == ngramAsciiMaxSectionLength {
			// This entry is an ascii gram with a section too long
			// to be represented, so skip the entry.
			continue
		}
		m[ngramAsciiPackedToNgram(ngramAscii(ent>>11))] = simpleSection{
			off: off,
			sz:  length,
		}
		off += length
	}
	return m
}

func (a *asciiNgramOffset) SizeBytes() int {
	return 4*len(a.entries) + 4*len(a.chunkOffsets)
}

type ngramIndexGetStats struct {
	// NgramsAccessed is the number of ngrams accessed to lookup gram.
	NgramsAccessed int
}

type ngramIndex interface {
	Get(gram ngram) (simpleSection, ngramIndexGetStats)
	DumpMap() map[ngram]simpleSection
	SizeBytes() int
}

// This is a temporary type to wrap two very different implementations of the
// inverted index for the purpose of feature-flagging. We will remove this after
// we enable the b-tree permanently.
//
// Alternatively we could have adapted/extended the interface "ngramIndex".
// However, adapting the existing implementations and their tests to match the
// access pattern of map[ngram][]byte seems more cumbersome than this makeshift
// wrapper. In the end, both ngramIndex and this wrapper will be replaced by a
// concrete type.
type fileNameNgrams struct {
	m  map[ngram][]byte
	bt btreeIndex
}

func (n fileNameNgrams) GetBlob(ng ngram) ([]byte, error) {
	if n.m != nil {
		return n.m[ng], nil
	}
	sec, statsTODO := n.bt.Get(ng)
	_ = statsTODO
	return n.bt.file.Read(sec.off, sec.sz)
}

func (n fileNameNgrams) Frequency(ng ngram) uint32 {
	if n.m != nil {
		return uint32(len(n.m[ng]))
	}
	sec, statsTODO := n.bt.Get(ng)
	_ = statsTODO
	return sec.sz
}

func (n fileNameNgrams) SizeBytes() int {
	if n.m != nil {
		// these slices reference mmap-ed memory
		return 12 * len(n.m)
	}
	return n.bt.SizeBytes()
}
