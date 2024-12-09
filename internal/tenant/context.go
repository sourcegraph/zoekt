package tenant

import (
	"context"
	"fmt"
	"runtime/pprof"
	"sync"

	"go.uber.org/atomic"

	"github.com/sourcegraph/zoekt/internal/tenant/internal/enforcement"
	"github.com/sourcegraph/zoekt/internal/tenant/internal/tenanttype"
	"github.com/sourcegraph/zoekt/trace"
)

var ErrMissingTenant = fmt.Errorf("missing tenant")

func FromContext(ctx context.Context) (*tenanttype.Tenant, error) {
	tnt, ok := tenanttype.GetTenant(ctx)
	if !ok {
		return nil, ErrMissingTenant
	}
	return tnt, nil
}

type contextKey int

const (
	skipLogging contextKey = iota
)

// WithSkipMissingLogging skips logging when the tenant ID is missing. We use
// this, for example in the health check handler, when we know that the tenant
// ID is not needed.
func WithSkipMissingLogging(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipLogging, skipLogging)
}

// Log logs the tenant ID to the trace. If tenant logging is enabled, it also
// logs a stack trace to a pprof profile.
func Log(ctx context.Context, tr *trace.Trace) {
	tnt, ok := tenanttype.GetTenant(ctx)
	if !ok {
		if profile := pprofMissingTenant(); profile != nil {
			if _, ok := ctx.Value(skipLogging).(contextKey); !ok {
				// We want to track every stack trace, so need a unique value for the event
				eventValue := pprofUniqID.Add(1)

				// skip stack for Add and this function (2).
				profile.Add(eventValue, 2)
			}
		}
		tr.LazyPrintf("tenant: missing")
		return
	}
	tr.LazyPrintf("tenant: %d", tnt.ID())
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
