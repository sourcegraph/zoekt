package main

import (
	"archive/tar"
	"bufio"
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
