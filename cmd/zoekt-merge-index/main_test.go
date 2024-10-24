package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/shards"
)

func TestMerge(t *testing.T) {
	v16Shards, err := filepath.Glob("../../testdata/shards/*_v16.*.zoekt")
	require.NoError(t, err)
	sort.Strings(v16Shards)

	testShards, err := copyTestShards(t.TempDir(), v16Shards)
	require.NoError(t, err)
	t.Log(testShards)

	dir := t.TempDir()
	cs, err := merge(dir, testShards)
	require.NoError(t, err)
	// The name of the compound shard is based on the merged repos, so it should be
	// stable
	require.Equal(t, filepath.Base(cs), "compound-ea9613e2ffba7d7361856aebfca75fb714856509_v17.00000.zoekt")

	ss, err := shards.NewDirectorySearcher(dir)
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

	testShards, err := copyTestShards(t.TempDir(), v16Shards)
	require.NoError(t, err)
	t.Log(testShards)

	dir := t.TempDir()
	_, err = merge(dir, testShards)
	require.NoError(t, err)

	cs, err := filepath.Glob(filepath.Join(dir, "compound-*.zoekt"))
	require.NoError(t, err)
	err = explode(dir, cs[0])
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

	ss, err := shards.NewDirectorySearcher(dir)
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

func copyTestShards(dstDir string, srcShards []string) ([]string, error) {
	var tmpShards []string
	for _, s := range srcShards {
		dst := filepath.Join(dstDir, filepath.Base(s))
		tmpShards = append(tmpShards, dst)
		if err := copyFile(s, dst); err != nil {
			return nil, err
		}
	}
	return tmpShards, nil
}

func copyFile(src, dst string) (err error) {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()

	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(d, s); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}
