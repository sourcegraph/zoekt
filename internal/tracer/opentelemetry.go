package tracer

import (
	"context"
	"fmt"
	"regexp"

	"github.com/pkg/errors"
	sglog "github.com/sourcegraph/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	otelresource "go.opentelemetry.io/otel/sdk/resource"
	oteltracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"

	"github.com/sourcegraph/zoekt/internal/otlpenv"
)

// configureOpenTelemetry creates a native OpenTelemetry provider. Configuration
// is sourced from the environment using the specification laid out in
// https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/protocol/exporter.md.
//
// This setup is based on the one done in sourcegraph/sourcegraph - when making changes,
// be wary of divergences from the source: https://github.com/sourcegraph/sourcegraph/blob/main/internal/tracer/otel.go
func configureOpenTelemetry(resource sglog.Resource) (*oteltracesdk.TracerProvider, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	otelLogger := sglog.Scoped("otel")
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		otelLogger.Debug("error encountered", sglog.Error(err))
	}))

	processor, err := newOTelCollectorExporter(context.Background(), otlpenv.GetEndpoint())
	if err != nil {
		return nil, fmt.Errorf("new exporter: %w", err)
	}
	return newTracerProvider(resource, processor), nil
}

func newTracerProvider(resource sglog.Resource, processor oteltracesdk.SpanProcessor) *oteltracesdk.TracerProvider {
	return oteltracesdk.NewTracerProvider(
		oteltracesdk.WithResource(otelresource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(resource.Name),
			semconv.ServiceInstanceIDKey.String(resource.InstanceID),
			semconv.ServiceVersionKey.String(resource.Version))),
		oteltracesdk.WithSampler(oteltracesdk.ParentBased(oteltracesdk.NeverSample())),
		oteltracesdk.WithSpanProcessor(processor),
	)
}

// newOTelCollectorExporter creates a processor that exports spans to an OpenTelemetry
// collector.
func newOTelCollectorExporter(ctx context.Context, endpoint string) (oteltracesdk.SpanProcessor, error) {
	// Set up client to otel-collector - we replicate some of the logic used internally in
	// https://github.com/open-telemetry/opentelemetry-go/blob/21c1641831ca19e3acf341cc11459c87b9791f2f/exporters/otlp/internal/otlpconfig/envconfig.go
	// based on our own inferred endpoint.
	var (
		client          otlptrace.Client
		protocol        = otlpenv.GetProtocol()
		trimmedEndpoint = trimSchema(endpoint)
		insecure        = otlpenv.IsInsecure(endpoint)
	)

	// Work with different protocols
	switch protocol {
	case otlpenv.ProtocolGRPC:
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(trimmedEndpoint),
		}
		if insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		client = otlptracegrpc.NewClient(opts...)

	case otlpenv.ProtocolHTTPJSON:
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(trimmedEndpoint),
		}
		if insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		client = otlptracehttp.NewClient(opts...)
	}

	// Initialize the exporter
	traceExporter, err := otlptrace.New(ctx, client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create trace exporter")
	}

	return oteltracesdk.NewBatchSpanProcessor(traceExporter), nil
}

var httpSchemeRegexp = regexp.MustCompile(`(?i)^http://|https://`)

func trimSchema(endpoint string) string {
	return httpSchemeRegexp.ReplaceAllString(endpoint, "")
}
