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

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
	"github.com/google/zoekt/gitindex"
)

var _ = log.Println

type largeFilesFlag []string

func (f *largeFilesFlag) String() string {
	s := append([]string{""}, *f...)
	return strings.Join(s, "-large_file ")
}

func (f *largeFilesFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func main() {
	var sizeMax = flag.Int("file_limit", 128*1024, "maximum file size")
	var shardLimit = flag.Int("shard_limit", 100<<20, "maximum corpus size for a shard")
	var parallelism = flag.Int("parallelism", 4, "maximum number of parallel indexing processes.")
	allowMissing := flag.Bool("allow_missing_branches", false, "allow missing branches.")
	submodules := flag.Bool("submodules", true, "if set to false, do not recurse into submodules")
	branchesStr := flag.String("branches", "HEAD", "git branches to index.")
	branchPrefix := flag.String("prefix", "refs/heads/", "prefix for branch names")

	indexDir := flag.String("index", build.DefaultDir, "index directory for *.zoekt files.")
	incremental := flag.Bool("incremental", true, "only index changed repositories")
	repoCacheDir := flag.String("repo_cache", "", "directory holding bare git repos, named by URL. "+
		"this is used to find repositories for submodules. "+
		"It also affects name if the indexed repository is under this directory.")
	ctags := flag.Bool("require_ctags", false, "If set, ctags calls must succeed.")
	version := flag.Bool("version", false, "Print version number")
	largeFiles := largeFilesFlag{}
	flag.Var(&largeFiles, "large_file", "A glob pattern where matching files are to be index regardless of their size.")
	flag.Parse()

	if *version {
		fmt.Printf("zoekt-git-index version %q\n", zoekt.Version)
		os.Exit(0)
	}

	if *repoCacheDir != "" {
		dir, err := filepath.Abs(*repoCacheDir)
		if err != nil {
			log.Fatalf("Abs: %v", err)
		}
		*repoCacheDir = dir
	}
	opts := build.Options{
		Parallelism:      *parallelism,
		SizeMax:          *sizeMax,
		ShardMax:         *shardLimit,
		IndexDir:         *indexDir,
		CTagsMustSucceed: *ctags,
		LargeFiles:       largeFiles,
	}
	opts.SetDefaults()

	var branches []string
	if *branchesStr != "" {
		branches = strings.Split(*branchesStr, ",")
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
			BuildOptions:       opts,
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
