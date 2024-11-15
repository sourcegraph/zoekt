package tenant

import (
	"context"
)

// EqualsID returns true if the tenant ID in the context matches the
// given ID. If tenant enforcement is disabled, it always returns true.
func EqualsID(ctx context.Context, id int) bool {
	if !EnforceTenant() {
		return true
	}
	t, err := FromContext(ctx)
	if err != nil {
		return false
	}
	return t.ID() == id
}
