package defaults

import (
	"context"
	"net"
	"testing"
	"time"

	sglog "github.com/sourcegraph/log"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	oteltracesdk "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

func TestServerPreservesOpenTelemetryTraceContext(t *testing.T) {
	provider := oteltracesdk.NewTracerProvider(
		oteltracesdk.WithSampler(oteltracesdk.ParentBased(oteltracesdk.NeverSample())),
	)
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
		_ = provider.Shutdown(context.Background())
	})

	listener := bufconn.Listen(1 << 20)
	server := NewServer(sglog.NoOp())
	health := &traceHealthServer{contexts: make(chan oteltrace.SpanContext, 1)}
	healthv1.RegisterHealthServer(server, health)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := healthv1.NewHealthClient(conn)

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
				TraceID:    oteltrace.TraceID{1},
				SpanID:     oteltrace.SpanID{2},
				TraceFlags: flags,
				Remote:     true,
			})
			ctx := oteltrace.ContextWithRemoteSpanContext(context.Background(), parent)
			ctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()

			if _, err := client.Check(ctx, &healthv1.HealthCheckRequest{}); err != nil {
				t.Fatal(err)
			}
			serverContext := <-health.contexts
			if got, want := serverContext.TraceID(), parent.TraceID(); got != want {
				t.Errorf("server trace ID = %s, want %s", got, want)
			}
			if got := serverContext.IsSampled(); got != tc.sampled {
				t.Errorf("server sampled = %t, want %t", got, tc.sampled)
			}
		})
	}
}

type traceHealthServer struct {
	healthv1.UnimplementedHealthServer
	contexts chan oteltrace.SpanContext
}

func (s *traceHealthServer) Check(ctx context.Context, _ *healthv1.HealthCheckRequest) (*healthv1.HealthCheckResponse, error) {
	s.contexts <- oteltrace.SpanContextFromContext(ctx)
	return &healthv1.HealthCheckResponse{Status: healthv1.HealthCheckResponse_SERVING}, nil
}
