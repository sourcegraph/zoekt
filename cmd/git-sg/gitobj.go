package main

import (
	"io"

	"github.com/git-lfs/gitobj/v2"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type archiveWriterBlobGitObj struct {
	blob *gitobj.Blob
}

func (b archiveWriterBlobGitObj) Size() int64 {
	// TODO close if only asking size?
	return b.blob.Size
}

func (b archiveWriterBlobGitObj) Reader() (io.ReadCloser, error) {
	return b, nil
}

func (b archiveWriterBlobGitObj) Read(p []byte) (int, error) {
	return b.blob.Contents.Read(p)
}

func (b archiveWriterBlobGitObj) Close() error {
	return b.blob.Close()
}

type archiveWriterRepoGitObj struct {
	db *gitobj.ObjectDatabase
}

func (w archiveWriterRepoGitObj) TreeEntries(hash plumbing.Hash) ([]object.TreeEntry, error) {
	tree, err := w.db.Tree(hash[:])
	if err != nil {
		return nil, err
	}

	entries := make([]object.TreeEntry, len(tree.Entries))
	for i, e := range tree.Entries {
		copy(entries[i].Hash[:], e.Oid)
		entries[i].Mode = filemode.FileMode(e.Filemode)
		entries[i].Name = e.Name
	}

	return entries, nil
}

func (w archiveWriterRepoGitObj) Blob(hash plumbing.Hash) (archiveWriterBlob, error) {
	blob, err := w.db.Blob(hash[:])
	if err != nil {
		return nil, err
	}
	return archiveWriterBlobGitObj{
		blob: blob,
	}, nil
}
