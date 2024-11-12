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

const TenantsDir = "tenants"

// ContextIndexDir returns a context and index dir for the given tenant ID.
//
// 🚨 SECURITY: Do not use this function anywhere else than in
// sourcegraph-indexserver to derive a context and index directory from
// IndexOptions.
func ContextIndexDir(tenantID int, repoDir string) (context.Context, string, error) {
	if !EnforceTenant() {
		// Default to tenant 1 if enforcement is disabled.
		tnt, err := tenanttype.FromID(1)
		if err != nil {
			return nil, "", err
		}
		return tenanttype.WithTenant(context.Background(), tnt), repoDir, nil
	}
	tnt, err := tenanttype.FromID(tenantID)
	if err != nil {
		return nil, "", err
	}
	return tenanttype.WithTenant(context.Background(), tnt), filepath.Join(repoDir, TenantsDir, strconv.Itoa(tenantID)), nil
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
	fds, err := os.ReadDir(filepath.Join(indexDir, TenantsDir))
	if err != nil {
		log.Printf("listTenantDirs: error reading dir: %s", err)
		return nil
	}
	for _, file := range fds {
		if !file.IsDir() {
			continue
		}
		dirs = append(dirs, filepath.Join(indexDir, TenantsDir, file.Name()))
	}
	return dirs
}

type ContextRepoIDs struct {
	Ctx     context.Context
	RepoIDs []uint32
}

func toUint32(s []int32) []uint32 {
	u := make([]uint32, len(s))
	for i, v := range s {
		u[i] = uint32(v)
	}
	return u
}

func NewTenantRepoIdIterator(ctx context.Context, response *proto.ListResponse) iter.Seq2[*ContextRepoIDs, error] {
	// Guarantee backwards compatibility with Sourcegraph. During rollout, old
	// instances of Sourcegraph may still respond with RepoIds, in which case we
	// assume they belong to tenant 1.
	if len(response.RepoIds) != 0 {
		return func(yield func(*ContextRepoIDs, error) bool) {
			if !yield(&ContextRepoIDs{ctx, toUint32(response.RepoIds)}, nil) {
				return
			}
		}
	}

	return func(yield func(*ContextRepoIDs, error) bool) {
		for tenantID, v := range response.TenantIdReposMap {
			tnt, err := tenanttype.FromID(int(tenantID))
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(&ContextRepoIDs{tenanttype.WithTenant(ctx, tnt), toUint32(v.Ids)}, nil) {
				return
			}
		}
	}
}
