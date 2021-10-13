package zoekt

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"syscall"
)

// TombstoneEnabled returns true if a file "RIP" is present in dir.
func TombstonesEnabled(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "RIP"))
	return err == nil
}

var mockRepos []*Repository

// SetTombstone idempotently sets a tombstone for repoName in .meta.
func SetTombstone(shardPath string, repoID uint32) error {
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
			repo.Tombstone = true
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
