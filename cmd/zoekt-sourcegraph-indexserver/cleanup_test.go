package main

import (
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
		name  string
		repos []string
		index []shard
		trash []shard
		tmps  []string

		wantIndex []shard
		wantTrash []shard
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

			var repoIDs []uint32
			for _, name := range tt.repos {
				repoIDs = append(repoIDs, fakeID(name))
			}
			cleanup(dir, repoIDs, now, false)

			if d := cmp.Diff(tt.wantIndex, glob(filepath.Join(dir, "*.zoekt"))); d != "" {
				t.Errorf("unexpected index (-want, +got):\n%s", d)
			}
			if d := cmp.Diff(tt.wantTrash, glob(filepath.Join(dir, ".trash", "*.zoekt"))); d != "" {
				t.Errorf("unexpected trash (-want, +got):\n%s", d)
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
