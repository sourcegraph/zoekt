package tenant

import (
	"os"

	"github.com/sourcegraph/zoekt/internal/tenant/internal/enforcement"
)

func init() {
	v, ok := os.LookupEnv("SRC_TENANT_ENFORCEMENT_MODE")
	if !ok {
		v = "disabled"
	}
	enforcement.EnforcementMode.Store(v)
}

func EnforceTenant() bool {
	switch enforcement.EnforcementMode.Load() {
	case "strict":
		return true
	default:
		return false
	}
}
