package tenant

import (
	"context"
	"runtime/pprof"
	"testing"

	"github.com/sourcegraph/zoekt/internal/tenant/tenanttest"
	"github.com/sourcegraph/zoekt/internal/trace"
)

// TestLogMissingTenantWithExistingProfile checks that logging a missing
// tenant does not panic when the embedding program already created the
// missing_tenant profile, as Sourcegraph's tenant package does.
func TestLogMissingTenantWithExistingProfile(t *testing.T) {
	tenanttest.MockEnforce(t)
	existing := pprof.NewProfile("missing_tenant")

	tr, ctx := trace.New(context.Background(), "test", "test")
	Log(ctx, tr)

	if got := pprofMissingTenant(); got != existing {
		t.Fatalf("got profile %p, want the existing one %p", got, existing)
	}
	if existing.Count() != 1 {
		t.Fatalf("existing profile has %d events, want 1", existing.Count())
	}
}
