package shards

import (
	"context"
	"sync"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/stream"
	"github.com/google/zoekt/trace"
)

// typeRepoSearcher evaluates all type:repo sub-queries before sending the query
// to the underlying searcher. We need to evaluate type:repo queries first
// since they need to do cross shard operations.
type typeRepoSearcher struct {
	stream.Searcher
}

func (s *typeRepoSearcher) Search(ctx context.Context, q query.Q, opts *zoekt.SearchOptions) (sr *zoekt.SearchResult, err error) {
	tr, ctx := trace.New(ctx, "typeRepoSearcher.Search", "")
	tr.LazyLog(q, true)
	tr.LazyPrintf("opts: %+v", opts)
	defer func() {
		if sr != nil {
			tr.LazyPrintf("num files: %d", len(sr.Files))
			tr.LazyPrintf("stats: %+v", sr.Stats)
		}
		if err != nil {
			tr.LazyPrintf("error: %v", err)
			tr.SetError(err)
		}
		tr.Finish()
	}()

	q, err = s.eval(ctx, q)
	if err != nil {
		return nil, err
	}

	return s.Searcher.Search(ctx, q, opts)
}

func (s *typeRepoSearcher) StreamSearch(ctx context.Context, q query.Q, opts *zoekt.SearchOptions, sender stream.Sender) (err error) {
	tr, ctx := trace.New(ctx, "typeRepoSearcher.StreamSearch", "")

	a := struct {
		sync.Mutex
		n     int
		stats *zoekt.Stats
	}{
		stats: new(zoekt.Stats),
	}

	tr.LazyLog(q, true)
	tr.LazyPrintf("opts: %+v", opts)
	defer func() {
		tr.LazyPrintf("num files: %d", a.n)
		tr.LazyPrintf("stats: %+v", a.stats)
		if err != nil {
			tr.LazyPrintf("error: %v", err)
			tr.SetError(err)
		}
		tr.Finish()
	}()

	q, err = s.eval(ctx, q)
	if err != nil {
		return err
	}

	return s.Searcher.StreamSearch(ctx, q, opts, stream.SenderFunc(func(event *zoekt.SearchResult) {
		a.Lock()
		a.n += len(event.Files)
		a.stats.Add(event.Stats)
		a.Unlock()

		sender.Send(event)
	}))
}

func (s *typeRepoSearcher) List(ctx context.Context, r query.Q) (rl *zoekt.RepoList, err error) {
	tr, ctx := trace.New(ctx, "typeRepoSearcher.List", "")
	tr.LazyLog(r, true)
	defer func() {
		if rl != nil {
			tr.LazyPrintf("repos size: %d", len(rl.Repos))
			tr.LazyPrintf("crashes: %d", rl.Crashes)
		}
		if err != nil {
			tr.LazyPrintf("error: %v", err)
			tr.SetError(err)
		}
		tr.Finish()
	}()

	r, err = s.eval(ctx, r)
	if err != nil {
		return nil, err
	}

	return s.Searcher.List(ctx, r)
}

func (s *typeRepoSearcher) eval(ctx context.Context, q query.Q) (query.Q, error) {
	var err error
	q = query.Map(q, func(q query.Q) query.Q {
		if err != nil {
			return nil
		}

		rq, ok := q.(*query.Type)
		if !ok || rq.Type != query.TypeRepo {
			return q
		}

		var rl *zoekt.RepoList
		rl, err = s.Searcher.List(ctx, rq.Child)
		if err != nil {
			return nil
		}

		rs := &query.RepoSet{Set: make(map[string]bool, len(rl.Repos))}
		for _, r := range rl.Repos {
			rs.Set[r.Repository.Name] = true
		}
		return rs
	})
	return q, err
}
