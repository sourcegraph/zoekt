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
	"time"

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
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

	// EnableIncrementalFetching indicates whether the experimental incremental fetching logic is enabled.
	EnableIncrementalFetching bool

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

// TODO: Perhaps this should be a general function? I think all but
// one of the call-sites of zoekt.ReadMetadataPath could be replaced with this.
type repoMetadataSource interface {
	// GetMetadata returns the repository metadata for the given build options,
	// or nil if the metadata couldn't be found.
	GetMetadata(o *build.Options) (*zoekt.Repository, error)
}

// TODO: should I just replace this with a function type?
type shardSource struct{}

func (*shardSource) GetMetadata(o *build.Options) (*zoekt.Repository, error) {
	shardFiles := o.FindAllShards()
	if len(shardFiles) == 0 {
		return nil, nil
	}

	fn := shardFiles[0] // all shard files contain the same metadata, so the first one is fine.
	repos, _, err := zoekt.ReadMetadataPath(fn)
	if err != nil {
		return nil, fmt.Errorf("reading shard metadata from %q: %w", fn, err)
	}

	for _, r := range repos {
		// A compound shard has repositories other than the one we're currently indexing.
		// Only return the metadata for the repository that we're looking for.
		if r.ID == o.RepositoryDescription.ID {
			return r, nil
		}
	}

	// We didn't find a matching repository even though we found a matching shard.
	// This should never happen.
	return nil, fmt.Errorf("unable to find repository metadata even though we found a matching shard at %q", fn)
}

func gitIndex(o *indexArgs, metadataSource repoMetadataSource, runCmd func(*exec.Cmd) error) error {
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

	allFetchesSucceeded := true
	totalFetchedCommits := 0
	var totalFetchDuration time.Duration

	if o.EnableIncrementalFetching {
		// If we're incrementally fetching, we also need to fetch the git data
		// for the commit that we most recently indexed (so that we can "git diff" later).

		metadata, err := metadataSource.GetMetadata(o.BuildOptions())
		if err != nil {
			log.Printf("failed to get repository metadata: %s", err)
		} else {
			var indexedCommits []string
			if metadata != nil {
				for _, b := range metadata.Branches {
					indexedCommits = append(indexedCommits, b.Version)
				}
			}

			// We need to handle fetches of invalid commits (commits that the remote doesn't know about).
			// When running, "git fetch A B ...", the whole fetch isn't performed if a single commit is invalid.
			//
			// We still want to be able to proceed in this case, since zoekt-git-index can still recover by
			// re-calculating the entire index from scratch. Therefore, we fetch these previously indexed commits separately
			// from the new commits. Unlike the fetches for the new commits, we don't fail outright if we can't fetch
			// these old ones.

			// TODO: we can parallelize these fetches if necessary, but I thought it'd be
			// simpler to do this sequentially at first.
			for _, c := range indexedCommits {
				start := time.Now()

				err := gitFetch(c)
				if err != nil {
					allFetchesSucceeded = false
					log.Printf("failed to fetch commit %q for repo %q: %s", c, o.Name, err)
				}

				totalFetchDuration += time.Since(start)
				totalFetchedCommits++
			}
		}

	}

	// fetch new requested commits from IndexArgs
	fetchStart := time.Now()
	err = gitFetch(commits...)

	totalFetchDuration += time.Since(fetchStart)
	totalFetchedCommits += len(commits)
	allFetchesSucceeded = allFetchesSucceeded && (err == nil)

	metricFetchDuration.WithLabelValues(strconv.FormatBool(allFetchesSucceeded), repoNameForMetric(o.Name)).Observe(totalFetchDuration.Seconds())
	if err != nil {
		return fmt.Errorf("fetching git data for commits %s: %w", strings.Join(commits, ", "), err)
	}

	debug.Printf("fetched git data for %q (%d commit(s)) in %s", o.Name, totalFetchedCommits, totalFetchDuration)

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
