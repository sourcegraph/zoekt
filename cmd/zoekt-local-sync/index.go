// Copyright 2026 Sourcegraph, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/gitindex"
	"github.com/sourcegraph/zoekt/index"
)

type shardInfo struct {
	Path       string
	Repository *zoekt.Repository
}

type repositoryKey struct {
	Name   string
	Source string
}

type repositoryRecord struct {
	Name   string
	Source string
	Shards []string
}

type pruneAction struct {
	Shard  string
	Name   string
	Source string
	Reason string
}

func readInventory(indexDir string) ([]shardInfo, error) {
	entries, err := os.ReadDir(indexDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read index directory %s: %w", indexDir, err)
	}

	var shards []shardInfo
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".zoekt" {
			continue
		}
		path := filepath.Join(indexDir, entry.Name())
		repositories, _, err := index.ReadMetadataPathAlive(path)
		if err != nil {
			return nil, fmt.Errorf("read metadata from %s: %w", path, err)
		}
		if len(repositories) != 1 {
			return nil, fmt.Errorf("index shard %s contains %d repositories, want 1", path, len(repositories))
		}
		shards = append(shards, shardInfo{Path: path, Repository: repositories[0]})
	}
	return shards, nil
}

func normalizeSource(source string) string {
	if source == "" {
		return ""
	}
	if abs, err := filepath.Abs(source); err == nil {
		source = abs
	} else {
		source = filepath.Clean(source)
	}
	if filepath.Base(source) == ".git" {
		source = filepath.Dir(source)
	}
	return source
}

func planPrune(desired []repositorySpec, shards []shardInfo) []pruneAction {
	desiredBySource := make(map[string]repositorySpec, len(desired))
	for _, repo := range desired {
		desiredBySource[normalizeSource(repo.Source)] = repo
	}

	var actions []pruneAction
	for _, shard := range shards {
		repo := shard.Repository
		desiredRepo, exists := desiredBySource[normalizeSource(repo.Source)]
		if exists && desiredRepo.Name == repo.Name {
			continue
		}

		reason := "repository is no longer selected"
		if exists {
			reason = fmt.Sprintf("repository is now named %q", desiredRepo.Name)
		}
		actions = append(actions, pruneAction{
			Shard:  shard.Path,
			Name:   repo.Name,
			Source: normalizeSource(repo.Source),
			Reason: reason,
		})
	}

	sort.Slice(actions, func(i, j int) bool { return actions[i].Shard < actions[j].Shard })
	return actions
}

func removeShard(path string) error {
	paths, err := index.IndexFilePaths(path)
	if err != nil {
		return err
	}
	var errs []error
	// Remove the optional metadata sidecar first. If deletion then fails, the
	// shard remains self-consistent and can be retried on the next sync.
	for i := len(paths) - 1; i >= 0; i-- {
		path := paths[i]
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func applyRemovals(actions []pruneAction, dryRun bool, out io.Writer) error {
	var errs []error
	for _, action := range actions {
		if dryRun {
			fmt.Fprintf(out, "Would remove %s (repository %q, source %s: %s)\n", action.Shard, action.Name, action.Source, action.Reason)
			continue
		}
		fmt.Fprintf(out, "Removing %s (repository %q, source %s: %s)\n", action.Shard, action.Name, action.Source, action.Reason)
		if err := removeShard(action.Shard); err != nil {
			errs = append(errs, fmt.Errorf("remove shard %s: %w", action.Shard, err))
		}
	}
	return errors.Join(errs...)
}

func indexRepositories(repositories []repositorySpec, opts gitindex.Options, out io.Writer) error {
	var errs []error
	for _, repo := range repositories {
		repoOpts := opts
		repoOpts.RepoDir = repo.Source
		repoOpts.BuildOptions.RepositoryDescription = zoekt.Repository{Name: repo.Name}
		if !repoOpts.DryRun {
			fmt.Fprintf(out, "Indexing %q from %s\n", repo.Name, repo.Source)
		}
		updated, err := gitindex.IndexGitRepo(repoOpts)
		if err != nil {
			errs = append(errs, fmt.Errorf("index %q from %s: %w", repo.Name, repo.Source, err))
			continue
		}
		if repoOpts.DryRun && updated {
			fmt.Fprintf(out, "Would index %q from %s\n", repo.Name, repo.Source)
		} else if updated {
			fmt.Fprintf(out, "Indexed %q from %s\n", repo.Name, repo.Source)
		} else {
			fmt.Fprintf(out, "Up to date %q from %s\n", repo.Name, repo.Source)
		}
	}
	return errors.Join(errs...)
}

func recordsFromShards(shards []shardInfo) []repositoryRecord {
	records := map[repositoryKey]*repositoryRecord{}
	for _, shard := range shards {
		repo := shard.Repository
		key := repositoryKey{Name: repo.Name, Source: normalizeSource(repo.Source)}
		record := records[key]
		if record == nil {
			record = &repositoryRecord{Name: key.Name, Source: key.Source}
			records[key] = record
		}
		record.Shards = append(record.Shards, shard.Path)
	}

	result := make([]repositoryRecord, 0, len(records))
	for _, record := range records {
		sort.Strings(record.Shards)
		result = append(result, *record)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].Source < result[j].Source
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func listRepositories(indexDir string, out io.Writer) error {
	shards, err := readInventory(indexDir)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(out, 8, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSOURCE\tSHARDS")
	for _, record := range recordsFromShards(shards) {
		fmt.Fprintf(w, "%s\t%s\t%s\n", record.Name, record.Source, strings.Join(record.Shards, ","))
	}
	return w.Flush()
}

func selectRecords(records []repositoryRecord, selectors []string) ([]repositoryRecord, error) {
	selected := map[repositoryKey]repositoryRecord{}
	for _, selector := range selectors {
		var matches []repositoryRecord
		for _, record := range records {
			if record.Name == selector {
				matches = append(matches, record)
			}
		}
		if len(matches) == 0 {
			source := normalizeSource(selector)
			for _, record := range records {
				if record.Source != "" && record.Source == source {
					matches = append(matches, record)
				}
			}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("repository %q not found", selector)
		}
		if len(matches) > 1 {
			return nil, fmt.Errorf("repository selector %q is ambiguous; use an exact source path", selector)
		}
		match := matches[0]
		selected[repositoryKey{Name: match.Name, Source: match.Source}] = match
	}

	result := make([]repositoryRecord, 0, len(selected))
	for _, record := range selected {
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].Source < result[j].Source
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func removeRepositories(indexDir string, selectors []string, dryRun bool, out io.Writer) error {
	shards, err := readInventory(indexDir)
	if err != nil {
		return err
	}
	records, err := selectRecords(recordsFromShards(shards), selectors)
	if err != nil {
		return err
	}

	var actions []pruneAction
	for _, record := range records {
		for _, shardPath := range record.Shards {
			actions = append(actions, pruneAction{
				Shard:  shardPath,
				Name:   record.Name,
				Source: record.Source,
				Reason: "explicitly selected",
			})
		}
	}

	sort.Slice(actions, func(i, j int) bool { return actions[i].Shard < actions[j].Shard })
	return applyRemovals(actions, dryRun, out)
}
