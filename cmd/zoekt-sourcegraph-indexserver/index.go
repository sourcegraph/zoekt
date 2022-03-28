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

	// IndexDir is the index directory to store the shards.
	IndexDir string

	// Parallelism is the number of shards to compute in parallel.
	Parallelism int

	// FileLimit is the maximum size of a file
	FileLimit int

	// UseDelta is true if we want to use the new delta indexer. This should
	// only be true for repositories we explicitly enable.
	UseDelta bool
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
		IsDelta:          o.UseDelta,
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

type gitIndexConfig struct {
	// runCmd is the function that's used to execute all external commands (such as calls to "git" or "zoekt-git-index")
	// that gitIndex may construct.
	runCmd func(*exec.Cmd) error

	// findRepositoryMetadata is the function that returns the repository metadata for the
	// repository specified in args. 'ok' is false if the repository's metadata
	// couldn't be found or if an error occurred.
	//
	// The primary purpose of this configuration option is to be able to provide a stub
	// implementation for this in our test suite. All other callers should use build.Options.FindRepositoryMetadata().
	findRepositoryMetadata func(args *indexArgs) (repository *zoekt.Repository, ok bool, err error)
}

func gitIndex(c gitIndexConfig, o *indexArgs) error {
	if len(o.Branches) == 0 {
		return errors.New("zoekt-git-index requires 1 or more branches")
	}

	if c.runCmd == nil {
		return errors.New("runCmd in provided configuration was nil - a function must be provided")
	}
	runCmd := c.runCmd

	if c.findRepositoryMetadata == nil {
		return errors.New("findRepositoryMetadata in provided configuration was nil - a function must be provided")
	}
	findRepositoryMetadata := c.findRepositoryMetadata

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

	var fetchDuration time.Duration
	successfullyFetchedCommitsCount := 0
	allFetchesSucceeded := true

	defer func() {
		success := strconv.FormatBool(allFetchesSucceeded)
		name := repoNameForMetric(o.Name)
		metricFetchDuration.WithLabelValues(success, name).Observe(fetchDuration.Seconds())
	}()

	var fetchCommits = func(commits []string) error {
		// We shallow fetch each commit specified in zoekt.Branches. This requires
		// the server to have configured both uploadpack.allowAnySHA1InWant and
		// uploadpack.allowFilter. (See gitservice.go in the Sourcegraph repository)
		fetchArgs := []string{"-C", gitDir, "-c", "protocol.version=2", "fetch", "--depth=1", o.CloneURL}
		fetchArgs = append(fetchArgs, commits...)

		cmd = exec.CommandContext(ctx, "git", fetchArgs...)
		cmd.Stdin = &bytes.Buffer{}

		start := time.Now()
		err := runCmd(cmd)
		fetchDuration += time.Since(start)

		if err != nil {
			allFetchesSucceeded = false
			return err
		}

		successfullyFetchedCommitsCount += len(commits)
		return nil
	}

	var branchCommits []string
	for _, b := range o.Branches {
		branchCommits = append(branchCommits, b.Version)
	}

	err = fetchCommits(branchCommits)
	if err != nil {
		return err
	}

	if o.UseDelta {
		// Try fetching prior commits for delta builds
		// If we're unable to fetch prior commits, we continue anyway
		// knowing that zoekt-git-index will fall back to a "full" normal build

		var priorCommits []string

		existingRepository, found, err := findRepositoryMetadata(o)
		if err != nil {
			return fmt.Errorf("delta build: failed to get repository metadata: %w", err)
		}

		if found {
			for _, b := range existingRepository.Branches {
				priorCommits = append(priorCommits, b.Version)
			}
		}

		if len(priorCommits) > 0 {
			err = fetchCommits(priorCommits)
			if err != nil {
				repositoryName := buildOptions.RepositoryDescription.Name
				repositoryID := buildOptions.RepositoryDescription.ID

				var formattedCommits []string
				for _, b := range existingRepository.Branches {
					formattedCommits = append(formattedCommits, fmt.Sprintf("%s@%s", b.Name, b.Version))
				}

				log.Printf("delta build: failed to prepare delta build for %q (ID %d): failed to fetch prior commits (%s): %s", repositoryName, repositoryID, strings.Join(formattedCommits, ", "), err)
			}
		}
	}

	debug.Printf("successfully fetched git data for %q (%d commit(s)) in %s", o.Name, successfullyFetchedCommitsCount, fetchDuration)

	// We then create the relevant refs for each fetched commit.
	for _, b := range o.Branches {
		ref := b.Name
		if ref != "HEAD" {
			ref = "refs/heads/" + ref
		}
		cmd = exec.CommandContext(ctx, "git", "-C", gitDir, "update-ref", ref, b.Version)
		cmd.Stdin = &bytes.Buffer{}
		if err := runCmd(cmd); err != nil {
			return fmt.Errorf("failed update-ref %s to %s: %w", ref, b.Version, err)
		}
	}

	// create git gitConfiguration with options
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

	// write gitConfiguration to repo
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

	if o.UseDelta {
		args = append(args, "-delta")
	}

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
