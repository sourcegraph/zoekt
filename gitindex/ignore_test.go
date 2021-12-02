package gitindex

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/zoekt/query"

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
	"github.com/google/zoekt/shards"
)

func createSourcegraphignoreRepo(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	script := `mkdir repo
cd repo
git init
mkdir subdir
echo acont > afile
echo sub-cont > subdir/sub-file
git add afile subdir/sub-file
git config user.email "you@example.com"
git config user.name "Your Name"
git commit -am amsg

git branch branchdir/abranch

mkdir .sourcegraph
echo subdir/ > .sourcegraph/ignore
git add .sourcegraph/ignore 
git commit -am "ignore subdir/"

git update-ref refs/meta/config HEAD
`
	cmd := exec.Command("/bin/sh", "-euxc", script)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("execution error: %v, output %s", err, out)
	}
	return nil
}

func TestIgnore(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}
	defer os.RemoveAll(dir)

	if err := createSourcegraphignoreRepo(dir); err != nil {
		t.Fatalf("createSourcegraphignoreRepo: %v", err)
	}

	indexDir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(indexDir)

	buildOpts := build.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			Name: "repo",
		},
	}
	buildOpts.SetDefaults()

	opts := Options{
		RepoDir:      filepath.Join(dir + "/repo"),
		BuildOptions: buildOpts,
		BranchPrefix: "refs/heads",
		Branches:     []string{"master", "branchdir/*"},
		Submodules:   true,
		Incremental:  true,
	}
	if err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo: %v", err)
	}

	searcher, err := shards.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatal("NewDirectorySearcher", err)
	}
	defer searcher.Close()

	res, err := searcher.Search(context.Background(), &query.Substring{}, &zoekt.SearchOptions{}) //nolint:staticcheck,ineffassign // ignore ineffectual assignment to err

	if len(res.Files) != 3 {
		t.Fatalf("expected 3 file matches")
	}
	for _, match := range res.Files {
		switch match.FileName {
		case "afile":
			if !reflect.DeepEqual(match.Branches, []string{"master", "branchdir/abranch"}) {
				t.Fatalf("expected afile to be present on both branches")
			}
		case "subdir/sub-file":
			if len(match.Branches) != 1 || match.Branches[0] != "branchdir/abranch" {
				t.Fatalf("expected sub-file to be present only on branchdir/abranch")
			}
		case ".sourcegraph/ignore":
			if len(match.Branches) != 1 || match.Branches[0] != "master" {
				t.Fatalf("expected sourcegraphignore to be present only on master")
			}
		default:
			t.Fatalf("match %+v not handled", match)
		}
	}
}
