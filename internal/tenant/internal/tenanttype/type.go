package tenanttype

import (
	"context"
	"fmt"
)

type Tenant struct {
	// never expose this otherwise impersonation outside of this package is possible.
	_id int
}

func (t *Tenant) ID() int {
	return t._id
}

type contextKey int

const tenantKey contextKey = iota

// WithTenant returns a new context for the given tenant.
func WithTenant(ctx context.Context, tntID int) context.Context {
	return context.WithValue(ctx, tenantKey, &Tenant{_id: tntID})
}

var ErrNoTenantInContext = fmt.Errorf("no tenant in context")

func FromContext(ctx context.Context) (*Tenant, error) {
	tnt, ok := ctx.Value(tenantKey).(*Tenant)
	if !ok {
		return nil, ErrNoTenantInContext
	}
	return tnt, nil
}
