package tenant

import (
	"github.com/sourcegraph/zoekt/internal/tenant/internal/enforcement"
)

func EnforceTenant() bool {
	switch enforcement.EnforcementMode.Load() {
	case "strict":
		return true
	default:
		return false
	}
}
