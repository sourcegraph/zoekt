package tenanttype

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTenantRoundtrip(t *testing.T) {
	ctx := context.Background()
	tenantID := 42
	ctxWithTenant := WithTenant(ctx, tenantID)
	tenant, err := FromContext(ctxWithTenant)
	require.NoError(t, err)
	require.Equal(t, tenantID, tenant.ID())
}

func TestFromContextWithoutTenant(t *testing.T) {
	ctx := context.Background()
	_, err := FromContext(ctx)
	require.Equal(t, ErrNoTenantInContext, err)
}
