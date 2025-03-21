package tenant

import (
	"context"

	"github.com/sourcegraph/zoekt/tenant/systemtenant"
)

// HasAccess returns true if the tenant ID in the context matches the
// given ID. If tenant enforcement is disabled, it always returns true.
func HasAccess(ctx context.Context, id int) bool {
	if !EnforceTenant() {
		return true
	}
	if systemtenant.Is(ctx) {
		return true
	}
	t, err := FromContext(ctx)
	if err != nil {
		return false
	}
	return t.ID() == id
}
