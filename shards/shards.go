// Copyright 2016 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package shards

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"golang.org/x/net/trace"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
)

type rankedShard struct {
	zoekt.Searcher
	rank uint16
}

type shardedOp struct {
	ctx     context.Context
	q       query.Q
	opts    *zoekt.SearchOptions
	op      func(rankedShard)
	shards  []rankedShard
	results chan *shardResult
}

type shardedSearcher struct {
	ops    chan *shardedOp
	mu     sync.RWMutex
	shards map[string]rankedShard
	sorted []rankedShard
}

func newShardedSearcher(n int64) *shardedSearcher {
	ss := &shardedSearcher{
		ops:    make(chan *shardedOp),
		shards: make(map[string]rankedShard),
	}

	go ss.work(runtime.NumCPU() * 8)

	return ss
}

// NewDirectorySearcher returns a searcher instance that loads all
// shards corresponding to a glob into memory.
func NewDirectorySearcher(dir string) (zoekt.Searcher, error) {
	ss := newShardedSearcher(int64(runtime.NumCPU()))
	_, err := NewDirectoryWatcher(dir, &baseLoader{ss: ss})
	if err != nil {
		return nil, err
	}
	return &typeRepoSearcher{ss}, nil
}

type baseLoader struct {
	ss *shardedSearcher
}

func (l *baseLoader) load(key string) {
	shard, err := loadShard(key)
	if err != nil {
		log.Printf("reloading: %s, err %v ", key, err)
		return
	}

	l.ss.replace(key, shard)
}

func (l *baseLoader) drop(key string) {
	l.ss.replace(key, nil)
}

func (l *baseLoader) sort() {
	l.ss.sort()
}

func (ss *shardedSearcher) String() string {
	return "shardedSearcher"
}

// Close closes references to open files. It may be called only once.
func (ss *shardedSearcher) Close() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	defer close(ss.ops)
	for _, s := range ss.sorted {
		s.Close()
	}
}

func (ss *shardedSearcher) Search(ctx context.Context, q query.Q, opts *zoekt.SearchOptions) (sr *zoekt.SearchResult, err error) {
	tr := trace.New("shardedSearcher.Search", "")
	tr.LazyLog(q, true)
	tr.LazyPrintf("opts: %+v", opts)
	defer func() {
		if sr != nil {
			tr.LazyPrintf("num files: %d", len(sr.Files))
			tr.LazyPrintf("stats: %+v", sr.Stats)
		}
		if err != nil {
			tr.LazyPrintf("error: %v", err)
			tr.SetError()
		}
		tr.Finish()
	}()

	start := time.Now()

	aggregate := &zoekt.SearchResult{
		RepoURLs:      map[string]string{},
		LineFragments: map[string]string{},
	}

	ss.mu.RLock()
	defer ss.mu.RUnlock()

	tr.LazyPrintf("acquired read lock")
	aggregate.Wait = time.Since(start)
	start = time.Now()

	var childCtx context.Context
	var cancel context.CancelFunc
	if opts.MaxWallTime == 0 {
		childCtx, cancel = context.WithCancel(ctx)
	} else {
		childCtx, cancel = context.WithTimeout(ctx, opts.MaxWallTime)
	}

	defer cancel()

	shards := ss.getShards()
	op := shardedOp{
		ctx:     childCtx,
		q:       q,
		opts:    opts,
		shards:  shards,
		results: make(chan *shardResult),
	}

	op.op = op.searchOne
	ss.ops <- &op

	for range shards {
		r := <-op.results
		if r.err != nil {
			return nil, r.err
		}
		aggregate.Files = append(aggregate.Files, r.sr.Files...)
		aggregate.Stats.Add(r.sr.Stats)

		if len(r.sr.Files) > 0 {
			for k, v := range r.sr.RepoURLs {
				aggregate.RepoURLs[k] = v
			}
			for k, v := range r.sr.LineFragments {
				aggregate.LineFragments[k] = v
			}
		}

		if cancel != nil && opts.TotalMaxMatchCount > 0 && aggregate.Stats.MatchCount > opts.TotalMaxMatchCount {
			cancel()
			cancel = nil
		}
	}

	zoekt.SortFilesByScore(aggregate.Files)
	if max := opts.MaxDocDisplayCount; max > 0 && len(aggregate.Files) > max {
		aggregate.Files = aggregate.Files[:max]
	}
	for i := range aggregate.Files {
		copySlice(&aggregate.Files[i].Content)
		copySlice(&aggregate.Files[i].Checksum)
		for l := range aggregate.Files[i].LineMatches {
			copySlice(&aggregate.Files[i].LineMatches[l].Line)
		}
	}

	aggregate.Duration = time.Since(start)
	return aggregate, nil
}

func copySlice(src *[]byte) {
	dst := make([]byte, len(*src))
	copy(dst, *src)
	*src = dst
}

type shardResult struct {
	sr  *zoekt.SearchResult
	rl  *zoekt.RepoList
	err error
}

func (op *shardedOp) searchOne(s rankedShard) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("crashed shard: %s: %s, %s", s.String(), r, debug.Stack())

			var r zoekt.SearchResult
			r.Stats.Crashes = 1
			op.results <- &shardResult{sr: &r}
		}
	}()
	ms, err := s.Search(op.ctx, op.q, op.opts)
	op.results <- &shardResult{sr: ms, err: err}
}

func (op *shardedOp) listOne(s rankedShard) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("crashed shard: %s: %s, %s", s.String(), r, debug.Stack())
			op.results <- &shardResult{rl: &zoekt.RepoList{Crashes: 1}}
		}
	}()
	rl, err := s.List(op.ctx, op.q)
	op.results <- &shardResult{rl: rl, err: err}
}

func (ss *shardedSearcher) List(ctx context.Context, r query.Q) (rl *zoekt.RepoList, err error) {
	tr := trace.New("shardedSearcher.List", "")
	tr.LazyLog(r, true)
	defer func() {
		if rl != nil {
			tr.LazyPrintf("repos size: %d", len(rl.Repos))
			tr.LazyPrintf("crashes: %d", rl.Crashes)
		}
		if err != nil {
			tr.LazyPrintf("error: %v", err)
			tr.SetError()
		}
		tr.Finish()
	}()

	ss.mu.RLock()
	defer ss.mu.RUnlock()

	tr.LazyPrintf("acquired read lock")

	shards := ss.getShards()
	tr.LazyPrintf("shardCount: %d", len(shards))

	op := shardedOp{
		ctx:     ctx,
		q:       r,
		shards:  shards,
		results: make(chan *shardResult),
	}

	op.op = op.listOne
	ss.ops <- &op

	crashes := 0
	uniq := map[string]*zoekt.RepoListEntry{}

	for range shards {
		r := <-op.results
		if r.err != nil {
			return nil, r.err
		}
		crashes += r.rl.Crashes
		for _, r := range r.rl.Repos {
			prev, ok := uniq[r.Repository.Name]
			if !ok {
				cp := *r
				uniq[r.Repository.Name] = &cp
			} else {
				prev.Stats.Add(&r.Stats)
			}
		}
	}

	aggregate := make([]*zoekt.RepoListEntry, 0, len(uniq))
	for _, v := range uniq {
		aggregate = append(aggregate, v)
	}
	return &zoekt.RepoList{
		Repos:   aggregate,
		Crashes: crashes,
	}, nil
}

// getShards returns the currently loaded shards. The shards must be
// accessed under a rlock call. The shards are sorted by decreasing
// rank.
func (s *shardedSearcher) getShards() []rankedShard {
	return s.sorted
}

func shardRank(s zoekt.Searcher) uint16 {
	q := query.Repo{}
	result, err := s.List(context.Background(), &q)
	if err != nil {
		return 0
	}
	if len(result.Repos) == 0 {
		return 0
	}
	return result.Repos[0].Repository.Rank
}

func (s *shardedSearcher) replace(key string, shard zoekt.Searcher) {
	s.mu.Lock()
	defer s.mu.Unlock()

	old := s.shards[key]
	if old.Searcher != nil {
		old.Close()
	}

	if shard == nil {
		delete(s.shards, key)
	} else {
		s.shards[key] = rankedShard{
			rank:     shardRank(shard),
			Searcher: shard,
		}
	}
}

func (s *shardedSearcher) sort() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sorted == nil {
		s.sorted = make([]rankedShard, 0, len(s.shards))
	}

	s.sorted = s.sorted[:0]
	for _, shard := range s.shards {
		s.sorted = append(s.sorted, shard)
	}

	sort.Slice(s.sorted, func(i, j int) bool {
		return s.sorted[i].rank > s.sorted[j].rank
	})
}

func (ss *shardedSearcher) work(n int) {
	type shardOp struct {
		*shardedOp
		shard int
	}

	ch := make(chan *shardOp)
	defer close(ch)

	for i := 0; i < n; i++ {
		go func() {
			for op := range ch {
				op.op(op.shards[op.shard])
			}
		}()
	}

	for op := range ss.ops {
		for i := range op.shards {
			ch <- &shardOp{op, i}
		}
	}
}

func loadShard(fn string) (zoekt.Searcher, error) {
	f, err := os.Open(fn)
	if err != nil {
		return nil, err
	}

	iFile, err := zoekt.NewIndexFile(f)
	if err != nil {
		return nil, err
	}
	s, err := zoekt.NewSearcher(iFile)
	if err != nil {
		iFile.Close()
		return nil, fmt.Errorf("NewSearcher(%s): %v", fn, err)
	}

	return s, nil
}
