package tenant

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sourcegraph/zoekt/internal/tenant/internal/tenanttype"
)

// IsTenantPath returns true if the path is a subdirectory of tenant's index directory.
func IsTenantPath(ctx context.Context, path string) (bool, error) {
	if !EnforceTenant() {
		return true, nil
	}

	t, err := tenanttype.FromContext(ctx)
	if err != nil {
		return false, err
	}

	baseDir := filepath.Base(filepath.Dir(path))
	if !strings.EqualFold(baseDir, strconv.Itoa(t.ID())) {
		return false, nil
	}

	return true, nil
}

// WatchdogContext is a context for the watchdog. Don't use this context for
// anything else.
var WatchdogContext = tenanttype.WithTenant(context.Background(), mustTenantFromID(1))

// mustTenantFromID is a test helper which panics if the ID is invalid.
func mustTenantFromID(id int) *tenanttype.Tenant {
	tenant, err := tenanttype.FromID(id)
	if err != nil {
		panic(err)
	}
	return tenant
}
