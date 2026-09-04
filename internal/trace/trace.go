// Package trace provides a tracing API that invokes both golang.org/x/net/trace
// and OpenTelemetry.
package trace

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	nettrace "golang.org/x/net/trace"
)

const instrumentationScope = "github.com/sourcegraph/zoekt/internal/trace"

// New starts a trace with the supplied family and title.
func New(ctx context.Context, family, title string) (*Trace, context.Context) {
	ctx, span := otel.Tracer(instrumentationScope).Start(
		ctx, family,
		oteltrace.WithAttributes(attribute.String("title", title)),
	)

	tr := nettrace.New(family, title)
	return &Trace{span: span, trace: tr}, ctx
}

// Trace is a combined version of golang.org/x/net/trace.Trace and
// OpenTelemetry trace.Span. Use New to construct one.
type Trace struct {
	trace nettrace.Trace
	span  oteltrace.Span
}

// LazyPrintf evaluates its arguments with fmt.Sprintf each time the
// /debug/requests page is rendered. Any memory referenced by a will be
// pinned until the trace is finished and later discarded.
func (t *Trace) LazyPrintf(format string, a ...any) {
	if t.span.IsRecording() {
		t.span.AddEvent("log", oteltrace.WithAttributes(
			attribute.String("message", fmt.Sprintf(format, a...)),
		))
	}
	t.trace.LazyPrintf(format, a...)
}

// LazyLog evaluates x each time the /debug/requests page is rendered.
func (t *Trace) LazyLog(x fmt.Stringer, sensitive bool) {
	t.trace.LazyLog(x, sensitive)
}

// SetError declares that this trace and span resulted in an error.
func (t *Trace) SetError(err error) {
	if err == nil {
		return
	}
	t.trace.LazyPrintf("error: %v", err)
	t.trace.SetError()
	t.span.RecordError(err)
	t.span.SetStatus(codes.Error, err.Error())
}

// Finish declares that this trace and span is complete.
// The trace should not be used after calling this method.
func (t *Trace) Finish() {
	t.trace.Finish()
	t.span.End()
}
