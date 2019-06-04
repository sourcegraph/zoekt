package main

import (
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/zoekt"
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
	readdir := func(dir string) []shard {
		paths, _ := filepath.Glob(filepath.Join(dir, "*"))
		sort.Strings(paths)
		var shards []shard
		for _, path := range paths {
			if filepath.Base(path) == ".trash" {
				continue
			}
			name, _ := shardRepoName(path)
			fi, _ := os.Stat(path)
			shards = append(shards, shard{
				Repo:    name,
				Path:    filepath.Base(path),
				ModTime: fi.ModTime(),
			})
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

			{
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

				cleanup(dir, tt.repos, now)

				if got, want := readdir(dir), tt.wantIndex; !reflect.DeepEqual(got, want) {
					t.Errorf("unexpected index\ngot:  %+v\nwant: %+v", got, want)
				}
				if got, want := readdir(filepath.Join(dir, ".trash")), tt.wantTrash; !reflect.DeepEqual(got, want) {
					t.Errorf("unexpected trash\ngot:  %+v\nwant: %+v", got, want)
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
