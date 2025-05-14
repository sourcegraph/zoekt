package tenanttest

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/sourcegraph/zoekt/internal/tenant/internal/enforcement"
	"github.com/sourcegraph/zoekt/internal/tenant/internal/tenanttype"
)

// Counter for tenant IDs used in NewTestContext
var tenantIDCounter atomic.Int32

// resetTestTenants resets the tenant counter - called by MockEnforce and MockNoEnforce
func resetTestTenants() {
	tenantIDCounter.Store(0)
}

// MockEnforce temporarily sets the tenant enforcement mode to strict for the test.
// It automatically resets the enforcement mode after the test completes.
func MockEnforce(t *testing.T) {
	original := enforcement.EnforcementMode.Load()
	enforcement.EnforcementMode.Store("strict")
	// Reset tenant counter to ensure tests get predictable tenant IDs
	resetTestTenants()

	t.Cleanup(func() {
		enforcement.EnforcementMode.Store(original)
		resetTestTenants()
	})
}

// MockEnforceWithMode temporarily sets the tenant enforcement mode to the specified mode.
// It returns a cleanup function that should be called to restore the original mode.
func MockEnforceWithMode(mode string) func() {
	original := enforcement.EnforcementMode.Load()
	enforcement.EnforcementMode.Store(mode)
	return func() {
		enforcement.EnforcementMode.Store(original)
	}
}

// MockNoEnforce temporarily sets the tenant enforcement mode to disabled for the test.
// It automatically resets the enforcement mode after the test completes.
func MockNoEnforce(t *testing.T) {
	original := enforcement.EnforcementMode.Load()
	enforcement.EnforcementMode.Store("disabled")
	// Reset tenant counter to ensure tests get predictable tenant IDs
	resetTestTenants()

	t.Cleanup(func() {
		enforcement.EnforcementMode.Store(original)
		resetTestTenants()
	})
}

// TestContext creates a new context configured with the given tenant ID.
func TestContext(t *testing.T, tenantID uint32) context.Context {
	tenant, err := tenanttype.FromID(int(tenantID))
	if err != nil {
		t.Fatalf("Failed to create tenant from ID %d: %v", tenantID, err)
	}
	return tenanttype.WithTenant(context.Background(), tenant)
}

// NewTestContext creates a new test context with incrementing tenant IDs
func NewTestContext() context.Context {
	// Increment counter and get new tenant ID (starting at 1)
	id := tenantIDCounter.Add(1)
	if id == 0 {
		id = tenantIDCounter.Add(1) // Skip 0 as it's invalid
	}

	tenant, err := tenanttype.FromID(int(id))
	if err != nil {
		panic(err)
	}
	return tenanttype.WithTenant(context.Background(), tenant)
}
