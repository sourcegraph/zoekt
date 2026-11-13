package grpcutil

import (
	"net/http"
	"strings"

	"google.golang.org/grpc"
)

// SplitMethodName splits a full gRPC method name (e.g. "/package.service/method") in to its individual components (service, method)
//
// Copied from github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/reporter.go
func SplitMethodName(fullMethod string) (string, string) {
	fullMethod = strings.TrimPrefix(fullMethod, "/") // remove leading slash
	if before, after, ok := strings.Cut(fullMethod, "/"); ok {
		return before, after
	}
	return "unknown", "unknown"
}

// MultiplexGRPC takes a gRPC server and a plain HTTP handler and multiplexes the
// request handling. Any requests that declare themselves as gRPC requests are routed
// to the gRPC server, all others are routed to the httpHandler.
//
// gRPC requires HTTP/2, so the http.Server serving this handler must enable
// the protocols returned by Protocols.
func MultiplexGRPC(grpcServer *grpc.Server, httpHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.Contains(r.Header.Get("Content-Type"), "application/grpc") {
			grpcServer.ServeHTTP(w, r)
		} else {
			httpHandler.ServeHTTP(w, r)
		}
	})
}

// Protocols returns the protocols an http.Server serving gRPC should enable.
// Until we enable TLS, gRPC clients connect using unencrypted HTTP/2 (h2c),
// which net/http only serves when explicitly enabled.
func Protocols() *http.Protocols {
	var p http.Protocols
	p.SetHTTP1(true)
	p.SetHTTP2(true)
	p.SetUnencryptedHTTP2(true)
	return &p
}
