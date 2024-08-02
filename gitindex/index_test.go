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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/build"
	"github.com/sourcegraph/zoekt/ignore"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/shards"
)

func TestIndexEmptyRepo(t *testing.T) {
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
		BuildOptions: build.Options{
			RepositoryDescription: desc,
			IndexDir:              dir,
		},
	}

	if _, err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo: %v", err)
	}
}

func TestIndexDeltaBasic(t *testing.T) {
	type branchToDocumentMap map[string][]zoekt.Document

	type step struct {
		name             string
		addedDocuments   branchToDocumentMap
		deletedDocuments branchToDocumentMap
		optFn            func(t *testing.T, options *Options)

		expectedFallbackToNormalBuild bool
		expectedDocuments             []zoekt.Document
	}

	helloWorld := zoekt.Document{Name: "hello_world.txt", Content: []byte("hello")}

	fruitV1 := zoekt.Document{Name: "best_fruit.txt", Content: []byte("strawberry")}
	fruitV1InFolder := zoekt.Document{Name: "the_best/best_fruit.txt", Content: fruitV1.Content}
	fruitV1WithNewName := zoekt.Document{Name: "new_fruit.txt", Content: fruitV1.Content}

	fruitV2 := zoekt.Document{Name: "best_fruit.txt", Content: []byte("grapes")}
	fruitV2InFolder := zoekt.Document{Name: "the_best/best_fruit.txt", Content: fruitV2.Content}

	fruitV3 := zoekt.Document{Name: "best_fruit.txt", Content: []byte("oranges")}
	fruitV4 := zoekt.Document{Name: "best_fruit.txt", Content: []byte("apples")}

	foo := zoekt.Document{Name: "foo.txt", Content: []byte("bar")}

	emptySourcegraphIgnore := zoekt.Document{Name: ignore.IgnoreFile}
	sourcegraphIgnoreWithContent := zoekt.Document{Name: ignore.IgnoreFile, Content: []byte("good_content.txt")}

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
						"main": []zoekt.Document{helloWorld, fruitV1},
					},

					expectedDocuments: []zoekt.Document{helloWorld, fruitV1},
				},
				{
					name: "add newer version of fruits",
					addedDocuments: branchToDocumentMap{
						"main": []zoekt.Document{fruitV2},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []zoekt.Document{helloWorld, fruitV2},
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
						"main": []zoekt.Document{foo, fruitV1InFolder},
					},

					expectedDocuments: []zoekt.Document{foo, fruitV1InFolder},
				},
				{
					name: "add newer version of fruits inside folder",
					addedDocuments: branchToDocumentMap{
						"main": []zoekt.Document{fruitV2InFolder},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []zoekt.Document{foo, fruitV2InFolder},
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
						"main": []zoekt.Document{helloWorld, fruitV1},
					},

					expectedDocuments: []zoekt.Document{helloWorld, fruitV1},
				},
				{
					name: "add new file - foo",
					addedDocuments: branchToDocumentMap{
						"main": []zoekt.Document{foo},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []zoekt.Document{helloWorld, fruitV1, foo},
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
						"main": []zoekt.Document{helloWorld, fruitV1, foo},
					},

					expectedDocuments: []zoekt.Document{helloWorld, fruitV1, foo},
				},
				{
					name:           "delete foo file",
					addedDocuments: nil,
					deletedDocuments: branchToDocumentMap{
						"main": []zoekt.Document{foo},
					},

					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []zoekt.Document{helloWorld, fruitV1},
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
						"main":    []zoekt.Document{fruitV1},
						"release": []zoekt.Document{fruitV2},
						"dev":     []zoekt.Document{fruitV3},
					},

					expectedDocuments: []zoekt.Document{fruitV1, fruitV2, fruitV3},
				},
				{
					name: "replace fruits v3 with v4 on 'dev', delete fruits on 'main'",
					addedDocuments: branchToDocumentMap{
						"dev": []zoekt.Document{fruitV4},
					},
					deletedDocuments: branchToDocumentMap{
						"main": []zoekt.Document{fruitV1},
					},

					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []zoekt.Document{fruitV2, fruitV4},
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
						"main":    []zoekt.Document{fruitV1},
						"release": []zoekt.Document{fruitV2},
					},
					expectedDocuments: []zoekt.Document{fruitV1, fruitV2},
				},
				{
					name: "rename fruits file on 'main' + ensure that unmodified fruits file on 'release' is still searchable",
					addedDocuments: branchToDocumentMap{
						"main": []zoekt.Document{fruitV1WithNewName},
					},
					deletedDocuments: branchToDocumentMap{
						"main": []zoekt.Document{fruitV1},
					},

					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []zoekt.Document{fruitV1WithNewName, fruitV2},
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
						"main": []zoekt.Document{fruitV1},
						"dev":  []zoekt.Document{fruitV2},
					},
					expectedDocuments: []zoekt.Document{fruitV1, fruitV2},
				},
				{
					name: "switch main to dev's older version of fruits + bump dev's fruits to new version",
					addedDocuments: branchToDocumentMap{
						"main": []zoekt.Document{fruitV2},
						"dev":  []zoekt.Document{fruitV3},
					},

					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []zoekt.Document{fruitV2, fruitV3},
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
						"main": []zoekt.Document{fruitV1, foo},
						"dev":  []zoekt.Document{helloWorld},
					},
					expectedDocuments: []zoekt.Document{fruitV1, foo, helloWorld},
				},
				{
					name: "first no-op (normal build -> delta build)",
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []zoekt.Document{fruitV1, foo, helloWorld},
				},
				{
					name: "second no-op (delta build -> delta build)",
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []zoekt.Document{fruitV1, foo, helloWorld},
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
						"main": []zoekt.Document{helloWorld},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedFallbackToNormalBuild: true,
					expectedDocuments:             []zoekt.Document{helloWorld},
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
						"main":    []zoekt.Document{fruitV1},
						"release": []zoekt.Document{fruitV2},
						"dev":     []zoekt.Document{fruitV3},
					},

					expectedDocuments: []zoekt.Document{fruitV1, fruitV2, fruitV3},
				},
				{
					name: "try delta build after dropping 'main' branch from index ",
					addedDocuments: branchToDocumentMap{
						"release": []zoekt.Document{fruitV4},
					},
					optFn: func(t *testing.T, o *Options) {
						o.Branches = []string{"HEAD", "release", "dev"} // a bit of a hack to override it this way, but it gets the job done
						o.BuildOptions.IsDelta = true
					},

					expectedFallbackToNormalBuild: true,
					expectedDocuments:             []zoekt.Document{fruitV3, fruitV4},
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
						"main": []zoekt.Document{fruitV1},
					},

					expectedDocuments: []zoekt.Document{fruitV1},
				},
				{
					name: "try delta build after updating Disable CTags index option",
					addedDocuments: branchToDocumentMap{
						"main": []zoekt.Document{fruitV2},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
						o.BuildOptions.DisableCTags = true
					},

					expectedFallbackToNormalBuild: true,
					expectedDocuments:             []zoekt.Document{fruitV2},
				},
				{
					name: "try delta build after reverting Disable CTags index option",
					addedDocuments: branchToDocumentMap{
						"main": []zoekt.Document{fruitV3},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
						o.BuildOptions.DisableCTags = false
					},

					expectedFallbackToNormalBuild: true,
					expectedDocuments:             []zoekt.Document{fruitV3},
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
						"main": []zoekt.Document{fruitV1},
					},

					expectedDocuments: []zoekt.Document{fruitV1},
				},
				{
					name: "try delta build after updating Disable CTags index option",
					addedDocuments: branchToDocumentMap{
						"main": []zoekt.Document{fruitV2},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
						o.BuildOptions.DisableCTags = true
					},

					expectedFallbackToNormalBuild: true,
					expectedDocuments:             []zoekt.Document{fruitV2},
				},
				{
					name: "try another delta build while CTags is still disabled",
					addedDocuments: branchToDocumentMap{
						"main": []zoekt.Document{fruitV3},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
						o.BuildOptions.DisableCTags = true
					},

					expectedDocuments: []zoekt.Document{fruitV3},
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
						"main": []zoekt.Document{emptySourcegraphIgnore},
					},

					expectedDocuments: []zoekt.Document{emptySourcegraphIgnore},
				},
				{
					name: "attempt delta build after modifying ignore file",
					addedDocuments: branchToDocumentMap{
						"main": []zoekt.Document{sourcegraphIgnoreWithContent},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedFallbackToNormalBuild: true,
					expectedDocuments:             []zoekt.Document{sourcegraphIgnoreWithContent},
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
						"main": []zoekt.Document{foo},
					},

					expectedDocuments: []zoekt.Document{foo},
				},
				{
					name: "setup: second shard (delta)",
					addedDocuments: branchToDocumentMap{
						"main": []zoekt.Document{fruitV1},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []zoekt.Document{foo, fruitV1},
				},
				{
					name: "setup: third shard (delta)",
					addedDocuments: branchToDocumentMap{
						"main": []zoekt.Document{helloWorld},
					},
					optFn: func(t *testing.T, o *Options) {
						o.BuildOptions.IsDelta = true
					},

					expectedDocuments: []zoekt.Document{foo, fruitV1, helloWorld},
				},
				{
					name: "attempt another delta build after we already blew past the shard threshold",
					addedDocuments: branchToDocumentMap{
						"main": []zoekt.Document{fruitV2InFolder},
					},
					optFn: func(t *testing.T, o *Options) {
						o.DeltaShardNumberFallbackThreshold = 2
						o.BuildOptions.IsDelta = true
					},

					expectedFallbackToNormalBuild: true,
					expectedDocuments:             []zoekt.Document{foo, fruitV1, helloWorld, fruitV2InFolder},
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
			runScript(t, repositoryDir, "git init -b master")
			runScript(t, repositoryDir, fmt.Sprintf("git config user.email %q", "you@example.com"))
			runScript(t, repositoryDir, fmt.Sprintf("git config user.name %q", "Your Name"))

			for _, b := range test.branches {
				runScript(t, repositoryDir, fmt.Sprintf("git checkout -b %q", b))
				runScript(t, repositoryDir, fmt.Sprintf("git commit --allow-empty -m %q", "empty commit"))
			}

			for _, step := range test.steps {
				t.Run(step.name, func(t *testing.T) {
					for _, b := range test.branches {
						// setup: for each branch, process any document deletions / additions and commit those changes

						hadChange := false

						runScript(t, repositoryDir, fmt.Sprintf("git checkout %q", b))

						for _, d := range step.deletedDocuments[b] {
							hadChange = true

							file := filepath.Join(repositoryDir, d.Name)

							err := os.Remove(file)
							if err != nil {
								t.Fatalf("deleting file %q: %s", d.Name, err)
							}

							runScript(t, repositoryDir, fmt.Sprintf("git add %q", file))
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

							runScript(t, repositoryDir, fmt.Sprintf("git add %q", file))
						}

						if !hadChange {
							continue
						}

						runScript(t, repositoryDir, fmt.Sprintf("git commit -m %q", step.name))
					}

					// setup: prepare indexOptions with given overrides
					buildOptions := build.Options{
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
					prepareDeltaSpy := func(options Options, repository *git.Repository) (repos map[fileKey]BlobLocation, branchMap map[fileKey][]string, branchVersions map[string]map[string]plumbing.Hash, changedOrDeletedPaths []string, err error) {
						deltaBuildCalled = true
						return prepareDeltaBuild(options, repository)
					}

					normalBuildCalled := false
					prepareNormalSpy := func(options Options, repository *git.Repository) (repos map[fileKey]BlobLocation, branchMap map[fileKey][]string, branchVersions map[string]map[string]plumbing.Hash, err error) {
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

					ss, err := shards.NewDirectorySearcher(indexDir)
					if err != nil {
						t.Fatalf("NewDirectorySearcher(%s): %s", indexDir, err)
					}
					defer ss.Close()

					searchOpts := &zoekt.SearchOptions{Whole: true}
					result, err := ss.Search(context.Background(), &query.Const{Value: true}, searchOpts)
					if err != nil {
						t.Fatalf("Search: %s", err)
					}

					var receivedDocuments []zoekt.Document
					for _, f := range result.Files {
						receivedDocuments = append(receivedDocuments, zoekt.Document{
							Name:    f.FileName,
							Content: f.Content,
						})
					}

					for _, docs := range [][]zoekt.Document{step.expectedDocuments, receivedDocuments} {
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
						cmpopts.IgnoreFields(zoekt.Document{}, "Branches"),
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

func TestRepoPathRanks(t *testing.T) {
	pathRanks := repoPathRanks{
		Paths: map[string]float64{
			"search.go":              10.23,
			"internal/index.go":      5.5,
			"internal/scratch.go":    0.0,
			"backend/search_test.go": 2.1,
		},
		MeanRank: 3.3,
	}
	cases := []struct {
		name string
		path string
		rank float64
	}{
		{
			name: "rank for standard file",
			path: "search.go",
			rank: 10.23,
		},
		{
			name: "file with rank 0",
			path: "internal/scratch.go",
			rank: 0.0,
		},
		{
			name: "rank for test file",
			path: "backend/search_test.go",
			rank: 2.1,
		},
		{
			name: "file with missing rank",
			path: "internal/docs.md",
			rank: 3.3,
		},
		{
			name: "test file with missing rank",
			path: "backend/index_test.go",
			rank: 0.0,
		},
		{
			name: "third-party file with missing rank",
			path: "node_modules/search/index.js",
			rank: 0.0,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := pathRanks.rank(tt.path)
			if got != tt.rank {
				t.Errorf("expected file '%s' to have rank %f, but got %f", tt.path, tt.rank, got)
			}
		})
	}
}

func runScript(t *testing.T, cwd string, script string) {
	t.Helper()

	err := os.MkdirAll(cwd, 0o755)
	if err != nil {
		t.Fatalf("ensuring path %q exists: %s", cwd, err)
	}

	cmd := exec.Command("sh", "-euxc", script)
	cmd.Dir = cwd
	cmd.Env = append([]string{"GIT_CONFIG_GLOBAL=", "GIT_CONFIG_SYSTEM="}, os.Environ()...)

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("execution error: %v, output %s", err, out)
	}
}

func TestSetTemplate(t *testing.T) {
	repositoryDir := t.TempDir()

	// setup: initialize the repository and all of its branches
	runScript(t, repositoryDir, "git init -b master")
	runScript(t, repositoryDir, "git config remote.origin.url git@github.com:sourcegraph/zoekt.git")
	desc := zoekt.Repository{}
	if err := setTemplatesFromConfig(&desc, repositoryDir); err != nil {
		t.Fatalf("setTemplatesFromConfig: %v", err)
	}

	if got, want := desc.FileURLTemplate, "https://github.com/sourcegraph/zoekt/blob/{{.Version}}/{{.Path}}"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
