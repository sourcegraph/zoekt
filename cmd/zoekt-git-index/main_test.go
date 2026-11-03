package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sourcegraph/zoekt/internal/testutil"
)

func TestRunIndexesEachRepoUnderItsOwnName(t *testing.T) {
	dir := t.TempDir()
	indexDir := filepath.Join(dir, "index")
	args := []string{"zoekt-git-index", "-index", indexDir}
	for _, name := range []string{"alpha", "beta"} {
		repo := filepath.Join(dir, name)
		testutil.RunGit(t, dir, "init", "-b", "main", repo)
		if err := os.WriteFile(filepath.Join(repo, name+".txt"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		testutil.RunGit(t, repo, "add", ".")
		testutil.RunGit(t, repo, "commit", "-m", "init")
		args = append(args, repo)
	}

	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = args
	if code := run(); code != 0 {
		t.Fatalf("run() = %d", code)
	}

	for _, name := range []string{"alpha", "beta"} {
		shards, err := filepath.Glob(filepath.Join(indexDir, name+"_*.zoekt"))
		if err != nil {
			t.Fatal(err)
		}
		if len(shards) != 1 {
			t.Errorf("got %d shards for %s, want 1", len(shards), name)
		}
	}
}
