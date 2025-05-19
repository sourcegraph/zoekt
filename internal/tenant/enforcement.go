package tenant

import (
	"os"

	"github.com/sourcegraph/zoekt/internal/tenant/internal/enforcement"
)

func EnforceTenant() bool {
	switch enforcement.EnforcementMode.Load() {
	case "strict":
		return true
	default:
		return false
	}
}

// UseIDBasedShardNames returns true if the on disk layout of shards should
// instead use tenant ID and repository IDs in the names instead of the actual
// repository names.
//
// It is possible for repositories to have the same name, but have different
// content in a multi-tenant setup. As such, this implementation only returns
// true in those situations.
//
// Note: We could migrate all on-disk layout to only be ID based. However,
// ID's are a Sourcegraph specific feature so we will always need the two code
// paths. As such we only return true in multitenant setups.
//
// This is Sourcegraph specific.
func UseIDBasedShardNames() bool {
	// We use the presence of this environment variable to tell if we are in a
	// multi-tenant setup. This is the same check that is done in the
	// Sourcegraph monorepo.
	return os.Getenv("WORKSPACES_API_URL") != ""
}
