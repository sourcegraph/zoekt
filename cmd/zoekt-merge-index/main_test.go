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
