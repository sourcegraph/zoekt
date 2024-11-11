package tenant

import (
	"context"
	"fmt"
	"net/http"
	"runtime/pprof"

	"github.com/sourcegraph/zoekt/internal/tenant/internal/tenanttype"
)

// InternalHTTPTransport is a roundtripper that sets tenants within request context
// as headers on outgoing requests. The attached headers can be picked up and attached
// to incoming request contexts with tenant.InternalHTTPMiddleware.
type InternalHTTPTransport struct {
	RoundTripper http.RoundTripper
}

var _ http.RoundTripper = &InternalHTTPTransport{}

func (t *InternalHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.RoundTripper == nil {
		t.RoundTripper = http.DefaultTransport
	}

	// RoundTripper should not modify original request. All the code paths
	// below set a header, so we clone the request immediately.
	req = req.Clone(req.Context())

	tenant, err := tenanttype.FromContext(req.Context())

	if err != nil {
		// No tenant set
		req.Header.Set(headerKeyTenantID, headerValueNoTenant)
	} else {
		req.Header.Set(headerKeyTenantID, tenanttype.Marshal(tenant))
	}

	return t.RoundTripper.RoundTrip(req)
}

// InternalHTTPMiddleware wraps the given handle func and attaches the tenant indicated
// in incoming requests to the request header. This should only be used to wrap internal
// handlers for communication between Sourcegraph services.
// The client side has to use the InternalHTTPTransport to set the tenant header.
//
// 🚨 SECURITY: This should *never* be called to wrap externally accessible handlers (i.e.
// only use for internal endpoints), because header values allow to impersonate a tenant.
func InternalHTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		ctx := req.Context()

		raw := req.Header.Get(headerKeyTenantID)
		switch raw {
		case "", headerValueNoTenant:
			// Request not associated with a tenant, continue with request
			next.ServeHTTP(rw, req)
			return
		default:
			// Request associated with a tenant - add it to the context:
			tenant, err := tenanttype.Unmarshal(raw)
			if err != nil {
				// Do not proceed with request
				rw.WriteHeader(http.StatusForbidden)
				_, _ = rw.Write([]byte(fmt.Sprintf("%s was provided for tenant, but the value was invalid", headerKeyTenantID)))
				return
			}

			// Valid tenant
			ctx = tenanttype.WithTenant(ctx, tenant)

			pprof.Do(ctx, pprof.Labels("tenant", tenanttype.Marshal(tenant)), func(ctx context.Context) {
				next.ServeHTTP(rw, req.WithContext(ctx))
			})
		}
	})
}
