package gitindex

import (
	"context"
	"encoding/json"
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
	"sort"
	"strings"
	"testing"
)

// ---- delta_multibranch_ambiguity_test.go ----

const (
	ambiguityRepoName = "ambiguity-repository"
	ambiguityRepoID   = 7733
)

type ambiguityTree map[string]string

type ambiguitySearchHit struct {
	FileName string
	Content  string
	Branches []string
	Version  string
}

func TestIndexGitRepo_DeltaMultiBranchAmbiguityFallsBack(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name            string
		initialBranches []string
		initialTrees    map[string]ambiguityTree
		mutate          func(t *testing.T, repoDir string)
		finalBranches   []string
		cleanBranches   []string
		compareBranches []string
		patterns        []string
		requireFallback bool
	}{
		{
			name:            "ambiguous rename without provenance",
			initialBranches: []string{"foo", "bar"},
			initialTrees: map[string]ambiguityTree{
				"foo": {"foo.txt": "foo-original-needle\n"},
				"bar": {"bar.txt": "bar-stable-needle\n"},
			},
			mutate: func(t *testing.T, repoDir string) {
				runGit(t, repoDir, "branch", "baz", "foo")
				runGit(t, repoDir, "checkout", "bar")
				runGit(t, repoDir, "branch", "-D", "foo")
			},
			finalBranches:   []string{"baz", "bar"},
			compareBranches: []string{"foo", "baz", "bar"},
			patterns:        []string{"foo-original-needle", "bar-stable-needle"},
			requireFallback: true,
		},
		{
			name:            "many-to-one branch rename",
			initialBranches: []string{"a", "b"},
			initialTrees: map[string]ambiguityTree{
				"a": {"a.txt": "a-original-needle\n"},
				"b": {"b.txt": "b-original-needle\n"},
			},
			mutate: func(t *testing.T, repoDir string) {
				runGit(t, repoDir, "branch", "c", "a")
				runGit(t, repoDir, "checkout", "c")
				runGit(t, repoDir, "branch", "-D", "a")
				runGit(t, repoDir, "branch", "-D", "b")
			},
			finalBranches:   []string{"c"},
			compareBranches: []string{"a", "b", "c"},
			patterns:        []string{"a-original-needle", "b-original-needle"},
			requireFallback: true,
		},
		{
			name:            "one-to-many branch split",
			initialBranches: []string{"a"},
			initialTrees: map[string]ambiguityTree{
				"a": {
					"shared.txt": "split-shared-needle\n",
					"c.txt":      "split-c-original-needle\n",
				},
			},
			mutate: func(t *testing.T, repoDir string) {
				runGit(t, repoDir, "branch", "b", "a")
				runGit(t, repoDir, "checkout", "-B", "c", "a")
				ambiguityWriteCommit(t, repoDir, map[string]string{
					"c.txt": "split-c-final-needle\n",
				}, nil, "modify c branch")
				runGit(t, repoDir, "branch", "-D", "a")
			},
			finalBranches:   []string{"b", "c"},
			compareBranches: []string{"a", "b", "c"},
			patterns:        []string{"split-shared-needle", "split-c-original-needle", "split-c-final-needle"},
			requireFallback: true,
		},
		{
			name:            "duplicate final branch names after wildcard expansion",
			initialBranches: []string{"main"},
			initialTrees: map[string]ambiguityTree{
				"main": {"main.txt": "duplicate-main-needle\n"},
			},
			mutate:          func(t *testing.T, repoDir string) {},
			finalBranches:   []string{"main", "m*"},
			cleanBranches:   []string{"main"},
			compareBranches: []string{"main"},
			patterns:        []string{"duplicate-main-needle"},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repoDir := ambiguityInitRepository(t, tc.initialBranches, tc.initialTrees)
			indexDir := t.TempDir()

			initialOpts := ambiguityOptions(repoDir, indexDir, tc.initialBranches)
			if _, err := IndexGitRepo(initialOpts); err != nil {
				t.Fatalf("initial IndexGitRepo: %v", err)
			}

			tc.mutate(t, repoDir)

			deltaOpts := ambiguityOptions(repoDir, indexDir, tc.finalBranches)
			deltaOpts.BuildOptions.IsDelta = true
			deltaBuildCalled, normalBuildCalled, err := ambiguityIndexWithSpies(t, deltaOpts)
			if err != nil {
				t.Fatalf("delta IndexGitRepo: %v", err)
			}
			if !deltaBuildCalled {
				t.Fatal("expected delta build to be attempted before selecting the safe fallback")
			}
			if tc.requireFallback && !normalBuildCalled {
				t.Fatalf("expected ambiguous branch mapping to fall back to a normal rebuild instead of accepting an arbitrary delta mapping")
			}

			cleanBranches := tc.cleanBranches
			if cleanBranches == nil {
				cleanBranches = tc.finalBranches
			}
			cleanIndexDir := t.TempDir()
			cleanOpts := ambiguityOptions(repoDir, cleanIndexDir, cleanBranches)
			if _, err := IndexGitRepo(cleanOpts); err != nil {
				t.Fatalf("clean IndexGitRepo: %v", err)
			}

			ambiguityAssertIndexMatchesClean(t, indexDir, cleanIndexDir, tc.compareBranches, tc.patterns)
			ambiguityAssertNoDuplicateBranches(t, indexDir)
		})
	}
}

func TestIndexGitRepo_DeltaBranchRemovedAndReAddedSameNameUnrelatedHistory(t *testing.T) {
	t.Parallel()

	repoDir := ambiguityInitRepository(t, []string{"release"}, map[string]ambiguityTree{
		"release": {"release.txt": "release-old-lineage-needle\n"},
	})
	indexDir := t.TempDir()

	initialOpts := ambiguityOptions(repoDir, indexDir, []string{"release"})
	if _, err := IndexGitRepo(initialOpts); err != nil {
		t.Fatalf("initial IndexGitRepo: %v", err)
	}

	runGit(t, repoDir, "checkout", "--orphan", "replacement")
	ambiguityRemoveAllTracked(t, repoDir)
	ambiguityWriteCommit(t, repoDir, map[string]string{
		"release.txt": "release-new-lineage-needle\n",
	}, nil, "replacement release")
	runGit(t, repoDir, "branch", "-D", "release")
	runGit(t, repoDir, "branch", "-m", "release")

	deltaOpts := ambiguityOptions(repoDir, indexDir, []string{"release"})
	deltaOpts.BuildOptions.IsDelta = true
	deltaBuildCalled, _, err := ambiguityIndexWithSpies(t, deltaOpts)
	if err != nil {
		t.Fatalf("delta IndexGitRepo: %v", err)
	}
	if !deltaBuildCalled {
		t.Fatal("expected delta build to be attempted for same-name unrelated history")
	}

	cleanIndexDir := t.TempDir()
	cleanOpts := ambiguityOptions(repoDir, cleanIndexDir, []string{"release"})
	if _, err := IndexGitRepo(cleanOpts); err != nil {
		t.Fatalf("clean IndexGitRepo: %v", err)
	}

	ambiguityAssertIndexMatchesClean(t, indexDir, cleanIndexDir, []string{"release"}, []string{
		"release-old-lineage-needle",
		"release-new-lineage-needle",
	})
}

func TestIndexGitRepo_DeltaOldCommitMissingFallsBack(t *testing.T) {
	t.Parallel()

	repoDir := ambiguityInitRepository(t, []string{"release"}, map[string]ambiguityTree{
		"release": {"release.txt": "old-missing-commit-needle\n"},
	})
	indexDir := t.TempDir()

	initialOpts := ambiguityOptions(repoDir, indexDir, []string{"release"})
	if _, err := IndexGitRepo(initialOpts); err != nil {
		t.Fatalf("initial IndexGitRepo: %v", err)
	}

	if err := os.RemoveAll(repoDir); err != nil {
		t.Fatalf("RemoveAll(%q): %v", repoDir, err)
	}
	runGit(t, repoDir, "init", "-b", "release")
	ambiguityWriteCommit(t, repoDir, map[string]string{
		"release.txt": "new-repository-needle\n",
	}, nil, "new unrelated repository")

	deltaOpts := ambiguityOptions(repoDir, indexDir, []string{"release"})
	deltaOpts.BuildOptions.IsDelta = true
	deltaBuildCalled, normalBuildCalled, err := ambiguityIndexWithSpies(t, deltaOpts)
	if err != nil {
		t.Fatalf("delta IndexGitRepo: %v", err)
	}
	if !deltaBuildCalled {
		t.Fatal("expected delta build to be attempted before detecting the missing old commit")
	}
	if !normalBuildCalled {
		t.Fatal("expected missing old commit to fall back to a normal rebuild")
	}

	cleanIndexDir := t.TempDir()
	cleanOpts := ambiguityOptions(repoDir, cleanIndexDir, []string{"release"})
	if _, err := IndexGitRepo(cleanOpts); err != nil {
		t.Fatalf("clean IndexGitRepo: %v", err)
	}

	ambiguityAssertIndexMatchesClean(t, indexDir, cleanIndexDir, []string{"release"}, []string{
		"old-missing-commit-needle",
		"new-repository-needle",
	})
}

func TestIndexGitRepo_DeltaNewCommitMissingErrorsWithoutPartialIndex(t *testing.T) {
	t.Parallel()

	repoDir := ambiguityInitRepository(t, []string{"main"}, map[string]ambiguityTree{
		"main": {"main.txt": "new-missing-main-needle\n"},
	})
	indexDir := t.TempDir()

	initialOpts := ambiguityOptions(repoDir, indexDir, []string{"main"})
	if _, err := IndexGitRepo(initialOpts); err != nil {
		t.Fatalf("initial IndexGitRepo: %v", err)
	}

	before := ambiguityIndexedRepository(t, indexDir)

	deltaOpts := ambiguityOptions(repoDir, indexDir, []string{"main", "missing"})
	deltaOpts.BuildOptions.IsDelta = true
	deltaBuildCalled, normalBuildCalled, err := ambiguityIndexWithSpies(t, deltaOpts)
	if err == nil {
		t.Fatal("expected missing new branch commit to return an explicit error")
	}
	if deltaBuildCalled || normalBuildCalled {
		t.Fatalf("missing new branch should fail before any build preparation, got delta=%v normal=%v", deltaBuildCalled, normalBuildCalled)
	}

	after := ambiguityIndexedRepository(t, indexDir)
	if diff := cmp.Diff(before.Branches, after.Branches); diff != "" {
		t.Fatalf("missing new branch changed repository metadata despite failing (-before +after):\n%s", diff)
	}
	if got := ambiguitySearch(t, indexDir, &query.Substring{Pattern: "new-missing-main-needle"}); len(got) != 1 {
		t.Fatalf("existing index should remain searchable after failed missing-branch update, got %+v", got)
	}
}

func TestIndexGitRepo_DeltaCompoundShardBeforeBranchSetChangeFallsBack(t *testing.T) {
	t.Parallel()

	repoDir := ambiguityInitRepository(t, []string{"main", "release"}, map[string]ambiguityTree{
		"main":    {"main.txt": "compound-main-needle\n"},
		"release": {"release.txt": "compound-release-needle\n"},
	})
	indexDir := t.TempDir()

	initialOpts := ambiguityOptions(repoDir, indexDir, []string{"main", "release"})
	if _, err := IndexGitRepo(initialOpts); err != nil {
		t.Fatalf("initial IndexGitRepo: %v", err)
	}
	ambiguityMergeIndexIntoCompoundShard(t, indexDir)

	runGit(t, repoDir, "branch", "stable", "release")
	runGit(t, repoDir, "branch", "-D", "release")

	deltaOpts := ambiguityOptions(repoDir, indexDir, []string{"main", "stable"})
	deltaOpts.BuildOptions.IsDelta = true
	deltaBuildCalled, normalBuildCalled, err := ambiguityIndexWithSpies(t, deltaOpts)
	if err != nil {
		t.Fatalf("delta IndexGitRepo: %v", err)
	}
	if !deltaBuildCalled {
		t.Fatal("expected delta build to be attempted before rejecting the compound shard")
	}
	if !normalBuildCalled {
		t.Fatal("expected compound shard before branch-set change to fall back to a normal rebuild")
	}

	cleanIndexDir := t.TempDir()
	cleanOpts := ambiguityOptions(repoDir, cleanIndexDir, []string{"main", "stable"})
	if _, err := IndexGitRepo(cleanOpts); err != nil {
		t.Fatalf("clean IndexGitRepo: %v", err)
	}

	ambiguityAssertIndexMatchesClean(t, indexDir, cleanIndexDir, []string{"main", "release", "stable"}, []string{
		"compound-main-needle",
		"compound-release-needle",
	})
}

func ambiguityOptions(repoDir, indexDir string, branches []string) Options {
	return Options{
		RepoDir:            filepath.Join(repoDir, ".git"),
		Branches:           append([]string(nil), branches...),
		DeltaAdmissionMode: DeltaAdmissionModeStatsV1,
		DeltaAdmissionThresholds: DeltaAdmissionThresholds{
			MaxDeltaIndexedBytesRatio: 100,
			MaxPhysicalLiveBytesRatio: 100,
			MaxTombstonePathRatio:     100,
			MaxShardFanoutRatio:       100,
		},
		BuildOptions: index.Options{
			RepositoryDescription: zoekt.Repository{
				ID:   ambiguityRepoID,
				Name: ambiguityRepoName,
			},
			IndexDir:     indexDir,
			DisableCTags: true,
		},
	}
}

func ambiguityIndexWithSpies(t *testing.T, opts Options) (deltaBuildCalled, normalBuildCalled bool, err error) {
	t.Helper()

	prepareDeltaSpy := func(options Options, repository *git.Repository) (repos map[fileKey]BlobLocation, branchVersions map[string]map[string]plumbing.Hash, changedOrDeletedPaths []string, err error) {
		deltaBuildCalled = true
		return prepareDeltaBuild(options, repository)
	}
	prepareNormalSpy := func(options Options, repository *git.Repository) (repos map[fileKey]BlobLocation, branchVersions map[string]map[string]plumbing.Hash, err error) {
		normalBuildCalled = true
		return prepareNormalBuild(options, repository)
	}

	_, err = indexGitRepo(opts, gitIndexConfig{
		prepareDeltaBuild:  prepareDeltaSpy,
		prepareNormalBuild: prepareNormalSpy,
	})
	return deltaBuildCalled, normalBuildCalled, err
}

func ambiguityInitRepository(t *testing.T, branches []string, trees map[string]ambiguityTree) string {
	t.Helper()

	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-b", "main")
	runGit(t, repoDir, "commit", "--allow-empty", "-m", "base")
	base := ambiguityGitOutput(t, repoDir, "rev-parse", "HEAD")

	for _, branch := range branches {
		runGit(t, repoDir, "checkout", "-B", branch, base)
		ambiguityRemoveAllTracked(t, repoDir)
		ambiguityWriteCommit(t, repoDir, map[string]string(trees[branch]), nil, branch+" initial")
	}
	runGit(t, repoDir, "checkout", branches[0])
	return repoDir
}

func ambiguityWriteCommit(t *testing.T, repoDir string, files map[string]string, deletes []string, message string) {
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
	for _, name := range deletes {
		path := filepath.Join(repoDir, name)
		if err := os.Remove(path); err != nil {
			t.Fatalf("Remove(%q): %v", path, err)
		}
	}
	runGit(t, repoDir, "add", "-A")
	runGit(t, repoDir, "commit", "--allow-empty", "-m", message)
}

func ambiguityRemoveAllTracked(t *testing.T, repoDir string) {
	t.Helper()
	runGit(t, repoDir, "rm", "-r", "-f", "--ignore-unmatch", ".")
}

func ambiguityIndexedRepository(t *testing.T, indexDir string) *zoekt.Repository {
	t.Helper()

	opts := index.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			ID:   ambiguityRepoID,
			Name: ambiguityRepoName,
		},
	}
	repo, _, ok, err := opts.FindRepositoryMetadata()
	if err != nil {
		t.Fatalf("FindRepositoryMetadata(%q): %v", indexDir, err)
	}
	if !ok {
		t.Fatalf("FindRepositoryMetadata(%q): repository not found", indexDir)
	}
	return repo
}

func ambiguityAssertIndexMatchesClean(t *testing.T, indexDir, cleanIndexDir string, branches, patterns []string) {
	t.Helper()

	gotRepo := ambiguityIndexedRepository(t, indexDir)
	wantRepo := ambiguityIndexedRepository(t, cleanIndexDir)
	if diff := cmp.Diff(wantRepo.Branches, gotRepo.Branches); diff != "" {
		t.Fatalf("repository branches mismatch against clean rebuild (-want +got):\n%s", diff)
	}

	ambiguityAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "unfiltered/all", &query.Const{Value: true})
	for _, pattern := range patterns {
		ambiguityAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "unfiltered/"+pattern, &query.Substring{Pattern: pattern})
	}

	for _, branch := range branches {
		ambiguityAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "branch/"+branch+"/all", &query.Branch{Pattern: branch, Exact: true})
		ambiguityAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "branchesrepos/"+branch+"/all", query.NewSingleBranchesRepos(branch, ambiguityRepoID))
		for _, pattern := range patterns {
			substr := &query.Substring{Pattern: pattern}
			ambiguityAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "branch/"+branch+"/"+pattern, query.NewAnd(&query.Branch{Pattern: branch, Exact: true}, substr))
			ambiguityAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "branchesrepos/"+branch+"/"+pattern, query.NewAnd(query.NewSingleBranchesRepos(branch, ambiguityRepoID), substr))
		}
	}
}

func ambiguityAssertQueryMatchesClean(t *testing.T, indexDir, cleanIndexDir, label string, q query.Q) {
	t.Helper()

	got := ambiguitySearch(t, indexDir, q)
	want := ambiguitySearch(t, cleanIndexDir, q)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("%s mismatch against clean rebuild (-want +got):\n%s", label, diff)
	}
}

func ambiguitySearch(t *testing.T, indexDir string, q query.Q) []ambiguitySearchHit {
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
		t.Fatalf("Search(%s): %v", q.String(), err)
	}

	hits := make([]ambiguitySearchHit, 0, len(result.Files))
	for _, file := range result.Files {
		branches := append([]string(nil), file.Branches...)
		sort.Strings(branches)
		hits = append(hits, ambiguitySearchHit{
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

func ambiguityAssertNoDuplicateBranches(t *testing.T, indexDir string) {
	t.Helper()

	repo := ambiguityIndexedRepository(t, indexDir)
	seen := map[string]struct{}{}
	for _, branch := range repo.Branches {
		if _, ok := seen[branch.Name]; ok {
			t.Fatalf("repository metadata contains duplicate branch %q: %+v", branch.Name, repo.Branches)
		}
		seen[branch.Name] = struct{}{}
	}

	for _, hit := range ambiguitySearch(t, indexDir, &query.Const{Value: true}) {
		seen := map[string]struct{}{}
		for _, branch := range hit.Branches {
			if _, ok := seen[branch]; ok {
				t.Fatalf("search hit %q contains duplicate branch %q: %+v", hit.FileName, branch, hit.Branches)
			}
			seen[branch] = struct{}{}
		}
	}
}

func ambiguityMergeIndexIntoCompoundShard(t *testing.T, indexDir string) {
	t.Helper()

	opts := index.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			ID:   ambiguityRepoID,
			Name: ambiguityRepoName,
		},
	}
	shards := opts.FindAllShards()
	if len(shards) == 0 {
		t.Fatal("expected at least one simple shard before merging")
	}

	files := make([]index.IndexFile, 0, len(shards))
	for _, shard := range shards {
		file, err := os.Open(shard)
		if err != nil {
			t.Fatalf("Open(%q): %v", shard, err)
		}
		indexFile, err := index.NewIndexFile(file)
		if err != nil {
			_ = file.Close()
			t.Fatalf("NewIndexFile(%q): %v", shard, err)
		}
		files = append(files, indexFile)
	}

	tmpName, dstName, err := index.Merge(indexDir, files...)
	for _, file := range files {
		file.Close()
	}
	if err != nil {
		t.Fatalf("Merge(%q): %v", indexDir, err)
	}
	if err := os.Rename(tmpName, dstName); err != nil {
		t.Fatalf("Rename(%q, %q): %v", tmpName, dstName, err)
	}

	for _, shard := range shards {
		paths, err := index.IndexFilePaths(shard)
		if err != nil {
			t.Fatalf("IndexFilePaths(%q): %v", shard, err)
		}
		for _, path := range paths {
			if err := os.Remove(path); err != nil {
				t.Fatalf("Remove(%q): %v", path, err)
			}
		}
	}

	compoundShards, err := filepath.Glob(filepath.Join(indexDir, "compound-*.zoekt"))
	if err != nil {
		t.Fatalf("Glob compound shards: %v", err)
	}
	if len(compoundShards) != 1 {
		t.Fatalf("got compound shards %v, want exactly one", compoundShards)
	}
}

func ambiguityGitOutput(t *testing.T, cwd string, args ...string) string {
	t.Helper()

	out, err := ambiguityGitCombinedOutput(cwd, args...)
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func ambiguityGitCombinedOutput(cwd string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=",
		"GIT_CONFIG_SYSTEM=",
		"GIT_COMMITTER_NAME=Kierkegaard",
		"GIT_COMMITTER_EMAIL=soren@apache.com",
		"GIT_AUTHOR_NAME=Kierkegaard",
		"GIT_AUTHOR_EMAIL=soren@apache.com",
	)
	return cmd.CombinedOutput()
}

// ---- delta_multibranch_stats_test.go ----

const (
	statsBranchRepoID   = 9127
	statsBranchRepoName = "stats-branch-repository"
)

type statsBranchFiles map[string]string

type statsBranchSearchHit struct {
	FileName string
	Content  string
	Branches []string
}

func TestIndexGitRepo_DeltaMultiBranchStatsAfterBranchRename(t *testing.T) {
	t.Parallel()

	repoDir := statsBranchCreateRepository(t, []string{"feature-a", "release"}, map[string]statsBranchFiles{
		"feature-a": {
			"branch.txt": "stats-rename-old-feature\n",
		},
		"release": {
			"release.txt": "stats-rename-release\n",
		},
	})
	indexDir := t.TempDir()

	statsBranchRunIndex(t, repoDir, indexDir, []string{"feature-a", "release"}, false, "")
	oldShards := statsBranchFindAllShards(t, indexDir)

	statsBranchRenameBranch(t, repoDir, "feature-a", "feature-b")
	statsBranchCheckoutWriteCommit(t, repoDir, "feature-b", statsBranchFiles{
		"branch.txt": "stats-rename-new-feature\n",
	}, "modify renamed branch")

	deltaCalled, normalCalled := statsBranchRunIndex(t, repoDir, indexDir, []string{"feature-b", "release"}, true, "")
	if !deltaCalled {
		t.Error("expected branch rename to attempt a delta build")
	}
	if normalCalled {
		t.Error("expected branch rename to stay on the delta path without a normal-build fallback")
	}

	cleanIndexDir := t.TempDir()
	statsBranchRunIndex(t, repoDir, cleanIndexDir, []string{"feature-b", "release"}, false, "")

	statsBranchAssertRepositoryBranchesMatchClean(t, indexDir, cleanIndexDir, []string{"feature-b", "release"})
	statsBranchAssertLiveStatsMatchClean(t, indexDir, cleanIndexDir)
	statsBranchAssertDeltaDebt(t, indexDir, 1, []string{"branch.txt"})
	statsBranchAssertOldShardTombstones(t, oldShards, []string{"branch.txt"})

	statsBranchAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "new feature content", &query.Substring{Pattern: "stats-rename-new-feature"})
	statsBranchAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "release content", &query.Substring{Pattern: "stats-rename-release"})
	statsBranchAssertFileNames(t, indexDir, "old branch has no branch-query results", statsBranchBranchQuery("feature-a", &query.Const{Value: true}), nil)
	statsBranchAssertFileNames(t, indexDir, "old branch has no BranchesRepos results", statsBranchBranchesReposQuery("feature-a", &query.Const{Value: true}), nil)
	statsBranchAssertFileNames(t, indexDir, "renamed branch sees new file", statsBranchBranchQuery("feature-b", &query.Substring{Pattern: "stats-rename-new-feature"}), []string{"branch.txt"})
	statsBranchAssertFileNames(t, indexDir, "renamed branch sees new file through BranchesRepos", statsBranchBranchesReposQuery("feature-b", &query.Substring{Pattern: "stats-rename-new-feature"}), []string{"branch.txt"})
}

func TestIndexGitRepo_DeltaMultiBranchStatsAfterAddIdenticalBranch(t *testing.T) {
	t.Parallel()

	repoDir := statsBranchCreateRepository(t, []string{"main"}, map[string]statsBranchFiles{
		"main": {
			"one.txt": "stats-add-identical-one\n",
			"two.txt": "stats-add-identical-two\n",
		},
	})
	indexDir := t.TempDir()

	statsBranchRunIndex(t, repoDir, indexDir, []string{"main"}, false, "")
	initialRepo := statsBranchIndexedRepository(t, indexDir)
	if initialRepo.DeltaStats == nil {
		t.Fatal("initial full build DeltaStats is nil")
	}
	initialLiveDocumentCount := initialRepo.DeltaStats.LiveDocumentCount
	oldShards := statsBranchFindAllShards(t, indexDir)

	runGit(t, repoDir, "branch", "release", "main")

	deltaCalled, normalCalled := statsBranchRunIndex(t, repoDir, indexDir, []string{"main", "release"}, true, "")
	if !deltaCalled {
		t.Error("expected identical branch addition to attempt a delta build")
	}
	if normalCalled {
		t.Error("expected identical branch addition to stay on the delta path without a normal-build fallback")
	}

	cleanIndexDir := t.TempDir()
	statsBranchRunIndex(t, repoDir, cleanIndexDir, []string{"main", "release"}, false, "")

	statsBranchAssertRepositoryBranchesMatchClean(t, indexDir, cleanIndexDir, []string{"main", "release"})
	statsBranchAssertLiveStatsMatchClean(t, indexDir, cleanIndexDir)
	repo := statsBranchIndexedRepository(t, indexDir)
	if got := repo.DeltaStats.LiveDocumentCount; got != initialLiveDocumentCount {
		t.Fatalf("identical branch add LiveDocumentCount = %d, want unchanged from initial %d", got, initialLiveDocumentCount)
	}
	statsBranchAssertDeltaDebt(t, indexDir, 1, []string{"one.txt", "two.txt"})
	statsBranchAssertOldShardTombstones(t, oldShards, []string{"one.txt", "two.txt"})

	statsBranchAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "all files", &query.Const{Value: true})
	statsBranchAssertFileNames(t, indexDir, "main all files", statsBranchBranchQuery("main", &query.Const{Value: true}), []string{"one.txt", "two.txt"})
	statsBranchAssertFileNames(t, indexDir, "release all files", statsBranchBranchQuery("release", &query.Const{Value: true}), []string{"one.txt", "two.txt"})
	statsBranchAssertFileNames(t, indexDir, "release all files through BranchesRepos", statsBranchBranchesReposQuery("release", &query.Const{Value: true}), []string{"one.txt", "two.txt"})
}

func TestIndexGitRepo_DeltaMultiBranchStatsAfterRemoveBranchOnlyFiles(t *testing.T) {
	t.Parallel()

	repoDir := statsBranchCreateRepository(t, []string{"main", "release"}, map[string]statsBranchFiles{
		"main": {
			"main.txt": "stats-remove-main\n",
		},
		"release": {
			"release-only.txt": "stats-remove-release-only\n",
		},
	})
	indexDir := t.TempDir()

	statsBranchRunIndex(t, repoDir, indexDir, []string{"main", "release"}, false, "")
	oldShards := statsBranchFindAllShards(t, indexDir)

	runGit(t, repoDir, "checkout", "main")
	runGit(t, repoDir, "branch", "-D", "release")

	deltaCalled, normalCalled := statsBranchRunIndex(t, repoDir, indexDir, []string{"main"}, true, "")
	if !deltaCalled {
		t.Error("expected branch removal to attempt a delta build")
	}
	if normalCalled {
		t.Error("expected branch removal to stay on the delta path without a normal-build fallback")
	}

	cleanIndexDir := t.TempDir()
	statsBranchRunIndex(t, repoDir, cleanIndexDir, []string{"main"}, false, "")

	statsBranchAssertRepositoryBranchesMatchClean(t, indexDir, cleanIndexDir, []string{"main"})
	statsBranchAssertLiveStatsMatchClean(t, indexDir, cleanIndexDir)
	statsBranchAssertDeltaDebt(t, indexDir, 1, []string{"release-only.txt"})
	statsBranchAssertOldShardTombstones(t, oldShards, []string{"release-only.txt"})

	statsBranchAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "all files", &query.Const{Value: true})
	statsBranchAssertFileNames(t, indexDir, "release-only content absent unfiltered", &query.Substring{Pattern: "stats-remove-release-only"}, nil)
	statsBranchAssertFileNames(t, indexDir, "removed branch has no branch-query results", statsBranchBranchQuery("release", &query.Const{Value: true}), nil)
	statsBranchAssertFileNames(t, indexDir, "removed branch has no BranchesRepos results", statsBranchBranchesReposQuery("release", &query.Const{Value: true}), nil)
	statsBranchAssertFileNames(t, indexDir, "main branch remains searchable", statsBranchBranchQuery("main", &query.Substring{Pattern: "stats-remove-main"}), []string{"main.txt"})
}

func TestIndexGitRepo_DeltaMultiBranchStatsAdmissionLogAcceptedBranchSetDelta(t *testing.T) {
	t.Parallel()

	repoDir := statsBranchCreateRepository(t, []string{"feature-a", "release"}, map[string]statsBranchFiles{
		"feature-a": {
			"branch.txt": "stats-log-feature\n",
		},
		"release": {
			"release.txt": "stats-log-release\n",
		},
	})
	indexDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "delta-admission.jsonl")

	statsBranchRunIndex(t, repoDir, indexDir, []string{"feature-a", "release"}, false, "")
	statsBranchRenameBranch(t, repoDir, "feature-a", "feature-b")

	deltaCalled, normalCalled := statsBranchRunIndex(t, repoDir, indexDir, []string{"feature-b", "release"}, true, logPath)
	if !deltaCalled {
		t.Error("expected branch-set update to attempt a delta build")
	}
	if normalCalled {
		t.Error("expected branch-set update to be accepted as a delta build")
	}

	entries := statsBranchReadAdmissionLogObjects(t, logPath)
	if len(entries) != 1 {
		t.Fatalf("got %d admission log entries, want 1", len(entries))
	}
	entry := entries[0]
	statsBranchAssertJSONBool(t, entry, "accepted", true)
	statsBranchAssertJSONString(t, entry, "reason", "accepted")
	statsBranchAssertJSONNumber(t, entry, "old_branch_count", 2)
	statsBranchAssertJSONNumber(t, entry, "new_branch_count", 2)
	statsBranchAssertJSONPresent(t, entry, "branch_mapping")
	statsBranchAssertJSONPresent(t, entry, "candidate_indexed_bytes")
	statsBranchAssertJSONPresent(t, entry, "candidate_document_count")
	statsBranchAssertJSONPresent(t, entry, "write_bytes_ratio")
	statsBranchAssertJSONPresent(t, entry, "physical_live_ratio")
}

func TestIndexGitRepo_DeltaMultiBranchStatsAdmissionLogAmbiguousMappingFallback(t *testing.T) {
	t.Parallel()

	repoDir := statsBranchCreateRepository(t, []string{"foo", "bar"}, map[string]statsBranchFiles{
		"foo": {
			"foo.txt": "stats-ambiguous-foo\n",
		},
		"bar": {
			"bar.txt": "stats-ambiguous-bar\n",
		},
	})
	indexDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "delta-admission.jsonl")

	statsBranchRunIndex(t, repoDir, indexDir, []string{"foo", "bar"}, false, "")
	runGit(t, repoDir, "checkout", "foo")
	runGit(t, repoDir, "branch", "-m", "baz")

	deltaCalled, normalCalled := statsBranchRunIndex(t, repoDir, indexDir, []string{"baz", "bar"}, true, logPath)
	if !deltaCalled {
		t.Error("expected ambiguous branch mapping to attempt a delta build")
	}
	if !normalCalled {
		t.Error("expected ambiguous branch mapping to fall back to a normal rebuild")
	}

	entries := statsBranchReadAdmissionLogObjects(t, logPath)
	if len(entries) != 1 {
		t.Fatalf("got %d admission log entries, want 1", len(entries))
	}
	entry := entries[0]
	statsBranchAssertJSONBool(t, entry, "accepted", false)
	statsBranchAssertJSONStringContains(t, entry, "reason", "ambiguous branch mapping")
	statsBranchAssertJSONNumber(t, entry, "old_branch_count", 2)
	statsBranchAssertJSONNumber(t, entry, "new_branch_count", 2)
	statsBranchAssertJSONPresent(t, entry, "branch_mapping")
}

func statsBranchRunIndex(t *testing.T, repoDir, indexDir string, branches []string, isDelta bool, logPath string) (deltaBuildCalled, normalBuildCalled bool) {
	t.Helper()

	opts := statsBranchOptions(repoDir, indexDir, branches)
	opts.BuildOptions.IsDelta = isDelta
	opts.DeltaAdmissionLogPath = logPath
	if !isDelta {
		if _, err := IndexGitRepo(opts); err != nil {
			t.Fatalf("IndexGitRepo: %v", err)
		}
		return false, false
	}

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
		t.Fatalf("delta IndexGitRepo: %v", err)
	}
	return deltaBuildCalled, normalBuildCalled
}

func statsBranchOptions(repoDir, indexDir string, branches []string) Options {
	return Options{
		RepoDir:            filepath.Join(repoDir, ".git"),
		Branches:           append([]string(nil), branches...),
		DeltaAdmissionMode: DeltaAdmissionModeStatsV1,
		DeltaAdmissionThresholds: DeltaAdmissionThresholds{
			MaxDeltaIndexedBytesRatio: 100,
			MaxPhysicalLiveBytesRatio: 100,
			MaxTombstonePathRatio:     100,
			MaxShardFanoutRatio:       100,
		},
		BuildOptions: index.Options{
			RepositoryDescription: zoekt.Repository{
				ID:   statsBranchRepoID,
				Name: statsBranchRepoName,
			},
			IndexDir:     indexDir,
			DisableCTags: true,
		},
	}
}

func statsBranchCreateRepository(t *testing.T, branches []string, trees map[string]statsBranchFiles) string {
	t.Helper()

	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-b", "seed")
	runGit(t, repoDir, "commit", "--allow-empty", "-m", "seed")
	for _, branch := range branches {
		runGit(t, repoDir, "checkout", "-B", branch, "seed")
		statsBranchWriteFiles(t, repoDir, trees[branch])
		runGit(t, repoDir, "add", "-A")
		runGit(t, repoDir, "commit", "--allow-empty", "-m", "initial "+branch)
	}
	runGit(t, repoDir, "checkout", branches[0])
	return repoDir
}

func statsBranchCheckoutWriteCommit(t *testing.T, repoDir, branch string, files statsBranchFiles, message string) {
	t.Helper()

	runGit(t, repoDir, "checkout", branch)
	statsBranchWriteFiles(t, repoDir, files)
	runGit(t, repoDir, "add", "-A")
	runGit(t, repoDir, "commit", "-m", message)
}

func statsBranchWriteFiles(t *testing.T, repoDir string, files statsBranchFiles) {
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

func statsBranchRenameBranch(t *testing.T, repoDir, oldBranch, newBranch string) {
	t.Helper()

	runGit(t, repoDir, "branch", "-m", oldBranch, newBranch)
}

func statsBranchIndexedRepository(t *testing.T, indexDir string) *zoekt.Repository {
	t.Helper()

	opts := index.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			ID:   statsBranchRepoID,
			Name: statsBranchRepoName,
		},
	}
	repo, _, ok, err := opts.FindRepositoryMetadata()
	if err != nil {
		t.Fatalf("FindRepositoryMetadata: %v", err)
	}
	if !ok {
		t.Fatalf("FindRepositoryMetadata: repository %q not found", statsBranchRepoName)
	}
	return repo
}

func statsBranchFindAllShards(t *testing.T, indexDir string) []string {
	t.Helper()

	opts := index.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			ID:   statsBranchRepoID,
			Name: statsBranchRepoName,
		},
	}
	shards := opts.FindAllShards()
	sort.Strings(shards)
	if len(shards) == 0 {
		t.Fatal("expected at least one shard")
	}
	return shards
}

func statsBranchAssertRepositoryBranchesMatchClean(t *testing.T, indexDir, cleanIndexDir string, wantBranches []string) {
	t.Helper()

	gotRepo := statsBranchIndexedRepository(t, indexDir)
	cleanRepo := statsBranchIndexedRepository(t, cleanIndexDir)
	if diff := cmp.Diff(wantBranches, statsBranchRepositoryBranchNames(gotRepo.Branches)); diff != "" {
		t.Fatalf("delta branch names mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(cleanRepo.Branches, gotRepo.Branches); diff != "" {
		t.Fatalf("delta branch metadata differs from clean rebuild (-clean +delta):\n%s", diff)
	}
}

func statsBranchAssertLiveStatsMatchClean(t *testing.T, indexDir, cleanIndexDir string) {
	t.Helper()

	got := statsBranchIndexedRepository(t, indexDir)
	want := statsBranchIndexedRepository(t, cleanIndexDir)
	if got.DeltaStats == nil {
		t.Fatal("delta index DeltaStats is nil")
	}
	if want.DeltaStats == nil {
		t.Fatal("clean index DeltaStats is nil")
	}

	gotLive := statsBranchLiveStats{
		LiveIndexedBytes:  got.DeltaStats.LiveIndexedBytes,
		LiveDocumentCount: got.DeltaStats.LiveDocumentCount,
		LivePathCount:     got.DeltaStats.LivePathCount,
	}
	wantLive := statsBranchLiveStats{
		LiveIndexedBytes:  want.DeltaStats.LiveIndexedBytes,
		LiveDocumentCount: want.DeltaStats.LiveDocumentCount,
		LivePathCount:     want.DeltaStats.LivePathCount,
	}
	if diff := cmp.Diff(wantLive, gotLive); diff != "" {
		t.Fatalf("delta live stats differ from clean full rebuild (-want +got):\n%s", diff)
	}
}

type statsBranchLiveStats struct {
	LiveIndexedBytes  uint64
	LiveDocumentCount uint64
	LivePathCount     uint64
}

func statsBranchAssertDeltaDebt(t *testing.T, indexDir string, wantLayers uint64, wantTombstones []string) {
	t.Helper()

	repo := statsBranchIndexedRepository(t, indexDir)
	if repo.DeltaStats == nil {
		t.Fatal("DeltaStats is nil")
	}
	if got := repo.DeltaStats.DeltaLayerCount; got != wantLayers {
		t.Fatalf("DeltaLayerCount = %d, want %d", got, wantLayers)
	}
	if got, want := repo.DeltaStats.TombstonePathCount, uint64(len(wantTombstones)); got < want {
		t.Fatalf("TombstonePathCount = %d, want at least %d", got, want)
	}
	if repo.DeltaStats.PhysicalDocumentCount <= repo.DeltaStats.LiveDocumentCount {
		t.Fatalf("expected physical document debt, got physical %d live %d", repo.DeltaStats.PhysicalDocumentCount, repo.DeltaStats.LiveDocumentCount)
	}
	if repo.DeltaStats.PhysicalIndexedBytes <= repo.DeltaStats.LiveIndexedBytes {
		t.Fatalf("expected physical byte debt, got physical %d live %d", repo.DeltaStats.PhysicalIndexedBytes, repo.DeltaStats.LiveIndexedBytes)
	}
}

func statsBranchAssertOldShardTombstones(t *testing.T, oldShards []string, wantPaths []string) {
	t.Helper()

	for _, shard := range oldShards {
		repositories, _, err := index.ReadMetadataPathAlive(shard)
		if err != nil {
			t.Fatalf("ReadMetadataPathAlive(%q): %v", shard, err)
		}
		var repo *zoekt.Repository
		for _, candidate := range repositories {
			if candidate.ID == statsBranchRepoID {
				repo = candidate
				break
			}
		}
		if repo == nil {
			t.Fatalf("old shard %q no longer has alive repo ID %d metadata", shard, statsBranchRepoID)
		}
		for _, path := range wantPaths {
			if _, ok := repo.FileTombstones[path]; !ok {
				t.Fatalf("old shard %q missing file tombstone %q in %+v", shard, path, repo.FileTombstones)
			}
		}
	}
}

func statsBranchAssertQueryMatchesClean(t *testing.T, indexDir, cleanIndexDir, label string, q query.Q) {
	t.Helper()

	got := statsBranchSearch(t, indexDir, q)
	want := statsBranchSearch(t, cleanIndexDir, q)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("%s differs from clean rebuild (-clean +delta):\n%s", label, diff)
	}
}

func statsBranchAssertFileNames(t *testing.T, indexDir, label string, q query.Q, want []string) {
	t.Helper()

	got := statsBranchFileNames(statsBranchSearch(t, indexDir, q))
	want = append([]string(nil), want...)
	if want == nil {
		want = []string{}
	}
	sort.Strings(want)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("%s file names mismatch (-want +got):\n%s", label, diff)
	}
}

func statsBranchSearch(t *testing.T, indexDir string, q query.Q) []statsBranchSearchHit {
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

	hits := make([]statsBranchSearchHit, 0, len(result.Files))
	for _, file := range result.Files {
		branches := append([]string(nil), file.Branches...)
		sort.Strings(branches)
		hits = append(hits, statsBranchSearchHit{
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

func statsBranchFileNames(hits []statsBranchSearchHit) []string {
	names := make([]string, 0, len(hits))
	for _, hit := range hits {
		names = append(names, hit.FileName)
	}
	sort.Strings(names)
	return names
}

func statsBranchBranchQuery(branch string, q query.Q) query.Q {
	return query.NewAnd(&query.Branch{Pattern: branch, Exact: true}, q)
}

func statsBranchBranchesReposQuery(branch string, q query.Q) query.Q {
	return query.NewAnd(query.NewSingleBranchesRepos(branch, statsBranchRepoID), q)
}

func statsBranchRepositoryBranchNames(branches []zoekt.RepositoryBranch) []string {
	names := make([]string, 0, len(branches))
	for _, branch := range branches {
		names = append(names, branch.Name)
	}
	return names
}

func statsBranchReadAdmissionLogObjects(t *testing.T, path string) []map[string]any {
	t.Helper()

	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(blob)), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("Unmarshal(%q): %v", line, err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func statsBranchAssertJSONPresent(t *testing.T, entry map[string]any, key string) {
	t.Helper()

	if _, ok := entry[key]; !ok {
		t.Fatalf("admission log missing key %q in %#v", key, entry)
	}
}

func statsBranchAssertJSONBool(t *testing.T, entry map[string]any, key string, want bool) {
	t.Helper()

	got, ok := entry[key].(bool)
	if !ok {
		t.Fatalf("admission log key %q = %#v, want bool", key, entry[key])
	}
	if got != want {
		t.Fatalf("admission log key %q = %v, want %v", key, got, want)
	}
}

func statsBranchAssertJSONString(t *testing.T, entry map[string]any, key, want string) {
	t.Helper()

	got, ok := entry[key].(string)
	if !ok {
		t.Fatalf("admission log key %q = %#v, want string", key, entry[key])
	}
	if got != want {
		t.Fatalf("admission log key %q = %q, want %q", key, got, want)
	}
}

func statsBranchAssertJSONStringContains(t *testing.T, entry map[string]any, key, want string) {
	t.Helper()

	got, ok := entry[key].(string)
	if !ok {
		t.Fatalf("admission log key %q = %#v, want string", key, entry[key])
	}
	if !strings.Contains(got, want) {
		t.Fatalf("admission log key %q = %q, want substring %q", key, got, want)
	}
}

func statsBranchAssertJSONNumber(t *testing.T, entry map[string]any, key string, want float64) {
	t.Helper()

	got, ok := entry[key].(float64)
	if !ok {
		t.Fatalf("admission log key %q = %#v, want number", key, entry[key])
	}
	if got != want {
		t.Fatalf("admission log key %q = %v, want %v", key, got, want)
	}
}
