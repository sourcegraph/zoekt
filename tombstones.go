package zoekt

import (
	"bufio"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

// TombstoneFileName if present in IndexDir will create *.rip files containing
// tombstones operations.
const TombstoneFileName = "RIP"

func SetTombstone(shardPath string, repoName string) error {
	ts, err := LoadTombstones(shardPath)
	if err != nil {
		return err
	}

	ts[repoName] = struct{}{}

	tmp, err := ioutil.TempFile(filepath.Dir(shardPath), filepath.Base(shardPath)+".*.tmp")
	if err != nil {
		return err
	}
	defer tmp.Close()
	if runtime.GOOS != "windows" {
		if err = tmp.Chmod(0o666 &^ umask); err != nil {
			return err
		}
	}
	for r := range ts {
		_, err = tmp.WriteString(r + "\n")
		if err != nil {
			return err
		}
	}

	err = os.Rename(tmp.Name(), shardPath+".rip")
	if err != nil {
		return err
	}
	return nil
}

func LoadTombstones(path string) (map[string]struct{}, error) {
	m := make(map[string]struct{})

	file, err := os.Open(path + ".rip")
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		m[scanner.Text()] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return m, nil
}

// umask holds the Umask of the current process
var umask os.FileMode

func init() {
	umask = os.FileMode(syscall.Umask(0))
	syscall.Umask(int(umask))
}
