package srv

import (
	"context"
	"time"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
)

// defaultTimeout is the maximum amount of time a search request should
// take. This is the same default used by Sourcegraph.
const defaultTimeout = 20 * time.Second

type SearchArgs struct {
	Q    query.Q
	Opts *zoekt.SearchOptions
}

type SearchReply struct {
	Result *zoekt.SearchResult
}

type ListArgs struct {
	Q query.Q
	Opts *zoekt.ListOptions
}

type ListReply struct {
	List *zoekt.RepoList
}

type Searcher struct {
	Searcher zoekt.Searcher
}

func (s *Searcher) Search(ctx context.Context, args *SearchArgs, reply *SearchReply) error {
	// Set a timeout if the user hasn't specified one.
	if args.Opts != nil && args.Opts.MaxWallTime == 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}

	r, err := s.Searcher.Search(ctx, args.Q, args.Opts)
	if err != nil {
		return err
	}
	reply.Result = r
	return nil
}

func (s *Searcher) List(ctx context.Context, args *ListArgs, reply *ListReply) error {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	r, err := s.Searcher.List(ctx, args.Q, args.Opts)
	if err != nil {
		return err
	}
	reply.List = r
	return nil
}
