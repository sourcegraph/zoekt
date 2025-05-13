# Tenant Enforcement and Workspace Layout in Zoekt

## Overview

Zoekt supports two distinct but related features:

1. **Tenant Enforcement**: Ensures that tenants can only access their own repository data. This is controlled by the `SRC_TENANT_ENFORCEMENT_MODE` environment variable.

2. **Workspace Layout**: Determines how shards are organized on disk based on workspace requirements. This is controlled by the `WORKSPACES_API_URL` environment variable.

Both features affect how shard files are named and organized on disk, but they serve different purposes and can be enabled independently.

## Tenant Enforcement

Tenant enforcement ensures that repositories from different tenants are isolated from each other. 

### Configuration

- Set `SRC_TENANT_ENFORCEMENT_MODE=strict` to enable tenant enforcement.
- Set `SRC_TENANT_ID` to specify the tenant ID.
- Set `SRC_REPO_ID` to specify the repository ID.

When tenant enforcement is enabled, shard file names will include tenant and repository IDs in the format: `{tenant_id}_{repo_id}_v{version}_{shard_number}.zoekt`.

## Workspace Layout

Workspace layout determines how shards are stored on disk, specifically for workspace-enabled instances.

### Configuration

- Set `WORKSPACES_API_URL` to any non-empty value to enable workspace layout.

When workspace layout is enabled, shards will be placed in a `workspaces` subdirectory of the configured index directory.

## Combined Usage

You can enable both tenant enforcement and workspace layout independently. For example:

```sh
# Enable tenant enforcement only
export SRC_TENANT_ENFORCEMENT_MODE=strict
export SRC_TENANT_ID=123
export SRC_REPO_ID=456

# Enable workspace layout only
export WORKSPACES_API_URL=http://workspaces-api

# Enable both
export SRC_TENANT_ENFORCEMENT_MODE=strict
export SRC_TENANT_ID=123
export SRC_REPO_ID=456
export WORKSPACES_API_URL=http://workspaces-api
```

## Migration

If you are migrating from an existing Zoekt deployment, consider the following:

1. Enabling tenant enforcement or workspace layout will not automatically migrate existing shards.
2. For a smooth transition, you should rebuild your index after enabling these features.
3. You may need to adjust your search tools to be aware of the new shard locations.

## Command Line Tools

All Zoekt command line tools (zoekt-index, zoekt-git-index, etc.) respect both tenant enforcement and workspace layout settings via environment variables. 

You no longer need to specify shard prefixes explicitly—they are determined automatically based on tenant enforcement and repository ID settings.