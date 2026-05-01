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
	"sort"
	"strings"
	"testing"
)

// ---- delta_multibranch_single_rename_test.go ----

const (
	singleRenameRepoName = "single-rename-repository"
	singleRenameRepoID   = 4242

	singleRenameOldBranch = "feature-a"
	singleRenameNewBranch = "feature-b"
	singleRenameRelease   = "release"
)

func TestIndexGitRepo_DeltaMultiBranchSingleRename(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name              string
		mutateAfterRename func(t *testing.T, repoDir string)
		expectations      []singleRenameExpectation
		compareQueries    []singleRenameNamedQuery
	}{
		{
			name: "same commit no content changes",
			expectations: []singleRenameExpectation{
				{
					name:   "renamed branch keeps existing file",
					branch: singleRenameNewBranch,
					query:  singleRenameContentQuery("feature-a-initial-needle"),
					want:   []string{"branch.txt"},
				},
				{
					name:   "unchanged release branch keeps release file",
					branch: singleRenameRelease,
					query:  singleRenameContentQuery("release-initial-needle"),
					want:   []string{"release.txt"},
				},
			},
			compareQueries: []singleRenameNamedQuery{
				{name: "feature content", q: singleRenameContentQuery("feature-a-initial-needle")},
				{name: "release content", q: singleRenameContentQuery("release-initial-needle")},
				{name: "branch path", q: singleRenameFileNameQuery("branch.txt")},
				{name: "release path", q: singleRenameFileNameQuery("release.txt")},
			},
		},
		{
			name: "modified path on renamed branch",
			mutateAfterRename: func(t *testing.T, repoDir string) {
				singleRenameWriteAndCommitFile(t, repoDir, "branch.txt", "feature-b-modified-needle\n", "modify renamed branch")
			},
			expectations: []singleRenameExpectation{
				{
					name:   "renamed branch sees modified content",
					branch: singleRenameNewBranch,
					query:  singleRenameContentQuery("feature-b-modified-needle"),
					want:   []string{"branch.txt"},
				},
				{
					name:   "renamed branch no longer sees old content",
					branch: singleRenameNewBranch,
					query:  singleRenameContentQuery("feature-a-initial-needle"),
					want:   nil,
				},
				{
					name:   "unchanged release branch keeps release file",
					branch: singleRenameRelease,
					query:  singleRenameContentQuery("release-initial-needle"),
					want:   []string{"release.txt"},
				},
			},
			compareQueries: []singleRenameNamedQuery{
				{name: "old feature content", q: singleRenameContentQuery("feature-a-initial-needle")},
				{name: "new feature content", q: singleRenameContentQuery("feature-b-modified-needle")},
				{name: "release content", q: singleRenameContentQuery("release-initial-needle")},
				{name: "branch path", q: singleRenameFileNameQuery("branch.txt")},
			},
		},
		{
			name: "deleted path on renamed branch",
			mutateAfterRename: func(t *testing.T, repoDir string) {
				if err := os.Remove(filepath.Join(repoDir, "branch.txt")); err != nil {
					t.Fatalf("Remove branch.txt: %v", err)
				}
				runGit(t, repoDir, "add", "-A")
				runGit(t, repoDir, "commit", "-m", "delete renamed branch path")
			},
			expectations: []singleRenameExpectation{
				{
					name:   "renamed branch no longer has deleted path",
					branch: singleRenameNewBranch,
					query:  singleRenameFileNameQuery("branch.txt"),
					want:   nil,
				},
				{
					name:   "old feature content is absent",
					branch: singleRenameNewBranch,
					query:  singleRenameContentQuery("feature-a-initial-needle"),
					want:   nil,
				},
				{
					name:   "unchanged release branch keeps release file",
					branch: singleRenameRelease,
					query:  singleRenameContentQuery("release-initial-needle"),
					want:   []string{"release.txt"},
				},
			},
			compareQueries: []singleRenameNamedQuery{
				{name: "old feature content", q: singleRenameContentQuery("feature-a-initial-needle")},
				{name: "release content", q: singleRenameContentQuery("release-initial-needle")},
				{name: "branch path", q: singleRenameFileNameQuery("branch.txt")},
				{name: "release path", q: singleRenameFileNameQuery("release.txt")},
			},
		},
		{
			name: "added path on renamed branch",
			mutateAfterRename: func(t *testing.T, repoDir string) {
				singleRenameWriteAndCommitFile(t, repoDir, "new.txt", "feature-b-added-needle\n", "add renamed branch path")
			},
			expectations: []singleRenameExpectation{
				{
					name:   "renamed branch keeps existing file",
					branch: singleRenameNewBranch,
					query:  singleRenameContentQuery("feature-a-initial-needle"),
					want:   []string{"branch.txt"},
				},
				{
					name:   "renamed branch sees added file",
					branch: singleRenameNewBranch,
					query:  singleRenameContentQuery("feature-b-added-needle"),
					want:   []string{"new.txt"},
				},
				{
					name:   "unchanged release branch keeps release file",
					branch: singleRenameRelease,
					query:  singleRenameContentQuery("release-initial-needle"),
					want:   []string{"release.txt"},
				},
			},
			compareQueries: []singleRenameNamedQuery{
				{name: "feature content", q: singleRenameContentQuery("feature-a-initial-needle")},
				{name: "added feature content", q: singleRenameContentQuery("feature-b-added-needle")},
				{name: "release content", q: singleRenameContentQuery("release-initial-needle")},
				{name: "branch path", q: singleRenameFileNameQuery("branch.txt")},
				{name: "added path", q: singleRenameFileNameQuery("new.txt")},
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repoDir := singleRenameInitRepo(t)
			indexDir := t.TempDir()

			initialOpts := singleRenameOptions(repoDir, indexDir, []string{singleRenameOldBranch, singleRenameRelease})
			if _, err := IndexGitRepo(initialOpts); err != nil {
				t.Fatalf("initial IndexGitRepo: %v", err)
			}

			singleRenameRenameBranch(t, repoDir)
			if tc.mutateAfterRename != nil {
				tc.mutateAfterRename(t, repoDir)
			}

			deltaOpts := singleRenameOptions(repoDir, indexDir, []string{singleRenameNewBranch, singleRenameRelease})
			deltaOpts.BuildOptions.IsDelta = true
			deltaBuildCalled, normalBuildCalled := singleRenameIndexGitRepoWithPrepareSpies(t, deltaOpts)
			if !deltaBuildCalled {
				t.Error("expected delta build to be attempted")
			}
			if normalBuildCalled {
				t.Error("expected single branch rename to stay on the delta path, got normal rebuild fallback")
			}

			repo := singleRenameIndexedRepository(t, indexDir)
			singleRenameAssertBranchMetadata(t, repoDir, repo)
			singleRenameAssertOldBranchHasNoResults(t, indexDir)

			cleanIndexDir := t.TempDir()
			cleanOpts := singleRenameOptions(repoDir, cleanIndexDir, []string{singleRenameNewBranch, singleRenameRelease})
			if _, err := IndexGitRepo(cleanOpts); err != nil {
				t.Fatalf("clean IndexGitRepo: %v", err)
			}

			for _, expectation := range tc.expectations {
				singleRenameAssertExpectation(t, indexDir, expectation)
			}

			singleRenameCompareWithCleanRebuild(t, indexDir, cleanIndexDir, tc.compareQueries)
		})
	}
}

type singleRenameExpectation struct {
	name   string
	branch string
	query  query.Q
	want   []string
}

type singleRenameNamedQuery struct {
	name string
	q    query.Q
}

type singleRenameSearchHit struct {
	FileName string
	Content  string
	Branches []string
	Version  string
}

func singleRenameOptions(repoDir, indexDir string, branches []string) Options {
	return Options{
		RepoDir:                   filepath.Join(repoDir, ".git"),
		Branches:                  append([]string(nil), branches...),
		AllowDeltaBranchSetChange: true,
		BuildOptions: index.Options{
			IndexDir: indexDir,
			RepositoryDescription: zoekt.Repository{
				Name: singleRenameRepoName,
				ID:   singleRenameRepoID,
			},
			DisableCTags: true,
		},
	}
}

func singleRenameInitRepo(t *testing.T) string {
	t.Helper()

	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-b", "base")
	runGit(t, repoDir, "commit", "--allow-empty", "-m", "base")

	runGit(t, repoDir, "checkout", "-b", singleRenameRelease)
	singleRenameWriteAndCommitFile(t, repoDir, "release.txt", "release-initial-needle\n", "release content")

	runGit(t, repoDir, "checkout", "base")
	runGit(t, repoDir, "checkout", "-b", singleRenameOldBranch)
	singleRenameWriteAndCommitFile(t, repoDir, "branch.txt", "feature-a-initial-needle\n", "feature content")

	return repoDir
}

func singleRenameRenameBranch(t *testing.T, repoDir string) {
	t.Helper()

	runGit(t, repoDir, "checkout", singleRenameOldBranch)
	runGit(t, repoDir, "branch", "-m", singleRenameNewBranch)
}

func singleRenameWriteAndCommitFile(t *testing.T, repoDir, name, content, message string) {
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

func singleRenameIndexGitRepoWithPrepareSpies(t *testing.T, opts Options) (deltaBuildCalled, normalBuildCalled bool) {
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

func singleRenameIndexedRepository(t *testing.T, indexDir string) *zoekt.Repository {
	t.Helper()

	opts := index.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			Name: singleRenameRepoName,
			ID:   singleRenameRepoID,
		},
	}
	repo, _, ok, err := opts.FindRepositoryMetadata()
	if err != nil {
		t.Fatalf("FindRepositoryMetadata: %v", err)
	}
	if !ok {
		t.Fatalf("FindRepositoryMetadata: repository %q not found", singleRenameRepoName)
	}
	return repo
}

func singleRenameAssertBranchMetadata(t *testing.T, repoDir string, repo *zoekt.Repository) {
	t.Helper()

	wantBranches := []zoekt.RepositoryBranch{
		{Name: singleRenameNewBranch, Version: singleRenameRevParse(t, repoDir, singleRenameNewBranch)},
		{Name: singleRenameRelease, Version: singleRenameRevParse(t, repoDir, singleRenameRelease)},
	}
	if diff := cmp.Diff(wantBranches, repo.Branches); diff != "" {
		t.Fatalf("repository branches mismatch (-want +got):\n%s", diff)
	}
}

func singleRenameRevParse(t *testing.T, repoDir, rev string) string {
	t.Helper()

	hash, err := git.PlainOpen(repoDir)
	if err != nil {
		t.Fatalf("PlainOpen(%q): %v", repoDir, err)
	}
	ref, err := hash.ResolveRevision(plumbing.Revision(rev))
	if err != nil {
		t.Fatalf("ResolveRevision(%q): %v", rev, err)
	}
	return ref.String()
}

func singleRenameAssertOldBranchHasNoResults(t *testing.T, indexDir string) {
	t.Helper()

	allFiles := &query.Const{Value: true}
	singleRenameAssertSearchFileNames(t, indexDir, singleRenameBranchQuery(singleRenameOldBranch, allFiles), nil)
	singleRenameAssertSearchFileNames(t, indexDir, singleRenameBranchesReposQuery(singleRenameOldBranch, allFiles), nil)
}

func singleRenameAssertExpectation(t *testing.T, indexDir string, expectation singleRenameExpectation) {
	t.Helper()

	t.Run(expectation.name+"/branch", func(t *testing.T) {
		singleRenameAssertSearchFileNames(t, indexDir, singleRenameBranchQuery(expectation.branch, expectation.query), expectation.want)
	})
	t.Run(expectation.name+"/branches-repos", func(t *testing.T) {
		singleRenameAssertSearchFileNames(t, indexDir, singleRenameBranchesReposQuery(expectation.branch, expectation.query), expectation.want)
	})
}

func singleRenameCompareWithCleanRebuild(t *testing.T, deltaIndexDir, cleanIndexDir string, queries []singleRenameNamedQuery) {
	t.Helper()

	branches := []string{singleRenameOldBranch, singleRenameNewBranch, singleRenameRelease}
	for _, named := range queries {
		t.Run("compare/"+named.name+"/unfiltered", func(t *testing.T) {
			singleRenameAssertSameSearchHits(t, deltaIndexDir, cleanIndexDir, named.q)
		})
		for _, branch := range branches {
			branch := branch
			t.Run("compare/"+named.name+"/branch/"+branch, func(t *testing.T) {
				singleRenameAssertSameSearchHits(t, deltaIndexDir, cleanIndexDir, singleRenameBranchQuery(branch, named.q))
			})
			t.Run("compare/"+named.name+"/branches-repos/"+branch, func(t *testing.T) {
				singleRenameAssertSameSearchHits(t, deltaIndexDir, cleanIndexDir, singleRenameBranchesReposQuery(branch, named.q))
			})
		}
	}
}

func singleRenameAssertSameSearchHits(t *testing.T, deltaIndexDir, cleanIndexDir string, q query.Q) {
	t.Helper()

	deltaHits := singleRenameSearchHits(t, deltaIndexDir, q)
	cleanHits := singleRenameSearchHits(t, cleanIndexDir, q)
	if diff := cmp.Diff(cleanHits, deltaHits); diff != "" {
		t.Fatalf("delta search results differ from clean rebuild (-clean +delta):\n%s", diff)
	}
}

func singleRenameAssertSearchFileNames(t *testing.T, indexDir string, q query.Q, want []string) {
	t.Helper()

	hits := singleRenameSearchHits(t, indexDir, q)
	var got []string
	for _, hit := range hits {
		got = append(got, hit.FileName)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("search file names mismatch (-want +got):\n%s", diff)
	}
}

func singleRenameSearchHits(t *testing.T, indexDir string, q query.Q) []singleRenameSearchHit {
	t.Helper()

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%q): %v", indexDir, err)
	}
	defer searcher.Close()

	result, err := searcher.Search(context.Background(), q, &zoekt.SearchOptions{Whole: true})
	if err != nil {
		t.Fatalf("Search(%s): %v", q, err)
	}

	hits := make([]singleRenameSearchHit, 0, len(result.Files))
	for _, file := range result.Files {
		branches := append([]string(nil), file.Branches...)
		sort.Strings(branches)
		hits = append(hits, singleRenameSearchHit{
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

func singleRenameBranchQuery(branch string, q query.Q) query.Q {
	return query.NewAnd(&query.Branch{Pattern: branch}, q)
}

func singleRenameBranchesReposQuery(branch string, q query.Q) query.Q {
	return query.NewAnd(&query.BranchesRepos{List: []query.BranchRepos{{
		Branch: branch,
		Repos:  roaring.BitmapOf(singleRenameRepoID),
	}}}, q)
}

func singleRenameContentQuery(pattern string) query.Q {
	return &query.Substring{Pattern: pattern, Content: true}
}

func singleRenameFileNameQuery(pattern string) query.Q {
	return &query.Substring{Pattern: pattern, FileName: true}
}

// ---- delta_multibranch_multi_rename_test.go ----

func TestIndexGitRepo_DeltaMultipleBranchRenamesNoFileChanges(t *testing.T) {
	t.Parallel()

	repositoryDir := multiRenameInitRepository(t, map[string]map[string]string{
		"feature-a": {
			"feature.txt": "feature-a-carryover-needle\n",
		},
		"qa-a": {
			"qa.txt": "qa-a-carryover-needle\n",
		},
		"release": {
			"release.txt": "release-stable-needle\n",
		},
	})

	multiRenameRunCase(t, multiRenameCase{
		repositoryDir:   repositoryDir,
		initialBranches: []string{"feature-a", "qa-a", "release"},
		finalBranches:   []string{"feature-b", "qa-b", "release"},
		mutate: func(t *testing.T, repositoryDir string) {
			multiRenameBranch(t, repositoryDir, "feature-a", "feature-b")
			multiRenameBranch(t, repositoryDir, "qa-a", "qa-b")
		},
		checks: []multiRenameCheck{
			{
				pattern:    "feature-a-carryover-needle",
				unfiltered: []string{"feature.txt"},
				branches: map[string][]string{
					"feature-a": nil,
					"feature-b": {"feature.txt"},
					"qa-a":      nil,
					"qa-b":      nil,
					"release":   nil,
				},
			},
			{
				pattern:    "qa-a-carryover-needle",
				unfiltered: []string{"qa.txt"},
				branches: map[string][]string{
					"feature-a": nil,
					"feature-b": nil,
					"qa-a":      nil,
					"qa-b":      {"qa.txt"},
					"release":   nil,
				},
			},
			{
				pattern:    "release-stable-needle",
				unfiltered: []string{"release.txt"},
				branches: map[string][]string{
					"feature-a": nil,
					"feature-b": nil,
					"qa-a":      nil,
					"qa-b":      nil,
					"release":   {"release.txt"},
				},
			},
		},
	})
}

func TestIndexGitRepo_DeltaMultipleBranchRenamesIndependentContentChanges(t *testing.T) {
	t.Parallel()

	repositoryDir := multiRenameInitRepository(t, map[string]map[string]string{
		"feature-a": {
			"feature.txt": "feature-a-old-needle\n",
		},
		"qa-a": {
			"qa.txt": "qa-a-old-needle\n",
		},
		"release": {
			"release.txt": "release-stable-needle\n",
		},
	})

	multiRenameRunCase(t, multiRenameCase{
		repositoryDir:      repositoryDir,
		initialBranches:    []string{"feature-a", "qa-a", "release"},
		finalBranches:      []string{"feature-b", "qa-b", "release"},
		expectedTombstones: []string{"feature.txt", "qa.txt"},
		mutate: func(t *testing.T, repositoryDir string) {
			multiRenameBranch(t, repositoryDir, "feature-a", "feature-b")
			multiRenameBranch(t, repositoryDir, "qa-a", "qa-b")
			multiRenameCheckout(t, repositoryDir, "feature-b")
			multiRenameWriteAndCommit(t, repositoryDir, map[string]string{
				"feature.txt": "feature-b-new-needle\n",
			}, "update feature-b")
			multiRenameCheckout(t, repositoryDir, "qa-b")
			multiRenameWriteAndCommit(t, repositoryDir, map[string]string{
				"qa.txt": "qa-b-new-needle\n",
			}, "update qa-b")
		},
		checks: []multiRenameCheck{
			{
				pattern:    "feature-b-new-needle",
				unfiltered: []string{"feature.txt"},
				branches: map[string][]string{
					"feature-a": nil,
					"feature-b": {"feature.txt"},
					"qa-a":      nil,
					"qa-b":      nil,
					"release":   nil,
				},
			},
			{
				pattern:    "qa-b-new-needle",
				unfiltered: []string{"qa.txt"},
				branches: map[string][]string{
					"feature-a": nil,
					"feature-b": nil,
					"qa-a":      nil,
					"qa-b":      {"qa.txt"},
					"release":   nil,
				},
			},
			{
				pattern:    "feature-a-old-needle",
				unfiltered: nil,
				branches: map[string][]string{
					"feature-a": nil,
					"feature-b": nil,
					"qa-a":      nil,
					"qa-b":      nil,
					"release":   nil,
				},
			},
			{
				pattern:    "qa-a-old-needle",
				unfiltered: nil,
				branches: map[string][]string{
					"feature-a": nil,
					"feature-b": nil,
					"qa-a":      nil,
					"qa-b":      nil,
					"release":   nil,
				},
			},
			{
				pattern:    "release-stable-needle",
				unfiltered: []string{"release.txt"},
				branches: map[string][]string{
					"feature-a": nil,
					"feature-b": nil,
					"qa-a":      nil,
					"qa-b":      nil,
					"release":   {"release.txt"},
				},
			},
		},
	})
}

func TestIndexGitRepo_DeltaMultipleBranchRenamesConvergeToSharedBlob(t *testing.T) {
	t.Parallel()

	repositoryDir := multiRenameInitRepository(t, map[string]map[string]string{
		"feature-a": {
			"shared.txt": "feature-a-shared-old-needle\n",
		},
		"qa-a": {
			"shared.txt": "qa-a-shared-old-needle\n",
		},
		"release": {
			"release.txt": "release-stable-needle\n",
		},
	})

	multiRenameRunCase(t, multiRenameCase{
		repositoryDir:      repositoryDir,
		initialBranches:    []string{"feature-a", "qa-a", "release"},
		finalBranches:      []string{"feature-b", "qa-b", "release"},
		expectedTombstones: []string{"shared.txt"},
		mutate: func(t *testing.T, repositoryDir string) {
			multiRenameBranch(t, repositoryDir, "feature-a", "feature-b")
			multiRenameBranch(t, repositoryDir, "qa-a", "qa-b")
			multiRenameCheckout(t, repositoryDir, "feature-b")
			multiRenameWriteAndCommit(t, repositoryDir, map[string]string{
				"shared.txt": "renamed-branches-shared-blob-needle\n",
			}, "converge feature-b")
			multiRenameCheckout(t, repositoryDir, "qa-b")
			multiRenameWriteAndCommit(t, repositoryDir, map[string]string{
				"shared.txt": "renamed-branches-shared-blob-needle\n",
			}, "converge qa-b")
		},
		checks: []multiRenameCheck{
			{
				pattern:    "renamed-branches-shared-blob-needle",
				unfiltered: []string{"shared.txt"},
				branches: map[string][]string{
					"feature-a": nil,
					"feature-b": {"shared.txt"},
					"qa-a":      nil,
					"qa-b":      {"shared.txt"},
					"release":   nil,
				},
			},
			{
				pattern:    "feature-a-shared-old-needle",
				unfiltered: nil,
				branches: map[string][]string{
					"feature-a": nil,
					"feature-b": nil,
					"qa-a":      nil,
					"qa-b":      nil,
					"release":   nil,
				},
			},
			{
				pattern:    "qa-a-shared-old-needle",
				unfiltered: nil,
				branches: map[string][]string{
					"feature-a": nil,
					"feature-b": nil,
					"qa-a":      nil,
					"qa-b":      nil,
					"release":   nil,
				},
			},
			{
				pattern:    "release-stable-needle",
				unfiltered: []string{"release.txt"},
				branches: map[string][]string{
					"feature-a": nil,
					"feature-b": nil,
					"qa-a":      nil,
					"qa-b":      nil,
					"release":   {"release.txt"},
				},
			},
		},
	})
}

func TestIndexGitRepo_DeltaMultipleBranchRenamesAndBranchOrderChanges(t *testing.T) {
	t.Parallel()

	repositoryDir := multiRenameInitRepository(t, map[string]map[string]string{
		"feature-a": {
			"feature.txt": "feature-a-order-needle\n",
		},
		"qa-a": {
			"qa.txt": "qa-a-order-needle\n",
		},
		"release": {
			"release.txt": "release-order-needle\n",
		},
	})

	multiRenameRunCase(t, multiRenameCase{
		repositoryDir:   repositoryDir,
		initialBranches: []string{"feature-a", "release", "qa-a"},
		finalBranches:   []string{"qa-b", "feature-b", "release"},
		mutate: func(t *testing.T, repositoryDir string) {
			multiRenameBranch(t, repositoryDir, "feature-a", "feature-b")
			multiRenameBranch(t, repositoryDir, "qa-a", "qa-b")
		},
		checks: []multiRenameCheck{
			{
				pattern:    "feature-a-order-needle",
				unfiltered: []string{"feature.txt"},
				branches: map[string][]string{
					"feature-a": nil,
					"feature-b": {"feature.txt"},
					"qa-a":      nil,
					"qa-b":      nil,
					"release":   nil,
				},
			},
			{
				pattern:    "qa-a-order-needle",
				unfiltered: []string{"qa.txt"},
				branches: map[string][]string{
					"feature-a": nil,
					"feature-b": nil,
					"qa-a":      nil,
					"qa-b":      {"qa.txt"},
					"release":   nil,
				},
			},
			{
				pattern:    "release-order-needle",
				unfiltered: []string{"release.txt"},
				branches: map[string][]string{
					"feature-a": nil,
					"feature-b": nil,
					"qa-a":      nil,
					"qa-b":      nil,
					"release":   {"release.txt"},
				},
			},
		},
	})
}

type multiRenameCase struct {
	repositoryDir      string
	initialBranches    []string
	finalBranches      []string
	expectedTombstones []string
	mutate             func(t *testing.T, repositoryDir string)
	checks             []multiRenameCheck
}

type multiRenameCheck struct {
	pattern    string
	unfiltered []string
	branches   map[string][]string
}

type multiRenameSearchResult struct {
	FileName string
	Content  string
	Version  string
	Branches []string
}

func multiRenameRunCase(t *testing.T, tc multiRenameCase) {
	t.Helper()

	indexDir := t.TempDir()
	initialOpts := multiRenameIndexOptions(tc.repositoryDir, indexDir, tc.initialBranches)
	if _, err := IndexGitRepo(initialOpts); err != nil {
		t.Fatalf("initial IndexGitRepo: %v", err)
	}

	tc.mutate(t, tc.repositoryDir)

	deltaOpts := multiRenameIndexOptions(tc.repositoryDir, indexDir, tc.finalBranches)
	deltaOpts.BuildOptions.IsDelta = true
	deltaBuildCalled, normalBuildCalled := multiRenameIndexGitRepoWithPrepareSpies(t, deltaOpts)
	if !deltaBuildCalled {
		t.Error("expected delta build to be attempted")
	}
	if normalBuildCalled {
		t.Error("expected multiple branch renames to use delta, got normal build fallback")
	}

	cleanIndexDir := t.TempDir()
	cleanOpts := multiRenameIndexOptions(tc.repositoryDir, cleanIndexDir, tc.finalBranches)
	if _, err := IndexGitRepo(cleanOpts); err != nil {
		t.Fatalf("clean IndexGitRepo: %v", err)
	}

	multiRenameAssertRepositoryMetadataMatchesClean(t, indexDir, cleanIndexDir)
	multiRenameAssertSearchesMatchClean(t, indexDir, cleanIndexDir, tc.checks)
	if len(tc.expectedTombstones) > 0 {
		multiRenameAssertTombstones(t, indexDir, tc.expectedTombstones)
	}
}

func multiRenameInitRepository(t *testing.T, branchFiles map[string]map[string]string) string {
	t.Helper()

	repositoryDir := t.TempDir()
	runGit(t, repositoryDir, "init", "-b", "main")
	multiRenameWriteAndCommit(t, repositoryDir, map[string]string{
		"base.txt": "base-needle\n",
	}, "base")

	branchNames := make([]string, 0, len(branchFiles))
	for branch := range branchFiles {
		branchNames = append(branchNames, branch)
	}
	sort.Strings(branchNames)
	for _, branch := range branchNames {
		runGit(t, repositoryDir, "checkout", "main")
		runGit(t, repositoryDir, "checkout", "-b", branch)
		multiRenameWriteAndCommit(t, repositoryDir, branchFiles[branch], "add "+branch)
	}

	runGit(t, repositoryDir, "checkout", "main")
	return repositoryDir
}

func multiRenameIndexOptions(repositoryDir, indexDir string, branches []string) Options {
	return Options{
		RepoDir:                   filepath.Join(repositoryDir, ".git"),
		Branches:                  append([]string(nil), branches...),
		AllowDeltaBranchSetChange: true,
		BuildOptions: index.Options{
			RepositoryDescription: zoekt.Repository{Name: "repository"},
			IndexDir:              indexDir,
			DisableCTags:          true,
		},
	}
}

func multiRenameIndexGitRepoWithPrepareSpies(t *testing.T, opts Options) (deltaBuildCalled, normalBuildCalled bool) {
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

func multiRenameAssertRepositoryMetadataMatchesClean(t *testing.T, deltaIndexDir, cleanIndexDir string) {
	t.Helper()

	deltaRepo := indexedRepositoryForTest(t, deltaIndexDir, "repository")
	cleanRepo := indexedRepositoryForTest(t, cleanIndexDir, "repository")
	if diff := cmp.Diff(cleanRepo.Branches, deltaRepo.Branches); diff != "" {
		t.Errorf("repository branches mismatch against clean rebuild (-want +got):\n%s", diff)
	}
}

func multiRenameAssertSearchesMatchClean(t *testing.T, deltaIndexDir, cleanIndexDir string, checks []multiRenameCheck) {
	t.Helper()

	deltaRepo := indexedRepositoryForTest(t, deltaIndexDir, "repository")
	cleanRepo := indexedRepositoryForTest(t, cleanIndexDir, "repository")

	for _, check := range checks {
		t.Run("unfiltered/"+check.pattern, func(t *testing.T) {
			delta := multiRenameSearch(t, deltaIndexDir, multiRenameSubstringQuery(check.pattern))
			clean := multiRenameSearch(t, cleanIndexDir, multiRenameSubstringQuery(check.pattern))
			multiRenameAssertResultNames(t, "delta unfiltered", check.unfiltered, delta)
			multiRenameAssertResultNames(t, "clean unfiltered", check.unfiltered, clean)
			if diff := cmp.Diff(clean, delta); diff != "" {
				t.Errorf("unfiltered search mismatch against clean rebuild (-want +got):\n%s", diff)
			}
		})

		branchNames := make([]string, 0, len(check.branches))
		for branch := range check.branches {
			branchNames = append(branchNames, branch)
		}
		sort.Strings(branchNames)
		for _, branch := range branchNames {
			want := check.branches[branch]
			t.Run("branch/"+check.pattern+"/"+branch, func(t *testing.T) {
				q := query.NewAnd(&query.Branch{Pattern: branch, Exact: true}, multiRenameSubstringQuery(check.pattern))
				delta := multiRenameSearch(t, deltaIndexDir, q)
				clean := multiRenameSearch(t, cleanIndexDir, q)
				multiRenameAssertResultNames(t, "delta branch:"+branch, want, delta)
				multiRenameAssertResultNames(t, "clean branch:"+branch, want, clean)
				if diff := cmp.Diff(clean, delta); diff != "" {
					t.Errorf("branch:%s search mismatch against clean rebuild (-want +got):\n%s", branch, diff)
				}
			})

			t.Run("branchesrepos/"+check.pattern+"/"+branch, func(t *testing.T) {
				deltaQ := query.NewAnd(query.NewSingleBranchesRepos(branch, deltaRepo.ID), multiRenameSubstringQuery(check.pattern))
				cleanQ := query.NewAnd(query.NewSingleBranchesRepos(branch, cleanRepo.ID), multiRenameSubstringQuery(check.pattern))
				delta := multiRenameSearch(t, deltaIndexDir, deltaQ)
				clean := multiRenameSearch(t, cleanIndexDir, cleanQ)
				multiRenameAssertResultNames(t, "delta BranchesRepos:"+branch, want, delta)
				multiRenameAssertResultNames(t, "clean BranchesRepos:"+branch, want, clean)
				if diff := cmp.Diff(clean, delta); diff != "" {
					t.Errorf("BranchesRepos %s search mismatch against clean rebuild (-want +got):\n%s", branch, diff)
				}
			})
		}
	}
}

func multiRenameSearch(t *testing.T, indexDir string, q query.Q) []multiRenameSearchResult {
	t.Helper()

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %v", indexDir, err)
	}
	defer searcher.Close()

	result, err := searcher.Search(context.Background(), q, &zoekt.SearchOptions{Whole: true})
	if err != nil {
		t.Fatalf("Search(%s): %v", q.String(), err)
	}

	results := make([]multiRenameSearchResult, 0, len(result.Files))
	for _, file := range result.Files {
		branches := append([]string(nil), file.Branches...)
		sort.Strings(branches)
		results = append(results, multiRenameSearchResult{
			FileName: file.FileName,
			Content:  string(file.Content),
			Version:  file.Version,
			Branches: branches,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.FileName != b.FileName {
			return a.FileName < b.FileName
		}
		if a.Content != b.Content {
			return a.Content < b.Content
		}
		return strings.Join(a.Branches, "\x00") < strings.Join(b.Branches, "\x00")
	})
	return results
}

func multiRenameSubstringQuery(pattern string) query.Q {
	return &query.Substring{Pattern: pattern, Content: true}
}

func multiRenameAssertResultNames(t *testing.T, label string, want []string, results []multiRenameSearchResult) {
	t.Helper()

	if want == nil {
		want = []string{}
	}
	got := make([]string, 0, len(results))
	for _, result := range results {
		got = append(got, result.FileName)
	}
	sort.Strings(got)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("%s file names mismatch (-want +got):\n%s", label, diff)
	}
}

func multiRenameAssertTombstones(t *testing.T, indexDir string, wantPaths []string) {
	t.Helper()

	opts := index.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			Name: "repository",
		},
	}

	got := map[string]struct{}{}
	for _, shard := range opts.FindAllShards() {
		repositories, _, err := index.ReadMetadataPathAlive(shard)
		if err != nil {
			t.Fatalf("ReadMetadataPathAlive(%q): %v", shard, err)
		}
		for _, repository := range repositories {
			if repository.Name != "repository" {
				continue
			}
			for path := range repository.FileTombstones {
				got[path] = struct{}{}
			}
		}
	}

	for _, path := range wantPaths {
		if _, ok := got[path]; !ok {
			t.Errorf("expected old shards to tombstone %q, got tombstones %v", path, multiRenameSortedKeys(got))
		}
	}
}

func multiRenameSortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func multiRenameBranch(t *testing.T, repositoryDir, oldName, newName string) {
	t.Helper()
	runGit(t, repositoryDir, "branch", "-m", oldName, newName)
}

func multiRenameCheckout(t *testing.T, repositoryDir, branch string) {
	t.Helper()
	runGit(t, repositoryDir, "checkout", branch)
}

func multiRenameWriteAndCommit(t *testing.T, repositoryDir string, files map[string]string, message string) {
	t.Helper()

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
	runGit(t, repositoryDir, "commit", "-m", message)
}

// ---- delta_multibranch_add_test.go ----

const addBranchRepoID uint32 = 4242

func TestIndexGitRepo_DeltaMultibranchAddBranches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		initialFiles  map[string]string
		mutate        func(t *testing.T, repoDir string)
		finalBranches []string
		lookups       []addBranchLookup
	}{
		{
			name: "add one new branch identical to existing branch",
			initialFiles: map[string]string{
				"common.txt": "common-token\n",
				"shared.txt": "shared-token\n",
			},
			mutate: func(t *testing.T, repoDir string) {
				runGit(t, repoDir, "branch", "release", "main")
			},
			finalBranches: []string{"main", "release"},
			lookups: []addBranchLookup{
				{name: "unfiltered shared content", pattern: "shared-token", want: []string{"shared.txt"}},
				{name: "main all files", branch: "main", want: []string{"common.txt", "shared.txt"}},
				{name: "release all files", branch: "release", want: []string{"common.txt", "shared.txt"}},
				{name: "release common content", pattern: "common-token", branch: "release", want: []string{"common.txt"}},
			},
		},
		{
			name: "add one new branch with one changed path",
			initialFiles: map[string]string{
				"release.txt": "base-release-token\n",
				"stable.txt":  "stable-token\n",
			},
			mutate: func(t *testing.T, repoDir string) {
				runGit(t, repoDir, "checkout", "-b", "release", "main")
				addBranchWriteCommit(t, repoDir, map[string]string{
					"release.txt": "release-only-token\n",
				}, nil, "release changes one path")
			},
			finalBranches: []string{"main", "release"},
			lookups: []addBranchLookup{
				{name: "main keeps old content", pattern: "base-release-token", branch: "main", want: []string{"release.txt"}},
				{name: "release misses old content", pattern: "base-release-token", branch: "release", want: nil},
				{name: "release sees changed content", pattern: "release-only-token", branch: "release", want: []string{"release.txt"}},
				{name: "main misses changed content", pattern: "release-only-token", branch: "main", want: nil},
				{name: "stable shared on release", pattern: "stable-token", branch: "release", want: []string{"stable.txt"}},
			},
		},
		{
			name: "add one new branch with deletions relative to existing branch",
			initialFiles: map[string]string{
				"keep.txt":  "keep-token\n",
				"large.txt": "large-token\n",
			},
			mutate: func(t *testing.T, repoDir string) {
				runGit(t, repoDir, "checkout", "-b", "slim", "main")
				addBranchWriteCommit(t, repoDir, nil, []string{"large.txt"}, "slim deletes large path")
			},
			finalBranches: []string{"main", "slim"},
			lookups: []addBranchLookup{
				{name: "main keeps deleted-on-new-branch file", pattern: "large-token", branch: "main", want: []string{"large.txt"}},
				{name: "slim misses deleted file", pattern: "large-token", branch: "slim", want: nil},
				{name: "slim keeps shared file", pattern: "keep-token", branch: "slim", want: []string{"keep.txt"}},
				{name: "slim all files", branch: "slim", want: []string{"keep.txt"}},
			},
		},
		{
			name: "add multiple new branches at once",
			initialFiles: map[string]string{
				"dev.txt":     "base-dev-token\n",
				"main.txt":    "main-token\n",
				"release.txt": "base-release-token\n",
				"shared.txt":  "shared-token\n",
			},
			mutate: func(t *testing.T, repoDir string) {
				runGit(t, repoDir, "checkout", "-b", "release", "main")
				addBranchWriteCommit(t, repoDir, map[string]string{
					"release.txt": "release-multi-token\n",
				}, nil, "release changes one path")
				runGit(t, repoDir, "checkout", "main")
				runGit(t, repoDir, "checkout", "-b", "dev", "main")
				addBranchWriteCommit(t, repoDir, map[string]string{
					"dev.txt": "dev-multi-token\n",
				}, nil, "dev changes one path")
			},
			finalBranches: []string{"main", "release", "dev"},
			lookups: []addBranchLookup{
				{name: "main all files", branch: "main", want: []string{"dev.txt", "main.txt", "release.txt", "shared.txt"}},
				{name: "release all files", branch: "release", want: []string{"dev.txt", "main.txt", "release.txt", "shared.txt"}},
				{name: "dev all files", branch: "dev", want: []string{"dev.txt", "main.txt", "release.txt", "shared.txt"}},
				{name: "release sees release content", pattern: "release-multi-token", branch: "release", want: []string{"release.txt"}},
				{name: "main misses release content", pattern: "release-multi-token", branch: "main", want: nil},
				{name: "dev sees dev content", pattern: "dev-multi-token", branch: "dev", want: []string{"dev.txt"}},
				{name: "main misses dev content", pattern: "dev-multi-token", branch: "main", want: nil},
			},
		},
		{
			name: "add one branch that shares some docs and diverges on others",
			initialFiles: map[string]string{
				"a.txt": "a-main-token\n",
				"b.txt": "b-shared-token\n",
				"d.txt": "d-main-token\n",
			},
			mutate: func(t *testing.T, repoDir string) {
				runGit(t, repoDir, "checkout", "-b", "feature", "main")
				addBranchWriteCommit(t, repoDir, map[string]string{
					"a.txt": "a-feature-token\n",
					"c.txt": "c-feature-token\n",
				}, []string{"d.txt"}, "feature shares diverges adds deletes")
			},
			finalBranches: []string{"main", "feature"},
			lookups: []addBranchLookup{
				{name: "main all files", branch: "main", want: []string{"a.txt", "b.txt", "d.txt"}},
				{name: "feature all files", branch: "feature", want: []string{"a.txt", "b.txt", "c.txt"}},
				{name: "main keeps old a", pattern: "a-main-token", branch: "main", want: []string{"a.txt"}},
				{name: "feature misses old a", pattern: "a-main-token", branch: "feature", want: nil},
				{name: "feature sees changed a", pattern: "a-feature-token", branch: "feature", want: []string{"a.txt"}},
				{name: "shared b on feature", pattern: "b-shared-token", branch: "feature", want: []string{"b.txt"}},
				{name: "feature sees added c", pattern: "c-feature-token", branch: "feature", want: []string{"c.txt"}},
				{name: "main misses added c", pattern: "c-feature-token", branch: "main", want: nil},
				{name: "main keeps feature-deleted d", pattern: "d-main-token", branch: "main", want: []string{"d.txt"}},
				{name: "feature misses deleted d", pattern: "d-main-token", branch: "feature", want: nil},
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repoDir := addBranchInitRepo(t, tc.initialFiles)
			indexDir := t.TempDir()
			initialOpts := addBranchOptions(repoDir, indexDir, []string{"main"})
			if _, err := IndexGitRepo(initialOpts); err != nil {
				t.Fatalf("initial IndexGitRepo: %v", err)
			}

			tc.mutate(t, repoDir)

			deltaOpts := addBranchOptions(repoDir, indexDir, tc.finalBranches)
			deltaOpts.BuildOptions.IsDelta = true
			deltaCalled, normalCalled := indexGitRepoWithPrepareSpies(t, deltaOpts)
			if !deltaCalled {
				t.Errorf("expected delta build to be attempted")
			}
			if normalCalled {
				t.Errorf("expected branch-add update to use delta without falling back to a normal build")
			}

			cleanIndexDir := t.TempDir()
			cleanOpts := addBranchOptions(repoDir, cleanIndexDir, tc.finalBranches)
			if _, err := IndexGitRepo(cleanOpts); err != nil {
				t.Fatalf("clean full IndexGitRepo: %v", err)
			}

			addBranchAssertMetadata(t, indexDir, cleanIndexDir, tc.finalBranches)
			addBranchAssertLiveStats(t, indexDir, cleanIndexDir)
			for _, lookup := range tc.lookups {
				addBranchAssertLookup(t, indexDir, cleanIndexDir, lookup)
			}
		})
	}
}

type addBranchLookup struct {
	name    string
	pattern string
	branch  string
	want    []string
}

func addBranchInitRepo(t *testing.T, files map[string]string) string {
	t.Helper()

	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-b", "main")
	addBranchWriteCommit(t, repoDir, files, nil, "initial main")
	return repoDir
}

func addBranchWriteCommit(t *testing.T, repoDir string, files map[string]string, deletes []string, message string) {
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
	runGit(t, repoDir, "commit", "-m", message)
}

func addBranchOptions(repoDir, indexDir string, branches []string) Options {
	return Options{
		RepoDir:                   filepath.Join(repoDir, ".git"),
		Branches:                  branches,
		AllowDeltaBranchSetChange: true,
		DeltaAdmissionMode:        DeltaAdmissionModeStatsV1,
		DeltaAdmissionThresholds: DeltaAdmissionThresholds{
			MaxDeltaIndexedBytesRatio: 100,
			MaxPhysicalLiveBytesRatio: 100,
			MaxTombstonePathRatio:     100,
			MaxShardFanoutRatio:       100,
		},
		BuildOptions: index.Options{
			RepositoryDescription: zoekt.Repository{
				ID:   addBranchRepoID,
				Name: "repository",
			},
			IndexDir:     indexDir,
			DisableCTags: true,
		},
	}
}

func addBranchAssertMetadata(t *testing.T, deltaIndexDir, cleanIndexDir string, wantBranches []string) {
	t.Helper()

	deltaRepo := addBranchIndexedRepository(t, deltaIndexDir)
	cleanRepo := addBranchIndexedRepository(t, cleanIndexDir)

	if got := repositoryBranchNames(deltaRepo.Branches); !cmp.Equal(got, wantBranches) {
		t.Fatalf("delta branch names mismatch (-want +got):\n%s", cmp.Diff(wantBranches, got))
	}
	if diff := cmp.Diff(cleanRepo.Branches, deltaRepo.Branches); diff != "" {
		t.Fatalf("delta branch metadata differs from clean full rebuild (-want +got):\n%s", diff)
	}
}

func addBranchAssertLiveStats(t *testing.T, deltaIndexDir, cleanIndexDir string) {
	t.Helper()

	deltaRepo := addBranchIndexedRepository(t, deltaIndexDir)
	cleanRepo := addBranchIndexedRepository(t, cleanIndexDir)
	if deltaRepo.DeltaStats == nil {
		t.Fatal("delta index DeltaStats is nil")
	}
	if cleanRepo.DeltaStats == nil {
		t.Fatal("clean index DeltaStats is nil")
	}

	got := addBranchLiveStats{
		LiveIndexedBytes:  deltaRepo.DeltaStats.LiveIndexedBytes,
		LiveDocumentCount: deltaRepo.DeltaStats.LiveDocumentCount,
		LivePathCount:     deltaRepo.DeltaStats.LivePathCount,
	}
	want := addBranchLiveStats{
		LiveIndexedBytes:  cleanRepo.DeltaStats.LiveIndexedBytes,
		LiveDocumentCount: cleanRepo.DeltaStats.LiveDocumentCount,
		LivePathCount:     cleanRepo.DeltaStats.LivePathCount,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("delta live stats differ from clean full rebuild (-want +got):\n%s", diff)
	}
}

func addBranchIndexedRepository(t *testing.T, indexDir string) *zoekt.Repository {
	t.Helper()

	opts := index.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			ID:   addBranchRepoID,
			Name: "repository",
		},
	}

	repo, _, ok, err := opts.FindRepositoryMetadata()
	if err != nil {
		t.Fatalf("FindRepositoryMetadata: %v", err)
	}
	if !ok {
		t.Fatal("FindRepositoryMetadata: repository not found")
	}
	return repo
}

type addBranchLiveStats struct {
	LiveIndexedBytes  uint64
	LiveDocumentCount uint64
	LivePathCount     uint64
}

func addBranchAssertLookup(t *testing.T, deltaIndexDir, cleanIndexDir string, lookup addBranchLookup) {
	t.Helper()

	base := addBranchContentQuery(lookup.pattern)
	if lookup.branch == "" {
		addBranchAssertQuery(t, deltaIndexDir, cleanIndexDir, lookup.name+"/unfiltered", base, lookup.want)
		return
	}

	branchQuery := query.NewAnd(&query.Branch{Pattern: lookup.branch, Exact: true}, base)
	addBranchAssertQuery(t, deltaIndexDir, cleanIndexDir, lookup.name+"/branch-query", branchQuery, lookup.want)

	branchesReposQuery := query.NewAnd(query.NewSingleBranchesRepos(lookup.branch, addBranchRepoID), base)
	addBranchAssertQuery(t, deltaIndexDir, cleanIndexDir, lookup.name+"/branches-repos", branchesReposQuery, lookup.want)
}

func addBranchContentQuery(pattern string) query.Q {
	if pattern == "" {
		return &query.Const{Value: true}
	}
	return &query.Substring{Pattern: pattern}
}

func addBranchAssertQuery(t *testing.T, deltaIndexDir, cleanIndexDir, name string, q query.Q, want []string) {
	t.Helper()

	delta := addBranchSearchHits(t, deltaIndexDir, q)
	clean := addBranchSearchHits(t, cleanIndexDir, q)
	if diff := cmp.Diff(clean, delta); diff != "" {
		t.Fatalf("%s: delta results differ from clean full rebuild (-want +got):\n%s", name, diff)
	}

	gotNames := addBranchHitFileNames(delta)
	want = append([]string(nil), want...)
	if want == nil {
		want = []string{}
	}
	sort.Strings(want)
	if diff := cmp.Diff(want, gotNames); diff != "" {
		t.Fatalf("%s: result file names mismatch (-want +got):\n%s", name, diff)
	}
}

type addBranchSearchHit struct {
	FileName string
	Content  string
}

func addBranchSearchHits(t *testing.T, indexDir string, q query.Q) []addBranchSearchHit {
	t.Helper()

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%q): %v", indexDir, err)
	}
	defer searcher.Close()

	result, err := searcher.Search(context.Background(), q, &zoekt.SearchOptions{Whole: true})
	if err != nil {
		t.Fatalf("Search(%s): %v", q, err)
	}

	hits := make([]addBranchSearchHit, 0, len(result.Files))
	for _, file := range result.Files {
		hits = append(hits, addBranchSearchHit{
			FileName: file.FileName,
			Content:  string(file.Content),
		})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].FileName != hits[j].FileName {
			return hits[i].FileName < hits[j].FileName
		}
		return hits[i].Content < hits[j].Content
	})
	return hits
}

func addBranchHitFileNames(hits []addBranchSearchHit) []string {
	names := make([]string, 0, len(hits))
	for _, hit := range hits {
		names = append(names, hit.FileName)
	}
	sort.Strings(names)
	return names
}

// ---- delta_multibranch_remove_test.go ----

const removeBranchRepoID uint32 = 42

type removeBranchTree map[string]string

type removeBranchDocument struct {
	Name     string
	Content  string
	Branches []string
}

func TestIndexGitRepo_DeltaRemoveBranchNoContentChanges(t *testing.T) {
	t.Parallel()

	removeBranchRunScenario(t, removeBranchScenario{
		initialBranches: []string{"main", "release"},
		initialTrees: map[string]removeBranchTree{
			"main": {
				"shared.txt": "shared-retained-needle\n",
			},
			"release": {
				"shared.txt": "shared-retained-needle\n",
			},
		},
		finalBranches:    []string{"main"},
		removedBranches:  []string{"release"},
		retainedBranches: []string{"main"},
		mutate: func(t *testing.T, repoDir string) {
			removeBranchDeleteBranches(t, repoDir, "release")
		},
		patterns: []string{
			"shared-retained-needle",
			"release-only-needle",
		},
	})
}

func TestIndexGitRepo_DeltaRemoveBranchOnlyFiles(t *testing.T) {
	t.Parallel()

	removeBranchRunScenario(t, removeBranchScenario{
		initialBranches: []string{"main", "release"},
		initialTrees: map[string]removeBranchTree{
			"main": {
				"main.txt": "main-retained-needle\n",
			},
			"release": {
				"release-only.txt": "release-only-needle\n",
			},
		},
		finalBranches:    []string{"main"},
		removedBranches:  []string{"release"},
		retainedBranches: []string{"main"},
		mutate: func(t *testing.T, repoDir string) {
			removeBranchDeleteBranches(t, repoDir, "release")
		},
		patterns: []string{
			"main-retained-needle",
			"release-only-needle",
		},
	})
}

func TestIndexGitRepo_DeltaRemoveBranchSamePathDifferentContent(t *testing.T) {
	t.Parallel()

	removeBranchRunScenario(t, removeBranchScenario{
		initialBranches: []string{"main", "release"},
		initialTrees: map[string]removeBranchTree{
			"main": {
				"shared.txt": "main-shared-retained-needle\n",
			},
			"release": {
				"shared.txt": "release-shared-stale-needle\n",
			},
		},
		finalBranches:    []string{"main"},
		removedBranches:  []string{"release"},
		retainedBranches: []string{"main"},
		mutate: func(t *testing.T, repoDir string) {
			removeBranchDeleteBranches(t, repoDir, "release")
		},
		patterns: []string{
			"main-shared-retained-needle",
			"release-shared-stale-needle",
		},
	})
}

func TestIndexGitRepo_DeltaRemoveMultipleBranches(t *testing.T) {
	t.Parallel()

	removeBranchRunScenario(t, removeBranchScenario{
		initialBranches: []string{"main", "release", "dev", "qa"},
		initialTrees: map[string]removeBranchTree{
			"main": {
				"main.txt": "main-retained-needle\n",
			},
			"release": {
				"release.txt": "release-stale-needle\n",
			},
			"dev": {
				"dev.txt": "dev-stale-needle\n",
			},
			"qa": {
				"qa.txt": "qa-stale-needle\n",
			},
		},
		finalBranches:    []string{"main"},
		removedBranches:  []string{"release", "dev", "qa"},
		retainedBranches: []string{"main"},
		mutate: func(t *testing.T, repoDir string) {
			removeBranchDeleteBranches(t, repoDir, "release", "dev", "qa")
		},
		patterns: []string{
			"main-retained-needle",
			"release-stale-needle",
			"dev-stale-needle",
			"qa-stale-needle",
		},
	})
}

func TestIndexGitRepo_DeltaRemoveBranchAndModifyRetainedBranch(t *testing.T) {
	t.Parallel()

	removeBranchRunScenario(t, removeBranchScenario{
		initialBranches: []string{"main", "release"},
		initialTrees: map[string]removeBranchTree{
			"main": {
				"a.txt": "main-old-stale-needle\n",
			},
			"release": {
				"release-only.txt": "release-only-needle\n",
			},
		},
		finalBranches:    []string{"main"},
		removedBranches:  []string{"release"},
		retainedBranches: []string{"main"},
		mutate: func(t *testing.T, repoDir string) {
			runGit(t, repoDir, "checkout", "main")
			removeBranchWriteFile(t, repoDir, "a.txt", "main-new-retained-needle\n")
			runGit(t, repoDir, "add", "-A")
			runGit(t, repoDir, "commit", "-m", "modify retained main")
			removeBranchDeleteBranches(t, repoDir, "release")
		},
		patterns: []string{
			"main-new-retained-needle",
			"main-old-stale-needle",
			"release-only-needle",
		},
	})
}

type removeBranchScenario struct {
	initialBranches []string
	initialTrees    map[string]removeBranchTree
	finalBranches   []string

	removedBranches  []string
	retainedBranches []string

	mutate   func(t *testing.T, repoDir string)
	patterns []string
}

func removeBranchRunScenario(t *testing.T, scenario removeBranchScenario) {
	t.Helper()

	repoDir := removeBranchInitRepository(t, scenario.initialBranches, scenario.initialTrees)
	indexDir := t.TempDir()

	initialOpts := removeBranchOptions(repoDir, indexDir, scenario.initialBranches)
	if _, err := IndexGitRepo(initialOpts); err != nil {
		t.Fatalf("initial IndexGitRepo: %v", err)
	}

	scenario.mutate(t, repoDir)

	deltaOpts := removeBranchOptions(repoDir, indexDir, scenario.finalBranches)
	deltaOpts.BuildOptions.IsDelta = true
	deltaBuildCalled, normalBuildCalled := removeBranchIndexWithSpies(t, deltaOpts)
	if !deltaBuildCalled {
		t.Errorf("expected delta build to be attempted")
	}
	if normalBuildCalled {
		t.Errorf("expected branch removal to use delta build without falling back to normal build")
	}

	cleanIndexDir := t.TempDir()
	cleanOpts := removeBranchOptions(repoDir, cleanIndexDir, scenario.finalBranches)
	if _, err := IndexGitRepo(cleanOpts); err != nil {
		t.Fatalf("clean IndexGitRepo: %v", err)
	}

	removeBranchAssertRepositoryMatchesClean(t, indexDir, cleanIndexDir)
	removeBranchAssertIndexMatchesClean(t, indexDir, cleanIndexDir, scenario.retainedBranches, scenario.removedBranches, scenario.patterns)
}

func removeBranchOptions(repoDir, indexDir string, branches []string) Options {
	return Options{
		RepoDir:                   filepath.Join(repoDir, ".git"),
		Branches:                  append([]string(nil), branches...),
		AllowDeltaBranchSetChange: true,
		BuildOptions: index.Options{
			RepositoryDescription: zoekt.Repository{
				ID:   removeBranchRepoID,
				Name: "repository",
			},
			IndexDir:     indexDir,
			DisableCTags: true,
		},
	}
}

func removeBranchIndexWithSpies(t *testing.T, opts Options) (deltaBuildCalled, normalBuildCalled bool) {
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
		t.Fatalf("delta IndexGitRepo: %v", err)
	}

	return deltaBuildCalled, normalBuildCalled
}

func removeBranchAssertRepositoryMatchesClean(t *testing.T, indexDir, cleanIndexDir string) {
	t.Helper()

	got := removeBranchIndexedRepositoryForTest(t, indexDir)
	want := removeBranchIndexedRepositoryForTest(t, cleanIndexDir)
	if diff := cmp.Diff(want.Branches, got.Branches); diff != "" {
		t.Errorf("indexed branches mismatch against clean full rebuild (-want +got):\n%s", diff)
	}
}

func removeBranchIndexedRepositoryForTest(t *testing.T, indexDir string) *zoekt.Repository {
	t.Helper()

	opts := index.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			ID:   removeBranchRepoID,
			Name: "repository",
		},
	}

	repo, _, ok, err := opts.FindRepositoryMetadata()
	if err != nil {
		t.Fatalf("FindRepositoryMetadata: %v", err)
	}
	if !ok {
		t.Fatalf("FindRepositoryMetadata: repository not found")
	}

	return repo
}

func removeBranchAssertIndexMatchesClean(t *testing.T, indexDir, cleanIndexDir string, retainedBranches, removedBranches, patterns []string) {
	t.Helper()

	removeBranchAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "unfiltered/all", &query.Const{Value: true})
	for _, pattern := range patterns {
		removeBranchAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "unfiltered/"+pattern, &query.Substring{Pattern: pattern})
	}

	for _, branch := range retainedBranches {
		removeBranchAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "branch/"+branch+"/all", &query.Branch{Pattern: branch, Exact: true})
		removeBranchAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "branchesrepos/"+branch+"/all", query.NewSingleBranchesRepos(branch, removeBranchRepoID))
		for _, pattern := range patterns {
			substr := &query.Substring{Pattern: pattern}
			removeBranchAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "branch/"+branch+"/"+pattern, query.NewAnd(&query.Branch{Pattern: branch, Exact: true}, substr))
			removeBranchAssertQueryMatchesClean(t, indexDir, cleanIndexDir, "branchesrepos/"+branch+"/"+pattern, query.NewAnd(query.NewSingleBranchesRepos(branch, removeBranchRepoID), substr))
		}
	}

	for _, branch := range removedBranches {
		removeBranchAssertNoResults(t, indexDir, "removed branch/"+branch, &query.Branch{Pattern: branch, Exact: true})
		removeBranchAssertNoResults(t, indexDir, "removed branchesrepos/"+branch, query.NewSingleBranchesRepos(branch, removeBranchRepoID))
	}
}

func removeBranchAssertQueryMatchesClean(t *testing.T, indexDir, cleanIndexDir, name string, q query.Q) {
	t.Helper()

	got := removeBranchSearchDocuments(t, indexDir, q)
	want := removeBranchSearchDocuments(t, cleanIndexDir, q)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("%s mismatch against clean full rebuild (-want +got):\n%s", name, diff)
	}
}

func removeBranchAssertNoResults(t *testing.T, indexDir, name string, q query.Q) {
	t.Helper()

	if got := removeBranchSearchDocuments(t, indexDir, q); len(got) != 0 {
		t.Errorf("%s returned removed-branch results: %+v", name, got)
	}
}

func removeBranchSearchDocuments(t *testing.T, indexDir string, q query.Q) []removeBranchDocument {
	t.Helper()

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %v", indexDir, err)
	}
	defer searcher.Close()

	result, err := searcher.Search(context.Background(), q, &zoekt.SearchOptions{Whole: true})
	if err != nil {
		t.Fatalf("Search(%s): %v", q.String(), err)
	}

	docs := make([]removeBranchDocument, 0, len(result.Files))
	for _, file := range result.Files {
		branches := append([]string(nil), file.Branches...)
		sort.Strings(branches)
		docs = append(docs, removeBranchDocument{
			Name:     file.FileName,
			Content:  string(file.Content),
			Branches: branches,
		})
	}
	sort.Slice(docs, func(i, j int) bool {
		if docs[i].Name != docs[j].Name {
			return docs[i].Name < docs[j].Name
		}
		if docs[i].Content != docs[j].Content {
			return docs[i].Content < docs[j].Content
		}
		return strings.Join(docs[i].Branches, "\x00") < strings.Join(docs[j].Branches, "\x00")
	})
	return docs
}

func removeBranchInitRepository(t *testing.T, branches []string, trees map[string]removeBranchTree) string {
	t.Helper()

	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-b", "main")
	runGit(t, repoDir, "commit", "--allow-empty", "-m", "base")
	base := removeBranchGitOutput(t, repoDir, "rev-parse", "HEAD")

	for _, branch := range branches {
		runGit(t, repoDir, "checkout", "-B", branch, base)
		removeBranchReplaceTree(t, repoDir, trees[branch])
		runGit(t, repoDir, "add", "-A")
		runGit(t, repoDir, "commit", "--allow-empty", "-m", branch+" initial")
	}
	runGit(t, repoDir, "checkout", branches[0])
	return repoDir
}

func removeBranchReplaceTree(t *testing.T, repoDir string, tree removeBranchTree) {
	t.Helper()

	runGit(t, repoDir, "rm", "-r", "--ignore-unmatch", ".")

	names := make([]string, 0, len(tree))
	for name := range tree {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		removeBranchWriteFile(t, repoDir, name, tree[name])
	}
}

func removeBranchWriteFile(t *testing.T, repoDir, name, content string) {
	t.Helper()

	path := filepath.Join(repoDir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func removeBranchDeleteBranches(t *testing.T, repoDir string, branches ...string) {
	t.Helper()

	runGit(t, repoDir, "checkout", "main")
	for _, branch := range branches {
		runGit(t, repoDir, "branch", "-D", branch)
	}
}

func removeBranchGitOutput(t *testing.T, cwd string, args ...string) string {
	t.Helper()

	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("ensuring path %q exists: %s", cwd, err)
	}

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

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("execution error: %v, output %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// ---- delta_multibranch_combined_test.go ----

const (
	combinedBranchRepoID   = 4242
	combinedBranchRepoName = "repository"
)

type combinedBranchFiles map[string]string

type combinedBranchExpectedLookup struct {
	branch    string
	pattern   string
	wantFiles []string
}

func TestIndexGitRepo_DeltaMultiBranchCombinedRenameAddRemove(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		initialBranches    []string
		finalBranches      []string
		initialFiles       map[string]combinedBranchFiles
		mutate             func(t *testing.T, repoDir string)
		patterns           []string
		expectedLookups    []combinedBranchExpectedLookup
		expectedTombstones []string
	}{
		{
			name:            "rename one branch and add one branch",
			initialBranches: []string{"feature-a", "release"},
			finalBranches:   []string{"feature-b", "release", "dev"},
			initialFiles: map[string]combinedBranchFiles{
				"feature-a": {"branch.txt": "feature-a-stale-needle\n"},
				"release":   {"release.txt": "release-unchanged-needle\n"},
			},
			mutate: func(t *testing.T, repoDir string) {
				combinedBranchRenameBranch(t, repoDir, "feature-a", "feature-b")
				combinedBranchCheckoutWriteCommit(t, repoDir, "feature-b", combinedBranchFiles{
					"branch.txt": "feature-b-renamed-needle\n",
				}, "update feature-b")
				combinedBranchCheckoutNewBranchWriteCommit(t, repoDir, "dev", "release", combinedBranchFiles{
					"dev.txt": "dev-added-needle\n",
				}, "add dev")
			},
			patterns: []string{
				"feature-a-stale-needle",
				"feature-b-renamed-needle",
				"release-unchanged-needle",
				"dev-added-needle",
			},
			expectedLookups: []combinedBranchExpectedLookup{
				{branch: "feature-b", pattern: "feature-b-renamed-needle", wantFiles: []string{"branch.txt"}},
				{branch: "release", pattern: "release-unchanged-needle", wantFiles: []string{"release.txt"}},
				{branch: "dev", pattern: "release-unchanged-needle", wantFiles: []string{"release.txt"}},
				{branch: "dev", pattern: "dev-added-needle", wantFiles: []string{"dev.txt"}},
			},
			expectedTombstones: []string{"branch.txt"},
		},
		{
			name:            "rename one branch and remove one branch",
			initialBranches: []string{"feature-a", "release", "dev"},
			finalBranches:   []string{"feature-b", "release"},
			initialFiles: map[string]combinedBranchFiles{
				"feature-a": {"feature.txt": "feature-a-stale-needle\n"},
				"release":   {"release.txt": "release-kept-needle\n"},
				"dev":       {"dev.txt": "dev-removed-needle\n"},
			},
			mutate: func(t *testing.T, repoDir string) {
				combinedBranchRenameBranch(t, repoDir, "feature-a", "feature-b")
				combinedBranchCheckoutWriteCommit(t, repoDir, "feature-b", combinedBranchFiles{
					"feature.txt": "feature-b-after-remove-needle\n",
				}, "update feature-b")
				combinedBranchRunGit(t, repoDir, "checkout", "release")
				combinedBranchDeleteBranch(t, repoDir, "dev")
			},
			patterns: []string{
				"feature-a-stale-needle",
				"feature-b-after-remove-needle",
				"release-kept-needle",
				"dev-removed-needle",
			},
			expectedLookups: []combinedBranchExpectedLookup{
				{branch: "feature-b", pattern: "feature-b-after-remove-needle", wantFiles: []string{"feature.txt"}},
				{branch: "release", pattern: "release-kept-needle", wantFiles: []string{"release.txt"}},
			},
			expectedTombstones: []string{"feature.txt", "dev.txt"},
		},
		{
			name:            "add one branch and remove one branch",
			initialBranches: []string{"main", "old-release"},
			finalBranches:   []string{"main", "new-release"},
			initialFiles: map[string]combinedBranchFiles{
				"main":        {"main.txt": "main-kept-needle\n"},
				"old-release": {"old.txt": "old-release-stale-needle\n"},
			},
			mutate: func(t *testing.T, repoDir string) {
				combinedBranchRunGit(t, repoDir, "checkout", "main")
				combinedBranchDeleteBranch(t, repoDir, "old-release")
				combinedBranchCheckoutNewBranchWriteCommit(t, repoDir, "new-release", "main", combinedBranchFiles{
					"new.txt": "new-release-added-needle\n",
				}, "add new-release")
			},
			patterns: []string{
				"main-kept-needle",
				"old-release-stale-needle",
				"new-release-added-needle",
			},
			expectedLookups: []combinedBranchExpectedLookup{
				{branch: "main", pattern: "main-kept-needle", wantFiles: []string{"main.txt"}},
				{branch: "new-release", pattern: "main-kept-needle", wantFiles: []string{"main.txt"}},
				{branch: "new-release", pattern: "new-release-added-needle", wantFiles: []string{"new.txt"}},
			},
			expectedTombstones: []string{"old.txt"},
		},
		{
			name:            "rename multiple branches, add one branch, and remove one branch",
			initialBranches: []string{"feature-a", "qa-a", "old-release", "main"},
			finalBranches:   []string{"feature-b", "qa-b", "new-release", "main"},
			initialFiles: map[string]combinedBranchFiles{
				"feature-a":   {"feature.txt": "feature-a-stale-needle\n"},
				"qa-a":        {"qa.txt": "qa-a-stale-needle\n"},
				"old-release": {"old-release.txt": "old-release-stale-needle\n"},
				"main":        {"main.txt": "main-stable-needle\n"},
			},
			mutate: func(t *testing.T, repoDir string) {
				combinedBranchRenameBranch(t, repoDir, "feature-a", "feature-b")
				combinedBranchRenameBranch(t, repoDir, "qa-a", "qa-b")
				combinedBranchCheckoutWriteCommit(t, repoDir, "feature-b", combinedBranchFiles{
					"feature.txt": "feature-b-final-needle\n",
				}, "update feature-b")
				combinedBranchCheckoutWriteCommit(t, repoDir, "qa-b", combinedBranchFiles{
					"qa.txt": "qa-b-final-needle\n",
				}, "update qa-b")
				combinedBranchRunGit(t, repoDir, "checkout", "main")
				combinedBranchDeleteBranch(t, repoDir, "old-release")
				combinedBranchCheckoutNewBranchWriteCommit(t, repoDir, "new-release", "main", combinedBranchFiles{
					"new-release.txt": "new-release-final-needle\n",
				}, "add new-release")
			},
			patterns: []string{
				"feature-a-stale-needle",
				"feature-b-final-needle",
				"qa-a-stale-needle",
				"qa-b-final-needle",
				"old-release-stale-needle",
				"main-stable-needle",
				"new-release-final-needle",
			},
			expectedLookups: []combinedBranchExpectedLookup{
				{branch: "feature-b", pattern: "feature-b-final-needle", wantFiles: []string{"feature.txt"}},
				{branch: "qa-b", pattern: "qa-b-final-needle", wantFiles: []string{"qa.txt"}},
				{branch: "new-release", pattern: "main-stable-needle", wantFiles: []string{"main.txt"}},
				{branch: "new-release", pattern: "new-release-final-needle", wantFiles: []string{"new-release.txt"}},
				{branch: "main", pattern: "main-stable-needle", wantFiles: []string{"main.txt"}},
			},
			expectedTombstones: []string{"feature.txt", "qa.txt", "old-release.txt"},
		},
		{
			name:            "branch count and order change together",
			initialBranches: []string{"main", "release", "dev"},
			finalBranches:   []string{"qa", "main", "release-renamed"},
			initialFiles: map[string]combinedBranchFiles{
				"main":    {"main.txt": "main-order-kept-needle\n"},
				"release": {"release.txt": "release-before-order-needle\n"},
				"dev":     {"dev.txt": "dev-order-removed-needle\n"},
			},
			mutate: func(t *testing.T, repoDir string) {
				combinedBranchRenameBranch(t, repoDir, "release", "release-renamed")
				combinedBranchCheckoutWriteCommit(t, repoDir, "release-renamed", combinedBranchFiles{
					"release.txt": "release-renamed-order-needle\n",
				}, "update release-renamed")
				combinedBranchCheckoutNewBranchWriteCommit(t, repoDir, "qa", "main", combinedBranchFiles{
					"qa.txt": "qa-order-added-needle\n",
				}, "add qa")
				combinedBranchRunGit(t, repoDir, "checkout", "main")
				combinedBranchDeleteBranch(t, repoDir, "dev")
			},
			patterns: []string{
				"main-order-kept-needle",
				"release-before-order-needle",
				"release-renamed-order-needle",
				"dev-order-removed-needle",
				"qa-order-added-needle",
			},
			expectedLookups: []combinedBranchExpectedLookup{
				{branch: "qa", pattern: "main-order-kept-needle", wantFiles: []string{"main.txt"}},
				{branch: "qa", pattern: "qa-order-added-needle", wantFiles: []string{"qa.txt"}},
				{branch: "main", pattern: "main-order-kept-needle", wantFiles: []string{"main.txt"}},
				{branch: "release-renamed", pattern: "release-renamed-order-needle", wantFiles: []string{"release.txt"}},
			},
			expectedTombstones: []string{"release.txt", "dev.txt"},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repoDir := combinedBranchCreateRepository(t, test.initialBranches, test.initialFiles)
			indexDir := t.TempDir()

			combinedBranchRunIndex(t, repoDir, indexDir, test.initialBranches, false)
			test.mutate(t, repoDir)

			deltaBuildCalled, normalBuildCalled := combinedBranchRunIndex(t, repoDir, indexDir, test.finalBranches, true)
			if !deltaBuildCalled {
				t.Error("expected delta build to be attempted")
			}
			if normalBuildCalled {
				t.Error("expected combined branch update to stay on the delta path")
			}

			cleanIndexDir := t.TempDir()
			combinedBranchRunIndex(t, repoDir, cleanIndexDir, test.finalBranches, false)

			combinedBranchAssertMetadataMatchesClean(t, indexDir, cleanIndexDir, test.finalBranches, test.initialBranches)

			branchesToCheck := combinedBranchUnion(test.initialBranches, test.finalBranches)
			combinedBranchAssertSearchEquivalent(t, indexDir, cleanIndexDir, branchesToCheck, test.patterns)
			combinedBranchAssertExpectedLookups(t, indexDir, cleanIndexDir, test.expectedLookups)
			combinedBranchAssertStaleBranchesAbsent(t, indexDir, cleanIndexDir, test.initialBranches, test.finalBranches, test.patterns)

			if !normalBuildCalled {
				combinedBranchAssertTombstones(t, indexDir, test.expectedTombstones)
			}
		})
	}
}

func combinedBranchCreateRepository(t *testing.T, initialBranches []string, initialFiles map[string]combinedBranchFiles) string {
	t.Helper()

	repoDir := t.TempDir()
	combinedBranchRunGit(t, repoDir, "init", "-b", "seed")
	combinedBranchRunGit(t, repoDir, "commit", "--allow-empty", "-m", "seed")

	for _, branch := range initialBranches {
		combinedBranchRunGit(t, repoDir, "checkout", "-B", branch, "seed")
		combinedBranchWriteFiles(t, repoDir, initialFiles[branch])
		combinedBranchRunGit(t, repoDir, "add", "-A")
		combinedBranchRunGit(t, repoDir, "commit", "-m", "initial "+branch)
	}

	combinedBranchRunGit(t, repoDir, "checkout", initialBranches[0])
	return repoDir
}

func combinedBranchRunIndex(t *testing.T, repoDir, indexDir string, branches []string, isDelta bool) (deltaBuildCalled, normalBuildCalled bool) {
	t.Helper()

	opts := Options{
		RepoDir:                   filepath.Join(repoDir, ".git"),
		Branches:                  append([]string(nil), branches...),
		AllowDeltaBranchSetChange: true,
		BuildOptions: index.Options{
			RepositoryDescription: zoekt.Repository{
				ID:   combinedBranchRepoID,
				Name: combinedBranchRepoName,
			},
			IndexDir:     indexDir,
			DisableCTags: true,
			IsDelta:      isDelta,
		},
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
		t.Fatalf("IndexGitRepo(delta=%t, branches=%v): %v", isDelta, branches, err)
	}

	return deltaBuildCalled, normalBuildCalled
}

func combinedBranchAssertMetadataMatchesClean(t *testing.T, indexDir, cleanIndexDir string, finalBranches, initialBranches []string) {
	t.Helper()

	gotRepo := combinedBranchIndexedRepository(t, indexDir)
	cleanRepo := combinedBranchIndexedRepository(t, cleanIndexDir)

	if diff := cmp.Diff(finalBranches, combinedBranchNames(gotRepo.Branches)); diff != "" {
		t.Fatalf("indexed branch names mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(cleanRepo.Branches, gotRepo.Branches); diff != "" {
		t.Fatalf("indexed branch metadata differs from clean full rebuild (-want +got):\n%s", diff)
	}

	finalSet := combinedBranchSet(finalBranches)
	for _, branch := range initialBranches {
		if !finalSet[branch] && combinedBranchContains(combinedBranchNames(gotRepo.Branches), branch) {
			t.Fatalf("stale branch %q remained in repository metadata: %+v", branch, gotRepo.Branches)
		}
	}
}

func combinedBranchAssertSearchEquivalent(t *testing.T, indexDir, cleanIndexDir string, branchesToCheck, patterns []string) {
	t.Helper()

	for _, pattern := range patterns {
		substr := &query.Substring{Pattern: pattern}
		combinedBranchAssertQueryEquivalent(t, indexDir, cleanIndexDir, "unfiltered "+pattern, substr)

		for _, branch := range branchesToCheck {
			branchQuery := query.NewAnd(&query.Branch{Pattern: branch, Exact: true}, substr)
			combinedBranchAssertQueryEquivalent(t, indexDir, cleanIndexDir, "branch:"+branch+" "+pattern, branchQuery)

			branchesReposQuery := query.NewAnd(query.NewSingleBranchesRepos(branch, combinedBranchRepoID), substr)
			combinedBranchAssertQueryEquivalent(t, indexDir, cleanIndexDir, "BranchesRepos:"+branch+" "+pattern, branchesReposQuery)
		}
	}
}

func combinedBranchAssertExpectedLookups(t *testing.T, indexDir, cleanIndexDir string, lookups []combinedBranchExpectedLookup) {
	t.Helper()

	for _, lookup := range lookups {
		for _, target := range []struct {
			name     string
			indexDir string
		}{
			{name: "delta", indexDir: indexDir},
			{name: "clean", indexDir: cleanIndexDir},
		} {
			branchQuery := query.NewAnd(&query.Branch{Pattern: lookup.branch, Exact: true}, &query.Substring{Pattern: lookup.pattern})
			combinedBranchAssertFileNames(t, target.indexDir, target.name+" branch:"+lookup.branch+" "+lookup.pattern, branchQuery, lookup.wantFiles)

			branchesReposQuery := query.NewAnd(query.NewSingleBranchesRepos(lookup.branch, combinedBranchRepoID), &query.Substring{Pattern: lookup.pattern})
			combinedBranchAssertFileNames(t, target.indexDir, target.name+" BranchesRepos:"+lookup.branch+" "+lookup.pattern, branchesReposQuery, lookup.wantFiles)
		}
	}
}

func combinedBranchAssertStaleBranchesAbsent(t *testing.T, indexDir, cleanIndexDir string, initialBranches, finalBranches, patterns []string) {
	t.Helper()

	finalSet := combinedBranchSet(finalBranches)
	for _, branch := range initialBranches {
		if finalSet[branch] {
			continue
		}
		for _, pattern := range patterns {
			for _, target := range []struct {
				name     string
				indexDir string
			}{
				{name: "delta", indexDir: indexDir},
				{name: "clean", indexDir: cleanIndexDir},
			} {
				branchQuery := query.NewAnd(&query.Branch{Pattern: branch, Exact: true}, &query.Substring{Pattern: pattern})
				combinedBranchAssertFileNames(t, target.indexDir, target.name+" stale branch:"+branch+" "+pattern, branchQuery, nil)

				branchesReposQuery := query.NewAnd(query.NewSingleBranchesRepos(branch, combinedBranchRepoID), &query.Substring{Pattern: pattern})
				combinedBranchAssertFileNames(t, target.indexDir, target.name+" stale BranchesRepos:"+branch+" "+pattern, branchesReposQuery, nil)
			}
		}
	}
}

func combinedBranchAssertQueryEquivalent(t *testing.T, indexDir, cleanIndexDir, label string, q query.Q) {
	t.Helper()

	got := combinedBranchSearch(t, indexDir, q)
	want := combinedBranchSearch(t, cleanIndexDir, q)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("%s search differs from clean full rebuild (-want +got):\n%s", label, diff)
	}
}

func combinedBranchAssertFileNames(t *testing.T, indexDir, label string, q query.Q, want []string) {
	t.Helper()

	got := combinedBranchFileNames(combinedBranchSearch(t, indexDir, q))
	if want == nil {
		want = []string{}
	}
	sort.Strings(want)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("%s file names mismatch (-want +got):\n%s", label, diff)
	}
}

func combinedBranchAssertTombstones(t *testing.T, indexDir string, wantPaths []string) {
	t.Helper()

	opts := index.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			ID:   combinedBranchRepoID,
			Name: combinedBranchRepoName,
		},
	}

	got := map[string]struct{}{}
	for _, shard := range opts.FindAllShards() {
		repositories, _, err := index.ReadMetadataPathAlive(shard)
		if err != nil {
			t.Fatalf("ReadMetadataPathAlive(%q): %v", shard, err)
		}
		for _, repository := range repositories {
			if repository.ID != combinedBranchRepoID {
				continue
			}
			for path := range repository.FileTombstones {
				got[path] = struct{}{}
			}
		}
	}

	for _, path := range wantPaths {
		if _, ok := got[path]; !ok {
			t.Fatalf("missing expected tombstone for %q; all tombstones: %v", path, combinedBranchMapKeys(got))
		}
	}
}

type combinedBranchSearchHit struct {
	FileName string
	Branches []string
	Version  string
	Content  string
}

func combinedBranchSearch(t *testing.T, indexDir string, q query.Q) []combinedBranchSearchHit {
	t.Helper()

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %v", indexDir, err)
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

	hits := make([]combinedBranchSearchHit, 0, len(result.Files))
	for _, file := range result.Files {
		branches := append([]string(nil), file.Branches...)
		sort.Strings(branches)
		hits = append(hits, combinedBranchSearchHit{
			FileName: file.FileName,
			Branches: branches,
			Version:  file.Version,
			Content:  string(file.Content),
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

func combinedBranchFileNames(hits []combinedBranchSearchHit) []string {
	names := make([]string, 0, len(hits))
	for _, hit := range hits {
		names = append(names, hit.FileName)
	}
	sort.Strings(names)
	return names
}

func combinedBranchIndexedRepository(t *testing.T, indexDir string) *zoekt.Repository {
	t.Helper()

	opts := index.Options{
		IndexDir: indexDir,
		RepositoryDescription: zoekt.Repository{
			ID:   combinedBranchRepoID,
			Name: combinedBranchRepoName,
		},
	}
	repo, _, ok, err := opts.FindRepositoryMetadata()
	if err != nil {
		t.Fatalf("FindRepositoryMetadata(%s): %v", indexDir, err)
	}
	if !ok {
		t.Fatalf("FindRepositoryMetadata(%s): repository not found", indexDir)
	}
	return repo
}

func combinedBranchCheckoutWriteCommit(t *testing.T, repoDir, branch string, files combinedBranchFiles, message string) {
	t.Helper()

	combinedBranchRunGit(t, repoDir, "checkout", branch)
	combinedBranchWriteFiles(t, repoDir, files)
	combinedBranchRunGit(t, repoDir, "add", "-A")
	combinedBranchRunGit(t, repoDir, "commit", "-m", message)
}

func combinedBranchCheckoutNewBranchWriteCommit(t *testing.T, repoDir, branch, startPoint string, files combinedBranchFiles, message string) {
	t.Helper()

	combinedBranchRunGit(t, repoDir, "checkout", "-B", branch, startPoint)
	combinedBranchWriteFiles(t, repoDir, files)
	combinedBranchRunGit(t, repoDir, "add", "-A")
	combinedBranchRunGit(t, repoDir, "commit", "-m", message)
}

func combinedBranchWriteFiles(t *testing.T, repoDir string, files combinedBranchFiles) {
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

func combinedBranchRenameBranch(t *testing.T, repoDir, oldBranch, newBranch string) {
	t.Helper()
	combinedBranchRunGit(t, repoDir, "branch", "-m", oldBranch, newBranch)
}

func combinedBranchDeleteBranch(t *testing.T, repoDir, branch string) {
	t.Helper()
	combinedBranchRunGit(t, repoDir, "branch", "-D", branch)
}

func combinedBranchRunGit(t *testing.T, cwd string, args ...string) {
	t.Helper()

	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", cwd, err)
	}

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
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func combinedBranchNames(branches []zoekt.RepositoryBranch) []string {
	names := make([]string, 0, len(branches))
	for _, branch := range branches {
		names = append(names, branch.Name)
	}
	return names
}

func combinedBranchUnion(lists ...[]string) []string {
	seen := map[string]struct{}{}
	var union []string
	for _, list := range lists {
		for _, item := range list {
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			union = append(union, item)
		}
	}
	return union
}

func combinedBranchSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item] = true
	}
	return set
}

func combinedBranchContains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func combinedBranchMapKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
