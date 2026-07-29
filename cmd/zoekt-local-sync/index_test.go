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
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
)

func createTestShard(t *testing.T, indexDir, name, source string) string {
	t.Helper()

	opts := index.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			Name:   name,
			Source: source,
		},
		DisableCTags: true,
	}
	opts.SetDefaults()
	builder, err := index.NewBuilder(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.AddFile("README.md", []byte("test repository\n")); err != nil {
		t.Fatal(err)
	}
	if err := builder.Finish(); err != nil {
		t.Fatal(err)
	}
	shards := opts.FindAllShards()
	if len(shards) != 1 {
		t.Fatalf("got %d shards, want 1", len(shards))
	}
	return shards[0]
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=",
		"GIT_CONFIG_SYSTEM=",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func createGitRepository(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, filepath.Dir(path), "init", "-b", "main", path)
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("local repository\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, path, "add", ".")
	runGit(t, path, "commit", "-m", "initial commit")
	runGit(t, path, "remote", "add", "origin", "git@github.com:example/different-name.git")
}

func TestSyncIndexesWithRootRelativeName(t *testing.T) {
	root := t.TempDir()
	repositoryPath := filepath.Join(root, "team", "repo")
	createGitRepository(t, repositoryPath)
	indexDir := t.TempDir()

	var out bytes.Buffer
	if err := execute([]string{
		"-index", indexDir,
		"-disable_ctags",
		"-submodules=false",
		root,
	}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `Would index "team/repo"`) {
		t.Fatalf("preview did not report repository requiring indexing:\n%s", out.String())
	}
	if shards, err := readInventory(indexDir); err != nil {
		t.Fatal(err)
	} else if len(shards) != 0 {
		t.Fatalf("preview created %d shards", len(shards))
	}

	out.Reset()
	err := execute([]string{
		"-index", indexDir,
		"-disable_ctags",
		"-submodules=false",
		"-f",
		root,
	}, &out, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	shards, err := readInventory(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	records := recordsFromShards(shards)
	if len(records) != 1 {
		t.Fatalf("got %d indexed repositories, want 1", len(records))
	}
	if got, want := records[0].Name, "team/repo"; got != want {
		t.Fatalf("got repository name %q, want %q", got, want)
	}
	if got := records[0].Source; got != repositoryPath {
		t.Fatalf("got repository source %q, want %q", got, repositoryPath)
	}
	if !strings.HasPrefix(out.String(), `Indexing "team/repo" from `) {
		t.Fatalf("sync output did not announce repository before indexing:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `Indexed "team/repo"`) {
		t.Fatalf("sync output did not report indexed repository:\n%s", out.String())
	}

	out.Reset()
	if err := execute([]string{
		"-index", indexDir,
		"-disable_ctags",
		"-submodules=false",
		"-f",
		root,
	}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `Up to date "team/repo"`) {
		t.Fatalf("second sync did not report repository as up to date:\n%s", out.String())
	}

	out.Reset()
	if err := execute([]string{
		"-index", indexDir,
		"-disable_ctags",
		"-submodules=false",
		root,
	}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `Up to date "team/repo"`) {
		t.Fatalf("preview did not recognize up-to-date repository:\n%s", out.String())
	}
}

func TestSyncPrunesRepositoriesOutsideSelectedRoots(t *testing.T) {
	root := t.TempDir()
	createGitRepository(t, filepath.Join(root, "team", "repo"))
	indexDir := t.TempDir()
	staleShard := createTestShard(t, indexDir, "old/repo", filepath.Join(t.TempDir(), "old", "repo"))

	if err := execute([]string{
		"-index", indexDir,
		"-disable_ctags",
		"-submodules=false",
		"-f",
		root,
	}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staleShard); !os.IsNotExist(err) {
		t.Fatalf("expected repository outside selected roots to be pruned, got %v", err)
	}
}

func TestSyncDryRunAndForcePrune(t *testing.T) {
	root := t.TempDir()
	indexDir := t.TempDir()
	shard := createTestShard(t, indexDir, "gone", filepath.Join(root, "gone"))
	// Force creation of an optional metadata sidecar so the test verifies that
	// shard removal handles both index artifacts.
	tempMeta, finalMeta, err := index.JsonMarshalRepoMetaTemp(shard, &zoekt.Repository{
		Name:   "gone",
		Source: filepath.Join(root, "gone"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tempMeta, finalMeta); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(shard + ".meta"); err != nil {
		t.Fatalf("expected metadata sidecar: %v", err)
	}

	var out bytes.Buffer
	if err := execute([]string{"-index", indexDir, root}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Would remove "+shard) {
		t.Fatalf("dry-run output did not include shard removal:\n%s", out.String())
	}
	if _, err := os.Stat(shard); err != nil {
		t.Fatalf("dry run removed shard: %v", err)
	}
	if !strings.Contains(out.String(), "Pass -f to apply these changes.") {
		t.Fatalf("dry-run output did not explain how to apply changes:\n%s", out.String())
	}

	out.Reset()
	if err := execute([]string{"-index", indexDir, "-f", root}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{shard, shard + ".meta"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, got %v", path, err)
		}
	}
}

func TestListAndRemove(t *testing.T) {
	root := t.TempDir()
	indexDir := t.TempDir()
	source := filepath.Join(root, "team", "repo")
	shard := createTestShard(t, indexDir, "team/repo", source)

	var out bytes.Buffer
	if err := execute([]string{"list", "-index", indexDir}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"NAME", "team/repo", filepath.Join(root, "team", "repo"), shard} {
		if !strings.Contains(out.String(), value) {
			t.Fatalf("list output does not contain %q:\n%s", value, out.String())
		}
	}

	out.Reset()
	if err := execute([]string{"remove", "-index", indexDir, "team/repo"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Would remove "+shard) {
		t.Fatalf("remove dry-run output did not include shard:\n%s", out.String())
	}
	if _, err := os.Stat(shard); err != nil {
		t.Fatalf("remove dry run removed shard: %v", err)
	}
	if !strings.Contains(out.String(), "Pass -f to apply these changes.") {
		t.Fatalf("remove preview did not explain how to apply changes:\n%s", out.String())
	}

	if err := execute([]string{"remove", "-index", indexDir, "-f", "team/repo"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(shard); !os.IsNotExist(err) {
		t.Fatalf("expected shard to be removed, got %v", err)
	}

	shard = createTestShard(t, indexDir, "team/repo", source)
	if err := execute([]string{"remove", "-index", indexDir, "-f", source}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(shard); !os.IsNotExist(err) {
		t.Fatalf("expected shard selected by source to be removed, got %v", err)
	}
}
