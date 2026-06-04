package gitindex

import (
	"context"
	"fmt"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/go-cmp/cmp"
	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/search"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ---- delta_multibranch_baseline_test.go ----

func TestIndexGitRepo_MultiBranchDeltaBaselineExistingBehaviorControls(t *testing.T) {
	t.Parallel()

	const (
		repoName = "repository"
		repoID   = 707
	)

	branches := []string{"main", "release"}

	for _, tc := range []struct {
		name           string
		initialMain    map[string]string
		initialRelease map[string]string
		finalMain      map[string]string
		tombstones     []string
		checks         []baselineSearchCheck
	}{
		{
			name: "changed path on one branch",
			initialMain: map[string]string{
				"shared.txt": "baseline-shared-old\n",
			},
			initialRelease: map[string]string{
				"shared.txt": "baseline-shared-old\n",
			},
			finalMain: map[string]string{
				"shared.txt": "baseline-main-new\n",
			},
			tombstones: []string{"shared.txt"},
			checks: []baselineSearchCheck{
				{pattern: "baseline-main-new", wantFiles: []string{"shared.txt"}},
				{branch: "main", pattern: "baseline-main-new", wantFiles: []string{"shared.txt"}},
				{branch: "release", pattern: "baseline-main-new"},
				{pattern: "baseline-shared-old", wantFiles: []string{"shared.txt"}},
				{branch: "main", pattern: "baseline-shared-old"},
				{branch: "release", pattern: "baseline-shared-old", wantFiles: []string{"shared.txt"}},
			},
		},
		{
			name: "deletion on one branch while another keeps the path",
			initialMain: map[string]string{
				"shared.txt": "baseline-delete-kept\n",
			},
			initialRelease: map[string]string{
				"shared.txt": "baseline-delete-kept\n",
			},
			finalMain:  map[string]string{},
			tombstones: []string{"shared.txt"},
			checks: []baselineSearchCheck{
				{pattern: "baseline-delete-kept", wantFiles: []string{"shared.txt"}},
				{branch: "main", pattern: "baseline-delete-kept"},
				{branch: "release", pattern: "baseline-delete-kept", wantFiles: []string{"shared.txt"}},
			},
		},
		{
			name: "file becomes identical across branches",
			initialMain: map[string]string{
				"a.txt": "baseline-identical-alpha\n",
			},
			initialRelease: map[string]string{
				"a.txt": "baseline-identical-beta\n",
			},
			finalMain: map[string]string{
				"a.txt": "baseline-identical-beta\n",
			},
			tombstones: []string{"a.txt"},
			checks: []baselineSearchCheck{
				{pattern: "baseline-identical-beta", wantFiles: []string{"a.txt"}},
				{branch: "main", pattern: "baseline-identical-beta", wantFiles: []string{"a.txt"}},
				{branch: "release", pattern: "baseline-identical-beta", wantFiles: []string{"a.txt"}},
				{pattern: "baseline-identical-alpha"},
				{branch: "main", pattern: "baseline-identical-alpha"},
				{branch: "release", pattern: "baseline-identical-alpha"},
			},
		},
		{
			name: "file diverges from shared content",
			initialMain: map[string]string{
				"a.txt": "baseline-shared-same\n",
			},
			initialRelease: map[string]string{
				"a.txt": "baseline-shared-same\n",
			},
			finalMain: map[string]string{
				"a.txt": "baseline-diverged-main\n",
			},
			tombstones: []string{"a.txt"},
			checks: []baselineSearchCheck{
				{pattern: "baseline-diverged-main", wantFiles: []string{"a.txt"}},
				{branch: "main", pattern: "baseline-diverged-main", wantFiles: []string{"a.txt"}},
				{branch: "release", pattern: "baseline-diverged-main"},
				{pattern: "baseline-shared-same", wantFiles: []string{"a.txt"}},
				{branch: "main", pattern: "baseline-shared-same"},
				{branch: "release", pattern: "baseline-shared-same", wantFiles: []string{"a.txt"}},
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repositoryDir := t.TempDir()
			baselineInitTwoBranchRepo(t, repositoryDir, tc.initialMain, tc.initialRelease)

			indexDir := t.TempDir()
			opts := baselineIndexOptions(repositoryDir, indexDir, repoName, repoID, branches)
			if _, err := IndexGitRepo(opts); err != nil {
				t.Fatalf("initial IndexGitRepo: %v", err)
			}

			oldShards := baselineFindAllShards(t, opts.BuildOptions)
			baselineReplaceBranchFilesAndCommit(t, repositoryDir, "main", tc.finalMain, "final main")

			deltaOpts := opts
			deltaOpts.BuildOptions.IsDelta = true
			deltaBuildCalled, normalBuildCalled := baselineIndexGitRepoWithPrepareSpies(t, deltaOpts)
			if !deltaBuildCalled {
				t.Fatal("expected delta build to be attempted")
			}
			if normalBuildCalled {
				t.Fatal("expected exact same multi-branch set to use delta without normal-build fallback")
			}

			cleanIndexDir := baselineCleanFullRebuild(t, opts)
			baselineAssertRepositoryBranchesMatchClean(t, indexDir, cleanIndexDir, repoName, repoID, branches)
			baselineAssertOldShardTombstones(t, oldShards, repoID, tc.tombstones)

			for _, check := range tc.checks {
				baselineAssertSearchCheck(t, indexDir, cleanIndexDir, repoName, repoID, check)
			}
		})
	}
}

type baselineSearchCheck struct {
	branch    string
	pattern   string
	wantFiles []string
}

type baselineSearchHit struct {
	FileName string
	Content  string
	Branches []string
}

func baselineInitTwoBranchRepo(t *testing.T, repositoryDir string, mainFiles, releaseFiles map[string]string) {
	t.Helper()

	runGit(t, repositoryDir, "init", "-b", "main")
	baselineReplaceCurrentBranchFilesAndCommit(t, repositoryDir, mainFiles, "initial main")
	runGit(t, repositoryDir, "checkout", "-b", "release")
	baselineReplaceCurrentBranchFilesAndCommit(t, repositoryDir, releaseFiles, "initial release")
	runGit(t, repositoryDir, "checkout", "main")
}

func baselineReplaceBranchFilesAndCommit(t *testing.T, repositoryDir, branch string, files map[string]string, message string) {
	t.Helper()

	runGit(t, repositoryDir, "checkout", branch)
	baselineReplaceCurrentBranchFilesAndCommit(t, repositoryDir, files, message)
}

func baselineReplaceCurrentBranchFilesAndCommit(t *testing.T, repositoryDir string, files map[string]string, message string) {
	t.Helper()

	runGit(t, repositoryDir, "rm", "-r", "--ignore-unmatch", ".")
	for name, content := range files {
		path := filepath.Join(repositoryDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}
	runGit(t, repositoryDir, "add", "-A")
	runGit(t, repositoryDir, "commit", "--allow-empty", "-m", message)
}

func baselineIndexOptions(repositoryDir, indexDir, repoName string, repoID uint32, branches []string) Options {
	return Options{
		RepoDir:  filepath.Join(repositoryDir, ".git"),
		Branches: append([]string(nil), branches...),
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

func baselineIndexGitRepoWithPrepareSpies(t *testing.T, opts Options) (deltaBuildCalled, normalBuildCalled bool) {
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

func baselineCleanFullRebuild(t *testing.T, opts Options) string {
	t.Helper()

	cleanIndexDir := t.TempDir()
	cleanOpts := opts
	cleanOpts.BuildOptions.IndexDir = cleanIndexDir
	cleanOpts.BuildOptions.IsDelta = false
	cleanOpts.BuildOptions.RepositoryDescription.DeltaStats = nil
	if _, err := IndexGitRepo(cleanOpts); err != nil {
		t.Fatalf("clean IndexGitRepo: %v", err)
	}
	return cleanIndexDir
}

func baselineAssertRepositoryBranchesMatchClean(t *testing.T, deltaIndexDir, cleanIndexDir, repoName string, repoID uint32, wantBranches []string) {
	t.Helper()

	deltaRepo := baselineIndexedRepository(t, deltaIndexDir, repoName, repoID)
	cleanRepo := baselineIndexedRepository(t, cleanIndexDir, repoName, repoID)

	if got := baselineRepositoryBranchNames(deltaRepo.Branches); !cmp.Equal(got, wantBranches) {
		t.Fatalf("delta branch names mismatch (-want +got):\n%s", cmp.Diff(wantBranches, got))
	}
	if diff := cmp.Diff(cleanRepo.Branches, deltaRepo.Branches); diff != "" {
		t.Fatalf("delta branch metadata differs from clean rebuild (-clean +delta):\n%s", diff)
	}
}

func baselineAssertOldShardTombstones(t *testing.T, oldShards []string, repoID uint32, wantPaths []string) {
	t.Helper()

	for _, shard := range oldShards {
		repositories, _, err := index.ReadMetadataPathAlive(shard)
		if err != nil {
			t.Fatalf("ReadMetadataPathAlive(%q): %v", shard, err)
		}

		var repo *zoekt.Repository
		for _, candidate := range repositories {
			if candidate.ID == repoID {
				repo = candidate
				break
			}
		}
		if repo == nil {
			t.Fatalf("old shard %q no longer has alive repo ID %d metadata", shard, repoID)
		}

		for _, path := range wantPaths {
			if _, ok := repo.FileTombstones[path]; !ok {
				t.Fatalf("old shard %q missing file tombstone %q in %+v", shard, path, repo.FileTombstones)
			}
		}
	}
}

func baselineAssertSearchCheck(t *testing.T, deltaIndexDir, cleanIndexDir, repoName string, repoID uint32, check baselineSearchCheck) {
	t.Helper()

	deltaRepo := baselineIndexedRepository(t, deltaIndexDir, repoName, repoID)
	cleanRepo := baselineIndexedRepository(t, cleanIndexDir, repoName, repoID)

	if check.branch == "" {
		deltaHits := baselineSearchHits(t, deltaIndexDir, &query.Substring{Pattern: check.pattern})
		cleanHits := baselineSearchHits(t, cleanIndexDir, &query.Substring{Pattern: check.pattern})
		baselineAssertHits(t, "unfiltered "+check.pattern, deltaHits, cleanHits, check.wantFiles)
		return
	}

	parsedBranchQuery := baselineParseBranchQuery(t, check.branch, check.pattern)
	deltaHits := baselineSearchHits(t, deltaIndexDir, parsedBranchQuery)
	cleanHits := baselineSearchHits(t, cleanIndexDir, parsedBranchQuery)
	baselineAssertHits(t, fmt.Sprintf("branch:%s %s", check.branch, check.pattern), deltaHits, cleanHits, check.wantFiles)

	deltaBranchesReposQuery := query.NewAnd(
		query.NewSingleBranchesRepos(check.branch, deltaRepo.ID),
		&query.Substring{Pattern: check.pattern},
	)
	cleanBranchesReposQuery := query.NewAnd(
		query.NewSingleBranchesRepos(check.branch, cleanRepo.ID),
		&query.Substring{Pattern: check.pattern},
	)
	deltaHits = baselineSearchHits(t, deltaIndexDir, deltaBranchesReposQuery)
	cleanHits = baselineSearchHits(t, cleanIndexDir, cleanBranchesReposQuery)
	baselineAssertHits(t, fmt.Sprintf("BranchesRepos(%s) %s", check.branch, check.pattern), deltaHits, cleanHits, check.wantFiles)
}

func baselineParseBranchQuery(t *testing.T, branch, pattern string) query.Q {
	t.Helper()

	q, err := query.Parse(fmt.Sprintf("branch:%s %s", branch, pattern))
	if err != nil {
		t.Fatalf("query.Parse(branch:%s %s): %v", branch, pattern, err)
	}
	return q
}

func baselineAssertHits(t *testing.T, label string, deltaHits, cleanHits []baselineSearchHit, wantFiles []string) {
	t.Helper()

	if diff := cmp.Diff(cleanHits, deltaHits); diff != "" {
		t.Fatalf("%s search differs from clean full rebuild (-clean +delta):\n%s", label, diff)
	}
	wantFiles = append([]string(nil), wantFiles...)
	if wantFiles == nil {
		wantFiles = []string{}
	}
	if got := baselineSearchHitFileNames(deltaHits); !cmp.Equal(got, wantFiles) {
		t.Fatalf("%s file names mismatch (-want +got):\n%s", label, cmp.Diff(wantFiles, got))
	}
}

func baselineSearchHits(t *testing.T, indexDir string, q query.Q) []baselineSearchHit {
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

	hits := make([]baselineSearchHit, 0, len(result.Files))
	for _, file := range result.Files {
		branches := append([]string(nil), file.Branches...)
		sort.Strings(branches)
		hits = append(hits, baselineSearchHit{
			FileName: file.FileName,
			Content:  string(file.Content),
			Branches: branches,
		})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].FileName != hits[j].FileName {
			return hits[i].FileName < hits[j].FileName
		}
		if hits[i].Content != hits[j].Content {
			return hits[i].Content < hits[j].Content
		}
		return strings.Join(hits[i].Branches, "\x00") < strings.Join(hits[j].Branches, "\x00")
	})
	return hits
}

func baselineSearchHitFileNames(hits []baselineSearchHit) []string {
	names := make([]string, 0, len(hits))
	for _, hit := range hits {
		names = append(names, hit.FileName)
	}
	sort.Strings(names)
	return names
}

func baselineFindAllShards(t *testing.T, opts index.Options) []string {
	t.Helper()

	shards := opts.FindAllShards()
	sort.Strings(shards)
	if len(shards) == 0 {
		t.Fatal("expected initial full build to write at least one shard")
	}
	return shards
}

func baselineIndexedRepository(t *testing.T, indexDir, repoName string, repoID uint32) *zoekt.Repository {
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
		t.Fatalf("FindRepositoryMetadata(%q): not found", repoName)
	}
	return repo
}

func baselineRepositoryBranchNames(branches []zoekt.RepositoryBranch) []string {
	names := make([]string, 0, len(branches))
	for _, branch := range branches {
		names = append(names, branch.Name)
	}
	return names
}
