package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/internal/tenant/tenanttest"
	"github.com/stretchr/testify/require"
)

func TestPurgeTenantShards(t *testing.T) {
	// TestPurgeTenantShards verifies both the basic shard purging functionality
	// and proper isolation between tenants. It ensures that:
	// 1. Shards are only purged when a valid tenant context is provided
	// 2. Only shards belonging to the specified tenant are purged
	// 3. Compound shards are preserved regardless of tenant
	// 4. Other tenants' shards remain untouched
	dir := t.TempDir()

	// Create test shards for different tenants
	tenant1Ctx := tenanttest.NewTestContext()
	tenant2Ctx := tenanttest.NewTestContext()

	// Helper to set tenant ID for test shards
	setTenantID := func(id int) func(in *zoekt.Repository) {
		return func(in *zoekt.Repository) {
			in.TenantID = id
		}
	}

	// Create test shards for tenant 1
	tenant1Shard1 := filepath.Join(dir, "tenant1_repo1.zoekt")
	tenant1Shard2 := filepath.Join(dir, "tenant1_repo2.zoekt")
	createTestShard(t, "tenant1_repo1", 1, tenant1Shard1, setTenantID(1))
	createTestShard(t, "tenant1_repo2", 2, tenant1Shard2, setTenantID(1))

	// Create test shards for tenant 2
	tenant2Shard := filepath.Join(dir, "tenant2_repo1.zoekt")
	createTestShard(t, "tenant2_repo1", 3, tenant2Shard, setTenantID(2))

	// Create a compound shard (should be skipped)
	compoundShard := filepath.Join(dir, "compound-1234.zoekt")
	createTestShard(t, "compound_repo", 4, compoundShard, setTenantID(1))

	// Test cases
	tests := []struct {
		name    string
		ctx     context.Context
		wantErr bool
		check   func(t *testing.T, dir string)
	}{
		{
			name:    "no tenant in context",
			ctx:     context.Background(),
			wantErr: true,
			check: func(t *testing.T, dir string) {
				// All files should still exist
				require.FileExists(t, tenant1Shard1)
				require.FileExists(t, tenant1Shard2)
				require.FileExists(t, tenant2Shard)
				require.FileExists(t, compoundShard)
			},
		},
		{
			name: "purge tenant 1 shards",
			ctx:  tenant1Ctx,
			check: func(t *testing.T, dir string) {
				// Tenant 1 shards should be deleted
				require.NoFileExists(t, tenant1Shard1)
				require.NoFileExists(t, tenant1Shard2)
				// Other shards should still exist
				require.FileExists(t, tenant2Shard)
				require.FileExists(t, compoundShard)
			},
		},
		{
			name: "purge tenant 2 shards",
			ctx:  tenant2Ctx,
			check: func(t *testing.T, dir string) {
				// Tenant 2 shard should be deleted
				require.NoFileExists(t, tenant2Shard)
				// Compound shard should still exist
				require.FileExists(t, compoundShard)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := purgeTenantShards(tt.ctx, dir)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			tt.check(t, dir)
		})
	}
}
