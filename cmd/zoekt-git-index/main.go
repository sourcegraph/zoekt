// Copyright 2016 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/zoekt/cmd"
	"github.com/google/zoekt/gitindex"
	"go.uber.org/automaxprocs/maxprocs"
)

func main() {
	allowMissing := flag.Bool("allow_missing_branches", false, "allow missing branches.")
	submodules := flag.Bool("submodules", true, "if set to false, do not recurse into submodules")
	branchesStr := flag.String("branches", "HEAD", "comma separated list of branch names (ex: HEAD,main) or commit ranges (ex: deadbeef..HEAD) to index.")
	branchPrefix := flag.String("prefix", "refs/heads/", "prefix for branch names")

	incremental := flag.Bool("incremental", true, "only index changed repositories")
	repoCacheDir := flag.String("repo_cache", "", "directory holding bare git repos, named by URL. "+
		"this is used to find repositories for submodules. "+
		"It also affects name if the indexed repository is under this directory.")
	flag.Parse()

	// Tune GOMAXPROCS to match Linux container CPU quota.
	_, _ = maxprocs.Set()

	if *repoCacheDir != "" {
		dir, err := filepath.Abs(*repoCacheDir)
		if err != nil {
			log.Fatalf("Abs: %v", err)
		}
		*repoCacheDir = dir
	}
	opts := cmd.OptionsFromFlags()

	var branches []gitindex.Branch
	if *branchesStr != "" {
		bs, err := parseBranchString(*branchesStr)
		if err != nil {
			log.Fatalf("failed to parse 'branches' argument %q: %s", bs, err)
		}

		branches = bs
	}

	gitRepos := map[string]string{}
	for _, repoDir := range flag.Args() {
		repoDir, err := filepath.Abs(repoDir)
		if err != nil {
			log.Fatal(err)
		}
		repoDir = filepath.Clean(repoDir)

		name := strings.TrimSuffix(repoDir, "/.git")
		if *repoCacheDir != "" && strings.HasPrefix(name, *repoCacheDir) {
			name = strings.TrimPrefix(name, *repoCacheDir+"/")
			name = strings.TrimSuffix(name, ".git")
		} else {
			name = strings.TrimSuffix(filepath.Base(name), ".git")
		}
		gitRepos[repoDir] = name
	}

	exitStatus := 0
	for dir, name := range gitRepos {
		opts.RepositoryDescription.Name = name
		gitOpts := gitindex.Options{
			BranchPrefix:       *branchPrefix,
			Incremental:        *incremental,
			Submodules:         *submodules,
			RepoCacheDir:       *repoCacheDir,
			AllowMissingBranch: *allowMissing,
			BuildOptions:       *opts,
			Branches:           branches,
			RepoDir:            dir,
		}

		if err := gitindex.IndexGitRepo(gitOpts); err != nil {
			log.Printf("indexGitRepo(%s): %v", dir, err)
			exitStatus = 1
		}
	}
	os.Exit(exitStatus)
}

func parseBranchString(s string) ([]gitindex.Branch, error) {
	var out []gitindex.Branch

	for _, b := range strings.Split(s, ",") {
		if b == "" {
			continue
		}

		var priorCommit string
		var name string

		parts := strings.Split(b, "..")

		switch len(parts) {
		case 1:
			name = parts[0]
		case 2:
			priorCommit = parts[0]
			name = parts[1]
		default:
			return nil, fmt.Errorf("expected 1 or 2 parts when splitting %q, got: %d", b, len(parts))
		}

		if name == "" {
			return nil, fmt.Errorf("%q has no branch name", b)
		}

		out = append(out, gitindex.Branch{
			Name:        name,
			PriorCommit: priorCommit,
		})
	}

	return out, nil
}
