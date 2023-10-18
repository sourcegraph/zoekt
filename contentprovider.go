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
	"bytes"
	"fmt"
	"log"
	"sort"
	"strings"
	"unicode/utf8"
)

var _ = log.Println

// contentProvider is an abstraction to treat matches for names and
// content with the same code.
type contentProvider struct {
	id    *indexData
	stats *Stats

	// mutable
	err      error
	idx      uint32
	_data    []byte
	_nl      []uint32
	_nlBuf   []uint32
	_sects   []DocumentSection
	_sectBuf []DocumentSection
	fileSize uint32
}

// setDocument skips to the given document.
func (p *contentProvider) setDocument(docID uint32) {
	fileStart := p.id.boundaries[docID]

	p.idx = docID
	p.fileSize = p.id.boundaries[docID+1] - fileStart

	p._nl = nil
	p._sects = nil
	p._data = nil
}

func (p *contentProvider) docSections() []DocumentSection {
	if p._sects == nil {
		var sz uint32
		p._sects, sz, p.err = p.id.readDocSections(p.idx, p._sectBuf)
		p.stats.ContentBytesLoaded += int64(sz)
		p._sectBuf = p._sects
	}
	return p._sects
}

func (p *contentProvider) newlines() newlines {
	if p._nl == nil {
		var sz uint32
		p._nl, sz, p.err = p.id.readNewlines(p.idx, p._nlBuf)
		p._nlBuf = p._nl
		p.stats.ContentBytesLoaded += int64(sz)
	}
	return newlines{locs: p._nl, fileSize: p.fileSize}
}

func (p *contentProvider) data(fileName bool) []byte {
	if fileName {
		return p.id.fileNameContent[p.id.fileNameIndex[p.idx]:p.id.fileNameIndex[p.idx+1]]
	}

	if p._data == nil {
		p._data, p.err = p.id.readContents(p.idx)
		p.stats.FilesLoaded++
		p.stats.ContentBytesLoaded += int64(len(p._data))
	}
	return p._data
}

// Find offset in bytes (relative to corpus start) for an offset in
// runes (relative to document start). If filename is set, the corpus
// is the set of filenames, with the document being the name itself.
func (p *contentProvider) findOffset(filename bool, r uint32) uint32 {
	if p.id.metaData.PlainASCII {
		return r
	}

	sample := p.id.runeOffsets
	runeEnds := p.id.fileEndRunes
	fileStartByte := p.id.boundaries[p.idx]
	if filename {
		sample = p.id.fileNameRuneOffsets
		runeEnds = p.id.fileNameEndRunes
		fileStartByte = p.id.fileNameIndex[p.idx]
	}

	absR := r
	if p.idx > 0 {
		absR += runeEnds[p.idx-1]
	}

	byteOff, left := sample.lookup(absR)

	var data []byte

	if filename {
		data = p.id.fileNameContent[byteOff:]
	} else {
		data, p.err = p.id.readContentSlice(byteOff, 3*runeOffsetFrequency)
		if p.err != nil {
			return 0
		}
	}
	for left > 0 {
		_, sz := utf8.DecodeRune(data)
		byteOff += uint32(sz)
		data = data[sz:]
		left--
	}

	byteOff -= fileStartByte
	return byteOff
}

func (p *contentProvider) fillMatches(ms []*candidateMatch, numContextLines int, language string, debug bool) []LineMatch {
	var result []LineMatch
	if ms[0].fileName {
		// There is only "line" in a filename.
		res := LineMatch{
			Line:     p.id.fileName(p.idx),
			FileName: true,
		}

		for _, m := range ms {
			res.LineFragments = append(res.LineFragments, LineFragmentMatch{
				LineOffset:  int(m.byteOffset),
				MatchLength: int(m.byteMatchSz),
				Offset:      m.byteOffset,
			})

			result = []LineMatch{res}
		}
	} else {
		ms = breakMatchesOnNewlines(ms, p.data(false))
		result = p.fillContentMatches(ms, numContextLines)
	}

	sects := p.docSections()
	for i, m := range result {
		result[i].Score, result[i].DebugScore = p.matchScore(sects, &m, language, debug)
	}

	return result
}

func (p *contentProvider) fillChunkMatches(ms []*candidateMatch, numContextLines int, language string, debug bool) []ChunkMatch {
	var result []ChunkMatch
	if ms[0].fileName {
		// If the first match is a filename match, there will only be
		// one match and the matched content will be the filename.

		fileName := p.id.fileName(p.idx)
		ranges := make([]Range, 0, len(ms))
		for _, m := range ms {
			ranges = append(ranges, Range{
				Start: Location{
					ByteOffset: m.byteOffset,
					LineNumber: 1,
					Column:     uint32(utf8.RuneCount(fileName[:m.byteOffset]) + 1),
				},
				End: Location{
					ByteOffset: m.byteOffset + m.byteMatchSz,
					LineNumber: 1,
					Column:     uint32(utf8.RuneCount(fileName[:m.byteOffset+m.byteMatchSz]) + 1),
				},
			})
		}

		result = []ChunkMatch{{
			Content:      fileName,
			ContentStart: Location{ByteOffset: 0, LineNumber: 1, Column: 1},
			Ranges:       ranges,
			FileName:     true,
		}}
	} else {
		result = p.fillContentChunkMatches(ms, numContextLines)
	}

	sects := p.docSections()
	for i, m := range result {
		result[i].Score, result[i].DebugScore = p.chunkMatchScore(sects, &m, language, debug)
	}

	return result
}

func (p *contentProvider) fillContentMatches(ms []*candidateMatch, numContextLines int) []LineMatch {
	var result []LineMatch
	for len(ms) > 0 {
		m := ms[0]
		num, lineStart, lineEnd := p.newlines().atOffset(m.byteOffset)

		var lineCands []*candidateMatch

		endMatch := m.byteOffset + m.byteMatchSz

		for len(ms) > 0 {
			m := ms[0]
			if int(m.byteOffset) <= lineEnd {
				endMatch = m.byteOffset + m.byteMatchSz
				lineCands = append(lineCands, m)
				ms = ms[1:]
			} else {
				break
			}
		}

		if len(lineCands) == 0 {
			log.Panicf(
				"%s %v infinite loop: num %d start,end %d,%d, offset %d",
				p.id.fileName(p.idx), p.id.metaData,
				num, lineStart, lineEnd,
				m.byteOffset)
		}

		data := p.data(false)

		// Due to merging matches, we may have a match that
		// crosses a line boundary. Prevent confusion by
		// taking lines until we pass the last match
		for lineEnd < len(data) && endMatch > uint32(lineEnd) {
			next := bytes.IndexByte(data[lineEnd+1:], '\n')
			if next == -1 {
				lineEnd = len(data)
			} else {
				// TODO(hanwen): test that checks "+1" part here.
				lineEnd += next + 1
			}
		}

		finalMatch := LineMatch{
			LineStart:  lineStart,
			LineEnd:    lineEnd,
			LineNumber: num,
		}
		finalMatch.Line = data[lineStart:lineEnd]

		if numContextLines > 0 {
			finalMatch.Before = p.newlines().getLines(data, num-numContextLines, num)
			finalMatch.After = p.newlines().getLines(data, num+1, num+1+numContextLines)
		}

		for _, m := range lineCands {
			fragment := LineFragmentMatch{
				Offset:      m.byteOffset,
				LineOffset:  int(m.byteOffset) - lineStart,
				MatchLength: int(m.byteMatchSz),
			}
			if m.symbol {
				start := p.id.fileEndSymbol[p.idx]
				fragment.SymbolInfo = p.id.symbols.data(start + m.symbolIdx)
				if fragment.SymbolInfo != nil {
					sec := p.docSections()[m.symbolIdx]
					fragment.SymbolInfo.Sym = string(data[sec.Start:sec.End])
				}
			}

			finalMatch.LineFragments = append(finalMatch.LineFragments, fragment)
		}
		result = append(result, finalMatch)
	}
	return result
}

func (p *contentProvider) fillContentChunkMatches(ms []*candidateMatch, numContextLines int) []ChunkMatch {
	newlines := p.newlines()
	chunks := chunkCandidates(ms, newlines, numContextLines)
	data := p.data(false)
	chunkMatches := make([]ChunkMatch, 0, len(chunks))
	for _, chunk := range chunks {
		ranges := make([]Range, 0, len(chunk.candidates))
		var symbolInfo []*Symbol
		for i, cm := range chunk.candidates {
			startOffset := cm.byteOffset
			endOffset := cm.byteOffset + cm.byteMatchSz
			startLine, startLineOffset, _ := newlines.atOffset(startOffset)
			endLine, endLineOffset, _ := newlines.atOffset(endOffset)

			ranges = append(ranges, Range{
				Start: Location{
					ByteOffset: startOffset,
					LineNumber: uint32(startLine),
					Column:     uint32(utf8.RuneCount(data[startLineOffset:startOffset]) + 1),
				},
				End: Location{
					ByteOffset: endOffset,
					LineNumber: uint32(endLine),
					Column:     uint32(utf8.RuneCount(data[endLineOffset:endOffset]) + 1),
				},
			})

			if cm.symbol {
				if symbolInfo == nil {
					symbolInfo = make([]*Symbol, len(chunk.candidates))
				}
				start := p.id.fileEndSymbol[p.idx]
				si := p.id.symbols.data(start + cm.symbolIdx)
				if si != nil {
					sec := p.docSections()[cm.symbolIdx]
					si.Sym = string(data[sec.Start:sec.End])
				}
				symbolInfo[i] = si
			}
		}

		firstLineNumber := int(chunk.firstLine) - numContextLines
		if firstLineNumber < 1 {
			firstLineNumber = 1
		}
		firstLineStart, _ := newlines.lineBounds(firstLineNumber)

		chunkMatches = append(chunkMatches, ChunkMatch{
			Content: newlines.getLines(data, firstLineNumber, int(chunk.lastLine)+numContextLines+1),
			ContentStart: Location{
				ByteOffset: firstLineStart,
				LineNumber: uint32(firstLineNumber),
				Column:     1,
			},
			FileName:   false,
			Ranges:     ranges,
			SymbolInfo: symbolInfo,
		})
	}
	return chunkMatches
}

type candidateChunk struct {
	firstLine  uint32 // 1-based, inclusive
	lastLine   uint32 // 1-based, inclusive
	minOffset  uint32 // 0-based, inclusive
	maxOffset  uint32 // 0-based, exclusive
	candidates []*candidateMatch
}

// chunkCandidates groups a set of sorted, non-overlapping candidate matches by line number. Adjacent
// chunks will be merged if adding `numContextLines` to the beginning and end of the chunk would cause
// it to overlap with an adjacent chunk.
func chunkCandidates(ms []*candidateMatch, newlines newlines, numContextLines int) []candidateChunk {
	var chunks []candidateChunk
	for _, m := range ms {
		startOffset := m.byteOffset
		endOffset := m.byteOffset + m.byteMatchSz
		firstLine, _, _ := newlines.atOffset(startOffset)
		lastLine, _, _ := newlines.atOffset(endOffset)

		if len(chunks) > 0 && int(chunks[len(chunks)-1].lastLine)+numContextLines >= firstLine-numContextLines {
			// If a new chunk created with the current candidateMatch would
			// overlap with the previous chunk, instead add the candidateMatch
			// to the last chunk and extend end of the last chunk.
			last := &chunks[len(chunks)-1]
			last.candidates = append(last.candidates, m)
			if last.maxOffset < endOffset {
				last.lastLine = uint32(lastLine)
				last.maxOffset = uint32(endOffset)
			}
		} else {
			chunks = append(chunks, candidateChunk{
				firstLine:  uint32(firstLine),
				lastLine:   uint32(lastLine),
				minOffset:  startOffset,
				maxOffset:  endOffset,
				candidates: []*candidateMatch{m},
			})
		}
	}
	return chunks
}

type newlines struct {
	// locs is the sorted set of byte offsets of the newlines in the file
	locs []uint32

	// fileSize is just the number of bytes in the file. It is stored
	// on this struct so we can safely know the length of the last line
	// in the file since not all files end in a newline.
	fileSize uint32
}

// atOffset returns the line containing the offset. If the offset lands on
// the newline ending line M, we return M.  The line is characterized
// by its linenumber (base-1, byte index of line start, byte index of
// line end). The line end is the index of a newline, or the filesize
// (if matching the last line of the file.)
func (nls newlines) atOffset(offset uint32) (lineNumber, lineStart, lineEnd int) {
	idx := sort.Search(len(nls.locs), func(n int) bool {
		return nls.locs[n] >= offset
	})

	start, end := nls.lineBounds(idx + 1)
	return idx + 1, int(start), int(end)
}

// lineBounds returns the byte offsets of the start and end of the 1-based
// lineNumber. The end offset is exclusive and will not contain the line-ending
// newline. If the line number is out of range of the lines in the file, start
// and end will be clamped to [0,fileSize].
func (nls newlines) lineBounds(lineNumber int) (start, end uint32) {
	// nls.locs[0] + 1 is the start of the 2nd line of data.
	startIdx := lineNumber - 2
	endIdx := lineNumber - 1

	if startIdx < 0 {
		start = 0
	} else if startIdx >= len(nls.locs) {
		start = nls.fileSize
	} else {
		start = nls.locs[startIdx] + 1
	}

	if endIdx < 0 {
		end = 0
	} else if endIdx >= len(nls.locs) {
		end = nls.fileSize
	} else {
		end = nls.locs[endIdx]
	}

	return start, end
}

// getLines returns a slice of data containing the lines [low, high).
// low is 1-based and inclusive. high is 1-based and exclusive.
func (nls newlines) getLines(data []byte, low, high int) []byte {
	if low >= high {
		return nil
	}

	lowStart, _ := nls.lineBounds(low)
	_, highEnd := nls.lineBounds(high - 1)

	return data[lowStart:highEnd]
}

const (
	// Query-dependent scoring signals. All of these together are bounded at ~9000
	// (scoreWordMatch + scoreSymbol + scoreKindMatch * 10 + scoreFactorAtomMatch).
	scorePartialWordMatch = 50.0
	scoreWordMatch        = 500.0
	scoreBase             = 7000.0
	scorePartialBase      = 4000.0
	scoreSymbol           = 7000.0
	scorePartialSymbol    = 4000.0
	scoreKindMatch        = 100.0
	scoreRepetitionFactor = 1.0
	scoreFactorAtomMatch  = 400.0

	// File-only scoring signals. For now these are also bounded ~9000 to give them
	// equal weight with the query-dependent signals.
	scoreFileRankFactor  = 9000.0
	scoreFileOrderFactor = 10.0
	scoreRepoRankFactor  = 20.0

	// Used for ordering line and chunk matches within a file.
	scoreLineOrderFactor = 1.0
)

// findSection checks whether a section defined by offset and size lies within
// one of the sections in secs.
func findSection(secs []DocumentSection, off, sz uint32) (int, bool) {
	j := sort.Search(len(secs), func(i int) bool {
		return secs[i].End >= off+sz
	})

	if j == len(secs) {
		return 0, false
	}

	if secs[j].Start <= off && off+sz <= secs[j].End {
		return j, true
	}
	return 0, false
}

func (p *contentProvider) chunkMatchScore(secs []DocumentSection, m *ChunkMatch, language string, debug bool) (float64, string) {
	type debugScore struct {
		score float64
		what  string
	}

	score := &debugScore{}
	maxScore := &debugScore{}

	addScore := func(what string, s float64) {
		if debug {
			score.what += fmt.Sprintf("%s:%.2f, ", what, s)
		}
		score.score += s
	}

	for i, r := range m.Ranges {
		// calculate the start and end offset relative to the start of the content
		relStartOffset := int(r.Start.ByteOffset - m.ContentStart.ByteOffset)
		relEndOffset := int(r.End.ByteOffset - m.ContentStart.ByteOffset)

		startBoundary := relStartOffset < len(m.Content) && (relStartOffset == 0 || byteClass(m.Content[relStartOffset-1]) != byteClass(m.Content[relStartOffset]))
		endBoundary := relEndOffset > 0 && (relEndOffset == len(m.Content) || byteClass(m.Content[relEndOffset-1]) != byteClass(m.Content[relEndOffset]))

		score.score = 0
		score.what = ""

		if startBoundary && endBoundary {
			addScore("WordMatch", scoreWordMatch)
		} else if startBoundary || endBoundary {
			addScore("PartialWordMatch", scorePartialWordMatch)
		}

		if m.FileName {
			sep := bytes.LastIndexByte(m.Content, '/')
			startMatch := relStartOffset == sep+1
			endMatch := relEndOffset == len(m.Content)
			if startMatch && endMatch {
				addScore("Base", scoreBase)
			} else if startMatch || endMatch {
				addScore("EdgeBase", (scoreBase+scorePartialBase)/2)
			} else if sep < relStartOffset {
				addScore("InnerBase", scorePartialBase)
			}
		} else if secIdx, ok := findSection(secs, uint32(r.Start.ByteOffset), uint32(r.End.ByteOffset-r.Start.ByteOffset)); ok {
			sec := secs[secIdx]
			startMatch := sec.Start == uint32(r.Start.ByteOffset)
			endMatch := sec.End == uint32(r.End.ByteOffset)
			if startMatch && endMatch {
				addScore("Symbol", scoreSymbol)
			} else if startMatch || endMatch {
				addScore("EdgeSymbol", (scoreSymbol+scorePartialSymbol)/2)
			} else {
				addScore("InnerSymbol", scorePartialSymbol)
			}

			var si *Symbol
			if m.SymbolInfo != nil {
				si = m.SymbolInfo[i]
			}
			if si == nil {
				// for non-symbol queries, we need to hydrate in SymbolInfo.
				start := p.id.fileEndSymbol[p.idx]
				si = p.id.symbols.data(start + uint32(secIdx))
			}
			if si != nil {
				addScore(fmt.Sprintf("kind:%s:%s", language, si.Kind), scoreKind(language, si.Kind))
			}
		}

		if score.score > maxScore.score {
			maxScore.score = score.score
			maxScore.what = score.what
		}
	}

	if debug {
		maxScore.what = fmt.Sprintf("score:%f <- %s", maxScore.score, strings.TrimRight(maxScore.what, ", "))
	}

	return maxScore.score, maxScore.what
}

func (p *contentProvider) matchScore(secs []DocumentSection, m *LineMatch, language string, debug bool) (float64, string) {
	type debugScore struct {
		score float64
		what  string
	}

	score := &debugScore{}
	maxScore := &debugScore{}

	addScore := func(what string, s float64) {
		if debug {
			score.what += fmt.Sprintf("%s:%.2f, ", what, s)
		}
		score.score += s
	}

	for _, f := range m.LineFragments {
		startBoundary := f.LineOffset < len(m.Line) && (f.LineOffset == 0 || byteClass(m.Line[f.LineOffset-1]) != byteClass(m.Line[f.LineOffset]))

		end := int(f.LineOffset) + f.MatchLength
		endBoundary := end > 0 && (end == len(m.Line) || byteClass(m.Line[end-1]) != byteClass(m.Line[end]))

		score.score = 0
		score.what = ""

		if startBoundary && endBoundary {
			addScore("WordMatch", scoreWordMatch)
		} else if startBoundary || endBoundary {
			addScore("PartialWordMatch", scorePartialWordMatch)
		}

		if m.FileName {
			sep := bytes.LastIndexByte(m.Line, '/')
			startMatch := sep+1 == f.LineOffset
			endMatch := len(m.Line) == f.LineOffset+f.MatchLength
			if startMatch && endMatch {
				addScore("Base", scoreBase)
			} else if startMatch || endMatch {
				addScore("EdgeBase", (scoreBase+scorePartialBase)/2)
			} else if sep < f.LineOffset {
				addScore("InnerBase", scorePartialBase)
			}
		} else if secIdx, ok := findSection(secs, f.Offset, uint32(f.MatchLength)); ok {
			sec := secs[secIdx]
			startMatch := sec.Start == f.Offset
			endMatch := sec.End == f.Offset+uint32(f.MatchLength)
			if startMatch && endMatch {
				addScore("Symbol", scoreSymbol)
			} else if startMatch || endMatch {
				addScore("EdgeSymbol", (scoreSymbol+scorePartialSymbol)/2)
			} else {
				addScore("InnerSymbol", scorePartialSymbol)
			}

			si := f.SymbolInfo
			if si == nil {
				// for non-symbol queries, we need to hydrate in SymbolInfo.
				start := p.id.fileEndSymbol[p.idx]
				si = p.id.symbols.data(start + uint32(secIdx))
			}
			if si != nil {
				// the LineFragment may not be on a symbol, then si will be nil.
				addScore(fmt.Sprintf("kind:%s:%s", language, si.Kind), scoreKind(language, si.Kind))
			}
		}

		if score.score > maxScore.score {
			maxScore.score = score.score
			maxScore.what = score.what
		}
	}

	if debug {
		maxScore.what = fmt.Sprintf("score:%.2f <- %s", maxScore.score, strings.TrimSuffix(maxScore.what, ", "))
	}

	return maxScore.score, maxScore.what
}

// scoreKind boosts a match based on the combination of language and kind. The
// language string comes from go-enry, the kind string from ctags.
func scoreKind(language string, kind string) float64 {
	var factor float64

	// Generic ranking which will be overriden by language specific ranking
	switch kind {
	case "type": // scip-ctags regression workaround https://github.com/sourcegraph/sourcegraph/issues/57659
		factor = 8
	case "class":
		factor = 10
	case "struct":
		factor = 9.5
	case "enum":
		factor = 9
	case "interface":
		factor = 8
	case "function", "func":
		factor = 7
	case "method":
		factor = 6
	case "member", "field":
		factor = 5.5
	case "constant", "const":
		factor = 5
	case "var", "variable":
		factor = 4
	}

	// Refer to universal-ctags --list-kinds-full=<language> to learn about which
	// kinds are detected for which language.
	//
	// Note that go-ctags uses universal-ctags's interactive mode and thus returns
	// the full name for "kind" and not the one-letter abbreviation.
	switch language {
	case "Java", "java":
		switch kind {
		// 2022-03-30: go-ctags contains a regex rule for Java classes that sets "kind"
		// to "classes" instead of "c". We have to cover both cases to support existing
		// indexes.
		case "class", "classes":
			factor = 10
		case "enum":
			factor = 9
		case "interface":
			factor = 8
		case "method":
			factor = 7
		case "field":
			factor = 6
		case "enumConstant":
			factor = 5
		}
	case "Kotlin", "kotlin":
		switch kind {
		case "class":
			factor = 10
		case "interface":
			factor = 9
		case "method":
			factor = 8
		case "typealias":
			factor = 7
		case "constant":
			factor = 6
		case "variable":
			factor = 5
		}
	case "Go", "go":
		switch kind {
		// scip-ctags regression workaround https://github.com/sourcegraph/sourcegraph/issues/57659
		// for each case a description of the fields in ctags in the comment
		case "type": // interface struct talias
			factor = 10
		case "method": // methodSpec
			factor = 8.5
		case "function": // func
			factor = 8
		case "variable": // var member
			factor = 7
		case "constant": // const
			factor = 6

		case "interface": // interfaces
			factor = 10
		case "struct": // structs
			factor = 9
		case "talias": // type aliases
			factor = 9
		case "methodSpec": // interface method specification
			factor = 8.5
		case "func": // functions
			factor = 8
		case "member": // struct members
			factor = 7
		case "const": // constants
			factor = 6
		case "var": // variables
			factor = 5
		}
		// Could also rank on:
		//
		//   - anonMember  struct anonymous members
		//   - packageName name for specifying imported package
		//   - receiver    receivers
		//   - package     packages
		//   - type        types
		//   - unknown     unknown
	case "C++", "c++":
		switch kind {
		case "class": // classes
			factor = 10
		case "enum": // enumeration names
			factor = 9
		case "function": // function definitions
			factor = 8
		case "struct": // structure names
			factor = 7
		case "union": // union names
			factor = 6
		case "typdef": // typedefs
			factor = 5
		case "member": // class, struct, and union members
			factor = 4
		case "variable": // varialbe definitions
			factor = 3
		}
	// Could also rank on:
	// NAME        DESCRIPTION
	// macro       macro definitions
	// enumerator  enumerators (values inside an enumeration)
	// header      included header files
	// namespace   namespaces
	// variable    variable definitions
	case "Scala", "scala":
		switch kind {
		case "class":
			factor = 10
		case "interface":
			factor = 9
		case "object":
			factor = 8
		case "method":
			factor = 7
		case "type":
			factor = 6
		case "variable":
			factor = 5
		case "package":
			factor = 4
		}
	case "Python", "python":
		switch kind {
		case "class": // classes
			factor = 10
		case "function": // function definitions
			factor = 8
		case "member": // class, struct, and union members
			factor = 4
		case "variable": // variable definitions
			factor = 3
		case "local": // local variables
			factor = 2
		}
		// Could also rank on:
		//
		//   - namespace name referring a module defined in other file
		//   - module    modules
		//   - unknown   name referring a class/variable/function/module defined in other module
		//   - parameter function parameters
	case "Ruby", "ruby":
		switch kind {
		case "class":
			factor = 10
		case "method":
			factor = 9
		case "alias":
			factor = 8
		case "module":
			factor = 7
		case "singletonMethod":
			factor = 6
		case "constant":
			factor = 5
		case "accessor":
			factor = 4
		case "library":
			factor = 3
		}
	case "PHP", "php":
		switch kind {
		case "class":
			factor = 10
		case "interface":
			factor = 9
		case "function":
			factor = 8
		case "trait":
			factor = 7
		case "define":
			factor = 6
		case "namespace":
			factor = 5
		case "alias":
			factor = 4
		case "variable":
			factor = 3
		case "local":
			factor = 3
		}
	case "GraphQL", "graphql":
		switch kind {
		case "type":
			factor = 10
		}
	}

	return factor * scoreKindMatch
}

type matchScoreSlice []LineMatch

func (m matchScoreSlice) Len() int           { return len(m) }
func (m matchScoreSlice) Swap(i, j int)      { m[i], m[j] = m[j], m[i] }
func (m matchScoreSlice) Less(i, j int) bool { return m[i].Score > m[j].Score }

type chunkMatchScoreSlice []ChunkMatch

func (m chunkMatchScoreSlice) Len() int           { return len(m) }
func (m chunkMatchScoreSlice) Swap(i, j int)      { m[i], m[j] = m[j], m[i] }
func (m chunkMatchScoreSlice) Less(i, j int) bool { return m[i].Score > m[j].Score }

type fileMatchesByScore []FileMatch

func (m fileMatchesByScore) Len() int           { return len(m) }
func (m fileMatchesByScore) Swap(i, j int)      { m[i], m[j] = m[j], m[i] }
func (m fileMatchesByScore) Less(i, j int) bool { return m[i].Score > m[j].Score }

func sortMatchesByScore(ms []LineMatch) {
	sort.Sort(matchScoreSlice(ms))
}

func sortChunkMatchesByScore(ms []ChunkMatch) {
	sort.Sort(chunkMatchScoreSlice(ms))
}

// SortFiles sorts files matches. The order depends on the match score, which includes both
// query-dependent signals like word overlap, and file-only signals like the file ranks (if
// file ranks are enabled).
func SortFiles(ms []FileMatch) {
	sort.Sort(fileMatchesByScore(ms))
}
