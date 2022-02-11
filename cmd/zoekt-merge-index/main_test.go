package main

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/shards"
)

func TestMerge(t *testing.T) {
	dir := t.TempDir()

	v16Shards, err := filepath.Glob("../../testdata/shards/*_v16.*.zoekt")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(v16Shards)
	t.Log(v16Shards)

	err = merge(dir, v16Shards)
	if err != nil {
		t.Fatal(err)
	}

	ss, err := shards.NewDirectorySearcher(dir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %v", dir, err)
	}
	defer ss.Close()

	q, err := query.Parse("hello")
	if err != nil {
		t.Fatalf("Parse(hello): %v", err)
	}

	var sOpts zoekt.SearchOptions
	ctx := context.Background()
	result, err := ss.Search(ctx, q, &sOpts)
	if err != nil {
		t.Fatalf("Search(%v): %v", q, err)
	}

	// we are merging the same shard twice, so we expect the same file twice.
	if len(result.Files) != 2 {
		t.Errorf("got %v, want 2 files.", result.Files)
	}
}

// TODO (stefan): make zoekt-git-index deterministic to compare the simple shards
// byte by byte instead of by search results.

// Merge 2 simple shards and then explode them.
func TestExplode(t *testing.T) {
	dir := t.TempDir()

	v16Shards, err := filepath.Glob("../../testdata/shards/repo*_v16.*.zoekt")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(v16Shards)
	t.Log(v16Shards)

	err = merge(dir, v16Shards)
	if err != nil {
		t.Fatal(err)
	}

	cs, err := filepath.Glob(filepath.Join(dir, "compound-*.zoekt"))
	if err != nil {
		t.Fatal(err)
	}
	err = explode(dir, cs[0])
	if err != nil {
		t.Fatal(err)
	}

	cs, err = filepath.Glob(filepath.Join(dir, "compound-*.zoekt"))
	if err != nil {
		t.Fatal(err)
	}

	if len(cs) != 0 {
		t.Fatalf("explode should have deleted the compound shard if it returned without error")
	}

	exploded, err := filepath.Glob(filepath.Join(dir, "*.zoekt"))
	if err != nil {
		t.Fatal(err)
	}

	if len(exploded) != len(v16Shards) {
		t.Fatalf("the number of simpled shards should be the same before and after")
	}

	ss, err := shards.NewDirectorySearcher(dir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %v", dir, err)
	}
	defer ss.Close()

	var sOpts zoekt.SearchOptions
	ctx := context.Background()

	cases := []struct {
		searchLiteral string
		wantResults   int
	}{
		{
			searchLiteral: "apple",
			wantResults:   1,
		},
		{
			searchLiteral: "hello",
			wantResults:   1,
		},
		{
			searchLiteral: "main",
			wantResults:   2,
		},
	}

	for _, c := range cases {
		t.Run(c.searchLiteral, func(t *testing.T) {
			q, err := query.Parse(c.searchLiteral)
			if err != nil {
				t.Fatalf("Parse(%s): %v", c.searchLiteral, err)
			}
			result, err := ss.Search(ctx, q, &sOpts)
			if err != nil {
				t.Fatalf("Search(%v): %v", q, err)
			}
			if got := len(result.Files); got != c.wantResults {
				t.Fatalf("wanted %d results, got %d", c.wantResults, got)
			}
		})
	}
}
