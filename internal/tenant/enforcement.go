package tenant

import (
	"os"

	"go.uber.org/atomic"
)

const TenantsDir = "tenants"

func init() {
	enforcement, ok := os.LookupEnv("SRC_TENANT_ENFORCEMENT_MODE")
	if !ok {
		enforcement = "disabled"
	}
	enforcementMode.Store(enforcement)
}

var enforcementMode atomic.String

func EnforceTenant() bool {
	switch enforcementMode.Load() {
	case "strict":
		return true
	default:
		return false
	}
}
