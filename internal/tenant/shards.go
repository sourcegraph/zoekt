package tenant

import "fmt"

// SrcPrefix returns the Sourcegraph prefix of a shard. We put it here to avoid
// circular dependencies.
func SrcPrefix(tenantID int, repoID uint32) string {
	return fmt.Sprintf("%09d_%09d", tenantID, repoID)
}
