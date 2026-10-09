package trace

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestTraceRecordsOpenTelemetrySpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := oteltracesdk.NewTracerProvider(
		oteltracesdk.WithSampler(oteltracesdk.AlwaysSample()),
		oteltracesdk.WithSpanProcessor(recorder),
	)
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		_ = provider.Shutdown(context.Background())
	})

	ctx, parent := provider.Tracer("test").Start(context.Background(), "parent")
	defer parent.End()

	tr, ctx := New(ctx, "search", "my search")
	tr.LazyPrintf("shards: %d", 3)
	tr.SetError(errors.New("failed"))
	tr.Finish()

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d completed spans, want 1", len(spans))
	}
	span := spans[0]
	if got, want := span.Name(), "search"; got != want {
		t.Errorf("span name = %q, want %q", got, want)
	}
	if got, want := span.Parent().SpanID(), parent.SpanContext().SpanID(); got != want {
		t.Errorf("parent span ID = %s, want %s", got, want)
	}
	if got, want := oteltrace.SpanContextFromContext(ctx), span.SpanContext(); !got.Equal(want) {
		t.Errorf("context span = %v, want %v", got, want)
	}

	if got := attributeString(span.Attributes(), "title"); got != "my search" {
		t.Errorf("title attribute = %q, want %q", got, "my search")
	}
	if got := span.Status(); got.Code != codes.Error || got.Description != "failed" {
		t.Errorf("span status = %+v, want error status for %q", got, "failed")
	}

	var message string
	for _, event := range span.Events() {
		if event.Name == "log" {
			message = attributeString(event.Attributes, "message")
		}
	}
	if message != "shards: 3" {
		t.Errorf("log event message = %q, want %q", message, "shards: 3")
	}
}

func attributeString(attributes []attribute.KeyValue, key attribute.Key) string {
	for _, attr := range attributes {
		if attr.Key == key {
			return attr.Value.AsString()
		}
	}
	return ""
}
