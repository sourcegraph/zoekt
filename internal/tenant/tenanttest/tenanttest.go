package tenanttest

import (
	"context"
	"testing"

	"go.uber.org/atomic"

	"github.com/sourcegraph/zoekt/internal/tenant/internal/enforcement"
	"github.com/sourcegraph/zoekt/internal/tenant/internal/tenanttype"
)

func MockEnforce(t *testing.T) {
	// prevent parallel tests from interfering with each other
	t.Setenv("mockEnforce", "true")

	old := enforcement.EnforcementMode.Load()
	t.Cleanup(func() {
		enforcement.EnforcementMode.Store(old)
		ResetTestTenants()
	})

	enforcement.EnforcementMode.Store("strict")
}

// TestTenantCounter is a counter that is tracks tenants created from NewTestContext().
var TestTenantCounter atomic.Int64

func NewTestContext() context.Context {
	return tenanttype.WithTenant(context.Background(), mustTenantFromID(int(TestTenantCounter.Inc())))
}

// ResetTestTenants resets the test tenant counter that tracks the tenants
// created from NewTestContext().
func ResetTestTenants() {
	TestTenantCounter.Store(0)
}

func mustTenantFromID(id int) *tenanttype.Tenant {
	tenant, err := tenanttype.FromID(id)
	if err != nil {
		panic(err)
	}
	return tenant
}
