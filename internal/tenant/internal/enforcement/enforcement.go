package enforcement

import (
	"os"
	"runtime/pprof"

	"go.uber.org/atomic"
)

// EnforcementMode is the current tenant enforcement mode. It resides here
// instead of in the tenant package to avoid a circular dependency. See
// tenanttest.MockEnforce.
var EnforcementMode = atomic.NewString(os.Getenv("SRC_TENANT_ENFORCEMENT_MODE"))

var PPROFUniqID atomic.Int64
var PPROFMissingTenant = func() *pprof.Profile {
	if !ShouldLogNoTenant() {
		return nil
	}
	return pprof.NewProfile("zoekt_missing_tenant")
}()

// ShouldLogNoTenant returns true if the tenant enforcement mode is logging or strict.
// It is used to log a warning if a request to a low-level store is made without a tenant
// so we can identify missing tenants. This will go away and only strict will be allowed
// once we are confident that all contexts carry tenants.
func ShouldLogNoTenant() bool {
	switch EnforcementMode.Load() {
	case "logging", "strict":
		return true
	default:
		return false
	}
}
