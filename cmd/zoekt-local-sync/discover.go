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
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type repositorySpec struct {
	Name   string
	Source string
}

func resolveRoots(paths []string) ([]string, error) {
	roots := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		root, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve root %q: %w", path, err)
		}
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("stat root %s: %w", root, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("root %s is not a directory", root)
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return nil, fmt.Errorf("resolve symlinks for root %s: %w", root, err)
		}
		if _, ok := seen[root]; ok {
			return nil, fmt.Errorf("duplicate root %s", root)
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	return roots, nil
}

func discoverRoot(root string) ([]repositorySpec, error) {
	rootDir, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open root %s: %w", root, err)
	}
	defer rootDir.Close()

	rootFS := rootDir.FS()
	var repositories []repositorySpec
	add := func(relativePath string, bare bool) error {
		source := filepath.Join(root, filepath.FromSlash(relativePath))
		name := relativePath
		if name == "." {
			name = filepath.Base(root)
		}
		if bare {
			name = strings.TrimSuffix(name, ".git")
		}
		repositories = append(repositories, repositorySpec{
			Name:   filepath.ToSlash(name),
			Source: source,
		})
		return fs.SkipDir
	}

	err = fs.WalkDir(rootFS, ".", func(relativePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}

		dotGit := path.Join(relativePath, ".git")
		dotGitInfo, err := fs.Stat(rootFS, dotGit)
		if err == nil && (dotGitInfo.IsDir() || dotGitInfo.Mode().IsRegular()) {
			return add(relativePath, false)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", filepath.Join(root, filepath.FromSlash(dotGit)), err)
		}

		entryName := entry.Name()
		if relativePath == "." {
			entryName = filepath.Base(root)
		}
		if !strings.HasSuffix(entryName, ".git") {
			return nil
		}
		objectsInfo, err := fs.Stat(rootFS, path.Join(relativePath, "objects"))
		if errors.Is(err, fs.ErrNotExist) || (err == nil && !objectsInfo.IsDir()) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stat bare repository %s: %w", relativePath, err)
		}

		return add(relativePath, true)
	})
	if err != nil {
		return nil, fmt.Errorf("discover repositories under %s: %w", root, err)
	}
	return repositories, nil
}

func discoverRepositories(rootPaths []string) ([]repositorySpec, error) {
	roots, err := resolveRoots(rootPaths)
	if err != nil {
		return nil, err
	}

	byName := map[string]repositorySpec{}
	bySource := map[string]repositorySpec{}
	var repositories []repositorySpec
	for _, root := range roots {
		discovered, err := discoverRoot(root)
		if err != nil {
			return nil, err
		}
		for _, repo := range discovered {
			if previous, ok := byName[repo.Name]; ok {
				return nil, fmt.Errorf("duplicate repository name %q for %s and %s", repo.Name, previous.Source, repo.Source)
			}
			if previous, ok := bySource[repo.Source]; ok {
				return nil, fmt.Errorf("repository %s was discovered by more than one root as %q and %q", repo.Source, previous.Name, repo.Name)
			}
			byName[repo.Name] = repo
			bySource[repo.Source] = repo
			repositories = append(repositories, repo)
		}
	}

	sort.Slice(repositories, func(i, j int) bool {
		return repositories[i].Name < repositories[j].Name
	})
	return repositories, nil
}
