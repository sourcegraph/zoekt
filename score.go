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
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	maxUInt16   = 0xffff
	ScoreOffset = 10_000_000
)

// addScore increments the score of the FileMatch by the computed score. If
// debugScore is true, it also adds a debug string to the FileMatch. If raw is
// -1, it is ignored. Otherwise, it is added to the debug string.
func (m *FileMatch) addScore(what string, computed float64, raw float64, debugScore bool) {
	if computed != 0 && debugScore {
		var b strings.Builder
		fmt.Fprintf(&b, "%s", what)
		if raw != -1 {
			fmt.Fprintf(&b, "(%s)", strconv.FormatFloat(raw, 'f', -1, 64))
		}
		fmt.Fprintf(&b, ":%.2f, ", computed)
		m.Debug += b.String()
	}
	m.Score += computed
}

// scoreFile computes a score for the file match using various scoring signals, like
// whether there's an exact match on a symbol, the number of query clauses that matched, etc.
func (d *indexData) scoreFile(fileMatch *FileMatch, doc uint32, mt matchTree, known map[matchTree]bool, opts *SearchOptions) {
	atomMatchCount := 0
	visitMatchAtoms(mt, known, func(mt matchTree) {
		atomMatchCount++
	})

	addScore := func(what string, computed float64) {
		fileMatch.addScore(what, computed, -1, opts.DebugScore)
	}

	// atom-count boosts files with matches from more than 1 atom. The
	// maximum boost is scoreFactorAtomMatch.
	if atomMatchCount > 0 {
		fileMatch.addScore("atom", (1.0-1.0/float64(atomMatchCount))*scoreFactorAtomMatch, float64(atomMatchCount), opts.DebugScore)
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

	// Maintain ordering of input files. This
	// strictly dominates the in-file ordering of
	// the matches.
	addScore("fragment", maxFileScore)

	// Add tiebreakers
	//
	// ScoreOffset shifts the score 7 digits to the left.
	fileMatch.Score = math.Trunc(fileMatch.Score) * ScoreOffset

	md := d.repoMetaData[d.repos[doc]]

	// md.Rank lies in the range [0, 65535]. Hence, we have to allocate 5 digits for
	// the rank. The scoreRepoRankFactor shifts the rank score 2 digits to the left,
	// reserving digits 3-7 for the repo rank.
	addScore("repo-rank", scoreRepoRankFactor*float64(md.Rank))

	// digits 1-2 and the decimals are reserved for the doc order. Doc order
	// (without the scaling factor) lies in the range [0, 1]. The upper bound is
	// achieved for matches in the first document of a shard.
	addScore("doc-order", scoreFileOrderFactor*(1.0-float64(doc)/float64(len(d.boundaries))))

	if opts.DebugScore {
		// To make the debug output easier to read, we split the score into the query
		// dependent score and the tiebreaker
		score := math.Trunc(fileMatch.Score / ScoreOffset)
		tiebreaker := fileMatch.Score - score*ScoreOffset
		fileMatch.Debug = fmt.Sprintf("score: %d (%.2f) <- %s", int(score), tiebreaker, strings.TrimSuffix(fileMatch.Debug, ", "))
	}
}

// idf computes the inverse document frequency for a term. nq is the number of
// documents that contain the term and documentCount is the total number of
// documents in the corpus.
func idf(nq, documentCount int) float64 {
	return math.Log(1.0 + ((float64(documentCount) - float64(nq) + 0.5) / (float64(nq) + 0.5)))
}

// termDocumentFrequency is a map "term" -> "number of documents that contain the term"
type termDocumentFrequency map[string]int

// termFrequency stores the term frequencies for doc.
type termFrequency struct {
	doc uint32
	tf  map[string]int
}

// scoreFilesUsingBM25 computes the score according to BM25, the most common
// scoring algorithm for text search: https://en.wikipedia.org/wiki/Okapi_BM25.
//
// This scoring strategy ignores all other signals including document ranks.
// This keeps things simple for now, since BM25 is not normalized and can be
// tricky to combine with other scoring signals.
func (d *indexData) scoreFilesUsingBM25(fileMatches []FileMatch, tfs []termFrequency, df termDocumentFrequency, opts *SearchOptions) {
	// Use standard parameter defaults (used in Lucene and academic papers)
	k, b := 1.2, 0.75

	averageFileLength := float64(d.boundaries[d.numDocs()]) / float64(d.numDocs())
	// This is very unlikely, but explicitly guard against division by zero.
	if averageFileLength == 0 {
		averageFileLength++
	}

	for i := range tfs {
		score := 0.0

		// Compute the file length ratio. Usually the calculation would be based on terms, but using
		// bytes should work fine, as we're just computing a ratio.
		doc := tfs[i].doc
		fileLength := float64(d.boundaries[doc+1] - d.boundaries[doc])

		L := fileLength / averageFileLength

		sumTF := 0 // Just for debugging
		for term, f := range tfs[i].tf {
			sumTF += f
			tfScore := ((k + 1.0) * float64(f)) / (k*(1.0-b+b*L) + float64(f))
			score += idf(df[term], int(d.numDocs())) * tfScore
		}

		fileMatches[i].Score = score

		if opts.DebugScore {
			fileMatches[i].Debug = fmt.Sprintf("bm25-score: %.2f <- sum-termFrequencies: %d, length-ratio: %.2f", score, sumTF, L)
		}
	}
}
