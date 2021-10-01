package main

import (
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
)

func TestCleanup(t *testing.T) {
	mk := func(name string, n int, mtime time.Time) shard {
		return shard{
			Repo:    name,
			Path:    fmt.Sprintf("%s_v%d.%05d.zoekt", url.QueryEscape(name), 15, n),
			ModTime: mtime,
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
			names, _ := shardRepoNames(path)
			fi, _ := os.Stat(path)
			for _, name := range names {
				shards = append(shards, shard{
					Repo:    name,
					Path:    filepath.Base(path),
					ModTime: fi.ModTime(),
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
		tmps  map[string]time.Time

		wantIndex []shard
		wantTrash []shard
		wantTmps  []string
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
		tmps: map[string]time.Time{
			"recent.tmp": recent,
			"old.tmp":    old,
		},
		wantTmps: []string{"recent.tmp"},
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
			dir, err := ioutil.TempDir("", "TestCleanup")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)

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
				createEmptyShard(t, f.Repo, f.Path)
				if err := os.Chtimes(f.Path, f.ModTime, f.ModTime); err != nil {
					t.Fatal(err)
				}
			}
			for name, mtime := range tt.tmps {
				path := filepath.Join(dir, name)
				if _, err := os.Create(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, mtime, mtime); err != nil {
					t.Fatal(err)
				}
			}

			cleanup(dir, tt.repos, now)

			if d := cmp.Diff(tt.wantIndex, glob(filepath.Join(dir, "*.zoekt"))); d != "" {
				t.Errorf("unexpected index (-want, +got):\n%s", d)
			}
			if d := cmp.Diff(tt.wantTrash, glob(filepath.Join(dir, ".trash", "*.zoekt"))); d != "" {
				t.Errorf("unexpected trash (-want, +got):\n%s", d)
			}
			if d := cmp.Diff(tt.wantTmps, globBase(filepath.Join(dir, "*.tmp"))); d != "" {
				t.Errorf("unexpected tmps (-want, +got):\n%s", d)
			}

			if testing.Verbose() {
				data, _ := ioutil.ReadFile(filepath.Join(dir, "zoekt-indexserver-shard-log.tsv"))
				if len(data) > 0 {
					t.Log("shard log contents:\n" + strings.TrimSpace(string(data)))
				}
			}
		})
	}
}

func createEmptyShard(t *testing.T, repo, path string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	b, err := zoekt.NewIndexBuilder(&zoekt.Repository{Name: repo})
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

	dir, err := ioutil.TempDir("", "TestRemoveIncompleteShards")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

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
	fn := createCompoundShard(t)

	err := zoekt.SetTombstone(fn, "repo2")
	if err != nil {
		t.Fatal(err)
	}

	got, err := removeTombstones(fn)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0] != "repo2" {
		t.Fatal(err)
	}
}

// createCompoundShard returns a path to a compound shard containing repos
// repo0..repo3
func createCompoundShard(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	repoNames := []string{"repo0", "repo1", "repo2", "repo3"}
	var repoFns []string

	for _, name := range repoNames {
		opts := build.Options{
			IndexDir: dir,
			RepositoryDescription: zoekt.Repository{
				Name: name,
				RawConfig: map[string]string{
					"public": "1",
				},
			},
		}
		opts.SetDefaults()
		b, err := build.NewBuilder(opts)
		if err != nil {
			t.Fatalf("NewBuilder: %v", err)
		}
		b.AddFile("F", []byte(strings.Repeat("abc", 100)))
		if err := b.Finish(); err != nil {
			t.Errorf("Finish: %v", err)
		}

		repoFns = append(repoFns, opts.FindAllShards()...)
	}

	// create a compound shard.
	dir = t.TempDir()
	fn, err := merge(dir, repoFns)
	if err != nil {
		t.Fatal(err)
	}
	return fn
}
