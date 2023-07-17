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
	"fmt"
	"hash/crc64"
	"log"
	"math/bits"
	"unicode/utf8"

	"golang.org/x/exp/slices"

	"github.com/sourcegraph/zoekt/query"
)

// indexData holds the pattern-independent data that we have to have
// in memory to search. Most of the memory is taken up by the ngram =>
// offset index.
type indexData struct {
	symbols symbolData

	file IndexFile

	ngrams btreeIndex

	newlinesStart uint32
	newlinesIndex []uint32

	docSectionsStart uint32
	docSectionsIndex []uint32

	runeDocSections    []DocumentSection
	runeDocSectionsRaw []byte

	// rune offset=>byte offset mapping, relative to the start of the content corpus
	runeOffsets runeOffsetMap

	// offsets of file contents; includes end of last file
	boundariesStart uint32
	boundaries      []uint32

	// rune offsets for the file content boundaries
	fileEndRunes []uint32

	fileNameContent []byte
	fileNameIndex   []uint32
	fileNameNgrams  btreeIndex

	// fileEndSymbol[i] is the index of the first symbol for document i.
	fileEndSymbol []uint32

	// rune offset=>byte offset mapping, relative to the start of the filename corpus
	fileNameRuneOffsets runeOffsetMap

	// rune offsets for the file name boundaries
	fileNameEndRunes []uint32

	fileBranchMasks []uint64

	// mask (power of 2) => name
	branchNames []map[uint]string

	// name => mask (power of 2)
	branchIDs []map[string]uint

	metaData     IndexMetadata
	repoMetaData []Repository

	subRepos     []uint32
	subRepoPaths [][]string

	// Checksums for all the files, at 8-byte intervals
	checksums []byte

	// languages for all the files.
	languages []byte

	// inverse of LanguageMap in metaData
	languageMap map[uint16]string

	repoListEntry []RepoListEntry

	// repository indexes for all the files
	repos []uint16

	// Experimental: docID => rank vec
	ranks [][]float64

	// rawConfigMasks contains the encoded RawConfig for each repository
	rawConfigMasks []uint8
}

type symbolData struct {
	// symContent stores Symbol.Sym and Symbol.Parent.
	// TODO we don't need to store Symbol.Sym.
	symContent []byte
	symIndex   []byte
	// symKindContent is an enum of sym.Kind and sym.ParentKind
	symKindContent []byte
	symKindIndex   []uint32
	// symMetadata is [4]uint32 0 Kind Parent ParentKind
	symMetaData []byte
}

func uint32SliceAt(a []byte, n uint32) uint32 {
	return binary.BigEndian.Uint32(a[n*4:])
}

func uint32SliceLen(a []byte) uint32 {
	return uint32(len(a) / 4)
}

// parent returns index i of the parent enum
func (d *symbolData) parent(i uint32) []byte {
	delta := uint32SliceAt(d.symIndex, 0)
	start := uint32SliceAt(d.symIndex, i) - delta
	var end uint32
	if i+1 == uint32SliceLen(d.symIndex) {
		end = uint32(len(d.symContent))
	} else {
		end = uint32SliceAt(d.symIndex, i+1) - delta
	}
	return d.symContent[start:end]
}

// kind returns index i of the kind enum
func (d *symbolData) kind(i uint32) []byte {
	return d.symKindContent[d.symKindIndex[i]:d.symKindIndex[i+1]]
}

// data returns the symbol at index i
func (d *symbolData) data(i uint32) *Symbol {
	size := uint32(4 * 4) // 4 uint32s
	offset := i * size
	if offset >= uint32(len(d.symMetaData)) {
		return nil
	}

	metadata := d.symMetaData[offset : offset+size]
	sym := &Symbol{}
	key := uint32SliceAt(metadata, 1)
	sym.Kind = string(d.kind(key))
	key = uint32SliceAt(metadata, 2)
	sym.Parent = string(d.parent(key))
	key = uint32SliceAt(metadata, 3)
	sym.ParentKind = string(d.kind(key))
	return sym
}

func (d *indexData) getChecksum(idx uint32) []byte {
	start := crc64.Size * idx
	return d.checksums[start : start+crc64.Size]
}

func (d *indexData) getLanguage(idx uint32) uint16 {
	if d.metaData.IndexFeatureVersion < 12 {
		// older zoekt files had 8-bit language entries
		return uint16(d.languages[idx])
	}
	// newer zoekt files have 16-bit language entries
	return uint16(d.languages[idx*2]) | uint16(d.languages[idx*2+1])<<8
}

// calculates stats for files in the range [start, end).
func (d *indexData) calculateStatsForFileRange(start, end uint32) RepoStats {
	if start >= end {
		// An empty shard for an empty repository.
		return RepoStats{
			Shards: 1,
		}
	}

	bytesContent := d.boundaries[end] - d.boundaries[start]
	bytesFN := d.fileNameIndex[end] - d.fileNameIndex[start]
	count, defaultCount, otherCount := d.calculateNewLinesStats(start, end)

	// CR keegan for stefan: I think we may want to restructure RepoListEntry so
	// that we don't change anything, except we have
	// []Repository. Alternatively, things we can divide up we do (like
	// here). Right now I don't like that these numbers are not true, especially
	// after aggregation. For now I will move forward with this until we can
	// chat more.
	return RepoStats{
		ContentBytes: int64(bytesContent) + int64(bytesFN),
		Documents:    int(end - start),
		// CR keegan for stefan: our shard count is going to go out of whack,
		// since we will aggregate these. So we will report more shards than are
		// present on disk. What should we do?
		Shards: 1,

		// Sourcegraph specific
		NewLinesCount:              count,
		DefaultBranchNewLinesCount: defaultCount,
		OtherBranchesNewLinesCount: otherCount,
	}
}

func (d *indexData) calculateStats() error {
	d.repoListEntry = make([]RepoListEntry, 0, len(d.repoMetaData))
	var start, end uint32

	for repoID, md := range d.repoMetaData {
		// determine the file range for repo i
		for end < uint32(len(d.repos)) && d.repos[end] == uint16(repoID) {
			end++
		}
		if start < end && d.repos[start] != uint16(repoID) {
			return fmt.Errorf("shard documents out of order with respect to repositories: expected document %d to be part of repo %d", start, repoID)
		}

		d.repoListEntry = append(d.repoListEntry, RepoListEntry{
			Repository:    md,
			IndexMetadata: d.metaData,
			Stats:         d.calculateStatsForFileRange(start, end),
		})
		start = end
	}

	// All repos in a compound shard share memoryUse. So we average out the
	// memoryUse per shard in our reporting. This has the benefit that when you
	// aggregate the IndexBytes you get back the actual memoryUse.
	//
	// TODO take into account tombstones for aggregation. Even better, adjust
	// API to be shard centric not repo centric.
	if len(d.repoListEntry) > 0 {
		indexBytes := d.memoryUse()
		indexBytesChunk := indexBytes / len(d.repoListEntry)
		for i := range d.repoListEntry {
			d.repoListEntry[i].Stats.IndexBytes = int64(indexBytesChunk)
			indexBytes -= indexBytesChunk
		}
		d.repoListEntry[0].Stats.IndexBytes += int64(indexBytes)
	}

	return nil
}

// calculateNewLinesStats computes some Sourcegraph specific statistics for files
// in the range [start, end). These are not as efficient to calculate as the
// normal statistics. We experimentally measured about a 10% slower shard load
// time. However, we find these values very useful to track and computing them
// outside of load time introduces a lot of complexity.
func (d *indexData) calculateNewLinesStats(start, end uint32) (count, defaultCount, otherCount uint64) {
	for i := start; i < end; i++ {
		// branchMask is a bitmask of the branches for a document. Zoekt by
		// convention represents the default branch as the lowest bit.
		branchMask := d.fileBranchMasks[i]
		isDefault := (branchMask & 1) == 1
		others := uint64(bits.OnesCount64(branchMask >> 1))

		// this is readNewlines but only reading the size of each section which
		// corresponds to the number of newlines.
		sec := simpleSection{
			off: d.newlinesStart + d.newlinesIndex[i],
			sz:  d.newlinesIndex[i+1] - d.newlinesIndex[i],
		}
		// We are only reading the first varint which is the size. So we don't
		// need to read more than MaxVarintLen64 bytes.
		if sec.sz > binary.MaxVarintLen64 {
			sec.sz = binary.MaxVarintLen64
		}
		blob, err := d.readSectionBlob(sec)
		if err != nil {
			log.Printf("error reading newline index for document %d on shard %s: %v", i, d.file.Name(), err)
			continue
		}
		sz, _ := binary.Uvarint(blob)

		count += sz
		if isDefault {
			defaultCount += sz
		}
		otherCount += (others * sz)
	}

	return
}

func (d *indexData) String() string {
	return fmt.Sprintf("shard(%s)", d.file.Name())
}

// calculates an approximate size of indexData in memory in bytes.
func (d *indexData) memoryUse() int {
	sz := 0
	for _, a := range [][]uint32{
		d.newlinesIndex, d.docSectionsIndex,
		d.boundaries, d.fileNameIndex,
		d.fileEndRunes, d.fileNameEndRunes,
		d.fileEndSymbol, d.symbols.symKindIndex,
		d.subRepos,
	} {
		sz += 4 * len(a)
	}
	sz += d.runeOffsets.sizeBytes()
	sz += d.fileNameRuneOffsets.sizeBytes()
	sz += len(d.languages)
	sz += len(d.checksums)
	sz += 2 * len(d.repos)
	if len(d.ranks) > 0 {
		sz += 8 * len(d.ranks) * len(d.ranks[0])
	}
	sz += 8 * len(d.runeDocSections)
	sz += 8 * len(d.fileBranchMasks)
	sz += d.ngrams.SizeBytes()
	sz += d.fileNameNgrams.SizeBytes()
	return sz
}

const maxUInt32 = 0xffffffff

func firstMinarg(xs []uint32) uint32 {
	m := uint32(maxUInt32)
	j := len(xs)
	for i, x := range xs {
		if x < m {
			m = x
			j = i
		}
	}
	return uint32(j)
}

func lastMinarg(xs []uint32) uint32 {
	m := uint32(maxUInt32)
	j := len(xs)
	for i, x := range xs {
		if x <= m {
			m = x
			j = i
		}
	}
	return uint32(j)
}

func (d *indexData) ngramFrequency(ng ngram, filename bool) uint32 {
	if filename {
		return d.fileNameNgrams.Get(ng).sz
	}
	return d.ngrams.Get(ng).sz
}

// ngramIndexes returns the indexes of the ngrams in the index. We return a
// slice of slices because we have to keep track of ngram variants in case of
// case-insensitive search.
func (d *indexData) ngramIndexes(ngrams []ngram, filename bool) ([]int, int) {
	if filename {
		return d.fileNameNgrams.NgramIndexes(ngrams)
	}
	return d.ngrams.NgramIndexes(ngrams)
}

func (d *indexData) ngramIndexFrequency(ngramIndex int, filename bool) uint32 {
	if filename {
		return d.fileNameNgrams.GetPostingList(ngramIndex).sz
	}
	return d.ngrams.GetPostingList(ngramIndex).sz
}

type ngramIterationResults struct {
	matchIterator

	caseSensitive bool
	fileName      bool
	substrBytes   []byte
	substrLowered []byte
}

func (r *ngramIterationResults) String() string {
	return fmt.Sprintf("wrapper(%v)", r.matchIterator)
}

func (r *ngramIterationResults) candidates() []*candidateMatch {
	cs := r.matchIterator.candidates()
	for _, c := range cs {
		c.caseSensitive = r.caseSensitive
		c.fileName = r.fileName
		c.substrBytes = r.substrBytes
		c.substrLowered = r.substrLowered
	}
	return cs
}

func (d *indexData) iterateNgrams(query *query.Substring) (*ngramIterationResults, error) {
	str := query.Pattern

	// Find the 2 least common ngrams from the string.
	ngramOffs := splitNGrams([]byte(query.Pattern))
	// PERF: Sort to increase the chances adjacent checks are in the same btree
	// bucket (which can cause disk IO).
	slices.SortFunc(ngramOffs, func(a, b runeNgramOff) bool {
		return a.ngram < b.ngram
	})

	frequencies := make([]uint32, 0, len(ngramOffs))
	ngramLookups := 0
	if query.CaseSensitive {
		// Perf: Look up ngram indexes without loading posting lists. This way we can
		// stop early if a ngram does not exist. On the flip side we incur an additional
		// loop and more memory allocations.

		ngrams := make([]ngram, 0, len(ngramOffs))
		for _, ng := range ngramOffs {
			ngrams = append(ngrams, ng.ngram)
		}

		var ngramIndexes []int
		ngramIndexes, ngramLookups = d.ngramIndexes(ngrams, query.FileName)
		if len(ngramIndexes) == 0 {
			return &ngramIterationResults{
				matchIterator: &noMatchTree{
					Why: "freq=0",
					Stats: Stats{
						NgramLookups: ngramLookups,
					},
				},
			}, nil
		}

		for _, ngramIndex := range ngramIndexes {
			frequencies = append(frequencies, d.ngramIndexFrequency(ngramIndex, query.FileName))
		}
	} else {
		for _, o := range ngramOffs {
			var freq uint32
			if query.CaseSensitive {
				freq = d.ngramFrequency(o.ngram, query.FileName)
				ngramLookups++
			} else {
				for _, v := range generateCaseNgrams(o.ngram) {
					freq += d.ngramFrequency(v, query.FileName)
					ngramLookups++
				}
			}

			if freq == 0 {
				return &ngramIterationResults{
					matchIterator: &noMatchTree{
						Why: "freq=0",
						Stats: Stats{
							NgramLookups: ngramLookups,
						},
					},
				}, nil
			}

			frequencies = append(frequencies, freq)
		}
	}

	var first, last runeNgramOff
	{
		firstI := firstMinarg(frequencies)
		frequencies[firstI] = maxUInt32
		lastI := lastMinarg(frequencies)
		first = ngramOffs[firstI]
		last = ngramOffs[lastI]
		if first.index > last.index {
			last, first = first, last
		}
	}

	iter := &ngramDocIterator{
		leftPad:      first.index,
		rightPad:     uint32(utf8.RuneCountInString(str)) - first.index,
		ngramLookups: ngramLookups,
	}
	if query.FileName {
		iter.ends = d.fileNameEndRunes
	} else {
		iter.ends = d.fileEndRunes
	}

	if first != last {
		runeDist := last.index - first.index
		i, err := d.newDistanceTrigramIter(first.ngram, last.ngram, runeDist, query.CaseSensitive, query.FileName)
		if err != nil {
			return nil, err
		}

		iter.iter = i
	} else {
		hitIter, err := d.trigramHitIterator(last.ngram, query.CaseSensitive, query.FileName)
		if err != nil {
			return nil, err
		}
		iter.iter = hitIter
	}

	patBytes := []byte(query.Pattern)
	lowerPatBytes := toLower(patBytes)

	return &ngramIterationResults{
		matchIterator: iter,
		caseSensitive: query.CaseSensitive,
		fileName:      query.FileName,
		substrBytes:   patBytes,
		substrLowered: lowerPatBytes,
	}, nil
}

func (d *indexData) fileName(i uint32) []byte {
	return d.fileNameContent[d.fileNameIndex[i]:d.fileNameIndex[i+1]]
}

func (d *indexData) numDocs() uint32 {
	return uint32(len(d.fileBranchMasks))
}

func (s *indexData) Close() {
	s.file.Close()
}

const (
	rawConfigYes = 1
	rawConfigNo  = 2
)

// encodeRawConfig encodes a rawConfig map into a uint8 mask.
func encodeRawConfig(rawConfig map[string]string) uint8 {
	var encoded uint8
	for i, f := range []string{"public", "fork", "archived"} {
		var e uint8
		v, ok := rawConfig[f]
		if ok && v == "1" {
			e |= rawConfigYes
		} else {
			e |= rawConfigNo
		}
		encoded = encoded | e<<(2*i)
	}
	return encoded
}
