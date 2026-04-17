package gitindex

import (
	"context"
	"github.com/RoaringBitmap/roaring"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/go-cmp/cmp"
	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/search"
	"os"
	"os/exec"
	"path/filepath"
	"regexp/syntax"
	"sort"
	"strings"
	"testing"
)

// ---- delta_multibranch_head_worktree_test.go ----

const (
	headWorktreeRepoName = "head-worktree-repository"
	headWorktreeRepoID   = 9701

	headWorktreeLinkedRepoAName = "head-worktree-linked-a"
	headWorktreeLinkedRepoAID   = 9702
	headWorktreeLinkedRepoBName = "head-worktree-linked-b"
	headWorktreeLinkedRepoBID   = 9703
)

func TestIndexGitRepo_DeltaHeadWorktreeSingleHEADResolvesBranchSwitch(t *testing.T) {
	t.Parallel()

	repoDir := headWorktreeInitRepo(t)
	indexDir := t.TempDir()

	initialOpts := headWorktreeOptions(repoDir, indexDir, headWorktreeRepoName, headWorktreeRepoID, []string{"HEAD"})
	if _, err := IndexGitRepo(initialOpts); err != nil {
		t.Fatalf("initial IndexGitRepo: %v", err)
	}

	headWorktreeRenameCurrentBranch(t, repoDir, "feature-a", "feature-b")
	headWorktreeReplaceFilesAndCommit(t, repoDir, map[string]string{
		"branch.txt": "feature-b-single-needle\n",
	}, "feature-b single")

	deltaOpts := headWorktreeOptions(repoDir, indexDir, headWorktreeRepoName, headWorktreeRepoID, []string{"HEAD"})
	deltaOpts.BuildOptions.IsDelta = true
	deltaBuildCalled, normalBuildCalled := headWorktreeIndexWithPrepareSpies(t, deltaOpts)
	if !deltaBuildCalled {
		t.Fatal("expected delta build to be attempted")
	}
	if normalBuildCalled {
		t.Fatal("expected resolved HEAD feature-a -> feature-b update to stay on the delta path")
	}

	cleanIndexDir := headWorktreeCleanFullRebuild(t, deltaOpts)
	headWorktreeAssertBranchMetadataMatchesClean(t, indexDir, cleanIndexDir, headWorktreeRepoName, headWorktreeRepoID, []string{"feature-b"})
	headWorktreeAssertLookup(t, indexDir, cleanIndexDir, headWorktreeRepoID, headWorktreeLookup{
		name:      "feature-b branch content",
		branch:    "feature-b",
		pattern:   "feature-b-single-needle",
		wantFiles: []string{"branch.txt"},
	})
	headWorktreeAssertLookup(t, indexDir, cleanIndexDir, headWorktreeRepoID, headWorktreeLookup{
		name:    "feature-a old branch absent",
		branch:  "feature-a",
		pattern: "feature-a-initial-needle",
	})
}

func TestIndexGitRepo_DeltaHeadWorktreeMultiBranchHEADAndReleaseSwitchesHEADBranch(t *testing.T) {
	t.Parallel()

	repoDir := headWorktreeInitRepo(t)
	indexDir := t.TempDir()

	initialOpts := headWorktreeOptions(repoDir, indexDir, headWorktreeRepoName, headWorktreeRepoID, []string{"HEAD", "release"})
	if _, err := IndexGitRepo(initialOpts); err != nil {
		t.Fatalf("initial IndexGitRepo: %v", err)
	}

	headWorktreeRenameCurrentBranch(t, repoDir, "feature-a", "feature-b")
	headWorktreeReplaceFilesAndCommit(t, repoDir, map[string]string{
		"branch.txt": "feature-b-multi-needle\n",
	}, "feature-b multi")

	deltaOpts := headWorktreeOptions(repoDir, indexDir, headWorktreeRepoName, headWorktreeRepoID, []string{"HEAD", "release"})
	deltaOpts.BuildOptions.IsDelta = true
	deltaBuildCalled, normalBuildCalled := headWorktreeIndexWithPrepareSpies(t, deltaOpts)
	if !deltaBuildCalled {
		t.Fatal("expected delta build to be attempted")
	}
	if normalBuildCalled {
		t.Fatal("expected resolved HEAD feature-a -> feature-b with release to stay on the delta path")
	}

	cleanIndexDir := headWorktreeCleanFullRebuild(t, deltaOpts)
	headWorktreeAssertBranchMetadataMatchesClean(t, indexDir, cleanIndexDir, headWorktreeRepoName, headWorktreeRepoID, []string{"feature-b", "release"})
	headWorktreeAssertLookup(t, indexDir, cleanIndexDir, headWorktreeRepoID, headWorktreeLookup{
		name:      "feature-b branch content",
		branch:    "feature-b",
		pattern:   "feature-b-multi-needle",
		wantFiles: []string{"branch.txt"},
	})
	headWorktreeAssertLookup(t, indexDir, cleanIndexDir, headWorktreeRepoID, headWorktreeLookup{
		name:      "release branch preserved",
		branch:    "release",
		pattern:   "release-initial-needle",
		wantFiles: []string{"release.txt"},
	})
	headWorktreeAssertLookup(t, indexDir, cleanIndexDir, headWorktreeRepoID, headWorktreeLookup{
		name:    "feature-a old branch absent",
		branch:  "feature-a",
		pattern: "feature-a-initial-needle",
	})
}

func TestIndexGitRepo_DeltaHeadWorktreeMultiBranchHEADResolvingToDuplicateReleaseFallsBack(t *testing.T) {
	t.Parallel()

	repoDir := headWorktreeInitRepo(t)
	indexDir := t.TempDir()

	initialOpts := headWorktreeOptions(repoDir, indexDir, headWorktreeRepoName, headWorktreeRepoID, []string{"HEAD", "release"})
	if _, err := IndexGitRepo(initialOpts); err != nil {
		t.Fatalf("initial IndexGitRepo: %v", err)
	}

	headWorktreeRunGit(t, repoDir, "checkout", "release")

	deltaOpts := headWorktreeOptions(repoDir, indexDir, headWorktreeRepoName, headWorktreeRepoID, []string{"HEAD", "release"})
	deltaOpts.BuildOptions.IsDelta = true
	deltaBuildCalled, normalBuildCalled := headWorktreeIndexWithPrepareSpies(t, deltaOpts)
	if !deltaBuildCalled {
		t.Fatal("expected delta build to be attempted before conservative fallback")
	}
	if normalBuildCalled {
		t.Fatal("expected HEAD resolving to duplicate release branch to dedupe and stay on the delta path")
	}

	cleanIndexDir := headWorktreeCleanFullRebuild(t, deltaOpts)
	headWorktreeAssertBranchMetadataMatchesClean(t, indexDir, cleanIndexDir, headWorktreeRepoName, headWorktreeRepoID, []string{"release"})
	headWorktreeAssertLookup(t, indexDir, cleanIndexDir, headWorktreeRepoID, headWorktreeLookup{
		name:      "release content after duplicate HEAD dedupe",
		branch:    "release",
		pattern:   "release-initial-needle",
		wantFiles: []string{"release.txt"},
	})
	headWorktreeAssertLookup(t, indexDir, cleanIndexDir, headWorktreeRepoID, headWorktreeLookup{
		name:    "feature-a branch removed after fallback",
		branch:  "feature-a",
		pattern: "feature-a-initial-needle",
	})
}

func TestIndexGitRepo_DeltaHeadWorktreeDetachedHEADInMultiBranchIndexFallsBack(t *testing.T) {
	t.Parallel()

	repoDir := headWorktreeInitRepo(t)
	indexDir := t.TempDir()

	initialOpts := headWorktreeOptions(repoDir, indexDir, headWorktreeRepoName, headWorktreeRepoID, []string{"HEAD", "release"})
	if _, err := IndexGitRepo(initialOpts); err != nil {
		t.Fatalf("initial IndexGitRepo: %v", err)
	}

	headWorktreeRunGit(t, repoDir, "checkout", "--detach", "feature-a")
	headWorktreeReplaceFilesAndCommit(t, repoDir, map[string]string{
		"branch.txt": "detached-head-needle\n",
	}, "detached head")

	deltaOpts := headWorktreeOptions(repoDir, indexDir, headWorktreeRepoName, headWorktreeRepoID, []string{"HEAD", "release"})
	deltaOpts.BuildOptions.IsDelta = true
	deltaBuildCalled, normalBuildCalled := headWorktreeIndexWithPrepareSpies(t, deltaOpts)
	if !deltaBuildCalled {
		t.Fatal("expected delta build to be attempted before conservative fallback")
	}
	if normalBuildCalled {
		t.Fatal("expected detached HEAD in a multi-branch index to stay on the delta path")
	}

	cleanIndexDir := headWorktreeCleanFullRebuild(t, deltaOpts)
	headWorktreeAssertBranchMetadataMatchesClean(t, indexDir, cleanIndexDir, headWorktreeRepoName, headWorktreeRepoID, []string{"HEAD", "release"})
	headWorktreeAssertLookup(t, indexDir, cleanIndexDir, headWorktreeRepoID, headWorktreeLookup{
		name:      "detached HEAD content remains queryable as HEAD",
		branch:    "HEAD",
		pattern:   "detached-head-needle",
		wantFiles: []string{"branch.txt"},
	})
	headWorktreeAssertLookup(t, indexDir, cleanIndexDir, headWorktreeRepoID, headWorktreeLookup{
		name:      "release branch preserved",
		branch:    "release",
		pattern:   "release-initial-needle",
		wantFiles: []string{"release.txt"},
	})
	headWorktreeAssertLookup(t, indexDir, cleanIndexDir, headWorktreeRepoID, headWorktreeLookup{
		name:    "feature-a branch name not kept for detached HEAD",
		branch:  "feature-a",
		pattern: "feature-a-initial-needle",
	})
}

func TestIndexGitRepo_HeadWorktreeTwoLinkedWorktreesSameIndexDirKeepResolvedIdentities(t *testing.T) {
	t.Parallel()

	_, worktreeA, worktreeB := headWorktreeInitLinkedWorktrees(t)
	indexDir := t.TempDir()

	optsA := headWorktreeOptions(worktreeA, indexDir, headWorktreeLinkedRepoAName, headWorktreeLinkedRepoAID, []string{"HEAD"})
	if _, err := IndexGitRepo(optsA); err != nil {
		t.Fatalf("IndexGitRepo(worktree A): %v", err)
	}
	optsB := headWorktreeOptions(worktreeB, indexDir, headWorktreeLinkedRepoBName, headWorktreeLinkedRepoBID, []string{"HEAD"})
	if _, err := IndexGitRepo(optsB); err != nil {
		t.Fatalf("IndexGitRepo(worktree B): %v", err)
	}

	headWorktreeAssertBranchMetadata(t, indexDir, headWorktreeLinkedRepoAName, headWorktreeLinkedRepoAID, []string{"feature-a"})
	headWorktreeAssertBranchMetadata(t, indexDir, headWorktreeLinkedRepoBName, headWorktreeLinkedRepoBID, []string{"feature-b"})

	headWorktreeAssertSearchFileNames(t, indexDir, "worktree A feature-a lookup",
		query.NewAnd(query.NewSingleBranchesRepos("feature-a", headWorktreeLinkedRepoAID), headWorktreeContentQuery("worktree-a-needle")),
		[]string{"a.txt"})
	headWorktreeAssertSearchFileNames(t, indexDir, "worktree B feature-b lookup",
		query.NewAnd(query.NewSingleBranchesRepos("feature-b", headWorktreeLinkedRepoBID), headWorktreeContentQuery("worktree-b-needle")),
		[]string{"b.txt"})
	headWorktreeAssertSearchFileNames(t, indexDir, "worktree A not conflated with feature-b",
		query.NewAnd(query.NewSingleBranchesRepos("feature-b", headWorktreeLinkedRepoAID), &query.Const{Value: true}),
		nil)
	headWorktreeAssertSearchFileNames(t, indexDir, "worktree B not conflated with feature-a",
		query.NewAnd(query.NewSingleBranchesRepos("feature-a", headWorktreeLinkedRepoBID), &query.Const{Value: true}),
		nil)
}

type headWorktreeLookup struct {
	name      string
	branch    string
	pattern   string
	wantFiles []string
}

type headWorktreeSearchHit struct {
	Repository   string
	RepositoryID uint32
	FileName     string
	Branches     []string
	Version      string
	Content      string
}

func headWorktreeInitRepo(t *testing.T) string {
	t.Helper()

	repoDir := t.TempDir()
	headWorktreeRunGit(t, repoDir, "init", "-b", "feature-a")
	headWorktreeReplaceFilesAndCommit(t, repoDir, map[string]string{
		"branch.txt": "feature-a-initial-needle\n",
	}, "initial feature-a")

	headWorktreeRunGit(t, repoDir, "checkout", "-B", "release")
	headWorktreeReplaceFilesAndCommit(t, repoDir, map[string]string{
		"release.txt": "release-initial-needle\n",
	}, "initial release")

	headWorktreeRunGit(t, repoDir, "checkout", "feature-a")
	return repoDir
}

func headWorktreeInitLinkedWorktrees(t *testing.T) (repoDir, worktreeA, worktreeB string) {
	t.Helper()

	root := t.TempDir()
	repoDir = filepath.Join(root, "repo")
	headWorktreeRunGit(t, root, "init", "-b", "main", "repo")
	headWorktreeReplaceFilesAndCommit(t, repoDir, map[string]string{
		"seed.txt": "seed\n",
	}, "seed")

	headWorktreeRunGit(t, repoDir, "checkout", "-B", "feature-a", "main")
	headWorktreeReplaceFilesAndCommit(t, repoDir, map[string]string{
		"a.txt": "worktree-a-needle\n",
	}, "feature-a")

	headWorktreeRunGit(t, repoDir, "checkout", "-B", "feature-b", "main")
	headWorktreeReplaceFilesAndCommit(t, repoDir, map[string]string{
		"b.txt": "worktree-b-needle\n",
	}, "feature-b")

	headWorktreeRunGit(t, repoDir, "checkout", "main")
	worktreeA = filepath.Join(root, "worktree-a")
	worktreeB = filepath.Join(root, "worktree-b")
	headWorktreeRunGit(t, repoDir, "worktree", "add", worktreeA, "feature-a")
	headWorktreeRunGit(t, repoDir, "worktree", "add", worktreeB, "feature-b")
	return repoDir, worktreeA, worktreeB
}

func headWorktreeOptions(repoDir, indexDir, repoName string, repoID uint32, branches []string) Options {
	return Options{
		RepoDir:             repoDir,
		Branches:            append([]string(nil), branches...),
		ResolveHEADToBranch: true,
		BuildOptions: index.Options{
			RepositoryDescription: zoekt.Repository{
				ID:   repoID,
				Name: repoName,
			},
			IndexDir:     indexDir,
			DisableCTags: true,
		},
	}
}

func headWorktreeIndexWithPrepareSpies(t *testing.T, opts Options) (deltaBuildCalled, normalBuildCalled bool) {
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
		t.Fatalf("IndexGitRepo: %v", err)
	}

	return deltaBuildCalled, normalBuildCalled
}

func headWorktreeCleanFullRebuild(t *testing.T, opts Options) string {
	t.Helper()

	cleanIndexDir := t.TempDir()
	cleanOpts := opts
	cleanOpts.BuildOptions.IndexDir = cleanIndexDir
	cleanOpts.BuildOptions.IsDelta = false
	cleanOpts.BuildOptions.RepositoryDescription.DeltaStats = nil
	if _, err := IndexGitRepo(cleanOpts); err != nil {
		t.Fatalf("clean full IndexGitRepo: %v", err)
	}
	return cleanIndexDir
}

func headWorktreeReplaceFilesAndCommit(t *testing.T, repoDir string, files map[string]string, message string) {
	t.Helper()

	headWorktreeRunGit(t, repoDir, "rm", "-r", "--ignore-unmatch", ".")
	for name, content := range files {
		path := filepath.Join(repoDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}
	headWorktreeRunGit(t, repoDir, "add", "-A")
	headWorktreeRunGit(t, repoDir, "commit", "--allow-empty", "-m", message)
}

func headWorktreeRenameCurrentBranch(t *testing.T, repoDir, oldBranch, newBranch string) {
	t.Helper()
	headWorktreeRunGit(t, repoDir, "branch", "-m", oldBranch, newBranch)
}

func headWorktreeAssertBranchMetadataMatchesClean(t *testing.T, indexDir, cleanIndexDir, repoName string, repoID uint32, wantBranches []string) {
	t.Helper()

	headWorktreeAssertBranchMetadata(t, indexDir, repoName, repoID, wantBranches)
	deltaRepo := headWorktreeIndexedRepository(t, indexDir, repoName, repoID)
	cleanRepo := headWorktreeIndexedRepository(t, cleanIndexDir, repoName, repoID)
	if diff := cmp.Diff(cleanRepo.Branches, deltaRepo.Branches); diff != "" {
		t.Fatalf("delta branch metadata differs from clean full rebuild (-want +got):\n%s", diff)
	}
}

func headWorktreeAssertBranchMetadata(t *testing.T, indexDir, repoName string, repoID uint32, wantBranches []string) {
	t.Helper()

	repo := headWorktreeIndexedRepository(t, indexDir, repoName, repoID)
	if diff := cmp.Diff(wantBranches, headWorktreeBranchNames(repo.Branches)); diff != "" {
		t.Fatalf("indexed branch names mismatch (-want +got):\n%s", diff)
	}
}

func headWorktreeAssertLookup(t *testing.T, indexDir, cleanIndexDir string, repoID uint32, lookup headWorktreeLookup) {
	t.Helper()

	base := headWorktreeContentQuery(lookup.pattern)
	if lookup.branch == "" {
		headWorktreeAssertQueryMatchesClean(t, indexDir, cleanIndexDir, lookup.name+"/unfiltered", base, lookup.wantFiles)
		return
	}

	branchQuery := query.NewAnd(&query.Branch{Pattern: lookup.branch, Exact: true}, base)
	headWorktreeAssertQueryMatchesClean(t, indexDir, cleanIndexDir, lookup.name+"/branch", branchQuery, lookup.wantFiles)

	branchesReposQuery := query.NewAnd(query.NewSingleBranchesRepos(lookup.branch, repoID), base)
	headWorktreeAssertQueryMatchesClean(t, indexDir, cleanIndexDir, lookup.name+"/branches-repos", branchesReposQuery, lookup.wantFiles)
}

func headWorktreeAssertQueryMatchesClean(t *testing.T, indexDir, cleanIndexDir, label string, q query.Q, wantFiles []string) {
	t.Helper()

	deltaHits := headWorktreeSearchHits(t, indexDir, q)
	cleanHits := headWorktreeSearchHits(t, cleanIndexDir, q)
	if diff := cmp.Diff(cleanHits, deltaHits); diff != "" {
		t.Fatalf("%s: delta search results differ from clean full rebuild (-want +got):\n%s", label, diff)
	}

	gotFiles := headWorktreeFileNames(deltaHits)
	wantFiles = append([]string(nil), wantFiles...)
	if wantFiles == nil {
		wantFiles = []string{}
	}
	sort.Strings(wantFiles)
	if diff := cmp.Diff(wantFiles, gotFiles); diff != "" {
		t.Fatalf("%s: search file names mismatch (-want +got):\n%s", label, diff)
	}
}

func headWorktreeAssertSearchFileNames(t *testing.T, indexDir, label string, q query.Q, wantFiles []string) {
	t.Helper()

	gotFiles := headWorktreeFileNames(headWorktreeSearchHits(t, indexDir, q))
	wantFiles = append([]string(nil), wantFiles...)
	if wantFiles == nil {
		wantFiles = []string{}
	}
	sort.Strings(wantFiles)
	if diff := cmp.Diff(wantFiles, gotFiles); diff != "" {
		t.Fatalf("%s: search file names mismatch (-want +got):\n%s", label, diff)
	}
}

func headWorktreeSearchHits(t *testing.T, indexDir string, q query.Q) []headWorktreeSearchHit {
	t.Helper()

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%q): %v", indexDir, err)
	}
	defer searcher.Close()

	result, err := searcher.Search(context.Background(), q, &zoekt.SearchOptions{
		Whole:                true,
		ShardMaxMatchCount:   1000,
		TotalMaxMatchCount:   1000,
		MaxDocDisplayCount:   1000,
		MaxMatchDisplayCount: 1000,
	})
	if err != nil {
		t.Fatalf("Search(%s): %v", q, err)
	}

	hits := make([]headWorktreeSearchHit, 0, len(result.Files))
	for _, file := range result.Files {
		branches := append([]string(nil), file.Branches...)
		sort.Strings(branches)
		hits = append(hits, headWorktreeSearchHit{
			Repository:   file.Repository,
			RepositoryID: file.RepositoryID,
			FileName:     file.FileName,
			Branches:     branches,
			Version:      file.Version,
			Content:      string(file.Content),
		})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].RepositoryID != hits[j].RepositoryID {
			return hits[i].RepositoryID < hits[j].RepositoryID
		}
		if hits[i].Repository != hits[j].Repository {
			return hits[i].Repository < hits[j].Repository
		}
		if hits[i].FileName != hits[j].FileName {
			return hits[i].FileName < hits[j].FileName
		}
		if hits[i].Content != hits[j].Content {
			return hits[i].Content < hits[j].Content
		}
		if hits[i].Version != hits[j].Version {
			return hits[i].Version < hits[j].Version
		}
		return strings.Join(hits[i].Branches, "\x00") < strings.Join(hits[j].Branches, "\x00")
	})
	return hits
}

func headWorktreeContentQuery(pattern string) query.Q {
	if pattern == "" {
		return &query.Const{Value: true}
	}
	return &query.Substring{Pattern: pattern, Content: true}
}

func headWorktreeFileNames(hits []headWorktreeSearchHit) []string {
	names := make([]string, 0, len(hits))
	for _, hit := range hits {
		names = append(names, hit.FileName)
	}
	sort.Strings(names)
	return names
}

func headWorktreeIndexedRepository(t *testing.T, indexDir, repoName string, repoID uint32) *zoekt.Repository {
	t.Helper()

	opts := index.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			ID:   repoID,
			Name: repoName,
		},
	}
	repo, _, ok, err := opts.FindRepositoryMetadata()
	if err != nil {
		t.Fatalf("FindRepositoryMetadata(%q): %v", repoName, err)
	}
	if !ok {
		t.Fatalf("FindRepositoryMetadata(%q): repository not found", repoName)
	}
	return repo
}

func headWorktreeBranchNames(branches []zoekt.RepositoryBranch) []string {
	names := make([]string, 0, len(branches))
	for _, branch := range branches {
		names = append(names, branch.Name)
	}
	return names
}

func headWorktreeRunGit(t *testing.T, cwd string, args ...string) {
	t.Helper()

	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", cwd, err)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=",
		"GIT_CONFIG_SYSTEM=",
		"GIT_COMMITTER_NAME=Simone Weil",
		"GIT_COMMITTER_EMAIL=simone@apache.com",
		"GIT_AUTHOR_NAME=Simone Weil",
		"GIT_AUTHOR_EMAIL=simone@apache.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// ---- delta_multibranch_query_surfaces_test.go ----

const (
	querySurfaceRepoName = "query-surface-repository"
	querySurfaceRepoID   = 91046

	querySurfaceMain       = "main"
	querySurfaceOldFeature = "feature-a"
	querySurfaceNewFeature = "feature-b"
	querySurfaceOldRelease = "old-release"
	querySurfaceNewRelease = "new-release"
)

func TestIndexGitRepo_DeltaMultiBranchQuerySurfaces(t *testing.T) {
	t.Parallel()

	repoDir := querySurfaceInitRepo(t)
	indexDir := t.TempDir()

	initialBranches := []string{querySurfaceOldFeature, querySurfaceOldRelease, querySurfaceMain}
	initialOpts := querySurfaceOptions(repoDir, indexDir, initialBranches)
	if _, err := IndexGitRepo(initialOpts); err != nil {
		t.Fatalf("initial IndexGitRepo: %v", err)
	}

	querySurfaceMutateRenameAddRemove(t, repoDir)

	finalBranches := []string{querySurfaceNewFeature, querySurfaceNewRelease, querySurfaceMain}
	deltaOpts := querySurfaceOptions(repoDir, indexDir, finalBranches)
	deltaOpts.BuildOptions.IsDelta = true
	deltaBuildCalled, normalBuildCalled := querySurfaceIndexGitRepoWithPrepareSpies(t, deltaOpts)
	if !deltaBuildCalled {
		t.Error("expected query surface scenario to attempt a delta build")
	}
	if normalBuildCalled {
		t.Error("expected query surface scenario to stay on the delta path, got normal-build fallback")
	}

	cleanIndexDir := t.TempDir()
	cleanOpts := querySurfaceOptions(repoDir, cleanIndexDir, finalBranches)
	if _, err := IndexGitRepo(cleanOpts); err != nil {
		t.Fatalf("clean IndexGitRepo: %v", err)
	}

	querySurfaceAssertRepositoryBranchesMatchClean(t, indexDir, cleanIndexDir, finalBranches)

	querySurfaces := []struct {
		name      string
		q         query.Q
		wantFiles []string
	}{
		{
			name:      "plain substring across all branches",
			q:         &query.Substring{Pattern: "query-surface-final-common"},
			wantFiles: []string{"new_release_surface.go", "renamed_surface.go"},
		},
		{
			name:      "parsed branch query for renamed branch",
			q:         querySurfaceParseQuery(t, "branch:"+querySurfaceNewFeature+" query-surface-renamed-only"),
			wantFiles: []string{"renamed_surface.go"},
		},
		{
			name:      "parsed branch query for removed branch",
			q:         querySurfaceParseQuery(t, "branch:"+querySurfaceOldRelease+" query-surface-old-release-only"),
			wantFiles: nil,
		},
		{
			name: "BranchesRepos single branch and repo ID",
			q: query.NewAnd(
				query.NewSingleBranchesRepos(querySurfaceNewRelease, querySurfaceRepoID),
				&query.Substring{Pattern: "query-surface-new-release-only", Content: true},
			),
			wantFiles: []string{"new_release_surface.go"},
		},
		{
			name: "BranchesRepos multiple branch/repo pairs",
			q: query.NewAnd(
				&query.BranchesRepos{List: []query.BranchRepos{
					{Branch: querySurfaceNewFeature, Repos: roaring.BitmapOf(querySurfaceRepoID)},
					{Branch: querySurfaceNewRelease, Repos: roaring.BitmapOf(querySurfaceRepoID)},
				}},
				&query.Substring{Pattern: "query-surface-final-common", Content: true},
			),
			wantFiles: []string{"new_release_surface.go", "renamed_surface.go"},
		},
		{
			name:      "file-name query after branch rename add remove",
			q:         &query.Substring{Pattern: "surface.go", FileName: true},
			wantFiles: []string{"main_surface.go", "new_release_surface.go", "renamed_surface.go"},
		},
		{
			name:      "regex content query after branch rename add remove",
			q:         querySurfaceRegexpContentQuery(t, `regex-surface-(renamed|added)-2026`),
			wantFiles: []string{"new_release_surface.go", "renamed_surface.go"},
		},
		{
			name:      "case-sensitive content query after branch rename add remove",
			q:         &query.Substring{Pattern: "CaseSurfaceExact", Content: true, CaseSensitive: true},
			wantFiles: []string{"new_release_surface.go", "renamed_surface.go"},
		},
		{
			name:      "case-sensitive content query rejects lowercase",
			q:         &query.Substring{Pattern: "casesurfaceexact", Content: true, CaseSensitive: true},
			wantFiles: nil,
		},
	}

	for _, surface := range querySurfaces {
		t.Run(surface.name, func(t *testing.T) {
			querySurfaceAssertQueryMatchesClean(t, indexDir, cleanIndexDir, surface.q, surface.wantFiles)
		})
	}
}

type querySurfaceSearchHit struct {
	FileName string
	Content  string
	Branches []string
	Version  string
}

func querySurfaceInitRepo(t *testing.T) string {
	t.Helper()

	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-b", querySurfaceMain)
	querySurfaceWriteAndCommitFile(t, repoDir, "main_surface.go", "package main\n\nconst mainSurface = \"query-surface-main-stable\"\n", "main surface")

	runGit(t, repoDir, "checkout", "-b", querySurfaceOldFeature)
	querySurfaceWriteAndCommitFile(t, repoDir, "feature_surface.go", "package main\n\nconst featureSurface = \"query-surface-feature-old\"\nconst regexSurface = \"regex-surface-old-feature-2026\"\n", "feature surface")

	runGit(t, repoDir, "checkout", querySurfaceMain)
	runGit(t, repoDir, "checkout", "-b", querySurfaceOldRelease)
	querySurfaceWriteAndCommitFile(t, repoDir, "old_release_surface.go", "package main\n\nconst oldReleaseSurface = \"query-surface-old-release-only\"\nconst caseSurface = \"CaseSurfaceExact\"\n", "old release surface")

	runGit(t, repoDir, "checkout", querySurfaceMain)
	return repoDir
}

func querySurfaceMutateRenameAddRemove(t *testing.T, repoDir string) {
	t.Helper()

	runGit(t, repoDir, "checkout", querySurfaceOldFeature)
	runGit(t, repoDir, "branch", "-m", querySurfaceNewFeature)
	if err := os.Remove(filepath.Join(repoDir, "feature_surface.go")); err != nil {
		t.Fatalf("Remove feature_surface.go: %v", err)
	}
	querySurfaceWriteAndCommitFile(t, repoDir, "renamed_surface.go", strings.Join([]string{
		"package main",
		"",
		"const renamedSurface = \"query-surface-renamed-only\"",
		"const finalCommonSurface = \"query-surface-final-common\"",
		"const regexSurface = \"regex-surface-renamed-2026\"",
		"const caseSurface = \"CaseSurfaceExact\"",
		"",
	}, "\n"), "rename feature surface")

	runGit(t, repoDir, "checkout", querySurfaceMain)
	runGit(t, repoDir, "checkout", "-b", querySurfaceNewRelease)
	querySurfaceWriteAndCommitFile(t, repoDir, "new_release_surface.go", strings.Join([]string{
		"package main",
		"",
		"const newReleaseSurface = \"query-surface-new-release-only\"",
		"const finalCommonSurface = \"query-surface-final-common\"",
		"const regexSurface = \"regex-surface-added-2026\"",
		"const caseSurface = \"CaseSurfaceExact\"",
		"",
	}, "\n"), "new release surface")

	runGit(t, repoDir, "checkout", querySurfaceMain)
	runGit(t, repoDir, "branch", "-D", querySurfaceOldRelease)
}

func querySurfaceWriteAndCommitFile(t *testing.T, repoDir, name, content, message string) {
	t.Helper()

	file := filepath.Join(repoDir, name)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(file), err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", file, err)
	}
	runGit(t, repoDir, "add", "-A")
	runGit(t, repoDir, "commit", "-m", message)
}

func querySurfaceOptions(repoDir, indexDir string, branches []string) Options {
	return Options{
		RepoDir:  filepath.Join(repoDir, ".git"),
		Branches: append([]string(nil), branches...),
		BuildOptions: index.Options{
			IndexDir: indexDir,
			RepositoryDescription: zoekt.Repository{
				Name: querySurfaceRepoName,
				ID:   querySurfaceRepoID,
			},
			DisableCTags: true,
		},
	}
}

func querySurfaceIndexGitRepoWithPrepareSpies(t *testing.T, opts Options) (deltaBuildCalled, normalBuildCalled bool) {
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
		t.Fatalf("IndexGitRepo: %v", err)
	}

	return deltaBuildCalled, normalBuildCalled
}

func querySurfaceAssertRepositoryBranchesMatchClean(t *testing.T, deltaIndexDir, cleanIndexDir string, wantBranches []string) {
	t.Helper()

	deltaRepo := querySurfaceIndexedRepository(t, deltaIndexDir)
	cleanRepo := querySurfaceIndexedRepository(t, cleanIndexDir)

	if got := querySurfaceBranchNames(deltaRepo.Branches); !cmp.Equal(got, wantBranches) {
		t.Fatalf("delta branch names mismatch (-want +got):\n%s", cmp.Diff(wantBranches, got))
	}
	if diff := cmp.Diff(cleanRepo.Branches, deltaRepo.Branches); diff != "" {
		t.Fatalf("delta branch metadata differs from clean rebuild (-clean +delta):\n%s", diff)
	}
}

func querySurfaceAssertQueryMatchesClean(t *testing.T, deltaIndexDir, cleanIndexDir string, q query.Q, wantFiles []string) {
	t.Helper()

	deltaHits := querySurfaceSearchHits(t, deltaIndexDir, q)
	cleanHits := querySurfaceSearchHits(t, cleanIndexDir, q)
	if diff := cmp.Diff(cleanHits, deltaHits); diff != "" {
		t.Fatalf("%s search differs from clean full rebuild (-clean +delta):\n%s", q.String(), diff)
	}

	gotFiles := querySurfaceSearchHitFileNames(deltaHits)
	if wantFiles == nil {
		wantFiles = []string{}
	}
	sort.Strings(wantFiles)
	if diff := cmp.Diff(wantFiles, gotFiles); diff != "" {
		t.Fatalf("%s file names mismatch (-want +got):\n%s", q.String(), diff)
	}
}

func querySurfaceSearchHits(t *testing.T, indexDir string, q query.Q) []querySurfaceSearchHit {
	t.Helper()

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%q): %v", indexDir, err)
	}
	defer searcher.Close()

	result, err := searcher.Search(context.Background(), q, &zoekt.SearchOptions{Whole: true})
	if err != nil {
		t.Fatalf("Search(%s): %v", q.String(), err)
	}

	hits := make([]querySurfaceSearchHit, 0, len(result.Files))
	for _, file := range result.Files {
		branches := append([]string(nil), file.Branches...)
		sort.Strings(branches)
		hits = append(hits, querySurfaceSearchHit{
			FileName: file.FileName,
			Content:  string(file.Content),
			Branches: branches,
			Version:  file.Version,
		})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].FileName != hits[j].FileName {
			return hits[i].FileName < hits[j].FileName
		}
		if hits[i].Content != hits[j].Content {
			return hits[i].Content < hits[j].Content
		}
		if hits[i].Version != hits[j].Version {
			return hits[i].Version < hits[j].Version
		}
		return strings.Join(hits[i].Branches, "\x00") < strings.Join(hits[j].Branches, "\x00")
	})
	return hits
}

func querySurfaceSearchHitFileNames(hits []querySurfaceSearchHit) []string {
	names := make([]string, 0, len(hits))
	for _, hit := range hits {
		names = append(names, hit.FileName)
	}
	sort.Strings(names)
	return names
}

func querySurfaceIndexedRepository(t *testing.T, indexDir string) *zoekt.Repository {
	t.Helper()

	opts := index.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			Name: querySurfaceRepoName,
			ID:   querySurfaceRepoID,
		},
	}
	repo, _, ok, err := opts.FindRepositoryMetadata()
	if err != nil {
		t.Fatalf("FindRepositoryMetadata: %v", err)
	}
	if !ok {
		t.Fatalf("FindRepositoryMetadata: repository %q not found", querySurfaceRepoName)
	}
	return repo
}

func querySurfaceBranchNames(branches []zoekt.RepositoryBranch) []string {
	names := make([]string, 0, len(branches))
	for _, branch := range branches {
		names = append(names, branch.Name)
	}
	return names
}

func querySurfaceParseQuery(t *testing.T, raw string) query.Q {
	t.Helper()

	q, err := query.Parse(raw)
	if err != nil {
		t.Fatalf("query.Parse(%q): %v", raw, err)
	}
	return q
}

func querySurfaceRegexpContentQuery(t *testing.T, pattern string) query.Q {
	t.Helper()

	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		t.Fatalf("syntax.Parse(%q): %v", pattern, err)
	}
	return &query.Regexp{Regexp: re, Content: true}
}
