package tenanttest

import (
	"context"
	"testing"

	"github.com/sourcegraph/zoekt/internal/tenant/internal/enforcement"
	"github.com/sourcegraph/zoekt/internal/tenant/internal/tenanttype"
)

func MockEnforce(t *testing.T) {
	// prevent parallel tests from interfering with each other
	t.Setenv("mockEnforce", "true")

	old := enforcement.EnforcementMode.Load()
	t.Cleanup(func() {
		enforcement.EnforcementMode.Store(old)
	})

	enforcement.EnforcementMode.Store("strict")
}

func TenantEqualsID(t *testing.T, ctx context.Context, id int) bool {
	tnt, err := tenanttype.FromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}

	return tnt.ID() == id
}
