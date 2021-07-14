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
	"os"
	"sort"
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

	secs := toc.sections()

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
		fileNameNgrams: map[ngram][]byte{},
		branchIDs:      []map[string]uint{},
		branchNames:    []map[uint]string{},
	}

	repo, md, err := r.readMetadata(toc)
	if md != nil && md.IndexFormatVersion != IndexFormatVersion {
		return nil, fmt.Errorf("file is v%d, want v%d", md.IndexFormatVersion, IndexFormatVersion)
	} else if err != nil {
		return nil, err
	}

	d.metaData = *md
	d.repoMetaData = []Repository{*repo}

	for _, md := range d.repoMetaData {
		md.RawConfigEncoded = EncodeRawConfig(md.RawConfig)
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

	d.ngrams, err = d.readNgrams(toc)
	if err != nil {
		return nil, err
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

	d.fileNameNgrams, err = d.readFileNameNgrams(toc)
	if err != nil {
		return nil, err
	}

	for _, md := range d.repoMetaData {
		repoBranchIDs := make(map[string]uint, len(md.Branches))
		repoBranchNames := make(map[uint]string, len(md.Branches))
		for j, br := range md.Branches {
			id := uint(1) << uint(j)
			repoBranchIDs[br.Name] = id
			repoBranchNames[id] = br.Name
		}
		d.branchIDs = append(d.branchIDs, repoBranchIDs)
		d.branchNames = append(d.branchNames, repoBranchNames)
	}

	blob, err := d.readSectionBlob(toc.runeDocSections)
	if err != nil {
		return nil, err
	}
	d.runeDocSections = blob

	var runeOffsets, fileNameRuneOffsets []uint32

	for sect, dest := range map[simpleSection]*[]uint32{
		toc.subRepos:        &d.subRepos,
		toc.runeOffsets:     &runeOffsets,
		toc.nameRuneOffsets: &fileNameRuneOffsets,
		toc.nameEndRunes:    &d.fileNameEndRunes,
		toc.fileEndRunes:    &d.fileEndRunes,
	} {
		if blob, err := d.readSectionBlob(sect); err != nil {
			return nil, err
		} else {
			*dest = fromSizedDeltas(blob, nil)
		}
	}

	d.runeOffsets = makeRuneOffsetMap(runeOffsets)
	d.fileNameRuneOffsets = makeRuneOffsetMap(fileNameRuneOffsets)

	d.subRepoPaths = make([][]string, 0, len(d.repoMetaData))
	for i := 0; i < len(d.repoMetaData); i++ {
		keys := make([]string, 0, len(d.repoMetaData[i].SubRepoMap))
		for k := range d.repoMetaData[i].SubRepoMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		d.subRepoPaths = append(d.subRepoPaths, keys)
	}

	d.languageMap = map[byte]string{}
	for k, v := range d.metaData.LanguageMap {
		d.languageMap[v] = k
	}

	if err := d.verify(); err != nil {
		return nil, err
	}

	// This is a hack for now to keep the shard format unchanged. To support shard
	// merging we will store "repos" in the shard.
	repos := make([]uint16, 0, len(d.fileBranchMasks))
	for i := 0; i < len(d.fileBranchMasks); i++ {
		repos = append(repos, 0) // just support 1 repo for now.
	}
	d.repos = repos

	if err := d.calculateStats(); err != nil {
		return nil, err
	}

	return &d, nil
}

const (
	rawConfigYes uint8 = iota + 1
	rawConfigNo
)

// EncodeRawConfig encodes a rawConfig map into a uint8 flag that corresponds to
// the encoding of query.RawConfig.Encoded.
func EncodeRawConfig(rawConfig map[string]string) uint8 {
	var encoded uint8
	for i, f := range []string{"public", "fork", "archived"} {
		var e uint8
		v, ok := rawConfig[f]
		if ok && v == "1" {
			e = e | rawConfigYes
		} else {
			e = e | rawConfigNo
		}
		encoded = encoded | e<<(2*i)
	}
	return encoded
}

func (r *reader) readMetadata(toc *indexTOC) (*Repository, *IndexMetadata, error) {
	var md IndexMetadata
	if err := r.readJSON(&md, &toc.metaData); err != nil {
		return nil, nil, err
	}

	var repo Repository
	if err := r.readJSON(&repo, &toc.repoMetaData); err != nil {
		return nil, &md, err
	}

	// Sourcegraph specific: we support mutating metadata via an additional
	// ".meta" file. This is to support tombstoning. An additional benefit is we
	// can update metadata (such as Rank and Name) without re-indexing content.
	if b, err := os.ReadFile(r.r.Name() + ".meta"); err != nil && !os.IsNotExist(err) {
		return nil, &md, fmt.Errorf("failed to read meta file: %w", err)
	} else if len(b) > 0 {
		err = json.Unmarshal(b, &repo)
		if err != nil {
			return nil, &md, fmt.Errorf("failed to unmarshal meta file: %w", err)
		}
	}

	return &repo, &md, nil
}

const ngramEncoding = 8

func (d *indexData) readNgrams(toc *indexTOC) (arrayNgramOffset, error) {
	textContent, err := d.readSectionBlob(toc.ngramText)
	if err != nil {
		return arrayNgramOffset{}, err
	}
	postingsIndex := toc.postings.relativeIndex()

	for i := 0; i < len(postingsIndex); i++ {
		postingsIndex[i] += toc.postings.data.off
	}

	ngrams := make([]ngram, 0, len(textContent)/ngramEncoding)
	for i := 0; i < len(textContent); i += ngramEncoding {
		ng := ngram(binary.BigEndian.Uint64(textContent[i : i+ngramEncoding]))
		ngrams = append(ngrams, ng)
	}

	return makeArrayNgramOffset(ngrams, postingsIndex), nil
}

func (d *indexData) readFileNameNgrams(toc *indexTOC) (map[ngram][]byte, error) {
	nameNgramText, err := d.readSectionBlob(toc.nameNgramText)
	if err != nil {
		return nil, err
	}

	fileNamePostingsData, err := d.readSectionBlob(toc.namePostings.data)
	if err != nil {
		return nil, err
	}

	fileNamePostingsIndex := toc.namePostings.relativeIndex()

	fileNameNgrams := make(map[ngram][]byte, len(nameNgramText)/ngramEncoding)
	for i := 0; i < len(nameNgramText); i += ngramEncoding {
		j := i / ngramEncoding
		off := fileNamePostingsIndex[j]
		end := fileNamePostingsIndex[j+1]
		ng := ngram(binary.BigEndian.Uint64(nameNgramText[i : i+ngramEncoding]))
		fileNameNgrams[ng] = fileNamePostingsData[off:end]
	}

	return fileNameNgrams, nil
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
		sz:  sz,
	})
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

	return rd.readMetadata(&toc)
}

// IndexFilePaths returns all paths for the IndexFile at filepath p that
// exist. Note: if no files exist this will return an empty slice and nil
// error.
//
// This is p and the ".meta" file for p.
func IndexFilePaths(p string) ([]string, error) {
	paths := []string{p, p + ".meta"}
	exist := paths[:0]
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			exist = append(exist, p)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return exist, nil
}

func loadIndexData(r IndexFile) (*indexData, error) {
	rd := &reader{r: r}

	var toc indexTOC
	if err := rd.readTOC(&toc); err != nil {
		return nil, err
	}
	return rd.readIndexData(&toc)
}

// PrintNgramStats outputs a list of the form
//    n_1 trigram_1
//    n_2 trigram_2
//    ...
// where n_i is the length of the postings list of trigram_i stored in r.
func PrintNgramStats(r IndexFile) error {
	id, err := loadIndexData(r)
	if err != nil {
		return err
	}
	var rNgram [3]rune
	for ngram, ss := range id.ngrams.DumpMap() {
		rNgram = ngramToRunes(ngram)
		fmt.Printf("%d\t%q\n", ss.sz, string(rNgram[:]))
	}
	return nil
}
