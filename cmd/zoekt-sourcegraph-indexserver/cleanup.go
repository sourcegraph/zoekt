package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/zoekt"
	"github.com/hashicorp/go-multierror"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"gopkg.in/natefinch/lumberjack.v2"
)

var metricCleanupDuration = promauto.NewHistogram(prometheus.HistogramOpts{
	Name:    "index_cleanup_duration_seconds",
	Help:    "The duration of one cleanup run",
	Buckets: prometheus.LinearBuckets(1, 1, 10),
})

// cleanup trashes shards in indexDir that do not exist in repos. For repos
// that do not exist in indexDir, but do in indexDir/.trash it will move them
// back into indexDir. Additionally it uses now to remove shards that have
// been in the trash for 24 hours. It also deletes .tmp files older than 4 hours.
func cleanup(indexDir string, repos []uint32, now time.Time) {
	start := time.Now()
	trashDir := filepath.Join(indexDir, ".trash")
	if err := os.MkdirAll(trashDir, 0755); err != nil {
		log.Printf("failed to create trash dir: %v", err)
	}

	trash := getShards(trashDir)
	index := getShards(indexDir)

	tombstonesEnabled := zoekt.TombstonesEnabled(indexDir)

	// trash: Remove old shards and conflicts with index
	minAge := now.Add(-24 * time.Hour)
	for repo, shards := range trash {
		old := false
		for _, shard := range shards {
			if shard.ModTime.Before(minAge) {
				old = true
			} else if shard.ModTime.After(now) {
				debug.Printf("trashed shard %s has timestamp in the future, reseting to now", shard.Path)
				_ = os.Chtimes(shard.Path, now, now)
			}
		}

		if _, conflicts := index[repo]; !conflicts && !old {
			continue
		}

		log.Printf("removing old shards from trash for %v", repo)
		removeAll(shards...)
		delete(trash, repo)
	}

	// index: Move missing repos from trash into index
	for _, repo := range repos {
		// Delete from index so that index will only contain shards to be
		// trashed.
		delete(index, repo)

		shards, ok := trash[repo]
		if !ok {
			continue
		}

		log.Printf("restoring shards from trash for %v", repo)
		moveAll(indexDir, shards)
		shardsLog(indexDir, "restore", shards)
	}

	// index: Move non-existent repos into trash
	for repo, shards := range index {
		// Best-effort touch. If touch fails, we will just remove from the
		// trash sooner.
		for _, shard := range shards {
			_ = os.Chtimes(shard.Path, now, now)
		}

		if tombstonesEnabled {
			// 1 repo can be split across many simple shards but it should only be contained
			// in 1 compound shard. Hence we check that len(shards)==1 and only consider the
			// shard at index 0.
			if len(shards) == 1 && strings.HasPrefix(filepath.Base(shards[0].Path), "compound-") {
				shardsLog(indexDir, "tomb", shards)
				if err := zoekt.SetTombstone(shards[0].Path, repo); err != nil {
					log.Printf("error setting tombstone for %v in shard %s: %s. Removing shard\n", repo, shards[0].Path, err)
					_ = os.Remove(shards[0].Path)
				}
				continue
			}
		}
		moveAll(trashDir, shards)
		shardsLog(indexDir, "remove", shards)
	}

	// Remove old .tmp files from crashed indexer runs-- for example, if
	// an indexer OOMs, it will leave around .tmp files, usually in a loop.
	maxAge := now.Add(-4 * time.Hour)
	if failures, err := filepath.Glob(filepath.Join(indexDir, "*.tmp")); err != nil {
		log.Printf("Glob: %v", err)
	} else {
		for _, f := range failures {
			st, err := os.Stat(f)
			if err != nil {
				log.Printf("Stat(%q): %v", f, err)
				continue
			}
			if !st.IsDir() && st.ModTime().Before(maxAge) {
				log.Printf("removing old tmp file: %s", f)
				os.Remove(f)
			}
		}
	}
	metricCleanupDuration.Observe(time.Since(start).Seconds())
}

type shard struct {
	RepoID   uint32
	RepoName string
	Path     string
	ModTime  time.Time
}

func getShards(dir string) map[uint32][]shard {
	d, err := os.Open(dir)
	if err != nil {
		debug.Printf("failed to getShards: %s", dir)
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
			debug.Printf("stat failed: %v", err)
			continue
		}
		if fi.IsDir() || filepath.Ext(path) != ".zoekt" {
			continue
		}

		repos, _, err := zoekt.ReadMetadataPathAlive(path)
		if err != nil {
			debug.Printf("failed to read shard: %v", err)
			continue
		}

		for _, repo := range repos {
			shards[repo.ID] = append(shards[repo.ID], shard{
				RepoID:   repo.ID,
				RepoName: repo.Name,
				Path:     path,
				ModTime:  fi.ModTime(),
			})
		}
	}
	return shards
}

var incompleteRE = regexp.MustCompile(`\.zoekt[0-9]+(\.\w+)?$`)

func removeIncompleteShards(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		debug.Printf("failed to removeIncompleteShards: %s", dir)
		return
	}
	defer d.Close()

	names, _ := d.Readdirnames(-1)
	for _, n := range names {
		if incompleteRE.MatchString(n) {
			path := filepath.Join(dir, n)
			if err := os.Remove(path); err != nil {
				debug.Printf("failed to remove incomplete shard %s: %v", path, err)
			} else {
				debug.Printf("cleaned up incomplete shard %s", path)
			}
		}
	}
}

func removeAll(shards ...shard) {
	// Note on error handling here: We only expect this to fail due to
	// IsNotExist, which is fine. Additionally this shouldn't fail
	// partially. But if it does, and the file still exists, then we have the
	// potential for a partial index for a repo. However, this should be
	// exceedingly rare due to it being a mix of partial failure on something in
	// trash + an admin re-adding a repository.
	for _, shard := range shards {
		err := removeShardAtPath(shard.Path)
		if err != nil {
			debug.Println(err)
		}
	}
}

// removeShardAtPath removes a shard and all its accompanying files. Check
// zoekt.IndexFilePaths to see which files are deleted.
func removeShardAtPath(path string) error {
	var errs error
	paths, err := zoekt.IndexFilePaths(path)
	if err != nil {
		errs = multierror.Append(errs, fmt.Errorf("failed to removeShardAtPath shard %s: %v", path, err))
	}
	for _, p := range paths {
		if err := os.Remove(p); err != nil {
			errs = multierror.Append(errs, fmt.Errorf("failed to removeShardAtPath shard file %s: %v", p, err))
		}
	}
	return errs
}

func moveAll(dstDir string, shards []shard) {
	for i, shard := range shards {
		paths, err := zoekt.IndexFilePaths(shard.Path)
		if err != nil {
			log.Printf("failed to stat shard paths, deleting all shards for %s: %v", shard.RepoName, err)
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
			log.Printf("HACK removing compound shard since we don't support tombstoning: %s", shard.Path)
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
			log.Printf("failed to move shard, deleting all shards for %s: %v", shard.RepoName, err)
			removeAll(dstShard) // some files may have moved to dst
			removeAll(shards...)
			return
		}

		// update shards so partial failure removes the dst path
		shards[i] = dstShard
	}
}

func shardsLog(indexDir, action string, shards []shard) {
	shardLogger := &lumberjack.Logger{
		Filename:   filepath.Join(indexDir, "zoekt-indexserver-shard-log.tsv"),
		MaxSize:    100, // Megabyte
		MaxBackups: 5,
	}
	defer shardLogger.Close()

	for _, s := range shards {
		shard := filepath.Base(s.Path)
		var shardSize int64
		if fi, err := os.Stat(filepath.Join(indexDir, shard)); err == nil {
			shardSize = fi.Size()
		}
		_, _ = fmt.Fprintf(shardLogger, "%d\t%s\t%s\t%d\t%s\t%d\n", time.Now().UTC().Unix(), action, shard, shardSize, s.RepoName, s.RepoID)
	}
}

// vacuum removes tombstoned repos from compound shards and removes compound
// shards if they shrink below minSizeBytes. Vacuum locks the index directory for
// each compound shard it vacuums.
func (s *Server) vacuum() {
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

		path := filepath.Join(s.IndexDir, fn)
		info, err := os.Stat(path)
		if err != nil {
			debug.Printf("vacuum stat failed: %v", err)
			continue
		}

		if info.Size() < s.minSizeBytes {
			s.muIndexDir.Lock()
			err = removeShardAtPath(path)
			s.muIndexDir.Unlock()
			if err != nil {
				debug.Println(err)
			} else {
				shardsLog(s.IndexDir, "delete", []shard{{Path: path}})
			}
			continue
		}

		s.muIndexDir.Lock()
		removed, err := removeTombstones(path)
		s.muIndexDir.Unlock()

		if err != nil {
			debug.Printf("error while removing tombstones in %s: %s", fn, err)
		}
		for _, repo := range removed {
			shardsLog(s.IndexDir, "vac", []shard{{
				RepoID:   repo.ID,
				RepoName: repo.Name,
				Path:     filepath.Join(s.IndexDir, fn),
				ModTime:  info.ModTime(),
			}})
		}
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
		runMerge = exec.Command("zoekt-merge-index", fn).Run
	}

	repos, _, err := zoekt.ReadMetadataPath(fn)
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
		paths, err := zoekt.IndexFilePaths(fn)
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
