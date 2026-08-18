package defaults

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/prometheus/client_golang/prometheus"
	sglog "github.com/sourcegraph/log"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	"github.com/sourcegraph/zoekt/grpc/internalerrs"
	"github.com/sourcegraph/zoekt/grpc/messagesize"
	"github.com/sourcegraph/zoekt/grpc/propagator"
	"github.com/sourcegraph/zoekt/internal/tenant"
)

func NewServer(logger sglog.Logger, additionalOpts ...grpc.ServerOption) *grpc.Server {
	metrics := serverMetricsOnce()

	recoveryOpt := recovery.WithRecoveryHandlerContext(panicRecoveryHandler(logger))

	opts := []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainStreamInterceptor(
			propagator.StreamServerPropagator(tenant.Propagator{}),
			tenant.StreamServerInterceptor,
			metrics.StreamServerInterceptor(),
			messagesize.StreamServerInterceptor,
			internalerrs.LoggingStreamServerInterceptor(logger),
			recovery.StreamServerInterceptor(recoveryOpt),
		),
		grpc.ChainUnaryInterceptor(
			propagator.UnaryServerPropagator(tenant.Propagator{}),
			tenant.UnaryServerInterceptor,
			metrics.UnaryServerInterceptor(),
			messagesize.UnaryServerInterceptor,
			internalerrs.LoggingUnaryServerInterceptor(logger),
			recovery.UnaryServerInterceptor(recoveryOpt),
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

// panicRecoveryHandler converts a recovered handler panic into an Internal
// error. Shard searches already recover their own panics in searchOneShard, so
// this only sees bugs in the layer between gRPC and the shard searchers. The
// panic value is logged rather than returned, since it is internal detail.
func panicRecoveryHandler(logger sglog.Logger) recovery.RecoveryHandlerFuncContext {
	return func(ctx context.Context, p any) error {
		stack := make([]byte, 64<<10)
		stack = stack[:runtime.Stack(stack, false)]

		method, ok := grpc.Method(ctx)
		if !ok {
			method = "unknown"
		}

		logger.Error("recovered from panic in gRPC handler",
			sglog.String("method", method),
			sglog.String("panic", fmt.Sprint(p)),
			sglog.String("stacktrace", string(stack)),
		)

		return status.Error(codes.Internal, "internal error")
	}
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
