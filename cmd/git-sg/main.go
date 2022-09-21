package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/sourcegraph/zoekt/ignore"
)

func do(w io.Writer) error {
	r, err := openGitRepo()
	if err != nil {
		return err
	}

	head, err := r.Head()
	if err != nil {
		return err
	}

	commit, err := r.CommitObject(head.Hash())
	if err != nil {
		return err
	}

	root, err := r.TreeObject(commit.TreeHash)
	if err != nil {
		return err
	}

	// Gating this right now because I get inconsistent performance on my
	// macbook. Want to test on linux and larger repos.
	if os.Getenv("GIT_SG_BUFFER") != "" {
		log.Println("buffering output")
		bw := bufio.NewWriter(w)
		defer bw.Flush()
		w = bw
	}

	opts := &archiveOpts{
		Ignore: getIgnoreFilter(r, root),
		SkipContent: func(hdr *tar.Header) string {
			if hdr.Size > 2<<20 {
				return "large file"
			}
			return ""
		},
	}

	if os.Getenv("GIT_SG_FILTER") != "" {
		log.Println("filtering git archive output")
		return archiveFilter(w, r, root, opts)
	}

	return archiveWrite(w, r, root, opts)
}

type archiveOpts struct {
	// Ignore if true will exclude path from the archive
	Ignore func(path string) bool
	// SkipContent if returning a non-empty string will include an entry for
	// path but with no content. The PAX header SOURCEGRAPH.skip will contain
	// the returned string (a reason for skipping).
	SkipContent func(hdr *tar.Header) string
}

func archiveWrite(w io.Writer, repo *git.Repository, tree *object.Tree, opts *archiveOpts) error {
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

type archiveWriter struct {
	w    *tar.Writer
	opts *archiveOpts

	repo *git.Repository

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
			child, err := a.repo.TreeObject(e.Hash)
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

			a.stack = append(a.stack, item{entries: child.Entries, path: p})

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
	blob, err := a.repo.BlobObject(entry.Hash)
	if err != nil {
		log.Printf("failed to get blob object for %s %v: %v", path, entry.Hash, err)
		return nil
	}

	// TODO symlinks, mode, etc. Handle large Linkname
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     path,
		Size:     blob.Size,
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

func getIgnoreFilter(r *git.Repository, root *object.Tree) func(string) bool {
	m, err := parseIgnoreFile(r, root)
	if err != nil {
		// likely malformed, just log and ignore
		log.Printf("WARN: failed to parse sourcegraph ignore file: %v", err)
		return func(_ string) bool { return false }
	}

	return m.Match
}

func parseIgnoreFile(r *git.Repository, root *object.Tree) (*ignore.Matcher, error) {
	entry, err := root.FindEntry(ignore.IgnoreFile)
	if isNotExist(err) {
		return &ignore.Matcher{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to find %s: %w", ignore.IgnoreFile, err)
	}

	if !entry.Mode.IsFile() {
		return &ignore.Matcher{}, nil
	}

	blob, err := r.BlobObject(entry.Hash)
	if err != nil {
		return nil, err
	}

	reader, err := blob.Reader()
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	m, err := ignore.ParseIgnoreFile(reader)
	if err != nil {
		return nil, err
	}

	return m, nil
}

func isNotExist(err error) bool {
	if err == nil {
		return false
	}
	// go-git does not have an interface to check for not found, and can
	// returned a myraid of errors for not found depending on were along looking
	// for a file it failed (object, tree, entry, etc). So strings are the best
	// we can do.
	return os.IsNotExist(err) || strings.Contains(err.Error(), "not found")
}

func openGitRepo() (*git.Repository, error) {
	dir := os.Getenv("GIT_DIR")
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}

	fs := osfs.New(dir)
	if _, err := fs.Stat(git.GitDirName); err == nil {
		fs, err = fs.Chroot(git.GitDirName)
		if err != nil {
			return nil, err
		}
	}

	// TODO PERF try skip object caching since we don't need it for archive.
	s := filesystem.NewStorageWithOptions(fs, cache.NewObjectLRUDefault(), filesystem.Options{
		// PERF: important, otherwise we pay the cost of opening and closing
		// packfiles per object access and read.
		KeepDescriptors: true,
	})

	return git.Open(s, fs)
}

func main() {
	cpuprofile := flag.String("cpuprofile", "", "write cpu profile to `file`")
	memprofile := flag.String("memprofile", "", "write memory profile to `file`")
	flag.Parse()

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal("could not create CPU profile: ", err)
		}
		defer f.Close() // error handling omitted for example
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal("could not start CPU profile: ", err)
		}
		defer pprof.StopCPUProfile()
	}

	err := do(os.Stdout)
	if err != nil {
		log.Fatal(err)
	}

	if *memprofile != "" {
		f, err := os.Create(*memprofile)
		if err != nil {
			log.Fatal("could not create memory profile: ", err)
		}
		defer f.Close() // error handling omitted for example
		runtime.GC()    // get up-to-date statistics
		if err := pprof.WriteHeapProfile(f); err != nil {
			log.Fatal("could not write memory profile: ", err)
		}
	}
}
