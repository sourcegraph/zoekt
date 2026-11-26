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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/grafana/regexp"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/internal/testutil"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/search"
)

func createSubmoduleRepo(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	script := `
# Fix fatal: transport 'file' not allowed
export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0=protocol.file.allow
export GIT_CONFIG_VALUE_0=always

mkdir adir bdir
cd adir
git init -b master
mkdir subdir
echo acont > afile
echo sub-cont > subdir/sub-file
git add afile subdir/sub-file
git config user.email "you@example.com"
git config user.name "Your Name"
git commit -am amsg

cd ..
cd bdir
git init -b master
echo bcont > bfile
ln -s bfile bsymlink
git add bfile bsymlink
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

mkdir -p gerrit.googlesource.com/team/scope/
cp -r gerrit.googlesource.com/adir.git gerrit.googlesource.com/team/scope/repoa.git
cp -r gerrit.googlesource.com/bdir.git gerrit.googlesource.com/team/scope/repob.git

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
	cmd.Env = testutil.GitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("execution error: %v, output %s", err, out)
	}
	return nil
}

func TestFindGitRepos(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

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
		"adir/.git":                                    true,
		"bdir/.git":                                    true,
		"gerrit.googlesource.com/adir.git":             true,
		"gerrit.googlesource.com/bdir.git":             true,
		"gerrit.googlesource.com/sub/bdir.git":         true,
		"gerrit.googlesource.com/team/scope/repoa.git": true,
		"gerrit.googlesource.com/team/scope/repob.git": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestCollectFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

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

	rw := NewRepoWalker(repo, aURL.String(), cache)
	versions, err := rw.CollectFiles(tree, "main")
	if err != nil {
		t.Fatalf("CollectFiles: %v", err)
	}

	bnameHash := versions["bname"]
	if entry, err := tree.FindEntry("bname"); err != nil {
		t.Fatalf("FindEntry %v", err)
	} else if !bytes.Equal(bnameHash[:], entry.Hash[:]) {
		t.Fatalf("got 'bname' versions %v, want %v", bnameHash, entry.Hash)
	}

	var paths []string
	for k := range rw.Files {
		paths = append(paths, k.FullPath())
	}
	sort.Strings(paths)

	want := []string{".gitmodules", "afile", "bname/bfile", "bname/bsymlink", "subdir/sub-file"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("got %v, want %v", paths, want)
	}
}

func TestSubmoduleIndex(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if err := createSubmoduleRepo(dir); err != nil {
		t.Fatalf("createSubmoduleRepo: %v", err)
	}

	indexDir := t.TempDir()

	buildOpts := index.Options{
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
	if _, err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo: %v", err)
	}

	searcher, err := search.NewDirectorySearcher(indexDir)
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

func TestSubmoduleIndexWithoutRepocache(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if err := createSubmoduleRepo(dir); err != nil {
		t.Fatalf("createSubmoduleRepo: %v", err)
	}

	indexDir := t.TempDir()

	buildOpts := index.Options{
		RepositoryDescription: zoekt.Repository{Name: "adir"},
		IndexDir:              indexDir,
	}
	opts := Options{
		RepoDir:      filepath.Join(dir, "adir"),
		BuildOptions: buildOpts,
		BranchPrefix: "refs/heads/",
		Branches:     []string{"master"},
		Submodules:   true,
		Incremental:  true,
	}
	if _, err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo: %v", err)
	}

	searcher, err := search.NewDirectorySearcher(indexDir)
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
	if got, want := file.SubRepositoryName, "bname"; got != want {
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

func createSymlinkRepo(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	script := `mkdir adir bdir
git init -b master
git config user.email "you@example.com"
git config user.name "Your Name"

echo acont > adir/afile
git add adir/afile

echo bcont > bdir/bfile
git add bdir/bfile

ln -s ./adir/afile asymlink
git add asymlink

git commit -am amsg

cat << EOF  > .git/config
[core]
	repositoryformatversion = 0
	filemode = true
	bare = true
[remote "origin"]
	url = http://codehost.com/arepo
[branch "master"]
	remote = origin
	merge = refs/heads/master
EOF
`
	cmd := exec.Command("/bin/sh", "-euxc", script)
	cmd.Dir = dir
	cmd.Env = testutil.GitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("execution error: %v, output %s", err, out)
	}
	return nil
}

func TestSearchSymlinkByContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if err := createSymlinkRepo(dir); err != nil {
		t.Fatalf("createSubmoduleRepo: %v", err)
	}

	indexDir := t.TempDir()

	buildOpts := index.Options{
		IndexDir: indexDir,
	}
	opts := Options{
		RepoDir:      filepath.Join(dir),
		BuildOptions: buildOpts,
		BranchPrefix: "refs/heads/",
		Branches:     []string{"master"},
		Submodules:   true,
		Incremental:  true,
		RepoCacheDir: dir,
	}
	if _, err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo: %v", err)
	}

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatal("NewDirectorySearcher", err)
	}
	defer searcher.Close()

	// The content of the symlink and the file path the symlink points to both
	// contain the string "afile". Hence we expect 1 path match and 1 content match.
	results, err := searcher.Search(context.Background(),
		&query.Substring{Pattern: "afile"},
		&zoekt.SearchOptions{})
	if err != nil {
		t.Fatal("Search", err)
	}

	if len(results.Files) != 2 {
		t.Fatalf("got search result %v, want 2 files", results.Files)
	}

	got := make([]string, 0, 2)
	for _, file := range results.Files {
		got = append(got, file.FileName)
	}
	sort.Strings(got)

	want := []string{"adir/afile", "asymlink"}

	if d := cmp.Diff(want, got); d != "" {
		t.Fatalf("-want, +got %s\n", d)
	}
}

func TestAllowMissingBranch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if err := createSubmoduleRepo(dir); err != nil {
		t.Fatalf("createSubmoduleRepo: %v", err)
	}

	indexDir := t.TempDir()

	buildOpts := index.Options{
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
	if _, err := IndexGitRepo(opts); err == nil {
		t.Fatalf("IndexGitRepo(nonexist) succeeded")
	}
	opts.AllowMissingBranch = true
	if _, err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo(nonexist, allow): %v", err)
	}
}

func createMultibranchRepo(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	script := `mkdir repo
cd repo
git init -b master
mkdir subdir
echo acont > afile
echo sub-cont > subdir/sub-file
git add afile subdir/sub-file
git config user.email "you@example.com"
git config user.name "Your Name"
GIT_COMMITTER_DATE="Mon 5 Oct 2021 11:00:00 +0000" git commit -am amsg

git branch branchdir/a

echo acont >> afile
git add afile subdir/sub-file
GIT_COMMITTER_DATE="Tue 6 Oct 2021 12:00:00 +0000" git commit -am amsg

git branch branchdir/b

git branch c

git update-ref refs/meta/config HEAD
`
	cmd := exec.Command("/bin/sh", "-euxc", script)
	cmd.Dir = dir
	cmd.Env = testutil.GitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("execution error: %v, output %s", err, out)
	}
	return nil
}

func TestBranchWildcard(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if err := createMultibranchRepo(dir); err != nil {
		t.Fatalf("createMultibranchRepo: %v", err)
	}

	indexDir := t.TempDir()

	buildOpts := index.Options{
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
	if _, err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo: %v", err)
	}

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatal("NewDirectorySearcher", err)
	}
	defer searcher.Close()

	if rlist, err := searcher.List(context.Background(), &query.Repo{Regexp: regexp.MustCompile("repo")}, nil); err != nil {
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
	t.Parallel()

	dir := t.TempDir()

	if err := createSubmoduleRepo(dir); err != nil {
		t.Fatalf("createMultibranchRepo: %v", err)
	}

	indexDir := t.TempDir()

	buildOpts := index.Options{
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
	if _, err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo: %v", err)
	}
}

func TestFullAndShortRefNames(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if err := createMultibranchRepo(dir); err != nil {
		t.Fatalf("createMultibranchRepo: %v", err)
	}

	indexDir := t.TempDir()

	buildOpts := index.Options{
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
	if _, err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo: %v", err)
	}

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatal("NewDirectorySearcher", err)
	}
	defer searcher.Close()

	if rlist, err := searcher.List(context.Background(), &query.Repo{Regexp: regexp.MustCompile("repo")}, nil); err != nil {
		t.Fatalf("List(): %v", err)
	} else if len(rlist.Repos) != 1 {
		t.Errorf("got %v, want 1 result", rlist.Repos)
	} else if repo := rlist.Repos[0]; len(repo.Repository.Branches) != 3 {
		t.Errorf("got branches %v, want 3", repo.Repository.Branches)
	}
}

func TestSubURL(t *testing.T) {
	t.Parallel()

	rw := NewRepoWalker(nil, "https://example.com/org/sup", nil)
	// Git resolves "./" and "../" against the superproject's URL.
	for relURL, want := range map[string]string{
		"./s":                       "https://example.com/org/sup/s",
		"./s/t":                     "https://example.com/org/sup/s/t",
		"../t":                      "https://example.com/org/t",
		"../../other/t":             "https://example.com/other/t",
		"https://example.com/abs/u": "https://example.com/abs/u",
	} {
		u, err := rw.subURL(relURL)
		if err != nil {
			t.Fatalf("subURL(%q): %v", relURL, err)
		}
		if got := u.String(); got != want {
			t.Errorf("subURL(%q) = %q, want %q", relURL, got, want)
		}
	}
}

func TestUniq(t *testing.T) {
	t.Parallel()

	in := []string{"a", "b", "b", "c", "c"}
	want := []string{"a", "b", "c"}
	got := uniq(in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLatestCommit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	indexDir := t.TempDir()

	if err := createMultibranchRepo(dir); err != nil {
		t.Fatalf("createMultibranchRepo: %v", err)
	}

	buildOpts := index.Options{
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
		Branches:     []string{"branchdir/a", "branchdir/b"},
	}
	if _, err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo: %v", err)
	}

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatal("NewDirectorySearcher", err)
	}
	defer searcher.Close()

	rlist, err := searcher.List(context.Background(), &query.Repo{Regexp: regexp.MustCompile("repo")}, nil)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}

	if want := time.Date(2021, 10, 6, 12, 0, 0, 0, time.UTC); rlist.Repos[0].Repository.LatestCommitDate != want {
		t.Fatalf("want %s, got %s", want, rlist.Repos[0].Repository.LatestCommitDate)
	}
}

// TestSubmoduleIndexWithoutRepocacheUsesPinnedCommit tests that a submodule is
// indexed at the commit the superproject records, not at the submodule's own
// branch of the same name.
func TestSubmoduleIndexWithoutRepocacheUsesPinnedCommit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `
export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0=protocol.file.allow
export GIT_CONFIG_VALUE_0=always

git init -q -b master sub
cd sub
echo pinned-content > bfile
git add bfile
git commit -qm v1
echo tip-content > bfile
git commit -qam v2

cd ..
git init -q -b master super
cd super
echo acont > afile
git add afile
git commit -qm a
git submodule -q add ../sub bname
git -C bname checkout -q HEAD~1
git add bname
git commit -qm "pin sub at v1"
git -C bname rev-parse HEAD
`
	cmd := exec.Command("/bin/sh", "-euc", script)
	cmd.Dir = dir
	cmd.Env = testutil.GitEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("script: %v", err)
	}
	pinned := strings.TrimSpace(string(out))

	indexDir := t.TempDir()
	opts := Options{
		RepoDir:    filepath.Join(dir, "super"),
		Branches:   []string{"master"},
		Submodules: true,
		BuildOptions: index.Options{
			RepositoryDescription: zoekt.Repository{Name: "super"},
			IndexDir:              indexDir,
			DisableCTags:          true,
		},
	}
	if _, err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo: %v", err)
	}

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatal("NewDirectorySearcher", err)
	}
	defer searcher.Close()

	results, err := searcher.Search(context.Background(),
		&query.Substring{Pattern: "content", Content: true},
		&zoekt.SearchOptions{Whole: true})
	if err != nil {
		t.Fatal("Search", err)
	}
	if len(results.Files) != 1 {
		t.Fatalf("got %d files, want 1", len(results.Files))
	}
	f := results.Files[0]
	if got := string(f.Content); f.FileName != "bname/bfile" || got != "pinned-content\n" || f.Version != pinned {
		t.Fatalf("got %s with %q at %s, want bname/bfile with %q at the pinned %s",
			f.FileName, got, f.Version, "pinned-content\n", pinned)
	}
}

// TestSubmoduleIndexWithoutRepocacheIgnoresNestedVersions tests that a
// submodule is indexed only at the commits its superproject's branches pin.
// Here submodule a has a submodule of its own at path b, so the versions of a
// hold a b entry under the key a/master. Submodule b of the superproject must
// not take that as a branch.
func TestSubmoduleIndexWithoutRepocacheIgnoresNestedVersions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `
export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0=protocol.file.allow
export GIT_CONFIG_VALUE_0=always

git init -q -b master brepo
cd brepo
echo pinned-content > bfile
git add bfile
git commit -qm v1
echo nested-content > bfile
git commit -qam v2

cd ..
git init -q -b master arepo
cd arepo
echo acont > afile
git add afile
git commit -qm a
git submodule -q add ../brepo b
git commit -qm "add b at v2"

cd ..
git init -q -b master super
cd super
echo scont > sfile
git add sfile
git commit -qm s
git submodule -q add ../arepo a
git submodule -q add ../brepo b
git -C b checkout -q HEAD~1
git add b
git commit -qm "add a, and b at v1"
git submodule -q update --init --recursive
`
	cmd := exec.Command("/bin/sh", "-euc", script)
	cmd.Dir = dir
	cmd.Env = testutil.GitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}

	indexDir := t.TempDir()
	opts := Options{
		RepoDir:    filepath.Join(dir, "super"),
		Branches:   []string{"master"},
		Submodules: true,
		BuildOptions: index.Options{
			RepositoryDescription: zoekt.Repository{Name: "super"},
			IndexDir:              indexDir,
			DisableCTags:          true,
		},
	}
	if _, err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo: %v", err)
	}

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatal("NewDirectorySearcher", err)
	}
	defer searcher.Close()

	results, err := searcher.Search(context.Background(),
		&query.Substring{Pattern: "content", Content: true},
		&zoekt.SearchOptions{Whole: true})
	if err != nil {
		t.Fatal("Search", err)
	}
	var got []string
	for _, f := range results.Files {
		got = append(got, f.FileName+": "+strings.TrimSpace(string(f.Content)))
	}
	sort.Strings(got)
	want := []string{"a/b/bfile: nested-content", "b/bfile: pinned-content"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
