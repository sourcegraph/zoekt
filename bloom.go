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

// Bloom implements a simple bloom filter over case-insensitive word fragments,
// with the default hash function providing a blocked bloom filter:
// https://algo2.iti.kit.edu/singler/publications/cacheefficientbloomfilters-wea2007.pdf
//
// Various permutations of hash functions, fragment sizes, and block sizes were
// tested to determine the pareto frontier of false positive rate vs avg bloom filter size.
// FPR = (false positives / (false positives + true negatives))
//
// This determined the hash function in use:
// CRC over word fragments of length 4-8, in a block size of 512 bits, and 3 probes.
//
// In particular:
// * using a crypto hash like siphash provided no benefit, and is slower.
// * having longer word fragments increases false positive rate, and 3-long fragments
//   are handled by the trigram index.
// * a 1% FPR is near the optimal bits-per-precision tradeoff, with 2.5% FPR
//   only reducing bloom filter sizes by 25%.

package zoekt // import "github.com/google/zoekt"

import (
	"bytes"
	"errors"
	"hash/crc32"
	"math"
	"math/bits"
	"reflect"
	"unicode"
	"unicode/utf8"
)

type bloom struct {
	hasher bloomHash
	bits   []uint8
}

type bloomHash func([]byte) []uint32

// Least common multiple of of {1..18}.
// This permits precise resizing for many different factors without
// using excessive RAM during processing. Some shards will saturate
// the bloom filter (have a load factor greater than the target),
// but they tend to be edge cases with a huge number of distinct
// ngrams, so we have to rely on the trigram index iteration to search.
const bloomSizeBase = 12252240

// A smaller base bloom filter size for faster tests. LCM(1..10)
const bloomSizeTest = 2520

// bloomDefaultHash and bloomDefaultLoad were empirically
// determined to achieve 1% FPR with minimal space usage.
var bloomDefaultHash = bloomHasherCRCBlocked64B8K3

const bloomDefaultLoad = 0.42

// Castagnoli CRCs have hardware instructions to compute them.
var crcTab = crc32.MakeTable(crc32.Castagnoli)

func makeBloomFilterEmpty() bloom {
	return bloom{bloomDefaultHash, make([]uint8, bloomSizeBase)}
}

func makeBloomFilterWithHasher(hash bloomHash) bloom {
	return bloom{hash, make([]uint8, bloomSizeBase)}
}

func (b *bloom) Len() int {
	return len(b.bits) * 8
}

func (b *bloom) add(xs []uint32) {
	for _, x := range xs {
		b.bits[int(x/8)%len(b.bits)] |= 1 << (x % 8)
	}
}

// addBytes splits the input into case-insensitive word fragments, hashes them,
// and adds them all to the bloom filter.
func (b *bloom) addBytes(data []byte) {
	b.add(b.hasher(data))
}

// maybeHas returns whether all input hashes are in the bloom filter.
// False positives are possible, but false negatives are impossible.
func (b *bloom) maybeHas(xs []uint32) bool {
	if len(b.bits) == 0 {
		return true
	}
	for _, x := range xs {
		if b.bits[int(x/8)%len(b.bits)]&(1<<(x%8)) == 0 {
			return false
		}
	}
	return true
}

// maybeHasBytes splits the input into case-insensitive word fragments,
// hashes them, and tests if they're all in the bloom filter.
func (b *bloom) maybeHasBytes(xs []byte) bool {
	if b.hasher == nil {
		return true
	}
	return b.maybeHas(b.hasher(xs))
}

func (b *bloom) load() float64 {
	// TODO: this is 4x faster with unsafe 64-bit casting, or
	// constant time if add() tracks the load directly.
	total := 0
	for _, x := range b.bits {
		total += bits.OnesCount8(x)
	}
	return float64(total) / float64(len(b.bits)*8)
}

// shrinkToSize returns a resized bloom filter with a bit density close to target.
// This exploits the fact that a test for a probe x in the bloom filter is actually
// a test for bit x%len, and a bloom filter of size newlen that divides len is easily
// derived by oring the bits together len/newlen times. This works because
// x%newlen == x%len%newlen iff newlen divides len, so we can shrink the bloom filter
// without having the original probes or keys! This functionality lets us construct
// a bloom filter while only having an upper bound on cardinality, instead of having
// to have a separate, expensive input-counting phase.
func (b *bloom) shrinkToSize(target float64) bloom {
	if target <= 0.0 || target >= 1.0 {
		return *b
	}

	// shrinking sets each output bit to the OR-ed together
	// output of k=`factor` bits that are set with probability
	// x=`b.load()`. We want to achieve a target load `y`.
	//
	// The probability that a bit is set is one minus the probability
	// that its inputs are all unset-- 1-(1-x)^k. To get k given y,
	// https://www.wolframalpha.com/input/?i=solve+for+k+in+y%3D1-%281-x%29%5Ek
	// => k=log(1-y)/log(1-x)
	factor := len(b.bits)
	divisor := math.Log(1 - b.load())
	if divisor != 0 { // avoid divide by zero for empty filter (b.load() is 0)
		factor = int(math.Log(1-target) / divisor)
	}

	// We can only shrink the bloom filter to a size that is a factor of the
	// input size. This is made easier by bloomSizeBase being highly composite.
	for factor > 0 && len(b.bits)%factor != 0 {
		factor--
	}

	if factor <= 1 {
		return *b
	}
	out := bloom{b.hasher, make([]uint8, len(b.bits)/factor)}
	j := 0
	for i := 0; i < len(b.bits); i++ {
		out.bits[j] |= b.bits[i]
		j++
		if j >= len(out.bits) {
			j = 0
		}
	}

	return out
}

func (b bloom) write(w *writer) {
	// header: serialization version, hasher id
	w.Write([]byte{1, bloomHasherIds[reflect.ValueOf(b.hasher).Pointer()]}) //nolint:errcheck // ignore that we don't check Write's error status
	w.Write(b.bits)                                                         //nolint:errcheck // ignore that we don't check Write's error status
}

func makeBloomFilterFromEncoded(buf []byte) (bloom, error) {
	b := bloom{}
	if len(buf) < 2 || buf[0] != 1 {
		return b, errors.New("invalid bloom filter encoding (wrong size/version)")
	}
	if buf[1] <= 0 || int(buf[1]) > len(bloomHashers) {
		return b, errors.New("invalid bloom filter encoding (unknown hasher type)")
	}
	b.hasher = bloomHashers[buf[1]-1]
	b.bits = buf[2:]
	return b, nil
}

// bloomHasherIds maps from function pointers to hash numbers, to allow
// backwards compatible hash function changes.
var bloomHasherIds = map[uintptr]byte{
	reflect.ValueOf(bloomHasherCRCBlocked64B8K3).Pointer(): 1,
}

// bloomHashers maps from hash identifierss stored in encoded bloom filters to
// hash functions, to allo backwards compatible hash function evolution.
var bloomHashers = []bloomHash{
	bloomHasherCRCBlocked64B8K3,
}

// The following functions and constants *must not* be changed unless you can prove
// they have exactly identical behavior. Instead of changing these functions,
// add a new hash function and a new entry in bloomHasherIds and bloomHashers,
// then change the default hash function.
//
// This allows changing to a new hash function without invalidating all existing
// files, and more importantly, without starting to return false negatives (!!!)
// because the hash function changed unexpectedly.
const bloomHashMinWordLength = 4

// bloomWordTab uses a table to implement a matcher for the regex \w{4,}
var bloomWordTab [128 / 64]uint64 = initBloomWordTab()

func initBloomWordTab() [128 / 64]uint64 {
	var tab [128 / 64]uint64
	for x := byte(0); x < 128; x++ {
		if x == '_' || 'a' <= x && x <= 'z' || 'A' <= x && x <= 'Z' || '0' <= x && x <= '9' {
			tab[x/64] |= 1 << (x % 64)
		}
	}
	return tab
}

func findNextWord(i int, in []byte) (int, []byte) {
	// Dropping the unicode case-folding requirement would
	// improve performance here. There are *exactly* two Unicode
	// codepoints that map down to ASCII:
	//   K: U+212A KELVIN SIGN
	//   ſ: U+017F LATIN SMALL LETTER LONG S
	for i < len(in) {
		// skip non-word runes
		for i < len(in) {
			c, sz := utf8.DecodeRune(in[i:])
			c = unicode.ToLower(c)
			if c < 128 && bloomWordTab[c/64]&(1<<(c%64)) != 0 {
				break
			}
			i += sz
		}
		// count length of word section
		wordStart := i
		runeLength := 0
		for i < len(in) {
			c, sz := utf8.DecodeRune(in[i:])
			c = unicode.ToLower(c)
			if c >= 128 || bloomWordTab[c/64]&(1<<(c%64)) == 0 {
				break
			}
			runeLength++
			i += sz
		}
		// Skip short words.
		if runeLength < bloomHashMinWordLength {
			continue
		}
		return i, bytes.ToLower(in[wordStart:i])
	}
	return i, nil
}

func bloomHasherCRC(in []byte) []uint32 {
	out := []uint32{}
	for i := 0; i < len(in); {
		var s []byte
		i, s = findNextWord(i, in)
		// Add all substrings of length 4-10 to the bloom filter.
		// Not having a bound on the maximum length causes quadratic
		// probe counts on long "words"-- like a 241KB line of
		// DNA ("gtggcaccctgactgg...")
		for l := 10; l >= 4; l-- {
			for i := 0; i+l <= len(s); i++ {
				if '0' <= s[i] && s[i] <= '9' {
					// Long numeric/hex constants are generally unlikely
					// to be searched for, so don't include probes for
					// substrings that start with a number.
					continue
				}
				out = append(out, crc32.Checksum(s[i:i+l], crcTab))
			}
		}
	}
	return out
}

func bloomHasherCRCBlocked64B8K3(in []byte) []uint32 {
	out := []uint32{}
	for i := 0; i < len(in); {
		var s []byte
		i, s = findNextWord(i, in)
		for i := 0; i <= len(s)-4; i++ {
			if '0' <= s[i] && s[i] <= '9' {
				continue
			}
			base := crc32.Checksum(s[i:i+4], crcTab) * 512
			for j := i + 4; j < i+8 && j <= len(s); j++ {
				h := crc32.Checksum(s[i:j], crcTab)
				out = append(out,
					base|h%512, base|(h>>9)%512,
					base|(h>>18)%512,
				)
			}
		}
	}
	return out
}
