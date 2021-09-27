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
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/stream"
	"github.com/google/zoekt/trace"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricShardsLoaded = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zoekt_shards_loaded",
		Help: "The number of shards currently loaded",
	})
	metricShardsLoadedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "zoekt_shards_loaded_total",
		Help: "The total number of shards loaded",
	})
	metricShardsLoadFailedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "zoekt_shards_load_failed_total",
		Help: "The total number of shard loads that failed",
	})

	metricSearchRunning = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zoekt_search_running",
		Help: "The number of concurrent search requests running",
	})
	metricSearchShardRunning = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zoekt_search_shard_running",
		Help: "The number of concurrent search requests in a shard running",
	})
	metricSearchFailedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "zoekt_search_failed_total",
		Help: "The total number of search requests that failed",
	})
	metricSearchDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "zoekt_search_duration_seconds",
		Help:    "The duration a search request took in seconds",
		Buckets: prometheus.DefBuckets, // DefBuckets good for service timings
	})

	// A Counter per Stat. Name should match field in zoekt.Stats.
	metricSearchContentBytesLoadedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "zoekt_search_content_loaded_bytes_total",
		Help: "Total amount of I/O for reading contents",
	})
	metricSearchIndexBytesLoadedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "zoekt_search_index_loaded_bytes_total",
		Help: "Total amount of I/O for reading from index",
	})
	metricSearchCrashesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "zoekt_search_crashes_total",
		Help: "Total number of search shards that had a crash",
	})
	metricSearchFileCountTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "zoekt_search_file_count_total",
		Help: "Total number of files containing a match",
	})
	metricSearchShardFilesConsideredTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "zoekt_search_shard_files_considered_total",
		Help: "Total number of files in shards that we considered",
	})
	metricSearchFilesConsideredTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "zoekt_search_files_considered_total",
		Help: "Total files that we evaluated. Equivalent to files for which all atom matches (including negations) evaluated to true",
	})
	metricSearchFilesLoadedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "zoekt_search_files_loaded_total",
		Help: "Total files for which we loaded file content to verify substring matches",
	})
	metricSearchFilesSkippedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "zoekt_search_files_skipped_total",
		Help: "Total candidate files whose contents weren't examined because we gathered enough matches",
	})
	metricSearchShardsSkippedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "zoekt_search_shards_skipped_total",
		Help: "Total shards that we did not process because a query was canceled",
	})
	metricSearchMatchCountTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "zoekt_search_match_count_total",
		Help: "Total number of non-overlapping matches",
	})
	metricSearchNgramMatchesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "zoekt_search_ngram_matches_total",
		Help: "Total number of candidate matches as a result of searching ngrams",
	})

	metricListRunning = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zoekt_list_running",
		Help: "The number of concurrent list requests running",
	})
	metricListShardRunning = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zoekt_list_shard_running",
		Help: "The number of concurrent list requests in a shard running",
	})
	metricShardCloseDurationSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "zoekt_shard_close_duration_seconds",
		Help:    "The time it takes to close a Searcher.",
		Buckets: []float64{0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 30},
	})
	metricRankCacheUpdateDurationSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "zoekt_rank_cache_update_duration_seconds",
		Help:    "The time it takes to update the shard cache with new ranked shards.",
		Buckets: []float64{0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 30},
	})

	metricListAllRepos = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zoekt_list_all_stats_repos",
		Help: "The last List(true) value for RepoStats.Repos. Repos is used for aggregrating the number of repositories.",
	})
	metricListAllShards = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zoekt_list_all_stats_shards",
		Help: "The last List(true) value for RepoStats.Shards. Shards is the total number of search shards.",
	})
	metricListAllDocuments = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zoekt_list_all_stats_documents",
		Help: "The last List(true) value for RepoStats.Documents. Documents holds the number of documents or files.",
	})
	metricListAllIndexBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zoekt_list_all_stats_index_bytes",
		Help: "The last List(true) value for RepoStats.IndexBytes. IndexBytes is the amount of RAM used for index overhead.",
	})
	metricListAllContentBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zoekt_list_all_stats_content_bytes",
		Help: "The last List(true) value for RepoStats.ContentBytes. ContentBytes is the amount of RAM used for raw content.",
	})
	metricListAllNewLinesCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zoekt_list_all_stats_new_lines_count",
		Help: "The last List(true) value for RepoStats.NewLinesCount.",
	})
	metricListAllDefaultBranchNewLinesCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zoekt_list_all_stats_default_branch_new_lines_count",
		Help: "The last List(true) value for RepoStats.DefaultBranchNewLinesCount.",
	})
	metricListAllOtherBranchesNewLinesCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zoekt_list_all_stats_other_branches_new_lines_count",
		Help: "The last List(true) value for RepoStats.OtherBranchesNewLinesCount.",
	})
)

type rankedShard struct {
	zoekt.Searcher

	priority float64

	// We have out of band ranking on compound shards which can change even if
	// the shard file does not. So we compute a rank in getShards. We store
	// names here to avoid the cost of List in the search request path.
	repos []*zoekt.Repository
}

type shardedSearcher struct {
	// Limit the number of parallel queries. Since searching is
	// CPU bound, we can't do better than #CPU queries in
	// parallel.  If we do so, we just create more memory
	// pressure.
	sched scheduler

	shards map[string]rankedShard

	rankedLock sync.Mutex // guards ranked
	ranked     []rankedShard
}

func newShardedSearcher(n int64) *shardedSearcher {
	ss := &shardedSearcher{
		shards: make(map[string]rankedShard),
		sched:  newScheduler(n),
	}
	return ss
}

// NewDirectorySearcher returns a searcher instance that loads all
// shards corresponding to a glob into memory.
func NewDirectorySearcher(dir string) (zoekt.Streamer, error) {
	ss := newShardedSearcher(int64(runtime.GOMAXPROCS(0)))
	tl := &loader{
		ss: ss,
	}
	dw, err := NewDirectoryWatcher(dir, tl)
	if err != nil {
		return nil, err
	}

	ds := &directorySearcher{
		Streamer:         ss,
		directoryWatcher: dw,
	}

	return &typeRepoSearcher{Streamer: ds}, nil
}

type directorySearcher struct {
	zoekt.Streamer

	directoryWatcher *DirectoryWatcher
}

func (s *directorySearcher) Close() {
	// We need to Stop directoryWatcher first since it calls load/unload on
	// Searcher.
	s.directoryWatcher.Stop()
	s.Streamer.Close()
}

type loader struct {
	ss *shardedSearcher
}

func (tl *loader) load(key string) {
	shard, err := loadShard(key)
	if err != nil {
		metricShardsLoadFailedTotal.Inc()
		log.Printf("reloading: %s, err %v ", key, err)
		return
	}

	metricShardsLoadedTotal.Inc()
	tl.ss.replace(key, shard)
}

func (tl *loader) drop(key string) {
	tl.ss.replace(key, nil)
}

func (ss *shardedSearcher) String() string {
	return "shardedSearcher"
}

// Close closes references to open files. It may be called only once.
func (ss *shardedSearcher) Close() {
	proc := ss.sched.Exclusive()
	defer proc.Release()
	for _, s := range ss.shards {
		s.Close()
	}
	ss.shards = make(map[string]rankedShard)
}

func selectRepoSet(shards []rankedShard, q query.Q) ([]rankedShard, query.Q) {
	and, ok := q.(*query.And)
	if !ok {
		return shards, q
	}

	// (and (reposet ...) (q))
	// (and true (q)) with a filtered shards
	// (and false) // noop

	// (and (repobranches ...) (q))
	// (and (repobranches ...) (q))

	hasReposForPredicate := func(pred func(repo *zoekt.Repository) bool) func(repos []*zoekt.Repository) (any, all bool) {
		return func(repos []*zoekt.Repository) (any, all bool) {
			any = false
			all = true
			for _, repo := range repos {
				b := pred(repo)
				any = any || b
				all = all && b
			}
			return any, all
		}
	}

	for i, c := range and.Children {
		var setSize int
		var hasRepos func([]*zoekt.Repository) (bool, bool)
		switch setQuery := c.(type) {
		case *query.RepoSet:
			setSize = len(setQuery.Set)
			hasRepos = hasReposForPredicate(func(repo *zoekt.Repository) bool {
				return setQuery.Contains(repo.Name, repo.ID)
			})
		case *query.RepoBranches:
			setSize = len(setQuery.Set)
			for _, ids := range setQuery.IDs {
				if ids != nil {
					setSize += int(ids.GetCardinality())
				}
			}

			if len(setQuery.Set) > 0 {
				hasRepos = hasReposForPredicate(func(repo *zoekt.Repository) bool {
					return len(setQuery.Set[repo.Name]) > 0
				})
			} else {
				hasRepos = hasReposForPredicate(func(repo *zoekt.Repository) bool {
					for _, ids := range setQuery.IDs {
						if ids != nil && ids.Contains(repo.ID) {
							return true
						}
					}
					return false
				})
			}
		default:
			continue
		}

		// setSize may be larger than the number of shards we have. The size of
		// filtered is bounded by min(len(set), len(shards))
		if setSize > len(shards) {
			setSize = len(shards)
		}

		filtered := make([]rankedShard, 0, setSize)
		filteredAll := true

		for _, s := range shards {
			if any, all := hasRepos(s.repos); any {
				filtered = append(filtered, s)
				filteredAll = filteredAll && all
			}
		}

		// We don't need to adjust the query since we are returning an empty set
		// of shards to search.
		if len(filtered) == 0 {
			return filtered, and
		}

		// We can't simplify the query since we are searching shards which contain
		// repos we aren't supposed to search.
		if !filteredAll {
			return filtered, and
		}

		// This optimization allows us to avoid the work done by
		// indexData.simplify for each shard.
		//
		// For example if our query is (and (reposet foo bar) (content baz))
		// then at this point filtered is [foo bar] and q is the same. For each
		// shard indexData.simplify will simplify to (and true (content baz)) ->
		// (content baz). This work can be done now once, rather than per shard.
		if _, ok := c.(*query.RepoSet); ok {
			and.Children[i] = &query.Const{Value: true}
			return filtered, query.Simplify(and)
		}
		if b, ok := c.(*query.RepoBranches); ok {
			// We can only replace if all the repos want the same branches.
			if len(b.Set) > 0 {
				want := b.Set[filtered[0].repos[0].Name]
				for _, s := range filtered {
					for _, repo := range s.repos {
						if !strSliceEqual(want, b.Set[repo.Name]) {
							return filtered, and
						}
					}
				}
			} else if len(b.IDs) != 1 {
				return filtered, and
			}

			// Every repo wants the same branches, so we can replace RepoBranches
			// with a list of branch queries.
			repo := filtered[0].repos[0]
			and.Children[i] = b.Branches(repo.Name, repo.ID)
			return filtered, query.Simplify(and)
		}

		// Stop after first RepoSet, otherwise we might append duplicate
		// shards to `filtered`
		return filtered, and
	}

	return shards, and
}

func (ss *shardedSearcher) Search(ctx context.Context, q query.Q, opts *zoekt.SearchOptions) (sr *zoekt.SearchResult, err error) {
	tr, ctx := trace.New(ctx, "shardedSearcher.Search", "")
	defer func() {
		if sr != nil {
			tr.LazyPrintf("num files: %d", len(sr.Files))
			tr.LazyPrintf("stats: %+v", sr.Stats)
		}
		tr.Finish()
	}()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	aggregate := struct {
		sync.Mutex
		*zoekt.SearchResult
	}{
		SearchResult: &zoekt.SearchResult{
			RepoURLs:      map[string]string{},
			LineFragments: map[string]string{},
		},
	}

	start := time.Now()
	proc, err := ss.sched.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer proc.Release()
	tr.LazyPrintf("acquired process")
	aggregate.Wait = time.Since(start)
	start = time.Now()

	err = ss.streamSearch(ctx, proc, q, opts, stream.SenderFunc(func(r *zoekt.SearchResult) {
		aggregate.Lock()
		defer aggregate.Unlock()

		aggregate.Stats.Add(r.Stats)

		if len(r.Files) > 0 {
			aggregate.Files = append(aggregate.Files, r.Files...)

			for k, v := range r.RepoURLs {
				aggregate.RepoURLs[k] = v
			}
			for k, v := range r.LineFragments {
				aggregate.LineFragments[k] = v
			}
		}

		if cancel != nil && opts.TotalMaxMatchCount > 0 && aggregate.Stats.MatchCount > opts.TotalMaxMatchCount {
			cancel()
			cancel = nil
		}
	}))
	if err != nil {
		return nil, err
	}

	zoekt.SortFilesByScore(aggregate.Files)
	if max := opts.MaxDocDisplayCount; max > 0 && len(aggregate.Files) > max {
		aggregate.Files = aggregate.Files[:max]
	}
	copyFiles(aggregate.SearchResult)

	aggregate.Duration = time.Since(start)
	return aggregate.SearchResult, nil
}

func (ss *shardedSearcher) StreamSearch(ctx context.Context, q query.Q, opts *zoekt.SearchOptions, sender zoekt.Sender) (err error) {
	tr, ctx := trace.New(ctx, "shardedSearcher.StreamSearch", "")
	defer func() {
		if err != nil {
			tr.LazyPrintf("error: %v", err)
			tr.SetError(err)
		}
		tr.Finish()
	}()

	start := time.Now()
	proc, err := ss.sched.Acquire(ctx)
	if err != nil {
		return err
	}
	defer proc.Release()
	tr.LazyPrintf("acquired process")
	sender.Send(&zoekt.SearchResult{
		Stats: zoekt.Stats{
			Wait: time.Since(start),
		},
	})

	return ss.streamSearch(ctx, proc, q, opts, stream.SenderFunc(func(event *zoekt.SearchResult) {
		copyFiles(event)
		sender.Send(event)
	}))
}

func (ss *shardedSearcher) streamSearch(ctx context.Context, proc *process, q query.Q, opts *zoekt.SearchOptions, sender zoekt.Sender) (err error) {
	tr, ctx := trace.New(ctx, "shardedSearcher.streamSearch", "")
	tr.LazyLog(q, true)
	tr.LazyPrintf("opts: %+v", opts)
	overallStart := time.Now()
	metricSearchRunning.Inc()
	defer func() {
		metricSearchRunning.Dec()
		metricSearchDuration.Observe(time.Since(overallStart).Seconds())
		if err != nil {
			metricSearchFailedTotal.Inc()

			tr.LazyPrintf("error: %v", err)
			tr.SetError(err)
		}
		tr.Finish()
	}()

	shards := ss.getShards()
	tr.LazyPrintf("before selectRepoSet shards:%d", len(shards))
	shards, q = selectRepoSet(shards, q)
	tr.LazyPrintf("after selectRepoSet shards:%d %s", len(shards), q)

	var childCtx context.Context
	var cancel context.CancelFunc
	if opts.MaxWallTime == 0 {
		childCtx, cancel = context.WithCancel(ctx)
	} else {
		childCtx, cancel = context.WithTimeout(ctx, opts.MaxWallTime)
	}

	defer cancel()

	mu := sync.Mutex{}
	pendingPriorities := prioritySlice{}

	g, ctx := errgroup.WithContext(childCtx)

	// For each query, throttle the number of parallel
	// actions. Since searching is mostly CPU bound, we limit the
	// number of parallel searches. This reduces the peak working
	// set, which hopefully stops https://cs.bazel.build from crashing
	// when looking for the string "com".
	//
	// We do yield inside of the feeder. This means we could have num_workers +
	// cap(feeder) searches run while yield blocks. However, doing it this way
	// avoids needing to have synchronization in yield, so is done for
	// simplicity.
	feeder := make(chan rankedShard, runtime.GOMAXPROCS(0))
	g.Go(func() error {
		defer close(feeder)
		// Note: shards is sorted in order of descending priority.
		for _, s := range shards {
			// We let searchOneShard handle context errors.
			_ = proc.Yield(ctx)
			mu.Lock()
			pendingPriorities.append(s.priority)
			mu.Unlock()
			feeder <- s
		}
		return nil
	})
	for i := 0; i < runtime.GOMAXPROCS(0); i++ {
		g.Go(func() error {
			for s := range feeder {
				err := searchOneShard(ctx, s, q, opts, stream.SenderFunc(func(sr *zoekt.SearchResult) {
					metricSearchContentBytesLoadedTotal.Add(float64(sr.Stats.ContentBytesLoaded))
					metricSearchIndexBytesLoadedTotal.Add(float64(sr.Stats.IndexBytesLoaded))
					metricSearchCrashesTotal.Add(float64(sr.Stats.Crashes))
					metricSearchFileCountTotal.Add(float64(sr.Stats.FileCount))
					metricSearchShardFilesConsideredTotal.Add(float64(sr.Stats.ShardFilesConsidered))
					metricSearchFilesConsideredTotal.Add(float64(sr.Stats.FilesConsidered))
					metricSearchFilesLoadedTotal.Add(float64(sr.Stats.FilesLoaded))
					metricSearchFilesSkippedTotal.Add(float64(sr.Stats.FilesSkipped))
					metricSearchShardsSkippedTotal.Add(float64(sr.Stats.ShardsSkipped))
					metricSearchMatchCountTotal.Add(float64(sr.Stats.MatchCount))
					metricSearchNgramMatchesTotal.Add(float64(sr.Stats.NgramMatches))

					// MaxPendingPriority *cannot* be this result's Priority, because
					// the priority is removed before computing max() and calling sender.Send.
					// (There may be duplicate priorities, though-- that's fine.) A PendingShard
					// is one that has not entered this critical section and sent its results.
					//
					// Note that there are at least two layers above this implementing streamSearch
					// or StreamSearch that also take a lock for the entirety of the Send() operation.
					//
					// This is to avoid a potential race between shards sending back results
					// if the priority were removed before sending without a lock:
					// 1) shard A (pri 1), B (pri 2), C (pri 3) dispatch, pendingPriorities = [1, 2, 3]
					// 2) C completes and removes itself from the priority list, pP = [1, 2]
					// 3) B completes, removes itself, computes max, *and sends results* as maxPendingPriority=1,
					//    indicating that no future results will come from a lower-ordered shard, pP = [1]
					// 4) A completes, removes itself, computes max, and sends results with maxPP=-Inf, indicating
					//    that the stream is finished (?)
					// 5) C finally wakes up, computes max, and sends results with maxPP=-Inf, but with priority=3.
					mu.Lock()
					pendingPriorities.remove(s.priority)
					sr.Progress.MaxPendingPriority = pendingPriorities.max()
					sr.Progress.Priority = s.priority
					sender.Send(sr)
					mu.Unlock()
				}))
				if err != nil {
					mu.Lock()
					pendingPriorities.remove(s.priority)
					mu.Unlock()
					return err
				}
			}
			return nil
		})
	}
	return g.Wait()
}

func copySlice(src *[]byte) {
	dst := make([]byte, len(*src))
	copy(dst, *src)
	*src = dst
}

// copyFiles must be protected by shardedSearcher.sched.
func copyFiles(sr *zoekt.SearchResult) {
	for i := range sr.Files {
		copySlice(&sr.Files[i].Content)
		copySlice(&sr.Files[i].Checksum)
		for l := range sr.Files[i].LineMatches {
			copySlice(&sr.Files[i].LineMatches[l].Line)
		}
	}
}

func searchOneShard(ctx context.Context, s zoekt.Searcher, q query.Q, opts *zoekt.SearchOptions, sender zoekt.Sender) error {
	metricSearchShardRunning.Inc()
	defer func() {
		metricSearchShardRunning.Dec()
		if r := recover(); r != nil {
			log.Printf("crashed shard: %s: %s, %s", s.String(), r, debug.Stack())

			var r zoekt.SearchResult
			r.Stats.Crashes = 1
			sender.Send(&r)
		}
	}()

	ms, err := s.Search(ctx, q, opts)
	if err != nil {
		return err
	}
	sender.Send(ms)
	return nil
}

type shardListResult struct {
	rl  *zoekt.RepoList
	err error
}

func listOneShard(ctx context.Context, s zoekt.Searcher, q query.Q, opts *zoekt.ListOptions, sink chan shardListResult) {
	metricListShardRunning.Inc()
	defer func() {
		metricListShardRunning.Dec()
		if r := recover(); r != nil {
			log.Printf("crashed shard: %s: %s, %s", s.String(), r, debug.Stack())
			sink <- shardListResult{
				&zoekt.RepoList{Crashes: 1}, nil,
			}
		}
	}()

	ms, err := s.List(ctx, q, opts)
	sink <- shardListResult{ms, err}
}

func (ss *shardedSearcher) List(ctx context.Context, r query.Q, opts *zoekt.ListOptions) (rl *zoekt.RepoList, err error) {
	tr, ctx := trace.New(ctx, "shardedSearcher.List", "")
	tr.LazyLog(r, true)
	tr.LazyPrintf("opts: %s", opts)
	metricListRunning.Inc()
	defer func() {
		metricListRunning.Dec()
		if rl != nil {
			tr.LazyPrintf("repos size: %d", len(rl.Repos))
			tr.LazyPrintf("crashes: %d", rl.Crashes)
			tr.LazyPrintf("minimal size: %d", len(rl.Minimal))
		}
		if err != nil {
			tr.LazyPrintf("error: %v", err)
			tr.SetError(err)
		}
		tr.Finish()
	}()

	r = query.Simplify(r)
	isAll := false
	if c, ok := r.(*query.Const); ok {
		isAll = c.Value
	}

	proc, err := ss.sched.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer proc.Release()
	tr.LazyPrintf("acquired process")

	shards := ss.getShards()
	shardCount := len(shards)
	all := make(chan shardListResult, shardCount)
	tr.LazyPrintf("shardCount: %d", len(shards))

	feeder := make(chan zoekt.Searcher, len(shards))
	for _, s := range shards {
		feeder <- s
	}
	close(feeder)

	for i := 0; i < runtime.GOMAXPROCS(0); i++ {
		go func() {
			for s := range feeder {
				listOneShard(ctx, s, r, opts, all)
			}
		}()
	}

	agg := zoekt.RepoList{
		Minimal: map[uint32]*zoekt.MinimalRepoListEntry{},
	}

	uniq := map[string]*zoekt.RepoListEntry{}

	for range shards {
		r := <-all
		if r.err != nil {
			return nil, r.err
		}

		agg.Crashes += r.rl.Crashes

		for _, r := range r.rl.Repos {
			prev, ok := uniq[r.Repository.Name]
			if !ok {
				cp := *r // We need to copy because we mutate r.Stats when merging duplicates
				uniq[r.Repository.Name] = &cp
			} else {
				prev.Stats.Add(&r.Stats)
			}
		}

		for id, r := range r.rl.Minimal {
			_, ok := agg.Minimal[id]
			if !ok {
				agg.Minimal[id] = r
			}
		}
	}

	agg.Repos = make([]*zoekt.RepoListEntry, 0, len(uniq))
	for _, r := range uniq {
		agg.Repos = append(agg.Repos, r)
	}

	isMinimal := opts != nil && opts.Minimal
	if isAll && !isMinimal {
		reportListAllMetrics(agg.Repos)
	}

	return &agg, nil
}

func reportListAllMetrics(repos []*zoekt.RepoListEntry) {
	var stats zoekt.RepoStats
	for _, r := range repos {
		stats.Add(&r.Stats)
	}

	metricListAllRepos.Set(float64(stats.Repos))
	metricListAllIndexBytes.Set(float64(stats.IndexBytes))
	metricListAllContentBytes.Set(float64(stats.ContentBytes))
	metricListAllDocuments.Set(float64(stats.Documents))
	metricListAllShards.Set(float64(stats.Shards))
	metricListAllNewLinesCount.Set(float64(stats.NewLinesCount))
	metricListAllDefaultBranchNewLinesCount.Set(float64(stats.DefaultBranchNewLinesCount))
	metricListAllOtherBranchesNewLinesCount.Set(float64(stats.OtherBranchesNewLinesCount))
}

// getShards returns the currently loaded shards. The shards are sorted by decreasing
// rank and should not be mutated.
func (s *shardedSearcher) getShards() []rankedShard {
	start := time.Now()
	s.rankedLock.Lock()
	defer s.rankedLock.Unlock()
	if len(s.ranked) > 0 {
		metricRankCacheUpdateDurationSeconds.Observe(time.Since(start).Seconds())
		return s.ranked
	}

	// Holding rankedLock during the search ensures that we only perform
	// the sort once-- any blocked goroutines would take just as long to
	// perform the sort themselves.
	res := make([]rankedShard, 0, len(s.shards))
	for _, sh := range s.shards {
		res = append(res, sh)
	}
	sort.Slice(res, func(i, j int) bool {
		priorityDiff := res[i].priority - res[j].priority
		if priorityDiff != 0 {
			return priorityDiff > 0
		}
		if len(res[i].repos) == 0 || len(res[j].repos) == 0 {
			// Protect against empty names which can happen if we fail to List or
			// the shard is full of tombstones. Prefer the shard which has names.
			return len(res[i].repos) >= len(res[j].repos)
		}
		return res[i].repos[0].Name < res[j].repos[0].Name
	})

	s.ranked = res

	return res
}

func mkRankedShard(s zoekt.Searcher) rankedShard {
	q := query.Const{Value: true}
	result, err := s.List(context.Background(), &q, nil)
	if err != nil {
		return rankedShard{Searcher: s}
	}
	if len(result.Repos) == 0 {
		return rankedShard{Searcher: s}
	}

	var (
		maxPriority float64
		repos       = make([]*zoekt.Repository, 0, len(result.Repos))
	)
	for i := range result.Repos {
		repo := &result.Repos[i].Repository
		repos = append(repos, repo)
		if repo.RawConfig != nil {
			priority, _ := strconv.ParseFloat(repo.RawConfig["priority"], 64)
			if priority > maxPriority {
				maxPriority = priority
			}
		}
	}

	return rankedShard{
		Searcher: s,
		repos:    repos,
		priority: maxPriority,
	}
}

func (s *shardedSearcher) replace(key string, shard zoekt.Searcher) {
	var ranked rankedShard
	if shard != nil {
		ranked = mkRankedShard(shard)
	}

	proc := s.sched.Exclusive()

	old := s.shards[key]
	if shard == nil {
		delete(s.shards, key)
	} else {
		s.shards[key] = ranked
	}
	s.rankedLock.Lock()
	s.ranked = nil
	s.rankedLock.Unlock()

	proc.Release()

	if old.Searcher != nil {
		start := time.Now()
		old.Close()
		metricShardCloseDurationSeconds.Observe(time.Since(start).Seconds())
	}

	metricShardsLoaded.Set(float64(len(s.shards)))
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

func strSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// prioritySlice is a trivial implementation of an array that provides three
// things: appending a value, removing a value, and getting the array's max.
// Operations take O(n) time, which is acceptable because N is restricted to
// GOMAXPROCS (i.e., number of cpu cores) by the shardedSearcher interface.
type prioritySlice []float64

func (p *prioritySlice) append(pri float64) {
	*p = append(*p, pri)
}

func (p *prioritySlice) remove(pri float64) {
	for i, opri := range *p {
		if opri == pri {
			if i != len(*p)-1 {
				// swap to make this element the tail
				(*p)[i] = (*p)[len(*p)-1]
			}
			// pop the end off
			*p = (*p)[:len(*p)-1]
			break
		}
	}
}

func (p *prioritySlice) max() float64 {
	// remove() and max() could be combined, but this is easier to read and
	// the expected performance difference from the extra lock and loop is
	// almost certainly irrelevant.
	maxPri := math.Inf(-1)
	for _, pri := range *p {
		if pri > maxPri {
			maxPri = pri
		}
	}
	return maxPri
}
