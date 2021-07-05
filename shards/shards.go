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
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
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
)

type repositorer interface {
	Repository() *zoekt.Repository
}

type rankedShard struct {
	zoekt.Searcher
	// SOURCEGRAPH we want to search shards in the order of the name to match
	// up with how we sort results in graphqlbackend.
	name string
}

type shardedSearcher struct {
	// Limit the number of parallel queries. Since searching is
	// CPU bound, we can't do better than #CPU queries in
	// parallel.  If we do so, we just create more memory
	// pressure.
	sched scheduler

	shards map[string]rankedShard

	rankedVersion uint64
	ranked        []rankedShard

	priority map[string]float64

	// public is a set of public repository names.
	public map[string]struct{}
}

func newShardedSearcher(n int64) *shardedSearcher {
	ss := &shardedSearcher{
		shards:   make(map[string]rankedShard),
		sched:    newScheduler(n),
		priority: make(map[string]float64),
		public:   make(map[string]struct{}),
	}
	return ss
}

// readPublicSet reads public.txt and acquires an exclusive lock to hydrate
// shardedSearcher with a set of public repos.
func (ss *shardedSearcher) readPublicSet(dir string, lastMtime time.Time) time.Time {
	publicPath := filepath.Join(dir, "public.txt")

	st, err := os.Stat(publicPath)
	if err != nil {
		log.Printf("watchPublic: os.Stat: %s", err)
		return lastMtime // ignore missing file errors
	}
	if st.ModTime() == lastMtime {
		log.Printf("watchPublic: public.txt unchanged, no need to reload")
		return lastMtime // file is unchanged
	}

	file, err := os.Open(publicPath)
	if err != nil {
		log.Printf("watchPublic: os.Open: %s", err)
		return st.ModTime()
	}
	defer file.Close()

	var publicRepos []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		repo := strings.TrimSpace(scanner.Text())
		if repo == "" {
			continue
		}
		publicRepos = append(publicRepos, repo)
	}
	if err := scanner.Err(); err != nil {
		log.Printf("watchPublic: scanner error: %s", err)
		return st.ModTime()
	}

	log.Printf("Reloading public.txt. %d public repositories", len(publicRepos))

	publicReposSet := make(map[string]struct{}, len(publicRepos))
	for _, r := range publicRepos {
		publicReposSet[r] = struct{}{}
	}
	proc := ss.sched.Exclusive()
	defer proc.Release()
	ss.public = publicReposSet

	return st.ModTime()
}

// watchPublic watches public.txt in dir by calling readPublicSet in regular
// intervals. The first call to readPublicSet is immediate.
func (ss *shardedSearcher) watchPublic(dir string, done chan struct{}) {
	// instant tick
	lastMtime := ss.readPublicSet(dir, time.Time{})

	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		for {
			select {
			case <-done:
				ticker.Stop()
				return
			case <-ticker.C:
				lastMtime = ss.readPublicSet(dir, lastMtime)
			}
		}
	}()
}

func (ss *shardedSearcher) watchPriorities(dir string, done chan struct{}) {
	priorityPath := path.Join(dir, "priority.json")
	var lastMtime time.Time

	loadPriority := func() {
		st, err := os.Stat(priorityPath)
		if err != nil {
			return // ignore missing file errors
		}
		if st.ModTime() == lastMtime {
			return // file is unchanged
		}
		lastMtime = st.ModTime()
		buf, err := ioutil.ReadFile(priorityPath)
		if err != nil {
			log.Printf("reloading priority.json, error %v", err)
			return
		}
		priority := make(map[string]float64)
		err = json.Unmarshal(buf, &priority)
		if err != nil {
			log.Printf("reloading priority.json, error %v", err)
			return
		}

		log.Printf("reloading priority.json: %d shards have priorities", len(priority))

		// get an exclusive lock to update priority map and invalidate ranking
		proc := ss.sched.Exclusive()
		defer proc.Release()
		ss.priority = priority
		ss.rankedVersion++
		ss.ranked = nil // will regenerate ranking on next request
	}

	loadPriority()

	// fsnotify is more efficient for watching a large number of files for changes, but a
	// single stat call once a minute to check one file's modified time is negligible.
	ticker := time.NewTicker(1 * time.Minute)
	for {
		select {
		case <-done:
			ticker.Stop()
			return
		case <-ticker.C:
			loadPriority()
		}
	}
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

	go ss.watchPriorities(dir, dw.quit)
	ss.watchPublic(dir, dw.quit)

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

	for i, c := range and.Children {
		var setSize int
		var hasRepo func(string) bool

		switch setQuery := c.(type) {
		case *query.RepoSet:
			setSize = len(setQuery.Set)
			hasRepo = func(name string) bool {
				return setQuery.Set[name]
			}
		case *query.RepoBranches:
			setSize = len(setQuery.Set)
			hasRepo = func(name string) bool {
				return len(setQuery.Set[name]) > 0
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

		for _, s := range shards {
			if hasRepo(s.name) {
				filtered = append(filtered, s)
			}
		}

		// We don't need to adjust the query since we are returning an empty set
		// of shards to search.
		if len(filtered) == 0 {
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
			want := b.Set[filtered[0].name]
			for _, s := range filtered[1:] {
				if !strSliceEqual(want, b.Set[s.name]) {
					return filtered, and
				}
			}

			// Every repo wants the same branches, so we can replace RepoBranches
			// with a list of branch queries.
			and.Children[i] = b.Branches(filtered[0].name)
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

	switch v := opts.Visibility; v {
	case "public", "private":
		q = &query.And{Children: []query.Q{query.RepoFunc(func(name string) bool {
			_, ok := ss.public[name]
			return ok == (v == "public")
		}), q}}
	case "":
		// nop
	default:
		return fmt.Errorf("got SearchOptions.Visibility=%s, expected {public, private, \"\"}", v)
	}

	var childCtx context.Context
	var cancel context.CancelFunc
	if opts.MaxWallTime == 0 {
		childCtx, cancel = context.WithCancel(ctx)
	} else {
		childCtx, cancel = context.WithTimeout(ctx, opts.MaxWallTime)
	}

	defer cancel()

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
	feeder := make(chan zoekt.Searcher, runtime.GOMAXPROCS(0))
	g.Go(func() error {
		defer close(feeder)
		for _, s := range shards {
			// We let searchOneShard handle context errors.
			_ = proc.Yield(ctx)
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

					sender.Send(sr)
				}))
				if err != nil {
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

	return &agg, nil
}

// getShards returns the currently loaded shards. The shards must be accessed
// under a rlock call. The shards are sorted by decreasing rank and should not
// be mutated.
func (s *shardedSearcher) getShards() []rankedShard {
	if len(s.ranked) > 0 {
		return s.ranked
	}

	var res []rankedShard
	for _, sh := range s.shards {
		res = append(res, sh)
	}
	sort.Slice(res, func(i, j int) bool {
		priorityDiff := s.priority[res[i].name] - s.priority[res[j].name]
		if priorityDiff != 0 {
			return priorityDiff > 0
		}
		return res[i].name < res[j].name
	})

	// Cache ranked. We currently hold a read lock, so start a goroutine which
	// acquires a write lock to update. Use requiredVersion to ensure our
	// cached slice is still current after acquiring the write lock.
	go func(ranked []rankedShard, requiredVersion uint64) {
		proc := s.sched.Exclusive()
		defer proc.Release()
		if s.rankedVersion == requiredVersion {
			s.ranked = ranked
		}
	}(res, s.rankedVersion)

	return res
}

func shardName(s zoekt.Searcher) string {
	q := query.Repo{}
	result, err := s.List(context.Background(), &q, nil)
	if err != nil {
		return ""
	}
	if len(result.Repos) == 0 {
		return ""
	}
	return result.Repos[0].Repository.Name
}

func (s *shardedSearcher) replace(key string, shard zoekt.Searcher) {
	var name string
	if shard != nil {
		name = shardName(shard)
	}

	proc := s.sched.Exclusive()
	defer proc.Release()

	old := s.shards[key]
	if old.Searcher != nil {
		old.Close()
	}

	if shard == nil {
		delete(s.shards, key)
	} else {
		s.shards[key] = rankedShard{
			name:     name,
			Searcher: shard,
		}
	}
	s.rankedVersion++
	s.ranked = nil

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
