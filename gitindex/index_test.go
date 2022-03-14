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
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/shards"
)

func TestIndexEmptyRepo(t *testing.T) {
	tmp, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("TempDir %v", err)
	}
	defer os.RemoveAll(tmp)

	cmd := exec.Command("git", "init", "-b", "master", "repo")
	cmd.Dir = tmp

	if err := cmd.Run(); err != nil {
		t.Fatalf("cmd.Run: %v", err)
	}

	desc := zoekt.Repository{
		Name: "repo",
	}
	opts := Options{
		RepoDir: filepath.Join(tmp, "repo", ".git"),
		BuildOptions: build.Options{
			RepositoryDescription: desc,
			IndexDir:              tmp,
		},
	}

	if err := IndexGitRepo(opts); err != nil {
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

		expectedDocuments []zoekt.Document
	}

	helloWorld := zoekt.Document{Name: "hello_world.txt", Content: []byte("hello")}

	fruitV1 := zoekt.Document{Name: "best_fruit.txt", Content: []byte("strawberry")}
	fruitV2 := zoekt.Document{Name: "best_fruit.txt", Content: []byte("grapes")}
	fruitV3 := zoekt.Document{Name: "best_fruit.txt", Content: []byte("oranges")}
	fruitV4 := zoekt.Document{Name: "best_fruit.txt", Content: []byte("apples")}

	foo := zoekt.Document{Name: "foo.txt", Content: []byte("bar")}

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
					optFn: func(t *testing.T, options *Options) {
						options.BuildOptions.IsDelta = true
					},

					expectedDocuments: []zoekt.Document{helloWorld, fruitV2},
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
					optFn: func(t *testing.T, options *Options) {
						options.BuildOptions.IsDelta = true
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

					optFn: func(t *testing.T, options *Options) {
						options.BuildOptions.IsDelta = true
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
					name: "delete v1, replace v3 with v4",
					addedDocuments: branchToDocumentMap{
						"dev": []zoekt.Document{fruitV4},
					},
					deletedDocuments: branchToDocumentMap{
						"main": []zoekt.Document{fruitV1},
					},

					optFn: func(t *testing.T, options *Options) {
						options.BuildOptions.IsDelta = true
					},

					expectedDocuments: []zoekt.Document{fruitV2, fruitV4},
				},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			indexDir := t.TempDir()
			repositoryDir := t.TempDir()

			runScript(t, repositoryDir, "git init")
			runScript(t, repositoryDir, fmt.Sprintf("git config user.email %q", "you@example.com"))
			runScript(t, repositoryDir, fmt.Sprintf("git config user.name %q", "Your Name"))

			for _, b := range test.branches {
				runScript(t, repositoryDir, fmt.Sprintf("git checkout -b %q", b))
				runScript(t, repositoryDir, fmt.Sprintf("git commit --allow-empty -m %q", "empty commit"))
			}

			for _, step := range test.steps {
				t.Run(step.name, func(t *testing.T) {
					for _, b := range test.branches {
						hadChange := false

						runScript(t, repositoryDir, fmt.Sprintf("git checkout %q", b))

						for _, d := range step.deletedDocuments[b] {
							hadChange = true

							err := os.Remove(filepath.Join(repositoryDir, d.Name))
							if err != nil {
								t.Fatalf("deleting file %q: %s", d.Name, err)
							}

							runScript(t, repositoryDir, fmt.Sprintf("git add %q", d.Name))
						}

						for _, d := range step.addedDocuments[b] {
							hadChange = true

							err := os.WriteFile(filepath.Join(repositoryDir, d.Name), d.Content, 0644)
							if err != nil {
								t.Fatalf("writing file %q: %s", d.Name, err)
							}

							runScript(t, repositoryDir, fmt.Sprintf("git add %q", d.Name))
						}

						if !hadChange {
							continue
						}

						runScript(t, repositoryDir, fmt.Sprintf("git commit -m %q", step.name))
					}

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

					err := IndexGitRepo(options)
					if err != nil {
						t.Fatalf("IndexGitRepo: %s", err)
					}

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

							if a.Name < b.Name {
								return true
							}

							return bytes.Compare(a.Content, b.Content) < 0
						})
					}

					if diff := cmp.Diff(step.expectedDocuments, receivedDocuments, cmpopts.IgnoreFields(zoekt.Document{}, "Branches")); diff != "" {
						t.Errorf("diff in received documents (-want +got):%s\n:", diff)
					}
				})
			}
		})
	}
}

func runScript(t *testing.T, cwd string, script string) {
	err := os.MkdirAll(cwd, 0755)
	if err != nil {
		t.Fatalf("ensuring path %q exists: %s", cwd, err)
	}

	cmd := exec.Command("/bin/sh", "-euxc", script)
	cmd.Dir = cwd

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("execution error: %v, output %s", err, out)
	}
}
