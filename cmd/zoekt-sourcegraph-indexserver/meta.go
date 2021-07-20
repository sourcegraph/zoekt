package main

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"syscall"

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
)

// mergeMeta updates the .meta files for the shards on disk for o.
//
// This process is best effort. If anything fails we return on the first
// failure. This means you might have an inconsistent state on disk if an
// error is returned. It is recommended to fallback to re-indexing in that
// case.
func mergeMeta(o *build.Options) error {
	todo := map[string]string{}
	for i := 0; ; i++ {
		fn := o.ShardName(i)

		repo, _, err := zoekt.ReadMetadataPath(fn)
		if os.IsNotExist(err) {
			break
		} else if err != nil {
			return err
		}

		if updated, err := repo.MergeMutable(&o.RepositoryDescription); err != nil {
			return err
		} else if !updated {
			// This shouldn't happen, but ignore it if it does. We may be working on
			// an interrupted shard. This helps us converge to something correct.
			continue
		}

		dst := fn + ".meta"
		tmp, err := jsonMarshalTmpFile(repo, dst)
		if err != nil {
			return err
		}

		todo[tmp] = dst

		// if we fail to rename, this defer will attempt to remove the tmp file.
		defer os.Remove(tmp)
	}

	// best effort once we get here. Rename everything. Return error of last
	// failure.
	var renameErr error
	for tmp, dst := range todo {
		if err := os.Rename(tmp, dst); err != nil {
			renameErr = err
		}
	}

	return renameErr
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

// respect process umask. build does this.
var umask os.FileMode

func init() {
	umask = os.FileMode(syscall.Umask(0))
	syscall.Umask(int(umask))
}
