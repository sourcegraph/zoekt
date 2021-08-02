package web

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/trace"
	othttp "github.com/opentracing-contrib/go-stdlib/nethttp"
	"github.com/opentracing/opentracing-go"
	"github.com/uber/jaeger-client-go"
)

// traceAwareSearcher wraps a zoekt.Searcher instance so that the tracing context item is set in the
// context. This context item toggles on trace collection via the
// github.com/sourcegraph/zoekt/trace/ot package.
type traceAwareSearcher struct {
	Searcher zoekt.Streamer
}

func (s traceAwareSearcher) Search(
	ctx context.Context,
	q query.Q,
	opts *zoekt.SearchOptions,
) (*zoekt.SearchResult, error) {
	ctx, finish := getTraceContext(ctx, "zoekt.traceAwareSearcher.Search", opts.Trace, opts.SpanContext)
	defer finish()
	return s.Searcher.Search(ctx, q, opts)
}

func (s traceAwareSearcher) StreamSearch(
	ctx context.Context,
	q query.Q,
	opts *zoekt.SearchOptions,
	sender zoekt.Sender,
) error {
	ctx, finish := getTraceContext(ctx, "zoekt.traceAwareSearcher.StreamSearch", opts.Trace, opts.SpanContext)
	defer finish()
	return s.Searcher.StreamSearch(ctx, q, opts, sender)
}

func getTraceContext(
	ctx context.Context,
	opName string,
	traceEnabled bool,
	spanContext map[string]string,
) (context.Context, func()) {
	ctx = trace.WithOpenTracingEnabled(ctx, traceEnabled)
	finish := func() {}
	if traceEnabled && spanContext != nil {
		spanContext, err := trace.GetOpenTracer(ctx, nil).
			Extract(opentracing.TextMap, opentracing.TextMapCarrier(spanContext))
		if err != nil {
			log.Printf("Error extracting span from opts: %s", err)
		}
		if spanContext != nil {
			span, newCtx := opentracing.StartSpanFromContext(ctx, opName, opentracing.ChildOf(spanContext))
			finish = span.Finish
			ctx = newCtx
		}
	}
	return ctx, finish
}

func (s traceAwareSearcher) List(ctx context.Context, q query.Q, opts *zoekt.ListOptions) (*zoekt.RepoList, error) {
	return s.Searcher.List(ctx, q, opts)
}
func (s traceAwareSearcher) Close()         { s.Searcher.Close() }
func (s traceAwareSearcher) String() string { return s.Searcher.String() }

func HTTPTraceMiddleware(next http.Handler) http.Handler {
	tracer := opentracing.GlobalTracer()
	return othttp.MiddlewareFunc(tracer, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		began := time.Now()
		next.ServeHTTP(w, r)
		duration := time.Since(began)

		traceID := ""
		span := opentracing.SpanFromContext(r.Context())
		if span != nil {
			traceID = span.Context().(jaeger.SpanContext).TraceID().String()
		}

		log.Printf("method=%s url=%s traceid=%s duration=%s", r.Method, r.URL, traceID, duration)
	}))
}
