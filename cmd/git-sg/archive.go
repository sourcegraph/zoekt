package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"io"
	"log"
	"sync"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type archiveOpts struct {
	// Ignore if true will exclude path from the archive
	Ignore func(path string) bool
	// SkipContent if returning a non-empty string will include an entry for
	// path but with no content. The PAX header SOURCEGRAPH.skip will contain
	// the returned string (a reason for skipping).
	SkipContent func(hdr *tar.Header) string
}

func archiveWrite(w io.Writer, repo archiveWriterRepo, tree *object.Tree, opts *archiveOpts) error {
	a := &archiveWriter{
		w:    tar.NewWriter(w),
		repo: repo,
		opts: opts,

		stack: []item{{entries: tree.Entries, path: ""}},

		// 32*1024 is the same size used by io.Copy
		buf: make([]byte, 32*1024),
	}

	for len(a.stack) > 0 {
		item := a.stack[len(a.stack)-1]
		a.stack = a.stack[:len(a.stack)-1]

		err := a.writeTree(item.entries, item.path)
		if err != nil {
			_ = a.w.Close()
			return err
		}
	}

	return a.w.Close()
}

type item struct {
	entries []object.TreeEntry
	path    string
}

type archiveWriterBlob interface {
	Size() int64
	Reader() (io.ReadCloser, error)
	Close() error
}

type archiveWriterRepo interface {
	TreeEntries(plumbing.Hash) ([]object.TreeEntry, error)
	Blob(plumbing.Hash) (archiveWriterBlob, error)
}

type archiveWriter struct {
	w    *tar.Writer
	opts *archiveOpts

	repo archiveWriterRepo

	stack []item

	buf []byte
}

func (a *archiveWriter) writeTree(entries []object.TreeEntry, path string) error {
	for _, e := range entries {
		var p string
		if e.Mode == filemode.Dir {
			p = path + e.Name + "/"
		} else {
			p = path + e.Name
		}

		if a.opts.Ignore(p) {
			continue
		}

		switch e.Mode {
		case filemode.Dir:
			child, err := a.repo.TreeEntries(e.Hash)
			if err != nil {
				log.Printf("failed to fetch tree object for %s %v: %v", p, e.Hash, err)
				continue
			}

			if err := a.w.WriteHeader(&tar.Header{
				Typeflag: tar.TypeDir,
				Name:     p,
				Mode:     0777,
				Format:   tar.FormatPAX, // TODO ?
			}); err != nil {
				return err
			}

			a.stack = append(a.stack, item{entries: child, path: p})

		case filemode.Deprecated, filemode.Executable, filemode.Regular, filemode.Symlink:
			if err := a.writeRegularTreeEntry(e, p); err != nil {
				return err
			}

		case filemode.Submodule:
			// TODO what do?
			continue

		default:
			log.Printf("WARN: unexpected filemode %+v", e)
		}
	}

	return nil
}

func (a *archiveWriter) writeRegularTreeEntry(entry object.TreeEntry, path string) error {
	blob, err := a.repo.Blob(entry.Hash)
	if err != nil {
		log.Printf("failed to get blob object for %s %v: %v", path, entry.Hash, err)
		return nil
	}
	defer blob.Close()

	// TODO symlinks, mode, etc. Handle large Linkname
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     path,
		Size:     blob.Size(),
		Mode:     0666,

		Format: tar.FormatPAX, // TODO ?
	}

	if reason := a.opts.SkipContent(hdr); reason != "" {
		return a.writeSkipHeader(hdr, reason)
	}

	r, err := blob.Reader()
	if err != nil {
		log.Printf("failed to read blob object for %s %v: %v", path, entry.Hash, err)
		return nil
	}

	// TODO confirm it is fine to call Close twice. From initial reading of
	// go-git it relies on io.Pipe for readers, so this should be fine.
	defer r.Close()

	// Heuristic: Assume file is binary if first 256 bytes contain a 0x00.
	blobSample := a.buf[:256]
	if n, err := io.ReadAtLeast(r, blobSample, 256); err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		log.Printf("failed to read blob object for %s %v: %v", path, entry.Hash, err)
		return nil
	} else {
		blobSample = blobSample[:n]
	}

	// TODO instead of just binary, should we only allow utf8? utf.Valid
	// works except for the fact we may be invalid utf8 at the 256 boundary
	// since we cut it off. So will need to copypasta that.
	if bytes.IndexByte(blobSample, 0x00) >= 0 {
		return a.writeSkipHeader(hdr, "binary")
	}

	if err := a.w.WriteHeader(hdr); err != nil {
		return err
	}

	// We read some bytes from r already, first write those.
	if _, err := a.w.Write(blobSample); err != nil {
		return err
	}

	// Write out the rest of r.
	if _, err := io.CopyBuffer(a.w, r, a.buf); err != nil {
		return err
	}

	return r.Close()
}

func (a *archiveWriter) writeSkipHeader(hdr *tar.Header, reason string) error {
	hdr.PAXRecords = map[string]string{"SG.skip": reason}
	hdr.Size = 0 // clear out size since we won't write the body
	return a.w.WriteHeader(hdr)
}

type archiveWriterBlobGoGit struct {
	blob *object.Blob
}

func (b archiveWriterBlobGoGit) Size() int64 {
	return b.blob.Size
}

func (b archiveWriterBlobGoGit) Reader() (io.ReadCloser, error) {
	return b.blob.Reader()
}

func (b archiveWriterBlobGoGit) Close() error {
	return nil
}

type archiveWriterRepoGoGit git.Repository

func (repo *archiveWriterRepoGoGit) TreeEntries(hash plumbing.Hash) ([]object.TreeEntry, error) {
	tree, err := (*git.Repository)(repo).TreeObject(hash)
	if err != nil {
		return nil, err
	}
	return tree.Entries, nil
}

func (repo *archiveWriterRepoGoGit) Blob(hash plumbing.Hash) (archiveWriterBlob, error) {
	blob, err := (*git.Repository)(repo).BlobObject(hash)
	if err != nil {
		return nil, err
	}
	return archiveWriterBlobGoGit{blob: blob}, nil
}

type archiveWriterBlobCatFile struct {
	catFile *gitCatFileBatch
	info    gitCatFileBatchInfo
}

func (b archiveWriterBlobCatFile) Size() int64 {
	return b.info.Size
}

func (b archiveWriterBlobCatFile) Reader() (io.ReadCloser, error) {
	_, err := b.catFile.Contents(b.info.Hash)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(b.catFile), nil
}

func (b archiveWriterBlobCatFile) Close() error {
	return nil
}

type archiveWriterRepoCatFile struct {
	catFile *gitCatFileBatch
}

var bufPool = sync.Pool{
	New: func() interface{} {
		return bufio.NewReader(nil)
	},
}

func (w archiveWriterRepoCatFile) TreeEntries(hash plumbing.Hash) ([]object.TreeEntry, error) {
	_, err := w.catFile.Contents(hash)
	if err != nil {
		return nil, err
	}

	var entries []object.TreeEntry

	// Copy-pasta from go-git/plumbing/object/tree.go
	r := bufPool.Get().(*bufio.Reader)
	defer bufPool.Put(r)
	r.Reset(w.catFile)
	for {
		str, err := r.ReadString(' ')
		if err != nil {
			if err == io.EOF {
				break
			}

			return nil, err
		}
		str = str[:len(str)-1] // strip last byte (' ')

		mode, err := filemode.New(str)
		if err != nil {
			return nil, err
		}

		name, err := r.ReadString(0)
		if err != nil && err != io.EOF {
			return nil, err
		}

		var hash plumbing.Hash
		if _, err = io.ReadFull(r, hash[:]); err != nil {
			return nil, err
		}

		baseName := name[:len(name)-1]
		entries = append(entries, object.TreeEntry{
			Hash: hash,
			Mode: mode,
			Name: baseName,
		})
	}

	return entries, nil
}

func (w archiveWriterRepoCatFile) Blob(hash plumbing.Hash) (archiveWriterBlob, error) {
	info, err := w.catFile.Info(hash)
	if err != nil {
		return nil, err
	}
	return archiveWriterBlobCatFile{
		catFile: w.catFile,
		info:    info,
	}, nil
}
