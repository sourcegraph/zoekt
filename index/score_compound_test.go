package index

import (
	"context"
	"testing"

	"github.com/grafana/regexp"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
)

// A repository filter holds for every file of a repository, so it says
// nothing about how well a file matches. A simple shard drops a filter that
// all its repositories pass, but a compound shard keeps it when only some of
// its repositories pass. The same file must still score the same in both.
func TestScoreFileRepoFiltersSameInSimpleAndCompoundShards(t *testing.T) {
	repoA := &zoekt.Repository{
		ID:        1,
		Name:      "github.com/org/a",
		RawConfig: map[string]string{"public": "1"},
		Metadata:  map[string]string{"team": "search"},
		Branches:  []zoekt.RepositoryBranch{{Name: "HEAD", Version: "v1"}},
	}
	repoB := &zoekt.Repository{
		ID:        2,
		Name:      "github.com/org/b",
		RawConfig: map[string]string{"public": "1", "fork": "1"},
		Branches:  []zoekt.RepositoryBranch{{Name: "HEAD", Version: "v1"}},
	}
	doc := Document{Name: "main.go", Content: []byte("package main\n\nfunc needle() {}\n"), Branches: []string{"HEAD"}}
	other := Document{Name: "other.go", Content: []byte("package other\n"), Branches: []string{"HEAD"}}

	// The simple shard holds a better ranked copy of the repository, so its
	// file must come first when the query scores both files the same.
	ranked := *repoA
	ranked.Rank = 100
	simple := searcherForTest(t, testShardBuilder(t, &ranked, doc))
	compound := searcherForTest(t, testShardBuilderCompound(t,
		[]*zoekt.Repository{repoA, repoB},
		[][]Document{{doc}, {other}},
	))

	needle := &query.Substring{Pattern: "needle", Content: true}
	filters := []struct {
		name   string
		filter query.Q
	}{
		{"RepoIDs", query.NewRepoIDs(repoA.ID)},
		{"RepoSet", query.NewRepoSet(repoA.Name)},
		{"Repo", &query.Repo{Regexp: regexp.MustCompile("org/a$")}},
		{"RepoRegexp", &query.RepoRegexp{Regexp: regexp.MustCompile("org/a$")}},
		{"RawConfig", query.RawConfig(query.RcNoForks)},
		{"Meta", &query.Meta{Field: "team", Value: regexp.MustCompile("search")}},
		// The shape of a Sourcegraph global search.
		{"BranchAndRawConfig", query.NewAnd(
			&query.Branch{Pattern: "HEAD", Exact: true},
			query.RawConfig(query.RcOnlyPublic|query.RcNoForks),
		)},
	}

	score := func(t *testing.T, s zoekt.Searcher, q query.Q, opts zoekt.SearchOptions) (float64, string) {
		t.Helper()
		res, err := s.Search(context.Background(), q, &opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Files) != 1 || res.Files[0].FileName != "main.go" {
			t.Fatalf("got %d files, want main.go only", len(res.Files))
		}
		return res.Files[0].Score, res.Files[0].Debug
	}

	for _, f := range filters {
		t.Run(f.name, func(t *testing.T) {
			q := query.NewAnd(needle, f.filter)
			for _, opts := range []zoekt.SearchOptions{
				{DebugScore: true},
				{DebugScore: true, ChunkMatches: true},
			} {
				simpleScore, simpleDebug := score(t, simple, q, opts)
				compoundScore, compoundDebug := score(t, compound, q, opts)

				// Doc order and repo rank are tiebreakers below ScoreOffset.
				// Compare only the query dependent part.
				if got, want := int(compoundScore/ScoreOffset), int(simpleScore/ScoreOffset); got != want {
					t.Errorf("ChunkMatches=%v: compound shard score %d, simple shard score %d\ncompound: %s\nsimple:   %s",
						opts.ChunkMatches, got, want, compoundDebug, simpleDebug)
				}
				if compoundScore >= simpleScore {
					t.Errorf("ChunkMatches=%v: the compound shard's file ranks first, %.0f >= %.0f", opts.ChunkMatches, compoundScore, simpleScore)
				}
			}
		})
	}
}
