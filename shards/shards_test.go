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

package shards

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
)

type crashSearcher struct{}

func (s *crashSearcher) Search(ctx context.Context, q query.Q, opts *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	panic("search")
}

func (s *crashSearcher) List(ctx context.Context, q query.Q, opts *zoekt.ListOptions) (*zoekt.RepoList, error) {
	panic("list")
}

func (s *crashSearcher) Stats() (*zoekt.RepoStats, error) {
	return &zoekt.RepoStats{}, nil
}

func (s *crashSearcher) Close() {}

func (s *crashSearcher) String() string { return "crashSearcher" }

func TestCrashResilience(t *testing.T) {
	out := &bytes.Buffer{}
	log.SetOutput(out)
	defer log.SetOutput(os.Stderr)
	ss := newShardedSearcher(2)
	ss.shards = map[string]rankedShard{
		"x": {Searcher: &crashSearcher{}},
	}

	q := &query.Substring{Pattern: "hoi"}
	opts := &zoekt.SearchOptions{}
	if res, err := ss.Search(context.Background(), q, opts); err != nil {
		t.Fatalf("Search: %v", err)
	} else if res.Stats.Crashes != 1 {
		t.Errorf("got stats %#v, want crashes = 1", res.Stats)
	}

	if res, err := ss.List(context.Background(), q, nil); err != nil {
		t.Fatalf("List: %v", err)
	} else if res.Crashes != 1 {
		t.Errorf("got result %#v, want crashes = 1", res)
	}
}

type rankSearcher struct {
	rank uint16
	repo *zoekt.Repository
}

func (s *rankSearcher) Close() {
}

func (s *rankSearcher) String() string {
	return ""
}

func (s *rankSearcher) Search(ctx context.Context, q query.Q, opts *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	select {
	case <-ctx.Done():
		return &zoekt.SearchResult{}, nil
	default:
	}

	// Ugly, but without sleep it's too fast, and we can't
	// simulate the cutoff.
	time.Sleep(time.Millisecond)
	return &zoekt.SearchResult{
		Files: []zoekt.FileMatch{
			{
				FileName: fmt.Sprintf("f%d", s.rank),
				Score:    float64(s.rank),
			},
		},
		Stats: zoekt.Stats{
			MatchCount: 1,
		},
	}, nil
}

func (s *rankSearcher) List(ctx context.Context, q query.Q, opts *zoekt.ListOptions) (*zoekt.RepoList, error) {
	r := zoekt.Repository{}
	if s.repo != nil {
		r = *s.repo
	}
	r.Rank = s.rank
	return &zoekt.RepoList{
		Repos: []*zoekt.RepoListEntry{
			{Repository: r},
		},
	}, nil
}

func (s *rankSearcher) Repository() *zoekt.Repository { return s.repo }

func TestOrderByShard(t *testing.T) {
	ss := newShardedSearcher(1)

	n := 10 * runtime.GOMAXPROCS(0)
	for i := 0; i < n; i++ {
		ss.replace(fmt.Sprintf("shard%d", i),
			&rankSearcher{
				rank: uint16(i),
			})
	}

	if res, err := ss.Search(context.Background(), &query.Substring{Pattern: "bla"}, &zoekt.SearchOptions{}); err != nil {
		t.Errorf("Search: %v", err)
	} else if len(res.Files) != n {
		t.Fatalf("empty options: got %d results, want %d", len(res.Files), n)
	}

	opts := zoekt.SearchOptions{
		TotalMaxMatchCount: 3,
	}
	res, err := ss.Search(context.Background(), &query.Substring{Pattern: "bla"}, &opts)
	if err != nil {
		t.Errorf("Search: %v", err)
	}

	if len(res.Files) < opts.TotalMaxMatchCount {
		t.Errorf("got %d results, want %d", len(res.Files), opts.TotalMaxMatchCount)
	}
	if len(res.Files) == n {
		t.Errorf("got %d results, want < %d", len(res.Files), n)
	}
	for i, f := range res.Files {
		rev := n - 1 - i
		want := fmt.Sprintf("f%d", rev)
		got := f.FileName

		if got != want {
			t.Logf("%d: got %q, want %q", i, got, want)
		}
	}
}

func TestFilteringShardsByRepoSet(t *testing.T) {
	ss := newShardedSearcher(1)

	repoSetNames := []string{}
	n := 10 * runtime.GOMAXPROCS(0)
	for i := 0; i < n; i++ {
		shardName := fmt.Sprintf("shard%d", i)
		repoName := fmt.Sprintf("repository%.3d", i)

		if i%3 == 0 {
			repoSetNames = append(repoSetNames, repoName)
		}

		ss.replace(shardName, &rankSearcher{
			repo: &zoekt.Repository{Name: repoName},
			rank: uint16(n - i),
		})
	}

	res, err := ss.Search(context.Background(), &query.Substring{Pattern: "bla"}, &zoekt.SearchOptions{})
	if err != nil {
		t.Errorf("Search: %v", err)
	}
	if len(res.Files) != n {
		t.Fatalf("no reposet: got %d results, want %d", len(res.Files), n)
	}

	repoBranches := &query.RepoBranches{Set: make(map[string][]string)}
	for _, name := range repoSetNames {
		repoBranches.Set[name] = []string{"HEAD"}
	}

	set := query.NewRepoSet(repoSetNames...)
	sub := &query.Substring{Pattern: "bla"}

	queries := []query.Q{
		query.NewAnd(set, sub),
		// Test with the same reposet again
		query.NewAnd(set, sub),

		query.NewAnd(repoBranches, sub),
		// Test with the same repoBranches again
		query.NewAnd(repoBranches, sub),
	}

	for _, q := range queries {
		res, err = ss.Search(context.Background(), q, &zoekt.SearchOptions{})
		if err != nil {
			t.Errorf("Search(%s): %v", q, err)
		}
		// Note: Assertion is based on fact that `rankSearcher` always returns a
		// result and using repoSet will half the number of results
		if len(res.Files) != len(repoSetNames) {
			t.Fatalf("%s: got %d results, want %d", q, len(res.Files), len(repoSetNames))
		}
	}
}

type memSeeker struct {
	data []byte
}

func (s *memSeeker) Name() string {
	return "memseeker"
}

func (s *memSeeker) Close() {}
func (s *memSeeker) Read(off, sz uint32) ([]byte, error) {
	return s.data[off : off+sz], nil
}

func (s *memSeeker) Size() (uint32, error) {
	return uint32(len(s.data)), nil
}

func TestUnloadIndex(t *testing.T) {
	b := testIndexBuilder(t, nil, zoekt.Document{
		Name:    "filename",
		Content: []byte("needle needle needle"),
	})

	var buf bytes.Buffer
	b.Write(&buf)
	indexBytes := buf.Bytes()
	indexFile := &memSeeker{indexBytes}
	searcher, err := zoekt.NewSearcher(indexFile)
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}

	ss := newShardedSearcher(2)
	ss.replace("key", searcher)

	var opts zoekt.SearchOptions
	q := &query.Substring{Pattern: "needle"}
	res, err := ss.Search(context.Background(), q, &opts)
	if err != nil {
		t.Fatalf("Search(%s): %v", q, err)
	}

	forbidden := byte(29)
	for i := range indexBytes {
		// non-ASCII
		indexBytes[i] = forbidden
	}

	for _, f := range res.Files {
		if bytes.Contains(f.Content, []byte{forbidden}) {
			t.Errorf("found %d in content %q", forbidden, f.Content)
		}
		if bytes.Contains(f.Checksum, []byte{forbidden}) {
			t.Errorf("found %d in checksum %q", forbidden, f.Checksum)
		}

		for _, l := range f.LineMatches {
			if bytes.Contains(l.Line, []byte{forbidden}) {
				t.Errorf("found %d in line %q", forbidden, l.Line)
			}
		}
	}
}

func TestShardedSearcher_List(t *testing.T) {
	repos := []*zoekt.Repository{
		{
			ID:        1234,
			Name:      "repo-a",
			Branches:  []zoekt.RepositoryBranch{{Name: "main"}, {Name: "dev"}},
			RawConfig: map[string]string{"repoid": "1234"},
		},
		{
			Name:     "repo-b",
			Branches: []zoekt.RepositoryBranch{{Name: "main"}, {Name: "dev"}},
		},
	}

	// Test duplicate removal when ListOptions.Minimal is true and false
	ss := newShardedSearcher(4)
	ss.replace("1", searcherForTest(t, testIndexBuilder(t, repos[0])))
	ss.replace("2", searcherForTest(t, testIndexBuilder(t, repos[0])))
	ss.replace("3", searcherForTest(t, testIndexBuilder(t, repos[1])))
	ss.replace("4", searcherForTest(t, testIndexBuilder(t, repos[1])))

	for _, tc := range []struct {
		name string
		opts *zoekt.ListOptions
		want *zoekt.RepoList
	}{
		{
			name: "nil opts",
			opts: nil,
			want: &zoekt.RepoList{
				Repos: []*zoekt.RepoListEntry{
					{
						Repository: *repos[0],
						Stats:      zoekt.RepoStats{Shards: 2},
					},
					{
						Repository: *repos[1],
						Stats:      zoekt.RepoStats{Shards: 2},
					},
				},
			},
		},
		{
			name: "minimal=false",
			opts: &zoekt.ListOptions{Minimal: false},
			want: &zoekt.RepoList{
				Repos: []*zoekt.RepoListEntry{
					{
						Repository: *repos[0],
						Stats:      zoekt.RepoStats{Shards: 2},
					},
					{
						Repository: *repos[1],
						Stats:      zoekt.RepoStats{Shards: 2},
					},
				},
			},
		},
		{
			name: "minimal=true",
			opts: &zoekt.ListOptions{Minimal: true},
			want: &zoekt.RepoList{
				Repos: []*zoekt.RepoListEntry{
					{
						Repository: *repos[1],
						Stats:      zoekt.RepoStats{Shards: 2},
					},
				},
				Minimal: map[uint32]*zoekt.MinimalRepoListEntry{
					repos[0].ID: {
						HasSymbols: repos[0].HasSymbols,
						Branches:   repos[0].Branches,
					},
				},
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			q := &query.Repo{Pattern: "epo"}

			res, err := ss.List(context.Background(), q, tc.opts)
			if err != nil {
				t.Fatalf("List(%v, %s): %v", q, tc.opts, err)
			}

			sort.Slice(res.Repos, func(i, j int) bool {
				return res.Repos[i].Repository.Name < res.Repos[j].Repository.Name
			})

			ignored := []cmp.Option{
				cmpopts.EquateEmpty(),
				cmpopts.IgnoreFields(zoekt.RepoListEntry{}, "IndexMetadata"),
				cmpopts.IgnoreFields(zoekt.RepoStats{}, "IndexBytes"),
				cmpopts.IgnoreFields(zoekt.Repository{}, "SubRepoMap"),
			}
			if diff := cmp.Diff(tc.want, res, ignored...); diff != "" {
				t.Fatalf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func testIndexBuilder(t testing.TB, repo *zoekt.Repository, docs ...zoekt.Document) *zoekt.IndexBuilder {
	b, err := zoekt.NewIndexBuilder(repo)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	for i, d := range docs {
		if err := b.Add(d); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}
	return b
}

func searcherForTest(t testing.TB, b *zoekt.IndexBuilder) zoekt.Searcher {
	var buf bytes.Buffer
	b.Write(&buf)
	f := &memSeeker{buf.Bytes()}

	searcher, err := zoekt.NewSearcher(f)
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}

	return searcher
}

func reposForTest(n int) (result []*zoekt.Repository) {
	for i := 0; i < n; i++ {
		result = append(result, &zoekt.Repository{
			Name: fmt.Sprintf("test-repository-%d", i),
		})
	}
	return result
}

func testSearcherForRepo(b testing.TB, r *zoekt.Repository, numFiles int) zoekt.Searcher {
	builder := testIndexBuilder(b, r)

	builder.Add(zoekt.Document{
		Name:    fmt.Sprintf("%s/filename-%d.go", r.Name, 0),
		Content: []byte("needle needle needle haystack"),
	})

	for i := 1; i < numFiles; i++ {
		builder.Add(zoekt.Document{
			Name:    fmt.Sprintf("%s/filename-%d.go", r.Name, i),
			Content: []byte("haystack haystack haystack"),
		})
	}

	return searcherForTest(b, builder)
}

func BenchmarkShardedSearch(b *testing.B) {
	ss := newShardedSearcher(int64(runtime.GOMAXPROCS(0)))

	filesPerRepo := 300
	repos := reposForTest(3000)
	repoSetNames := make([]string, 0, len(repos)/2)

	for i, r := range repos {
		searcher := testSearcherForRepo(b, r, filesPerRepo)
		ss.replace(r.Name, searcher)
		if i%2 == 0 {
			repoSetNames = append(repoSetNames, r.Name)
		}
	}

	ctx := context.Background()
	opts := &zoekt.SearchOptions{}

	needleSub := &query.Substring{Pattern: "needle"}
	haystackSub := &query.Substring{Pattern: "haystack"}
	helloworldSub := &query.Substring{Pattern: "helloworld"}

	setAnd := func(q query.Q) func() query.Q {
		return func() query.Q {
			return query.NewAnd(query.NewRepoSet(repoSetNames...), q)
		}
	}

	search := func(b *testing.B, q query.Q, wantFiles int) {
		b.Helper()

		res, err := ss.Search(ctx, q, opts)
		if err != nil {
			b.Fatalf("Search(%s): %v", q, err)
		}
		if have := len(res.Files); have != wantFiles {
			b.Fatalf("wrong number of file results. have=%d, want=%d", have, wantFiles)
		}
	}

	benchmarks := []struct {
		name      string
		q         func() query.Q
		wantFiles int
	}{
		{"substring all results", func() query.Q { return haystackSub }, len(repos) * filesPerRepo},
		{"substring no results", func() query.Q { return helloworldSub }, 0},
		{"substring some results", func() query.Q { return needleSub }, len(repos)},

		{"substring all results and repo set", setAnd(haystackSub), len(repoSetNames) * filesPerRepo},
		{"substring some results and repo set", setAnd(needleSub), len(repoSetNames)},
		{"substring no results and repo set", setAnd(helloworldSub), 0},
	}

	for _, bb := range benchmarks {
		b.Run(bb.name, func(b *testing.B) {
			b.ReportAllocs()

			for n := 0; n < b.N; n++ {
				q := bb.q()

				search(b, q, bb.wantFiles)
			}
		})
	}
}

func TestVisibilityQuery(t *testing.T) {
	ss := newShardedSearcher(1)

	var nextShardNum int
	addShard := func(repo string, public bool, docs ...zoekt.Document) {
		r := &zoekt.Repository{Name: repo}
		if public {
			r.RawConfig = map[string]string{"public": "1"}
		}
		b := testIndexBuilder(t, r, docs...)
		shard := searcherForTest(t, b)
		ss.replace(fmt.Sprintf("key-%d", nextShardNum), shard)
		nextShardNum++
	}
	addShard("publicRepo", true, zoekt.Document{Name: "f1", Content: []byte("bla the needle")})
	addShard("publicRepo2", true, zoekt.Document{Name: "f2", Content: []byte("needle in haystack")})
	addShard("privateRepo", false, zoekt.Document{Name: "f3", Content: []byte("the banana")}, zoekt.Document{Name: "f3", Content: []byte("needle banana")})

	cases := []struct {
		pattern    string
		visibility string
		wantRepos  []string
		wantFiles  int
	}{
		{
			pattern:    "the",
			visibility: "public",
			wantRepos:  []string{"publicRepo"},
			wantFiles:  1,
		},
		{
			pattern:    "banana",
			visibility: "public",
			wantFiles:  0,
		},
		{
			pattern:    "needle",
			visibility: "public",
			wantRepos:  []string{"publicRepo", "publicRepo2"},
			wantFiles:  2,
		},
		{
			pattern:    "the",
			visibility: "private",
			wantRepos:  []string{"privateRepo"},
			wantFiles:  1,
		},
		{
			pattern:    "banana",
			visibility: "private",
			wantRepos:  []string{"privateRepo"},
			wantFiles:  2,
		},
		{
			pattern:    "bla",
			visibility: "private",
			wantFiles:  0,
		},
		{
			pattern:    "needle",
			visibility: "private",
			wantRepos:  []string{"privateRepo"},
			wantFiles:  1,
		},
	}

	for _, c := range cases {
		t.Run(fmt.Sprintf("pattern:%s,visibility:%s", c.pattern, c.visibility), func(t *testing.T) {

			sr, err := ss.Search(context.Background(), query.NewAnd(&query.Substring{Pattern: c.pattern}, &query.Visibility{c.visibility}), &zoekt.SearchOptions{})
			if err != nil {
				t.Fatal(err)
			}

			if got := len(sr.Files); got != c.wantFiles {
				t.Fatalf("wanted %d, got %d", c.wantFiles, got)
			}

			if c.wantFiles == 0 {
				return
			}

			gotRepos := make([]string, 0, len(sr.RepoURLs))
			for k, _ := range sr.RepoURLs {
				gotRepos = append(gotRepos, k)
			}
			sort.Strings(gotRepos)
			sort.Strings(c.wantRepos)
			if d := cmp.Diff(c.wantRepos, gotRepos); d != "" {
				t.Fatalf("(-want, +got):\n%s", d)
			}
		})
	}
}
