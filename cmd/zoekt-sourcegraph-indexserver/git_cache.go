package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"time"

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
const gitRepoCacheMaxAge = 7 * 24 * time.Hour

const gitRepoCacheMetadata = "zoekt-cache.json"

type gitRepoCacheEntry struct {
	Created  time.Time
	RepoID   uint32
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

func prepareCachedGitDir(o *indexArgs, now time.Time) (string, gitRepoCacheEntry, error) {
	dir := cachedGitDir(o)
	entry, err := readGitRepoCacheEntry(dir)
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

// cleanupGitRepoCache runs under indexMutex.Global, just like shard cleanup.
// Sweeping also reclaims expired clones of repositories that never index again.
func cleanupGitRepoCache(repos []uint32, now time.Time) error {
	dirs, err := filepath.Glob(filepath.Join(gitRepoCacheDir(), "*", "*.git"))
	if err != nil {
		return err
	}
	var errs error
	for _, dir := range dirs {
		entry, err := readGitRepoCacheEntry(dir)
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
