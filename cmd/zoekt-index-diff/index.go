package main

import (
	"bytes"
	"io"
	"log"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
	"github.com/sourcegraph/go-diff/diff"
)

func getBranchSetFromShard(bOpts build.Options) []zoekt.RepositoryBranch {
	repo, ok, err := bOpts.FindRepositoryMetadata()
	if !ok {
		log.Fatalf("Could not read branches from disk: %v", err)
	}
	return repo.Branches
}

func existingShardPaths(bOpts build.Options) []string {
	shards := bOpts.FindAllShards()
	return shards
}

func removeBranch(branch string, branches []string) []string {
	for i, b := range branches {
		if branch == b {
			return append(branches[:i], branches[i+1:]...)
		}
	}
	return nil
}

func index(r io.Reader, branch string, sha string, bOpts build.Options) error {
	dr := diff.NewMultiFileDiffReader(r)

	d, err := zoekt.NewDocReader(existingShardPaths(bOpts))
	if err != nil {
		log.Fatalf("")
	}
	defer d.Close()
	// Debugging
	// d.ListDocs()
	// return nil

	b, err := build.NewBuilder(bOpts)
	if err != nil {
		return err
	}
	defer b.Finish()

	for {
		f, err := dr.ReadFile()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}

		if f.NewName == "/dev/null" {
			// File path removed
			fileRemoved(branch, f, b, d)
		} else if f.OrigName == "/dev/null" {
			// File path added
			fileAdded(branch, f, b, d)
		} else {
			// File content modified
			fileModified(branch, f, b, d)
		}

	}

	err = b.Finish()
	if err != nil {
		return err
	}

	return nil
}

func fileModified(branch string, f *diff.FileDiff, b *build.Builder, d *zoekt.DocReader) {
	c := parseDiffHuskNew(f.Hunks[0])
	oldHash, newHash := parseGitHashFromDiff(f)
	b.MarkFileAsChangedOrRemoved(f.NewName)

	docs := d.ReadDocs(f.NewName)
	hashes := make(map[plumbing.Hash]*zoekt.Document, len(docs))
	for _, doc := range docs {
		hashes[computeGitHash(doc)] = &doc
	}

	// Remove old version of file
	if doc, ok := hashes[oldHash]; ok {
		br := removeBranch(branch, doc.Branches)
		// Either old doc was unique, remove it. Or just remove from branches of doc
		if len(br) == 0 {
			delete(hashes, oldHash)
		} else {
			doc.Branches = br
		}
	}

	// Add new version of file
	if doc, ok := hashes[newHash]; ok {
		doc.Branches = append(doc.Branches, branch)
	} else {
		b.Add(zoekt.Document{
			Name:     f.NewName,
			Content:  c,
			Branches: []string{branch},
		})
	}

	// Re-add docs
	for _, doc := range hashes {
		b.Add(*doc)
	}
}

func fileAdded(branch string, f *diff.FileDiff, b *build.Builder, d *zoekt.DocReader) {
	c := parseDiffHuskNew(f.Hunks[0])
	b.MarkFileAsChangedOrRemoved(f.NewName)

	docs := d.ReadDocs(f.NewName)
	for i, doc := range docs {
		if bytes.Equal(c, doc.Content) {
			docs[i].Branches = append(doc.Branches, branch)

			// Re-add docs
			for _, doc := range docs[i:] {
				b.Add(doc)
			}
			return
		} else {
			b.Add(doc)
		}
	}

	// Unique to this branch
	b.Add(zoekt.Document{
		Name:     f.NewName,
		Content:  c,
		Branches: []string{branch},
	})
}

func fileRemoved(branch string, f *diff.FileDiff, b *build.Builder, d *zoekt.DocReader) {
	// Use original file contents for comparison
	c := parseDiffHuskOrig(f.Hunks[0])
	b.MarkFileAsChangedOrRemoved(f.OrigName)

	docs := d.ReadDocs(f.OrigName)
	for i, doc := range docs {
		if bytes.Equal(c, doc.Content) {
			br := removeBranch(branch, doc.Branches)

			// Document only existed on this branch
			if len(br) == 0 {
				docs = append(docs[:i], docs[i+1:]...)
			} else {
				docs[i].Branches = br
			}

			// Re-add docs
			for _, doc := range docs[i:] {
				b.Add(doc)
			}

			return
		} else {
			b.Add(doc)
		}
	}
}
