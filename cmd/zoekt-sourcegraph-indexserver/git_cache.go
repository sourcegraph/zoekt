package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	sglog "github.com/sourcegraph/log"

	"github.com/sourcegraph/zoekt"
)

// Selected monorepos keep their shallow bare clones between index jobs to avoid
// repeatedly transferring unchanged objects. Ordinary Git GC is not sufficient
// to bound this cache: repacking partial clones retains unreachable objects in
// promisor packs. Instead, expire clones seven days after creation (not access).
// The existing indexMutex excludes concurrent use/cleanup, so no reader pins or
// background Git maintenance are needed. setupTmpDir also clears it on restart.
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
const gitRepoCacheMaxAge = 7 * 24 * time.Hour

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

func gitRepoCacheDir() string {
	return filepath.Join(os.TempDir(), "git-cache")
}

func cachedGitDir(o *indexArgs) string {
	// Include the name because indexMutex serializes by name; a rename must not
	// share a mutable clone with an already-running job under the old name.
	return filepath.Join(gitRepoCacheDir(), strconv.Itoa(o.TenantID),
		fmt.Sprintf("%d-%x.git", o.RepoID, sha256.Sum256([]byte(o.Name))))
}

func readGitRepoCacheEntry(dir string) (gitRepoCacheEntry, error) {
	var entry gitRepoCacheEntry
	b, err := os.ReadFile(filepath.Join(dir, gitRepoCacheMetadata))
	if err != nil {
		return entry, err
	}
	err = json.Unmarshal(b, &entry)
	return entry, err
}

func prepareCachedGitDir(o *indexArgs, now time.Time, logger sglog.Logger) (string, gitRepoCacheEntry, string, error) {
	dir := cachedGitDir(o)
	entry, err := readGitRepoCacheEntry(dir)
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
		removed, err := removeGitRepoCache(dir, reason, logger)
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

// cleanupGitRepoCache runs under indexMutex.Global, just like shard cleanup.
// Sweeping also reclaims expired clones of repositories that never index again.
func cleanupGitRepoCache(repos []uint32, now time.Time, logger sglog.Logger) error {
	dirs, err := filepath.Glob(filepath.Join(gitRepoCacheDir(), "*", "*.git"))
	if err != nil {
		return err
	}
	var errs error
	var sizeErr error
	var total int64
	var repositories int
	for _, dir := range dirs {
		entry, err := readGitRepoCacheEntry(dir)
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
			if _, err := removeGitRepoCache(dir, reason, logger); err == nil {
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

// removeGitRepoCache reports actual removals only, not every uncached repo's
// no-op cleanup. Size-accounting failures must not prevent data deletion.
func removeGitRepoCache(dir, reason string, logger sglog.Logger) (bool, error) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if entry, err := readGitRepoCacheEntry(dir); err == nil {
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
