// Copyright 2016 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gitindex

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/shards"
)

func createSubmoduleRepo(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	script := `mkdir adir bdir
cd adir
git init
mkdir subdir
echo acont > afile
echo sub-cont > subdir/sub-file
git add afile subdir/sub-file
git config user.email "you@example.com"
git config user.name "Your Name"
git commit -am amsg

cd ..
cd bdir
git init
echo bcont > bfile
git add bfile
git config user.email "you@example.com"
git config user.name "Your Name"
git commit -am bmsg

cd ../adir
git submodule add --name bname -- ../bdir bname
git commit -am bmodmsg
cat .gitmodules
cd ..
mkdir gerrit.googlesource.com
git clone --bare adir gerrit.googlesource.com/adir.git
git clone --bare bdir gerrit.googlesource.com/bdir.git

mkdir gerrit.googlesource.com/bogus.git
mkdir gerrit.googlesource.com/sub
git clone --bare bdir gerrit.googlesource.com/sub/bdir.git

cat << EOF  > gerrit.googlesource.com/adir.git/config
[core]
	repositoryformatversion = 0
	filemode = true
	bare = true
[remote "origin"]
	url = http://gerrit.googlesource.com/adir
[branch "master"]
	remote = origin
	merge = refs/heads/master
EOF
`
	cmd := exec.Command("/bin/sh", "-euxc", script)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("execution error: %v, output %s", err, out)
	}
	return nil
}

func TestFindGitRepos(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}
	defer os.RemoveAll(dir)

	if err := createSubmoduleRepo(dir); err != nil {
		t.Error("createSubmoduleRepo", err)
	}
	repos, err := FindGitRepos(dir)
	if err != nil {
		t.Error("FindGitRepos", err)
	}

	got := map[string]bool{}
	for _, r := range repos {
		p, err := filepath.Rel(dir, r)
		if err != nil {
			t.Fatalf("Relative: %v", err)
		}

		got[p] = true
	}

	want := map[string]bool{
		"gerrit.googlesource.com/bdir.git":     true,
		"gerrit.googlesource.com/sub/bdir.git": true,
		"adir/.git":                            true,
		"bdir/.git":                            true,
		"gerrit.googlesource.com/adir.git":     true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestTreeToFiles(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}
	defer os.RemoveAll(dir)

	if err := createSubmoduleRepo(dir); err != nil {
		t.Fatalf("TempDir: %v", err)
	}

	cache := NewRepoCache(dir)

	aURL, _ := url.Parse("http://gerrit.googlesource.com/adir")
	repo, err := cache.Open(aURL)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	headRef, err := repo.Head()
	if err != nil {
		t.Fatalf("HEAD tree: %v", err)
	}
	commit, err := repo.CommitObject(headRef.Hash())
	if err != nil {
		t.Fatalf("commit obj HEAD: %v", err)
	}

	tree, err := repo.TreeObject(commit.TreeHash)
	if err != nil {
		t.Fatalf("AsTree: %v", err)
	}

	files, versions, err := TreeToFiles(repo, tree, aURL.String(), cache)
	if err != nil {
		t.Fatalf("TreeToFiles: %v", err)
	}

	bnameHash := versions["bname"]
	if entry, err := tree.FindEntry("bname"); err != nil {
		t.Fatalf("FindEntry %v", err)
	} else if !bytes.Equal(bnameHash[:], entry.Hash[:]) {
		t.Fatalf("got 'bname' versions %v, want %v", bnameHash, entry.Hash)
	}

	var paths []string
	for k := range files {
		paths = append(paths, k.FullPath())
	}
	sort.Strings(paths)

	want := []string{".gitmodules", "afile", "bname/bfile", "subdir/sub-file"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("got %v, want %v", paths, want)
	}
}

func TestSubmoduleIndex(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}
	defer os.RemoveAll(dir)

	if err := createSubmoduleRepo(dir); err != nil {
		t.Fatalf("createSubmoduleRepo: %v", err)
	}

	indexDir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(indexDir)

	buildOpts := build.Options{
		IndexDir: indexDir,
	}
	opts := Options{
		RepoDir:      filepath.Join(dir, "gerrit.googlesource.com", "adir.git"),
		BuildOptions: buildOpts,
		BranchPrefix: "refs/heads/",
		Branches:     []string{"master"},
		Submodules:   true,
		Incremental:  true,
		RepoCacheDir: dir,
	}
	if err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo: %v", err)
	}

	searcher, err := shards.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatal("NewDirectorySearcher", err)
	}
	defer searcher.Close()

	results, err := searcher.Search(context.Background(),
		&query.Substring{Pattern: "bcont"},
		&zoekt.SearchOptions{})
	if err != nil {
		t.Fatal("Search", err)
	}

	if len(results.Files) != 1 {
		t.Fatalf("got search result %v, want 1 file", results.Files)
	}

	file := results.Files[0]
	if got, want := file.SubRepositoryName, "gerrit.googlesource.com/bdir"; got != want {
		t.Errorf("got subrepo name %q, want %q", got, want)
	}
	if got, want := file.SubRepositoryPath, "bname"; got != want {
		t.Errorf("got subrepo path %q, want %q", got, want)
	}

	subVersion := file.Version
	if len(subVersion) != 40 {
		t.Fatalf("got %q, want hex sha1", subVersion)
	}

	if results, err := searcher.Search(context.Background(), &query.Substring{Pattern: "acont"}, &zoekt.SearchOptions{}); err != nil {
		t.Fatalf("Search('acont'): %v", err)
	} else if len(results.Files) != 1 {
		t.Errorf("got %v, want 1 result", results.Files)
	} else if f := results.Files[0]; f.Version == subVersion {
		t.Errorf("version in super repo matched version is subrepo.")
	}
}

func TestAllowMissingBranch(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}
	defer os.RemoveAll(dir)
	if err := createSubmoduleRepo(dir); err != nil {
		t.Fatalf("createSubmoduleRepo: %v", err)
	}

	indexDir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(indexDir)

	buildOpts := build.Options{
		IndexDir: indexDir,
	}

	opts := Options{
		RepoDir:      filepath.Join(dir, "gerrit.googlesource.com", "adir.git"),
		BuildOptions: buildOpts,
		BranchPrefix: "refs/heads/",
		Branches:     []string{"master", "nonexist"},
		Submodules:   true,
		Incremental:  true,
		RepoCacheDir: dir,
	}
	if err := IndexGitRepo(opts); err == nil {
		t.Fatalf("IndexGitRepo(nonexist) succeeded")
	}
	opts.AllowMissingBranch = true
	if err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo(nonexist, allow): %v", err)
	}
}

func createMultibranchRepo(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
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

git branch branchdir/a

echo acont >> afile
git add afile subdir/sub-file
git commit -am amsg

git branch branchdir/b

git branch c

git update-ref refs/meta/config HEAD
`
	cmd := exec.Command("/bin/sh", "-euxc", script)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("execution error: %v, output %s", err, out)
	}
	return nil
}

func TestBranchWildcard(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}
	defer os.RemoveAll(dir)

	if err := createMultibranchRepo(dir); err != nil {
		t.Fatalf("createMultibranchRepo: %v", err)
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
		Branches:     []string{"branchdir/*"},
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

	if rlist, err := searcher.List(context.Background(), &query.Repo{Pattern: ""}, nil); err != nil {
		t.Fatalf("List(): %v", err)
	} else if len(rlist.Repos) != 1 {
		t.Errorf("got %v, want 1 result", rlist.Repos)
	} else if repo := rlist.Repos[0]; len(repo.Repository.Branches) != 2 {
		t.Errorf("got branches %v, want 2", repo.Repository.Branches)
	} else if repo := rlist.Repos[0]; repo.Stats.Documents != 3 {
		t.Errorf("got document count %d, want 3", repo.Stats.Documents)
	}
}

func TestSkipSubmodules(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}
	defer os.RemoveAll(dir)

	if err := createSubmoduleRepo(dir); err != nil {
		t.Fatalf("createMultibranchRepo: %v", err)
	}

	indexDir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(indexDir)

	buildOpts := build.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			Name: "gerrit.googlesource.com/adir",
		},
	}
	if err := os.Rename(dir+"/gerrit.googlesource.com/bdir.git",
		dir+"/gerrit.googlesource.com/notexist.git"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	opts := Options{
		RepoDir:      filepath.Join(dir, "gerrit.googlesource.com", "adir.git"),
		BuildOptions: buildOpts,
		BranchPrefix: "refs/heads",
		Branches:     []string{"master"},
		Submodules:   false,
	}
	if err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo: %v", err)
	}
}

func TestFullAndShortRefNames(t *testing.T) {
	dir, err := ioutil.TempDir("", "git")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}
	defer os.RemoveAll(dir)

	if err := createMultibranchRepo(dir); err != nil {
		t.Fatalf("createMultibranchRepo: %v", err)
	}

	indexDir, err := ioutil.TempDir("", "index-")
	if err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(indexDir)

	buildOpts := build.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			Name: "repo",
		},
	}
	buildOpts.SetDefaults()

	opts := Options{
		RepoDir:            filepath.Join(dir + "/repo"),
		BuildOptions:       buildOpts,
		BranchPrefix:       "refs/heads",
		Branches:           []string{"refs/heads/master", "branchdir/a", "refs/meta/config"},
		Submodules:         false,
		Incremental:        false,
		AllowMissingBranch: false,
	}
	if err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo: %v", err)
	}

	searcher, err := shards.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatal("NewDirectorySearcher", err)
	}
	defer searcher.Close()

	if rlist, err := searcher.List(context.Background(), &query.Repo{Pattern: ""}, nil); err != nil {
		t.Fatalf("List(): %v", err)
	} else if len(rlist.Repos) != 1 {
		t.Errorf("got %v, want 1 result", rlist.Repos)
	} else if repo := rlist.Repos[0]; len(repo.Repository.Branches) != 3 {
		t.Errorf("got branches %v, want 3", repo.Repository.Branches)
	}
}

func TestUniq(t *testing.T) {
	in := []string{"a", "b", "b", "c", "c"}
	want := []string{"a", "b", "c"}
	got := uniq(in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
