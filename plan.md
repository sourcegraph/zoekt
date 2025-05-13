# Plan for DINF-940: Make zoekt-enforcetenant Work for Non-workspace Instances

## Context and Background

Currently, tenant enforcement in Zoekt is tied to the disk layout through the `shardPrefix` mechanism. The enforcement mechanism is controlled by the `SRC_TENANT_ENFORCEMENT_MODE` environment variable, which when set to "strict" enables the `tenant.EnforceTenant()` function to return true.

When tenant enforcement is enabled, it generates a special shard prefix in the format `{tenant_id}_{repo_id}` using the `tenant.SrcPrefix()` function. However, this implementation has created a dependency between tenant enforcement and disk layout decisions.

We want to refactor this so that:
1. Tenant enforcement remains a separate concept from disk layout choice
2. Disk layout should be determined by whether the `WORKSPACES_API_URL` environment variable is set
3. Introduce a `workspaces.Enabled()` function to check which disk layout to use

## Current Implementation Status

A work-in-progress implementation has been started as indicated by the latest commit with message "wip". The key changes include:

1. Removed direct use of the `tenant` package in `cmd/zoekt-sourcegraph-indexserver/index.go`
2. Changed from passing `ShardPrefix` to directly passing `TenantID` and `RepoID` to the index options
3. Modified the `shardNameVersion` function in `index/builder.go` to determine the prefix based on tenant enforcement status, tenant ID, and repo ID
4. Removed the `-shard_prefix` CLI flag

Specifically, the implementation now:
- Uses the tenant/repo IDs along with `tenant.EnforceTenant()` to determine the shard prefix directly in the builder
- Avoids passing explicit shard prefixes through the command-line arguments

## Implementation Plan

### 1. Complete the Current Refactoring

- [x] Remove the `ShardPrefix` field from `index.Options` and related code
- [x] Add `TenantID` and `RepoID` fields to `index.Options`
- [x] Update `shardNameVersion` to use tenant information when determining shard names

### 2. Create Workspaces Package

- [x] Create a new package `internal/workspaces` with:
  - [x] `Enabled()` function that checks if `WORKSPACES_API_URL` environment variable is set
  - [x] Helper functions for workspace-related operations

### 3. Separate Tenant Enforcement from Disk Layout

- [x] Modify the `shardNameVersion` function in `index/builder.go` to use both:
  - [x] `tenant.EnforceTenant()` for tenant-related decisions
  - [x] `workspaces.Enabled()` for disk layout decisions

### 4. Update CLI Tools and Tests

- [ ] Update CLI tools to support the new parameters and remove obsolete ones
- [ ] Update the tests to verify both tenant enforcement and workspace layouts work correctly
- [ ] Ensure backward compatibility for existing deployments

### 5. Documentation and Integration

- [ ] Update documentation to reflect the changes in environment variables and configuration
- [ ] Add examples for different deployment scenarios
- [ ] Document migration path for existing deployments

## Testing Strategy

1. Unit tests to verify `workspaces.Enabled()` functionality
2. Integration tests that simulate different combinations of tenant enforcement and workspace settings
3. Backward compatibility tests to ensure existing deployments continue to work

## Status

This work is in progress. The current implementation has completed steps 1-3 of the plan:
1. Changed from explicit shard prefixes to using tenant/repo IDs for determining prefixes
2. Created a workspaces package with an Enabled() function that checks for WORKSPACES_API_URL
3. Modified the shardNameVersion function to separate tenant enforcement from disk layout decisions

Steps 4-5 remain to be implemented.