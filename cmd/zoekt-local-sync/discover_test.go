// Copyright 2026 Sourcegraph, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestDiscoverRepositories(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		filepath.Join(root, "team", "normal", ".git"),
		filepath.Join(root, "bare.git", "objects"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	linked := filepath.Join(root, "linked")
	if err := os.MkdirAll(linked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repositories, err := discoverRepositories([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, repo := range repositories {
		names = append(names, repo.Name)
	}
	if diff := cmp.Diff([]string{"bare", "linked", "team/normal"}, names); diff != "" {
		t.Fatalf("unexpected repositories (-want +got):\n%s", diff)
	}
}

func TestDiscoverRootRepository(t *testing.T) {
	parent := t.TempDir()
	for _, test := range []struct {
		name    string
		root    string
		gitPath string
		want    string
	}{
		{name: "worktree", root: filepath.Join(parent, "worktree"), gitPath: ".git", want: "worktree"},
		{name: "bare", root: filepath.Join(parent, "bare.git"), gitPath: "objects", want: "bare"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.MkdirAll(filepath.Join(test.root, test.gitPath), 0o755); err != nil {
				t.Fatal(err)
			}
			repositories, err := discoverRepositories([]string{test.root})
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff([]string{test.want}, []string{repositories[0].Name}); diff != "" {
				t.Fatalf("unexpected repositories (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDiscoverRepositoriesRejectsDuplicateNames(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	for _, root := range []string{rootA, rootB} {
		if err := os.MkdirAll(filepath.Join(root, "repo", ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	_, err := discoverRepositories([]string{rootA, rootB})
	if err == nil || !strings.Contains(err.Error(), `duplicate repository name "repo"`) {
		t.Fatalf("got error %v, want duplicate repository name", err)
	}
}
