// Package systemtenant contains function to mark a context as allowed to
// access shards across all tenants. This must only be used for tasks that are
// not request specific.
package systemtenant

import (
	"context"
	"fmt"

	"github.com/sourcegraph/zoekt/internal/tenant/internal/tenanttype"
)

type contextKey int

const systemTenantKey contextKey = iota

// With marks a ctx to be allowed to access shards across all tenants. This MUST
// NOT BE USED on the user request path.
func With(ctx context.Context) (context.Context, error) {
	// We don't want to allow setting the system tenant on a context that already
	// has a user tenant set.
	if _, ok := tenanttype.GetTenant(ctx); ok {
		return nil, fmt.Errorf("tenant context already set")
	}
	return context.WithValue(ctx, systemTenantKey, systemTenantKey), nil
}

// Is returns true if the context has been marked to allow queries across all
// tenants.
func Is(ctx context.Context) bool {
	_, ok := ctx.Value(systemTenantKey).(contextKey)
	return ok
}
