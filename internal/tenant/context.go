package tenant

import (
	"context"
	"fmt"
	"runtime/pprof"

	"go.uber.org/atomic"

	"github.com/sourcegraph/zoekt/internal/tenant/internal/enforcement"
	"github.com/sourcegraph/zoekt/internal/tenant/internal/tenanttype"
)

var ErrMissingTenant = fmt.Errorf("missing tenant")

func FromContext(ctx context.Context) (*tenanttype.Tenant, error) {
	tnt, ok := tenanttype.GetTenant(ctx)
	if !ok {
		if pprofMissingTenant != nil {
			// We want to track every stack trace, so need a unique value for the event
			eventValue := pprofUniqID.Add(1)

			// skip stack for Add and this function (2).
			pprofMissingTenant.Add(eventValue, 2)
		}

		return nil, ErrMissingTenant
	}
	return tnt, nil
}

var pprofUniqID atomic.Int64
var pprofMissingTenant = func() *pprof.Profile {
	if !enforcement.ShouldLogNoTenant() {
		return nil
	}
	return pprof.NewProfile("missing_tenant")
}()
