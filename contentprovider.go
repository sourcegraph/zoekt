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
	// TODO - how to scale this relative to rank?
	scorePartialWordMatch   = 50.0
	scoreWordMatch          = 500.0
	scoreBase               = 7000.0
	scorePartialBase        = 4000.0
	scoreImportantThreshold = 2000.0
	scoreSymbol             = 7000.0
	scorePartialSymbol      = 4000.0
	scoreKindMatch          = 1000.0
	scoreFactorAtomMatch    = 400.0
	scoreShardRankFactor    = 20.0
	scoreFileOrderFactor    = 10.0
	scoreLineOrderFactor    = 1.0
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

func (p *contentProvider) matchScore(secs []DocumentSection, m *LineMatch, language string, debug bool) (float64, string) {
	type debugScore struct {
		score float64
		what  string
	}

	score := &debugScore{}
	maxScore := &debugScore{}

	addScore := func(what string, s float64) {
		if debug {
			score.what += fmt.Sprintf("%s:%f, ", what, s)
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
	return maxScore.score, strings.TrimRight(maxScore.what, ", ")
}

// scoreKind boosts a match based on the combination of language and kind. The
// language string comes from go-enry, the kind string from ctags.
func scoreKind(language string, kind string) float64 {
	// Refer to universal-ctags --list-kinds=<language> to learn about the mappings
	// for a language.
	switch language {
	case "Java":
		switch kind {
		// 2022-03-30: go-ctags contains a regex rule for Java classes that sets "kind"
		// to "classes" instead of "c". We have to cover both cases to support existing
		// indexes.
		case "c", "classes":
			return scoreKindMatch
		}
	}
	return 0
}

type matchScoreSlice []LineMatch

func (m matchScoreSlice) Len() int           { return len(m) }
func (m matchScoreSlice) Swap(i, j int)      { m[i], m[j] = m[j], m[i] }
func (m matchScoreSlice) Less(i, j int) bool { return m[i].Score > m[j].Score }

type fileMatchSlice []FileMatch

func (m fileMatchSlice) Len() int           { return len(m) }
func (m fileMatchSlice) Swap(i, j int)      { m[i], m[j] = m[j], m[i] }
func (m fileMatchSlice) Less(i, j int) bool { return m[i].Score > m[j].Score }

func sortMatchesByScore(ms []LineMatch) {
	sort.Sort(matchScoreSlice(ms))
}

// Sort a slice of results.
func SortFilesByScore(ms []FileMatch) {
	sort.Sort(fileMatchSlice(ms))
}
