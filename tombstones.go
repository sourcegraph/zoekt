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
func SetTombstone(shardPath string, repoName string) error {
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
		if repo.Name == repoName {
			repo.Tombstone = true
		}
	}

	dest := shardPath + ".meta"
	fn, err := jsonMarshalTmpFile(repos, dest)
	if err != nil {
		return err
	}

	err = os.Rename(fn, dest)
	if err != nil {
		return err
	}
	return nil
}

// jsonMarshalFileTmp marshals v to the temporary file p + ".*.tmp" and
// returns the file name.
//
// Note: .tmp is the same suffix used by Builder. indexserver knows to clean
// them up.
func jsonMarshalTmpFile(v interface{}, p string) (_ string, err error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}

	f, err := ioutil.TempFile(filepath.Dir(p), filepath.Base(p)+".*.tmp")
	if err != nil {
		return "", err
	}
	defer func() {
		f.Close()
		if err != nil {
			_ = os.Remove(f.Name())
		}
	}()

	if err := f.Chmod(0o666 &^ umask); err != nil {
		return "", err
	}
	if _, err := f.Write(b); err != nil {
		return "", err
	}

	return f.Name(), f.Close()
}

// umask holds the Umask of the current process
var umask os.FileMode

func init() {
	umask = os.FileMode(syscall.Umask(0))
	syscall.Umask(int(umask))
}
