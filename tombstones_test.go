package zoekt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSetTombstone(t *testing.T) {
	mockRepos = mkRepos("r1", "r2", "r3")

	readMeta := func(shard string) []byte {
		blob, err := os.ReadFile(shard + ".meta")
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return blob
	}

	dir := t.TempDir()
	ghostShard := filepath.Join(dir, "test.zoekt")

	SetTombstone(ghostShard, 2)

	blob := readMeta(ghostShard)
	gotRepos := []*Repository{}
	if err := json.Unmarshal(blob, &gotRepos); err != nil {
		t.Fatal(err)
	}

	if gotRepos[0].Tombstone {
		t.Fatal("r1 should have been alive")
	}
	if !gotRepos[1].Tombstone {
		t.Fatal("r2 should have been dead")
	}
	if gotRepos[2].Tombstone {
		t.Fatal("r3 should have been alive")
	}

	SetTombstone(ghostShard, 1)

	blob = readMeta(ghostShard)
	gotRepos = nil
	if err := json.Unmarshal(blob, &gotRepos); err != nil {
		t.Fatal(err)
	}

	if !gotRepos[0].Tombstone {
		t.Fatal("r1 should have been dead")
	}
	if !gotRepos[1].Tombstone {
		t.Fatal("r2 should have been dead")
	}
	if gotRepos[2].Tombstone {
		t.Fatal("r3 should have been alive")
	}
}

func mkRepos(repoNames ...string) []*Repository {
	ret := make([]*Repository, 0, len(repoNames))
	for i, n := range repoNames {
		ret = append(ret, &Repository{ID: uint32(i + 1), Name: n})
	}
	return ret
}
