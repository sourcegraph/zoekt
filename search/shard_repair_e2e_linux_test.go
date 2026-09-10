//go:build linux

package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/query"
)

const shardRepairMarker = "UNIQUEMARKERALPHA"

// TestShardRepairReopensRealShardWithoutWatcher exercises the complete repair
// path against a real shard and filesystem mapping. Replacing the truncated
// shard at a new inode cannot heal the old mapping; only reopening the path can
// make the final search succeed. No DirectoryWatcher runs in this test.
func TestShardRepairReopensRealShardWithoutWatcher(t *testing.T) {
	dir := t.TempDir()
	shardPath := filepath.Join(dir, "repair_v16.00000.zoekt")
	shardBytes := buildShardRepairFixture(t, shardPath)

	ss := newShardedSearcher(1)
	(&loader{ss: ss}).load(shardPath)

	q := &query.Substring{Pattern: shardRepairMarker}
	search := func() (*zoekt.SearchResult, error) {
		return ss.Search(context.Background(), q, &zoekt.SearchOptions{
			ShardMaxMatchCount: 100000,
			TotalMaxMatchCount: 100000,
		})
	}

	before, err := search()
	if err != nil {
		t.Fatalf("baseline search: %v", err)
	}
	if before.Stats.FileCount == 0 {
		t.Fatal("baseline search found no fixture documents")
	}

	if err := os.Truncate(shardPath, 0); err != nil {
		t.Fatalf("truncate shard: %v", err)
	}
	faulted, err := search()
	if err != nil {
		t.Fatalf("faulted search returned an error: %v", err)
	}
	if faulted.Stats.Crashes == 0 {
		t.Fatalf(
			"truncating the mapped shard induced no fault: FileCount=%d Crashes=%d",
			faulted.Stats.FileCount,
			faulted.Stats.Crashes,
		)
	}

	staged := shardPath + ".staged"
	if err := os.WriteFile(staged, shardBytes, 0o644); err != nil {
		t.Fatalf("stage replacement shard: %v", err)
	}
	if err := os.Rename(staged, shardPath); err != nil {
		t.Fatalf("publish replacement shard: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		got, err := search()
		if err == nil && got.Stats.Crashes == 0 && got.Stats.FileCount == before.Stats.FileCount {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"shard did not heal: FileCount=%d Crashes=%d, want FileCount=%d Crashes=0",
				got.Stats.FileCount,
				got.Stats.Crashes,
				before.Stats.FileCount,
			)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func buildShardRepairFixture(t *testing.T, shardPath string) []byte {
	t.Helper()

	buildDir := t.TempDir()
	builder, err := index.NewBuilder(index.Options{
		IndexDir: buildDir,
		RepositoryDescription: zoekt.Repository{
			Name: "shard-repair-fixture",
		},
		DisableCTags: true,
	})
	if err != nil {
		t.Fatalf("create shard builder: %v", err)
	}

	var filler strings.Builder
	for i := range 200 {
		fmt.Fprintf(
			&filler,
			"func pad%d() { println(\"payload %d abcdefghij klmnopqrst uvwxyz\") }\n",
			i,
			i,
		)
	}
	for i := range 700 {
		content := fmt.Sprintf(
			"package main\n// %s in file %d\n%s",
			shardRepairMarker,
			i,
			filler.String(),
		)
		if err := builder.AddFile(fmt.Sprintf("src/file%d.go", i), []byte(content)); err != nil {
			t.Fatalf("add fixture file: %v", err)
		}
	}
	if err := builder.Finish(); err != nil {
		t.Fatalf("finish shard: %v", err)
	}

	shards, err := filepath.Glob(filepath.Join(buildDir, "*.zoekt"))
	if err != nil {
		t.Fatalf("find built shard: %v", err)
	}
	if len(shards) != 1 {
		t.Fatalf("built shard count = %d, want 1", len(shards))
	}
	data, err := os.ReadFile(shards[0])
	if err != nil {
		t.Fatalf("read built shard: %v", err)
	}
	if err := os.WriteFile(shardPath, data, 0o644); err != nil {
		t.Fatalf("write fixture shard: %v", err)
	}
	return data
}
