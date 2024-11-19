// Copyright 2016 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package build

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/grafana/regexp"
	"github.com/stretchr/testify/require"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/internal/tenant"
	"github.com/sourcegraph/zoekt/internal/tenant/tenanttest"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/shards"
)

func TestBasic(t *testing.T) {
	dir := t.TempDir()

	opts := Options{
		IndexDir: dir,
		ShardMax: 1024,
		RepositoryDescription: zoekt.Repository{
			Name: "repo",
		},
		Parallelism: 2,
		SizeMax:     1 << 20,
	}

	b, err := NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	for i := 0; i < 4; i++ {
		s := fmt.Sprintf("%d", i)
		if err := b.AddFile("F"+s, []byte(strings.Repeat(s, 1000))); err != nil {
			t.Fatal(err)
		}
	}

	if err := b.Finish(); err != nil {
		t.Errorf("Finish: %v", err)
	}

	fs, _ := filepath.Glob(dir + "/*.zoekt")
	if len(fs) <= 1 {
		t.Fatalf("want multiple shards, got %v", fs)
	}

	_, md0, err := zoekt.ReadMetadataPath(fs[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs[1:] {
		_, md, err := zoekt.ReadMetadataPath(f)
		if err != nil {
			t.Fatal(err)
		}
		if md.IndexTime != md0.IndexTime {
			t.Fatalf("wanted identical time stamps but got %v!=%v", md.IndexTime, md0.IndexTime)
		}
		if md.ID != md0.ID {
			t.Fatalf("wanted identical IDs but got %s!=%s", md.ID, md0.ID)
		}
	}

	ss, err := shards.NewDirectorySearcher(dir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %v", dir, err)
	}
	defer ss.Close()

	q, err := query.Parse("111")
	if err != nil {
		t.Fatalf("Parse(111): %v", err)
	}

	var sOpts zoekt.SearchOptions
	ctx := context.Background()
	result, err := ss.Search(ctx, q, &sOpts)
	if err != nil {
		t.Fatalf("Search(%v): %v", q, err)
	}

	if len(result.Files) != 1 {
		t.Errorf("got %v, want 1 file.", result.Files)
	} else if gotFile, wantFile := result.Files[0].FileName, "F1"; gotFile != wantFile {
		t.Errorf("got file %q, want %q", gotFile, wantFile)
	} else if gotRepo, wantRepo := result.Files[0].Repository, "repo"; gotRepo != wantRepo {
		t.Errorf("got repo %q, want %q", gotRepo, wantRepo)
	}

	t.Run("meta file", func(t *testing.T) {
		// use retryTest to allow for the directory watcher to notice the meta
		// file
		retryTest(t, func(fatalf func(format string, args ...interface{})) {
			// Add a .meta file for each shard with repo.Name set to
			// "repo-mutated". We do this inside retry helper since we have noticed
			// some flakiness on github CI.
			for _, p := range fs {
				repos, _, err := zoekt.ReadMetadataPath(p)
				if err != nil {
					t.Fatal(err)
				}
				repos[0].Name = "repo-mutated"
				b, err := json.Marshal(repos[0])
				if err != nil {
					t.Fatal(err)
				}

				if err := os.WriteFile(p+".meta", b, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			result, err := ss.Search(ctx, q, &sOpts)
			if err != nil {
				fatalf("Search(%v): %v", q, err)
			}

			if len(result.Files) != 1 {
				fatalf("got %v, want 1 file.", result.Files)
			} else if gotFile, wantFile := result.Files[0].FileName, "F1"; gotFile != wantFile {
				fatalf("got file %q, want %q", gotFile, wantFile)
			} else if gotRepo, wantRepo := result.Files[0].Repository, "repo-mutated"; gotRepo != wantRepo {
				fatalf("got repo %q, want %q", gotRepo, wantRepo)
			}
		})
	})
}

func TestSearchTenant(t *testing.T) {
	tenanttest.MockEnforce(t)

	dir := t.TempDir()

	ctx1 := tenanttest.NewTestContext()
	tnt1, err := tenant.FromContext(ctx1)
	require.NoError(t, err)

	opts := Options{
		IndexDir: dir,
		ShardMax: 1024,
		RepositoryDescription: zoekt.Repository{
			Name:      "repo",
			RawConfig: map[string]string{"tenantID": strconv.Itoa(tnt1.ID())},
		},
		Parallelism: 2,
		SizeMax:     1 << 20,
	}

	b, err := NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	for i := 0; i < 4; i++ {
		s := fmt.Sprintf("%d", i)
		if err := b.AddFile("F"+s, []byte(strings.Repeat(s, 1000))); err != nil {
			t.Fatal(err)
		}
	}

	if err := b.Finish(); err != nil {
		t.Errorf("Finish: %v", err)
	}

	fs, _ := filepath.Glob(dir + "/*.zoekt")
	if len(fs) <= 1 {
		t.Fatalf("want multiple shards, got %v", fs)
	}

	_, md0, err := zoekt.ReadMetadataPath(fs[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs[1:] {
		_, md, err := zoekt.ReadMetadataPath(f)
		if err != nil {
			t.Fatal(err)
		}
		if md.IndexTime != md0.IndexTime {
			t.Fatalf("wanted identical time stamps but got %v!=%v", md.IndexTime, md0.IndexTime)
		}
		if md.ID != md0.ID {
			t.Fatalf("wanted identical IDs but got %s!=%s", md.ID, md0.ID)
		}
	}

	ss, err := shards.NewDirectorySearcher(dir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %v", dir, err)
	}
	defer ss.Close()

	q, err := query.Parse("111")
	if err != nil {
		t.Fatalf("Parse(111): %v", err)
	}

	var sOpts zoekt.SearchOptions

	// Tenant 1 has access to the repo
	result, err := ss.Search(ctx1, q, &sOpts)
	require.NoError(t, err)
	require.Len(t, result.Files, 1)

	// Tenant 2 does not have access to the repo
	ctx2 := tenanttest.NewTestContext()
	result, err = ss.Search(ctx2, q, &sOpts)
	require.NoError(t, err)
	require.Len(t, result.Files, 0)
}

func TestListTenant(t *testing.T) {
	tenanttest.MockEnforce(t)

	dir := t.TempDir()

	ctx1 := tenanttest.NewTestContext()
	tnt1, err := tenant.FromContext(ctx1)
	require.NoError(t, err)

	opts := Options{
		IndexDir: dir,
		RepositoryDescription: zoekt.Repository{
			Name:      "repo",
			RawConfig: map[string]string{"tenantID": strconv.Itoa(tnt1.ID())},
		},
	}
	opts.SetDefaults()

	b, err := NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	if err := b.Finish(); err != nil {
		t.Errorf("Finish: %v", err)
	}

	fs, _ := filepath.Glob(dir + "/*.zoekt")
	if len(fs) != 1 {
		t.Fatalf("want a shard, got %v", fs)
	}

	ss, err := shards.NewDirectorySearcher(dir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %v", dir, err)
	}
	defer ss.Close()

	// Tenant 1 has access to the repo
	result, err := ss.List(ctx1, &query.Const{Value: true}, nil)
	require.NoError(t, err)
	require.Len(t, result.Repos, 1)

	// Tenant 2 does not have access to the repo
	ctx2 := tenanttest.NewTestContext()
	result, err = ss.List(ctx2, &query.Const{Value: true}, nil)
	require.NoError(t, err)
	require.Len(t, result.Repos, 0)
}

// retryTest will retry f until min(t.Deadline(), time.Minute). It returns
// once f doesn't call fatalf.
func retryTest(t *testing.T, f func(fatalf func(format string, args ...interface{}))) {
	t.Helper()

	sleep := 10 * time.Millisecond
	deadline := time.Now().Add(time.Minute)
	if d, ok := t.Deadline(); ok && d.Before(deadline) {
		// give 1s for us to do a final test run
		deadline = d.Add(-time.Second)
	}

	for {
		done := make(chan bool)
		go func() {
			defer close(done)

			f(func(format string, args ...interface{}) {
				runtime.Goexit()
			})

			done <- true
		}()

		success := <-done
		if success {
			return
		}

		// each time we increase sleep by 1.5
		sleep := sleep*2 - sleep/2
		if time.Now().Add(sleep).After(deadline) {
			break
		}
		time.Sleep(sleep)
	}

	// final run for the test, using the real t.Fatalf
	f(t.Fatalf)
}

func TestLargeFileOption(t *testing.T) {
	dir := t.TempDir()

	sizeMax := 1000
	opts := Options{
		IndexDir:   dir,
		LargeFiles: []string{"F0", "F1", "F2", "!F1"},
		RepositoryDescription: zoekt.Repository{
			Name: "repo",
		},
		SizeMax: sizeMax,
	}

	b, err := NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	for i := 0; i < 4; i++ {
		s := fmt.Sprintf("%d", i)
		if err := b.AddFile("F"+s, []byte(strings.Repeat("a", sizeMax+1))); err != nil {
			t.Fatal(err)
		}
	}

	if err := b.Finish(); err != nil {
		t.Errorf("Finish: %v", err)
	}

	ss, err := shards.NewDirectorySearcher(dir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %v", dir, err)
	}

	q, err := query.Parse("aaa")
	if err != nil {
		t.Fatalf("Parse(aaa): %v", err)
	}

	var sOpts zoekt.SearchOptions
	ctx := context.Background()
	result, err := ss.Search(ctx, q, &sOpts)
	if err != nil {
		t.Fatalf("Search(%v): %v", q, err)
	}

	if len(result.Files) != 2 {
		t.Errorf("got %v files, want 2 files.", len(result.Files))
	}
	defer ss.Close()
}

func TestUpdate(t *testing.T) {
	dir := t.TempDir()

	opts := Options{
		IndexDir: dir,
		ShardMax: 1024,
		RepositoryDescription: zoekt.Repository{
			Name:            "repo",
			FileURLTemplate: "url",
		},
		Parallelism: 2,
		SizeMax:     1 << 20,
	}

	if b, err := NewBuilder(opts); err != nil {
		t.Fatalf("NewBuilder: %v", err)
	} else {
		if err := b.AddFile("F", []byte("hoi")); err != nil {
			t.Errorf("AddFile: %v", err)
		}
		if err := b.Finish(); err != nil {
			t.Errorf("Finish: %v", err)
		}
	}
	ss, err := shards.NewDirectorySearcher(dir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %v", dir, err)
	}

	ctx := context.Background()
	repos, err := ss.List(ctx, &query.Repo{Regexp: regexp.MustCompile("repo")}, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(repos.Repos) != 1 {
		t.Errorf("List(repo): got %v, want 1 repo", repos.Repos)
	}

	fs, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	opts.RepositoryDescription = zoekt.Repository{
		Name:            "repo2",
		FileURLTemplate: "url2",
	}

	if b, err := NewBuilder(opts); err != nil {
		t.Fatalf("NewBuilder: %v", err)
	} else {
		if err := b.AddFile("F", []byte("hoi")); err != nil {
			t.Errorf("AddFile: %v", err)
		}
		if err := b.Finish(); err != nil {
			t.Errorf("Finish: %v", err)
		}
	}

	// This is ugly, and potentially flaky, but there is no
	// observable synchronization for the Sharded searcher, so
	// this is the best we can do.
	time.Sleep(100 * time.Millisecond)

	ctx = context.Background()
	if repos, err = ss.List(ctx, &query.Repo{Regexp: regexp.MustCompile("repo")}, nil); err != nil {
		t.Fatalf("List: %v", err)
	} else if len(repos.Repos) != 2 {
		t.Errorf("List(repo): got %v, want 2 repos", repos.Repos)
	}

	for _, fn := range fs {
		log.Printf("removing %s", fn)
		if err := os.Remove(fn); err != nil {
			t.Fatalf("Remove(%s): %v", fn, err)
		}
	}

	time.Sleep(100 * time.Millisecond)

	ctx = context.Background()
	if repos, err = ss.List(ctx, &query.Repo{Regexp: regexp.MustCompile("repo")}, nil); err != nil {
		t.Fatalf("List: %v", err)
	} else if len(repos.Repos) != 1 {
		var ss []string
		for _, r := range repos.Repos {
			ss = append(ss, r.Repository.Name)
		}
		t.Errorf("List(repo): got %v, want 1 repo", ss)
	}
}

func TestDeleteOldShards(t *testing.T) {
	dir := t.TempDir()

	opts := Options{
		IndexDir: dir,
		ShardMax: 1024,
		RepositoryDescription: zoekt.Repository{
			Name:            "repo",
			FileURLTemplate: "url",
		},
		SizeMax: 1 << 20,
	}
	opts.SetDefaults()

	b, err := NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	for i := 0; i < 4; i++ {
		s := fmt.Sprintf("%d\n", i)
		if err := b.AddFile("F"+s, []byte(strings.Repeat(s, 1024/2))); err != nil {
			t.Errorf("AddFile: %v", err)
		}
	}
	if err := b.Finish(); err != nil {
		t.Errorf("Finish: %v", err)
	}

	glob := filepath.Join(dir, "*.zoekt")
	fs, err := filepath.Glob(glob)
	if err != nil {
		t.Fatalf("Glob(%s): %v", glob, err)
	} else if len(fs) != 4 {
		t.Fatalf("Glob(%s): got %v, want 4 shards", glob, fs)
	}

	if fi, err := os.Lstat(fs[0]); err != nil {
		t.Fatalf("Lstat: %v", err)
	} else if fi.Mode()&0o666 == 0o600 {
		// This fails spuriously if your umask is very restrictive.
		t.Errorf("got mode %o, should respect umask.", fi.Mode())
	}

	// Do again, without sharding.
	opts.ShardMax = 1 << 20
	b, err = NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	for i := 0; i < 4; i++ {
		s := fmt.Sprintf("%d\n", i)
		if err := b.AddFile("F"+s, []byte(strings.Repeat(s, 1024/2))); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Finish(); err != nil {
		t.Errorf("Finish: %v", err)
	}

	fs, err = filepath.Glob(glob)
	if err != nil {
		t.Fatalf("Glob(%s): %v", glob, err)
	} else if len(fs) != 1 {
		t.Fatalf("Glob(%s): got %v, want 1 shard", glob, fs)
	}

	// Again, but don't index anything; should leave old shards intact.
	b, err = NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	if err := b.Finish(); err != nil {
		t.Errorf("Finish: %v", err)
	}

	fs, err = filepath.Glob(glob)
	if err != nil {
		t.Fatalf("Glob(%s): %v", glob, err)
	} else if len(fs) != 1 {
		t.Fatalf("Glob(%s): got %v, want 1 shard", glob, fs)
	}
}

func TestPartialSuccess(t *testing.T) {
	dir := t.TempDir()

	opts := Options{
		IndexDir:    dir,
		ShardMax:    1024,
		SizeMax:     1 << 20,
		Parallelism: 1,
	}
	opts.RepositoryDescription.Name = "repo"
	opts.SetDefaults()

	b, err := NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	for i := 0; i < 4; i++ {
		nm := fmt.Sprintf("F%d", i)
		_ = b.AddFile(nm, []byte(strings.Repeat("01234567\n", 128)))
	}
	b.buildError = fmt.Errorf("any error")

	// No error checking.
	_ = b.Finish()

	// Finish cleans up temporary files.
	if fs, err := filepath.Glob(dir + "/*"); err != nil {
		t.Errorf("glob(%s): %v", dir, err)
	} else if len(fs) != 0 {
		t.Errorf("got shards %v, want []", fs)
	}
}

type filerankCase struct {
	name string
	docs []*zoekt.Document
	want []int
}

func testFileRankAspect(t *testing.T, c filerankCase) {
	var want []*zoekt.Document
	for _, j := range c.want {
		want = append(want, c.docs[j])
	}

	got := make([]*zoekt.Document, len(c.docs))
	copy(got, c.docs)
	sortDocuments(got)

	print := func(ds []*zoekt.Document) string {
		r := ""
		for _, d := range ds {
			r += fmt.Sprintf("%v, ", d)
		}
		return r
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got docs [%v], want [%v]", print(got), print(want))
	}
}

func TestFileRank(t *testing.T) {
	for _, c := range []filerankCase{{
		name: "filename",
		docs: []*zoekt.Document{
			{
				Name:    "longlonglong",
				Content: []byte("bla"),
			},
			{
				Name:    "short",
				Content: []byte("bla"),
			},
		},
		want: []int{1, 0},
	}, {
		name: "test",
		docs: []*zoekt.Document{
			{
				Name:    "foo_test.go",
				Content: []byte("bla"),
			},
			{
				Name:    "longlonglong",
				Content: []byte("bla"),
			},
		},
		want: []int{1, 0},
	}, {
		name: "content",
		docs: []*zoekt.Document{
			{
				Content: []byte("bla"),
			},
			{
				Content: []byte("blablablabla"),
			},
			{
				Content: []byte("blabla"),
			},
		},
		want: []int{0, 2, 1},
	}, {
		name: "skipped docs",
		docs: []*zoekt.Document{
			{
				Name:       "binary_file",
				SkipReason: "binary file",
			},
			{
				Name:    "some_test.go",
				Content: []byte("bla"),
			},
			{
				Name:       "large_file.go",
				SkipReason: "too large",
			},
			{
				Name:    "file.go",
				Content: []byte("blabla"),
			},
		},
		want: []int{3, 1, 0, 2},
	}} {
		t.Run(c.name, func(t *testing.T) {
			testFileRankAspect(t, c)
		})
	}
}

func TestEmptyContent(t *testing.T) {
	dir := t.TempDir()

	opts := Options{
		IndexDir: dir,
		RepositoryDescription: zoekt.Repository{
			Name: "repo",
		},
	}
	opts.SetDefaults()

	b, err := NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	if err := b.Finish(); err != nil {
		t.Errorf("Finish: %v", err)
	}

	fs, _ := filepath.Glob(dir + "/*.zoekt")
	if len(fs) != 1 {
		t.Fatalf("want a shard, got %v", fs)
	}

	ss, err := shards.NewDirectorySearcher(dir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %v", dir, err)
	}
	defer ss.Close()

	ctx := context.Background()
	result, err := ss.List(ctx, &query.Const{Value: true}, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(result.Repos) != 1 || result.Repos[0].Repository.Name != "repo" {
		t.Errorf("got %+v, want 1 repo.", result.Repos)
	}
}

func TestDeltaShards(t *testing.T) {
	// TODO: Need to write a test for compound shards as well.
	type step struct {
		name      string
		documents []zoekt.Document
		optFn     func(t *testing.T, o *Options)

		query             string
		expectedDocuments []zoekt.Document
	}

	var (
		fooAtMain   = zoekt.Document{Name: "foo.go", Branches: []string{"main"}, Content: []byte("common foo-main-v1")}
		fooAtMainV2 = zoekt.Document{Name: "foo.go", Branches: []string{"main"}, Content: []byte("common foo-main-v2")}

		fooAtMainAndRelease = zoekt.Document{Name: "foo.go", Branches: []string{"main", "release"}, Content: []byte("common foo-main-and-release")}

		barAtMain   = zoekt.Document{Name: "bar.go", Branches: []string{"main"}, Content: []byte("common bar-main")}
		barAtMainV2 = zoekt.Document{Name: "bar.go", Branches: []string{"main"}, Content: []byte("common bar-main-v2")}
	)

	for _, test := range []struct {
		name  string
		steps []step
	}{
		{
			name: "tombstone older documents",
			steps: []step{
				{
					name:              "setup",
					documents:         []zoekt.Document{barAtMain, fooAtMain},
					query:             "common",
					expectedDocuments: []zoekt.Document{barAtMain, fooAtMain},
				},
				{
					name:      "add new version of foo, tombstone older ones",
					documents: []zoekt.Document{fooAtMainV2},
					optFn: func(t *testing.T, o *Options) {
						o.IsDelta = true
						o.changedOrRemovedFiles = []string{"foo.go"}
					},
					query:             "common",
					expectedDocuments: []zoekt.Document{barAtMain, fooAtMainV2},
				},
				{
					name:      "add new version of bar, tombstone older ones",
					documents: []zoekt.Document{barAtMainV2},
					optFn: func(t *testing.T, o *Options) {
						o.IsDelta = true
						o.changedOrRemovedFiles = []string{"bar.go"}
					},
					query:             "common",
					expectedDocuments: []zoekt.Document{barAtMainV2, fooAtMainV2},
				},
			},
		},
		{
			name: "tombstone older documents even if the latest shard has no documents",
			steps: []step{
				{
					name:              "setup",
					documents:         []zoekt.Document{barAtMain, fooAtMain},
					query:             "common",
					expectedDocuments: []zoekt.Document{barAtMain, fooAtMain},
				},
				{
					// a build with no documents could represent a deletion
					name:      "tombstone older documents",
					documents: nil,
					optFn: func(t *testing.T, o *Options) {
						o.IsDelta = true
						o.changedOrRemovedFiles = []string{"foo.go"}
					},
					query:             "common",
					expectedDocuments: []zoekt.Document{barAtMain},
				},
			},
		},
		{
			name: "tombstones affect document across branches",
			steps: []step{
				{
					name:              "setup",
					documents:         []zoekt.Document{barAtMain, fooAtMainAndRelease},
					query:             "common",
					expectedDocuments: []zoekt.Document{barAtMain, fooAtMainAndRelease},
				},
				{
					name:      "tombstone foo",
					documents: nil,
					optFn: func(t *testing.T, o *Options) {
						o.IsDelta = true
						o.changedOrRemovedFiles = []string{"foo.go"}
					},
					query:             "common",
					expectedDocuments: []zoekt.Document{barAtMain},
				},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			indexDir := t.TempDir()

			branchSet := make(map[string]struct{})

			for _, s := range test.steps {
				for _, d := range s.documents {
					for _, b := range d.Branches {
						branchSet[b] = struct{}{}
					}
				}
			}

			for _, step := range test.steps {
				repository := zoekt.Repository{ID: 1, Name: "repository"}

				for b := range branchSet {
					repository.Branches = append(repository.Branches, zoekt.RepositoryBranch{Name: b})
				}

				sort.Slice(repository.Branches, func(i, j int) bool {
					a, b := repository.Branches[i], repository.Branches[j]

					return a.Name < b.Name
				})

				buildOpts := Options{
					IndexDir:              indexDir,
					RepositoryDescription: repository,
				}
				buildOpts.SetDefaults()

				if step.optFn != nil {
					step.optFn(t, &buildOpts)
				}

				b, err := NewBuilder(buildOpts)
				if err != nil {
					t.Fatalf("step %q: NewBuilder: %s", step.name, err)
				}

				for _, d := range step.documents {
					err := b.Add(d)
					if err != nil {
						t.Fatalf("step %q: adding document %q to builder: %s", step.name, d.Name, err)
					}
				}

				// Call b.Finish() multiple times to ensure that it is idempotent
				for i := 0; i < 3; i++ {

					err = b.Finish()
					if err != nil {
						t.Fatalf("step %q: finishing builder (call #%d): %s", step.name, i, err)
					}
				}

				err = b.Finish()
				if err != nil {
					t.Fatalf("step %q: finishing builder: %s", step.name, err)
				}

				state, _ := buildOpts.IndexState()
				if diff := cmp.Diff(IndexStateEqual, state); diff != "" {
					t.Errorf("unexpected diff in index state (-want +got):\n%s", diff)
				}

				ss, err := shards.NewDirectorySearcher(indexDir)
				if err != nil {
					t.Fatalf("step %q: NewDirectorySearcher(%s): %s", step.name, indexDir, err)
				}
				defer ss.Close()

				searchOpts := &zoekt.SearchOptions{Whole: true}
				q := &query.Substring{Pattern: step.query}

				result, err := ss.Search(context.Background(), q, searchOpts)
				if err != nil {
					t.Fatalf("step %q: Search(%q): %s", step.name, step.query, err)
				}

				var receivedDocuments []zoekt.Document
				for _, f := range result.Files {
					receivedDocuments = append(receivedDocuments, zoekt.Document{
						Name:    f.FileName,
						Content: f.Content,
					})
				}

				cmpOpts := []cmp.Option{
					cmpopts.IgnoreFields(zoekt.Document{}, "Branches"),
					cmpopts.SortSlices(func(a, b zoekt.Document) bool {
						if a.Name < b.Name {
							return true
						}

						return bytes.Compare(a.Content, b.Content) < 0
					}),
				}

				if diff := cmp.Diff(step.expectedDocuments, receivedDocuments, cmpOpts...); diff != "" {
					t.Errorf("step %q: diff in received documents (-want +got):%s\n:", step.name, diff)
				}
			}
		})
	}
}
