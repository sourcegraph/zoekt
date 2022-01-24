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

	isAlive := func(alive []bool) {
		t.Helper()
		blob := readMeta(ghostShard)
		ghostRepos := []*Repository{}
		if err := json.Unmarshal(blob, &ghostRepos); err != nil {
			t.Fatal(err)
		}
		for i, repo := range ghostRepos {
			if repo.Tombstone == alive[i] {
				t.Fatalf("r%d: want %t, got %t\n", i+1, alive[i], repo.Tombstone)
			}
		}
	}

	if err := SetTombstone(ghostShard, 2); err != nil {
		t.Fatal(err)
	}
	isAlive([]bool{true, false, true})

	if err := SetTombstone(ghostShard, 1); err != nil {
		t.Fatal(err)
	}
	isAlive([]bool{false, false, true})

	if err := UnsetTombstone(ghostShard, 2); err != nil {
		t.Fatal(err)
	}
	isAlive([]bool{false, true, true})
}

func mkRepos(repoNames ...string) []*Repository {
	ret := make([]*Repository, 0, len(repoNames))
	for i, n := range repoNames {
		ret = append(ret, &Repository{ID: uint32(i + 1), Name: n})
	}
	return ret
}
