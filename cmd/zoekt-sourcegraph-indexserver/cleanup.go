package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/zoekt"
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
func cleanup(indexDir string, repos []string, now time.Time) {
	start := time.Now()
	trashDir := filepath.Join(indexDir, ".trash")
	if err := os.MkdirAll(trashDir, 0755); err != nil {
		log.Printf("failed to create trash dir: %v", err)
	}

	trash := getShards(trashDir)
	index := getShards(indexDir)

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

		log.Printf("removing old shards from trash for %s", repo)
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

		log.Printf("restoring shards from trash for %s", repo)
		moveAll(indexDir, shards)
		shardsLog(indexDir, "restore", shards)
	}

	// index: Move non-existant repos into trash
	for _, shards := range index {
		// Best-effort touch. If touch fails, we will just remove from the
		// trash sooner.
		for _, shard := range shards {
			_ = os.Chtimes(shard.Path, now, now)
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
	Repo    string
	Path    string
	ModTime time.Time
}

func getShards(dir string) map[string][]shard {
	d, err := os.Open(dir)
	if err != nil {
		debug.Printf("failed to getShards: %s", dir)
		return nil
	}
	defer d.Close()
	names, _ := d.Readdirnames(-1)
	sort.Strings(names)

	shards := make(map[string][]shard, len(names))
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

		names, err := shardRepoNames(path)
		if err != nil {
			debug.Printf("failed to read shard: %v", err)
			continue
		}

		for _, name := range names {
			shards[name] = append(shards[name], shard{
				Repo:    name,
				Path:    path,
				ModTime: fi.ModTime(),
			})
		}
	}
	return shards
}

func shardRepoNames(path string) ([]string, error) {
	repos, _, err := zoekt.ReadMetadataPath(path)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(repos))
	for _, repo := range repos {
		names = append(names, repo.Name)
	}
	return names, nil
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
		paths, err := zoekt.IndexFilePaths(shard.Path)
		if err != nil {
			debug.Printf("failed to remove shard %s: %v", shard.Path, err)
		}
		for _, p := range paths {
			if err := os.Remove(p); err != nil {
				debug.Printf("failed to remove shard file %s: %v", p, err)
			}
		}
	}
}

func moveAll(dstDir string, shards []shard) {
	for i, shard := range shards {
		paths, err := zoekt.IndexFilePaths(shard.Path)
		if err != nil {
			log.Printf("failed to stat shard paths, deleting all shards for %s: %v", shard.Repo, err)
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
			log.Printf("failed to move shard, deleting all shards for %s: %v", shard.Repo, err)
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
		_, _ = fmt.Fprintf(shardLogger, "%d\t%s\t%s\t%d\n", time.Now().UTC().Unix(), action, shard, shardSize)
	}
}
