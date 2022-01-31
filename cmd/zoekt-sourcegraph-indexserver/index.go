package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
	"golang.org/x/sync/semaphore"
)

// IndexOptions are the options that Sourcegraph can set via it's search
// configuration endpoint.
type IndexOptions struct {
	// LargeFiles is a slice of glob patterns where matching file paths should
	// be indexed regardless of their size. The pattern syntax can be found
	// here: https://golang.org/pkg/path/filepath/#Match.
	LargeFiles []string

	// Symbols if true will make zoekt index the output of ctags.
	Symbols bool

	// Branches is a slice of branches to index.
	Branches []zoekt.RepositoryBranch

	// RepoID is the Sourcegraph Repository ID.
	RepoID uint32

	// Name is the Repository Name.
	Name string

	// CloneURL is the internal clone URL for Name.
	CloneURL string

	// Priority indicates ranking in results, higher first.
	Priority float64

	// Public is true if the repository is public.
	Public bool

	// Fork is true if the repository is a fork.
	Fork bool

	// Archived is true if the repository is archived.
	Archived bool
}

// indexArgs represents the arguments we pass to zoekt-git-index
type indexArgs struct {
	IndexOptions

	// Incremental indicates to skip indexing if already indexed.
	Incremental bool

	// IndexDir is the index directory to store the shards.
	IndexDir string

	// Parallelism is the number of shards to compute in parallel.
	Parallelism int

	// FileLimit is the maximum size of a file
	FileLimit int

	// DownloadLimitMBPS is the maximum MB/s to use when downloading the
	// archive.
	DownloadLimitMBPS string
}

func TODOReplaceuseIncrementalFetching() bool {
	return false
}

// BuildOptions returns a build.Options represented by indexArgs. Note: it
// doesn't set fields like repository/branch.
func (o *indexArgs) BuildOptions() *build.Options {
	return &build.Options{
		// It is important that this RepositoryDescription exactly matches what
		// the indexer we call will produce. This is to ensure that
		// IncrementalSkipIndexing and IndexState can correctly calculate if
		// nothing needs to be done.
		RepositoryDescription: zoekt.Repository{
			ID:       uint32(o.IndexOptions.RepoID),
			Name:     o.Name,
			Branches: o.Branches,
			RawConfig: map[string]string{
				"repoid":   strconv.Itoa(int(o.IndexOptions.RepoID)),
				"priority": strconv.FormatFloat(o.Priority, 'g', -1, 64),
				"public":   marshalBool(o.Public),
				"fork":     marshalBool(o.Fork),
				"archived": marshalBool(o.Archived),
			},
		},
		IndexDir:         o.IndexDir,
		Parallelism:      o.Parallelism,
		SizeMax:          o.FileLimit,
		LargeFiles:       o.LargeFiles,
		CTagsMustSucceed: o.Symbols,
		DisableCTags:     !o.Symbols,
	}
}

func marshalBool(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func (o *indexArgs) String() string {
	s := fmt.Sprintf("%d %s", o.RepoID, o.Name)
	for i, b := range o.Branches {
		if i == 0 {
			s = fmt.Sprintf("%s@%s=%s", s, b.Name, b.Version)
		} else {
			s = fmt.Sprintf("%s,%s=%s", s, b.Name, b.Version)
		}
	}
	return s
}

func gitIndex(o *indexArgs, runCmd func(*exec.Cmd) error) error {
	if len(o.Branches) == 0 {
		return errors.New("zoekt-git-index requires 1 or more branches")
	}

	buildOptions := o.BuildOptions()

	// An index should never take longer than an hour.
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	gitDir, err := tmpGitDir(o.Name)
	if err != nil {
		return err
	}
	// We intentionally leave behind gitdir if indexing failed so we can
	// investigate. This is only during the experimental phase of indexing a
	// clone. So don't defer os.RemoveAll here

	// Create a repo to fetch into
	cmd := exec.CommandContext(ctx, "git",
		// use a random default branch. This is so that HEAD isn't a symref to a
		// branch that is indexed. For example if you are indexing
		// HEAD,master. Then HEAD would be pointing to master by default.
		"-c", "init.defaultBranch=nonExistentBranchBB0FOFCH32",
		"init",
		// we don't need a working copy
		"--bare",
		gitDir)
	cmd.Stdin = &bytes.Buffer{}
	if err := runCmd(cmd); err != nil {
		return err
	}

	fetchStart := time.Now()

	var gitFetch = func(commits ...string) error {

		// We shallow fetch each commit specified in zoekt.Branches. This requires
		// the server to have configured both uploadpack.allowAnySHA1InWant and
		// uploadpack.allowFilter. (See gitservice.go in the Sourcegraph repository)
		args := []string{"-C", gitDir, "-c", "protocol.version=2", "fetch", "--depth=1", o.CloneURL}
		args = append(args, commits...)

		cmd = exec.CommandContext(ctx, "git", args...)
		cmd.Stdin = &bytes.Buffer{}

		return runCmd(cmd)
	}

	var commits []string
	for _, b := range o.Branches {
		commits = append(commits, b.Version)
	}

	if TODOReplaceuseIncrementalFetching() {
		// If we're incrementally fetching, we also need to fetch the git data
		// for the commit that we most recently indexed (so that we can "git diff" later).

		indexedCommits := make(map[string]struct{})

		// walk through the metadata for the repository's shards
		for _, fn := range buildOptions.FindAllShards() {
			repos, _, err := zoekt.ReadMetadataPathAlive(fn)
			if err != nil {
				return fmt.Errorf("reading shard metadata from %q: %w", fn, err)
			}

			// extract all the branches from the metadata
			for _, r := range repos {
				if r.ID != o.RepoID {
					// a compound shard has repos other than the one we're currently indexing -
					// skip these
					continue
				}

				for _, b := range r.Branches {
					indexedCommits[b.Version] = struct{}{}
				}
			}
		}

		// Keegan brought up the point that we need to handle fetches of invalid commits
		// (commits that the repo doesn't know about).
		// If a single commit in "git fetch A B ..." is invalid, the whole fetch isn't performed.
		// We still want to be able to proceed in this case, since zoekt-git-index can still recover by re-calculating the entire index from scratch.

		// limit to 4 conccurent fetches at a time
		// TODO: replace this with an option or env var
		sem := semaphore.NewWeighted(4)

		var wg sync.WaitGroup
		for c, _ := range indexedCommits {
			sem.Acquire(ctx, 1)
			wg.Add(1)

			go func(commit string) {
				defer sem.Release(1)
				defer wg.Done()

				err := gitFetch(commit)
				if err != nil {
					log.Printf("failed to fetch ref %q for repo %q: %w", commit, o.Name, err)
				}
			}(c)
		}
		wg.Done()

		// TODO: fix fetch metrics for incremental case
		// TODO: consider whether we can make non-incremental and incremental fetching use same code path
		// (e.g. issue one git fetch commend per commit - but we might want to turn that off)
	}

	err = gitFetch(commits...)
	fetchDuration := time.Since(fetchStart)
	if err != nil {
		metricFetchDuration.WithLabelValues("false", repoNameForMetric(o.Name)).Observe(fetchDuration.Seconds())
		return err
	}

	metricFetchDuration.WithLabelValues("true", repoNameForMetric(o.Name)).Observe(fetchDuration.Seconds())
	debug.Printf("fetched git data for %q (%d commit(s)) in %s", o.Name, len(commits), fetchDuration)

	// We then create the relevant refs for each fetched commit.
	for _, b := range o.Branches {
		ref := b.Name
		if ref != "HEAD" {
			ref = "refs/heads/" + ref
		}
		// zoekt-git-index needs to be able to read the old shards on disk and be able to determine what has changed.
		//
		// assuming that we have the old commits in the local repository
		// then zoekt-git-index can purely look as inputs:
		// - the current shards => RepositoryBranches used to be
		// - repository => RepositoryBranches should be
		//
		// zoekt-git-index can do git diff on repo
		//
		// current shards give them the commit hashes, so zoekt-git-index can find out the changed paths using that and the repository.
		cmd = exec.CommandContext(ctx, "git", "-C", gitDir, "update-ref", ref, b.Version)
		cmd.Stdin = &bytes.Buffer{}
		if err := runCmd(cmd); err != nil {
			return fmt.Errorf("failed update-ref %s to %s: %w", ref, b.Version, err)
		}
	}

	// create git config with options
	type configKV struct{ Key, Value string }
	config := []configKV{{
		// zoekt.name is used by zoekt-git-index to set the repository name.
		Key:   "name",
		Value: o.Name,
	}}
	for k, v := range buildOptions.RepositoryDescription.RawConfig {
		config = append(config, configKV{Key: k, Value: v})
	}
	sort.Slice(config, func(i, j int) bool {
		return config[i].Key < config[j].Key
	})

	// write config to repo
	for _, kv := range config {
		cmd = exec.CommandContext(ctx, "git", "-C", gitDir, "config", "zoekt."+kv.Key, kv.Value)
		cmd.Stdin = &bytes.Buffer{}
		if err := runCmd(cmd); err != nil {
			return err
		}
	}

	args := []string{
		"-submodules=false",
	}

	// Even though we check for incremental in this process, we still pass it
	// in just in case we regress in how we check in process. We will still
	// notice thanks to metrics and increased load on gitserver.
	if o.Incremental {
		args = append(args, "-incremental")
	}

	var branches []string
	for _, b := range o.Branches {
		branches = append(branches, b.Name)
	}
	args = append(args, "-branches", strings.Join(branches, ","))

	args = append(args, buildOptions.Args()...)
	args = append(args, gitDir)

	cmd = exec.CommandContext(ctx, "zoekt-git-index", args...)
	cmd.Stdin = &bytes.Buffer{}
	if err := runCmd(cmd); err != nil {
		return err
	}

	// Do not return error, since we have successfully indexed. Just log it
	if err := os.RemoveAll(gitDir); err != nil {
		log.Printf("WARN: failed to cleanup %s after successfully indexing %s: %v", gitDir, o.String(), err)
	}

	return nil
}

func tmpGitDir(name string) (string, error) {
	abs := url.QueryEscape(name)
	if len(abs) > 200 {
		h := sha1.New()
		_, _ = io.WriteString(h, abs)
		abs = abs[:200] + fmt.Sprintf("%x", h.Sum(nil))[:8]
	}
	dir := filepath.Join(os.TempDir(), abs+".git")
	if _, err := os.Stat(dir); err == nil {
		if err := os.RemoveAll(dir); err != nil {
			return "", err
		}
	}
	return dir, nil
}
