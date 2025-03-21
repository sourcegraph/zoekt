package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/multierr"

	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/tenant"
)

// purgeTenantShards removes all simple shards from dir on a best-effort basis.
// It returns an error if there is no tenant in the context or if it encounters
// an error while removing a shard.
func purgeTenantShards(ctx context.Context, dir string) error {
	tnt, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}

	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()

	names, err := d.Readdirnames(-1)
	if err != nil {
		return err
	}

	var merr error
	for _, n := range names {
		path := filepath.Join(dir, n)
		fi, err := os.Stat(path)
		if err != nil {
			merr = multierr.Append(merr, err)
			continue
		}
		if fi.IsDir() || filepath.Ext(path) != ".zoekt" {
			continue
		}

		// Skip compound shards.
		if strings.HasPrefix(filepath.Base(path), "compound-") {
			continue
		}

		repos, _, err := index.ReadMetadataPath(path)
		if err != nil {
			merr = multierr.Append(merr, err)
			continue
		}
		// Since we excluded compound shards, we know there is exactly one repo
		if repos[0].TenantID == tnt.ID() {
			paths, err := index.IndexFilePaths(path)
			if err != nil {
				merr = multierr.Append(merr, err)
				continue
			}
			for _, p := range paths {
				if err := os.Remove(p); err != nil {
					merr = multierr.Append(merr, err)
				}
			}
		}
	}

	return merr
}
