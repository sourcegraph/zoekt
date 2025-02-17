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
	"fmt"
	"math"
	"strings"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/internal/ctags"
)

const (
	ScoreOffset     = 10_000_000
	ScoreOffsetBM25 = 1_000_000_000
)

type chunkScore struct {
	score      float64
	debugScore string
	bestLine   int
}

// scoreChunk calculates the score for each line in the chunk based on its candidate matches, and returns the score of
// the best-scoring line, along with its line number.
// Invariant: there should be at least one input candidate, len(ms) > 0.
func (p *contentProvider) scoreChunk(ms []*candidateMatch, language string, opts *zoekt.SearchOptions) (chunkScore, []*zoekt.Symbol) {
	nl := p.newlines()

	var bestScore lineScore
	bestLine := 0
	var symbolInfo []*zoekt.Symbol

	start := 0
	currentLine := -1
	for i, m := range ms {
		lineNumber := -1
		if !m.fileName {
			lineNumber = nl.atOffset(m.byteOffset)
		}

		// If this match represents a new line, then score the previous line and update 'start'.
		if i != 0 && lineNumber != currentLine {
			score, si := p.scoreLine(ms[start:i], language, currentLine, opts)
			symbolInfo = append(symbolInfo, si...)
			if score.score > bestScore.score {
				bestScore = score
				bestLine = currentLine
			}
			start = i
		}
		currentLine = lineNumber
	}

	// Make sure to score the last line
	line, si := p.scoreLine(ms[start:], language, currentLine, opts)
	symbolInfo = append(symbolInfo, si...)
	if line.score > bestScore.score {
		bestScore = line
		bestLine = currentLine
	}

	cs := chunkScore{
		score:    bestScore.score,
		bestLine: bestLine,
	}
	if opts.DebugScore {
		cs.debugScore = fmt.Sprintf("%s, (line: %d)", bestScore.debugScore, bestLine)
	}
	return cs, symbolInfo
}

type lineScore struct {
	score      float64
	debugScore string
}

// scoreLine calculates a score for the line based on its candidate matches.
// Invariants:
// - All candidate matches are assumed to come from the same line in the content.
// - If this line represents a filename, then lineNumber must be -1.
// - There should be at least one input candidate, len(ms) > 0.
func (p *contentProvider) scoreLine(ms []*candidateMatch, language string, lineNumber int, opts *zoekt.SearchOptions) (lineScore, []*zoekt.Symbol) {
	if opts.UseBM25Scoring {
		score, symbolInfo := p.scoreLineBM25(ms, lineNumber)
		ls := lineScore{score: score}
		if opts.DebugScore {
			ls.debugScore = fmt.Sprintf("tfScore:%.2f, ", score)
		}
		return ls, symbolInfo
	}

	score := 0.0
	what := ""
	addScore := func(w string, s float64) {
		if s != 0 && opts.DebugScore {
			what += fmt.Sprintf("%s:%.2f, ", w, s)
		}
		score += s
	}

	filename := p.data(true)
	var symbolInfo []*zoekt.Symbol

	var bestScore lineScore
	for i, m := range ms {
		data := p.data(m.fileName)

		endOffset := m.byteOffset + m.byteMatchSz
		startBoundary := m.byteOffset < uint32(len(data)) && (m.byteOffset == 0 || byteClass(data[m.byteOffset-1]) != byteClass(data[m.byteOffset]))
		endBoundary := endOffset > 0 && (endOffset == uint32(len(data)) || byteClass(data[endOffset-1]) != byteClass(data[endOffset]))

		score = 0
		what = ""

		if startBoundary && endBoundary {
			addScore("WordMatch", scoreWordMatch)
		} else if startBoundary || endBoundary {
			addScore("PartialWordMatch", scorePartialWordMatch)
		}

		if m.fileName {
			sep := bytes.LastIndexByte(data, '/')
			startMatch := int(m.byteOffset) == sep+1
			endMatch := endOffset == uint32(len(data))
			if startMatch && endMatch {
				addScore("Base", scoreBase)
			} else if startMatch || endMatch {
				addScore("EdgeBase", (scoreBase+scorePartialBase)/2)
			} else if sep < int(m.byteOffset) {
				addScore("InnerBase", scorePartialBase)
			}
		} else if sec, si, ok := p.findSymbol(m); ok {
			startMatch := sec.Start == m.byteOffset
			endMatch := sec.End == endOffset
			if startMatch && endMatch {
				addScore("Symbol", scoreSymbol)
			} else if startMatch || endMatch {
				addScore("EdgeSymbol", (scoreSymbol+scorePartialSymbol)/2)
			} else {
				addScore("OverlapSymbol", scorePartialSymbol)
			}

			// Score based on symbol data
			if si != nil {
				symbolKind := ctags.ParseSymbolKind(si.Kind)
				sym := sectionSlice(data, sec)

				addScore(fmt.Sprintf("kind:%s:%s", language, si.Kind), scoreSymbolKind(language, filename, sym, symbolKind))

				// This is from a symbol tree, so we need to store the symbol
				// information.
				if m.symbol {
					if symbolInfo == nil {
						symbolInfo = make([]*zoekt.Symbol, len(ms))
					}
					// findSymbols does not hydrate in Sym. So we need to store it.
					si.Sym = string(sym)
					symbolInfo[i] = si
				}
			}
		}

		// scoreWeight != 1 means it affects score
		if !epsilonEqualsOne(m.scoreWeight) {
			score = score * m.scoreWeight
			if opts.DebugScore {
				what += fmt.Sprintf("boost:%.2f, ", m.scoreWeight)
			}
		}

		if score > bestScore.score {
			bestScore.score = score
			bestScore.debugScore = what
		}
	}

	if opts.DebugScore {
		bestScore.debugScore = fmt.Sprintf("score:%.2f <- %s", bestScore.score, strings.TrimSuffix(bestScore.debugScore, ", "))
	}

	return bestScore, symbolInfo
}

// scoreLineBM25 computes the score of a line according to BM25, the most common scoring algorithm for text search:
// https://en.wikipedia.org/wiki/Okapi_BM25. Compared to the standard scoreLine algorithm, this score rewards multiple
// term matches on a line.
// Notes:
// - This BM25 calculation skips inverse document frequency (idf) to keep the implementation simple.
// - It uses the same calculateTermFrequency method as BM25 file scoring, which boosts filename and symbol matches.
func (p *contentProvider) scoreLineBM25(ms []*candidateMatch, lineNumber int) (float64, []*zoekt.Symbol) {
	// If this is a filename, then don't compute BM25. The score would not be comparable to line scores.
	if lineNumber < 0 {
		return 0, nil
	}

	// Use standard parameter defaults used in Lucene (https://lucene.apache.org/core/10_1_0/core/org/apache/lucene/search/similarities/BM25Similarity.html)
	k, b := 1.2, 0.75

	// Calculate the length ratio of this line. As a heuristic, we assume an average line length of 100 characters.
	// Usually the calculation would be based on terms, but using bytes should work fine, as we're just computing a ratio.
	nl := p.newlines()
	lineLength := nl.lineStart(lineNumber+1) - nl.lineStart(lineNumber)
	L := float64(lineLength) / 100.0

	score := 0.0
	tfs := p.calculateTermFrequency(ms)
	for _, f := range tfs {
		score += tfScore(k, b, L, f)
	}

	// Check if any index comes from a symbol match tree, and if so hydrate in symbol information
	var symbolInfo []*zoekt.Symbol
	for _, m := range ms {
		if m.symbol {
			if sec, si, ok := p.findSymbol(m); ok && si != nil {
				// findSymbols does not hydrate in Sym. So we need to store it.
				sym := sectionSlice(p.data(false), sec)
				si.Sym = string(sym)
				symbolInfo = append(symbolInfo, si)
			}
		}
	}
	return score, symbolInfo
}

// tfScore is the term frequency score for BM25.
func tfScore(k float64, b float64, L float64, f int) float64 {
	return ((k + 1.0) * float64(f)) / (k*(1.0-b+b*L) + float64(f))
}

// calculateTermFrequency computes the term frequency for the file match.
// Notes:
// - Filename matches count more than content matches. This mimics a common text search strategy to 'boost' matches on document titles.
// - Symbol matches also count more than content matches, to reward matches on symbol definitions.
func (p *contentProvider) calculateTermFrequency(cands []*candidateMatch) map[string]int {
	// Treat each candidate match as a term and compute the frequencies. For now, ignore case sensitivity and
	// ignore whether the index is a word boundary.
	termFreqs := map[string]int{}
	for _, m := range cands {
		term := string(m.substrLowered)
		if m.fileName || p.matchesSymbol(m) {
			termFreqs[term] += 5
		} else {
			termFreqs[term]++
		}
	}

	return termFreqs
}

// scoreFile computes a score for the file match using various scoring signals, like
// whether there's an exact match on a symbol, the number of query clauses that matched, etc.
func (d *indexData) scoreFile(fileMatch *zoekt.FileMatch, doc uint32, mt matchTree, known map[matchTree]bool, opts *zoekt.SearchOptions) {
	atomMatchCount := 0
	visitMatchAtoms(mt, known, func(mt matchTree) {
		atomMatchCount++
	})

	addScore := func(what string, computed float64) {
		fileMatch.AddScore(what, computed, -1, opts.DebugScore)
	}

	// atom-count boosts files with matches from more than 1 atom. The
	// maximum boost is scoreFactorAtomMatch.
	if atomMatchCount > 0 {
		fileMatch.AddScore("atom", (1.0-1.0/float64(atomMatchCount))*scoreFactorAtomMatch, float64(atomMatchCount), opts.DebugScore)
	}

	maxFileScore := 0.0
	for i := range fileMatch.LineMatches {
		if maxFileScore < fileMatch.LineMatches[i].Score {
			maxFileScore = fileMatch.LineMatches[i].Score
		}

		// Order by ordering in file.
		fileMatch.LineMatches[i].Score += scoreLineOrderFactor * (1.0 - (float64(i) / float64(len(fileMatch.LineMatches))))
	}

	for i := range fileMatch.ChunkMatches {
		if maxFileScore < fileMatch.ChunkMatches[i].Score {
			maxFileScore = fileMatch.ChunkMatches[i].Score
		}

		// Order by ordering in file.
		fileMatch.ChunkMatches[i].Score += scoreLineOrderFactor * (1.0 - (float64(i) / float64(len(fileMatch.ChunkMatches))))
	}

	// Maintain ordering of input files. This strictly dominates the in-file ordering of the matches.
	addScore("fragment", maxFileScore)

	// Truncate score to avoid overlap with the tiebreakers.
	fileMatch.Score = math.Trunc(fileMatch.Score)

	// Add tiebreakers
	repoRank := d.repoMetaData[d.repos[doc]].Rank                  // [0, 65535]
	docOrderScore := 1.0 - float64(doc)/float64(len(d.boundaries)) // [0, 1]

	if opts.DebugScore {
		// We log the score components individually for better readability.
		fileMatch.Debug = fmt.Sprintf("score: %d (repo-rank: %d, file-rank: %.2f) <- %s", int(fileMatch.Score), repoRank, docOrderScore, strings.TrimSuffix(fileMatch.Debug, ", "))
	}

	fileMatch.Score = ScoreOffset*fileMatch.Score + scoreRepoRankFactor*float64(repoRank) + scoreFileOrderFactor*docOrderScore
}

// scoreFilesUsingBM25 computes the score according to BM25, the most common scoring algorithm for text search:
// https://en.wikipedia.org/wiki/Okapi_BM25. Note that we treat the inverse document frequency (idf) as constant. This
// is supported by our evaluations which showed that for keyword style queries, idf can down-weight the score of some
// keywords too much, leading to a worse ranking. The intuition is that each keyword is important independently of how
// frequent it appears in the corpus.
//
// Unlike standard file scoring, this scoring strategy ignores all other signals including document ranks. This keeps
// things simple for now, since BM25 is not normalized and can be tricky to combine with other scoring signals. It also
// ignores the individual LineMatch and ChunkMatch scores, instead calculating a score over all matches in the file.
func (d *indexData) scoreFilesUsingBM25(fileMatch *zoekt.FileMatch, doc uint32, tf map[string]int, opts *zoekt.SearchOptions) {
	// Use standard parameter defaults used in Lucene (https://lucene.apache.org/core/10_1_0/core/org/apache/lucene/search/similarities/BM25Similarity.html)
	k, b := 1.2, 0.75

	averageFileLength := float64(d.boundaries[d.numDocs()]) / float64(d.numDocs())
	// This is very unlikely, but explicitly guard against division by zero.
	if averageFileLength == 0 {
		averageFileLength++
	}

	// Compute the file length ratio. Usually the calculation would be based on terms, but using
	// bytes should work fine, as we're just computing a ratio.
	fileLength := float64(d.boundaries[doc+1] - d.boundaries[doc])

	L := fileLength / averageFileLength

	score := 0.0
	sumTF := 0 // Just for debugging
	for _, f := range tf {
		sumTF += f
		score += tfScore(k, b, L, f)
	}
	// 2 digits of precision
	score = math.Trunc(score*100) / 100

	md := d.repoMetaData[d.repos[doc]]
	fileOrderScore := 1.0 - float64(doc)/float64(len(d.boundaries))

	// Offset score by 9 digits and add the tiebreaker.
	//
	// Example: For a BM25 score of 1.23, a repo rank of 456789 and a file order score of 0.12, we have a final score of
	// 12345678901.2
	// ^^^
	// bm25
	//    ^^^^^^
	//	  repo rank
	//          ^^^^
	//          doc order
	fileMatch.Score = score*ScoreOffsetBM25 + scoreRepoRankFactor*float64(md.Rank) + scoreFileOrderFactor*fileOrderScore

	if opts.DebugScore {
		// To make the debug output easier to read, we split the score into the query dependent score and the tiebreaker
		fileMatch.Debug = fmt.Sprintf("bm25-score: %.2f (repo-rank: %d, file-rank: %.2f) <- sum-termFrequencies: %d, length-ratio: %.2f", score, md.Rank, fileOrderScore, sumTF, L)
	}
}
