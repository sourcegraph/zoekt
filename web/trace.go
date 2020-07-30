package web

import (
	"log"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/trace/ot"
	"github.com/opentracing/opentracing-go"
	"golang.org/x/net/context"
)

// traceAwareSearcher wraps a zoekt.Searcher instance so that the tracing context item is set in the
// context. This context item toggles on trace collection via the
// github.com/sourcegraph/zoekt/trace/ot package.
type traceAwareSearcher struct {
	Searcher zoekt.Searcher
}

func (s traceAwareSearcher) Search(ctx context.Context, q query.Q, opts *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	ctx = ot.WithShouldTrace(ctx, opts.Trace)
	if opts.Trace && opts.SpanContext != nil {
		spanContext, err := ot.GetTracer(ctx).Extract(opentracing.TextMap, opentracing.TextMapCarrier(opts.SpanContext))
		if err != nil {
			log.Printf("Error extracting span from opts: %s", err)
		}
		if spanContext != nil {
			span, newCtx := ot.StartSpanFromContext(ctx, "zoekt.traceAwareSearcher.Search", opentracing.ChildOf(spanContext))
			defer span.Finish()
			ctx = newCtx
		}
	}
	return s.Searcher.Search(ctx, q, opts)
}

func (s traceAwareSearcher) List(ctx context.Context, q query.Q) (*zoekt.RepoList, error) {
	return s.Searcher.List(ctx, q)
}
func (s traceAwareSearcher) Close()         { s.Searcher.Close() }
func (s traceAwareSearcher) String() string { return s.Searcher.String() }
