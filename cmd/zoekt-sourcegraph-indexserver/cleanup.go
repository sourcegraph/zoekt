package main

import (
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/google/zoekt"
)

// cleanup trashes shards in indexDir that do not exist in repos. For repos
// that do not exist in indexDir, but do in indexDir/.trash it will move them
// back into indexDir. Additionally it uses now to remove shards that have
// been in the trash for 24 hours.
func cleanup(indexDir string, repos []string, now time.Time) {

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
		removeAll(shards)
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
	}

	// index: Move non-existant repos into trash
	for _, shards := range index {
		// Best-effort touch. If touch fails, we will just remove from the
		// trash sooner.
		for _, shard := range shards {
			_ = os.Chtimes(shard.Path, now, now)
		}

		moveAll(trashDir, shards)
	}
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
		if fi.IsDir() {
			continue
		}

		name, err := shardRepoName(path)
		if err != nil {
			debug.Printf("failed to read shard: %v", err)
			continue
		}

		shards[name] = append(shards[name], shard{
			Repo:    name,
			Path:    path,
			ModTime: fi.ModTime(),
		})
	}
	return shards
}

func shardRepoName(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	ifile, err := zoekt.NewIndexFile(f)
	if err != nil {
		return "", err
	}
	defer ifile.Close()

	repo, _, err := zoekt.ReadMetadata(ifile)
	if err != nil {
		return "", err
	}

	return repo.Name, nil
}

var incompleteRE = regexp.MustCompile("\\.zoekt[0-9]+$")

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
			}
		}
	}
}

func removeAll(shards []shard) {
	for _, shard := range shards {
		if err := os.Remove(shard.Path); err != nil {
			// We only expect this to fail due to IsNotExist, which is
			// fine. Additionally this shouldn't fail partially. But if it
			// does, and the file still exists, then we have the potential for
			// a partial index for a repo. However, this should be exceedingly
			// rare due to it being a mix of partial failure on something in
			// trash + an admin re-adding a repository.
			debug.Printf("failed to remove shard %s: %v", shard.Path, err)
		}
	}
}

func moveAll(dstDir string, shards []shard) {
	for i, shard := range shards {
		dst := filepath.Join(dstDir, filepath.Base(shard.Path))
		if err := os.Rename(shard.Path, dst); err != nil {
			log.Printf("failed to move shard, deleting all shards for %s: %v", shard.Repo, err)
			removeAll(shards)
			return
		}
		// update path so that partial failure removes the dst path
		shards[i].Path = dst
	}
}
