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
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
)

// IndexFile is a file suitable for concurrent read access. For performance
// reasons, it allows a mmap'd implementation.
type IndexFile interface {
	Read(off uint32, sz uint32) ([]byte, error)
	Size() (uint32, error)
	Close()
	Name() string
}

// reader is a stateful file
type reader struct {
	r   IndexFile
	off uint32
}

func (r *reader) seek(off uint32) {
	r.off = off
}

func (r *reader) U32() (uint32, error) {
	b, err := r.r.Read(r.off, 4)
	r.off += 4
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}

func (r *reader) U64() (uint64, error) {
	b, err := r.r.Read(r.off, 8)
	r.off += 8
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b), nil
}

func (r *reader) readTOC(toc *indexTOC) error {
	sz, err := r.r.Size()
	if err != nil {
		return err
	}
	r.off = sz - 8

	var tocSection simpleSection
	if err := tocSection.read(r); err != nil {
		return err
	}

	r.seek(tocSection.off)

	sectionCount, err := r.U32()
	if err != nil {
		return err
	}

	secs := toc.sectionsHACK(sectionCount)

	if len(secs) != int(sectionCount) {
		return fmt.Errorf("section count mismatch: got %d want %d", sectionCount, len(secs))
	}

	for _, s := range secs {
		if err := s.read(r); err != nil {
			return err
		}
	}
	return nil
}

func (r *indexData) readSectionBlob(sec simpleSection) ([]byte, error) {
	return r.file.Read(sec.off, sec.sz)
}

func readSectionU32(f IndexFile, sec simpleSection) ([]uint32, error) {
	if sec.sz%4 != 0 {
		return nil, fmt.Errorf("barf: section size %% 4 != 0: sz %d ", sec.sz)
	}
	blob, err := f.Read(sec.off, sec.sz)
	if err != nil {
		return nil, err
	}
	arr := make([]uint32, 0, len(blob)/4)
	for len(blob) > 0 {
		arr = append(arr, binary.BigEndian.Uint32(blob))
		blob = blob[4:]
	}
	return arr, nil
}

func readSectionU64(f IndexFile, sec simpleSection) ([]uint64, error) {
	if sec.sz%8 != 0 {
		return nil, fmt.Errorf("barf: section size %% 8 != 0: sz %d ", sec.sz)
	}
	blob, err := f.Read(sec.off, sec.sz)
	if err != nil {
		return nil, err
	}
	arr := make([]uint64, 0, len(blob)/8)
	for len(blob) > 0 {
		arr = append(arr, binary.BigEndian.Uint64(blob))
		blob = blob[8:]
	}
	return arr, nil
}

func (r *reader) readJSON(data interface{}, sec *simpleSection) error {
	blob, err := r.r.Read(sec.off, sec.sz)
	if err != nil {
		return err
	}

	return json.Unmarshal(blob, data)
}

func (r *reader) readIndexData(toc *indexTOC) (*indexData, error) {
	d := indexData{
		file:           r.r,
		ngrams:         map[ngram]simpleSection{},
		fileNameNgrams: map[ngram][]uint32{},
		branchIDs:      map[string]uint{},
		branchNames:    map[uint]string{},
	}

	blob, err := d.readSectionBlob(toc.metaData)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(blob, &d.metaData); err != nil {
		return nil, err
	}

	ensureSourcegraphSymbolsHack()

	// Once we are not on version 16 use this code again
	// if d.metaData.IndexFormatVersion != IndexFormatVersion {
	if d.metaData.IndexFormatVersion != 16 && d.metaData.IndexFormatVersion != 15 {
		return nil, fmt.Errorf("file is v%d, want v%d", d.metaData.IndexFormatVersion, IndexFormatVersion)
	}

	blob, err = d.readSectionBlob(toc.repoMetaData)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(blob, &d.repoMetaData); err != nil {
		return nil, err
	}

	d.boundariesStart = toc.fileContents.data.off
	d.boundaries = toc.fileContents.relativeIndex()
	d.newlinesStart = toc.newlines.data.off
	d.newlinesIndex = toc.newlines.relativeIndex()
	d.docSectionsStart = toc.fileSections.data.off
	d.docSectionsIndex = toc.fileSections.relativeIndex()

	if d.metaData.IndexFormatVersion == 16 {
		d.symbols.symKindIndex = toc.symbolKindMap.relativeIndex()
		d.fileEndSymbol, err = readSectionU32(d.file, toc.fileEndSymbol)
		if err != nil {
			return nil, err
		}

		// Call readSectionBlob on each section key, and store the result in
		// the blob value.
		for sect, blob := range map[simpleSection]*[]byte{
			toc.symbolMap.index:    &d.symbols.symIndex,
			toc.symbolMap.data:     &d.symbols.symContent,
			toc.symbolKindMap.data: &d.symbols.symKindContent,
			toc.symbolMetaData:     &d.symbols.symMetaData,
		} {
			if *blob, err = d.readSectionBlob(sect); err != nil {
				return nil, err
			}
		}
	}

	d.checksums, err = d.readSectionBlob(toc.contentChecksums)
	if err != nil {
		return nil, err
	}

	d.languages, err = d.readSectionBlob(toc.languages)
	if err != nil {
		return nil, err
	}

	textContent, err := d.readSectionBlob(toc.ngramText)
	if err != nil {
		return nil, err
	}
	postingsIndex := toc.postings.relativeIndex()

	const ngramEncoding = 8
	for i := 0; i < len(textContent); i += ngramEncoding {
		j := i / ngramEncoding
		ng := ngram(binary.BigEndian.Uint64(textContent[i : i+ngramEncoding]))
		d.ngrams[ng] = simpleSection{
			toc.postings.data.off + postingsIndex[j],
			postingsIndex[j+1] - postingsIndex[j],
		}
	}

	d.fileBranchMasks, err = readSectionU64(d.file, toc.branchMasks)
	if err != nil {
		return nil, err
	}

	d.fileNameContent, err = d.readSectionBlob(toc.fileNames.data)
	if err != nil {
		return nil, err
	}

	d.fileNameIndex = toc.fileNames.relativeIndex()

	nameNgramText, err := d.readSectionBlob(toc.nameNgramText)
	if err != nil {
		return nil, err
	}

	fileNamePostingsData, err := d.readSectionBlob(toc.namePostings.data)
	if err != nil {
		return nil, err
	}

	fileNamePostingsIndex := toc.namePostings.relativeIndex()
	for i := 0; i < len(nameNgramText); i += ngramEncoding {
		j := i / ngramEncoding
		off := fileNamePostingsIndex[j]
		end := fileNamePostingsIndex[j+1]
		ng := ngram(binary.BigEndian.Uint64(nameNgramText[i : i+ngramEncoding]))
		d.fileNameNgrams[ng] = fromDeltas(fileNamePostingsData[off:end], nil)
	}

	for j, br := range d.repoMetaData.Branches {
		id := uint(1) << uint(j)
		d.branchIDs[br.Name] = id
		d.branchNames[id] = br.Name
	}

	blob, err = d.readSectionBlob(toc.runeDocSections)
	if err != nil {
		return nil, err
	}
	d.runeDocSections = unmarshalDocSections(blob, nil)

	for sect, dest := range map[simpleSection]*[]uint32{
		toc.subRepos:        &d.subRepos,
		toc.runeOffsets:     &d.runeOffsets,
		toc.nameRuneOffsets: &d.fileNameRuneOffsets,
		toc.nameEndRunes:    &d.fileNameEndRunes,
		toc.fileEndRunes:    &d.fileEndRunes,
	} {
		if blob, err := d.readSectionBlob(sect); err != nil {
			return nil, err
		} else {
			*dest = fromSizedDeltas(blob, nil)
		}
	}

	var keys []string
	for k := range d.repoMetaData.SubRepoMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	d.subRepoPaths = keys

	d.languageMap = map[byte]string{}
	for k, v := range d.metaData.LanguageMap {
		d.languageMap[v] = k
	}

	if err := d.verify(); err != nil {
		return nil, err
	}

	d.calculateStats()
	return &d, nil
}

func (d *indexData) verify() error {
	// This is not an exhaustive check: the postings can easily
	// generate OOB acccesses, and are expensive to check, but this lets us rule out
	// other sources of OOB access.
	n := len(d.fileNameIndex)
	if n == 0 {
		return nil
	}

	n--
	for what, got := range map[string]int{
		"boundaries":        len(d.boundaries) - 1,
		"branch masks":      len(d.fileBranchMasks),
		"doc section index": len(d.docSectionsIndex) - 1,
		"newlines index":    len(d.newlinesIndex) - 1,
	} {
		if got != n {
			return fmt.Errorf("got %s %d, want %d", what, got, n)
		}
	}
	return nil
}

func (d *indexData) readContents(i uint32) ([]byte, error) {
	return d.readSectionBlob(simpleSection{
		off: d.boundariesStart + d.boundaries[i],
		sz:  d.boundaries[i+1] - d.boundaries[i],
	})
}

func (d *indexData) readContentSlice(off uint32, sz uint32) ([]byte, error) {
	// TODO(hanwen): cap result if it is at the end of the content
	// section.
	return d.readSectionBlob(simpleSection{
		off: d.boundariesStart + off,
		sz:  sz})
}

func (d *indexData) readNewlines(i uint32, buf []uint32) ([]uint32, uint32, error) {
	sec := simpleSection{
		off: d.newlinesStart + d.newlinesIndex[i],
		sz:  d.newlinesIndex[i+1] - d.newlinesIndex[i],
	}
	blob, err := d.readSectionBlob(sec)
	if err != nil {
		return nil, 0, err
	}

	return fromSizedDeltas(blob, buf), sec.sz, nil
}

func (d *indexData) readDocSections(i uint32, buf []DocumentSection) ([]DocumentSection, uint32, error) {
	sec := simpleSection{
		off: d.docSectionsStart + d.docSectionsIndex[i],
		sz:  d.docSectionsIndex[i+1] - d.docSectionsIndex[i],
	}
	blob, err := d.readSectionBlob(sec)
	if err != nil {
		return nil, 0, err
	}

	return unmarshalDocSections(blob, buf), sec.sz, nil
}

// Sourcegraph Experiment: Read content into memory and do not rely on
// MMAP. We have seen unexpected behaviour of the OS file cache which we
// haven't been able to narrow down. As such we want a toggle to just keep
// everything in memory. This will use a lot more memory.
var sourcegraphInMemoryContent bool

func init() {
	sourcegraphInMemoryContent, _ = strconv.ParseBool(os.Getenv("IN_MEMORY_CONTENT"))
	if sourcegraphInMemoryContent {
		log.Println("INFO: IN_MEMORY_CONTENT=true, this will load all file contents into memory to avoid paging out to disk in case of cache misses (at increased memory cost).")
	}
}

// NewSearcher creates a Searcher for a single index file.  Search
// results coming from this searcher are valid only for the lifetime
// of the Searcher itself, ie. []byte members should be copied into
// fresh buffers if the result is to survive closing the shard.
func NewSearcher(r IndexFile) (Searcher, error) {
	rd := &reader{r: r}

	var toc indexTOC
	if err := rd.readTOC(&toc); err != nil {
		return nil, err
	}
	indexData, err := rd.readIndexData(&toc)
	if err != nil {
		return nil, err
	}

	if sourcegraphInMemoryContent {
		cached := &cachedIndexFile{file: r}
		if err := cached.cache(toc.fileContents.data); err != nil {
			return nil, err
		}
		r = cached
	}

	indexData.file = r
	return indexData, nil
}

// ReadMetadata returns the metadata of index shard without reading
// the index data. The IndexFile is not closed.
func ReadMetadata(inf IndexFile) (*Repository, *IndexMetadata, error) {
	rd := &reader{r: inf}
	var toc indexTOC
	if err := rd.readTOC(&toc); err != nil {
		return nil, nil, err
	}

	var md IndexMetadata
	if err := rd.readJSON(&md, &toc.metaData); err != nil {
		return nil, nil, err
	}

	var repo Repository
	if err := rd.readJSON(&repo, &toc.repoMetaData); err != nil {
		return nil, nil, err
	}

	return &repo, &md, nil
}

// Sourcegraph experiment follows which caches blocks
type cachedBlock struct {
	off  uint32
	data []byte
}

type cachedIndexFile struct {
	file   IndexFile
	blocks []cachedBlock
}

func (c *cachedIndexFile) cache(ss simpleSection) error {
	b, err := c.file.Read(ss.off, ss.sz)
	if err != nil {
		return err
	}

	// copy to ensure in memory (likely pointing into mmap region)
	data := make([]byte, ss.sz)
	copy(data, b)

	c.blocks = append(c.blocks, cachedBlock{
		off:  ss.off,
		data: data,
	})
	return nil
}

func (c *cachedIndexFile) Read(off uint32, sz uint32) ([]byte, error) {
	for _, block := range c.blocks {
		if off < block.off {
			continue
		}
		relOff := off - block.off
		relEnd := relOff + sz
		if relEnd <= uint32(len(block.data)) {
			return block.data[relOff:relEnd], nil
		}
	}
	return c.file.Read(off, sz)
}
func (c *cachedIndexFile) Size() (uint32, error) {
	return c.file.Size()
}
func (c *cachedIndexFile) Close() {
	c.blocks = nil
	c.file.Close()
}

func (c *cachedIndexFile) Name() string {
	var b strings.Builder
	b.WriteString("Cached{")
	b.WriteString(c.file.Name())
	b.WriteString(", Blocks: ")
	for i, block := range c.blocks {
		if i != 0 {
			b.WriteString(", ")
		}
		_, _ = fmt.Fprintf(&b, "{%d %d}", block.off, len(block.data))
	}
	b.WriteString("}")
	return b.String()
}
