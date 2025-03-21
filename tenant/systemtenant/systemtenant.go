// Package systemtenant exports UnsafeCtx which allows to access shards across
// all tenants. This must only be used for tasks that are not request specific.
package systemtenant

import (
	"context"
)

type contextKey int

const systemTenantKey contextKey = iota

// WithUnsafeContext taints the context to allow queries across all tenants.
// Never use this for user requests.
func WithUnsafeContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, systemTenantKey, systemTenantKey)
}

// Is returns true if the context has been marked to allow queries across all
// tenants.
func Is(ctx context.Context) bool {
	_, ok := ctx.Value(systemTenantKey).(contextKey)
	return ok
}
