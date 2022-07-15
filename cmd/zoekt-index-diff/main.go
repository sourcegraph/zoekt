// command zoekt-index-diff parses a unified diff from stdin to create a delta build
// the unified diff should have the following options specified:
// --full-index
//		Full file git hash
// -U<large number>
//		Include entire file as context in diff.
//		<large number> specifies max lines per file. Should be greater than longest file
// --no-prefix
//		Remove default a/ & b/ prefixes from file names
// --no-renames
//		Handle renames as separate file removal and addition
package main

import (
	"bufio"
	"flag"
	"log"
	"os"
	"strconv"

	"github.com/google/zoekt/build"
	"github.com/google/zoekt/cmd"
	"go.uber.org/automaxprocs/maxprocs"
)

type Options struct {
	Name     string
	RepoURL  string
	Branch   string
	Commit   string
	IndexDir string
	ID       uint32
	Archived bool
	Public   bool
	Fork     bool
	Priority float64
	DiffFile string
}

// bOptsFromOpts parses build options from CLI flags
func bOptsFromOpts(opts Options, bOpts *build.Options) {
	if opts.Name == "" {
		log.Panic("-name required")
	}

	if opts.ID == 0 {
		log.Panic("-id required")
	}

	if opts.Branch == "" {
		log.Panic("-branch required")
	}

	if opts.Commit == "" {
		log.Panic("-commit required")
	}

	bOpts.IndexDir = opts.IndexDir
	bOpts.IsDelta = true

	bOpts.RepositoryDescription.Name = opts.Name
	bOpts.RepositoryDescription.ID = opts.ID

	// Branch set must be the same for a delta build so read from shard
	bOpts.RepositoryDescription.Branches = getBranchSetFromShard(*bOpts)
	for i, b := range bOpts.RepositoryDescription.Branches {
		if b.Name == opts.Branch {
			bOpts.RepositoryDescription.Branches[i].Version = opts.Commit
			break
		}
	}
	bOpts.RepositoryDescription.URL = opts.RepoURL
	bOpts.RepositoryDescription.RawConfig = map[string]string{
		"repoid":   strconv.Itoa(int(opts.ID)),
		"priority": strconv.FormatFloat(opts.Priority, 'g', -1, 64),
		"public":   marshalBool(opts.Public),
		"fork":     marshalBool(opts.Fork),
		"archived": marshalBool(opts.Archived),
	}
}

func marshalBool(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func main() {
	var (
		// required
		name   = flag.String("name", "", "The repository name for the archive")
		urlRaw = flag.String("url", "", "The repository URL for the archive")
		branch = flag.String("branch", "", "The branch name for the archive")
		commit = flag.String("commit", "", "The commit sha. If incremental this will avoid updating shards already at commit")
		id     = flag.Uint("id", 0, "Sourcegraph repository ID")
		// optional
		indexDir = flag.String("index_dir", build.DefaultDir, "Index directory for *.zoekt files")
		archived = flag.Bool("archived", false, "Set repository `archived` flag")
		public   = flag.Bool("public", false, "Set repository `public` flag")
		fork     = flag.Bool("fork", false, "Set repository `fork` flag")
		priority = flag.Float64("priority", 0, "Set repository priority")
		// if specified read diff from file
		diffFile = flag.String("diff_file", "", "Read diff from file instead of stdin")
	)

	flag.Parse()

	// Tune GOMAXPROCS to match Linux container CPU quota.
	_, _ = maxprocs.Set()

	log.SetFlags(log.LstdFlags | log.Lshortfile)

	bOpts := cmd.OptionsFromFlags()

	opts := Options{
		Name:     *name,
		RepoURL:  *urlRaw,
		Branch:   *branch,
		Commit:   *commit,
		IndexDir: *indexDir,
		ID:       uint32(*id),
		Archived: *archived,
		Public:   *public,
		Fork:     *fork,
		Priority: *priority,
		DiffFile: *diffFile,
	}

	// Parse options
	bOptsFromOpts(opts, bOpts)

	// If diff_file specified read from file instead of stdin
	diff := os.Stdin
	if *diffFile != "" {
		if _, err := os.Stat(*diffFile); err != nil {
			log.Fatalf("Could not get file info %s; %s", *diffFile, err)
		}
		f, err := os.Open(*diffFile)
		if err != nil {
			log.Fatalf("Could not open file %s; %s", *diffFile, err)
		}
		defer f.Close()
		diff = f
	}

	r := bufio.NewReader(diff)
	err := index(r, opts.Branch, opts.Commit, *bOpts)
	if err != nil {
		log.Panicf("Failed to index: %v", err)
		return
	}
}
