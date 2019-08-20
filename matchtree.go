// Copyright 2018 Google Inc. All rights reserved.
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
	"log"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/zoekt/query"
)

// A docIterator iterates over documents in order.
type docIterator interface {
	// provide the next document where we can may find something
	// interesting.
	nextDoc() uint32

	// clears any per-document state of the docIterator, and
	// prepares for evaluating the given doc. The argument is
	// strictly increasing over time.
	prepare(nextDoc uint32)
}

const costConst = 0
const costMemory = 1
const costContent = 2
const costRegexp = 3

const costMin = costConst
const costMax = costRegexp

// An expression tree coupled with matches. The matchtree has two
// functions:
//
// * it implements boolean combinations (and, or, not)
//
// * it implements shortcuts, where we skip documents (for example: if
// there are no trigram matches, we can be sure there are no substring
// matches). The matchtree iterates over the documents as they are
// ordered in the shard.
//
// The general process for a given (shard, query) is
//
// - construct matchTree for the query
//
// - find all different leaf matchTrees (substring, regexp, etc.)
//
// in a loop:
//
//   - find next doc to process using nextDoc
//
//   - evaluate atoms (leaf expressions that match text)
//
//   - evaluate the tree using matches(), storing the result in map.
//
//   - if the complete tree returns (matches() == true) for the document,
//     collect all text matches by looking at leaf matchTrees
//
type matchTree interface {
	docIterator

	// returns whether this matches, and if we are sure.
	matches(cp *contentProvider, cost int, known map[matchTree]bool) (match bool, sure bool)
}

type docMatchTree struct {
	// mutable
	docs    []uint32
	current []uint32
}

type bruteForceMatchTree struct {
	// mutable
	firstDone bool
	docID     uint32
}

type andMatchTree struct {
	children []matchTree
}

type orMatchTree struct {
	children []matchTree
}

type notMatchTree struct {
	child matchTree
}

type fileNameMatchTree struct {
	child matchTree
}

// Don't visit this subtree for collecting matches.
type noVisitMatchTree struct {
	matchTree
}

type regexpMatchTree struct {
	regexp *regexp.Regexp

	fileName bool

	// mutable
	reEvaluated bool
	found       []*candidateMatch

	// nextDoc, prepare.
	bruteForceMatchTree
}

type substrMatchTree struct {
	matchIterator

	query         *query.Substring
	caseSensitive bool
	fileName      bool

	// mutable
	current       []*candidateMatch
	contEvaluated bool
}

type branchQueryMatchTree struct {
	fileMasks []uint64
	mask      uint64

	// mutable
	firstDone bool
	docID     uint32
}

type symbolRegexpMatchTree struct {
	matchTree
	regexp *regexp.Regexp

	reEvaluated bool
	found       []*candidateMatch
}

func (t *symbolRegexpMatchTree) prepare(doc uint32) {
	t.reEvaluated = false
}

func (t *symbolRegexpMatchTree) matches(cp *contentProvider, cost int, known map[matchTree]bool) (bool, bool) {
	if t.reEvaluated {
		return len(t.found) > 0, true
	}

	if cost < costRegexp {
		return false, false
	}

	sections := cp.docSections()
	content := cp.data(false)

	found := t.found[:0]
	for i, sec := range sections {
		idx := t.regexp.FindIndex(content[sec.Start:sec.End])
		if idx == nil {
			continue
		}

		cm := &candidateMatch{
			byteOffset:  sec.Start + uint32(idx[0]),
			byteMatchSz: uint32(idx[1] - idx[0]),
		}
		if cp.idx < uint32(len(cp.id.fileEndSymbol)) { // If v15 fileEndSymbol is empty
			secID := cp.id.fileEndSymbol[cp.idx] + uint32(i)
			cm.symbolInfo = cp.id.symbolData.data(secID)
		}
		found = append(found, cm)
	}
	t.found = found
	t.reEvaluated = true

	return len(t.found) > 0, true
}

type symbolSubstrMatchTree struct {
	*substrMatchTree

	patternSize  uint32
	fileEndRunes []uint32

	doc        uint32
	sections   []DocumentSection
	symbolData *symbolData
	secID      uint32
}

func (t *symbolSubstrMatchTree) prepare(doc uint32) {
	t.substrMatchTree.prepare(doc)
	t.doc = doc

	var fileStart uint32
	if doc > 0 {
		fileStart = t.fileEndRunes[doc-1]
	}

	for len(t.sections) > 0 && t.sections[0].Start < fileStart {
		t.sections = t.sections[1:]
		t.secID++
	}

	// TODO we can use fileEndSymbol to skip
	trimmed := t.current[:0]
	for len(t.sections) > 0 && len(t.current) > 0 {
		start := fileStart + t.current[0].runeOffset
		end := start + t.patternSize
		if start >= t.sections[0].End {
			t.sections = t.sections[1:]
			t.secID++
			continue
		}

		if start < t.sections[0].Start {
			t.current = t.current[1:]
			continue
		}

		if end <= t.sections[0].End {
			t.current[0].symbolInfo = t.symbolData.data(t.secID)
			trimmed = append(trimmed, t.current[0])
		}

		t.current = t.current[1:]
	}
	t.current = trimmed
}

// all prepare methods

func (t *bruteForceMatchTree) prepare(doc uint32) {
	t.docID = doc
	t.firstDone = true
}

func (t *docMatchTree) prepare(doc uint32) {
	for len(t.docs) > 0 && t.docs[0] < doc {
		t.docs = t.docs[1:]
	}
	i := 0
	for ; i < len(t.docs) && t.docs[i] == doc; i++ {
	}

	t.current = t.docs[:i]
	t.docs = t.docs[i:]
}

func (t *andMatchTree) prepare(doc uint32) {
	for _, c := range t.children {
		c.prepare(doc)
	}
}

func (t *regexpMatchTree) prepare(doc uint32) {
	t.found = t.found[:0]
	t.reEvaluated = false
	t.bruteForceMatchTree.prepare(doc)
}

func (t *orMatchTree) prepare(doc uint32) {
	for _, c := range t.children {
		c.prepare(doc)
	}
}

func (t *notMatchTree) prepare(doc uint32) {
	t.child.prepare(doc)
}

func (t *fileNameMatchTree) prepare(doc uint32) {
	t.child.prepare(doc)
}

func (t *substrMatchTree) prepare(nextDoc uint32) {
	t.matchIterator.prepare(nextDoc)
	t.current = t.matchIterator.candidates()
	t.contEvaluated = false
}

func (t *branchQueryMatchTree) prepare(doc uint32) {
	t.firstDone = true
	t.docID = doc
}

// nextDoc

func (t *docMatchTree) nextDoc() uint32 {
	if len(t.docs) == 0 {
		return maxUInt32
	}
	return t.docs[0]
}

func (t *bruteForceMatchTree) nextDoc() uint32 {
	if !t.firstDone {
		return 0
	}
	return t.docID + 1
}

func (t *andMatchTree) nextDoc() uint32 {
	var max uint32
	for _, c := range t.children {
		m := c.nextDoc()
		if m > max {
			max = m
		}
	}
	return max
}

func (t *orMatchTree) nextDoc() uint32 {
	min := uint32(maxUInt32)
	for _, c := range t.children {
		m := c.nextDoc()
		if m < min {
			min = m
		}
	}
	return min
}

func (t *notMatchTree) nextDoc() uint32 {
	return 0
}

func (t *fileNameMatchTree) nextDoc() uint32 {
	return t.child.nextDoc()
}

func (t *branchQueryMatchTree) nextDoc() uint32 {
	var start uint32
	if t.firstDone {
		start = t.docID + 1
	}

	for i := start; i < uint32(len(t.fileMasks)); i++ {
		if (t.mask & t.fileMasks[i]) != 0 {
			return i
		}
	}
	return maxUInt32
}

// all String methods

func (t *bruteForceMatchTree) String() string {
	return "all"
}

func (t *docMatchTree) String() string {
	return fmt.Sprintf("docs%v", t.docs)
}

func (t *andMatchTree) String() string {
	return fmt.Sprintf("and%v", t.children)
}

func (t *regexpMatchTree) String() string {
	return fmt.Sprintf("re(%s)", t.regexp)
}

func (t *orMatchTree) String() string {
	return fmt.Sprintf("or%v", t.children)
}

func (t *notMatchTree) String() string {
	return fmt.Sprintf("not(%v)", t.child)
}

func (t *fileNameMatchTree) String() string {
	return fmt.Sprintf("f(%v)", t.child)
}

func (t *substrMatchTree) String() string {
	f := ""
	if t.fileName {
		f = "f"
	}

	return fmt.Sprintf("%ssubstr(%q, %v, %v)", f, t.query.Pattern, t.current, t.matchIterator)
}

func (t *branchQueryMatchTree) String() string {
	return fmt.Sprintf("branch(%x)", t.mask)
}

func (t *symbolSubstrMatchTree) String() string {
	return fmt.Sprintf("symbol(%v)", t.substrMatchTree)
}

func (t *symbolRegexpMatchTree) String() string {
	return fmt.Sprintf("symbol(%v)", t.matchTree)
}

// visitMatches visits all atoms in matchTree. Note: This visits
// noVisitMatchTree. For collecting matches use visitMatches.
func visitMatchTree(t matchTree, f func(matchTree)) {
	switch s := t.(type) {
	case *andMatchTree:
		for _, ch := range s.children {
			visitMatchTree(ch, f)
		}
	case *orMatchTree:
		for _, ch := range s.children {
			visitMatchTree(ch, f)
		}
	case *noVisitMatchTree:
		visitMatchTree(s.matchTree, f)
	case *notMatchTree:
		visitMatchTree(s.child, f)
	case *fileNameMatchTree:
		visitMatchTree(s.child, f)
	case *symbolSubstrMatchTree:
		visitMatchTree(s.substrMatchTree, f)
	case *symbolRegexpMatchTree:
		visitMatchTree(s.matchTree, f)
	default:
		f(t)
	}
}

// visitMatches visits all atoms which can contribute matches. Note: This
// skips noVisitMatchTree.
func visitMatches(t matchTree, known map[matchTree]bool, f func(matchTree)) {
	switch s := t.(type) {
	case *andMatchTree:
		for _, ch := range s.children {
			if known[ch] {
				visitMatches(ch, known, f)
			}
		}
	case *orMatchTree:
		for _, ch := range s.children {
			if known[ch] {
				visitMatches(ch, known, f)
			}
		}
	case *symbolSubstrMatchTree:
		visitMatches(s.substrMatchTree, known, f)
	case *notMatchTree:
	case *noVisitMatchTree:
		// don't collect into negative trees.
	case *fileNameMatchTree:
		// We will just gather the filename if we do not visit this tree.
	default:
		f(s)
	}
}

// all matches() methods.

func (t *docMatchTree) matches(cp *contentProvider, cost int, known map[matchTree]bool) (bool, bool) {
	return len(t.current) > 0, true
}

func (t *bruteForceMatchTree) matches(cp *contentProvider, cost int, known map[matchTree]bool) (bool, bool) {
	return true, true
}

func (t *andMatchTree) matches(cp *contentProvider, cost int, known map[matchTree]bool) (bool, bool) {
	sure := true

	for _, ch := range t.children {
		v, ok := evalMatchTree(cp, cost, known, ch)
		if ok && !v {
			return false, true
		}
		if !ok {
			sure = false
		}
	}
	return true, sure
}

func (t *orMatchTree) matches(cp *contentProvider, cost int, known map[matchTree]bool) (bool, bool) {
	matches := false
	sure := true
	for _, ch := range t.children {
		v, ok := evalMatchTree(cp, cost, known, ch)
		if ok {
			// we could short-circuit, but we want to use
			// the other possibilities as a ranking
			// signal.
			matches = matches || v
		} else {
			sure = false
		}
	}
	return matches, sure
}

func (t *branchQueryMatchTree) matches(cp *contentProvider, cost int, known map[matchTree]bool) (bool, bool) {
	return t.fileMasks[t.docID]&t.mask != 0, true
}

func (t *regexpMatchTree) matches(cp *contentProvider, cost int, known map[matchTree]bool) (bool, bool) {
	if t.reEvaluated {
		return len(t.found) > 0, true
	}

	if cost < costRegexp {
		return false, false
	}

	idxs := t.regexp.FindAllIndex(cp.data(t.fileName), -1)
	found := t.found[:0]
	for _, idx := range idxs {
		cm := &candidateMatch{
			byteOffset:  uint32(idx[0]),
			byteMatchSz: uint32(idx[1] - idx[0]),
			fileName:    t.fileName,
		}

		found = append(found, cm)
	}
	t.found = found
	t.reEvaluated = true

	return len(t.found) > 0, true
}

// breakMatchesOnNewlines returns matches resulting from breaking each element
// of cms on newlines within text.
func breakMatchesOnNewlines(cms []*candidateMatch, text []byte) []*candidateMatch {
	var lineCMs []*candidateMatch
	for _, cm := range cms {
		lineCMs = append(lineCMs, breakOnNewlines(cm, text)...)
	}
	return lineCMs
}

// breakOnNewlines returns matches resulting from breaking cm on newlines
// within text.
func breakOnNewlines(cm *candidateMatch, text []byte) []*candidateMatch {
	var cms []*candidateMatch
	addMe := &candidateMatch{}
	*addMe = *cm
	for i := uint32(cm.byteOffset); i < cm.byteOffset+cm.byteMatchSz; i++ {
		if text[i] == '\n' {
			addMe.byteMatchSz = i - addMe.byteOffset
			if addMe.byteMatchSz != 0 {
				cms = append(cms, addMe)
			}

			addMe = &candidateMatch{}
			*addMe = *cm
			addMe.byteOffset = i + 1
		}
	}
	addMe.byteMatchSz = cm.byteOffset + cm.byteMatchSz - addMe.byteOffset
	if addMe.byteMatchSz != 0 {
		cms = append(cms, addMe)
	}
	return cms
}

func evalMatchTree(cp *contentProvider, cost int, known map[matchTree]bool, mt matchTree) (bool, bool) {
	if v, ok := known[mt]; ok {
		return v, true
	}

	v, ok := mt.matches(cp, cost, known)
	if ok {
		known[mt] = v
	}

	return v, ok
}

func (t *notMatchTree) matches(cp *contentProvider, cost int, known map[matchTree]bool) (bool, bool) {
	v, ok := evalMatchTree(cp, cost, known, t.child)
	return !v, ok
}

func (t *fileNameMatchTree) matches(cp *contentProvider, cost int, known map[matchTree]bool) (bool, bool) {
	return evalMatchTree(cp, cost, known, t.child)
}

func (t *substrMatchTree) matches(cp *contentProvider, cost int, known map[matchTree]bool) (bool, bool) {
	if t.contEvaluated {
		return len(t.current) > 0, true
	}

	if len(t.current) == 0 {
		return false, true
	}

	if t.fileName && cost < costMemory {
		return false, false
	}

	if !t.fileName && cost < costContent {
		return false, false
	}

	pruned := t.current[:0]
	for _, m := range t.current {
		if m.byteOffset == 0 && m.runeOffset > 0 {
			m.byteOffset = cp.findOffset(m.fileName, m.runeOffset)
		}
		if m.matchContent(cp.data(m.fileName)) {
			pruned = append(pruned, m)
		}
	}
	t.current = pruned
	t.contEvaluated = true

	return len(t.current) > 0, true
}

func (d *indexData) newMatchTree(q query.Q) (matchTree, error) {
	switch s := q.(type) {
	case *query.Regexp:
		subQ := query.RegexpToQuery(s.Regexp, ngramSize)
		subQ = query.Map(subQ, func(q query.Q) query.Q {
			if sub, ok := q.(*query.Substring); ok {
				sub.FileName = s.FileName
				sub.CaseSensitive = s.CaseSensitive
			}
			return q
		})

		subMT, err := d.newMatchTree(subQ)
		if err != nil {
			return nil, err
		}

		prefix := ""
		if !s.CaseSensitive {
			prefix = "(?i)"
		}

		tr := &regexpMatchTree{
			regexp:   regexp.MustCompile(prefix + s.Regexp.String()),
			fileName: s.FileName,
		}

		return &andMatchTree{
			children: []matchTree{
				tr, &noVisitMatchTree{subMT},
			},
		}, nil
	case *query.And:
		var r []matchTree
		for _, ch := range s.Children {
			ct, err := d.newMatchTree(ch)
			if err != nil {
				return nil, err
			}
			r = append(r, ct)
		}
		return &andMatchTree{r}, nil
	case *query.Or:
		var r []matchTree
		for _, ch := range s.Children {
			ct, err := d.newMatchTree(ch)
			if err != nil {
				return nil, err
			}
			r = append(r, ct)
		}
		return &orMatchTree{r}, nil
	case *query.Not:
		ct, err := d.newMatchTree(s.Child)
		return &notMatchTree{
			child: ct,
		}, err

	case *query.Type:
		if s.Type != query.TypeFileName {
			break
		}

		ct, err := d.newMatchTree(s.Child)
		if err != nil {
			return nil, err
		}

		return &fileNameMatchTree{
			child: ct,
		}, nil

	case *query.Substring:
		return d.newSubstringMatchTree(s)

	case *query.Branch:
		mask := uint64(0)
		if s.Pattern == "HEAD" {
			mask = 1
		} else {
			for nm, m := range d.branchIDs {
				if strings.Contains(nm, s.Pattern) {
					mask |= uint64(m)
				}
			}
		}
		return &branchQueryMatchTree{
			mask:      mask,
			fileMasks: d.fileBranchMasks,
		}, nil
	case *query.Const:
		if s.Value {
			return &bruteForceMatchTree{}, nil
		} else {
			return &noMatchTree{"const"}, nil
		}
	case *query.Language:
		code, ok := d.metaData.LanguageMap[s.Language]
		if !ok {
			return &noMatchTree{"lang"}, nil
		}
		docs := make([]uint32, 0, len(d.languages))
		for d, l := range d.languages {
			if l == code {
				docs = append(docs, uint32(d))
			}
		}
		return &docMatchTree{
			docs: docs,
		}, nil

	case *query.Symbol:
		subMT, err := d.newMatchTree(s.Expr)
		if err != nil {
			return nil, err
		}

		if substr, ok := subMT.(*substrMatchTree); ok {
			return &symbolSubstrMatchTree{
				substrMatchTree: substr,
				patternSize:     uint32(utf8.RuneCountInString(substr.query.Pattern)),
				fileEndRunes:    d.fileEndRunes,
				sections:        d.runeDocSections,
				symbolData:      &d.symbolData,
			}, nil
		}

		var regexp *regexp.Regexp
		visitMatchTree(subMT, func(mt matchTree) {
			if t, ok := mt.(*regexpMatchTree); ok {
				regexp = t.regexp
			}
		})
		if regexp == nil {
			return nil, fmt.Errorf("found %T inside query.Symbol", subMT)
		}

		return &symbolRegexpMatchTree{
			regexp:    regexp,
			matchTree: subMT,
		}, nil
	}
	log.Panicf("type %T", q)
	return nil, nil
}

func (d *indexData) newSubstringMatchTree(s *query.Substring) (matchTree, error) {
	st := &substrMatchTree{
		query:         s,
		caseSensitive: s.CaseSensitive,
		fileName:      s.FileName,
	}

	if utf8.RuneCountInString(s.Pattern) < ngramSize {
		prefix := ""
		if !s.CaseSensitive {
			prefix = "(?i)"
		}
		t := &regexpMatchTree{
			regexp:   regexp.MustCompile(prefix + regexp.QuoteMeta(s.Pattern)),
			fileName: s.FileName,
		}
		return t, nil
	}

	result, err := d.iterateNgrams(s)
	if err != nil {
		return nil, err
	}
	st.matchIterator = result
	return st, nil
}
