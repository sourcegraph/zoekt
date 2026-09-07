package tracer

import (
	"context"
	"testing"

	sglog "github.com/sourcegraph/log"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestTracerProviderPreservesParentSampling(t *testing.T) {
	traceID := oteltrace.TraceID{1}
	spanID := oteltrace.SpanID{2}

	for _, tc := range []struct {
		name    string
		sampled bool
	}{
		{name: "unsampled"},
		{name: "sampled", sampled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags := oteltrace.TraceFlags(0)
			if tc.sampled {
				flags = oteltrace.FlagsSampled
			}
			parent := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
				TraceID:    traceID,
				SpanID:     spanID,
				TraceFlags: flags,
				Remote:     true,
			})

			recorder := tracetest.NewSpanRecorder()
			provider := newTracerProvider(sglog.Resource{Name: "test"}, recorder)
			t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

			ctx := oteltrace.ContextWithRemoteSpanContext(context.Background(), parent)
			_, span := provider.Tracer("test").Start(ctx, "child")
			child := span.SpanContext()
			span.End()

			if got := child.TraceID(); got != traceID {
				t.Errorf("trace ID = %s, want %s", got, traceID)
			}
			if got := child.IsSampled(); got != tc.sampled {
				t.Errorf("sampled = %t, want %t", got, tc.sampled)
			}

			wantRecorded := 0
			if tc.sampled {
				wantRecorded = 1
			}
			if got := len(recorder.Ended()); got != wantRecorded {
				t.Errorf("recorded spans = %d, want %d", got, wantRecorded)
			}
		})
	}
}
