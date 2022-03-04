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

func TestDeltaShard(t *testing.T) {
	// prepare repository + whatever options

	helloWorld := zoekt.Document{Name: "hello_world.txt", Content: []byte("hello")}
	fruitV1 := zoekt.Document{Name: "best_fruit.txt", Content: []byte("strawberry")}
	fruitV2 := zoekt.Document{Name: "best_fruit.txt", Content: []byte("grapes")}

	indexDir := t.TempDir()
	repositoryDir := filepath.Join(indexDir, "repo")

	runScript(t, repositoryDir, "git init")

	step1ExpectedDocuments := []zoekt.Document{
		helloWorld,
		fruitV1,
	}

	for _, d := range step1ExpectedDocuments {
		addDocument(t, repositoryDir, d)
	}

	runScript(t, repositoryDir, `git commit -m "initial commit"`)

	buildOptions := build.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			Name: "repository",
		},
		IsDelta: false,
	}
	buildOptions.SetDefaults()

	opts := Options{
		RepoDir:      filepath.Join(repositoryDir, ".git"),
		BuildOptions: buildOptions,
		Branches:     []string{"HEAD"},
	}

	err := IndexGitRepo(opts)
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

	for _, docs := range [][]zoekt.Document{receivedDocuments, step1ExpectedDocuments} {
		sort.Slice(docs, func(i, j int) bool {
			a, b := docs[i], docs[j]

			return a.Name < b.Name
		})
	}

	if diff := cmp.Diff(step1ExpectedDocuments, receivedDocuments, cmpopts.IgnoreFields(zoekt.Document{}, "Branches")); diff != "" {
		t.Errorf("diff in received documents (-want +got):%s\n:", diff)
	}

	addDocument(t, repositoryDir, fruitV2)
	runScript(t, repositoryDir, `git commit -m "grapes are better"`)

	buildOptionsDelta := build.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			Name: "repository",
		},
		IsDelta: true,
	}

	buildOptions.SetDefaults()

	optsDelta := Options{
		RepoDir:      filepath.Join(repositoryDir, ".git"),
		BuildOptions: buildOptionsDelta,
		Branches:     []string{"HEAD"},
	}

	err = IndexGitRepo(optsDelta)
	if err != nil {
		t.Fatalf("IndexGitRepo: %s", err)
	}

	ss, err = shards.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %s", indexDir, err)
	}
	defer ss.Close()

	searchOpts = &zoekt.SearchOptions{Whole: true}
	result, err = ss.Search(context.Background(), &query.Const{Value: true}, searchOpts)

	//result, err := ss.Search(context.Background(), &query.Regexp{Regexp: mustParseRE(".*")}, searchOpts)
	if err != nil {
		t.Fatalf("Search: %s", err)
	}

	var receivedDocumentsDelta []zoekt.Document
	for _, f := range result.Files {
		receivedDocumentsDelta = append(receivedDocumentsDelta, zoekt.Document{
			Name:    f.FileName,
			Content: f.Content,
		})
	}

	deltaExpectedDocuments := []zoekt.Document{helloWorld, fruitV2}

	for _, docs := range [][]zoekt.Document{deltaExpectedDocuments, receivedDocumentsDelta} {
		sort.Slice(docs, func(i, j int) bool {
			a, b := docs[i], docs[j]

			return a.Name < b.Name
		})
	}

	if diff := cmp.Diff(deltaExpectedDocuments, receivedDocumentsDelta, cmpopts.IgnoreFields(zoekt.Document{}, "Branches")); diff != "" {
		t.Errorf("diff in received documents (-want +got):%s\n:", diff)
	}

}

func addDocument(t *testing.T, repositoryDir string, d zoekt.Document) {
	err := os.WriteFile(filepath.Join(repositoryDir, d.Name), d.Content, 0644)
	if err != nil {
		t.Fatalf("writing file %q: %s", d.Name, err)
	}

	runScript(t, repositoryDir, fmt.Sprintf("git add %s", d.Name))
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
