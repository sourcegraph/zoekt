package gitindex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/go-cmp/cmp"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/query"
)

const (
	smartStatsRepoName = "smart-branchset-repository"
	smartStatsRepoID   = 9851
)

func TestIndexGitRepo_StatsV1SmartHEADBranchSwitchTinyDiffAccepted(t *testing.T) {
	t.Parallel()

	repoDir := smartStatsCreateNearIdenticalBranchRepo(t)
	indexDir := t.TempDir()

	initialOpts := smartStatsOptions(repoDir, indexDir, []string{"HEAD", "release"})
	if _, err := IndexGitRepo(initialOpts); err != nil {
		t.Fatalf("initial IndexGitRepo: %v", err)
	}

	runGit(t, repoDir, "checkout", "feature-b")

	logPath := filepath.Join(t.TempDir(), "delta-admission.jsonl")
	deltaOpts := smartStatsOptions(repoDir, indexDir, []string{"HEAD", "release"})
	deltaOpts.BuildOptions.IsDelta = true
	deltaOpts.DeltaAdmissionLogPath = logPath
	deltaBuildCalled, normalBuildCalled := smartStatsIndexWithSpies(t, deltaOpts)
	if !deltaBuildCalled {
		t.Fatal("expected delta build to be attempted")
	}
	if normalBuildCalled {
		entry := smartStatsLastAdmissionLog(t, logPath)
		t.Fatalf("expected near-identical HEAD branch switch to be accepted by stats-v1, got fallback with log entry %#v", entry)
	}

	entry := smartStatsLastAdmissionLog(t, logPath)
	smartStatsAssertAcceptedSmallCandidate(t, entry, 0.20)
	smartStatsAssertJSONLessOrEqual(t, entry, "changed_or_deleted_paths", 2)

	cleanIndexDir := smartStatsCleanFullRebuild(t, deltaOpts)
	smartStatsAssertRepositoryBranchesMatchClean(t, indexDir, cleanIndexDir, []string{"feature-b", "release"})
	smartStatsAssertQuerySurfacesMatchClean(t, indexDir, cleanIndexDir, "feature-b", "feature-b-tiny-needle")
	smartStatsAssertQuerySurfacesMatchClean(t, indexDir, cleanIndexDir, "release", "release-needle")
	smartStatsAssertQuerySurfacesMatchClean(t, indexDir, cleanIndexDir, "feature-b", "large-shared-needle")
	smartStatsAssertNoBranchHits(t, indexDir, "feature-a", "feature-a-tiny-needle")
}

func TestIndexGitRepo_StatsV1SmartLinkedWorktreeBranchSwitchTinyDiffAccepted(t *testing.T) {
	t.Parallel()

	_, worktreeA, worktreeB := smartStatsCreateNearIdenticalLinkedWorktrees(t)
	indexDir := t.TempDir()

	initialOpts := smartStatsOptions(worktreeA, indexDir, []string{"HEAD", "release"})
	if _, err := IndexGitRepo(initialOpts); err != nil {
		t.Fatalf("initial IndexGitRepo(worktreeA): %v", err)
	}

	logPath := filepath.Join(t.TempDir(), "delta-admission.jsonl")
	deltaOpts := smartStatsOptions(worktreeB, indexDir, []string{"HEAD", "release"})
	deltaOpts.BuildOptions.IsDelta = true
	deltaOpts.DeltaAdmissionLogPath = logPath
	deltaBuildCalled, normalBuildCalled := smartStatsIndexWithSpies(t, deltaOpts)
	if !deltaBuildCalled {
		t.Fatal("expected delta build to be attempted")
	}
	if normalBuildCalled {
		entry := smartStatsLastAdmissionLog(t, logPath)
		t.Fatalf("expected linked worktree branch switch to be accepted by stats-v1, got fallback with log entry %#v", entry)
	}

	entry := smartStatsLastAdmissionLog(t, logPath)
	smartStatsAssertAcceptedSmallCandidate(t, entry, 0.20)
	smartStatsAssertJSONLessOrEqual(t, entry, "changed_or_deleted_paths", 2)

	cleanIndexDir := smartStatsCleanFullRebuild(t, deltaOpts)
	smartStatsAssertRepositoryBranchesMatchClean(t, indexDir, cleanIndexDir, []string{"feature-b", "release"})
	smartStatsAssertQuerySurfacesMatchClean(t, indexDir, cleanIndexDir, "feature-b", "feature-b-tiny-needle")
	smartStatsAssertQuerySurfacesMatchClean(t, indexDir, cleanIndexDir, "release", "release-needle")
}

func TestIndexGitRepo_StatsV1SmartBranchRenameIdenticalTreeAccepted(t *testing.T) {
	t.Parallel()

	repoDir := smartStatsCreateNearIdenticalBranchRepo(t)
	indexDir := t.TempDir()

	initialOpts := smartStatsOptions(repoDir, indexDir, []string{"HEAD", "release"})
	if _, err := IndexGitRepo(initialOpts); err != nil {
		t.Fatalf("initial IndexGitRepo: %v", err)
	}

	runGit(t, repoDir, "branch", "-m", "feature-renamed")

	logPath := filepath.Join(t.TempDir(), "delta-admission.jsonl")
	deltaOpts := smartStatsOptions(repoDir, indexDir, []string{"HEAD", "release"})
	deltaOpts.BuildOptions.IsDelta = true
	deltaOpts.DeltaAdmissionLogPath = logPath
	deltaBuildCalled, normalBuildCalled := smartStatsIndexWithSpies(t, deltaOpts)
	if !deltaBuildCalled {
		t.Fatal("expected delta build to be attempted")
	}
	if normalBuildCalled {
		entry := smartStatsLastAdmissionLog(t, logPath)
		t.Fatalf("expected identical-tree branch rename to be accepted by stats-v1, got fallback with log entry %#v", entry)
	}

	entry := smartStatsLastAdmissionLog(t, logPath)
	smartStatsAssertAcceptedSmallCandidate(t, entry, 0.01)
	smartStatsAssertJSONLessOrEqual(t, entry, "changed_or_deleted_paths", 1)

	cleanIndexDir := smartStatsCleanFullRebuild(t, deltaOpts)
	smartStatsAssertRepositoryBranchesMatchClean(t, indexDir, cleanIndexDir, []string{"feature-renamed", "release"})
	smartStatsAssertQuerySurfacesMatchClean(t, indexDir, cleanIndexDir, "feature-renamed", "feature-a-tiny-needle")
	smartStatsAssertNoBranchHits(t, indexDir, "feature-a", "feature-a-tiny-needle")
}

func TestIndexGitRepo_StatsV1SmartDoesNotRewriteUnchangedLargeFile(t *testing.T) {
	t.Parallel()

	repoDir := smartStatsCreateNearIdenticalBranchRepo(t)
	indexDir := t.TempDir()

	initialOpts := smartStatsOptions(repoDir, indexDir, []string{"HEAD", "release"})
	if _, err := IndexGitRepo(initialOpts); err != nil {
		t.Fatalf("initial IndexGitRepo: %v", err)
	}

	runGit(t, repoDir, "checkout", "feature-b")

	logPath := filepath.Join(t.TempDir(), "delta-admission.jsonl")
	deltaOpts := smartStatsOptions(repoDir, indexDir, []string{"HEAD", "release"})
	deltaOpts.BuildOptions.IsDelta = true
	deltaOpts.DeltaAdmissionLogPath = logPath
	_, normalBuildCalled := smartStatsIndexWithSpies(t, deltaOpts)
	if normalBuildCalled {
		entry := smartStatsLastAdmissionLog(t, logPath)
		t.Fatalf("expected smart branch switch not to rewrite the unchanged large file, got fallback with log entry %#v", entry)
	}

	entry := smartStatsLastAdmissionLog(t, logPath)
	candidateBytes := smartStatsJSONNumber(t, entry, "candidate_indexed_bytes")
	if candidateBytes >= float64(len(smartStatsLargeContent())/2) {
		t.Fatalf("candidate_indexed_bytes = %.0f, want well below unchanged large file size %d; log entry %#v", candidateBytes, len(smartStatsLargeContent()), entry)
	}
	smartStatsAssertJSONLessOrEqual(t, entry, "changed_or_deleted_paths", 2)
}

func TestIndexGitRepo_StatsV1IdenticalBranchAddKeepsFullRewriteFallback(t *testing.T) {
	t.Parallel()

	repoDir := smartStatsCreateSingleBranchRepo(t)
	indexDir := t.TempDir()

	initialOpts := smartStatsOptions(repoDir, indexDir, []string{"main"})
	initialOpts.ResolveHEADToBranch = false
	if _, err := IndexGitRepo(initialOpts); err != nil {
		t.Fatalf("initial IndexGitRepo: %v", err)
	}

	runGit(t, repoDir, "branch", "copy", "main")

	logPath := filepath.Join(t.TempDir(), "delta-admission.jsonl")
	deltaOpts := smartStatsOptions(repoDir, indexDir, []string{"main", "copy"})
	deltaOpts.ResolveHEADToBranch = false
	deltaOpts.BuildOptions.IsDelta = true
	deltaOpts.DeltaAdmissionLogPath = logPath
	deltaBuildCalled, normalBuildCalled := smartStatsIndexWithSpies(t, deltaOpts)
	if !deltaBuildCalled {
		t.Fatal("expected delta build to be attempted")
	}
	if !normalBuildCalled {
		t.Fatal("expected adding an identical branch to keep falling back unless branch membership can be represented without rewriting documents")
	}

	entry := smartStatsLastAdmissionLog(t, logPath)
	statsBranchAssertJSONBool(t, entry, "accepted", false)
	statsBranchAssertJSONStringContains(t, entry, "reason", "write indexed bytes ratio")
}

func TestIndexGitRepo_StatsV1BranchReorderKeepsFullRewriteFallback(t *testing.T) {
	t.Parallel()

	repoDir := smartStatsCreateNearIdenticalBranchRepo(t)
	indexDir := t.TempDir()

	initialOpts := smartStatsOptions(repoDir, indexDir, []string{"HEAD", "release"})
	if _, err := IndexGitRepo(initialOpts); err != nil {
		t.Fatalf("initial IndexGitRepo: %v", err)
	}

	logPath := filepath.Join(t.TempDir(), "delta-admission.jsonl")
	deltaOpts := smartStatsOptions(repoDir, indexDir, []string{"release", "HEAD"})
	deltaOpts.BuildOptions.IsDelta = true
	deltaOpts.DeltaAdmissionLogPath = logPath
	deltaBuildCalled, normalBuildCalled := smartStatsIndexWithSpies(t, deltaOpts)
	if !deltaBuildCalled {
		t.Fatal("expected delta build to be attempted")
	}
	if !normalBuildCalled {
		t.Fatal("expected branch reordering to avoid the smart branch-set delta path and fall back")
	}

	entry := smartStatsLastAdmissionLog(t, logPath)
	statsBranchAssertJSONBool(t, entry, "accepted", false)
	statsBranchAssertJSONStringContains(t, entry, "reason", "write indexed bytes ratio")
}

func smartStatsOptions(repoDir, indexDir string, branches []string) Options {
	return Options{
		RepoDir:                   repoDir,
		Branches:                  append([]string(nil), branches...),
		ResolveHEADToBranch:       true,
		AllowDeltaBranchSetChange: true,
		DeltaAdmissionMode:        DeltaAdmissionModeStatsV1,
		BuildOptions: index.Options{
			RepositoryDescription: zoekt.Repository{
				ID:   smartStatsRepoID,
				Name: smartStatsRepoName,
			},
			IndexDir:     indexDir,
			DisableCTags: true,
		},
	}
}

func smartStatsIndexWithSpies(t *testing.T, opts Options) (deltaBuildCalled, normalBuildCalled bool) {
	t.Helper()

	prepareDeltaSpy := func(options Options, repository *git.Repository) (repos map[fileKey]BlobLocation, branchVersions map[string]map[string]plumbing.Hash, changedOrDeletedPaths []string, err error) {
		deltaBuildCalled = true
		return prepareDeltaBuild(options, repository)
	}
	prepareNormalSpy := func(options Options, repository *git.Repository) (repos map[fileKey]BlobLocation, branchVersions map[string]map[string]plumbing.Hash, err error) {
		normalBuildCalled = true
		return prepareNormalBuild(options, repository)
	}

	if _, err := indexGitRepo(opts, gitIndexConfig{
		prepareDeltaBuild:  prepareDeltaSpy,
		prepareNormalBuild: prepareNormalSpy,
	}); err != nil {
		t.Fatalf("IndexGitRepo(delta=%t, branches=%v): %v", opts.BuildOptions.IsDelta, opts.Branches, err)
	}
	return deltaBuildCalled, normalBuildCalled
}

func smartStatsCleanFullRebuild(t *testing.T, opts Options) string {
	t.Helper()

	cleanIndexDir := t.TempDir()
	cleanOpts := opts
	cleanOpts.BuildOptions.IndexDir = cleanIndexDir
	cleanOpts.BuildOptions.IsDelta = false
	cleanOpts.DeltaAdmissionLogPath = ""
	if _, err := IndexGitRepo(cleanOpts); err != nil {
		t.Fatalf("clean IndexGitRepo: %v", err)
	}
	return cleanIndexDir
}

func smartStatsCreateNearIdenticalBranchRepo(t *testing.T) string {
	t.Helper()

	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-b", "feature-a")
	smartStatsConfigureRepo(t, repoDir)
	smartStatsWriteFiles(t, repoDir, map[string]string{
		"large/shared.txt": smartStatsLargeContent(),
		"tiny.txt":         "feature-a-tiny-needle\n",
		"common.txt":       "common-needle\n",
	})
	smartStatsWriteStableFiles(t, repoDir, 24)
	runGit(t, repoDir, "add", "-A")
	runGit(t, repoDir, "commit", "-m", "feature-a initial")

	runGit(t, repoDir, "branch", "release", "feature-a")
	runGit(t, repoDir, "checkout", "release")
	smartStatsWriteFiles(t, repoDir, map[string]string{
		"release.txt": "release-needle\n",
	})
	runGit(t, repoDir, "add", "-A")
	runGit(t, repoDir, "commit", "-m", "release")

	runGit(t, repoDir, "checkout", "-B", "feature-b", "feature-a")
	smartStatsWriteFiles(t, repoDir, map[string]string{
		"tiny.txt": "feature-b-tiny-needle\n",
	})
	runGit(t, repoDir, "add", "-A")
	runGit(t, repoDir, "commit", "-m", "feature-b tiny change")

	runGit(t, repoDir, "checkout", "feature-a")
	return repoDir
}

func smartStatsCreateNearIdenticalLinkedWorktrees(t *testing.T) (repoDir, worktreeA, worktreeB string) {
	t.Helper()

	root := t.TempDir()
	repoDir = filepath.Join(root, "repo")
	runGit(t, root, "init", "-b", "main", "repo")
	smartStatsConfigureRepo(t, repoDir)
	smartStatsWriteFiles(t, repoDir, map[string]string{
		"large/shared.txt": smartStatsLargeContent(),
		"tiny.txt":         "base-tiny-needle\n",
	})
	smartStatsWriteStableFiles(t, repoDir, 24)
	runGit(t, repoDir, "add", "-A")
	runGit(t, repoDir, "commit", "-m", "base")

	runGit(t, repoDir, "checkout", "-B", "release", "main")
	smartStatsWriteFiles(t, repoDir, map[string]string{
		"release.txt": "release-needle\n",
	})
	runGit(t, repoDir, "add", "-A")
	runGit(t, repoDir, "commit", "-m", "release")

	runGit(t, repoDir, "checkout", "-B", "feature-a", "main")
	smartStatsWriteFiles(t, repoDir, map[string]string{
		"tiny.txt": "feature-a-tiny-needle\n",
	})
	runGit(t, repoDir, "add", "-A")
	runGit(t, repoDir, "commit", "-m", "feature-a")

	runGit(t, repoDir, "checkout", "-B", "feature-b", "feature-a")
	smartStatsWriteFiles(t, repoDir, map[string]string{
		"tiny.txt": "feature-b-tiny-needle\n",
	})
	runGit(t, repoDir, "add", "-A")
	runGit(t, repoDir, "commit", "-m", "feature-b tiny change")

	runGit(t, repoDir, "checkout", "main")
	worktreeA = filepath.Join(root, "worktree-a")
	worktreeB = filepath.Join(root, "worktree-b")
	runGit(t, repoDir, "worktree", "add", worktreeA, "feature-a")
	runGit(t, repoDir, "worktree", "add", worktreeB, "feature-b")
	return repoDir, worktreeA, worktreeB
}

func smartStatsCreateSingleBranchRepo(t *testing.T) string {
	t.Helper()

	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-b", "main")
	smartStatsConfigureRepo(t, repoDir)
	smartStatsWriteFiles(t, repoDir, map[string]string{
		"large/shared.txt": smartStatsLargeContent(),
		"tiny.txt":         "main-tiny-needle\n",
	})
	smartStatsWriteStableFiles(t, repoDir, 24)
	runGit(t, repoDir, "add", "-A")
	runGit(t, repoDir, "commit", "-m", "main")
	return repoDir
}

func smartStatsConfigureRepo(t *testing.T, repoDir string) {
	t.Helper()

	runGit(t, repoDir, "config", "zoekt.name", smartStatsRepoName)
	runGit(t, repoDir, "config", "zoekt.repoid", fmt.Sprintf("%d", smartStatsRepoID))
}

func smartStatsWriteFiles(t *testing.T, repoDir string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(repoDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}
}

func smartStatsWriteStableFiles(t *testing.T, repoDir string, n int) {
	t.Helper()

	files := make(map[string]string, n)
	for i := range n {
		files[fmt.Sprintf("stable/file-%02d.txt", i)] = fmt.Sprintf("stable-needle-%02d\n", i)
	}
	smartStatsWriteFiles(t, repoDir, files)
}

func smartStatsLargeContent() string {
	return "large-shared-needle\n" + strings.Repeat("large unchanged payload\n", 8192)
}

func smartStatsLastAdmissionLog(t *testing.T, path string) map[string]any {
	t.Helper()

	entries := statsBranchReadAdmissionLogObjects(t, path)
	if len(entries) == 0 {
		t.Fatalf("expected at least one admission log entry in %q", path)
	}
	return entries[len(entries)-1]
}

func smartStatsAssertAcceptedSmallCandidate(t *testing.T, entry map[string]any, maxWriteRatio float64) {
	t.Helper()

	statsBranchAssertJSONBool(t, entry, "accepted", true)
	statsBranchAssertJSONString(t, entry, "reason", "accepted")
	ratio := smartStatsJSONNumber(t, entry, "write_bytes_ratio")
	if ratio > maxWriteRatio {
		t.Fatalf("write_bytes_ratio = %.4f, want <= %.4f in log entry %#v", ratio, maxWriteRatio, entry)
	}
}

func smartStatsAssertJSONLessOrEqual(t *testing.T, entry map[string]any, key string, want float64) {
	t.Helper()

	got := smartStatsJSONNumber(t, entry, key)
	if got > want {
		t.Fatalf("admission log key %q = %.4f, want <= %.4f in %#v", key, got, want, entry)
	}
}

func smartStatsJSONNumber(t *testing.T, entry map[string]any, key string) float64 {
	t.Helper()

	got, ok := entry[key].(float64)
	if !ok {
		t.Fatalf("admission log key %q = %#v, want number in %#v", key, entry[key], entry)
	}
	return got
}

func smartStatsAssertRepositoryBranchesMatchClean(t *testing.T, indexDir, cleanIndexDir string, wantBranches []string) {
	t.Helper()

	gotRepo := smartStatsIndexedRepository(t, indexDir)
	cleanRepo := smartStatsIndexedRepository(t, cleanIndexDir)
	if diff := cmp.Diff(wantBranches, smartStatsRepositoryBranchNames(gotRepo.Branches)); diff != "" {
		t.Fatalf("delta branch names mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(cleanRepo.Branches, gotRepo.Branches); diff != "" {
		t.Fatalf("delta branch metadata differs from clean rebuild (-clean +delta):\n%s", diff)
	}
}

func smartStatsAssertQuerySurfacesMatchClean(t *testing.T, indexDir, cleanIndexDir, branch, pattern string) {
	t.Helper()

	contentQuery := &query.Substring{Pattern: pattern, Content: true}
	smartStatsAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "unfiltered "+pattern, contentQuery)
	smartStatsAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "branch:"+branch+" "+pattern, query.NewAnd(&query.Branch{Pattern: branch, Exact: true}, contentQuery))
	smartStatsAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "BranchesRepos:"+branch+" "+pattern, query.NewAnd(query.NewSingleBranchesRepos(branch, smartStatsRepoID), contentQuery))
}

func smartStatsAssertNoBranchHits(t *testing.T, indexDir, branch, pattern string) {
	t.Helper()

	contentQuery := &query.Substring{Pattern: pattern, Content: true}
	for _, q := range []query.Q{
		query.NewAnd(&query.Branch{Pattern: branch, Exact: true}, contentQuery),
		query.NewAnd(query.NewSingleBranchesRepos(branch, smartStatsRepoID), contentQuery),
	} {
		if hits := statsBranchSearch(t, indexDir, q); len(hits) != 0 {
			t.Fatalf("expected no hits for %s, got %+v", q.String(), hits)
		}
	}
}

func smartStatsAssertQueryMatchesClean(t *testing.T, indexDir, cleanIndexDir, label string, q query.Q) {
	t.Helper()

	got := statsBranchSearch(t, indexDir, q)
	want := statsBranchSearch(t, cleanIndexDir, q)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("%s differs from clean rebuild (-clean +delta):\n%s", label, diff)
	}
}

func smartStatsIndexedRepository(t *testing.T, indexDir string) *zoekt.Repository {
	t.Helper()

	opts := index.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			ID:   smartStatsRepoID,
			Name: smartStatsRepoName,
		},
	}
	repo, _, ok, err := opts.FindRepositoryMetadata()
	if err != nil {
		t.Fatalf("FindRepositoryMetadata: %v", err)
	}
	if !ok {
		t.Fatalf("FindRepositoryMetadata: repository %q not found", smartStatsRepoName)
	}
	return repo
}

func smartStatsRepositoryBranchNames(branches []zoekt.RepositoryBranch) []string {
	names := make([]string, 0, len(branches))
	for _, branch := range branches {
		names = append(names, branch.Name)
	}
	return names
}
