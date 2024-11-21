// Package systemtenant contains function to mark a context as allowed to
// access shards across all tenants. This must only be used for tasks that are
// not request specific.
package systemtenant

import (
	"context"
)

type contextKey int

const systemTenantKey contextKey = iota

// Ctx is a context that allows queries across all tenants. This must only be
// used for tasks that are not user request specific.
var Ctx = context.WithValue(context.Background(), systemTenantKey, systemTenantKey)

// Is returns true if the context has been marked to allow queries across all
// tenants.
func Is(ctx context.Context) bool {
	_, ok := ctx.Value(systemTenantKey).(contextKey)
	return ok
}
