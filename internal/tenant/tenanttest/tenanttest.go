package tenanttest

import (
	"testing"

	"github.com/sourcegraph/zoekt/internal/tenant/internal/enforcement"
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
