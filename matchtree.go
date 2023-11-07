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
	"bytes"
	"fmt"
	"log"
	"regexp/syntax"
	"strings"
	"unicode/utf8"

	"github.com/grafana/regexp"

	"github.com/sourcegraph/zoekt/query"
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

const (
	costConst   = 0
	costMemory  = 1
	costContent = 2
	costRegexp  = 3
)

const (
	costMin = costConst
	costMax = costRegexp
)

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
// The general process for a given (shard, query) is:
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
type matchTree interface {
	docIterator

	// returns whether this matches, and if we are sure.
	matches(cp *contentProvider, cost int, known map[matchTree]bool) (match bool, sure bool)
}

// docMatchTree iterates over documents for which predicate(docID) returns true.
type docMatchTree struct {
	// the number of documents in a shard.
	numDocs uint32

	predicate func(docID uint32) bool

	// provides additional information about the reason why the docMatchTree was
	// created.
	reason string

	// mutable
	firstDone bool
	docID     uint32
}

type bruteForceMatchTree struct {
	// mutable
	firstDone bool
	docID     uint32
}

type andLineMatchTree struct {
	andMatchTree
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

// Returns only the filename of child matches.
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

// \bLITERAL\b
type wordMatchTree struct {
	word string

	fileName bool

	// mutable
	evaluated bool
	found     []*candidateMatch

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
	masks     []uint64
	repos     []uint16

	// mutable
	firstDone bool
	docID     uint32
}

func (t *branchQueryMatchTree) branchMask() uint64 {
	return t.fileMasks[t.docID] & t.masks[t.repos[t.docID]]
}

type symbolRegexpMatchTree struct {
	matchTree
	regexp *regexp.Regexp
	all    bool // skips regex match if .*

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
		var idx []int
		if t.all {
			idx = []int{0, int(sec.End - sec.Start)}
		} else {
			idx = t.regexp.FindIndex(content[sec.Start:sec.End])
			if idx == nil {
				continue
			}
		}

		cm := &candidateMatch{
			byteOffset:  sec.Start + uint32(idx[0]),
			byteMatchSz: uint32(idx[1] - idx[0]),
			symbol:      true,
			symbolIdx:   uint32(i),
		}
		found = append(found, cm)
	}
	t.found = found
	t.reEvaluated = true

	return len(t.found) > 0, true
}

type symbolSubstrMatchTree struct {
	*substrMatchTree

	exact         bool
	patternSize   uint32
	fileEndRunes  []uint32
	fileEndSymbol []uint32

	doc      uint32
	sections []DocumentSection

	secID uint32
}

func (t *symbolSubstrMatchTree) prepare(doc uint32) {
	t.substrMatchTree.prepare(doc)
	t.doc = doc

	var fileStart uint32
	if doc > 0 {
		fileStart = t.fileEndRunes[doc-1]
	}

	var sections []DocumentSection
	if len(t.sections) > 0 {
		most := t.fileEndSymbol[len(t.fileEndSymbol)-1]
		if most == uint32(len(t.sections)) {
			sections = t.sections[t.fileEndSymbol[doc]:t.fileEndSymbol[doc+1]]
		} else {
			for t.secID < uint32(len(t.sections)) && t.sections[t.secID].Start < fileStart {
				t.secID++
			}

			fileEnd, symbolEnd := t.fileEndRunes[doc], t.secID
			for symbolEnd < uint32(len(t.sections)) && t.sections[symbolEnd].Start < fileEnd {
				symbolEnd++
			}

			sections = t.sections[t.secID:symbolEnd]
		}
	}

	secIdx := 0
	trimmed := t.current[:0]
	for len(sections) > secIdx && len(t.current) > 0 {
		start := fileStart + t.current[0].runeOffset
		end := start + t.patternSize
		if start >= sections[secIdx].End {
			secIdx++
			continue
		}

		if start < sections[secIdx].Start {
			t.current = t.current[1:]
			continue
		}

		if end > sections[secIdx].End {
			t.current = t.current[1:]
			continue
		}

		if t.exact && !(start == sections[secIdx].Start && end == sections[secIdx].End) {
			t.current = t.current[1:]
			continue
		}

		t.current[0].symbol = true
		t.current[0].symbolIdx = uint32(secIdx)
		trimmed = append(trimmed, t.current[0])
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
	t.docID = doc
	t.firstDone = true
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

func (t *wordMatchTree) prepare(doc uint32) {
	t.found = t.found[:0]
	t.evaluated = false
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
	var start uint32
	if t.firstDone {
		start = t.docID + 1
	}
	for i := start; i < t.numDocs; i++ {
		if t.predicate(i) {
			return i
		}
	}
	return maxUInt32
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
		if (t.masks[t.repos[i]] & t.fileMasks[i]) != 0 {
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
	return fmt.Sprintf("doc(%s)", t.reason)
}

func (t *andMatchTree) String() string {
	return fmt.Sprintf("and%v", t.children)
}

func (t *regexpMatchTree) String() string {
	f := ""
	if t.fileName {
		f = "f"
	}
	return fmt.Sprintf("%sre(%s)", f, t.regexp)
}

func (t *wordMatchTree) String() string {
	f := ""
	if t.fileName {
		f = "f"
	}
	return fmt.Sprintf("%sword(%s)", f, t.word)
}

func (t *orMatchTree) String() string {
	return fmt.Sprintf("or%v", t.children)
}

func (t *notMatchTree) String() string {
	return fmt.Sprintf("not(%v)", t.child)
}

func (t *noVisitMatchTree) String() string {
	return fmt.Sprintf("novisit(%v)", t.matchTree)
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
	return fmt.Sprintf("branch(%x)", t.masks)
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
	case *andLineMatchTree:
		visitMatchTree(&s.andMatchTree, f)
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

// updateMatchTreeStats calls updateStats on all atoms in mt which have that
// function defined.
func updateMatchTreeStats(mt matchTree, stats *Stats) {
	visitMatchTree(mt, func(mt matchTree) {
		if atom, ok := mt.(interface{ updateStats(*Stats) }); ok {
			atom.updateStats(stats)
		}
	})
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
	case *andLineMatchTree:
		visitMatches(&s.andMatchTree, known, f)
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
	return t.predicate(cp.idx), true
}

func (t *bruteForceMatchTree) matches(cp *contentProvider, cost int, known map[matchTree]bool) (bool, bool) {
	return true, true
}

// andLineMatchTree is a performance optimization of andMatchTree. For content
// searches we don't want to run the regex engine if there is no line that
// contains matches from all terms.
func (t *andLineMatchTree) matches(cp *contentProvider, cost int, known map[matchTree]bool) (bool, bool) {
	matches, sure := t.andMatchTree.matches(cp, cost, known)
	if !(sure && matches) {
		return matches, sure
	}

	// find child with fewest candidates
	min := maxUInt32
	fewestChildren := 0
	for ix, child := range t.children {
		v, ok := child.(*substrMatchTree)
		// make sure we are running a content search and that all candidates are a
		// substrMatchTree
		if !ok || v.fileName {
			return matches, sure
		}
		if len(v.current) < min {
			min = len(v.current)
			fewestChildren = ix
		}
	}

	type lineRange struct {
		start int
		end   int
	}
	lines := make([]lineRange, 0, len(t.children[fewestChildren].(*substrMatchTree).current))
	prev := -1
	for _, candidate := range t.children[fewestChildren].(*substrMatchTree).current {
		line, byteStart, byteEnd := cp.newlines().atOffset(candidate.byteOffset)
		if line == prev {
			continue
		}
		prev = line
		lines = append(lines, lineRange{byteStart, byteEnd})
	}

	// children keeps track of the children's candidates we have already seen.
	children := make([][]*candidateMatch, 0, len(t.children)-1)
	for j, child := range t.children {
		if j == fewestChildren {
			continue
		}
		children = append(children, child.(*substrMatchTree).current)
	}

nextLine:
	for i := 0; i < len(lines); i++ {
		hits := 1
	nextChild:
		for j := range children {
		nextCandidate:
			for len(children[j]) > 0 {
				candidate := children[j][0]
				bo := int(cp.findOffset(false, candidate.runeOffset))
				if bo < lines[i].start {
					children[j] = children[j][1:]
					continue nextCandidate
				}
				if bo <= lines[i].end {
					hits++
					continue nextChild
				}
				// move the `lines` iterator forward until bo <= line.end
				for i < len(lines) && bo > lines[i].end {
					i++
				}
				i--
				continue nextLine
			}
		}
		// return early once we found any line that contains matches from all children
		if hits == len(t.children) {
			return matches, true
		}
	}
	return false, true
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
	return t.branchMask() != 0, true
}

func (t *regexpMatchTree) matches(cp *contentProvider, cost int, known map[matchTree]bool) (bool, bool) {
	if t.reEvaluated {
		return len(t.found) > 0, true
	}

	if cost < costRegexp {
		return false, false
	}

	cp.stats.RegexpsConsidered++
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

func (t *wordMatchTree) matches(cp *contentProvider, cost int, known map[matchTree]bool) (bool, bool) {
	if t.evaluated {
		return len(t.found) > 0, true
	}

	if cost < costRegexp {
		return false, false
	}

	data := cp.data(t.fileName)
	offset := 0
	found := t.found[:0]
	for {
		idx := bytes.Index(data[offset:], []byte(t.word))
		if idx < 0 {
			break
		}

		relStartOffset := offset + idx
		relEndOffset := relStartOffset + len(t.word)

		startBoundary := relStartOffset < len(data) && (relStartOffset == 0 || !characterClass(data[relStartOffset-1]))
		endBoundary := relEndOffset > 0 && (relEndOffset == len(data) || !characterClass(data[relEndOffset]))
		if startBoundary && endBoundary {
			found = append(found, &candidateMatch{
				byteOffset:  uint32(offset + idx),
				byteMatchSz: uint32(len(t.word)),
				fileName:    t.fileName,
			})
		}
		offset += idx + len(t.word)
	}

	t.found = found
	t.evaluated = true

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

type matchTreeOpt struct {
	// DisableWordMatchOptimization is used to disable the use of wordMatchTree.
	// This was added since we do not support wordMatchTree with symbol search.
	DisableWordMatchOptimization bool
}

func (d *indexData) newMatchTree(q query.Q, opt matchTreeOpt) (matchTree, error) {
	if q == nil {
		return nil, fmt.Errorf("got nil (sub)query")
	}
	switch s := q.(type) {
	case *query.Regexp:
		// RegexpToMatchTreeRecursive tries to distill a matchTree that matches a
		// superset of the regexp. If the returned matchTree is equivalent to the
		// original regexp, it returns true. An equivalent matchTree has the same
		// behaviour as the original regexp and can be used instead.
		//
		subMT, isEq, _, err := d.regexpToMatchTreeRecursive(s.Regexp, ngramSize, s.FileName, s.CaseSensitive)
		if err != nil {
			return nil, err
		}
		// if the query can be used in place of the regexp
		// return the subtree
		if isEq {
			return subMT, nil
		}

		var tr matchTree
		if wmt, ok := regexpToWordMatchTree(s, opt); ok {
			// A common search we get is "\bLITERAL\b". Avoid the regex engine and
			// provide something faster.
			tr = wmt
		} else {
			prefix := ""
			if !s.CaseSensitive {
				prefix = "(?i)"
			}

			tr = &regexpMatchTree{
				regexp:   regexp.MustCompile(prefix + s.Regexp.String()),
				fileName: s.FileName,
			}
		}

		return &andMatchTree{
			children: []matchTree{
				tr, &noVisitMatchTree{subMT},
			},
		}, nil
	case *query.And:
		var r []matchTree
		for _, ch := range s.Children {
			ct, err := d.newMatchTree(ch, opt)
			if err != nil {
				return nil, err
			}
			r = append(r, ct)
		}
		return &andMatchTree{r}, nil
	case *query.Or:
		var r []matchTree
		for _, ch := range s.Children {
			ct, err := d.newMatchTree(ch, opt)
			if err != nil {
				return nil, err
			}
			r = append(r, ct)
		}
		return &orMatchTree{r}, nil
	case *query.Not:
		ct, err := d.newMatchTree(s.Child, opt)
		return &notMatchTree{
			child: ct,
		}, err

	case *query.Type:
		if s.Type != query.TypeFileName {
			break
		}

		ct, err := d.newMatchTree(s.Child, opt)
		if err != nil {
			return nil, err
		}

		return &fileNameMatchTree{
			child: ct,
		}, nil

	case *query.Substring:
		return d.newSubstringMatchTree(s)

	case *query.Branch:
		masks := make([]uint64, 0, len(d.repoMetaData))
		if s.Pattern == "HEAD" {
			for i := 0; i < len(d.repoMetaData); i++ {
				masks = append(masks, 1)
			}
		} else {
			for _, branchIDs := range d.branchIDs {
				mask := uint64(0)
				for nm, m := range branchIDs {
					if (s.Exact && nm == s.Pattern) || (!s.Exact && strings.Contains(nm, s.Pattern)) {
						mask |= uint64(m)
					}
				}
				masks = append(masks, mask)
			}

		}
		return &branchQueryMatchTree{
			masks:     masks,
			fileMasks: d.fileBranchMasks,
			repos:     d.repos,
		}, nil
	case *query.Const:
		if s.Value {
			return &bruteForceMatchTree{}, nil
		} else {
			return &noMatchTree{Why: "const"}, nil
		}
	case *query.Language:
		code, ok := d.metaData.LanguageMap[s.Language]
		if !ok {
			return &noMatchTree{Why: "lang"}, nil
		}
		return &docMatchTree{
			reason:  "language",
			numDocs: d.numDocs(),
			predicate: func(docID uint32) bool {
				return d.getLanguage(docID) == code
			},
		}, nil

	case *query.Symbol:
		// Disable WordMatchTree since we don't support it in symbols yet.
		optCopy := opt
		optCopy.DisableWordMatchOptimization = true

		expr, wasAnchored := s.Expr, false
		if regexpExpr, ok := expr.(*query.Regexp); ok {
			expr, wasAnchored = stripAnchors(regexpExpr)
		}

		subMT, err := d.newMatchTree(expr, optCopy)
		if err != nil {
			return nil, err
		}

		if substr, ok := subMT.(*substrMatchTree); ok {
			return &symbolSubstrMatchTree{
				substrMatchTree: substr,
				exact:           wasAnchored,
				patternSize:     uint32(utf8.RuneCountInString(substr.query.Pattern)),
				fileEndRunes:    d.fileEndRunes,
				fileEndSymbol:   d.fileEndSymbol,
				sections:        d.runeDocSections,
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
			all:       regexp.String() == "(?i)(?-s:.)*",
			matchTree: subMT,
		}, nil

	case *query.FileNameSet:
		return &docMatchTree{
			reason:  "FileNameSet",
			numDocs: d.numDocs(),
			predicate: func(docID uint32) bool {
				fileName := d.fileName(docID)
				_, ok := s.Set[string(fileName)]
				return ok
			},
		}, nil

	case *query.BranchesRepos:
		reposBranchesWant := make([]uint64, len(d.repoMetaData))
		for repoIdx := range d.repoMetaData {
			var mask uint64
			for _, br := range s.List {
				if br.Repos.Contains(d.repoMetaData[repoIdx].ID) {
					mask |= uint64(d.branchIDs[repoIdx][br.Branch])
				}
			}
			reposBranchesWant[repoIdx] = mask
		}
		return &docMatchTree{
			reason:  "BranchesRepos",
			numDocs: d.numDocs(),
			predicate: func(docID uint32) bool {
				return d.fileBranchMasks[docID]&reposBranchesWant[d.repos[docID]] != 0
			},
		}, nil

	case *query.RepoSet:
		reposWant := make([]bool, len(d.repoMetaData))
		for repoIdx, r := range d.repoMetaData {
			if _, ok := s.Set[r.Name]; ok {
				reposWant[repoIdx] = true
			}
		}
		return &docMatchTree{
			reason:  "RepoSet",
			numDocs: d.numDocs(),
			predicate: func(docID uint32) bool {
				return reposWant[d.repos[docID]]
			},
		}, nil

	case *query.RepoIDs:
		reposWant := make([]bool, len(d.repoMetaData))
		for repoIdx, r := range d.repoMetaData {
			if s.Repos.Contains(r.ID) {
				reposWant[repoIdx] = true
			}
		}
		return &docMatchTree{
			reason:  "RepoIDs",
			numDocs: d.numDocs(),
			predicate: func(docID uint32) bool {
				return reposWant[d.repos[docID]]
			},
		}, nil

	case *query.Repo:
		reposWant := make([]bool, len(d.repoMetaData))
		for repoIdx, r := range d.repoMetaData {
			if s.Regexp.MatchString(r.Name) {
				reposWant[repoIdx] = true
			}
		}
		return &docMatchTree{
			reason:  "Repo",
			numDocs: d.numDocs(),
			predicate: func(docID uint32) bool {
				return reposWant[d.repos[docID]]
			},
		}, nil

	case *query.RepoRegexp:
		reposWant := make([]bool, len(d.repoMetaData))
		for repoIdx, r := range d.repoMetaData {
			if s.Regexp.MatchString(r.Name) {
				reposWant[repoIdx] = true
			}
		}
		return &docMatchTree{
			reason:  "RepoRegexp",
			numDocs: d.numDocs(),
			predicate: func(docID uint32) bool {
				return reposWant[d.repos[docID]]
			},
		}, nil

	case query.RawConfig:
		return &docMatchTree{
			reason:  s.String(),
			numDocs: d.numDocs(),
			predicate: func(docID uint32) bool {
				return uint8(s)&d.rawConfigMasks[d.repos[docID]] == uint8(s)
			},
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

func regexpToWordMatchTree(q *query.Regexp, opt matchTreeOpt) (_ *wordMatchTree, ok bool) {
	if opt.DisableWordMatchOptimization {
		return nil, false
	}
	// Needs to be case sensitive
	if !q.CaseSensitive || q.Regexp.Flags&syntax.FoldCase != 0 {
		return nil, false
	}
	// We want a regex that looks like Op.Concat[OpWordBoundary OpLiteral OpWordBoundary]
	if q.Regexp.Op != syntax.OpConcat || len(q.Regexp.Sub) != 3 {
		return nil, false
	}
	sub := q.Regexp.Sub
	if sub[0].Op != syntax.OpWordBoundary || sub[1].Op != syntax.OpLiteral || sub[2].Op != syntax.OpWordBoundary {
		return nil, false
	}

	return &wordMatchTree{
		word:     string(sub[1].Rune),
		fileName: q.FileName,
	}, true
}

// pruneMatchTree removes impossible branches from the matchTree, as indicated
// by substrMatchTree having a noMatchTree and the resulting impossible and clauses and so forth.
func pruneMatchTree(mt matchTree) (matchTree, error) {
	var err error
	switch mt := mt.(type) {
	// leaf nodes that we test with the filter:
	case *substrMatchTree:
		if res, ok := mt.matchIterator.(*ngramIterationResults); ok {
			if _, ok := res.matchIterator.(*noMatchTree); ok {
				return nil, nil
			}
		}
	// recursive tree structures:
	case *andMatchTree:
		// Any branch of an and becoming impossible means the entire clause
		// is impossible. Otherwise, just handle rewrites.
		for i, child := range mt.children {
			newChild, err := pruneMatchTree(child)
			if err != nil {
				return nil, err
			}
			if newChild == nil {
				return nil, nil
			}
			mt.children[i] = newChild
		}
	case *orMatchTree:
		// *All* branches of an OR becoming impossible means the entire clause
		// is impossible. Otherwise, drop impossible subclauses and handle
		// rewrites, including simplifying to a singular resulting child branch.
		n := 0
		for _, child := range mt.children {
			newChild, err := pruneMatchTree(child)
			if err != nil {
				return nil, err
			}
			if newChild != nil {
				mt.children[n] = newChild
				n++
			}
		}
		mt.children = mt.children[:n]
		if len(mt.children) == 1 {
			return mt.children[0], nil
		} else if len(mt.children) == 0 {
			return nil, nil
		}
	case *noVisitMatchTree:
		mt.matchTree, err = pruneMatchTree(mt.matchTree)
		if err != nil {
			return nil, err
		}
		if mt.matchTree == nil {
			return nil, nil
		}
	case *fileNameMatchTree:
		mt.child, err = pruneMatchTree(mt.child)
	case *andLineMatchTree:
		child, err := pruneMatchTree(&mt.andMatchTree)
		if err != nil {
			return nil, err
		}
		if child == nil {
			return nil, nil
		}
		if c, ok := child.(*andMatchTree); ok {
			mt.andMatchTree = *c
		} else {
			// the and was simplified to a single clause,
			// so the linematch portion is irrelevant.
			return mt, nil
		}
	case *notMatchTree:
		mt.child, err = pruneMatchTree(mt.child)
		if err != nil {
			return nil, err
		}
		if mt.child == nil {
			// not false => true
			return &bruteForceMatchTree{}, nil
		}
	// unhandled:
	case *docMatchTree:
	case *bruteForceMatchTree:
	case *regexpMatchTree:
	case *wordMatchTree:
	}
	return mt, err
}

func stripAnchors(in *query.Regexp) (out *query.Regexp, stripped bool) {
	stripRegexpAnchors := func(in *syntax.Regexp) (out *syntax.Regexp, stripped bool) {
		if in.Op != syntax.OpConcat {
			return out, false
		}

		if len(in.Sub) < 3 {
			return out, false
		}

		firstOp, lastOp := in.Sub[0].Op, in.Sub[len(in.Sub)-1].Op

		if firstOp != syntax.OpBeginLine && firstOp != syntax.OpBeginText {
			return out, false
		}
		if lastOp != syntax.OpEndLine && lastOp != syntax.OpEndText {
			return out, false
		}

		inCopy := *in
		inCopy.Sub = in.Sub[1 : len(in.Sub)-1] // remove the first and last ops, which are the anchors
		return &inCopy, true
	}

	newRegexp, stripped := stripRegexpAnchors(in.Regexp)
	if !stripped {
		return in, false
	}

	inCopy := *in
	inCopy.Regexp = newRegexp
	return &inCopy, true
}
