package search

import (
	"context"
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
)

func TestCopyFaultWithZeroRepositoryIDsRepairsShardByName(t *testing.T) {
	repairer := &recordingRepairer{}
	ss := newShardedSearcher(1)
	ss.shardRepairs = newShardRepairQueue(repairer.reload)

	other := &rankedShard{
		Searcher: &repairTestSearcher{name: "other.zoekt"},
		repos:    []*zoekt.Repository{{Name: "org/other"}},
	}
	target := &rankedShard{
		Searcher: &repairTestSearcher{name: "target.zoekt"},
		repos:    []*zoekt.Repository{{Name: "org/target"}},
	}
	ss.ranked.Store([]*rankedShard{other, target})
	ss.shardRepairs.register(other, "other.zoekt")
	ss.shardRepairs.register(target, "target.zoekt")

	ss.handleFileCopyFault(
		&query.Const{Value: true},
		ss.getLoaded().shards,
		&zoekt.FileMatch{Repository: "org/target"},
		context.Canceled,
		[]byte("test stack"),
	)

	waitForRepair(t, "name-attributed repair", ss.shardRepairs.ready)
	if got := repairer.count("target.zoekt"); got != 1 {
		t.Fatalf("target repair count = %d, want 1", got)
	}
	if got := repairer.count("other.zoekt"); got != 0 {
		t.Fatalf("other repair count = %d, want 0", got)
	}
}

func TestCopyFaultUsesNonzeroRepositoryIDAsFallback(t *testing.T) {
	repairer := &recordingRepairer{}
	ss := newShardedSearcher(1)
	ss.shardRepairs = newShardRepairQueue(repairer.reload)

	target := &rankedShard{
		Searcher: &repairTestSearcher{name: "renamed.zoekt"},
		repos:    []*zoekt.Repository{{ID: 42, Name: "org/current-name"}},
	}
	ss.ranked.Store([]*rankedShard{target})
	ss.shardRepairs.register(target, "renamed.zoekt")

	ss.handleFileCopyFault(
		&query.Const{Value: true},
		ss.getLoaded().shards,
		&zoekt.FileMatch{RepositoryID: 42, Repository: "org/old-name"},
		context.Canceled,
		[]byte("test stack"),
	)

	waitForRepair(t, "ID-attributed repair", ss.shardRepairs.ready)
	if got := repairer.count("renamed.zoekt"); got != 1 {
		t.Fatalf("repair count = %d, want 1", got)
	}
}

func TestCopyFaultDoesNotRepairConcurrentWatcherReplacement(t *testing.T) {
	repairer := &recordingRepairer{}
	ss := newShardedSearcher(1)
	ss.shardRepairs = newShardRepairQueue(repairer.reload)
	const key = "repository.zoekt"

	original := &repairTestSearcher{name: "original"}
	ss.replace(map[string]zoekt.Searcher{key: original})
	faultedSnapshot := ss.getLoaded().shards
	faultedSnapshot[0].repos = []*zoekt.Repository{{Name: "org/repository"}}

	replacement := &repairTestSearcher{name: "replacement"}
	ss.replace(map[string]zoekt.Searcher{key: replacement})

	ss.handleFileCopyFault(
		&query.Const{Value: true},
		faultedSnapshot,
		&zoekt.FileMatch{Repository: "org/repository"},
		context.Canceled,
		[]byte("test stack"),
	)
	waitForRepair(t, "superseded fault handling", ss.shardRepairs.ready)
	if got := repairer.count(key); got != 0 {
		t.Fatalf("replacement repair count = %d, want 0", got)
	}
}
