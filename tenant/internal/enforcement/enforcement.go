package enforcement

import (
	"os"

	"go.uber.org/atomic"
)

// EnforcementMode is the current tenant enforcement mode. It resides here
// instead of in the tenant package to avoid a circular dependency. See
// tenanttest.MockEnforce.
var EnforcementMode = atomic.NewString(os.Getenv("SRC_TENANT_ENFORCEMENT_MODE"))
