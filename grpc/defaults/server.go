package defaults

import (
	"sync"

	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	sglog "github.com/sourcegraph/log"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/sourcegraph/zoekt/grpc/internalerrs"
	"github.com/sourcegraph/zoekt/grpc/messagesize"
	"github.com/sourcegraph/zoekt/grpc/propagator"
	"github.com/sourcegraph/zoekt/tenant"
)

func NewServer(logger sglog.Logger, additionalOpts ...grpc.ServerOption) *grpc.Server {
	metrics := serverMetricsOnce()

	opts := []grpc.ServerOption{
		grpc.ChainStreamInterceptor(
			propagator.StreamServerPropagator(tenant.Propagator{}),
			tenant.StreamServerInterceptor,
			otelgrpc.StreamServerInterceptor(),
			metrics.StreamServerInterceptor(),
			messagesize.StreamServerInterceptor,
			internalerrs.LoggingStreamServerInterceptor(logger),
		),
		grpc.ChainUnaryInterceptor(
			propagator.UnaryServerPropagator(tenant.Propagator{}),
			tenant.UnaryServerInterceptor,
			otelgrpc.UnaryServerInterceptor(),
			metrics.UnaryServerInterceptor(),
			messagesize.UnaryServerInterceptor,
			internalerrs.LoggingUnaryServerInterceptor(logger),
		),
	}

	opts = append(opts, additionalOpts...)

	// Ensure that the message size options are set last, so they override any other
	// server-specific options that tweak the message size.
	//
	// The message size options are only provided if the environment variable is set. These options serve as an escape hatch, so they
	// take precedence over everything else with a uniform size setting that's easy to reason about.
	opts = append(opts, messagesize.MustGetServerMessageSizeFromEnv()...)

	s := grpc.NewServer(opts...)
	reflection.Register(s)
	return s
}

// serviceMetricsOnce returns a singleton instance of the server metrics
// that are shared across all gRPC servers that this process creates.
//
// This function panics if the metrics cannot be registered with the default
// Prometheus registry.
var serverMetricsOnce = sync.OnceValue(func() *grpcprom.ServerMetrics {
	serverMetrics := grpcprom.NewServerMetrics(
		grpcprom.WithServerCounterOptions(),
		grpcprom.WithServerHandlingTimeHistogram(), // record the overall response latency for a gRPC request)
	)
	prometheus.DefaultRegisterer.MustRegister(serverMetrics)
	return serverMetrics
})
