package defaults_test

import (
	"context"
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/cmd/zoekt-webserver/grpc/server"
	"github.com/sourcegraph/zoekt/grpc/defaults"
	webserverv1 "github.com/sourcegraph/zoekt/grpc/protos/zoekt/webserver/v1"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/internal/tenant/tenanttest"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/search"
)

// TestTenantServerOptions checks that a server built from plain grpc options
// plus TenantServerOptions enforces tenants like NewServer: each tenant sees
// only its own shards, rather than none.
func TestTenantServerOptions(t *testing.T) {
	tenanttest.MockEnforce(t)

	dir := t.TempDir()
	opts := index.Options{
		IndexDir: dir,
		RepositoryDescription: zoekt.Repository{
			ID:        7,
			Name:      "repo",
			RawConfig: map[string]string{"tenantID": "1"},
		},
	}
	opts.SetDefaults()
	b, err := index.NewBuilder(opts)
	require.NoError(t, err)
	require.NoError(t, b.AddFile("f", []byte("needle")))
	require.NoError(t, b.Finish())
	ss, err := search.NewDirectorySearcher(dir)
	require.NoError(t, err)
	t.Cleanup(ss.Close)

	searchAs := func(t *testing.T, serverOpts []grpc.ServerOption, tenantID int) int {
		t.Helper()
		s := grpc.NewServer(serverOpts...)
		webserverv1.RegisterWebserverServiceServer(s, server.NewServer(ss))
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		go func() { _ = s.Serve(lis) }()
		t.Cleanup(s.Stop)

		conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		ctx := metadata.AppendToOutgoingContext(context.Background(), "X-Sourcegraph-Tenant-ID", strconv.Itoa(tenantID))
		resp, err := webserverv1.NewWebserverServiceClient(conn).Search(ctx, &webserverv1.SearchRequest{
			Query: query.QToProto(&query.Substring{Pattern: "needle"}),
		})
		require.NoError(t, err)
		return len(resp.GetFiles())
	}

	require.Equal(t, 1, searchAs(t, defaults.TenantServerOptions(), 1), "own tenant")
	require.Equal(t, 0, searchAs(t, defaults.TenantServerOptions(), 2), "other tenant")
	// Without the options the tenant never reaches the shards.
	require.Equal(t, 0, searchAs(t, nil, 1), "no tenant options")
}
