package tenanttest

import (
	"context"
	"testing"

	"github.com/sourcegraph/zoekt/internal/tenant"
	"github.com/sourcegraph/zoekt/internal/tenant/internal/enforcement"
)

// MockEnforce temporarily sets the tenant enforcement mode and returns a function
// to restore the original mode.
func MockEnforce(mode string) func() {
	original := enforcement.EnforcementMode.Load()
	enforcement.EnforcementMode.Store(mode)
	return func() {
		enforcement.EnforcementMode.Store(original)
	}
}

// TestContext creates a new context configured with the given tenant ID.
func TestContext(t *testing.T, tenantID uint32) context.Context {
	return tenant.WithTenantID(context.Background(), tenantID)
}

// NewTestContext creates a new test context with the tenant ID (backward compatibility)
func NewTestContext(tenantID uint32) context.Context {
	return tenant.WithTenantID(context.Background(), tenantID)
}