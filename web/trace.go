package web

import (
	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/trace/ot"
	"golang.org/x/net/context"
)

// traceAwareSearcher wraps a zoekt.Searcher instance so that the tracing context item is set in the
// context. This context item toggles on trace collection via the
// github.com/sourcegraph/zoekt/trace/ot package.
type traceAwareSearcher struct {
	Searcher zoekt.Searcher
}

func (s traceAwareSearcher) Search(ctx context.Context, q query.Q, opts *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	return s.Searcher.Search(ot.WithShouldTrace(ctx, opts.Trace), q, opts)
}

func (s traceAwareSearcher) List(ctx context.Context, q query.Q) (*zoekt.RepoList, error) {
	return s.Searcher.List(ctx, q)
}
func (s traceAwareSearcher) Close()         { s.Searcher.Close() }
func (s traceAwareSearcher) String() string { return s.Searcher.String() }
