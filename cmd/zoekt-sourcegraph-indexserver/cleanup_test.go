package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/build"
)

func TestCleanup(t *testing.T) {

	mk := func(name string, n int, mtime time.Time) shard {
		return shard{
			RepoID:        fakeID(name),
			RepoName:      name,
			Path:          fmt.Sprintf("%s_v%d.%05d.zoekt", url.QueryEscape(name), 15, n),
			ModTime:       mtime,
			RepoTombstone: false,
		}
	}

	mkMeta := func(name string, n int) string {
		return fmt.Sprintf("%s_v%d.%05d.zoekt.meta", url.QueryEscape(name), 15, n)
	}

	// We don't use getShards so that we have two implementations of the same
	// thing (ie pick up bugs in one)
	glob := func(pattern string) []shard {
		paths, _ := filepath.Glob(pattern)
		sort.Strings(paths)
		var shards []shard
		for _, path := range paths {
			if filepath.Ext(path) != ".zoekt" {
				continue
			}
			repos, _, _ := zoekt.ReadMetadataPathAlive(path)
			fi, _ := os.Stat(path)
			for _, repo := range repos {
				shards = append(shards, shard{
					RepoID:   repo.ID,
					RepoName: repo.Name,
					Path:     filepath.Base(path),
					ModTime:  fi.ModTime(),
				})
			}
		}
		return shards
	}

	now := time.Now().Truncate(time.Second)
	recent := now.Add(-time.Hour)
	old := now.Add(-25 * time.Hour)
	cases := []struct {
		name           string
		repos          []string
		indexMetaFiles []string
		index          []shard
		trash          []shard
		tmps           []string

		wantIndex          []shard
		wantIndexMetaFiles []string
		wantTrash          []shard
	}{{
		name: "noop",
	}, {
		name:  "not indexed yet",
		repos: []string{"foo", "bar"},
	}, {
		name:      "just trash",
		trash:     []shard{mk("foo", 0, recent), mk("bar", 0, recent), mk("bar", 5, old)},
		wantTrash: []shard{mk("foo", 0, recent)},
	}, {
		name:      "single trash",
		repos:     []string{"foo"},
		index:     []shard{mk("foo", 0, old), mk("bar", 0, old), mk("bar", 1, old)},
		wantIndex: []shard{mk("foo", 0, old)},
		wantTrash: []shard{mk("bar", 0, now), mk("bar", 1, now)},
	}, {
		name:      "just index",
		repos:     []string{"foo"},
		index:     []shard{mk("foo", 0, old), mk("foo", 1, recent), mk("bar", 0, recent), mk("bar", 1, old)},
		wantIndex: []shard{mk("foo", 0, old), mk("foo", 1, recent)},
		wantTrash: []shard{mk("bar", 0, now), mk("bar", 1, now)},
	}, {
		name:      "future timestamp",
		trash:     []shard{mk("foo", 0, now.Add(time.Hour))},
		wantTrash: []shard{mk("foo", 0, now)},
	}, {
		name:      "conflict",
		repos:     []string{"foo"},
		trash:     []shard{mk("foo", 0, recent), mk("foo", 1, recent), mk("bar", 0, recent), mk("bar", 1, old)},
		index:     []shard{mk("foo", 0, recent), mk("bar", 0, recent)},
		wantIndex: []shard{mk("foo", 0, recent)},
		wantTrash: []shard{mk("bar", 0, now)},
	}, {
		name:               "remove metafiles with no associated shards",
		repos:              []string{"foo", "bar"},
		index:              []shard{mk("foo", 0, recent), mk("bar", 0, recent)},
		indexMetaFiles:     []string{mkMeta("foo", 0), mkMeta("foo", 1), mkMeta("bar", 0)},
		wantIndex:          []shard{mk("foo", 0, recent), mk("bar", 0, recent)},
		wantIndexMetaFiles: []string{mkMeta("foo", 0), mkMeta("bar", 0)},
	}, {
		name: "clean old .tmp files",
		tmps: []string{"recent.tmp", "old.tmp"},
	}, {
		name:      "all",
		repos:     []string{"exists", "trashed"},
		trash:     []shard{mk("trashed", 0, recent), mk("delete", 0, old)},
		index:     []shard{mk("exists", 0, recent), mk("trash", 0, recent)},
		wantIndex: []shard{mk("exists", 0, recent), mk("trashed", 0, recent)},
		wantTrash: []shard{mk("trash", 0, now)},
	}}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()

			// Create index files
			var fs []shard
			for _, f := range tt.index {
				f.Path = filepath.Join(dir, f.Path)
				fs = append(fs, f)
			}
			for _, f := range tt.trash {
				f.Path = filepath.Join(dir, ".trash", f.Path)
				fs = append(fs, f)
			}
			for _, f := range fs {
				createTestShard(t, f.RepoName, fakeID(f.RepoName), f.Path)
				if err := os.Chtimes(f.Path, f.ModTime, f.ModTime); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range tt.tmps {
				path := filepath.Join(dir, name)
				if _, err := os.Create(path); err != nil {
					t.Fatal(err)
				}
			}
			for _, f := range tt.indexMetaFiles {
				path := filepath.Join(dir, f)
				if _, err := os.Create(path); err != nil {
					t.Fatal(err)
				}
			}

			var repoIDs []uint32
			for _, name := range tt.repos {
				repoIDs = append(repoIDs, fakeID(name))
			}
			cleanup(dir, repoIDs, now, false)

			actualIndexShards := glob(filepath.Join(dir, "*.zoekt"))

			sort.Slice(actualIndexShards, func(i, j int) bool {
				return actualIndexShards[i].Path < actualIndexShards[j].Path
			})
			sort.Slice(tt.wantIndex, func(i, j int) bool {
				return tt.wantIndex[i].Path < tt.wantIndex[j].Path
			})

			if d := cmp.Diff(tt.wantIndex, actualIndexShards); d != "" {
				t.Errorf("unexpected index (-want, +got):\n%s", d)
			}

			actualTrashShards := glob(filepath.Join(dir, ".trash", "*.zoekt"))

			sort.Slice(actualTrashShards, func(i, j int) bool {
				return actualTrashShards[i].Path < actualTrashShards[j].Path
			})

			sort.Slice(tt.wantTrash, func(i, j int) bool {
				return tt.wantTrash[i].Path < tt.wantTrash[j].Path
			})
			if d := cmp.Diff(tt.wantTrash, actualTrashShards); d != "" {
				t.Errorf("unexpected trash (-want, +got):\n%s", d)
			}

			actualIndexMetaFiles := globBase(filepath.Join(dir, "*.meta"))

			sort.Strings(actualIndexMetaFiles)
			sort.Strings(tt.wantIndexMetaFiles)

			if d := cmp.Diff(tt.wantIndexMetaFiles, actualIndexMetaFiles, cmpopts.EquateEmpty()); d != "" {
				t.Errorf("unexpected metadata files (-want, +got):\n%s", d)
			}

			if tmps := globBase(filepath.Join(dir, "*.tmp")); len(tmps) > 0 {
				t.Errorf("unexpected tmps: %v", tmps)
			}

			if testing.Verbose() {
				data, _ := os.ReadFile(filepath.Join(dir, "zoekt-indexserver-shard-log.tsv"))
				if len(data) > 0 {
					t.Log("shard log contents:\n" + strings.TrimSpace(string(data)))
				}
			}
		})
	}
}

func createTestShard(t *testing.T, repo string, id uint32, path string, optFns ...func(in *zoekt.Repository)) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	r := &zoekt.Repository{
		ID:   id,
		Name: repo,
	}
	for _, optFn := range optFns {
		optFn(r)
	}
	b, err := zoekt.NewIndexBuilder(r)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := b.Write(f); err != nil {
		t.Fatal(err)
	}
}

func globBase(pattern string) []string {
	paths, _ := filepath.Glob(pattern)
	for i := range paths {
		paths[i] = filepath.Base(paths[i])
	}
	sort.Strings(paths)
	return paths
}

func TestRemoveIncompleteShards(t *testing.T) {
	shards, incomplete := []string{
		"test.zoekt",
		"foo.zoekt",
		"bar.zoekt",
		"bar.zoekt.meta",
	}, []string{
		"incomplete.zoekt123",
		"crash.zoekt567",
		"metacrash.zoekt789.meta",
	}
	sort.Strings(shards)

	dir := t.TempDir()

	for _, shard := range append(shards, incomplete...) {
		_, err := os.Create(filepath.Join(dir, shard))
		if err != nil {
			t.Fatal(err)
		}
	}
	removeIncompleteShards(dir)

	left, _ := filepath.Glob(filepath.Join(dir, "*"))
	sort.Strings(left)
	for i := range left {
		left[i] = filepath.Base(left[i])
	}

	if !reflect.DeepEqual(shards, left) {
		t.Errorf("\ngot shards: %v\nwant: %v\n", left, shards)
	}
}

func TestVacuum(t *testing.T) {
	tmpDir := t.TempDir()
	fn := createCompoundShard(t, tmpDir, []uint32{1, 2, 3, 4})

	err := zoekt.SetTombstone(fn, 2)
	if err != nil {
		t.Fatal(err)
	}

	mockMerger = func() error { return mergeHelper(t, fn) }
	got, err := removeTombstones(fn)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].Name != "repo2" {
		t.Fatal(err)
	}

	shards, err := filepath.Glob(tmpDir + "/compound-*")
	if err != nil {
		t.Fatal(err)
	}

	if len(shards) != 1 {
		t.Fatalf("expected 1 shard, but instead got %d", len(shards))
	}

	repos, _, err := zoekt.ReadMetadataPath(shards[0])
	if err != nil {
		t.Fatal(err)
	}

	if len(repos) != 3 {
		t.Fatalf("wanted 3, got %d repos", len(repos))
	}

	for _, r := range repos {
		if r.Tombstone {
			t.Fatalf("found tombstone for %s", r.Name)
		}
	}
}

// Create 2 compound shards, each of which contains the same tombstoned repo but
// from different commit dates.
func TestGetTombstonedRepos(t *testing.T) {
	setLastCommitDate := func(lastCommitDate time.Time) func(repository *zoekt.Repository) {
		return func(repository *zoekt.Repository) {
			repository.LatestCommitDate = lastCommitDate
		}
	}

	dir := t.TempDir()
	var repoID uint32 = 2
	csOld := createCompoundShard(t, dir, []uint32{1, 2, 3, 4}, setLastCommitDate(time.Now().Add(-1*time.Hour)))
	if err := zoekt.SetTombstone(csOld, repoID); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	csNew := createCompoundShard(t, dir, []uint32{5, 2, 6, 7}, setLastCommitDate(now))
	if err := zoekt.SetTombstone(csNew, repoID); err != nil {
		t.Fatal(err)
	}

	// Check that getTombstonedRepos returns the compound shard containing the
	// tombstoned repo with id repoID with the latest commit.
	got := getTombstonedRepos(dir)

	if len(got) != 1 {
		t.Fatalf("want 1 shard, got %d shards", len(got))
	}

	v, ok := got[repoID]
	if !ok || v.Path != csNew {
		t.Fatalf("want %s, got %s", csNew, v.Path)
	}
}

// HAVE
// ----
// index/
// CS 1
//
//	r1, tombstoned, old
//	r2, tombstoned, old
//	r3, tombstoned, old
//
// CS 2
//
//	r1, tombstoned, recent
//	r2, tombstoned, recent
//	r4, tombstoned, recent
//
// SS 1
//
//	r1, now
//
// .trash/
// SS 3
//
//	r3, now
//
// SS 5
//
//	r5, now
//
// TO BE INDEXED
// -------------
// repos r1, r2, r3, r4, r5
//
// WANT
// ----
// index/
// CS 1
//
//	r1, tombstoned, old
//	r2, tombstoned, old
//	r3, tombstoned, old
//
// CS 2
//
//	r1, tombstoned, recent
//	r2, recent
//	r4, recent
//
// SS 1
//
//	r1, now
//
// SS 3
//
//	r3, now
//
// SS 5
//
//	r5, now
//
// .trash/ --> empty
func TestCleanupCompoundShards(t *testing.T) {
	dir := t.TempDir()

	// timestamps
	now := time.Now()
	recent := now.Add(-1 * time.Hour)
	old := now.Add(-2 * time.Hour)

	setTombstone := func(shardPath string, repoID uint32) {
		t.Helper()
		if err := zoekt.SetTombstone(shardPath, repoID); err != nil {
			t.Fatal(err)
		}
	}

	cs1 := createCompoundShard(t, dir, []uint32{1, 2, 3}, func(in *zoekt.Repository) {
		in.LatestCommitDate = old
	})
	setTombstone(cs1, 1)
	setTombstone(cs1, 2)
	setTombstone(cs1, 3)

	cs2 := createCompoundShard(t, dir, []uint32{1, 2, 4}, func(in *zoekt.Repository) {
		in.LatestCommitDate = recent
	})
	setTombstone(cs2, 1)
	setTombstone(cs2, 2)
	setTombstone(cs2, 4)

	createTestShard(t, "repo1", 1, filepath.Join(dir, "repo1.zoekt"), func(in *zoekt.Repository) {
		in.LatestCommitDate = now
	})
	createTestShard(t, "repo3", 3, filepath.Join(dir, ".trash", "repo3.zoekt"), func(in *zoekt.Repository) {
		in.LatestCommitDate = now
	})
	createTestShard(t, "repo5", 5, filepath.Join(dir, ".trash", "repo5.zoekt"), func(in *zoekt.Repository) {
		in.LatestCommitDate = now
	})

	// want indexed
	repos := []uint32{1, 2, 3, 4, 5}

	cleanup(dir, repos, now, true)

	index := getShards(dir)
	trash := getShards(filepath.Join(dir, ".trash"))

	if len(trash) != 0 {
		t.Fatalf("expected empty trash, got %+v", trash)
	}

	wantIndex := map[uint32][]shard{
		1: {{
			RepoID:   1,
			RepoName: "repo1",
			Path:     filepath.Join(dir, "repo1.zoekt"),
		}},
		2: {{
			RepoID:   2,
			RepoName: "repo2",
			Path:     cs2,
		}},
		3: {{
			RepoID:   3,
			RepoName: "repo3",
			Path:     filepath.Join(dir, "repo3.zoekt"),
		}},
		4: {{
			RepoID:   4,
			RepoName: "repo4",
			Path:     cs2,
		}},
		5: {{
			RepoID:   5,
			RepoName: "repo5",
			Path:     filepath.Join(dir, "repo5.zoekt"),
		}},
	}

	if d := cmp.Diff(wantIndex, index, cmpopts.IgnoreFields(shard{}, "ModTime")); d != "" {
		t.Fatalf("-want, +got: %s", d)
	}
}

func TestDeleteShards(t *testing.T) {
	remainingRepoA := zoekt.Repository{ID: 1, Name: "A"}
	remainingRepoB := zoekt.Repository{ID: 2, Name: "B"}
	repositoryToDelete := zoekt.Repository{ID: 99, Name: "DELETE_ME"}

	t.Run("delete repository from set of normal shards", func(t *testing.T) {
		indexDir := t.TempDir()

		// map of repoID -> list of associated shard paths + metadata paths
		shardMap := make(map[uint32][]string)

		// setup: create shards for each repository, and populate the shard map
		for _, r := range []zoekt.Repository{
			remainingRepoA,
			remainingRepoB,
			repositoryToDelete,
		} {
			shards := createTestNormalShard(t, indexDir, r, 3)

			for _, shard := range shards {
				// create stub meta file
				metaFile := shard + ".meta"
				f, err := os.Create(metaFile)
				if err != nil {
					t.Fatalf("creating metadata file %q: %s", metaFile, err)
				}

				f.Close()

				shardMap[r.ID] = append(shardMap[r.ID], shard, metaFile)
			}
		}

		// run test: delete repository
		err := deleteShards(indexDir, repositoryToDelete.ID)
		if err != nil {
			t.Errorf("unexpected error when deleting shards: %s", err)
		}

		// run assertions: gather all the shards + meta files that remain and
		// check to see that only the files associated with the "remaining" repositories
		// are present
		var actualShardFiles []string

		for _, pattern := range []string{"*.zoekt", "*.meta"} {
			files, err := filepath.Glob(filepath.Join(indexDir, pattern))
			if err != nil {
				t.Fatalf("globbing indexDir: %s", err)
			}

			actualShardFiles = append(actualShardFiles, files...)
		}

		var expectedShardFiles []string
		expectedShardFiles = append(expectedShardFiles, shardMap[remainingRepoA.ID]...)
		expectedShardFiles = append(expectedShardFiles, shardMap[remainingRepoB.ID]...)

		sort.Strings(actualShardFiles)
		sort.Strings(expectedShardFiles)

		if diff := cmp.Diff(expectedShardFiles, actualShardFiles); diff != "" {
			t.Errorf("unexpected diff in list of shard files (-want +got):\n%s", diff)
		}
	})

	t.Run("delete repository from compound shard", func(t *testing.T) {
		indexDir := t.TempDir()

		// setup: enable shard merging for compound shards
		t.Setenv("SRC_ENABLE_SHARD_MERGING", "1")

		// setup: create compound shard with all repositories
		repositories := []zoekt.Repository{remainingRepoA, remainingRepoB, repositoryToDelete}
		shard := createTestCompoundShard(t, indexDir, repositories)

		err := deleteShards(indexDir, repositoryToDelete.ID)
		if err != nil {
			t.Errorf("unexpected error when deleting shards: %s", err)
		}

		// verify: read the compound shard, and ensure that only
		// the repositories that we expect are in the shard (and the deleted one has been tombstoned)
		actualRepositories, _, err := zoekt.ReadMetadataPathAlive(shard)
		if err != nil {
			t.Fatalf("reading repository metadata from shard: %s", err)
		}

		expectedRepositories := []*zoekt.Repository{&remainingRepoA, &remainingRepoB}

		sort.Slice(actualRepositories, func(i, j int) bool {
			return actualRepositories[i].ID < actualRepositories[j].ID
		})

		sort.Slice(expectedRepositories, func(i, j int) bool {
			return expectedRepositories[i].ID < expectedRepositories[j].ID
		})

		opts := []cmp.Option{
			cmpopts.IgnoreUnexported(zoekt.Repository{}),
			cmpopts.IgnoreFields(zoekt.Repository{}, "IndexOptions", "HasSymbols"),
			cmpopts.EquateEmpty(),
		}
		if diff := cmp.Diff(expectedRepositories, actualRepositories, opts...); diff != "" {
			t.Errorf("unexpected diff in list of repositories (-want +got):\n%s", diff)
		}
	})

	t.Run("returns errRepositoryNotFound if the repoID isn't in indexDir", func(t *testing.T) {
		indexDir := t.TempDir()

		// setup: create compound shard with all repositories
		repositories := []zoekt.Repository{remainingRepoA, remainingRepoB, repositoryToDelete}
		for _, r := range repositories {
			createTestNormalShard(t, indexDir, r, 3)
		}

		// test: delete some random repository and check to see if we get the expected error
		err := deleteShards(indexDir, 7777777)
		if !errors.Is(err, errRepositoryNotFound) {
			t.Errorf("expected errRepositoryNotFound when deleting shards, got: %s", err)
		}
	})
}

// createCompoundShard returns a path to a compound shard containing repos with
// ids. Use optsFns to overwrite fields of zoekt.Repository for all repos.
func createCompoundShard(t *testing.T, dir string, ids []uint32, optFns ...func(in *zoekt.Repository)) string {
	t.Helper()

	var repoFns []string

	for _, id := range ids {
		repo := zoekt.Repository{
			ID:   id,
			Name: fmt.Sprintf("repo%d", id),
			RawConfig: map[string]string{
				"public": "1",
			},
		}
		for _, optsFn := range optFns {
			optsFn(&repo)
		}

		opts := build.Options{
			IndexDir:              dir,
			RepositoryDescription: repo,
		}
		opts.SetDefaults()
		b, err := build.NewBuilder(opts)
		if err != nil {
			t.Fatalf("NewBuilder: %v", err)
		}
		if err := b.AddFile("F", []byte(strings.Repeat("abc", 100))); err != nil {
			t.Errorf("AddFile: %v", err)
		}
		if err := b.Finish(); err != nil {
			t.Errorf("Finish: %v", err)
		}

		repoFns = append(repoFns, opts.FindAllShards()...)
	}

	// create a compound shard.
	tmpFn, dstFn, err := merge(t, dir, repoFns)
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range repoFns {
		if err := os.Remove(old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(tmpFn, dstFn); err != nil {
		t.Fatal(err)
	}
	return dstFn
}

func mergeHelper(t *testing.T, fn string) error {
	t.Helper()

	f, err := os.Open(fn)
	if err != nil {
		return fmt.Errorf("os.Open: %s", err)
	}
	defer f.Close()

	indexFile, err := zoekt.NewIndexFile(f)
	if err != nil {
		return fmt.Errorf("zoekt.NewIndexFile: %s ", err)
	}
	defer indexFile.Close()

	_, _, err = zoekt.Merge(filepath.Dir(fn), indexFile)
	return err
}
