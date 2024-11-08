package tenant

import (
	"context"
	"strconv"

	"google.golang.org/grpc/metadata"

	"github.com/sourcegraph/zoekt/internal/tenant/internal/tenanttype"
)

const (
	// headerKeyTenantID is the header key for the tenant ID.
	headerKeyTenantID = "X-Sourcegraph-Tenant-ID"

	// headerValueNoTenant indicates the request has no tenant.
	headerValueNoTenant = "none"
)

// Propagator implements the propagator.Propagator interface
// for propagating tenants across RPC calls. This is modeled directly on
// the HTTP middleware in this package, and should work exactly the same.
type Propagator struct{}

func (Propagator) FromContext(ctx context.Context) metadata.MD {
	md := make(metadata.MD)
	tenant, err := tenanttype.FromContext(ctx)
	if err != nil {
		md.Append(headerKeyTenantID, headerValueNoTenant)
	} else {
		md.Append(headerKeyTenantID, strconv.Itoa(tenant.ID()))
	}
	return md
}

func (Propagator) InjectContext(ctx context.Context, md metadata.MD) context.Context {
	var idStr string
	if vals := md.Get(headerKeyTenantID); len(vals) > 0 {
		idStr = vals[0]
	}
	switch idStr {
	case headerValueNoTenant:
		// Nothing to do, empty tenant.
		return ctx
	default:
		id, err := strconv.Atoi(idStr)
		if err != nil {
			// If the tenant is invalid, ignore the error and return the original context
			return ctx
		}
		return tenanttype.WithTenant(ctx, id)
	}
}
