package grpcutil

import (
	"net/http"
	"strings"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
)

// SplitMethodName splits a full gRPC method name (e.g. "/package.service/method") in to its individual components (service, method)
//
// Copied from github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/reporter.go
func SplitMethodName(fullMethod string) (string, string) {
	fullMethod = strings.TrimPrefix(fullMethod, "/") // remove leading slash
	if i := strings.Index(fullMethod, "/"); i >= 0 {
		return fullMethod[:i], fullMethod[i+1:]
	}
	return "unknown", "unknown"
}

// MultiplexGRPC takes a gRPC server and a plain HTTP handler and multiplexes the
// request handling. Any requests that declare themselves as gRPC requests are routed
// to the gRPC server, all others are routed to the httpHandler.
func MultiplexGRPC(grpcServer *grpc.Server, httpHandler http.Handler) http.Handler {
	newHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.Contains(r.Header.Get("Content-Type"), "application/grpc") {
			grpcServer.ServeHTTP(w, r)
		} else {
			httpHandler.ServeHTTP(w, r)
		}
	})

	// Until we enable TLS, we need to fall back to the h2c protocol, which is
	// basically HTTP2 without TLS. The standard library does not implement the
	// h2s protocol, so this hijacks h2s requests and handles them correctly.
	return h2c.NewHandler(newHandler, &http2.Server{})
}
