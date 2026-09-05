package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	sglog "github.com/sourcegraph/log"

	"github.com/sourcegraph/zoekt"
)

// gitRepoCache manages the acquisition and lifetime of shallow Git clones.
// Selected monorepos keep their shallow bare clones between index jobs to avoid
// repeatedly transferring unchanged objects. Ordinary Git GC is not sufficient
// to bound this cache: repacking partial clones retains unreachable objects in
// promisor packs. Instead, expire clones seven days after creation (not access).
// Clones and metadata live under the indexserver tmp root, so setupTmpDir clears
// both on restart. Paths are resolved at use time, after setupTmpDir sets TMPDIR.
// Callers hold the appropriate indexMutex lock to exclude concurrent use/cleanup;
// the cache provides no synchronization or background Git maintenance.
//
// The metadata file is a completion marker: remove it before mutating the clone
// and write it only after success. An interrupted job cannot leave a reusable
// clone with stale locks or partially updated refs. Metadata lives outside Git's
// config because this acquisition policy must not affect shard/index identity.
type gitRepoCache struct{}

var gitCache gitRepoCache

const gitRepoCacheMaxAge = 7 * 24 * time.Hour

const gitRepoCacheMetadata = "zoekt-cache.json"

type gitRepoCacheEntry struct {
	Created  time.Time
	RepoID   uint32
	CloneURL string
	Filtered bool
	Branches []zoekt.RepositoryBranch
}

// withFetchedRepo lends a ready bare repository to use. After use succeeds,
// cached clones are marked reusable and temporary clones are deleted. Any
// failure (including a panic) discards the clone; cleanup errors are logged
// without hiding the job error. The callback must not retain the directory.
func (cache gitRepoCache) withFetchedRepo(ctx context.Context, c gitIndexConfig, o *indexArgs, logger sglog.Logger, use func(string) error) error {
	var gitDir string
	var cached gitRepoCacheEntry
	var err error
	if o.CacheGitRepo {
		gitDir, cached, err = cache.prepare(o, time.Now())
	} else {
		gitDir, err = tmpGitDir(o.Name)
	}
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			if err := os.RemoveAll(gitDir); err != nil {
				logger.Warn("failed to remove git clone", sglog.String("path", gitDir), sglog.Error(err))
			}
		}
	}()

	if o.CacheGitRepo {
		// Drop removed refs before installing new ones, including transitions
		// such as "release" to "release/v2" that conflict in Git's ref namespace.
		if err := removeStaleGitRefs(ctx, gitDir, cached.Branches, o.Branches, c); err != nil {
			return err
		}
	}
	if err := cache.fetch(ctx, gitDir, o, c, logger); err != nil {
		return err
	}
	if err := use(gitDir); err != nil {
		return err
	}
	if o.CacheGitRepo {
		cached.Branches = o.Branches
		b, err := json.Marshal(cached)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(gitDir, gitRepoCacheMetadata), b, 0o600); err != nil {
			return err
		}
		keep = true
	}
	return nil
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

	for i, header := range []string{
		"X-Sourcegraph-Actor-UID: internal",
		"X-Sourcegraph-Tenant-ID: " + strconv.Itoa(o.TenantID),
	} {
		action := "--add"
		if i == 0 {
			// Reset the multi-value key before adding the expected headers.
			action = "--replace-all"
		}
		cmd = exec.CommandContext(ctx, "git", "-C", gitDir, "config", action, "http.extraHeader", header)
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
			"fetch", "--depth=1", "--no-tags", "--atomic", "--no-auto-maintenance",
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
			if o.CacheGitRepo {
				// Do not let a failed fetch poison a retained clone, including the
				// existing fallback when a delta base is no longer available.
				if err := os.RemoveAll(gitDir); err != nil {
					return err
				}
				if err := initGitRepo(ctx, gitDir, o, c); err != nil {
					return err
				}
			}
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

func (gitRepoCache) rootDir() string {
	return filepath.Join(os.TempDir(), "git-cache")
}

func (cache gitRepoCache) cachedDir(o *indexArgs) string {
	// Include the name because indexMutex serializes by name; a rename must not
	// share a mutable clone with an already-running job under the old name.
	return filepath.Join(cache.rootDir(), strconv.Itoa(o.TenantID),
		fmt.Sprintf("%d-%x.git", o.RepoID, sha256.Sum256([]byte(o.Name))))
}

func (gitRepoCache) readEntry(dir string) (gitRepoCacheEntry, error) {
	var entry gitRepoCacheEntry
	b, err := os.ReadFile(filepath.Join(dir, gitRepoCacheMetadata))
	if err != nil {
		return entry, err
	}
	err = json.Unmarshal(b, &entry)
	return entry, err
}

func (cache gitRepoCache) prepare(o *indexArgs, now time.Time) (string, gitRepoCacheEntry, error) {
	dir := cache.cachedDir(o)
	entry, err := cache.readEntry(dir)
	if err != nil || !now.Before(entry.Created.Add(gitRepoCacheMaxAge)) ||
		entry.CloneURL != o.CloneURL || entry.Filtered != (len(o.LargeFiles) == 0) {
		// Changing from filtered to full fetching can otherwise leave previously
		// omitted blobs missing even when fetching a commit we already have.
		if err := os.RemoveAll(dir); err != nil {
			return "", entry, err
		}
		entry = gitRepoCacheEntry{Created: now, RepoID: o.RepoID, CloneURL: o.CloneURL, Filtered: len(o.LargeFiles) == 0}
	} else if err := os.Remove(filepath.Join(dir, gitRepoCacheMetadata)); err != nil {
		return "", entry, err
	}
	return dir, entry, nil
}

func removeStaleGitRefs(ctx context.Context, dir string, previous, current []zoekt.RepositoryBranch, c gitIndexConfig) error {
	for _, branch := range previous {
		if slices.ContainsFunc(current, func(b zoekt.RepositoryBranch) bool { return b.Name == branch.Name }) {
			continue
		}
		ref := branch.Name
		if ref != "HEAD" {
			ref = "refs/heads/" + ref
		}
		if err := c.runCmd(exec.CommandContext(ctx, "git", "-C", dir, "update-ref", "-d", ref)); err != nil {
			return err
		}
	}
	return nil
}

func (cache gitRepoCache) remove(o *indexArgs) error {
	return os.RemoveAll(cache.cachedDir(o))
}

func (cache gitRepoCache) removeTenant(tenantID int) error {
	return os.RemoveAll(filepath.Join(cache.rootDir(), strconv.Itoa(tenantID)))
}

// cleanup runs under indexMutex.Global, just like shard cleanup.
// Sweeping also reclaims expired clones of repositories that never index again.
func (cache gitRepoCache) cleanup(repos []uint32, now time.Time) error {
	dirs, err := filepath.Glob(filepath.Join(cache.rootDir(), "*", "*.git"))
	if err != nil {
		return err
	}
	var errs error
	for _, dir := range dirs {
		entry, err := cache.readEntry(dir)
		// Only a handful of repos are cached; avoid allocating a second map of
		// the potentially much larger complete assignment list for their lookup.
		if err == nil && slices.Contains(repos, entry.RepoID) && now.Before(entry.Created.Add(gitRepoCacheMaxAge)) {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return errs
}
