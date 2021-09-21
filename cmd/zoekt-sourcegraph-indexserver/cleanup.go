package main

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

// TODO: deal with concurrent access to .rip files cleanup vs builder

// cleanup trashes shards in indexDir that do not exist in repos. For repos
// that do not exist in indexDir, but do in indexDir/.trash it will move them
// back into indexDir. Additionally it uses now to remove shards that have
// been in the trash for 24 hours. It also deletes .tmp files older than 4 hours.
func cleanup(indexDir string, repos []string, now time.Time) {
	fmt.Println(">>>>>>>>>>>>>>>>>>>>>>>>>>>>>>")
	defer fmt.Printf("<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<\n\n")
	start := time.Now()
	trashDir := filepath.Join(indexDir, ".trash")
	if err := os.MkdirAll(trashDir, 0755); err != nil {
		log.Printf("failed to create trash dir: %v", err)
	}

	trash := getShards(trashDir)
	index := getShards(indexDir)

	{
		fmt.Printf("repos = %+v\n", repos)
		reposInIndex := make([]string, 0, len(index))
		for k := range index {
			reposInIndex = append(reposInIndex, k)
		}
		fmt.Printf("index = %+v\n", reposInIndex)
	}

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
		//if _, err := os.Stat(filepath.Join(indexDir, tombstoneFileName)); err == nil {
		//	if len(shards) > 0 && strings.HasPrefix(filepath.Base(shards[0].Path), "compound-") {
		//		shardsLog(indexDir, fmt.Sprintf("unsetTombstone %s", repo), shards)
		//		if err := setTombstones(shards, repo, removeTombstone); err != nil {
		//			log.Printf("error setting tombstone %s", err)
		//		}
		//		break
		//	}
		//}
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

		if _, err := os.Stat(filepath.Join(indexDir, tombstoneFileName)); err == nil {
			if len(shards) > 0 && strings.HasPrefix(filepath.Base(shards[0].Path), "compound-") {
				shardsLog(indexDir, fmt.Sprintf("setTombstone %s", repo), shards)
				if err := setTombstones(shards, repo, addTombstone); err != nil {
					log.Printf("error setting tombstone %s", err)
				}
				break
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

type tombstoneOp int8

const (
	addTombstone    tombstoneOp = 1
	removeTombstone tombstoneOp = -1
)

//func setTombstone(shards []shard, repoName string) {
//	err := appendTombstones(shards, repoName, addTombstone)
//	if err != nil {
//		log.Printf("setTombstone failed with error: %s", err)
//	}
//}
//
//func unsetTombstone(shards []shard, repoName string) {
//	err := appendTombstones(shards, repoName, removeTombstone)
//	if err != nil {
//		log.Printf("unsetTombstone failed with error: %s", err)
//	}
//}
//
//func appendTombstones(shards []shard, repoName string, op tombstoneOp) error {
//	for _, s := range shards {
//		repos, _, err := zoekt.ReadMetadataPath(s.Path)
//		if err != nil {
//			return err
//		}
//		for ix, repo := range repos {
//			if repo.Name == repoName {
//				err = appendTombstone(s.Path+".rip", uint64(ix), op)
//				if err != nil {
//					return err
//				}
//				break
//			}
//		}
//	}
//	return nil
//}

//func appendTombstone(file string, repoID uint64, op tombstoneOp) error {
//	fmt.Printf("appendTombstone, file=%s\n", file)
//	f, err := os.OpenFile(file,
//		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
//	if err != nil {
//		return err
//	}
//	defer f.Close()
//	if _, err := f.WriteString(fmt.Sprintf("%d\t%d\n", repoID, op)); err != nil {
//		return err
//	}
//
//	return nil
//}

func setTombstones(shards []shard, repoName string, op tombstoneOp) error {
	for ix, s := range shards {
		fmt.Println("set tombstone for ", ix, s.Path)
		if err := setTombstone(s.Path, repoName, op); err != nil {
			return err
		}
	}
	return nil
}

func setTombstone(shardPath string, repoName string, op tombstoneOp) error {
	repos, _, err := zoekt.ReadMetadataPath(shardPath)
	if err != nil {
		return err
	}
	for repoID, repo := range repos {
		if repo.Name != repoName {
			continue
		}

		tmp, err := ioutil.TempFile(filepath.Dir(shardPath), filepath.Base(shardPath)+".*.tmp")
		defer tmp.Close()

		dest := shardPath + ".rip"

		setNewAndRename := func() error {
			if _, err := tmp.WriteString(fmt.Sprintf("%d\t%d\n", repoID, op)); err != nil {
				return err
			}
			err = os.Rename(tmp.Name(), dest)
			if err != nil {
				return err
			}
			return nil
		}

		b, err := os.ReadFile(dest)
		if os.IsNotExist(err) {
			if err := setNewAndRename(); err != nil {
				return err
			}
			break
		}
		if err != nil {
			return err
		}
		_, err = tmp.Write(b)
		if err != nil {
			return err
		}
		if err := setNewAndRename(); err != nil {
			return err
		}
		break
	}
	return nil
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

	tombstoneMap, err := loadTombstones(path)
	if err != nil {
		fmt.Println("loadTombstones ERR", err)
	}
	res := make(map[string]struct{})
	for ix, repo := range repos {
		if cnt, ok := tombstoneMap[ix]; ok && cnt > 0 {
			res[repo.Name] = struct{}{}
		}
	}

	names := make([]string, 0, len(repos))
	for _, repo := range repos {
		if _, ok := res[repo.Name]; ok {
			continue
		}
		names = append(names, repo.Name)
	}
	fmt.Printf("shardRepoNames[%s] = %v\n", path, names)
	return names, nil
}

func loadTombstones(path string) (m map[int]int, _ error) {
	defer func() {
		fmt.Printf("loadTombstones %+v\n", m)
	}()
	file, err := os.Open(path + ".rip")
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[int]int, 0), nil
		}
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	tombstoneMap := make(map[int]int)
	for scanner.Scan() {
		repoOps := strings.Split(scanner.Text(), "\t")
		repoId, err := strconv.Atoi(repoOps[0])
		if err != nil {
			return nil, err
		}
		repoOp, err := strconv.Atoi(repoOps[1])
		if err != nil {
			return nil, err
		}
		tombstoneMap[repoId] += repoOp
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return tombstoneMap, nil
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
