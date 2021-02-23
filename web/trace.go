package web

import (
	"log"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/stream"
	"github.com/google/zoekt/trace"
	"github.com/opentracing/opentracing-go"
	"golang.org/x/net/context"
)

// traceAwareSearcher wraps a zoekt.Searcher instance so that the tracing context item is set in the
// context. This context item toggles on trace collection via the
// github.com/sourcegraph/zoekt/trace/ot package.
type traceAwareSearcher struct {
	Searcher stream.Searcher
}

func (s traceAwareSearcher) Search(ctx context.Context, q query.Q, opts *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	ctx, finish := getTraceContext(ctx, opts.Trace, opts.SpanContext)
	defer finish()
	return s.Searcher.Search(ctx, q, opts)
}

func (s traceAwareSearcher) StreamSearch(ctx context.Context, q query.Q, opts *zoekt.SearchOptions, sender zoekt.Sender) error {
	ctx, finish := getTraceContext(ctx, opts.Trace, opts.SpanContext)
	defer finish()
	return s.Searcher.StreamSearch(ctx, q, opts, sender)
}

func getTraceContext(ctx context.Context, traceEnabled bool, spanContext map[string]string) (context.Context, func()) {
	ctx = trace.WithOpenTracingEnabled(ctx, traceEnabled)
	finish := func() {}
	if traceEnabled && spanContext != nil {
		spanContext, err := trace.GetOpenTracer(ctx, nil).Extract(opentracing.TextMap, opentracing.TextMapCarrier(spanContext))
		if err != nil {
			log.Printf("Error extracting span from opts: %s", err)
		}
		if spanContext != nil {
			span, newCtx := opentracing.StartSpanFromContext(ctx, "zoekt.traceAwareSearcher.Search", opentracing.ChildOf(spanContext))
			finish = span.Finish
			ctx = newCtx
		}
	}
	return ctx, finish
}
func (s traceAwareSearcher) List(ctx context.Context, q query.Q) (*zoekt.RepoList, error) {
	return s.Searcher.List(ctx, q)
}
func (s traceAwareSearcher) Close()         { s.Searcher.Close() }
func (s traceAwareSearcher) String() string { return s.Searcher.String() }
