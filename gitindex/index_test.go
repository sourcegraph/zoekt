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
	type step struct {
		name           string
		addedDocuments []zoekt.Document
		optFn          func(t *testing.T, options *Options)

		expectedDocuments []zoekt.Document
	}

	helloWorld := zoekt.Document{Name: "hello_world.txt", Content: []byte("hello")}
	fruitV1 := zoekt.Document{Name: "best_fruit.txt", Content: []byte("strawberry")}
	fruitV2 := zoekt.Document{Name: "best_fruit.txt", Content: []byte("grapes")}

	indexDir := t.TempDir()

	repositoryDir := t.TempDir()
	runScript(t, repositoryDir, "git init")

	for _, step := range []step{
		{
			name:           "setup",
			addedDocuments: []zoekt.Document{helloWorld, fruitV1},

			expectedDocuments: []zoekt.Document{helloWorld, fruitV1},
		},
		{
			name:           "add newer version of fruits",
			addedDocuments: []zoekt.Document{fruitV2},
			optFn: func(t *testing.T, options *Options) {
				options.BuildOptions.IsDelta = true
			},

			expectedDocuments: []zoekt.Document{helloWorld, fruitV2},
		},
	} {
		t.Run(step.name, func(t *testing.T) {
			for _, d := range step.addedDocuments {
				err := os.WriteFile(filepath.Join(repositoryDir, d.Name), d.Content, 0644)
				if err != nil {
					t.Fatalf("writing file %q: %s", d.Name, err)
				}

				runScript(t, repositoryDir, fmt.Sprintf("git add %s", d.Name))
			}

			runScript(t, repositoryDir, fmt.Sprintf("git commit -m %q", step.name))

			buildOptions := build.Options{
				IndexDir: indexDir,
				RepositoryDescription: zoekt.Repository{
					Name: "repository",
				},
				IsDelta: false,
			}
			buildOptions.SetDefaults()

			options := Options{
				RepoDir:      filepath.Join(repositoryDir, ".git"),
				BuildOptions: buildOptions,
				Branches:     []string{"HEAD"},
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

					return a.Name < b.Name
				})
			}

			if diff := cmp.Diff(step.expectedDocuments, receivedDocuments, cmpopts.IgnoreFields(zoekt.Document{}, "Branches")); diff != "" {
				t.Errorf("diff in received documents (-want +got):%s\n:", diff)
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
