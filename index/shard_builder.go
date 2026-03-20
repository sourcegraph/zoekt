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

package index

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc64"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/languages"
)

var _ = log.Println

const ngramSize = 3

type searchableString struct {
	data []byte
}

// Filled by the linker
var Version string

func HostnameBestEffort() string {
	if h := os.Getenv("NODE_NAME"); h != "" {
		return h
	}
	if h := os.Getenv("HOSTNAME"); h != "" {
		return h
	}
	hostname, _ := os.Hostname()
	return hostname
}

// Store character (unicode codepoint) offset (in bytes) this often.
const runeOffsetFrequency = 100

// postingList holds the varint-encoded delta data and last offset for a
// single ngram. Stored by pointer in the asciiPostings array or the
// postings map so appending to data does not require rewriting the
// map entry or array slot.
type postingList struct {
	data    []byte
	lastOff uint32
}

// asciiNgramBits is the number of bits needed to index all ASCII trigrams.
// ASCII runes are 0-127 (7 bits), so 3 runes = 21 bits = 2M entries.
const asciiNgramBits = 21

// asciiNgramIndex packs three ASCII bytes into a 21-bit array index.
func asciiNgramIndex(a, b, c byte) uint32 {
	return uint32(a)<<14 | uint32(b)<<7 | uint32(c)
}

// asciiIndexToNgram converts a 21-bit ASCII array index back to the
// canonical ngram encoding (rune[0]<<42 | rune[1]<<21 | rune[2]).
func asciiIndexToNgram(idx uint32) ngram {
	r0 := uint64(idx >> 14)
	r1 := uint64((idx >> 7) & 0x7f)
	r2 := uint64(idx & 0x7f)
	return ngram(r0<<42 | r1<<21 | r2)
}

type postingsBuilder struct {
	// ASCII trigrams use direct-indexed array (zero hash/probe cost).
	// Non-ASCII trigrams fall back to the map.
	asciiPostings [1 << asciiNgramBits]*postingList
	postings      map[ngram]*postingList

	// To support UTF-8 searching, we must map back runes to byte
	// offsets. As a first attempt, we sample regularly. The
	// precise offset can be found by walking from the recorded
	// offset to the desired rune.
	runeOffsets []uint32
	runeCount   uint32

	isPlainASCII bool

	endRunes []uint32
	endByte  uint32
}

// Initial capacity for each posting list's byte slice. Empirically,
// the average posting list in a source-code corpus is ~900 bytes
// (600 entries × 1.5 bytes/varint). Pre-allocating 1024 avoids the
// first growth event for the majority of ngrams.
const initialPostingCap = 1024

// estimateNgrams returns a pre-size hint for the non-ASCII postings map,
// derived from the maximum shard content size. Intentionally over-estimates
// (the map only holds non-ASCII trigrams) to avoid rehashing.
func estimateNgrams(shardMaxBytes int) int {
	n := shardMaxBytes / 600
	if n < 1024 {
		n = 1024
	}
	return n
}

func newPostingsBuilder(shardMaxBytes int) *postingsBuilder {
	return &postingsBuilder{
		postings:     make(map[ngram]*postingList, estimateNgrams(shardMaxBytes)),
		isPlainASCII: true,
	}
}

// reset clears the builder for reuse. The ASCII array, map, and their
// postingList allocations are retained so the next shard build avoids
// re-allocating them, cutting memclr and madvise overhead.
func (s *postingsBuilder) reset() {
	for _, pl := range &s.asciiPostings {
		if pl != nil {
			pl.data = pl.data[:0]
			pl.lastOff = 0
		}
	}
	for _, pl := range s.postings {
		pl.data = pl.data[:0]
		pl.lastOff = 0
	}
	s.runeOffsets = s.runeOffsets[:0]
	s.runeCount = 0
	s.isPlainASCII = true
	s.endRunes = s.endRunes[:0]
	s.endByte = 0
}

// Store trigram offsets for the given UTF-8 data. The
// DocumentSections must correspond to rune boundaries in the UTF-8
// data.
func (s *postingsBuilder) newSearchableString(data []byte, byteSections []DocumentSection) (*searchableString, []DocumentSection, error) {
	dest := searchableString{
		data: data,
	}
	var buf [8]byte
	var runeGram [3]rune

	var runeIndex uint32
	byteCount := 0
	dataSz := uint32(len(data))

	byteSectionBoundaries := make([]uint32, 0, 2*len(byteSections))
	for _, s := range byteSections {
		byteSectionBoundaries = append(byteSectionBoundaries, s.Start, s.End)
	}
	var runeSectionBoundaries []uint32

	endRune := s.runeCount
	for ; len(data) > 0; runeIndex++ {
		// ASCII fast path: avoid utf8.DecodeRune call overhead.
		// For source code, 95-99% of bytes are ASCII.
		var c rune
		sz := 1
		if data[0] < utf8.RuneSelf {
			c = rune(data[0])
		} else {
			c, sz = utf8.DecodeRune(data)
			s.isPlainASCII = false
		}
		data = data[sz:]

		runeGram[0], runeGram[1], runeGram[2] = runeGram[1], runeGram[2], c

		if idx := s.runeCount + runeIndex; idx%runeOffsetFrequency == 0 {
			s.runeOffsets = append(s.runeOffsets, s.endByte+uint32(byteCount))
		}
		for len(byteSectionBoundaries) > 0 && byteSectionBoundaries[0] == uint32(byteCount) {
			runeSectionBoundaries = append(runeSectionBoundaries,
				endRune+uint32(runeIndex))
			byteSectionBoundaries = byteSectionBoundaries[1:]
		}

		byteCount += sz

		if runeIndex < 2 {
			continue
		}

		newOff := endRune + uint32(runeIndex) - 2

		// ASCII trigrams use direct-indexed array (no hash/probe).
		var pl *postingList
		if runeGram[0] < utf8.RuneSelf && runeGram[1] < utf8.RuneSelf && runeGram[2] < utf8.RuneSelf {
			idx := asciiNgramIndex(byte(runeGram[0]), byte(runeGram[1]), byte(runeGram[2]))
			pl = s.asciiPostings[idx]
			if pl == nil {
				pl = &postingList{data: make([]byte, 0, initialPostingCap)}
				s.asciiPostings[idx] = pl
			}
		} else {
			ng := runesToNGram(runeGram)
			pl = s.postings[ng]
			if pl == nil {
				pl = &postingList{data: make([]byte, 0, initialPostingCap)}
				s.postings[ng] = pl
			}
		}
		m := binary.PutUvarint(buf[:], uint64(newOff-pl.lastOff))
		pl.data = append(pl.data, buf[:m]...)
		pl.lastOff = newOff
	}
	s.runeCount += runeIndex

	for len(byteSectionBoundaries) > 0 && byteSectionBoundaries[0] < uint32(byteCount) {
		return nil, nil, fmt.Errorf("no rune for section boundary at byte %d", byteSectionBoundaries[0])
	}

	// Handle symbol definition that ends at file end. This can
	// happen for labels at the end of .bat files.

	for len(byteSectionBoundaries) > 0 && byteSectionBoundaries[0] == uint32(byteCount) {
		runeSectionBoundaries = append(runeSectionBoundaries,
			endRune+runeIndex)
		byteSectionBoundaries = byteSectionBoundaries[1:]
	}
	runeSecs := make([]DocumentSection, 0, len(byteSections))
	for i := 0; i < len(runeSectionBoundaries); i += 2 {
		runeSecs = append(runeSecs, DocumentSection{
			Start: runeSectionBoundaries[i],
			End:   runeSectionBoundaries[i+1],
		})
	}

	s.endRunes = append(s.endRunes, s.runeCount)
	s.endByte += dataSz
	return &dest, runeSecs, nil
}

// ShardBuilder builds a single index shard.
type ShardBuilder struct {
	// The version we will write to disk. Sourcegraph Specific. This is to
	// enable feature flagging new format versions.
	indexFormatVersion int
	featureVersion     int

	contentStrings  []*searchableString
	nameStrings     []*searchableString
	docSections     [][]DocumentSection
	runeDocSections []DocumentSection

	symID        uint32
	symIndex     map[string]uint32
	symKindID    uint32
	symKindIndex map[string]uint32
	symMetaData  []uint32

	fileEndSymbol []uint32

	checksums []byte

	branchMasks []uint64
	subRepos    []uint32

	// docID => repoID
	repos []uint16

	contentPostings *postingsBuilder
	namePostings    *postingsBuilder

	// root repositories
	repoList []zoekt.Repository

	// name to index.
	subRepoIndices []map[string]uint32

	// language => language code
	languageMap map[string]uint16

	// language codes, uint16 encoded as little-endian
	languages []uint8

	categories []byte

	// IndexTime will be used as the time if non-zero. Otherwise
	// time.Now(). This is useful for doing reproducible builds in tests.
	IndexTime time.Time

	// a sortable 20 chars long id.
	ID string
}

func verify(repo *zoekt.Repository) error {
	for _, t := range []string{repo.FileURLTemplate, repo.LineFragmentTemplate, repo.CommitURLTemplate} {
		if _, err := ParseTemplate(t); err != nil {
			return err
		}
	}
	return nil
}

func urlJoinPath(base string, elem ...string) string {
	// golangs html/template always escapes "+" appearing in an HTML attribute
	// [1]. We may even want to treat more characters, differently but this
	// atleast makes it possible to visit URLs like [2].
	//
	// We only do this to elem since base will normally be a hardcoded string.
	//
	// [1]: https://sourcegraph.com/github.com/golang/go@go1.23.2/-/blob/src/html/template/html.go?L71-80
	// [2]: https://github.com/apple/swift-system/blob/main/Sources/System/Util+StringArray.swift
	elem = slices.Clone(elem) // copy to mutate
	for i := range elem {
		elem[i] = strings.ReplaceAll(elem[i], "+", "%2B")
	}
	u, err := url.JoinPath(base, elem...)
	if err != nil {
		return "#!error: " + err.Error()
	}
	return u
}

// ParseTemplate will parse the templates for FileURLTemplate,
// LineFragmentTemplate and CommitURLTemplate.
//
// It makes available the extra function UrlJoinPath.
func ParseTemplate(text string) (*template.Template, error) {
	return template.New("").Funcs(template.FuncMap{
		"URLJoinPath": urlJoinPath,
	}).Parse(text)
}

// ContentSize returns the number of content bytes so far ingested.
func (b *ShardBuilder) ContentSize() uint32 {
	// Add the name too so we don't skip building index if we have
	// lots of empty files.
	return b.contentPostings.endByte + b.namePostings.endByte
}

// NumFiles returns the number of files added to this builder
func (b *ShardBuilder) NumFiles() int {
	return len(b.contentStrings)
}

// NewShardBuilder creates a fresh ShardBuilder. The passed in
// Repository contains repo metadata, and may be set to nil.
func NewShardBuilder(r *zoekt.Repository) (*ShardBuilder, error) {
	b := newShardBuilder(0)

	if r == nil {
		r = &zoekt.Repository{}
	}
	if err := b.setRepository(r); err != nil {
		return nil, err
	}
	return b, nil
}

const defaultShardMax = 100 << 20 // 100 MB, matches Options.ShardMax default

// newShardBuilder creates a ShardBuilder with fresh postingsBuilders.
// shardMax is the maximum shard content size in bytes (0 uses defaultShardMax).
func newShardBuilder(shardMax int) *ShardBuilder {
	if shardMax <= 0 {
		shardMax = defaultShardMax
	}
	return newShardBuilderWithPostings(
		newPostingsBuilder(shardMax),
		newPostingsBuilder(shardMax),
	)
}

func newShardBuilderWithPostings(content, name *postingsBuilder) *ShardBuilder {
	return &ShardBuilder{
		indexFormatVersion: IndexFormatVersion,
		featureVersion:     FeatureVersion,

		contentPostings: content,
		namePostings:    name,
		fileEndSymbol:   []uint32{0},
		symIndex:        make(map[string]uint32),
		symKindIndex:    make(map[string]uint32),
		languageMap:     make(map[string]uint16),
	}
}

func (b *ShardBuilder) setRepository(desc *zoekt.Repository) error {
	if err := verify(desc); err != nil {
		return err
	}

	if len(desc.Branches) > 64 {
		return fmt.Errorf("too many branches")
	}

	repo := *desc

	// copy subrepomap without root
	repo.SubRepoMap = map[string]*zoekt.Repository{}
	for k, v := range desc.SubRepoMap {
		if k != "" {
			repo.SubRepoMap[k] = v
		}
	}

	b.repoList = append(b.repoList, repo)

	return b.populateSubRepoIndices()
}

type symbolSlice struct {
	symbols  []DocumentSection
	metaData []*zoekt.Symbol
}

func (s symbolSlice) Len() int { return len(s.symbols) }

func (s symbolSlice) Swap(i, j int) {
	s.symbols[i], s.symbols[j] = s.symbols[j], s.symbols[i]
	s.metaData[i], s.metaData[j] = s.metaData[j], s.metaData[i]
}

func (s symbolSlice) Less(i, j int) bool {
	return s.symbols[i].Start < s.symbols[j].Start
}

// AddFile is a convenience wrapper for Add
func (b *ShardBuilder) AddFile(name string, content []byte) error {
	return b.Add(Document{Name: name, Content: content})
}

func (b *ShardBuilder) populateSubRepoIndices() error {
	if len(b.subRepoIndices) == len(b.repoList) {
		return nil
	}
	if len(b.subRepoIndices) != len(b.repoList)-1 {
		return fmt.Errorf("populateSubRepoIndices not called for a repo: %d != %d - 1", len(b.subRepoIndices), len(b.repoList))
	}
	repo := b.repoList[len(b.repoList)-1]
	b.subRepoIndices = append(b.subRepoIndices, mkSubRepoIndices(repo))
	return nil
}

func mkSubRepoIndices(repo zoekt.Repository) map[string]uint32 {
	paths := []string{""}
	for k := range repo.SubRepoMap {
		paths = append(paths, k)
	}
	sort.Strings(paths)
	subRepoIndices := make(map[string]uint32, len(paths))
	for i, p := range paths {
		subRepoIndices[p] = uint32(i)
	}
	return subRepoIndices
}

const notIndexedMarker = "NOT-INDEXED: "

func (b *ShardBuilder) symbolID(sym string) uint32 {
	if _, ok := b.symIndex[sym]; !ok {
		b.symIndex[sym] = b.symID
		b.symID++
	}
	return b.symIndex[sym]
}

func (b *ShardBuilder) symbolKindID(t string) uint32 {
	if _, ok := b.symKindIndex[t]; !ok {
		b.symKindIndex[t] = b.symKindID
		b.symKindID++
	}
	return b.symKindIndex[t]
}

func (b *ShardBuilder) addSymbols(symbols []*zoekt.Symbol) {
	for _, sym := range symbols {
		b.symMetaData = append(b.symMetaData,
			// This field was removed due to redundancy. To avoid
			// needing to reindex, it is set to zero for now. In the
			// future, this field will be completely removed. It
			// will require incrementing the feature version.
			0,
			b.symbolKindID(sym.Kind),
			b.symbolID(sym.Parent),
			b.symbolKindID(sym.ParentKind))
	}
}

func DetermineLanguageIfUnknown(doc *Document) {
	if doc.Language != "" {
		return
	}

	// If this document has been skipped (doc.SkipReason != SkipReasonNone), it's
	// likely very large, or it's a non-code file like binary. In this case, we just
	// guess the language based on the file name to avoid examining the contents.
	// Note: passing nil content is allowed by the go-enry contract (the underlying
	// library we use here).
	var content []byte
	if doc.SkipReason == SkipReasonNone {
		content = doc.Content
	}
	langs := languages.GetLanguagesFromContent(doc.Name, content)
	if len(langs) > 0 {
		doc.Language = langs[0]
	}
}

// Add a file which only occurs in certain branches.
func (b *ShardBuilder) Add(doc Document) error {
	if index := bytes.IndexByte(doc.Content, 0); index > 0 {
		doc.SkipReason = SkipReasonBinary
	}

	if doc.SkipReason != SkipReasonNone {
		doc.Content = []byte(notIndexedMarker + doc.SkipReason.explanation())
		doc.Symbols = nil
		doc.SymbolsMetaData = nil
	}

	DetermineLanguageIfUnknown(&doc)
	DetermineFileCategory(&doc)

	sort.Sort(symbolSlice{doc.Symbols, doc.SymbolsMetaData})
	var last DocumentSection
	for i, s := range doc.Symbols {
		if i > 0 {
			if last.End > s.Start {
				return fmt.Errorf("sections overlap")
			}
		}
		last = s
	}
	if last.End > uint32(len(doc.Content)) {
		return fmt.Errorf("section goes past end of content")
	}

	if doc.SubRepositoryPath != "" {
		rel, err := filepath.Rel(doc.SubRepositoryPath, doc.Name)
		if err != nil || rel == doc.Name {
			return fmt.Errorf("path %q must start subrepo path %q", doc.Name, doc.SubRepositoryPath)
		}
	}
	docStr, runeSecs, err := b.contentPostings.newSearchableString(doc.Content, doc.Symbols)
	if err != nil {
		return err
	}
	nameStr, _, err := b.namePostings.newSearchableString([]byte(doc.Name), nil)
	if err != nil {
		return err
	}
	b.addSymbols(doc.SymbolsMetaData)

	repoIdx := len(b.repoList) - 1
	subRepoIdx, ok := b.subRepoIndices[repoIdx][doc.SubRepositoryPath]
	if !ok {
		return fmt.Errorf("unknown subrepo path %q", doc.SubRepositoryPath)
	}

	var mask uint64
	for _, br := range doc.Branches {
		m := b.branchMask(br)
		if m == 0 {
			return fmt.Errorf("no branch found for %s", br)
		}
		mask |= m
	}

	if repoIdx > 1<<16 {
		return fmt.Errorf("too many repos in shard: max is %d", 1<<16)
	}

	b.subRepos = append(b.subRepos, subRepoIdx)
	b.repos = append(b.repos, uint16(repoIdx))

	hasher := crc64.New(crc64.MakeTable(crc64.ISO))
	hasher.Write(doc.Content)

	b.contentStrings = append(b.contentStrings, docStr)
	b.runeDocSections = append(b.runeDocSections, runeSecs...)

	b.nameStrings = append(b.nameStrings, nameStr)
	b.docSections = append(b.docSections, doc.Symbols)
	b.fileEndSymbol = append(b.fileEndSymbol, uint32(len(b.runeDocSections)))
	b.branchMasks = append(b.branchMasks, mask)
	b.checksums = append(b.checksums, hasher.Sum(nil)...)

	langCode, ok := b.languageMap[doc.Language]
	if !ok {
		if len(b.languageMap) >= 65535 {
			return fmt.Errorf("too many languages")
		}
		langCode = uint16(len(b.languageMap))
		b.languageMap[doc.Language] = langCode
	}
	b.languages = append(b.languages, uint8(langCode), uint8(langCode>>8))

	category, err := doc.Category.encode()
	if err != nil {
		return err
	}
	b.categories = append(b.categories, category)

	return nil
}

func (b *ShardBuilder) branchMask(br string) uint64 {
	for i, b := range b.repoList[len(b.repoList)-1].Branches {
		if b.Name == br {
			return uint64(1) << uint(i)
		}
	}
	return 0
}

// repoIDs returns a list of sourcegraph IDs for the indexed repos. If the ID
// is missing or there are no repos, this returns false.
func (b *ShardBuilder) repoIDs() ([]uint32, bool) {
	if len(b.repoList) == 0 {
		return nil, false
	}

	ids := make([]uint32, 0, len(b.repoList))
	for _, repo := range b.repoList {
		if repo.ID == 0 {
			return nil, false
		}
		ids = append(ids, repo.ID)
	}
	return ids, true
}

type DocChecker struct {
	// A map to count the unique trigrams in a doc. Reused across docs to cut down on allocations.
	trigrams map[ngram]struct{}
}

// Check returns a reason why the given contents are probably not source texts.
func (t *DocChecker) Check(content []byte, maxTrigramCount int, allowLargeFile bool) SkipReason {
	if len(content) == 0 {
		return SkipReasonNone
	}

	if len(content) < ngramSize {
		return SkipReasonTooSmall
	}

	if index := bytes.IndexByte(content, 0); index > 0 {
		return SkipReasonBinary
	}

	// PERF: we only need to do the trigram check if the upperbound on content is greater than
	// our threshold. Also skip the trigram check if the file is explicitly marked as allowed.
	if trigramsUpperBound := len(content) - ngramSize + 1; trigramsUpperBound <= maxTrigramCount || allowLargeFile {
		return SkipReasonNone
	}

	var cur [3]rune
	byteCount := 0
	t.clearTrigrams(maxTrigramCount)

	for len(content) > 0 {
		r, sz := utf8.DecodeRune(content)
		content = content[sz:]
		byteCount += sz

		cur[0], cur[1], cur[2] = cur[1], cur[2], r
		if cur[0] == 0 {
			// start of file.
			continue
		}

		t.trigrams[runesToNGram(cur)] = struct{}{}
		if len(t.trigrams) > maxTrigramCount {
			// probably not text.
			return SkipReasonTooManyTrigrams
		}
	}
	return SkipReasonNone
}

func (t *DocChecker) clearTrigrams(maxTrigramCount int) {
	if t.trigrams == nil {
		t.trigrams = make(map[ngram]struct{}, maxTrigramCount)
	}
	for key := range t.trigrams {
		delete(t.trigrams, key)
	}
}

// shardName returns the name of the shard for the given prefix, version, and
// shard number.
func shardName(indexDir string, prefix string, version, n int) string {
	prefix = url.QueryEscape(prefix)
	if len(prefix) > 200 {
		prefix = prefix[:200] + hashString(prefix)[:8]
	}
	return filepath.Join(indexDir, fmt.Sprintf("%s_v%d.%05d.zoekt", prefix, version, n))
}
