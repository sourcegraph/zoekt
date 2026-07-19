// Copyright 2021 Google Inc. All rights reserved.
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
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/ignore"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/search"
)

func TestIndexEmptyRepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	cmd := exec.Command("git", "init", "-b", "master", "repo")
	cmd.Dir = dir

	if err := cmd.Run(); err != nil {
		t.Fatalf("cmd.Run: %v", err)
	}

	desc := zoekt.Repository{
		Name: "repo",
	}
	opts := Options{
		RepoDir: filepath.Join(dir, "repo", ".git"),
		BuildOptions: index.Options{
			RepositoryDescription: desc,
			IndexDir:              dir,
		},
	}

	if _, err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo: %v", err)
	}
}

func TestIndexNonexistentRepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	desc := zoekt.Repository{
		Name: "nonexistent",
	}
	opts := Options{
		RepoDir:  "does/not/exist",
		Branches: []string{"main"},
		BuildOptions: index.Options{
			RepositoryDescription: desc,
			IndexDir:              dir,
		},
	}

	if _, err := IndexGitRepo(opts); err == nil {
		t.Fatal("expected error, got none")
	} else if !errors.Is(err, git.ErrRepositoryNotExists) {
		t.Fatalf("expected git.ErrRepositoryNotExists, got %v", err)
	}
}

func TestIndexTinyRepo(t *testing.T) {
	t.Parallel()

	// Create a repo with one file in it.
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main", "repo")

	repoDir := filepath.Join(dir, "repo")
	if err := os.WriteFile(filepath.Join(repoDir, "file1.go"), []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "initial commit")

	// Test that indexing accepts both the repo directory, and the .git subdirectory.
	for _, testDir := range []string{"repo", "repo/.git"} {
		opts := Options{
			RepoDir:  filepath.Join(dir, testDir),
			Branches: []string{"main"},
			BuildOptions: index.Options{
				RepositoryDescription: zoekt.Repository{Name: "repo"},
				IndexDir:              dir,
			},
		}

		if _, err := IndexGitRepo(opts); err != nil {
			t.Fatalf("unexpected error %v", err)
		}

		searcher, err := search.NewDirectorySearcher(dir)
		if err != nil {
			t.Fatal("NewDirectorySearcher", err)
		}

		results, err := searcher.Search(context.Background(), &query.Const{Value: true}, &zoekt.SearchOptions{})
		searcher.Close()

		if err != nil {
			t.Fatal("search failed", err)
		}

		if len(results.Files) != 1 {
			t.Fatalf("got search result %v, want 1 file", results.Files)
		}
	}
}

func TestIndexGitRepo_FallbackWhenCatfileFilterUnsupported(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main", "repo")

	repoDir := filepath.Join(dir, "repo")
	if err := os.WriteFile(filepath.Join(repoDir, "file1.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "initial commit")

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("LookPath(git): %v", err)
	}

	shimDir := t.TempDir()
	shimPath := filepath.Join(shimDir, "git")
	shim := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "cat-file" ]; then
  shift
  for arg in "$@"; do
    case "$arg" in
      --filter=*)
        echo "error: unknown option '$arg'" >&2
        echo "usage: git cat-file" >&2
        exit 129
        ;;
    esac
  done
  exec %q cat-file "$@"
fi
exec %q "$@"
`, realGit, realGit)
	if err := os.WriteFile(shimPath, []byte(shim), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", shimPath, err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	indexDir := t.TempDir()
	opts := Options{
		RepoDir:  repoDir,
		Branches: []string{"main"},
		BuildOptions: index.Options{
			RepositoryDescription: zoekt.Repository{Name: "repo"},
			IndexDir:              indexDir,
			SizeMax:               1 << 20,
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

	results, err := searcher.Search(context.Background(), &query.Const{Value: true}, &zoekt.SearchOptions{})
	if err != nil {
		t.Fatal("search failed", err)
	}

	if len(results.Files) != 1 {
		t.Fatalf("got search result %v, want 1 file", results.Files)
	}
}

func TestIndexGitRepo_ErrorsIfCatfileFailsAfterFirstBlob(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main", "repo")

	repoDir := filepath.Join(dir, "repo")
	for name, content := range map[string]string{
		"a.go": "package main\n\nconst A = 1\n",
		"b.go": "package main\n\nconst B = 2\n",
	} {
		if err := os.WriteFile(filepath.Join(repoDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", name, err)
		}
	}
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "initial commit")

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("LookPath(git): %v", err)
	}

	shimDir := t.TempDir()
	shimPath := filepath.Join(shimDir, "git")
	shim := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "cat-file" ]; then
  shift
  IFS= read -r first || exit 1
  printf '%%s\n' "$first" | %q cat-file "$@"
  echo "error: synthetic cat-file failure after first object" >&2
  exit 1
fi
exec %q "$@"
`, realGit, realGit)
	if err := os.WriteFile(shimPath, []byte(shim), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", shimPath, err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	indexDir := t.TempDir()
	opts := Options{
		RepoDir:  repoDir,
		Branches: []string{"main"},
		BuildOptions: index.Options{
			RepositoryDescription: zoekt.Repository{Name: "repo"},
			IndexDir:              indexDir,
			SizeMax:               1 << 20,
		},
	}

	if _, err := IndexGitRepo(opts); err == nil {
		t.Fatal("IndexGitRepo succeeded, want error")
	} else if !strings.Contains(err.Error(), "synthetic cat-file failure after first object") {
		t.Fatalf("IndexGitRepo error = %v, want synthetic cat-file failure", err)
	}
}

func TestIndexGitRepo_Worktree(t *testing.T) {
	t.Parallel()

	_, worktreeDir := initGitWorktree(t, "file1.go", "package main\n\nfunc main() {}\n")
	indexDir := t.TempDir()

	opts := Options{
		RepoDir:  worktreeDir,
		Branches: []string{"HEAD"},
		BuildOptions: index.Options{
			RepositoryDescription: zoekt.Repository{Name: "repo"},
			IndexDir:              indexDir,
		},
	}

	if _, err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo(worktree): %v", err)
	}

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatal("NewDirectorySearcher", err)
	}
	defer searcher.Close()

	results, err := searcher.Search(context.Background(), &query.Const{Value: true}, &zoekt.SearchOptions{})
	if err != nil {
		t.Fatal("search failed", err)
	}

	if len(results.Files) != 1 {
		t.Fatalf("got search result %v, want 1 file", results.Files)
	}
}

func TestOpenRepoVariants(t *testing.T) {
	t.Parallel()

	repoDir, worktreeDir := initGitWorktree(t, "file1.go", "package main\n\nfunc main() {}\n")
	bareDir := cloneBareRepo(t, repoDir)

	paths := []struct {
		name string
		path string
	}{
		{name: "repo root", path: repoDir},
		{name: "dot git dir", path: filepath.Join(repoDir, ".git")},
		{name: "worktree root", path: worktreeDir},
		{name: "bare repo root", path: bareDir},
	}

	openers := []struct {
		name string
		open func(t *testing.T, repoDir string) *git.Repository
	}{
		{
			name: "plain",
			open: func(t *testing.T, repoDir string) *git.Repository {
				t.Helper()

				repo, err := plainOpenRepo(repoDir)
				if err != nil {
					t.Fatalf("plainOpenRepo(%q): %v", repoDir, err)
				}

				return repo
			},
		},
		{
			name: "optimized",
			open: func(t *testing.T, repoDir string) *git.Repository {
				t.Helper()

				repo, closer, err := openRepo(repoDir)
				if err != nil {
					t.Fatalf("openRepo(%q): %v", repoDir, err)
				}
				t.Cleanup(func() {
					_ = closer.Close()
				})

				return repo
			},
		},
	}

	for _, opener := range openers {
		for _, tc := range paths {
			t.Run(opener.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				repo := opener.open(t, tc.path)

				head, err := repo.Head()
				if err != nil {
					t.Fatalf("repo.Head(): %v", err)
				}

				if _, err := repo.CommitObject(head.Hash()); err != nil {
					t.Fatalf("repo.CommitObject(%s): %v", head.Hash(), err)
				}
			})
		}
	}
}

func TestIndexGitRepo_BareRepo_LegacyRepoOpen(t *testing.T) {
	repoDir, _ := initGitWorktree(t, "file1.go", "package main\n\nfunc main() {}\n")
	bareDir := cloneBareRepo(t, repoDir)
	indexDir := t.TempDir()

	t.Setenv("ZOEKT_DISABLE_GOGIT_OPTIMIZATION", "true")

	opts := Options{
		RepoDir:  bareDir,
		Branches: []string{"main"},
		BuildOptions: index.Options{
			RepositoryDescription: zoekt.Repository{Name: "repo"},
			IndexDir:              indexDir,
		},
	}

	if _, err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo(bare, legacy open): %v", err)
	}

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatal("NewDirectorySearcher", err)
	}
	defer searcher.Close()

	results, err := searcher.Search(context.Background(), &query.Const{Value: true}, &zoekt.SearchOptions{})
	if err != nil {
		t.Fatal("search failed", err)
	}

	if len(results.Files) != 1 || results.Files[0].FileName != "file1.go" {
		t.Fatalf("got search result %v, want file1.go", results.Files)
	}
}

func TestCatfileFilterSpec(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{
		{
			name: "size max",
			opts: Options{BuildOptions: index.Options{SizeMax: 1 << 20}},
			want: "blob:limit=1048577",
		},
		{
			name: "large file exception disables filter",
			opts: Options{BuildOptions: index.Options{SizeMax: 1 << 20, LargeFiles: []string{"*.bin"}}},
			want: "",
		},
		{
			name: "zero size max disables filter",
			opts: Options{BuildOptions: index.Options{SizeMax: 0}},
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := catfileFilterSpec(tc.opts); got != tc.want {
				t.Fatalf("catfileFilterSpec() = %q, want %q", got, tc.want)
			}
		})
	}
}

func initGitWorktree(t *testing.T, fileName, content string) (string, string) {
	t.Helper()

	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main", "repo")

	repoDir := filepath.Join(dir, "repo")
	if err := os.WriteFile(filepath.Join(repoDir, fileName), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit(t, repoDir, "config", "remote.origin.url", "git@github.com:sourcegraph/zoekt.git")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "initial commit")

	worktreeDir := filepath.Join(dir, "wt")
	runGit(t, repoDir, "worktree", "add", "-b", "worktree-branch", worktreeDir)

	return repoDir, worktreeDir
}

func cloneBareRepo(t *testing.T, repoDir string) string {
	t.Helper()

	bareDir := filepath.Join(t.TempDir(), "repo.git")
	runGit(t, filepath.Dir(repoDir), "clone", "--bare", repoDir, bareDir)

	return bareDir
}

func TestIndexDeltaBasic(t *testing.T) {
	t.Parallel()

	type branchToDocumentMap map[string][]index.Document

	type step struct {
		name             string
		addedDocuments   branchToDocumentMap
		deletedDocuments branchToDocumentMap
		optFn            func(t *testing.T, options *Options)

		expectedFallbackToNormalBuild bool
		expectedDocuments             []index.Document
	}

	helloWorld := index.Document{Name: "hello_world.txt", Content: []byte("hello")}

	fruitV1 := index.Document{Name: "best_fruit.txt", Content: []byte("strawberry")}
	fruitV1InFolder := index.Document{Name: "the_best/best_fruit.txt", Content: fruitV1.Content}
	fruitV1WithNewName := index.Document{Name: "new_fruit.txt", Content: fruitV1.Content}

	fruitV2 := index.Document{Name: "best_fruit.txt", Content: []byte("grapes")}
	fruitV2InFolder := index.Document{Name: "the_best/best_fruit.txt", Content: fruitV2.Content}

	fruitV3 := index.Document{Name: "best_fruit.txt", Content: []byte("oranges")}
	fruitV4 := index.Document{Name: "best_fruit.txt", Content: []byte("apples")}

	foo := index.Document{Name: "foo.txt", Content: []byte("bar")}

	emptySourcegraphIgnore := index.Document{Name: ignore.IgnoreFile}
	sourcegraphIgnoreWithContent := index.Document{Name: ignore.IgnoreFile, Content: []byte("good_content.txt")}

	for _, test := range []struct {
		name     string
		branches []string
		steps    []step
	}{
		{
			name:     "modification",
			branches: []string{"main"},
			steps: []step{
				{
					name: "setup",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{helloWorld, fruitV1},
					},

					expectedDocuments: []index.Document{helloWorld, fruitV1},
				},
				{
					name: "add newer version of fruits",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{fruitV2},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []index.Document{helloWorld, fruitV2},
				},
			},
		},
		{
			name:     "modification only inside nested folder",
			branches: []string{"main"},
			steps: []step{
				{
					name: "setup",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{foo, fruitV1InFolder},
					},

					expectedDocuments: []index.Document{foo, fruitV1InFolder},
				},
				{
					name: "add newer version of fruits inside folder",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{fruitV2InFolder},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []index.Document{foo, fruitV2InFolder},
				},
			},
		},
		{
			name:     "addition",
			branches: []string{"main"},
			steps: []step{
				{
					name: "setup",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{helloWorld, fruitV1},
					},

					expectedDocuments: []index.Document{helloWorld, fruitV1},
				},
				{
					name: "add new file - foo",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{foo},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []index.Document{helloWorld, fruitV1, foo},
				},
			},
		},
		{
			name:     "deletion",
			branches: []string{"main"},
			steps: []step{
				{
					name: "setup",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{helloWorld, fruitV1, foo},
					},

					expectedDocuments: []index.Document{helloWorld, fruitV1, foo},
				},
				{
					name:           "delete foo file",
					addedDocuments: nil,
					deletedDocuments: branchToDocumentMap{
						"main": []index.Document{foo},
					},

					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []index.Document{helloWorld, fruitV1},
				},
			},
		},
		{
			name:     "addition and deletion on only one branch",
			branches: []string{"main", "release", "dev"},
			steps: []step{
				{
					name: "setup",
					addedDocuments: branchToDocumentMap{
						"main":    []index.Document{fruitV1},
						"release": []index.Document{fruitV2},
						"dev":     []index.Document{fruitV3},
					},

					expectedDocuments: []index.Document{fruitV1, fruitV2, fruitV3},
				},
				{
					name: "replace fruits v3 with v4 on 'dev', delete fruits on 'main'",
					addedDocuments: branchToDocumentMap{
						"dev": []index.Document{fruitV4},
					},
					deletedDocuments: branchToDocumentMap{
						"main": []index.Document{fruitV1},
					},

					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []index.Document{fruitV2, fruitV4},
				},
			},
		},
		{
			name:     "rename",
			branches: []string{"main", "release"},
			steps: []step{
				{
					name: "setup",
					addedDocuments: branchToDocumentMap{
						"main":    []index.Document{fruitV1},
						"release": []index.Document{fruitV2},
					},
					expectedDocuments: []index.Document{fruitV1, fruitV2},
				},
				{
					name: "rename fruits file on 'main' + ensure that unmodified fruits file on 'release' is still searchable",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{fruitV1WithNewName},
					},
					deletedDocuments: branchToDocumentMap{
						"main": []index.Document{fruitV1},
					},

					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []index.Document{fruitV1WithNewName, fruitV2},
				},
			},
		},
		{
			name:     "modification: update one branch with version of document from another branch (a.k.a. Keegan's test)",
			branches: []string{"main", "dev"},
			steps: []step{
				{
					name: "setup",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{fruitV1},
						"dev":  []index.Document{fruitV2},
					},
					expectedDocuments: []index.Document{fruitV1, fruitV2},
				},
				{
					name: "switch main to dev's older version of fruits + bump dev's fruits to new version",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{fruitV2},
						"dev":  []index.Document{fruitV3},
					},

					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []index.Document{fruitV2, fruitV3},
				},
			},
		},
		{
			name:     "no-op delta builds (reindexing the same commits)",
			branches: []string{"main", "dev"},
			steps: []step{
				{
					name: "setup",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{fruitV1, foo},
						"dev":  []index.Document{helloWorld},
					},
					expectedDocuments: []index.Document{fruitV1, foo, helloWorld},
				},
				{
					name: "first no-op (normal build -> delta build)",
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []index.Document{fruitV1, foo, helloWorld},
				},
				{
					name: "second no-op (delta build -> delta build)",
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []index.Document{fruitV1, foo, helloWorld},
				},
			},
		},
		{
			name:     "should fallback to normal build if no prior shards exist",
			branches: []string{"main"},
			steps: []step{
				{
					name: "attempt delta build on a repository that hasn't been indexed yet",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{helloWorld},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedFallbackToNormalBuild: true,
					expectedDocuments:             []index.Document{helloWorld},
				},
			},
		},
		{
			name:     "should fallback to normal build if the set of requested repository branches changes",
			branches: []string{"main", "release", "dev"},
			steps: []step{
				{
					name: "setup",
					addedDocuments: branchToDocumentMap{
						"main":    []index.Document{fruitV1},
						"release": []index.Document{fruitV2},
						"dev":     []index.Document{fruitV3},
					},

					expectedDocuments: []index.Document{fruitV1, fruitV2, fruitV3},
				},
				{
					name: "try delta build after dropping 'main' branch from index ",
					addedDocuments: branchToDocumentMap{
						"release": []index.Document{fruitV4},
					},
					optFn: func(t *testing.T, o *Options) {
						o.Branches = []string{"HEAD", "release", "dev"} // a bit of a hack to override it this way, but it gets the job done
						o.BuildOptions.IsDelta = true
					},

					expectedFallbackToNormalBuild: true,
					expectedDocuments:             []index.Document{fruitV3, fruitV4},
				},
			},
		},
		{
			name:     "should expand branches correctly when using wildcards in branch names",
			branches: []string{"release/1", "release/2"},
			steps: []step{
				{
					name: "setup",
					addedDocuments: branchToDocumentMap{
						"release/1": []index.Document{fruitV1},
						"release/2": []index.Document{fruitV2},
					},

					expectedDocuments: []index.Document{fruitV1, fruitV2},
				},
				{
					name: "try delta build with wildcard in branches",
					optFn: func(t *testing.T, o *Options) {
						// use a wildcard here
						o.Branches = []string{"HEAD", "release/*"}
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []index.Document{fruitV1, fruitV2},
				},
			},
		},
		{
			name:     "should fallback to normal build if one or more index options updates requires a full build",
			branches: []string{"main"},
			steps: []step{
				{
					name: "setup",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{fruitV1},
					},

					expectedDocuments: []index.Document{fruitV1},
				},
				{
					name: "try delta build after updating Disable CTags index option",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{fruitV2},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
						o.BuildOptions.DisableCTags = true
					},

					expectedFallbackToNormalBuild: true,
					expectedDocuments:             []index.Document{fruitV2},
				},
				{
					name: "try delta build after reverting Disable CTags index option",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{fruitV3},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
						o.BuildOptions.DisableCTags = false
					},

					expectedFallbackToNormalBuild: true,
					expectedDocuments:             []index.Document{fruitV3},
				},
			},
		},
		{
			name:     "should successfully perform multiple delta builds after disabling symbols",
			branches: []string{"main"},
			steps: []step{
				{
					name: "setup",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{fruitV1},
					},

					expectedDocuments: []index.Document{fruitV1},
				},
				{
					name: "try delta build after updating Disable CTags index option",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{fruitV2},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
						o.BuildOptions.DisableCTags = true
					},

					expectedFallbackToNormalBuild: true,
					expectedDocuments:             []index.Document{fruitV2},
				},
				{
					name: "try another delta build while CTags is still disabled",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{fruitV3},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
						o.BuildOptions.DisableCTags = true
					},

					expectedDocuments: []index.Document{fruitV3},
				},
			},
		},
		{
			name:     "should fallback to normal build if repository has unsupported Sourcegraph ignore file",
			branches: []string{"main"},
			steps: []step{
				{
					name: "setup",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{emptySourcegraphIgnore},
					},

					expectedDocuments: []index.Document{emptySourcegraphIgnore},
				},
				{
					name: "attempt delta build after modifying ignore file",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{sourcegraphIgnoreWithContent},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedFallbackToNormalBuild: true,
					expectedDocuments:             []index.Document{sourcegraphIgnoreWithContent},
				},
			},
		},
		{
			name:     "should fallback to a full, normal build if the repository has more than the specified threshold of shards",
			branches: []string{"main"},
			steps: []step{
				{
					name: "setup: first shard",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{foo},
					},

					expectedDocuments: []index.Document{foo},
				},
				{
					name: "setup: second shard (delta)",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{fruitV1},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []index.Document{foo, fruitV1},
				},
				{
					name: "setup: third shard (delta)",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{helloWorld},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []index.Document{foo, fruitV1, helloWorld},
				},
				{
					name: "attempt another delta build after we already blew past the shard threshold",
					addedDocuments: branchToDocumentMap{
						"main": []index.Document{fruitV2InFolder},
					},
					optFn: func(t *testing.T, o *Options) {
						o.DeltaShardNumberFallbackThreshold = 2
						o.BuildOptions.IsDelta = true
					},

					expectedFallbackToNormalBuild: true,
					expectedDocuments:             []index.Document{foo, fruitV1, helloWorld, fruitV2InFolder},
				},
			},
		},
	} {
		test := test

		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			indexDir := t.TempDir()
			repositoryDir := t.TempDir()

			// setup: initialize the repository and all of its branches
			runGit(t, repositoryDir, "init", "-b", "master")

			for _, b := range test.branches {
				runGit(t, repositoryDir, "checkout", "-b", b)
				runGit(t, repositoryDir, "commit", "--allow-empty", "-m", "empty commit")
			}

			for _, step := range test.steps {
				t.Run(step.name, func(t *testing.T) {
					for _, b := range test.branches {
						// setup: for each branch, process any document deletions / additions and commit those changes

						hadChange := false

						runGit(t, repositoryDir, "checkout", b)

						for _, d := range step.deletedDocuments[b] {
							hadChange = true

							file := filepath.Join(repositoryDir, d.Name)

							err := os.Remove(file)
							if err != nil {
								t.Fatalf("deleting file %q: %s", d.Name, err)
							}
						}

						for _, d := range step.addedDocuments[b] {
							hadChange = true

							file := filepath.Join(repositoryDir, d.Name)

							err := os.MkdirAll(filepath.Dir(file), 0o755)
							if err != nil {
								t.Fatalf("ensuring that folders exist for file %q: %s", file, err)
							}

							err = os.WriteFile(file, d.Content, 0o644)
							if err != nil {
								t.Fatalf("writing file %q: %s", d.Name, err)
							}
						}

						if !hadChange {
							continue
						}

						runGit(t, repositoryDir, "add", "-A")
						runGit(t, repositoryDir, "commit", "-m", step.name)
					}

					// setup: prepare indexOptions with given overrides
					buildOptions := index.Options{
						IndexDir: indexDir,
						RepositoryDescription: zoekt.Repository{
							Name: "repository",
						},
						IsDelta: false,
					}
					buildOptions.SetDefaults()

					branches := append([]string{"HEAD"}, test.branches...)

					options := Options{
						RepoDir:      filepath.Join(repositoryDir, ".git"),
						BuildOptions: buildOptions,
						Branches:     branches,
					}

					if step.optFn != nil {
						step.optFn(t, &options)
					}

					// setup: prepare spy versions of prepare delta / normal build so that we can observe
					// whether they were called appropriately
					deltaBuildCalled := false
					prepareDeltaSpy := func(options Options, repository *git.Repository) (repos map[fileKey]BlobLocation, branchVersions map[string]map[string]plumbing.Hash, changedOrDeletedPaths []string, err error) {
						deltaBuildCalled = true
						return prepareDeltaBuild(options, repository)
					}

					normalBuildCalled := false
					prepareNormalSpy := func(options Options, repository *git.Repository) (repos map[fileKey]BlobLocation, branchVersions map[string]map[string]plumbing.Hash, err error) {
						normalBuildCalled = true
						return prepareNormalBuild(options, repository)
					}

					// run test
					_, err := indexGitRepo(options, gitIndexConfig{
						prepareDeltaBuild:  prepareDeltaSpy,
						prepareNormalBuild: prepareNormalSpy,
					})
					if err != nil {
						t.Fatalf("IndexGitRepo: %s", err)
					}

					if options.BuildOptions.IsDelta != deltaBuildCalled {
						// We should always try a delta build if we request it in the options.
						t.Fatalf("expected deltaBuildCalled to be %t, got %t", options.BuildOptions.IsDelta, deltaBuildCalled)
					}

					if options.BuildOptions.IsDelta && (step.expectedFallbackToNormalBuild != normalBuildCalled) {
						// We only check the normal spy on delta builds because it's only considered a "fallback" if we
						// asked for a delta build in the first place.
						t.Fatalf("expected normalBuildCalled to be %t, got %t", step.expectedFallbackToNormalBuild, normalBuildCalled)
					}

					// examine outcome: load shards into a searcher instance and run a dummy search query
					// that returns every document contained in the shards
					//
					// then, compare returned set of documents with the expected set for the step and see if they agree

					ss, err := search.NewDirectorySearcher(indexDir)
					if err != nil {
						t.Fatalf("NewDirectorySearcher(%s): %s", indexDir, err)
					}
					defer ss.Close()

					searchOpts := &zoekt.SearchOptions{Whole: true}
					result, err := ss.Search(context.Background(), &query.Const{Value: true}, searchOpts)
					if err != nil {
						t.Fatalf("Search: %s", err)
					}

					var receivedDocuments []index.Document
					for _, f := range result.Files {
						receivedDocuments = append(receivedDocuments, index.Document{
							Name:    f.FileName,
							Content: f.Content,
						})
					}

					for _, docs := range [][]index.Document{step.expectedDocuments, receivedDocuments} {
						sort.Slice(docs, func(i, j int) bool {
							a, b := docs[i], docs[j]

							// first compare names, then fallback to contents if the names are equal

							if a.Name < b.Name {
								return true
							}

							if a.Name > b.Name {
								return false
							}

							return bytes.Compare(a.Content, b.Content) < 0
						})
					}

					compareOptions := []cmp.Option{
						cmpopts.IgnoreFields(index.Document{}, "Branches"),
						cmpopts.EquateEmpty(),
					}

					if diff := cmp.Diff(step.expectedDocuments, receivedDocuments, compareOptions...); diff != "" {
						t.Errorf("diff in received documents (-want +got):%s\n:", diff)
					}
				})
			}
		})
	}
}

func runGit(t *testing.T, cwd string, args ...string) {
	t.Helper()

	err := os.MkdirAll(cwd, 0o755)
	if err != nil {
		t.Fatalf("ensuring path %q exists: %s", cwd, err)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=",
		"GIT_CONFIG_SYSTEM=",
		"GIT_COMMITTER_NAME=Kierkegaard",
		"GIT_COMMITTER_EMAIL=soren@apache.com",
		"GIT_AUTHOR_NAME=Kierkegaard",
		"GIT_AUTHOR_EMAIL=soren@apache.com",
	)

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("execution error: %v, output %s", err, out)
	}
}

func TestSetTemplates_e2e(t *testing.T) {
	t.Parallel()

	repositoryDir := t.TempDir()

	// setup: initialize the repository and all of its branches
	runGit(t, repositoryDir, "init", "-b", "master")
	runGit(t, repositoryDir, "config", "remote.origin.url", "git@github.com:sourcegraph/zoekt.git")
	desc := zoekt.Repository{}
	if err := setTemplatesFromConfig(&desc, repositoryDir); err != nil {
		t.Fatalf("setTemplatesFromConfig: %v", err)
	}

	if got, want := desc.FileURLTemplate, `{{URLJoinPath "https://github.com/sourcegraph/zoekt" "blob" .Version .Path}}`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSetTemplates_Worktree(t *testing.T) {
	t.Parallel()

	_, worktreeDir := initGitWorktree(t, "hello.go", "package main\n")
	desc := zoekt.Repository{}

	if err := setTemplatesFromConfig(&desc, worktreeDir); err != nil {
		t.Fatalf("setTemplatesFromConfig(worktree): %v", err)
	}

	if got, want := desc.FileURLTemplate, `{{URLJoinPath "https://github.com/sourcegraph/zoekt" "blob" .Version .Path}}`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSetTemplates(t *testing.T) {
	t.Parallel()

	base := "https://example.com/repo/name"
	version := "VERSION"
	path := "dir/name.txt"
	lineNumber := 10
	cases := []struct {
		typ    string
		commit string
		file   string
		line   string
	}{{
		typ:    "gitiles",
		commit: "https://example.com/repo/name/%2B/VERSION",
		file:   "https://example.com/repo/name/%2B/VERSION/dir/name.txt",
		line:   "#10",
	}, {
		typ:    "github",
		commit: "https://example.com/repo/name/commit/VERSION",
		file:   "https://example.com/repo/name/blob/VERSION/dir/name.txt",
		line:   "#L10",
	}, {
		typ:    "cgit",
		commit: "https://example.com/repo/name/commit/?id=VERSION",
		file:   "https://example.com/repo/name/tree/dir/name.txt/?id=VERSION",
		line:   "#n10",
	}, {
		typ:    "gitweb",
		commit: "https://example.com/repo/name;a=commit;h=VERSION",
		file:   "https://example.com/repo/name;a=blob;f=dir/name.txt;hb=VERSION",
		line:   "#l10",
	}, {
		typ:    "source.bazel.build",
		commit: "https://example.com/repo/name/%2B/VERSION",
		file:   "https://example.com/repo/name/%2B/VERSION:dir/name.txt",
		line:   ";l=10",
	}, {
		typ:    "bitbucket-server",
		commit: "https://example.com/repo/name/commits/VERSION",
		file:   "https://example.com/repo/name/dir/name.txt?at=VERSION",
		line:   "#10",
	}, {
		typ:    "bitbucket-cloud",
		commit: "https://example.com/repo/name/commits/VERSION",
		file:   "https://example.com/repo/name/src/VERSION/dir/name.txt",
		line:   "#10",
	}, {
		typ:    "azuredevops",
		commit: "https://example.com/repo/name/commit/VERSION",
		file:   "https://example.com/repo/name?path=/dir/name.txt&version=GCVERSION&_a=contents",
		line:   "&line=10&lineEnd=10&lineStartColumn=1&lineEndColumn=200",
	}, {
		typ:    "gitlab",
		commit: "https://example.com/repo/name/-/commit/VERSION",
		file:   "https://example.com/repo/name/-/blob/VERSION/dir/name.txt",
		line:   "#L10",
	}, {
		typ:    "gitea",
		commit: "https://example.com/repo/name/commit/VERSION",
		file:   "https://example.com/repo/name/src/commit/VERSION/dir/name.txt?display=source",
		line:   "#L10",
	}}

	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			t.Parallel()

			assertOutput := func(templateText string, want string) {
				t.Helper()

				tt, err := index.ParseTemplate(templateText)
				if err != nil {
					t.Fatal(err)
				}

				var sb strings.Builder
				err = tt.Execute(&sb, map[string]any{
					"Version":    version,
					"Path":       path,
					"LineNumber": lineNumber,
				})
				if err != nil {
					t.Fatal(err)
				}
				if got := sb.String(); got != want {
					t.Fatalf("want: %q\ngot:  %q", want, got)
				}
			}

			var repo zoekt.Repository
			u, _ := url.Parse(base)
			err := setTemplates(&repo, u, tc.typ)
			if err != nil {
				t.Fatal(err)
			}
			assertOutput(repo.CommitURLTemplate, tc.commit)
			assertOutput(repo.FileURLTemplate, tc.file)
			assertOutput(repo.LineFragmentTemplate, tc.line)
		})
	}
}

func BenchmarkPrepareNormalBuild(b *testing.B) {
	repoDir := requireBenchGitRepo(b)
	repo, err := plainOpenRepo(repoDir)
	if err != nil {
		b.Fatalf("Failed to open test repository: %v", err)
	}

	opts := Options{
		RepoDir:      repoDir,
		Submodules:   false,
		BranchPrefix: "refs/heads/",
		Branches:     []string{"HEAD"},
		BuildOptions: index.Options{
			RepositoryDescription: zoekt.Repository{
				Name: "test-repo",
				URL:  "https://github.com/example/test-repo",
			},
		},
	}

	b.ReportAllocs()

	repos, branchVersions, err := prepareNormalBuild(opts, repo)
	if err != nil {
		b.Fatalf("prepareNormalBuild failed: %v", err)
	}

	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	b.ReportMetric(float64(m.HeapInuse), "heap-used-bytes")
	b.ReportMetric(float64(m.HeapInuse), "heap-allocated-bytes")

	if len(repos) == 0 || len(branchVersions) == 0 {
		b.Fatalf("Unexpected empty results")
	}
}
