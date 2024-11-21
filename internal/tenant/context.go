package tenant

import (
	"context"
	"fmt"
	"strconv"

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
		if enforcement.PPROFMissingTenant != nil {
			// We want to track every stack trace, so need a unique value for the event
			eventValue := enforcement.PPROFUniqID.Add(1)

			// skip stack for Add and this function (2).
			enforcement.PPROFMissingTenant.Add(eventValue, 2)
		}
		return "missing"
	}
	return strconv.Itoa(tnt.ID())
}
