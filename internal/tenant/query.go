package tenant

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sourcegraph/zoekt/internal/tenant/internal/tenanttype"
)

// Match returns true if the path is a subdirectory of tenant's index directory.
func Match(ctx context.Context, path string) bool {
	if !EnforceTenant() {
		return true
	}

	t, err := tenanttype.FromContext(ctx)
	if err != nil {
		return false
	}

	baseDir := filepath.Base(filepath.Dir(path))
	if !strings.EqualFold(baseDir, strconv.Itoa(t.ID())) {
		return false
	}

	return true
}
