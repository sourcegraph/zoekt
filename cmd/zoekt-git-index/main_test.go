package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/gitindex"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/search"
)

func TestGitRepoSpecsForWorktreesIndexesAttachedHeadsTogether(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "repo")
	runGit(t, root, "init", "-b", "main", "repo")
	runGit(t, repoDir, "config", "zoekt.name", "multi-worktree-repo")

	writeFile(t, filepath.Join(repoDir, "seed.txt"), "seed\n")
	runGit(t, repoDir, "add", "-A")
	runGit(t, repoDir, "commit", "-m", "seed")

	runGit(t, repoDir, "checkout", "-B", "feature-a")
	writeFile(t, filepath.Join(repoDir, "a.txt"), "feature-a-needle\n")
	runGit(t, repoDir, "add", "-A")
	runGit(t, repoDir, "commit", "-m", "feature-a")

	runGit(t, repoDir, "checkout", "main")
	runGit(t, repoDir, "checkout", "-B", "feature-b")
	writeFile(t, filepath.Join(repoDir, "b.txt"), "feature-b-needle\n")
	runGit(t, repoDir, "add", "-A")
	runGit(t, repoDir, "commit", "-m", "feature-b")

	runGit(t, repoDir, "checkout", "main")
	worktreeA := filepath.Join(root, "worktree-a")
	worktreeB := filepath.Join(root, "worktree-b")
	runGit(t, repoDir, "worktree", "add", worktreeA, "feature-a")
	runGit(t, repoDir, "worktree", "add", worktreeB, "feature-b")

	specs, err := gitRepoSpecs([]string{worktreeA, worktreeB}, "", nil, false, true, false)
	if err != nil {
		t.Fatalf("gitRepoSpecs: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("got %d specs, want 1", len(specs))
	}
	spec := specs[0]
	if diff := cmp.Diff([]string{"feature-a", "feature-b"}, spec.branches); diff != "" {
		t.Fatalf("branches mismatch (-want +got):\n%s", diff)
	}
	if !spec.resolveHEADToBranch {
		t.Fatal("worktree mode should resolve HEAD to branch")
	}

	indexDir := t.TempDir()
	_, err = gitindex.IndexGitRepo(gitindex.Options{
		RepoDir:             spec.dir,
		Branches:            spec.branches,
		ResolveHEADToBranch: spec.resolveHEADToBranch,
		Submodules:          false,
		Incremental:         false,
		BuildOptions: index.Options{
			RepositoryDescription: zoekt.Repository{Name: spec.name},
			IndexDir:              indexDir,
			DisableCTags:          true,
		},
	})
	if err != nil {
		t.Fatalf("IndexGitRepo: %v", err)
	}

	repo := indexedRepository(t, indexDir, "multi-worktree-repo")
	if diff := cmp.Diff([]string{"feature-a", "feature-b"}, repositoryBranchNames(repo.Branches)); diff != "" {
		t.Fatalf("indexed branch names mismatch (-want +got):\n%s", diff)
	}

	assertSearchFiles(t, indexDir, query.NewAnd(&query.Branch{Pattern: "feature-a", Exact: true}, &query.Substring{Pattern: "feature-a-needle", Content: true}), []string{"a.txt"})
	assertSearchFiles(t, indexDir, query.NewAnd(&query.Branch{Pattern: "feature-b", Exact: true}, &query.Substring{Pattern: "feature-b-needle", Content: true}), []string{"b.txt"})
	assertSearchFiles(t, indexDir, query.NewAnd(&query.Branch{Pattern: "feature-a", Exact: true}, &query.Substring{Pattern: "feature-b-needle", Content: true}), nil)
}

func TestGitRepoSpecsForWorktreesRejectsBranchesFlag(t *testing.T) {
	if _, err := gitRepoSpecs([]string{"."}, "", nil, false, true, true); err == nil {
		t.Fatal("expected -worktrees with explicit -branches to fail")
	}
}

func runGit(t *testing.T, cwd string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=",
		"GIT_CONFIG_SYSTEM=",
		"GIT_COMMITTER_NAME=Zoekt Test",
		"GIT_COMMITTER_EMAIL=zoekt@example.com",
		"GIT_AUTHOR_NAME=Zoekt Test",
		"GIT_AUTHOR_EMAIL=zoekt@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func indexedRepository(t *testing.T, indexDir, name string) *zoekt.Repository {
	t.Helper()

	opts := index.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			Name: name,
		},
	}
	repo, _, ok, err := opts.FindRepositoryMetadata()
	if err != nil {
		t.Fatalf("FindRepositoryMetadata: %v", err)
	}
	if !ok {
		t.Fatalf("FindRepositoryMetadata: repository %q not found", name)
	}
	return repo
}

func repositoryBranchNames(branches []zoekt.RepositoryBranch) []string {
	names := make([]string, 0, len(branches))
	for _, branch := range branches {
		names = append(names, branch.Name)
	}
	return names
}

func assertSearchFiles(t *testing.T, indexDir string, q query.Q, want []string) {
	t.Helper()

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher: %v", err)
	}
	defer searcher.Close()

	result, err := searcher.Search(context.Background(), q, &zoekt.SearchOptions{})
	if err != nil {
		t.Fatalf("Search(%s): %v", q.String(), err)
	}

	got := make([]string, 0, len(result.Files))
	for _, file := range result.Files {
		got = append(got, file.FileName)
	}
	sort.Strings(got)
	if want == nil {
		want = []string{}
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("search files mismatch for %s (-want +got):\n%s", q.String(), diff)
	}
}
