// Command zoekt-archive-index indexes an archive.
//
// Example via github.com:
//
//   zoekt-archive-index -incremental -commit b57cb1605fd11ba2ecfa7f68992b4b9cc791934d -name github.com/gorilla/mux -strip_components 1 https://codeload.github.com/gorilla/mux/legacy.tar.gz/b57cb1605fd11ba2ecfa7f68992b4b9cc791934d
//
//   zoekt-archive-index -branch master https://github.com/gorilla/mux/commit/b57cb1605fd11ba2ecfa7f68992b4b9cc791934d
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/url"
	"reflect"
	"strings"

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
	"github.com/google/zoekt/cmd"
	"github.com/google/zoekt/gitindex"
	"go.uber.org/automaxprocs/maxprocs"
)

// stripComponents removes the specified number of leading path
// elements. Pathnames with fewer elements will return the empty string.
func stripComponents(path string, count int) string {
	for i := 0; path != "" && i < count; i++ {
		i := strings.Index(path, "/")
		if i < 0 {
			return ""
		}
		path = path[i+1:]
	}
	return path
}

// isGitOID checks if the revision is a git OID SHA string.
//
// Note: This doesn't mean the SHA exists in a repository, nor does it mean it
// isn't a ref. Git allows 40-char hexadecimal strings to be references.
func isGitOID(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		if !(('0' <= r && r <= '9') ||
			('a' <= r && r <= 'f') ||
			('A' <= r && r <= 'F')) {
			return false
		}
	}
	return true
}

type Options struct {
	Incremental bool

	Sources []Source
	Name    string
	RepoURL string
	Strip   int
}

type Source struct {
	Archive string
	Branch  string
	Commit  string
}

func (o *Options) SetDefaults() {
	// We guess based on the archive URL.
	for i := range o.Sources {
		src := &o.Sources[i]
		u, _ := url.Parse(src.Archive)
		if u == nil {
			continue
		}

		setRef := func(ref string) {
			if isGitOID(ref) && src.Commit == "" {
				src.Commit = ref
			}
			if !isGitOID(ref) && src.Branch == "" {
				src.Branch = ref
			}
		}

		switch u.Host {
		case "github.com", "codeload.github.com":
			// https://github.com/octokit/octokit.rb/commit/3d21ec53a331a6f037a91c368710b99387d012c1
			// https://github.com/octokit/octokit.rb/blob/master/README.md
			// https://github.com/octokit/octokit.rb/tree/master/lib
			// https://codeload.github.com/octokit/octokit.rb/legacy.tar.gz/master
			parts := strings.Split(u.Path, "/")
			if len(parts) > 2 && o.Name == "" {
				o.Name = fmt.Sprintf("github.com/%s/%s", parts[1], parts[2])
				o.RepoURL = fmt.Sprintf("https://github.com/%s/%s", parts[1], parts[2])
			}
			if len(parts) > 4 {
				setRef(parts[4])
				if u.Host == "github.com" {
					src.Archive = fmt.Sprintf("https://codeload.github.com/%s/%s/legacy.tar.gz/%s", parts[1], parts[2], parts[4])
				}
			}
			o.Strip = 1
		case "api.github.com":
			// https://api.github.com/repos/octokit/octokit.rb/tarball/master
			parts := strings.Split(u.Path, "/")
			if len(parts) > 2 && o.Name == "" {
				o.Name = fmt.Sprintf("github.com/%s/%s", parts[1], parts[2])
				o.RepoURL = fmt.Sprintf("https://github.com/%s/%s", parts[1], parts[2])
			}
			if len(parts) > 5 {
				setRef(parts[5])
			}
			o.Strip = 1
		}
	}
}

func do(opts Options, bopts build.Options) error {
	opts.SetDefaults()

	if opts.Name == "" && opts.RepoURL == "" {
		return errors.New("-name or -url required")
	}
	for _, src := range opts.Sources {
		if src.Branch == "" {
			return fmt.Errorf("-branch required with %d space seperated branch names", len(opts.Sources))
		}
	}

	if opts.Name != "" {
		bopts.RepositoryDescription.Name = opts.Name
	}
	if opts.RepoURL != "" {
		u, err := url.Parse(opts.RepoURL)
		if err != nil {
			return err
		}
		if err := gitindex.SetTemplatesFromOrigin(&bopts.RepositoryDescription, u); err != nil {
			return err
		}
	}
	bopts.SetDefaults()
	for _, src := range opts.Sources {
		bopts.RepositoryDescription.Branches = append(bopts.RepositoryDescription.Branches, zoekt.RepositoryBranch{Name: src.Branch, Version: src.Commit})
	}

	if opts.Incremental {
		versions := bopts.IndexVersions()
		if reflect.DeepEqual(versions, bopts.RepositoryDescription.Branches) {
			return nil
		}
	}

	bopts.RepositoryDescription.Source = opts.Sources[0].Archive
	builder, err := build.NewBuilder(bopts)
	if err != nil {
		return err
	}

	// TODO Do document deduplication. This is a naive way to create an index
	// with multiple branches, since it doesn't dedup documents.
	for _, src := range opts.Sources {
		a, err := openArchive(src.Archive)
		if err != nil {
			return err
		}
		defer a.Close()

		brs := []string{src.Branch}

		add := func(f *File) error {
			// We do not index large files
			if f.Size > int64(bopts.SizeMax) && !bopts.IgnoreSizeMax(f.Name) {
				return nil
			}

			name := stripComponents(f.Name, opts.Strip)
			if name == "" {
				return nil
			}

			r, err := f.Open()
			if err != nil {
				return err
			}
			defer r.Close()

			contents, err := ioutil.ReadAll(r)
			if err != nil {
				return err
			}

			return builder.Add(zoekt.Document{
				Name:     name,
				Content:  contents,
				Branches: brs,
			})
		}

		for {
			f, err := a.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}

			if err := add(f); err != nil {
				return err
			}
		}
	}

	return builder.Finish()
}

func split(s string, n int) []string {
	var parts []string
	if s != "" {
		parts = strings.Split(s, " ")
	}
	for len(parts) < n {
		parts = append(parts, "")
	}
	return parts
}

func main() {
	var (
		incremental = flag.Bool("incremental", true, "only index changed repositories")

		name   = flag.String("name", "", "The repository name for the archive")
		urlRaw = flag.String("url", "", "The repository URL for the archive")
		branch = flag.String("branch", "", "The branch name for the archive. If passing multiple archives, space seperated branch names.")
		commit = flag.String("commit", "", "The commit sha for the archive. If passing multiple archives, space seperated commit names. If incremental this will avoid updating shards already at commit")
		strip  = flag.Int("strip_components", 0, "Remove the specified number of leading path elements. Pathnames with fewer elements will be silently skipped.")
	)
	flag.Parse()

	// Tune GOMAXPROCS to match Linux container CPU quota.
	maxprocs.Set()

	log.SetFlags(log.LstdFlags | log.Lshortfile)

	if len(flag.Args()) < 1 {
		log.Fatal("expected argument(s) for archive location")
	}

	var sources []Source
	branches := split(*branch, len(flag.Args()))
	commits := split(*commit, len(flag.Args()))
	for i, archive := range flag.Args() {
		sources = append(sources, Source{
			Archive: archive,
			Branch:  branches[i],
			Commit:  commits[i],
		})
	}

	bopts := cmd.OptionsFromFlags()
	opts := Options{
		Incremental: *incremental,

		Sources: sources,
		Name:    *name,
		RepoURL: *urlRaw,
		Strip:   *strip,
	}

	if err := do(opts, *bopts); err != nil {
		log.Fatal(err)
	}
}
