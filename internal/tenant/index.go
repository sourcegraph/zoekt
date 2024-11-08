package tenant

import (
	"context"
	"iter"
	"log"
	"os"
	"path/filepath"
	"strconv"

	proto "github.com/sourcegraph/zoekt/cmd/zoekt-sourcegraph-indexserver/protos/sourcegraph/zoekt/configuration/v1"
	"github.com/sourcegraph/zoekt/internal/tenant/internal/tenanttype"
)

// ContextIndexDir returns a context and index dir for the given tenant ID.
func ContextIndexDir(tenantID int, repoDir string) (context.Context, string) {
	if !EnforceTenant() {
		// Default to tenant 1 if enforcement is disabled.
		return tenanttype.WithTenant(context.Background(), 1), repoDir
	}
	return tenanttype.WithTenant(context.Background(), tenantID), filepath.Join(repoDir, TenantsDir, strconv.Itoa(tenantID))
}

// HttpExtraHeader returns header we send to gitserver given a tenant context.
func HttpExtraHeader(ctx context.Context) string {
	key := headerKeyTenantID + ": "
	if !EnforceTenant() {
		return key + "1"
	}
	tnt, err := tenanttype.FromContext(ctx)
	if err != nil {
		return key + headerValueNoTenant
	}
	return key + strconv.Itoa(tnt.ID())
}

// ListIndexDirs returns a list of index directories for all tenants. If tenant
// enforcement is disabled, the list is []string{indexDir}.
func ListIndexDirs(indexDir string) []string {
	if !EnforceTenant() {
		return []string{indexDir}
	}

	var dirs []string
	files, err := os.ReadDir(filepath.Join(indexDir, TenantsDir))
	if err != nil {
		log.Printf("listTenantDirs: error reading dir: %s", err)
		return nil
	}
	for _, file := range files {
		if !file.IsDir() {
			continue
		}
		dirs = append(dirs, filepath.Join(indexDir, TenantsDir, file.Name()))
	}
	return dirs
}

func NewTenantRepoIdIterator(ctx context.Context, response *proto.ListResponse) iter.Seq2[context.Context, []uint32] {
	if !EnforceTenant() {
		// yield the original context and all repo ids of the first tenant. The
		// assumption is that Sourcegraph sends all repos assigned to tenant 1 if tenant
		// enforcement is disabled.
		return func(yield func(ctx context.Context, ids []uint32) bool) {
			for _, v := range response.TenantIdReposMap {
				yield(ctx, v.Ids)
				return
			}
		}
	}

	return func(yield func(ctx context.Context, ids []uint32) bool) {
		for tenantID, v := range response.TenantIdReposMap {
			if !yield(tenanttype.WithTenant(ctx, int(tenantID)), v.Ids) {
				return
			}
		}
	}
}
