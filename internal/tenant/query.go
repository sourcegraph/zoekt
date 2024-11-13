package tenant

import (
	"context"

	"github.com/sourcegraph/zoekt/internal/tenant/internal/tenanttype"
)

// EqualsID returns true if the tenant ID in the context matches the
// given ID. If tenant enforcement is disabled, it always returns true.
func EqualsID(ctx context.Context, id int) bool {
	if !EnforceTenant() {
		return true
	}
	t, err := tenanttype.FromContext(ctx)
	if err != nil {
		return false
	}
	return t.ID() == id
}
