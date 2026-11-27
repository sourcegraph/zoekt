package main

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/internal/testutil"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/search"
)

func TestMerge(t *testing.T) {
	v16Shards, err := filepath.Glob("../../testdata/shards/*_v16.*.zoekt")
	require.NoError(t, err)
	sort.Strings(v16Shards)

	testShards := testutil.CopyFiles(t, t.TempDir(), v16Shards...)
	t.Log(testShards)

	dir := t.TempDir()
	cs, err := merge(dir, testShards)
	require.NoError(t, err)
	// The name of the compound shard is based on the merged repos, so it should be
	// stable
	require.Equal(t, filepath.Base(cs), "compound-ea9613e2ffba7d7361856aebfca75fb714856509_v17.00000.zoekt")

	ss, err := search.NewDirectorySearcher(dir)
	require.NoError(t, err)
	defer ss.Close()

	q, err := query.Parse("hello")
	require.NoError(t, err)

	var sOpts zoekt.SearchOptions
	ctx := context.Background()
	result, err := ss.Search(ctx, q, &sOpts)
	require.NoError(t, err)

	// we are merging the same shard twice, so we expect the same file twice.
	require.Len(t, result.Files, 2)
}

// Merge 2 simple shards and then explode them.
func TestExplode(t *testing.T) {
	v16Shards, err := filepath.Glob("../../testdata/shards/repo*_v16.*.zoekt")
	require.NoError(t, err)
	sort.Strings(v16Shards)

	testShards := testutil.CopyFiles(t, t.TempDir(), v16Shards...)
	t.Log(testShards)

	dir := t.TempDir()
	_, err = merge(dir, testShards)
	require.NoError(t, err)

	cs, err := filepath.Glob(filepath.Join(dir, "compound-*.zoekt"))
	require.NoError(t, err)
	err = index.Explode(dir, cs[0])
	require.NoError(t, err)

	cs, err = filepath.Glob(filepath.Join(dir, "compound-*.zoekt"))
	require.NoError(t, err)

	if len(cs) != 0 {
		t.Fatalf("explode should have deleted the compound shard if it returned without error")
	}

	exploded, err := filepath.Glob(filepath.Join(dir, "*.zoekt"))
	require.NoError(t, err)

	if len(exploded) != len(testShards) {
		t.Fatalf("the number of simple shards before %d and after %d should be the same", len(testShards), len(exploded))
	}

	ss, err := search.NewDirectorySearcher(dir)
	require.NoError(t, err)
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
			require.NoError(t, err)
			result, err := ss.Search(ctx, q, &sOpts)
			require.NoError(t, err)
			require.Len(t, result.Files, c.wantResults)
		})
	}
}

// Merging the same repositories again yields the same compound shard name. The
// metadata sidecar of an earlier compound shard with that name, in which the
// repositories were tombstoned after being reindexed, must not hide them.
func TestMergeReplacesStaleMetadata(t *testing.T) {
	dir := t.TempDir()
	build := func() []string {
		var shards []string
		for id, name := range map[uint32]string{1: "repoA", 2: "repoB"} {
			opts := index.Options{
				IndexDir:              dir,
				RepositoryDescription: zoekt.Repository{ID: id, Name: name},
				DisableCTags:          true,
				ShardMerging:          true,
			}
			b, err := index.NewBuilder(opts)
			require.NoError(t, err)
			require.NoError(t, b.AddFile("f.txt", []byte("needle "+name)))
			require.NoError(t, b.Finish())
			shards = append(shards, opts.FindAllShards()...)
		}
		sort.Strings(shards)
		return shards
	}

	first, err := merge(dir, build())
	require.NoError(t, err)
	// Both repositories are reindexed (e.g. after an index format upgrade) into
	// simple shards, which tombstones them in the compound shard.
	shards := build()
	alive, _, err := index.ReadMetadataPathAlive(first)
	require.NoError(t, err)
	require.Empty(t, alive)

	second, err := merge(dir, shards)
	require.NoError(t, err)
	require.Equal(t, first, second)

	ss, err := search.NewDirectorySearcher(dir)
	require.NoError(t, err)
	defer ss.Close()
	result, err := ss.Search(context.Background(), &query.Substring{Pattern: "needle"}, &zoekt.SearchOptions{})
	require.NoError(t, err)
	require.Len(t, result.Files, 2, "repositories in the new compound shard are tombstoned")
}
