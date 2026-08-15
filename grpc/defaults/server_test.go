package defaults

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/sourcegraph/log/logtest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	webserverv1 "github.com/sourcegraph/zoekt/grpc/protos/zoekt/webserver/v1"
)

// panicSecret is planted in the panic value so the test can assert that no part
// of it reaches the client.
const panicSecret = "shard /data/index/secret-repo_v16.00000.zoekt"

type panickingServer struct {
	webserverv1.UnimplementedWebserverServiceServer
}

func (*panickingServer) Search(context.Context, *webserverv1.SearchRequest) (*webserverv1.SearchResponse, error) {
	panic(panicSecret)
}

func (*panickingServer) StreamSearch(*webserverv1.StreamSearchRequest, webserverv1.WebserverService_StreamSearchServer) error {
	panic(panicSecret)
}

// List does not panic, so the test can check the server is still serving
// afterwards.
func (*panickingServer) List(context.Context, *webserverv1.ListRequest) (*webserverv1.ListResponse, error) {
	return &webserverv1.ListResponse{}, nil
}

func newTestServer(t *testing.T) webserverv1.WebserverServiceClient {
	t.Helper()

	s := NewServer(logtest.Scoped(t))
	webserverv1.RegisterWebserverServiceServer(s, &panickingServer{})

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	// Serve has to be joined before the test returns. Logging from a goroutine
	// that outlives the test panics the whole package rather than failing this
	// one test.
	served := make(chan error, 1)
	go func() { served <- s.Serve(lis) }()
	t.Cleanup(func() {
		s.Stop()
		if err := <-served; err != nil {
			t.Errorf("Serve returned: %v", err)
		}
	})

	cc, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cc.Close() })

	return webserverv1.NewWebserverServiceClient(cc)
}

// The interceptors are the whole point of this change, so exercise them through
// a real server rather than calling the recovery handler directly. This covers
// that recovery is registered at all, that both the unary and the stream
// interceptor are wired, and that a panic leaves the server able to serve.
func TestServerRecoversHandlerPanics(t *testing.T) {
	client := newTestServer(t)
	ctx := context.Background()

	assertRecovered := func(t *testing.T, err error) {
		t.Helper()

		if got := status.Code(err); got != codes.Internal {
			t.Fatalf("status code is %s (err=%v), want %s", got, err, codes.Internal)
		}
		// The panic value carries a shard path here, and callers may have no
		// access to it. Neither it nor a stack trace should cross the wire.
		if msg := status.Convert(err).Message(); strings.Contains(msg, panicSecret) || strings.Contains(msg, "goroutine ") {
			t.Fatalf("panic detail reached the client: %q", msg)
		}
	}

	t.Run("unary", func(t *testing.T) {
		_, err := client.Search(ctx, &webserverv1.SearchRequest{})
		assertRecovered(t, err)
	})

	t.Run("stream", func(t *testing.T) {
		ss, err := client.StreamSearch(ctx, &webserverv1.StreamSearchRequest{})
		if err != nil {
			t.Fatal(err)
		}
		for {
			_, err = ss.Recv()
			if err != nil {
				break
			}
		}
		if errors.Is(err, io.EOF) {
			t.Fatal("stream ended cleanly, want the panic surfaced as an error")
		}
		assertRecovered(t, err)
	})

	t.Run("server still serving", func(t *testing.T) {
		if _, err := client.List(ctx, &webserverv1.ListRequest{}); err != nil {
			t.Fatalf("List after two panics: %v", err)
		}
	})

}

func TestPanicRecoveryHandler(t *testing.T) {
	handler := panicRecoveryHandler(logtest.Scoped(t))

	err := handler(context.Background(), "boom")
	if err == nil {
		t.Fatal("handler returned nil, want an error")
	}

	if got := status.Code(err); got != codes.Internal {
		t.Errorf("status code is %s, want %s", got, codes.Internal)
	}

	if strings.Contains(err.Error(), "goroutine ") {
		t.Errorf("error sent to the client contains a stack trace: %s", err)
	}
}
