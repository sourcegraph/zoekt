package main

import (
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestGenerateCompoundShards(t *testing.T) {
	old := time.Date(2021, 01, 01, 0, 0, 0, 0, time.UTC)
	recent := old.AddDate(0, 0, 30)

	var maxSize int64 = 1 * 1024 * 1024

	shards := []shard{
		{
			Repo:      "r1",
			ModTime:   old,
			SizeBytes: maxSize - 1,
			Rank:      1,
		},
		{
			Repo:      "r2",
			ModTime:   old,
			SizeBytes: maxSize - 1,
			Rank:      3,
		},
		{
			Repo:      "r3",
			ModTime:   old,
			SizeBytes: maxSize - 1,
			Rank:      2,
		},
		{
			Repo:      "r4",
			ModTime:   old,
			SizeBytes: maxSize - 1,
			Rank:      4,
		},
		// Too new
		{
			Repo:      "r5",
			ModTime:   recent,
			SizeBytes: 1,
			Rank:      5,
		},
		// Too big
		{
			Repo:      "r6",
			ModTime:   old,
			SizeBytes: 2 * maxSize,
			Rank:      1,
		},
	}

	compounds, excluded := generateCompounds(shards, compoundOpts{
		targetSizeBytes: maxSize,
		maxSizeBytes:    maxSize,
		cutoffDate:      old.AddDate(0, 0, 1),
	})

	if len(compounds) != 2 {
		t.Fatalf("expected 2 compound shards, but got %d", len(compounds))
	}

	totalShards := 0
	for _, c := range compounds {
		totalShards += len(c.shards)
	}
	totalShards += len(excluded)
	if totalShards != len(shards) {
		t.Fatalf("shards mismatch: wanted %d, got %d", len(shards), totalShards)
	}

	var excludedRepos []string
	for _, er := range excluded {
		excludedRepos = append(excludedRepos, er.Repo)
	}
	sort.Strings(excludedRepos)

	if diff := cmp.Diff([]string{"r5", "r6"}, excludedRepos); diff != "" {
		t.Fatalf("-want, +got: %s", diff)
	}
}

func TestGenerateCompoundShards_EmptyShards(t *testing.T) {
	compounds, excluded := generateCompounds([]shard{}, compoundOpts{
		targetSizeBytes: 1,
		maxSizeBytes:    1,
		cutoffDate:      time.Now(),
	})

	if !(len(compounds) == 0 && len(excluded) == 0) {
		t.Fatalf("Expect \"compounds\" and \"excluded\" to be empty")
	}
}

func TestGenerateCompoundShards_AllShards(t *testing.T) {
	compounds, excluded := generateCompounds([]shard{{
		Repo:      "r1",
		ModTime:   time.Now().AddDate(0, 0, -1),
		SizeBytes: 2,
	}}, compoundOpts{
		targetSizeBytes: 1,
		maxSizeBytes:    3,
		cutoffDate:      time.Now(),
	})

	if len(compounds) != 1 {
		t.Fatalf("want %d, got %d", 1, len(compounds))
	}
	if len(excluded) != 0 {
		t.Fatalf("want %d, got %d", 0, len(excluded))
	}
}

func TestGenerateCompoundShards_NoShards(t *testing.T) {
	compounds, excluded := generateCompounds([]shard{{
		Repo:      "r1",
		ModTime:   time.Now().AddDate(0, 0, 1),
		SizeBytes: 2,
	}}, compoundOpts{
		targetSizeBytes: 1,
		maxSizeBytes:    1,
		cutoffDate:      time.Now(),
	})

	if len(compounds) != 0 {
		t.Fatalf("want %d, got %d", 0, len(compounds))
	}
	if len(excluded) != 1 {
		t.Fatalf("want %d, got %d", 1, len(excluded))
	}
}
