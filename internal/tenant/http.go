package tenant

import (
	"strconv"
)

// HttpExtraHeader returns header we send to gitserver given a tenant context.
func HttpExtraHeader(tenantID int) string {
	key := headerKeyTenantID + ": "
	if !EnforceTenant() {
		return key + "1"
	}
	return key + strconv.Itoa(tenantID)
}
