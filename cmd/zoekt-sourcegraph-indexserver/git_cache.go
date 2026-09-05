package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sglog "github.com/sourcegraph/log"

	"github.com/sourcegraph/zoekt"
)

// gitRepoCache manages the acquisition and lifetime of shallow Git clones used
// by indexing jobs. Each job gets a fresh temporary clone, which is deleted
// when the job finishes. Callers hold the appropriate indexMutex lock to exclude
// concurrent use of the same repository directory.
type gitRepoCache struct{}

var gitCache gitRepoCache

// withFetchedRepo lends a ready bare repository to use and deletes it on return,
// including on failure or panic. The callback must not retain the directory.
func (cache gitRepoCache) withFetchedRepo(ctx context.Context, c gitIndexConfig, o *indexArgs, logger sglog.Logger, use func(string) error) error {
	gitDir, err := tmpGitDir(o.Name)
	if err != nil {
		return err
	}
	defer os.RemoveAll(gitDir) // best-effort cleanup

	if err := cache.fetch(ctx, gitDir, o, c, logger); err != nil {
		return err
	}
	return use(gitDir)
}

func initGitRepo(ctx context.Context, gitDir string, o *indexArgs, c gitIndexConfig) error {
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
	if err := c.runCmd(cmd); err != nil {
		return err
	}

	for _, header := range []string{
		"X-Sourcegraph-Actor-UID: internal",
		"X-Sourcegraph-Tenant-ID: " + strconv.Itoa(o.TenantID),
	} {
		cmd = exec.CommandContext(ctx, "git", "-C", gitDir, "config", "--add", "http.extraHeader", header)
		cmd.Stdin = &bytes.Buffer{}
		if err := c.runCmd(cmd); err != nil {
			return err
		}
	}
	return nil
}

func (gitRepoCache) fetch(ctx context.Context, gitDir string, o *indexArgs, c gitIndexConfig, logger sglog.Logger) error {
	if err := initGitRepo(ctx, gitDir, o, c); err != nil {
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

	runFetch := func(branches []zoekt.RepositoryBranch) error {
		// We shallow fetch each commit specified in zoekt.Branches. This requires
		// the server to have configured both uploadpack.allowAnySHA1InWant and
		// uploadpack.allowFilter. (See gitservice.go in the Sourcegraph repository)
		fetchArgs := []string{
			"-C", gitDir,
			"-c", "protocol.version=2",
			"fetch", "--depth=1", "--no-tags",
		}

		// Git's blob:limit filter excludes blobs whose size is >= the given limit,
		// while zoekt indexes files up to and including FileLimit bytes.
		if len(o.LargeFiles) == 0 {
			fetchArgs = append(fetchArgs, fmt.Sprintf("--filter=blob:limit=%d", int64(MaxFileSize)+1))
		}

		fetchArgs = append(fetchArgs, o.CloneURL)

		var commits []string
		for _, b := range branches {
			commits = append(commits, b.Version)
		}

		fetchArgs = append(fetchArgs, commits...)

		cmd := exec.CommandContext(ctx, "git", fetchArgs...)
		cmd.Stdin = &bytes.Buffer{}

		start := time.Now()
		err := c.runCmd(cmd)
		fetchDuration += time.Since(start)

		if err != nil {
			allFetchesSucceeded = false
			var bs []string
			for _, b := range branches {
				bs = append(bs, b.String())
			}

			formattedBranches := strings.Join(bs, ", ")
			return fmt.Errorf("fetching %s: %w", formattedBranches, err)
		}

		successfullyFetchedCommitsCount += len(commits)
		return nil
	}

	fetchPriorAndLatestCommits := func() error {
		prior, err := priorBranches(c, o)
		if err != nil {
			return err
		}

		var allBranches []zoekt.RepositoryBranch
		allBranches = append(allBranches, o.Branches...)
		allBranches = append(allBranches, prior...)

		return runFetch(allBranches)
	}

	fetchOnlyLatestCommits := func() error {
		return runFetch(o.Branches)
	}

	if o.UseDelta {
		err := fetchPriorAndLatestCommits()
		if err != nil {
			name := o.BuildOptions().RepositoryDescription.Name
			id := o.BuildOptions().RepositoryDescription.ID

			errorLog.Printf("delta build: failed to prepare delta build for %q (ID %d): failed to fetch both latest and prior commits: %s", name, id, err)
			err = fetchOnlyLatestCommits()
			if err != nil {
				return err
			}
		}
	} else {
		err := fetchOnlyLatestCommits()
		if err != nil {
			return err
		}
	}

	// We then create the relevant refs for each fetched commit.
	for _, b := range o.Branches {
		ref := b.Name
		if ref != "HEAD" {
			ref = "refs/heads/" + ref
		}
		cmd := exec.CommandContext(ctx, "git", "-C", gitDir, "update-ref", ref, b.Version)
		cmd.Stdin = &bytes.Buffer{}
		if err := c.runCmd(cmd); err != nil {
			return fmt.Errorf("failed update-ref %s to %s: %w", ref, b.Version, err)
		}
	}

	logger.Debug("successfully fetched git data",
		sglog.String("repo", o.Name),
		sglog.Uint32("id", o.RepoID),
		sglog.Int("commits_count", successfullyFetchedCommitsCount),
		sglog.Duration("duration", fetchDuration),
	)
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
