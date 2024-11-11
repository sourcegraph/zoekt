package tenant

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	proto "github.com/sourcegraph/zoekt/cmd/zoekt-sourcegraph-indexserver/protos/sourcegraph/zoekt/configuration/v1"
	"github.com/sourcegraph/zoekt/internal/tenant/internal/tenanttype"
)

func mockEnforce(t *testing.T) {
	// prevent parallel tests from interfering with each other
	t.Setenv("mockEnforce", "true")

	old := enforcementMode.Load()
	t.Cleanup(func() {
		enforcementMode.Store(old)
	})

	enforcementMode.Store("strict")
}

func TestNewTenantRepoIdIterator_EnforceTenantTrue(t *testing.T) {
	mockEnforce(t)

	response := &proto.ListResponse{
		TenantIdReposMap: map[int64]*proto.RepoIdList{
			1: {Ids: []int32{101, 102, 103}},
			2: {Ids: []int32{201, 202}},
		},
	}

	ctx := context.Background()
	iterator := NewTenantRepoIdIterator(ctx, response)

	expectedTenants := []int{1, 2}
	expectedIds := [][]uint32{{101, 102, 103}, {201, 202}}

	idx := 0
	iterator(func(ds *ContextRepoIDs, err error) bool {
		require.NoError(t, err)
		tenant, err := tenanttype.FromContext(ds.Ctx)
		require.NoError(t, err)
		require.Equal(t, expectedTenants[idx], tenant.ID())
		require.Equal(t, expectedIds[idx], ds.RepoIDs)

		idx++
		return true
	})

	require.Equal(t, len(expectedTenants), idx, "All tenants should be iterated")
}
