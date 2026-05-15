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

// Command zoekt-git-index indexes a single git repository. It works directly with git
// repositories and supports git-specific features like branches and submodules.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/pprof"
	"strings"

	"github.com/dustin/go-humanize"
	"go.uber.org/automaxprocs/maxprocs"

	"github.com/sourcegraph/zoekt/cmd"
	"github.com/sourcegraph/zoekt/gitindex"
	"github.com/sourcegraph/zoekt/internal/ctags"
	"github.com/sourcegraph/zoekt/internal/profiler"
)

func run() int {
	allowMissing := flag.Bool("allow_missing_branches", false, "allow missing branches.")
	submodules := flag.Bool("submodules", true, "if set to false, do not recurse into submodules")
	branchesStr := flag.String("branches", "HEAD", "git branches to index.")
	worktrees := flag.Bool("worktrees", false, "treat arguments as worktrees of one repository and index their attached HEAD branches together")
	branchPrefix := flag.String("prefix", "refs/heads/", "prefix for branch names")

	incremental := flag.Bool("incremental", true, "only index changed repositories")
	repoCacheDir := flag.String("repo_cache", "", "directory holding bare git repos, named by URL. "+
		"this is used to find repositories for submodules. "+
		"It also affects name if the indexed repository is under this directory.")
	isDelta := flag.Bool("delta", false, "whether we should use delta build")
	deltaShardNumberFallbackThreshold := flag.Uint64("delta_threshold", 0, "upper limit on the number of preexisting shards that can exist before attempting a delta build (0 to disable fallback behavior)")
	deltaAdmissionMode := flag.String("delta_admission_mode", "", "experimental delta admission mode (empty or stats-v1)")
	deltaAdmissionLogJSON := flag.String("delta_admission_log_json", "", "append experimental delta admission decisions as JSON lines to this file")
	resolveHEADToBranch := flag.Bool("resolve_head_to_branch", false, "resolve attached HEAD to its short branch name in indexed metadata")
	allowDeltaBranchSetChange := flag.Bool("allow_delta_branch_set_change", false, "allow delta builds to update branch sets by rewriting old shard metadata and tombstoning old live paths")
	languageMap := flag.String("language_map", "", "a mapping between a language and its ctags processor (a:0,b:3).")

	cpuProfile := flag.String("cpu_profile", "", "write cpu profile to `file`")

	flag.Parse()
	branchesFlagSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "branches" {
			branchesFlagSet = true
		}
	})

	// Tune GOMAXPROCS to match Linux container CPU quota.
	_, _ = maxprocs.Set()

	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			log.Fatal("could not create CPU profile: ", err)
		}
		defer f.Close() // error handling omitted for example
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal("could not start CPU profile: ", err)
		}
		defer pprof.StopCPUProfile()
	}

	if *repoCacheDir != "" {
		dir, err := filepath.Abs(*repoCacheDir)
		if err != nil {
			log.Fatalf("Abs: %v", err)
		}
		*repoCacheDir = dir
	}

	opts := cmd.OptionsFromFlags()
	opts.IsDelta = *isDelta

	var branches []string
	if *branchesStr != "" {
		branches = strings.Split(*branchesStr, ",")
	}

	gitRepos, err := gitRepoSpecs(flag.Args(), *repoCacheDir, branches, *resolveHEADToBranch, *worktrees, branchesFlagSet)
	if err != nil {
		log.Fatal(err)
	}

	opts.LanguageMap = make(ctags.LanguageMap)
	for _, mapping := range strings.Split(*languageMap, ",") {
		m := strings.Split(mapping, ":")
		if len(m) != 2 {
			continue
		}
		opts.LanguageMap[m[0]] = ctags.StringToParser(m[1])
	}

	if heapProfileTrigger := os.Getenv("ZOEKT_HEAP_PROFILE_TRIGGER"); heapProfileTrigger != "" {
		trigger, err := humanize.ParseBytes(heapProfileTrigger)
		if err != nil {
			log.Printf("invalid value for ZOEKT_HEAP_PROFILE_TRIGGER: %v", err)
		} else {
			opts.HeapProfileTriggerBytes = trigger
		}
	}

	profiler.Init("zoekt-git-index")
	exitStatus := 0
	for _, repoSpec := range gitRepos {
		opts.RepositoryDescription.Name = repoSpec.name
		gitOpts := gitindex.Options{
			BranchPrefix:                      *branchPrefix,
			Incremental:                       *incremental,
			Submodules:                        *submodules,
			RepoCacheDir:                      *repoCacheDir,
			AllowMissingBranch:                *allowMissing,
			BuildOptions:                      *opts,
			Branches:                          repoSpec.branches,
			RepoDir:                           repoSpec.dir,
			DeltaShardNumberFallbackThreshold: *deltaShardNumberFallbackThreshold,
			DeltaAdmissionMode:                *deltaAdmissionMode,
			DeltaAdmissionLogPath:             *deltaAdmissionLogJSON,
			ResolveHEADToBranch:               repoSpec.resolveHEADToBranch,
			AllowDeltaBranchSetChange:         *allowDeltaBranchSetChange,
		}

		if _, err := gitindex.IndexGitRepo(gitOpts); err != nil {
			log.Printf("indexGitRepo(%s, delta=%t): %v", repoSpec.dir, gitOpts.BuildOptions.IsDelta, err)
			exitStatus = 1
		}
	}

	return exitStatus
}

type gitRepoSpec struct {
	dir                 string
	name                string
	branches            []string
	resolveHEADToBranch bool
}

func gitRepoSpecs(args []string, repoCacheDir string, branches []string, resolveHEADToBranch, worktrees, branchesFlagSet bool) ([]gitRepoSpec, error) {
	if worktrees {
		return gitRepoSpecsForWorktrees(args, repoCacheDir, branchesFlagSet)
	}

	specs := make([]gitRepoSpec, 0, len(args))
	for _, repoDir := range args {
		repoDir, err := filepath.Abs(repoDir)
		if err != nil {
			return nil, err
		}
		repoDir = filepath.Clean(repoDir)
		specs = append(specs, gitRepoSpec{
			dir:                 repoDir,
			name:                inferRepoName(repoDir, repoCacheDir),
			branches:            append([]string(nil), branches...),
			resolveHEADToBranch: resolveHEADToBranch,
		})
	}
	return specs, nil
}

func gitRepoSpecsForWorktrees(args []string, repoCacheDir string, branchesFlagSet bool) ([]gitRepoSpec, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("-worktrees requires at least one worktree path")
	}
	if branchesFlagSet {
		return nil, fmt.Errorf("-worktrees cannot be combined with -branches; branches are inferred from each worktree HEAD")
	}

	var firstDir string
	var commonDir string
	seenBranches := map[string]struct{}{}
	branches := make([]string, 0, len(args))
	for _, repoDir := range args {
		repoDir, err := filepath.Abs(repoDir)
		if err != nil {
			return nil, err
		}
		repoDir = filepath.Clean(repoDir)
		if firstDir == "" {
			firstDir = repoDir
		}

		dir, branch, err := worktreeCommonDirAndBranch(repoDir)
		if err != nil {
			return nil, err
		}
		if commonDir == "" {
			commonDir = dir
		} else if commonDir != dir {
			return nil, fmt.Errorf("-worktrees arguments must share one git common dir: %q and %q differ", commonDir, dir)
		}
		if _, ok := seenBranches[branch]; ok {
			continue
		}
		seenBranches[branch] = struct{}{}
		branches = append(branches, branch)
	}

	nameSource := firstDir
	if commonDir != "" {
		nameSource = commonDir
	}
	return []gitRepoSpec{{
		dir:                 firstDir,
		name:                inferRepoName(nameSource, repoCacheDir),
		branches:            branches,
		resolveHEADToBranch: true,
	}}, nil
}

func worktreeCommonDirAndBranch(repoDir string) (commonDir, branch string, err error) {
	commonDir, err = gitOutput(repoDir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", "", err
	}
	commonDir = strings.TrimSpace(commonDir)
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(repoDir, commonDir)
	}
	commonDir, err = filepath.Abs(commonDir)
	if err != nil {
		return "", "", err
	}
	commonDir = filepath.Clean(commonDir)

	branch, err = gitOutput(repoDir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("worktree %q must have an attached HEAD: %w", repoDir, err)
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return "", "", fmt.Errorf("worktree %q must have an attached HEAD", repoDir)
	}
	return commonDir, branch, nil
}

func gitOutput(repoDir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git -C %q %s: %w: %s", repoDir, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func inferRepoName(repoDir, repoCacheDir string) string {
	name := strings.TrimSuffix(repoDir, "/.git")
	if repoCacheDir != "" && strings.HasPrefix(name, repoCacheDir) {
		name = strings.TrimPrefix(name, repoCacheDir+"/")
		name = strings.TrimSuffix(name, ".git")
	} else {
		name = strings.TrimSuffix(filepath.Base(name), ".git")
	}
	return name
}

func main() {
	exitStatus := run()
	os.Exit(exitStatus)
}
