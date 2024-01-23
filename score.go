package zoekt

import (
	"fmt"
	"math/bits"
)

// matchTreeScorer contains fields related to scoring we extract from how well
// our candidates match the full tree.
//
// We need to track this information outside of the candidate matches
// themselves since we do things like prune out filename matches if we have
// content matches.
//
// For example a FileMatch which contains a match from every atom in the tree
// is a better match than a FileMatch which only matches one atom.
type matchTreeScorer struct {
	// filenameQueryAtoms is a bitset of filename query atoms. The bit is set if
	// we found matches on it.
	filenameQueryAtoms uint64

	// filenameAtomLen is the number of filename atoms seen. Because we are
	// storing this in a bitset, we bound this by 64.
	filenameAtomLen uint8

	// contentAtomLen is the number of content atoms seen. Because we are
	// storing this in a bitset, we bound this by 64.
	contentAtomLen uint8
}

// setQueryAtom will set the value of candidateMatch.queryAtoms to the next
// available queryAtom bit for everything in cands. This should be called once
// for each match tree atom.
//
// filename is true if the matchtree is for filename matching.
//
// It returns cands convenience.
//
// Note: this relies on being called in the "in-order" walk of the matchtree
// for the bitset values to be useful.
func (mts *matchTreeScorer) setQueryAtoms(cands []*candidateMatch, filename bool) []*candidateMatch {
	if filename {
		// in the rare case we have more than 64 filename atoms, we ignore the
		// remaining.
		if mts.filenameAtomLen >= 64 {
			return cands
		}

		// Set the bit if we match
		if len(cands) > 0 {
			mts.filenameQueryAtoms = mts.filenameQueryAtoms | (1 << mts.filenameAtomLen)
		}

		mts.filenameAtomLen++

		return cands
	}

	// in the rare case we have more than 64 content atoms, we ignore the
	// remaining.
	if mts.contentAtomLen >= 64 {
		return cands
	}

	v := uint64(1 << mts.contentAtomLen)
	mts.contentAtomLen++
	for _, cm := range cands {
		cm.queryAtoms = v
	}
	return cands
}

func (mts *matchTreeScorer) score(ms []*candidateMatch, debug bool) (score float64, what string) {
	var scoreFilename float64
	if mts.filenameQueryAtoms != 0 {
		scoreFilename = float64(bits.OnesCount64(mts.filenameQueryAtoms)) * scoreQueryFilenameAtomsCountFactor / float64(mts.filenameAtomLen)
		if debug {
			what += fmt.Sprintf("%s:%.2f, ", "queryFilenameAtomsCount", scoreFilename)
		}
	}

	// Rest of scorer has to do with content, skip if there is none.
	if mts.contentAtomLen == 0 {
		return scoreFilename, what
	}

	queryAtoms := uint64(0)
	last := uint64(0)
	run := 0
	maxRun := 0
	for _, cm := range ms {
		qa := cm.queryAtoms
		queryAtoms = queryAtoms | qa

		// TODO handle multiple bits set in qa. only happens for linematch
		if last < qa {
			run++
			if run > maxRun {
				maxRun = run
			}
		} else if last > qa {
			run = 1
		}
		last = qa
	}

	scoreCount := float64(bits.OnesCount64(queryAtoms)) * scoreQueryContentAtomsCountFactor / float64(mts.contentAtomLen)
	scoreRun := float64(maxRun) * scoreQueryContentAtomsRunFactor / float64(mts.contentAtomLen)

	if debug && scoreCount > 0 {
		what += fmt.Sprintf("%s:%.2f, ", "queryContentAtomsCount", scoreCount)
	}
	if debug && scoreRun > 0 {
		what += fmt.Sprintf("%s:%.2f, ", "queryContentAtomsRun", scoreRun)
	}

	return scoreFilename + scoreCount + scoreRun, what
}

func scoreVecToFloat(score []float64) float64 {
	var total float64
	for _, f := range score {
		total += f
	}
	return total
}
