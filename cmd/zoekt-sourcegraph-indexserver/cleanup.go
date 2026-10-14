package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/grafana/regexp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
)

var metricCleanupDuration = promauto.NewHistogram(prometheus.HistogramOpts{
	Name:    "index_cleanup_duration_seconds",
	Help:    "The duration of one cleanup run",
	Buckets: prometheus.LinearBuckets(1, 1, 10),
})

// cleanup trashes shards in indexDir that do not exist in repos. For repos
// that do not exist in indexDir, but do in indexDir/.trash it will move them
// back into indexDir. Additionally it uses now to remove shards that have
// been in the trash for 24 hours. It also deletes files left behind by
// interrupted index operations.
func cleanup(mu *indexMutex, indexDir string, repos []uint32, now time.Time, shardMerging bool) {
	start := time.Now()
	defer func() { metricCleanupDuration.Observe(time.Since(start).Seconds()) }()
	trashDir := filepath.Join(indexDir, ".trash")
	if err := os.MkdirAll(trashDir, 0o755); err != nil {
		errorLog.Printf("failed to create trash dir: %v", err)
	}
	// Incomplete files cannot always be attributed to a repository. Only
	// scavenge them in an idle window; never wait for index jobs to finish.
	mu.TryGlobal(func() {
		removeStaleShardFiles(indexDir)
		removeStaleShardFiles(trashDir)
		removeTmpFiles(indexDir)
	})
	mu.Maintain(func(p *indexMaintenance) {
		cleanupRepos(p, indexDir, repos, now, shardMerging)
	})
}

func cleanupRepos(p *indexMaintenance, indexDir string, repos []uint32, now time.Time, shardMerging bool) {
	trashDir := filepath.Join(indexDir, ".trash")
	trash := getShards(trashDir)
	tombtones := getTombstonedRepos(indexDir)
	indexShards := getShards(indexDir)
	// Retain the original names even after an earlier phase removes a shard
	// from these maps (notably when repairing renamed repositories).
	repoShards := make(map[uint32][]shard)
	withRepo := func(repo uint32, f func()) {
		shards, ok := repoShards[repo]
		if !ok {
			shards = append([]shard{}, trash[repo]...)
			shards = append(shards, indexShards[repo]...)
			if s, ok := tombtones[repo]; ok {
				shards = append(shards, s)
			}
			repoShards[repo] = shards
		}
		p.withShards(shards, f)
	}

	// trash: Remove old shards and conflicts with index
	minAge := now.Add(-24 * time.Hour)
	for repo, shards := range trash {
		withRepo(repo, func() {
			old := false
			for _, shard := range shards {
				if shard.ModTime.Before(minAge) {
					old = true
				} else if shard.ModTime.After(now) {
					debugLog.Printf("trashed shard %s has timestamp in the future, reseting to now", shard.Path)
					_ = os.Chtimes(shard.Path, now, now)
				}
			}

			if _, conflicts := indexShards[repo]; !conflicts && !old {
				return
			}

			infoLog.Printf("removing old shards from trash for %v", repo)
			removeAll(shards...)
			delete(trash, repo)
		})
	}

	// tombstones: Remove tombstones that conflict with index or trash. After this,
	// tombstones only contain repos that are neither in the trash nor in the index.
	for repo := range tombtones {
		if _, conflicts := indexShards[repo]; conflicts {
			delete(tombtones, repo)
		}
		// Trash takes precedence over tombstones.
		if _, conflicts := trash[repo]; conflicts {
			delete(tombtones, repo)
		}
	}

	// index: We are ID based, but store shards by name still. If we end up with
	// shards that have the same ID but different names delete and start over.
	// This can happen when a repository is renamed. In future we should make
	// shard file names based on ID.
	for repo, shards := range indexShards {
		if consistentRepoName(shards) {
			continue
		}

		withRepo(repo, func() {
			// prevent further processing since we will delete
			delete(indexShards, repo)

			// This should be rare, so give an informative log message.
			var paths []string
			for _, shard := range shards {
				paths = append(paths, filepath.Base(shard.Path))
			}
			infoLog.Printf("removing shards for %v due to multiple repository names: %s", repo, strings.Join(paths, " "))

			// We may be in both normal and compound shards in this case. First
			// tombstone the compound shards so we don't just rm them.
			simple := shards[:0]
			for _, s := range shards {
				if shardMerging && maybeSetTombstone([]shard{s}, repo) {
					continue
				}

				simple = append(simple, s)
			}

			if len(simple) == 0 {
				return
			}

			removeAll(simple...)
		})
	}

	// index: Move missing repos from trash into index
	// index: Restore deleted or tombstoned repos.
	for _, repo := range repos {
		_, inTrash := trash[repo]
		_, tombstoned := tombtones[repo]
		if inTrash || tombstoned {
			withRepo(repo, func() {
				if shards, ok := trash[repo]; ok {
					infoLog.Printf("restoring shards from trash for %v", repo)
					moveAll(indexDir, shards)
					return
				}

				if s, ok := tombtones[repo]; ok {
					infoLog.Printf("removing tombstone for %v", repo)
					err := index.UnsetTombstone(s.Path, repo)
					if err != nil {
						errorLog.Printf("error removing tombstone for %v: %s", repo, err)
					}
				}
			})
		}
		// Assigned repositories must never be trashed, even if busy.
		delete(indexShards, repo)
	}

	// index: Move non-existent repos into trash
	for repo, shards := range indexShards {
		withRepo(repo, func() {
			// Best-effort touch. If touch fails, we will just remove from the
			// trash sooner.
			for _, shard := range shards {
				_ = os.Chtimes(shard.Path, now, now)
			}

			if shardMerging && maybeSetTombstone(shards, repo) {
				return
			}
			moveAll(trashDir, shards)
		})
	}
}

// removeTmpFiles requires the global lock: these may belong to active writers.
func removeTmpFiles(indexDir string) {
	if failures, err := filepath.Glob(filepath.Join(indexDir, "*.tmp")); err != nil {
		errorLog.Printf("Glob: %v", err)
	} else {
		for _, f := range failures {
			st, err := os.Stat(f)
			if err != nil {
				errorLog.Printf("Stat(%q): %v", f, err)
				continue
			}
			if !st.IsDir() {
				infoLog.Printf("removing tmp file: %s", f)
				os.Remove(f)
			}
		}
	}
}

// withShards reserves all members of affected shards, including tombstoned
// members: an index job can still rewrite their shared metadata sidecar.
func (p *indexMaintenance) withShards(shards []shard, f func()) bool {
	names := make(map[string]bool)
	ids := make(map[uint32]bool)
	for _, s := range shards {
		names[s.RepoName] = true
		ids[s.RepoID] = true
		if strings.HasPrefix(filepath.Base(s.Path), "compound-") {
			repos, _, err := index.ReadMetadataPath(s.Path)
			if err != nil {
				return false
			}
			for _, repo := range repos {
				names[repo.Name] = true
				ids[repo.ID] = true
			}
		}
	}
	return p.with(func(repo indexRepository) bool { return names[repo.name] || ids[repo.id] }, f)
}

type shard struct {
	RepoID        uint32
	RepoName      string
	Path          string
	ModTime       time.Time
	RepoTombstone bool
}

func getShards(dir string) map[uint32][]shard {
	d, err := os.Open(dir)
	if err != nil {
		debugLog.Printf("failed to getShards: %s", dir)
		return nil
	}
	defer d.Close()
	names, _ := d.Readdirnames(-1)
	sort.Strings(names)

	shards := make(map[uint32][]shard, len(names))
	for _, n := range names {
		path := filepath.Join(dir, n)
		fi, err := os.Stat(path)
		if err != nil {
			debugLog.Printf("stat failed: %v", err)
			continue
		}
		if fi.IsDir() || filepath.Ext(path) != ".zoekt" {
			continue
		}

		repos, _, err := index.ReadMetadataPathAlive(path)
		if err != nil {
			debugLog.Printf("failed to read shard: %v", err)
			continue
		}

		for _, repo := range repos {
			shards[repo.ID] = append(shards[repo.ID], shard{
				RepoID:        repo.ID,
				RepoName:      repo.Name,
				Path:          path,
				ModTime:       fi.ModTime(),
				RepoTombstone: repo.Tombstone,
			})
		}
	}
	return shards
}

// getTombstonedRepos return a map of tombstoned repositories in dir. If a
// repository is tombstoned in more than one compound shard, only the latest one,
// as determined by the date of the latest commit, is returned.
func getTombstonedRepos(dir string) map[uint32]shard {
	paths, err := filepath.Glob(filepath.Join(dir, "compound-*.zoekt"))
	if err != nil {
		return nil
	}
	if len(paths) == 0 {
		return nil
	}

	m := make(map[uint32]shard)

	for _, p := range paths {
		repos, _, err := index.ReadMetadataPath(p)
		if err != nil {
			continue
		}
		for _, repo := range repos {
			if !repo.Tombstone {
				continue
			}
			if v, ok := m[repo.ID]; ok && v.ModTime.After(repo.LatestCommitDate) {
				continue
			}
			m[repo.ID] = shard{
				RepoID:        repo.ID,
				RepoName:      repo.Name,
				Path:          p,
				ModTime:       repo.LatestCommitDate,
				RepoTombstone: repo.Tombstone,
			}
		}
	}
	return m
}

var incompleteRE = regexp.MustCompile(`\.zoekt[0-9]+(\.\w+)?$`)

func removeStaleShardFiles(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		debugLog.Printf("failed to remove stale shard files in %s: %v", dir, err)
		return
	}
	defer d.Close()

	names, _ := d.Readdirnames(-1)
	sort.Strings(names)

	for _, n := range names {
		path := filepath.Join(dir, n)
		kind := ""

		if incompleteRE.MatchString(n) {
			// Incomplete shard writes have a numeric suffix after .zoekt and
			// may also have a metadata suffix.
			kind = "incomplete shard"
		} else if strings.HasSuffix(n, ".zoekt.meta") &&
			!sortContainsString(names, strings.TrimSuffix(n, ".meta")) {
			// Metadata sidecars are orphaned when their corresponding shard
			// is not present in the directory.
			kind = "orphan metadata"
		} else {
			continue
		}

		if err := os.Remove(path); err != nil {
			debugLog.Printf("failed to remove %s %s: %v", kind, path, err)
		} else {
			debugLog.Printf("cleaned up %s %s", kind, path)
		}
	}
}

// sortContainsString reports whether a sorted list contains needle.
func sortContainsString(list []string, needle string) bool {
	i := sort.SearchStrings(list, needle)
	return i < len(list) && list[i] == needle
}

func removeAll(shards ...shard) {
	// Note on error handling here: We only expect this to fail due to
	// IsNotExist, which is fine. Additionally this shouldn't fail
	// partially. But if it does, and the file still exists, then we have the
	// potential for a partial index for a repo. However, this should be
	// exceedingly rare due to it being a mix of partial failure on something in
	// trash + an admin re-adding a repository.
	for _, shard := range shards {
		paths, err := index.IndexFilePaths(shard.Path)
		if err != nil {
			debugLog.Printf("failed to remove shard %s: %v", shard.Path, err)
		}
		for _, p := range paths {
			if err := os.Remove(p); err != nil {
				debugLog.Printf("failed to remove shard file %s: %v", p, err)
			}
		}
	}
}

func moveAll(dstDir string, shards []shard) {
	for i, shard := range shards {
		paths, err := index.IndexFilePaths(shard.Path)
		if err != nil {
			errorLog.Printf("failed to stat shard paths, deleting all shards for %s: %v", shard.RepoName, err)
			removeAll(shards...)
			return
		}

		// Remove all files in dstDir for shard. This is to avoid cases like not
		// overwriting an old meta file.
		dstShard := shard
		dstShard.Path = filepath.Join(dstDir, filepath.Base(shard.Path))
		removeAll(dstShard)

		// HACK we do not yet support tombstones in compound shard. So we avoid
		// needing to deal with it by always deleting the whole compound shard.
		if strings.HasPrefix(filepath.Base(shard.Path), "compound-") {
			infoLog.Printf("HACK removing compound shard since we don't support tombstoning: %s", shard.Path)
			removeAll(shard)
			continue
		}

		// Rename all paths, stop at first failure
		for _, p := range paths {
			dst := filepath.Join(dstDir, filepath.Base(p))
			err = os.Rename(p, dst)
			if err != nil {
				break
			}
		}

		if err != nil {
			errorLog.Printf("failed to move shard, deleting all shards for %s: %v", shard.RepoName, err)
			removeAll(dstShard) // some files may have moved to dst
			removeAll(shards...)
			return
		}

		// update shards so partial failure removes the dst path
		shards[i] = dstShard
	}
}

// consistentRepoName returns true if the list of shards have a unique
// repository name.
func consistentRepoName(shards []shard) bool {
	if len(shards) <= 1 {
		return true
	}
	name := shards[0].RepoName
	for _, shard := range shards[1:] {
		if shard.RepoName != name {
			return false
		}
	}
	return true
}

// maybeSetTombstone will call zoekt.SetTombstone for repoID if shards
// represents a compound shard. It returns true if shards represents a
// compound shard.
func maybeSetTombstone(shards []shard, repoID uint32) bool {
	// 1 repo can be split across many simple shards but it should only be contained
	// in 1 compound shard. Hence we check that len(shards)==1 and only consider the
	// shard at index 0.
	if len(shards) != 1 || !strings.HasPrefix(filepath.Base(shards[0].Path), "compound-") {
		return false
	}

	if err := index.SetTombstone(shards[0].Path, repoID); err != nil {
		errorLog.Printf("error setting tombstone for %d in shard %s: %s. Removing shard\n", repoID, shards[0].Path, err)
		_ = os.Remove(shards[0].Path)
	}
	return true
}

var metricVacuumRunning = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "index_vacuum_running",
	Help: "Set to 1 if indexserver's vacuum job is running.",
})

var metricNumberShards = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "index_number_shards",
	Help: "The number of total shards.",
})

var metricNumberCompoundShards = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "index_number_compound_shards",
	Help: "The number of compound shards.",
})

// vacuum removes tombstoned repos from compound shards and removes compound
// shards if they shrink below minSizeBytes. Busy compound shards are skipped.
func (s *Server) vacuum() {
	metricVacuumRunning.Set(1)
	defer metricVacuumRunning.Set(0)

	d, err := os.Open(s.IndexDir)
	if err != nil {
		return
	}
	defer d.Close()
	fns, _ := d.Readdirnames(-1)

	for _, fn := range fns {
		// We could run this over all shards, but based on our current setup, simple
		// shards won't have tombstones but instead will be moved to .trash.
		if !strings.HasPrefix(fn, "compound-") || !strings.HasSuffix(fn, ".zoekt") {
			continue
		}

		// Let cleanup and merging proceed between compound shards.
		s.muIndexDir.Maintain(func(p *indexMaintenance) {
			path := filepath.Join(s.IndexDir, fn)
			info, err := os.Stat(path)
			if err != nil {
				errorLog.Printf("vacuum stat failed: %v", err)
				return
			}

			p.withShards([]shard{{Path: path}}, func() {
				if info.Size() < s.mergeOpts.minSizeBytes {
					cmd := exec.Command("zoekt-merge-index", "explode", path)

					b, err := cmd.CombinedOutput()

					if err != nil {
						errorLog.Printf("failed to explode compound shard: shard=%s out=%s err=%s", path, string(b), err)
					} else {
						infoLog.Printf("exploded compound shard: shard=%s", path)
					}
					return
				}

				_, err = removeTombstones(path)

				if err != nil {
					errorLog.Printf("error while removing tombstones in %s: %s", path, err)
				}
			})
		})
	}
}

var mockMerger func() error

// removeTombstones removes all tombstones from a compound shard at fn by merging
// the compound shard with itself.
func removeTombstones(fn string) ([]*zoekt.Repository, error) {
	var runMerge func() error
	if mockMerger != nil {
		runMerge = mockMerger
	} else {
		runMerge = exec.Command("zoekt-merge-index", "merge", fn).Run
	}

	repos, _, err := index.ReadMetadataPath(fn)
	if err != nil {
		return nil, fmt.Errorf("zoekt.ReadMetadataPath: %s", err)
	}

	var tombstones []*zoekt.Repository
	for _, r := range repos {
		if r.Tombstone {
			tombstones = append(tombstones, r)
		}
	}
	if len(tombstones) == 0 {
		return nil, nil
	}

	defer func() {
		paths, err := index.IndexFilePaths(fn)
		if err != nil {
			return
		}
		for _, path := range paths {
			os.Remove(path)
		}
	}()
	err = runMerge()
	if err != nil {
		return nil, fmt.Errorf("runMerge: %s", err)
	}
	return tombstones, nil
}
