package zoekt

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// ShardMergingEnabled returns true if SRC_ENABLE_SHARD_MERGING is set to true.
func ShardMergingEnabled() bool {
	t := os.Getenv("SRC_ENABLE_SHARD_MERGING")
	enabled, _ := strconv.ParseBool(t)
	return enabled
}

var mockRepos []*Repository

// SetTombstone idempotently sets a tombstone for repoName in .meta.
func SetTombstone(shardPath string, repoID uint32) error {
	return setTombstone(shardPath, repoID, true)
}

// UnsetTombstone idempotently removes a tombstones for reopName in .meta.
func UnsetTombstone(shardPath string, repoID uint32) error {
	return setTombstone(shardPath, repoID, false)
}

func setTombstone(shardPath string, repoID uint32, tombstone bool) error {
	var repos []*Repository
	var err error

	if mockRepos != nil {
		repos = mockRepos
	} else {
		repos, _, err = ReadMetadataPath(shardPath)
		if err != nil {
			return err
		}
	}

	for _, repo := range repos {
		if repo.ID == repoID {
			repo.Tombstone = tombstone
		}
	}

	dest := shardPath + ".meta"
	err = jsonMarshalMeta(repos, dest)
	if err != nil {
		return err
	}

	return nil
}

func jsonMarshalMeta(v interface{}, p string) (err error) {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}

	f, err := ioutil.TempFile(filepath.Dir(p), filepath.Base(p)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		if err != nil {
			_ = os.Remove(f.Name())
		}
	}()

	err = f.Chmod(0o666 &^ umask)
	if err != nil {
		return err
	}

	_, err = f.Write(b)
	if err != nil {
		return err
	}

	return os.Rename(f.Name(), p)
}

// umask holds the Umask of the current process
var umask os.FileMode

func init() {
	umask = os.FileMode(syscall.Umask(0))
	syscall.Umask(int(umask))
}
