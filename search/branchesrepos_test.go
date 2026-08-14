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
	"math/rand/v2"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
)

const (
	branchesReposBenchmarkBranches   = 128
	branchesReposBenchmarkContainers = 2_048
	branchesReposBenchmarkRepos      = 4
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

type branchesReposSnapshot struct {
	bitmap   *roaring.Bitmap
	contents *roaring.Bitmap
}

func snapshotBranchesRepos(q *query.BranchesRepos) []branchesReposSnapshot {
	snapshots := make([]branchesReposSnapshot, len(q.List))
	for i, branch := range q.List {
		snapshots[i] = branchesReposSnapshot{
			bitmap:   branch.Repos,
			contents: branch.Repos.Clone(),
		}
	}
	return snapshots
}

func assertBranchesReposUnchanged(t *testing.T, name string, q *query.BranchesRepos, snapshots []branchesReposSnapshot) {
	t.Helper()

	for i, snapshot := range snapshots {
		if q.List[i].Repos != snapshot.bitmap {
			t.Fatalf("%s: selectRepoSet replaced bitmap for branch %q", name, q.List[i].Branch)
		}
		if !q.List[i].Repos.Equals(snapshot.contents) {
			t.Fatalf("%s: selectRepoSet mutated bitmap for branch %q", name, q.List[i].Branch)
		}
	}
}

func assertSelectRepoSetBranchesRepos(t *testing.T, name string, shards []*rankedShard, q *query.BranchesRepos) query.Q {
	t.Helper()

	snapshots := snapshotBranchesRepos(q)
	want := matchingBranchesReposShards(shards, q)
	got, gotQuery := selectRepoSet(shards, q)

	if len(got) != len(want) {
		t.Fatalf("%s: selected %d shards, want %d", name, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: selected shard %d = %p, want %p", name, i, got[i], want[i])
		}
	}
	assertBranchesReposUnchanged(t, name, q, snapshots)

	return gotQuery
}

func TestSelectRepoSetBranchesRepos(t *testing.T) {
	shards, ids := newBranchesReposShards(5, 2)
	shards[4].repos = nil

	q := newBranchesReposQuery(branchesReposBenchmarkBranches)
	addBranchesReposIDs(q, 0, ids[0])
	addBranchesReposIDs(q, branchesReposBenchmarkBranches-1, ids[1])
	addBranchesReposIDs(q, 4, []uint32{ids[2][0]})
	addBranchesReposIDs(q, branchesReposBenchmarkBranches-1, []uint32{ids[2][0]})

	if gotQuery := assertSelectRepoSetBranchesRepos(t, "mixed membership", shards, q); gotQuery != q {
		t.Fatalf("selectRepoSet changed multi-branch query: got %s, want %s", gotQuery, q)
	}
}

func TestSelectRepoSetBranchesReposManyBranches(t *testing.T) {
	shards, ids := newBranchesReposShards(branchesReposBenchmarkBranches+1, 1)
	shards[len(shards)-1].repos = nil

	q := newBranchesReposQuery(branchesReposBenchmarkBranches)
	addBranchesReposIDs(q, 0, ids[0])
	addBranchesReposIDs(q, 4, ids[1])
	addBranchesReposIDs(q, branchesReposBenchmarkBranches-1, ids[2])
	addBranchesReposIDs(q, branchesReposBenchmarkBranches-1, ids[0])

	if gotQuery := assertSelectRepoSetBranchesRepos(t, "many branches", shards, q); gotQuery != q {
		t.Fatalf("selectRepoSet changed multi-branch query: got %s, want %s", gotQuery, q)
	}
}

func TestSelectRepoSetBranchesReposBuildsBloom(t *testing.T) {
	// 128 one-repository misses through 128 branch bitmaps make exactly 16,384
	// direct miss probes before the final 16 shards.
	const missShards = 128
	shards, ids := newBranchesReposShards(missShards+branchesReposMinimumRemainingShards, 1)
	q := newBranchesReposQuery(branchesReposBenchmarkBranches)
	for shard, shardIDs := range ids[missShards:] {
		branch := 0
		if shard%2 != 0 {
			branch = branchesReposBenchmarkBranches - 1
		}
		addBranchesReposIDs(q, branch, shardIDs)
	}

	// Keep the multi-branch query after filtering so its identity can be
	// checked below.
	shards[missShards].repos = append(shards[missShards].repos, &zoekt.Repository{ID: ids[0][0]})

	snapshots := snapshotBranchesRepos(q)
	sel := newBranchesReposSelector(q.List, 2)
	sel.missProbes = branchesReposMinimumMissProbes

	bloom := sel.maybeBuildBloom(shards[missShards:])
	if bloom == nil {
		t.Fatal("selector did not build a membership filter")
	}
	for _, shard := range shards[missShards:] {
		id := shard.repos[0].ID
		if !bloom.mayContain(id) {
			t.Fatalf("matching shard %d was rejected by filter", id)
		}
	}
	assertBranchesReposUnchanged(t, "bloom filter", q, snapshots)

	if gotQuery := assertSelectRepoSetBranchesRepos(t, "bloom filter", shards, q); gotQuery != q {
		t.Fatalf("selectRepoSet changed multi-branch query: got %s, want %s", gotQuery, q)
	}
}

func TestBranchesReposCanBuildBloomRejectsSaturatedFilter(t *testing.T) {
	if !branchesReposCanBuildBloom(branchesReposBloomMaxCardinality) {
		t.Fatal("filter capacity was rejected")
	}
	if branchesReposCanBuildBloom(branchesReposBloomMaxCardinality + 1) {
		t.Fatal("saturated filter was accepted")
	}
}

func TestBranchesReposSelectorSkipsNonProbingShards(t *testing.T) {
	const missShards = 128
	shards, _ := newBranchesReposShards(missShards+128, 1)
	for _, shard := range shards[missShards:] {
		shard.repos = nil
	}

	q := newBranchesReposQuery(branchesReposBenchmarkBranches)
	q.List[0].Repos.Add(100_000)
	q.List[branchesReposBenchmarkBranches-1].Repos.Add(200_000)
	sel := newBranchesReposSelector(q.List, 2)
	sel.missProbes = branchesReposMinimumMissProbes

	if sel.maybeBuildBloom(shards[missShards:]) != nil {
		t.Fatal("selector built a filter despite only unlisted remaining shards")
	}
	if !sel.settled {
		t.Fatal("selector did not settle without known remaining shards")
	}

	if gotQuery := assertSelectRepoSetBranchesRepos(t, "unlisted remaining shards", shards, q); gotQuery != q {
		t.Fatalf("selectRepoSet changed multi-branch query: got %s, want %s", gotQuery, q)
	}

	for _, shard := range shards[missShards:] {
		shard.repos = []*zoekt.Repository{}
	}
	emptySelector := newBranchesReposSelector(q.List, 2)
	emptySelector.missProbes = branchesReposMinimumMissProbes
	if emptySelector.maybeBuildBloom(shards[missShards:]) != nil {
		t.Fatal("selector built a filter despite only empty remaining shard lists")
	}
}

func TestSelectRepoSetBranchesReposEmptyBitmaps(t *testing.T) {
	shards, _ := newBranchesReposShards(branchesReposBenchmarkBranches+100, 1)
	q := newBranchesReposQuery(branchesReposBenchmarkBranches)

	gotQuery := assertSelectRepoSetBranchesRepos(t, "empty bitmaps", shards, q)
	constant, ok := gotQuery.(*query.Const)
	if !ok || constant.Value {
		t.Fatalf("empty branch repository set returned %s, want FALSE", gotQuery)
	}
}

func TestSelectRepoSetBranchesReposDifferential(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 2))

	for testCase := range 1_500 {
		branches := 2 + random.IntN(15)
		shardCount := 1 + random.IntN(32)
		if testCase%10 == 0 {
			branches = branchesReposBenchmarkBranches
			shardCount = branchesReposBenchmarkBranches + 1 + random.IntN(branchesReposBenchmarkBranches)
		}

		shards, ids := newBranchesReposShards(shardCount, 1+random.IntN(branchesReposBenchmarkRepos))
		q := newBranchesReposQuery(branches)
		for _, shardIDs := range ids {
			for _, id := range shardIDs {
				if random.IntN(4) == 0 {
					addBranchesReposIDs(q, random.IntN(branches), []uint32{id})
				}
			}
		}
		for branch := range q.List {
			for range random.IntN(3) {
				q.List[branch].Repos.Add(uint32(random.Uint64()))
			}
		}
		for shard := range shards {
			if random.IntN(16) == 0 {
				shards[shard].repos = nil
			}
		}

		assertSelectRepoSetBranchesRepos(t, fmt.Sprintf("random case %d", testCase), shards, q)
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
// miss work to repay building a membership filter.
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

// BenchmarkSelectRepoSetBranchesReposDistributedBitmapsModerateShards covers
// sparse bitmaps with 2,048 roaring containers each. The scan reaches the
// adaptive threshold, but visiting every requested ID cannot repay itself
// across only 128 miss shards.
func BenchmarkSelectRepoSetBranchesReposDistributedBitmapsModerateShards(b *testing.B) {
	shards, _ := newBranchesReposShards(128, branchesReposBenchmarkRepos)
	q := newBranchesReposQuery(branchesReposBenchmarkBranches)

	for branch := range q.List {
		for container := uint32(1); container <= branchesReposBenchmarkContainers; container++ {
			q.List[branch].Repos.Add(container<<16 | uint32(branch))
		}
	}

	benchmarkSelectRepoSetBranchesRepos(b, shards, q, 0)
}

// BenchmarkSelectRepoSetBranchesReposUnlistedShards covers a failed shard-list
// lookup after enough known misses to reach the filter threshold. Unlisted
// shards skip membership checks and must not repay filter setup.
func BenchmarkSelectRepoSetBranchesReposUnlistedShards(b *testing.B) {
	const knownMissShards = 32
	shards, _ := newBranchesReposShards(10_000, branchesReposBenchmarkRepos)
	for _, shard := range shards[knownMissShards:] {
		shard.repos = nil
	}
	q := newBranchesReposQuery(branchesReposBenchmarkBranches)
	for branch := range q.List {
		for id := uint32(0); id < 100; id++ {
			q.List[branch].Repos.Add(100_000 + uint32(branch)*100 + id)
		}
	}

	benchmarkSelectRepoSetBranchesRepos(b, shards, q, len(shards)-knownMissShards)
}

// BenchmarkSelectRepoSetBranchesReposFirstBranchWithDistributedBitmap covers
// a cheap first-branch hit path plus a large nonmatching bitmap. Setup must
// retain the direct path rather than build a saturated filter for every query ID.
func BenchmarkSelectRepoSetBranchesReposFirstBranchWithDistributedBitmap(b *testing.B) {
	shards, ids := newBranchesReposShards(10_000, branchesReposBenchmarkRepos)
	q := newBranchesReposQuery(branchesReposBenchmarkBranches)
	for _, shardIDs := range ids {
		addBranchesReposIDs(q, 0, shardIDs)
	}
	for container := uint32(1); container <= branchesReposBenchmarkContainers; container++ {
		q.List[1].Repos.Add(container<<16 | 1)
	}

	benchmarkSelectRepoSetBranchesRepos(b, shards, q, len(shards))
}

// BenchmarkSelectRepoSetBranchesReposLatePrefixThenFirst covers a query whose
// early final-branch matches are followed by a much larger first-branch run.
// The selector must retain the recently matching branch across that suffix.
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
// direct path. The miss prefix must not make that suffix allocate.
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
// The observed miss work must still justify filtering the remaining shards.
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
