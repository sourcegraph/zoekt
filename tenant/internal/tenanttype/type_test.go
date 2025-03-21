package tenanttype

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTenantRoundtrip(t *testing.T) {
	ctx := context.Background()
	tenantID := 42
	ctxWithTenant := WithTenant(ctx, &Tenant{tenantID})
	tenant, ok := GetTenant(ctxWithTenant)
	require.True(t, ok)
	require.Equal(t, tenantID, tenant.ID())
}

func TestFromContextWithoutTenant(t *testing.T) {
	ctx := context.Background()
	_, ok := GetTenant(ctx)
	require.False(t, ok)
}
