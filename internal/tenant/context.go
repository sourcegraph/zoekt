package tenant

import (
	"context"
	"fmt"
	"runtime/pprof"
	"strconv"
	"sync"

	"go.uber.org/atomic"

	"github.com/sourcegraph/zoekt/internal/tenant/internal/enforcement"
	"github.com/sourcegraph/zoekt/internal/tenant/internal/tenanttype"
)

var ErrMissingTenant = fmt.Errorf("missing tenant")

func FromContext(ctx context.Context) (*tenanttype.Tenant, error) {
	tnt, ok := tenanttype.GetTenant(ctx)
	if !ok {
		return nil, ErrMissingTenant
	}
	return tnt, nil
}

// IDToString is a helper function that returns a printable string of the tenant
// ID in the context. This is useful for logging.
func IDToString(ctx context.Context) string {
	tnt, ok := tenanttype.GetTenant(ctx)
	if !ok {
		if profile := pprofMissingTenant(); profile != nil {
			// We want to track every stack trace, so need a unique value for the event
			eventValue := pprofUniqID.Add(1)

			// skip stack for Add and this function (2).
			profile.Add(eventValue, 2)
		}
		return "missing"
	}
	return strconv.Itoa(tnt.ID())
}

var pprofUniqID atomic.Int64
var pprofOnce sync.Once
var pprofProfile *pprof.Profile

// pprofMissingTenant returns the pprof profile for missing tenants,
// initializing it only once.
func pprofMissingTenant() *pprof.Profile {
	pprofOnce.Do(func() {
		if shouldLogNoTenant() {
			pprofProfile = pprof.NewProfile("missing_tenant")
		}
	})
	return pprofProfile
}

// shouldLogNoTenant returns true if the tenant enforcement mode is logging or strict.
// It is used to log a warning if a request to a low-level store is made without a tenant
// so we can identify missing tenants. This will go away and only strict will be allowed
// once we are confident that all contexts carry tenants.
func shouldLogNoTenant() bool {
	switch enforcement.EnforcementMode.Load() {
	case "logging", "strict":
		return true
	default:
		return false
	}
}
