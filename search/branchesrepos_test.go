// Copyright 2026 Sourcegraph
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package search

import (
	"fmt"
	"testing"

	"github.com/RoaringBitmap/roaring"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
)

const (
	branchesReposBenchmarkBranches = 128
	branchesReposBenchmarkRepos    = 4
)

func newBranchesReposQuery(branches int) *query.BranchesRepos {
	list := make([]query.BranchRepos, branches)
	for i := range list {
		list[i] = query.BranchRepos{
			Branch: fmt.Sprintf("branch-%d", i),
			Repos:  roaring.New(),
		}
	}
	return &query.BranchesRepos{List: list}
}

func newBranchesReposShards(shardCount, reposPerShard int) ([]*rankedShard, [][]uint32) {
	shards := make([]*rankedShard, shardCount)
	ids := make([][]uint32, shardCount)

	for shard := range shards {
		repos := make([]*zoekt.Repository, reposPerShard)
		ids[shard] = make([]uint32, reposPerShard)
		for repo := range repos {
			id := uint32(shard*reposPerShard + repo + 1)
			ids[shard][repo] = id
			repos[repo] = &zoekt.Repository{ID: id}
		}
		shards[shard] = &rankedShard{repos: repos}
	}

	return shards, ids
}

func addBranchesReposIDs(q *query.BranchesRepos, branch int, ids []uint32) {
	for _, id := range ids {
		q.List[branch].Repos.Add(id)
	}
}

func matchingBranchesReposShards(shards []*rankedShard, q *query.BranchesRepos) []*rankedShard {
	var matching []*rankedShard
	for _, shard := range shards {
		if shard.repos == nil {
			matching = append(matching, shard)
			continue
		}

		for _, repo := range shard.repos {
			for _, branch := range q.List {
				if branch.Repos.Contains(repo.ID) {
					matching = append(matching, shard)
					goto nextShard
				}
			}
		}
	nextShard:
	}
	return matching
}

func TestSelectRepoSetBranchesRepos(t *testing.T) {
	shards, ids := newBranchesReposShards(5, 2)
	shards[4].repos = nil

	q := newBranchesReposQuery(3)
	addBranchesReposIDs(q, 0, ids[0])
	addBranchesReposIDs(q, 2, ids[1])
	addBranchesReposIDs(q, 0, []uint32{ids[2][0]})
	addBranchesReposIDs(q, 2, []uint32{ids[2][0]})

	want := matchingBranchesReposShards(shards, q)
	got, gotQuery := selectRepoSet(shards, q)

	if len(got) != len(want) {
		t.Fatalf("selected %d shards, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("selected shard %d = %p, want %p", i, got[i], want[i])
		}
	}
	if gotQuery != q {
		t.Fatalf("selectRepoSet changed multi-branch query: got %s, want %s", gotQuery, q)
	}
}

func benchmarkSelectRepoSetBranchesRepos(b *testing.B, shards []*rankedShard, q query.Q, wantShards int) {
	b.Helper()

	var filtered []*rankedShard
	for b.Loop() {
		filtered, _ = selectRepoSet(shards, q)
	}

	if got := len(filtered); got != wantShards {
		b.Fatalf("selected %d shards, want %d", got, wantShards)
	}
}

func BenchmarkSelectRepoSetBranchesRepos(b *testing.B) {
	shards, ids := newBranchesReposShards(10_000, branchesReposBenchmarkRepos)
	q := newBranchesReposQuery(branchesReposBenchmarkBranches)

	for shard, shardIDs := range ids {
		if shard%10 == 0 {
			addBranchesReposIDs(q, shard%branchesReposBenchmarkBranches, shardIDs)
		}
	}

	benchmarkSelectRepoSetBranchesRepos(b, shards, q, 1_000)
}

func BenchmarkSelectRepoSetBranchesReposLateMatch(b *testing.B) {
	shards, ids := newBranchesReposShards(10_000, branchesReposBenchmarkRepos)
	q := newBranchesReposQuery(branchesReposBenchmarkBranches)

	for _, shardIDs := range ids {
		addBranchesReposIDs(q, branchesReposBenchmarkBranches-1, shardIDs)
	}

	benchmarkSelectRepoSetBranchesRepos(b, shards, q, len(shards))
}

func BenchmarkSelectRepoSetBranchesReposMatchingPrefix(b *testing.B) {
	shards, ids := newBranchesReposShards(10_000, branchesReposBenchmarkRepos)
	q := newBranchesReposQuery(branchesReposBenchmarkBranches)

	for _, shardIDs := range ids[:4] {
		addBranchesReposIDs(q, branchesReposBenchmarkBranches-1, shardIDs)
	}

	benchmarkSelectRepoSetBranchesRepos(b, shards, q, 4)
}

func BenchmarkSelectRepoSetBranchesReposOneShard(b *testing.B) {
	shards, ids := newBranchesReposShards(1, branchesReposBenchmarkRepos)
	q := newBranchesReposQuery(branchesReposBenchmarkBranches)
	addBranchesReposIDs(q, branchesReposBenchmarkBranches-1, ids[0])

	benchmarkSelectRepoSetBranchesRepos(b, shards, q, 1)
}

func BenchmarkSelectRepoSetBranchesReposOverlapping(b *testing.B) {
	shards, ids := newBranchesReposShards(32, 1)
	q := newBranchesReposQuery(branchesReposBenchmarkBranches)

	for branch := range q.List {
		for _, shardIDs := range ids {
			addBranchesReposIDs(q, branch, shardIDs)
		}
	}

	benchmarkSelectRepoSetBranchesRepos(b, shards, q, len(shards))
}

func BenchmarkSelectRepoSetBranchesReposOverlappingSingleRepo(b *testing.B) {
	shards := make([]*rankedShard, 32)
	for i := range shards {
		shards[i] = &rankedShard{repos: []*zoekt.Repository{{ID: 1}}}
	}

	q := newBranchesReposQuery(branchesReposBenchmarkBranches)
	for branch := range q.List {
		q.List[branch].Repos.Add(1)
	}

	benchmarkSelectRepoSetBranchesRepos(b, shards, q, len(shards))
}

func BenchmarkSelectRepoSetBranchesReposLargeBitmapsFewShards(b *testing.B) {
	shards, _ := newBranchesReposShards(10, branchesReposBenchmarkRepos)
	q := newBranchesReposQuery(branchesReposBenchmarkBranches)

	for branch := range q.List {
		for id := uint32(0); id < 100; id++ {
			q.List[branch].Repos.Add(100_000 + uint32(branch)*100 + id)
		}
	}

	benchmarkSelectRepoSetBranchesRepos(b, shards, q, 0)
}

// BenchmarkSelectRepoSetBranchesReposLargeBitmapsModerateShards covers the
// adaptive boundary where 128 100-ID bitmaps still have too little remaining
// miss work to repay materializing their aggregate.
func BenchmarkSelectRepoSetBranchesReposLargeBitmapsModerateShards(b *testing.B) {
	shards, _ := newBranchesReposShards(128, branchesReposBenchmarkRepos)
	q := newBranchesReposQuery(branchesReposBenchmarkBranches)

	for branch := range q.List {
		for id := uint32(0); id < 100; id++ {
			q.List[branch].Repos.Add(100_000 + uint32(branch)*100 + id)
		}
	}

	benchmarkSelectRepoSetBranchesRepos(b, shards, q, 0)
}

// BenchmarkSelectRepoSetBranchesReposLatePrefixThenFirst covers a query whose
// early final-branch matches are followed by a much larger first-branch run.
// The latter is already the linear scan's cheapest path, so building a union
// based only on the prefix would add work instead of removing it.
func BenchmarkSelectRepoSetBranchesReposLatePrefixThenFirst(b *testing.B) {
	shards, ids := newBranchesReposShards(10_000, branchesReposBenchmarkRepos)
	q := newBranchesReposQuery(branchesReposBenchmarkBranches)

	for _, shardIDs := range ids[:2_000] {
		addBranchesReposIDs(q, branchesReposBenchmarkBranches-1, shardIDs)
	}
	for _, shardIDs := range ids[2_000:] {
		addBranchesReposIDs(q, 0, shardIDs)
	}

	benchmarkSelectRepoSetBranchesRepos(b, shards, q, len(shards))
}

// BenchmarkSelectRepoSetBranchesReposMissPrefixThenFifth covers an initially
// miss-heavy scan followed by repositories that take the fifth branch's cheap
// linear path. Materializing an aggregate from the miss prefix must not make
// that suffix slower.
func BenchmarkSelectRepoSetBranchesReposMissPrefixThenFifth(b *testing.B) {
	shards, ids := newBranchesReposShards(10_000, branchesReposBenchmarkRepos)
	q := newBranchesReposQuery(branchesReposBenchmarkBranches)

	// Leave the first 1,400 shards outside every branch bitmap.
	for _, shardIDs := range ids[1_400:] {
		addBranchesReposIDs(q, 4, shardIDs)
	}

	benchmarkSelectRepoSetBranchesRepos(b, shards, q, len(shards)-1_400)
}

// BenchmarkSelectRepoSetBranchesReposSampledFirstRepoOnly covers a miss-heavy
// query whose interior sample shards match only through their first repository.
// A sampling guard must not discard aggregation while the other repositories
// and almost all other shards still require the expensive path.
func BenchmarkSelectRepoSetBranchesReposSampledFirstRepoOnly(b *testing.B) {
	shards, ids := newBranchesReposShards(10_000, branchesReposBenchmarkRepos)
	q := newBranchesReposQuery(branchesReposBenchmarkBranches)

	for shard, shardIDs := range ids {
		if shard%10 == 1 {
			addBranchesReposIDs(q, branchesReposBenchmarkBranches-1, shardIDs)
		}
	}
	for _, shard := range []int{2_500, 5_000, 7_500} {
		addBranchesReposIDs(q, 0, ids[shard][:1])
	}

	benchmarkSelectRepoSetBranchesRepos(b, shards, q, 1_003)
}
