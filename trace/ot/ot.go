// Package ot wraps github.com/opentracing/opentracing-go and
// github.com.opentracing-contrib/go-stdlib with selective tracing behavior that is toggled on and
// off with the presence of a context item (uses context.Context).
package ot

import (
	"context"

	"github.com/opentracing/opentracing-go"
	"go.uber.org/atomic"
)

type tracePolicy string

const (
	// TraceNone turns off tracing.
	TraceNone tracePolicy = "none"

	// TraceSelective turns on tracing only for requests with the X-Sourcegraph-Should-Trace header
	// set to a truthy value.
	TraceSelective tracePolicy = "selective"

	// Comprehensive turns on tracing for all requests.
	TraceAll tracePolicy = "all"
)

var trPolicy = atomic.NewString(string(TraceNone))

func SetTracePolicy(newTracePolicy tracePolicy) {
	trPolicy.Store(string(newTracePolicy))
}

func GetTracePolicy() tracePolicy {
	return tracePolicy(trPolicy.Load())
}

type key int

const (
	shouldTraceKey key = iota
)

// ShouldTrace returns true if the shouldTraceKey context value is true.
func ShouldTrace(ctx context.Context) bool {
	v, ok := ctx.Value(shouldTraceKey).(bool)
	if !ok {
		return false
	}
	return v
}

// WithShouldTrace sets the shouldTraceKey context value.
func WithShouldTrace(ctx context.Context, shouldTrace bool) context.Context {
	return context.WithValue(ctx, shouldTraceKey, shouldTrace)
}

// GetTracer returns the tracer to use for the given context. If ShouldTrace returns true, it
// returns the global tracer. Otherwise, it returns the NoopTracer.
func GetTracer(ctx context.Context) opentracing.Tracer {
	return getTracer(ctx, opentracing.GlobalTracer())
}

// getTracer is like GetTracer, but accepts a tracer as an argument. If ShouldTrace returns false,
// it returns the NoopTracer. If it returns true and the passed-in tracer is not nil, it returns the
// passed-in tracer. Otherwise, it returns the global tracer.
func getTracer(ctx context.Context, tracer opentracing.Tracer) opentracing.Tracer {
	if !ShouldTrace(ctx) {
		return opentracing.NoopTracer{}
	}
	if tracer == nil {
		return opentracing.GlobalTracer()
	}
	return tracer
}

// StartSpanFromContext starts a span using the tracer returned by GetTracer.
func StartSpanFromContext(ctx context.Context, operationName string, opts ...opentracing.StartSpanOption) (opentracing.Span, context.Context) {
	return StartSpanFromContextWithTracer(ctx, opentracing.GlobalTracer(), operationName, opts...)
}

// StartSpanFromContext starts a span using the tracer returned by invoking getTracer with the
// passed-in tracer.
func StartSpanFromContextWithTracer(ctx context.Context, tracer opentracing.Tracer, operationName string, opts ...opentracing.StartSpanOption) (opentracing.Span, context.Context) {
	return opentracing.StartSpanFromContextWithTracer(ctx, getTracer(ctx, tracer), operationName, opts...)
}
