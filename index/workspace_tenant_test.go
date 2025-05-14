package index

import (
	"path/filepath"
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/internal/tenant/tenanttest"
)

func TestShardNameWithTenantAndWorkspaces(t *testing.T) {
	tests := []struct {
		name              string
		setupEnv          func(t *testing.T)
		opts              Options
		expectedShardName string
	}{
		{
			name: "default_no_tenant_no_workspaces",
			setupEnv: func(t *testing.T) {
				tenanttest.MockNoEnforce(t)
				t.Setenv("WORKSPACES_API_URL", "")
			},
			opts: Options{
				IndexDir: "/tmp/zoekt",
				RepositoryDescription: zoekt.Repository{
					Name: "example/repo",
				},
				TenantID: 123,
				RepoID:   456,
			},
			expectedShardName: "/tmp/zoekt/example%2Frepo_v16.00000.zoekt",
		},
		{
			name: "tenant_enforcement_enabled",
			setupEnv: func(t *testing.T) {
				tenanttest.MockEnforce(t)
				t.Setenv("WORKSPACES_API_URL", "")
			},
			opts: Options{
				IndexDir: "/tmp/zoekt",
				RepositoryDescription: zoekt.Repository{
					Name: "example/repo",
				},
				TenantID: 123,
				RepoID:   456,
			},
			expectedShardName: "/tmp/zoekt/000000123_000000456_v16.00000.zoekt",
		},
		{
			name: "workspaces_enabled",
			setupEnv: func(t *testing.T) {
				// Make sure tenant enforcement is disabled
				tenanttest.MockNoEnforce(t)
				t.Setenv("WORKSPACES_API_URL", "http://workspaces-api")
			},
			opts: Options{
				IndexDir: "/tmp/zoekt",
				RepositoryDescription: zoekt.Repository{
					Name: "example/repo",
				},
				TenantID: 123,
				RepoID:   456,
			},
			expectedShardName: "/tmp/zoekt/workspaces/example%2Frepo_v16.00000.zoekt",
		},
		{
			name: "tenant_and_workspaces_enabled",
			setupEnv: func(t *testing.T) {
				tenanttest.MockEnforce(t)
				t.Setenv("WORKSPACES_API_URL", "http://workspaces-api")
			},
			opts: Options{
				IndexDir: "/tmp/zoekt",
				RepositoryDescription: zoekt.Repository{
					Name: "example/repo",
				},
				TenantID: 123,
				RepoID:   456,
			},
			expectedShardName: "/tmp/zoekt/workspaces/000000123_000000456_v16.00000.zoekt",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setupEnv != nil {
				tc.setupEnv(t)
			}

			gotShardName := tc.opts.shardName(0)
			// Normalize path separators for cross-platform testing
			gotShardName = filepath.ToSlash(gotShardName)
			expectedShardName := filepath.ToSlash(tc.expectedShardName)

			if gotShardName != expectedShardName {
				t.Errorf("Expected shard name %q, got %q", expectedShardName, gotShardName)
			}
		})
	}
}
