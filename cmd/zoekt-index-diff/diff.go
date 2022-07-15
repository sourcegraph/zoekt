package main

import (
	"bytes"
	"log"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/zoekt"
	"github.com/sourcegraph/go-diff/diff"
)

// parseDiffHuskNew parses diff husk into new file contents
// As we use -U<large number> each file is only a single husk
func parseDiffHuskNew(h *diff.Hunk) []byte {
	content := h.Body[:0]
	hunk := h.Body
	for len(hunk) > 0 {
		newLine := bytes.IndexByte(hunk, '\n')
		switch hunk[0] {
		case ' ', '+':
			if newLine < 0 {
				content = append(content, hunk[1:]...)
				hunk = nil
			} else {
				content = append(content, hunk[1:newLine+1]...)
				hunk = hunk[newLine+1:]
			}
		case '-':
			if newLine < 0 {
				hunk = nil
			} else {
				hunk = hunk[newLine+1:]
			}

		}
	}
	return content
}

// patseDiffHuskOrig parses diff husk into original file contents
func parseDiffHuskOrig(h *diff.Hunk) []byte {
	content := h.Body[:0]
	hunk := h.Body
	for len(hunk) > 0 {
		newLine := bytes.IndexByte(hunk, '\n')
		switch hunk[0] {
		case ' ', '-':
			if newLine < 0 {
				content = append(content, hunk[1:]...)
				hunk = nil
			} else {
				content = append(content, hunk[1:newLine+1]...)
				hunk = hunk[newLine+1:]
			}
		case '+':
			if newLine < 0 {
				hunk = nil
			} else {
				hunk = hunk[newLine+1:]
			}

		}
	}
	return content
}

// parseGitHashFromDiff returns the old and new file blob hashes
func parseGitHashFromDiff(f *diff.FileDiff) (plumbing.Hash, plumbing.Hash) {
	// TODO(jac): An upstream change to more easily expose this would be nice
	for _, v := range f.Extended {
		// index ABCD..EF12 100644
		if strings.HasPrefix(v, "index") {
			// ABCD..EF12
			i := strings.Split(v, " ")[1]
			// [ABCD, EF12]
			indices := strings.Split(i, "..")
			return plumbing.NewHash(indices[0]), plumbing.NewHash(indices[1])
		}
	}

	log.Panicf("Could not read file blob hashes for %s", f.NewName)
	// unreachable
	var x plumbing.Hash
	return x, x
}

// computeGitHash computes the git file blob hash of a document
func computeGitHash(doc zoekt.Document) plumbing.Hash {
	return plumbing.ComputeHash(plumbing.BlobObject, doc.Content)
}
