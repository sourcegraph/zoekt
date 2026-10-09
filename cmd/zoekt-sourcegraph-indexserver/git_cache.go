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
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	sglog "github.com/sourcegraph/log"

	"github.com/sourcegraph/zoekt"
)

// gitRepoCache manages the acquisition and lifetime of shallow Git clones.
// Selected monorepos keep their shallow bare clones between index jobs to avoid
// repeatedly transferring unchanged objects. Ordinary Git GC is not sufficient
// to bound this cache: repacking partial clones retains unreachable objects in
// promisor packs. Instead, expire clones after a bounded age from creation (not
// access), configurable with SRC_GIT_REPO_CACHE_MAX_AGE.
// Clones and metadata live under the indexserver tmp root, so setupTmpDir clears
// both on restart. Paths are resolved at use time, after setupTmpDir sets TMPDIR.
// Callers hold the appropriate indexMutex lock to exclude concurrent use/cleanup;
// the cache provides no synchronization or background Git maintenance.
//
// The metadata file is a completion marker: remove it before mutating the clone
// and write it only after success. An interrupted job cannot leave a reusable
// clone with stale locks or partially updated refs. Metadata lives outside Git's
// config because this acquisition policy must not affect shard/index identity.
//
// Cache gauges are sampled during cleanup, outside Prometheus's scrape path.
// They sum file sizes, not filesystem allocation or network bytes transferred.
// Lifecycle logs distinguish cold/reused fetches and explain resets; aggregate
// gauges show growth without introducing another per-repository metric family.
type gitRepoCache struct{}

var gitCache gitRepoCache

var gitRepoCacheMaxAge = getEnvWithDefaultDuration("SRC_GIT_REPO_CACHE_MAX_AGE", 7*24*time.Hour)

const gitRepoCacheMetadata = "zoekt-cache.json"

var (
	metricGitRepoCacheSize = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "index_git_repo_cache_size_bytes",
		Help: "Sum of regular file sizes in retained Git clones, sampled during cache cleanup.",
	})
	metricGitRepoCacheRepositories = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "index_git_repo_cache_repositories",
		Help: "Number of retained Git clones, sampled during cache cleanup.",
	})
)

type gitRepoCacheEntry struct {
	Created  time.Time
	RepoID   uint32
	Name     string
	TenantID int
	CloneURL string
	Filtered bool
	Branches []zoekt.RepositoryBranch
}

// withFetchedRepo lends a ready bare repository to use. After use succeeds,
// cached clones are marked reusable and temporary clones are deleted. Any
// failure (including a panic) discards the clone; cleanup errors are logged
// without hiding the job error. The callback must not retain the directory.
func (cache gitRepoCache) withFetchedRepo(ctx context.Context, c gitIndexConfig, o *indexArgs, logger sglog.Logger, use func(string) error) (err error) {
	var gitDir string
	var cached gitRepoCacheEntry
	var cacheState string
	cacheLogger := logger
	if o.CacheGitRepo {
		cacheLogger = logger.With(sglog.String("repo", o.Name), sglog.Uint32("id", o.RepoID), sglog.Int("tenant", o.TenantID))
		gitDir, cached, cacheState, err = cache.prepare(o, time.Now(), cacheLogger)
	} else {
		gitDir, err = tmpGitDir(o.Name)
	}
	if err != nil {
		return err
	}
	keep := false
	failureReason := "prepare_error"
	defer func() {
		if !keep {
			if o.CacheGitRepo {
				if _, removeErr := cache.removeDir(gitDir, failureReason, cacheLogger); removeErr != nil {
					cacheLogger.Warn("failed to remove git clone cache", sglog.Error(removeErr))
				}
			} else if removeErr := os.RemoveAll(gitDir); removeErr != nil {
				logger.Warn("failed to remove git clone", sglog.String("path", gitDir), sglog.Error(removeErr))
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
	failureReason = "fetch_error"
	fetchStart := time.Now()
	err = cache.fetch(ctx, gitDir, o, c, logger)
	if o.CacheGitRepo {
		fetchDuration := time.Since(fetchStart)
		size, sizeErr := gitRepoCacheSize(gitDir)
		outcome := "success"
		if err != nil {
			outcome = "failure"
		}
		cacheLogger.Info("git clone cache fetch", sglog.String("cache_state", cacheState),
			sglog.Duration("cache_age", fetchStart.Sub(cached.Created)), sglog.Duration("fetch_duration", fetchDuration),
			sglog.String("outcome", outcome), sglog.Int64("cache_size_bytes", size), sglog.Error(sizeErr))
	}
	if err != nil {
		return err
	}

	failureReason = "index_error"
	if err = use(gitDir); err != nil {
		return err
	}
	if o.CacheGitRepo {
		failureReason = "metadata_error"
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
		name := repoNameForMetric(o.Name, o.CacheGitRepo)
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
				cacheLogger := logger.With(sglog.String("repo", o.Name), sglog.Uint32("id", o.RepoID), sglog.Int("tenant", o.TenantID))
				if _, err := gitCache.removeDir(gitDir, "delta_fetch_error", cacheLogger); err != nil {
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

func (cache gitRepoCache) prepare(o *indexArgs, now time.Time, logger sglog.Logger) (string, gitRepoCacheEntry, string, error) {
	dir := cache.cachedDir(o)
	entry, err := cache.readEntry(dir)
	reason := ""
	switch {
	case err != nil:
		reason = "incomplete"
	case !now.Before(entry.Created.Add(gitRepoCacheMaxAge)):
		reason = "age"
	case entry.CloneURL != o.CloneURL:
		reason = "source_change"
	case entry.Filtered != (len(o.LargeFiles) == 0):
		// Changing from filtered to full fetching can otherwise leave previously
		// omitted blobs missing even when fetching a commit we already have.
		reason = "filter_change"
	}
	state := "reused"
	if reason != "" {
		removed, err := cache.removeDir(dir, reason, logger)
		if err != nil {
			return "", entry, "", err
		}
		state = "cold"
		if removed {
			state = "reset"
		}
		entry = gitRepoCacheEntry{Created: now, RepoID: o.RepoID, Name: o.Name, TenantID: o.TenantID, CloneURL: o.CloneURL, Filtered: len(o.LargeFiles) == 0}
	} else if err := os.Remove(filepath.Join(dir, gitRepoCacheMetadata)); err != nil {
		return "", entry, "", err
	}
	return dir, entry, state, nil
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

func (cache gitRepoCache) remove(o *indexArgs, reason string, logger sglog.Logger) (bool, error) {
	return cache.removeDir(cache.cachedDir(o), reason, logger)
}

func (cache gitRepoCache) removeTenant(tenantID int, reason string, logger sglog.Logger) (bool, error) {
	return cache.removeDir(filepath.Join(cache.rootDir(), strconv.Itoa(tenantID)), reason, logger)
}

// cleanup runs under indexMutex.Global, just like shard cleanup.
// Sweeping also reclaims expired clones of repositories that never index again.
func (cache gitRepoCache) cleanup(repos []uint32, now time.Time, logger sglog.Logger) error {
	dirs, err := filepath.Glob(filepath.Join(cache.rootDir(), "*", "*.git"))
	if err != nil {
		return err
	}
	var errs error
	var sizeErr error
	var total int64
	var repositories int
	for _, dir := range dirs {
		entry, err := cache.readEntry(dir)
		// Only a handful of repos are cached; avoid allocating a second map of
		// the potentially much larger complete assignment list for their lookup.
		reason := ""
		switch {
		case err != nil:
			reason = "incomplete"
		case !slices.Contains(repos, entry.RepoID):
			reason = "unassigned"
		case !now.Before(entry.Created.Add(gitRepoCacheMaxAge)):
			reason = "age"
		}
		if reason != "" {
			if _, err := cache.removeDir(dir, reason, logger); err == nil {
				continue
			} else {
				errs = errors.Join(errs, err)
			}
		}
		// Include clones whose deletion failed so the gauges don't hide them.
		size, err := gitRepoCacheSize(dir)
		sizeErr = errors.Join(sizeErr, err)
		total += size
		repositories++
	}
	if sizeErr == nil {
		metricGitRepoCacheSize.Set(float64(total))
		metricGitRepoCacheRepositories.Set(float64(repositories))
	}
	return errors.Join(errs, sizeErr)
}

func gitRepoCacheSize(dir string) (int64, error) {
	var size int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// removeDir reports actual removals only, not every uncached repo's
// no-op cleanup. Size-accounting failures must not prevent data deletion.
func (cache gitRepoCache) removeDir(dir, reason string, logger sglog.Logger) (bool, error) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if entry, err := cache.readEntry(dir); err == nil {
		logger = logger.With(sglog.String("repo", entry.Name), sglog.Uint32("id", entry.RepoID), sglog.Int("tenant", entry.TenantID))
	}
	size, sizeErr := gitRepoCacheSize(dir)
	if err := os.RemoveAll(dir); err != nil {
		return false, err
	}
	logger.Info("removed git clone cache", sglog.String("path", dir), sglog.String("reason", reason),
		sglog.Int64("cache_size_bytes", size), sglog.Error(sizeErr))
	return true, nil
}
