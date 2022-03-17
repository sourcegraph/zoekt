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

	// DownloadLimitMBPS is the maximum MB/s to use when downloading the
	// archive.
	DownloadLimitMBPS string

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
	// runCmd, if not nil, is used to execute all external commands (such as calls to "git" or "zoekt-git-index")
	// that gitIndex may construct.
	//
	// If runCmd is nil, then (*exec.Cmd).Run() will be used instead.
	runCmd func(*exec.Cmd) error

	// getRepositoryMetadata, if not nil, returns the repository metadata for the
	// repository specified in args (or nil if the repository metadata couldn't be found).
	//
	// If getRepositoryMetadata is nil, then the repository metadata is retrieved
	// from any existing shards on disk.
	getRepositoryMetadata func(args *indexArgs) (*zoekt.Repository, error)
}

func gitIndex(o *indexArgs, c gitIndexConfig) error {
	if len(o.Branches) == 0 {
		return errors.New("zoekt-git-index requires 1 or more branches")
	}

	runCmd := func(cmd *exec.Cmd) error {
		return cmd.Run()
	}

	if c.runCmd != nil {
		runCmd = c.runCmd
	}

	buildOptions := o.BuildOptions()

	getRepositoryMetadata := func(args *indexArgs) (*zoekt.Repository, error) {
		return buildOptions.RepositoryMetadata()
	}

	if c.getRepositoryMetadata != nil {
		getRepositoryMetadata = c.getRepositoryMetadata
	}

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

	var fetchCommits = func(commits []string) error {
		fetchArgs := []string{"-C", gitDir, "-c", "protocol.version=2", "fetch", "--depth=1", o.CloneURL}
		fetchArgs = append(fetchArgs, commits...)

		cmd = exec.CommandContext(ctx, "git", fetchArgs...)
		cmd.Stdin = &bytes.Buffer{}

		return runCmd(cmd)
	}

	// We shallow fetch each commit specified in zoekt.Branches. This requires
	// the server to have configured both uploadpack.allowAnySHA1InWant and
	// uploadpack.allowFilter. (See gitservice.go in the Sourcegraph repository)
	var branchCommits []string
	for _, b := range o.Branches {
		branchCommits = append(branchCommits, b.Version)
	}

	commitCount := 0
	commitCount += len(branchCommits)

	fetchStart := time.Now()
	err = fetchCommits(branchCommits)
	fetchDuration := time.Since(fetchStart)

	if err != nil {
		metricFetchDuration.WithLabelValues("false", repoNameForMetric(o.Name)).Observe(fetchDuration.Seconds())
		return err
	}

	if o.UseDelta {
		existingRepository, err := getRepositoryMetadata(o)
		if err != nil {
			// TODO @ggilmore: This is an example of where we could try a non-delta build immediately. Should we special case this error to allow for
			// errors.As inspection, or should we rely on IndexState() eventually telling us that we need to fallback?
			return &deltaBuildError{
				repositoryName: buildOptions.RepositoryDescription.Name,
				repositoryID:   buildOptions.RepositoryDescription.ID,
				err:            fmt.Errorf("failed to get repository metadata: %w", err),
			}
		}

		if existingRepository == nil {
			// TODO @ggilmore: This is an example of where we could try a non-delta build immediately. Should we special case this error to allow for
			// errors.As inspection, or should we rely on IndexState() eventually telling us that we need to fallback?
			return &deltaBuildError{
				repositoryName: buildOptions.RepositoryDescription.Name,
				repositoryID:   buildOptions.RepositoryDescription.ID,
				err:            fmt.Errorf("no prior shards found"),
			}
		}

		// check delta build invariant - the branch names need to be the same
		switch build.CompareBranches(existingRepository.Branches, o.Branches) {
		case build.IndexStateBranchSet:
			var existingBranchNames []string
			for _, b := range existingRepository.Branches {
				existingBranchNames = append(existingBranchNames, b.Name)
			}

			var providedBranchNames []string
			for _, b := range o.Branches {
				providedBranchNames = append(providedBranchNames, b.Name)
			}

			// TODO @ggilmore: This is an example of where we could try a non-delta build immediately. Should we special case this error to allow for
			// errors.As inspection, or should we rely on IndexState() eventually telling us that we need to fallback?
			return &deltaBuildError{
				repositoryName: buildOptions.RepositoryDescription.Name,
				repositoryID:   buildOptions.RepositoryDescription.ID,
				err:            fmt.Errorf("set of branch names differs between existing repository (%s) and provided options (%s)", strings.Join(existingBranchNames, ", "), strings.Join(providedBranchNames, ", ")),
			}

		case build.IndexStateCorrupt:
			return &deltaBuildError{
				repositoryName: buildOptions.RepositoryDescription.Name,
				repositoryID:   buildOptions.RepositoryDescription.ID,
				err:            fmt.Errorf("either set of branches in existing repository (%s) and or in provided options (%s) is invalid", existingRepository.Branches, o.Branches),
			}
		}

		var priorCommits []string
		for _, b := range existingRepository.Branches {
			priorCommits = append(priorCommits, b.Version)
		}

		if len(priorCommits) > 0 {
			start := time.Now()
			err := fetchCommits(priorCommits)

			fetchDuration += time.Since(start)

			if err != nil {
				// TODO @ggilmore: This is an example of where we must try a non delta build immediately. IndexState() will never run into this situation. Should we try a normal build within this same function
				// invocation, or should we bubble this up to the caller?
				//
				// Think about this more later. a "normal" build could be accomplished by just setting UseDelta = false and not returning?

				// TODO @ggilmore: If we continue with a normal build here, is it right to still capture a "failed" fetch metric?
				metricFetchDuration.WithLabelValues("false", repoNameForMetric(o.Name)).Observe(fetchDuration.Seconds())

				return &deltaBuildError{
					repositoryName: buildOptions.RepositoryDescription.Name,
					repositoryID:   buildOptions.RepositoryDescription.ID,
					err:            fmt.Errorf("fetching prior commits: %w", err),
				}
			}

			// we should only update the total commit count if we're proceeding with a delta build
			commitCount += len(priorCommits)
		}
	}

	metricFetchDuration.WithLabelValues("true", repoNameForMetric(o.Name)).Observe(fetchDuration.Seconds())
	debug.Printf("fetched git data for %q (%d commit(s)) in %s", o.Name, commitCount, fetchDuration)

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

type deltaBuildError struct {
	repositoryName string
	repositoryID   uint32

	err error
}

func (e *deltaBuildError) Error() string {
	return fmt.Sprintf("preparing delta build for %q (ID %d): %s", e.repositoryName, e.repositoryID, e.err.Error())
}

func (e *deltaBuildError) Unwrap() error {
	return e.err
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
