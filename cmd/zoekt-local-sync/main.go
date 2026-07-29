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

// Command zoekt-local-sync synchronizes a Zoekt index with Git repositories
// discovered under local directory roots.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/sourcegraph/zoekt/gitindex"
	"github.com/sourcegraph/zoekt/index"
)

const lockFileName = ".zoekt-local-sync.lock"

type directoryLock struct {
	file *os.File
}

type stringSliceFlag []string

type syncConfig struct {
	force        bool
	branches     string
	branchPrefix string
	allowMissing bool
	submodules   bool
	buildOptions index.Options
}

func (f *stringSliceFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringSliceFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func (l *directoryLock) Close() error {
	unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	return errors.Join(unlockErr, closeErr)
}

func acquireDirectoryLock(indexDir string) (*directoryLock, error) {
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return nil, fmt.Errorf("create index directory: %w", err)
	}

	path := filepath.Join(indexDir, lockFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}

	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("another zoekt-local-sync process is using %s", indexDir)
		}
		return nil, fmt.Errorf("lock index directory %s: %w", indexDir, err)
	}

	return &directoryLock{file: f}, nil
}

func normalizeIndexDir(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve index directory: %w", err)
	}
	return path, nil
}

func splitBranches(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func newSyncFlagSet(name string, output io.Writer) (*flag.FlagSet, *syncConfig) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(output)
	config := &syncConfig{}
	flags.BoolVar(&config.force, "f", false, "synchronize the index")
	flags.StringVar(&config.branches, "branches", "HEAD", "git branches to index")
	flags.StringVar(&config.branchPrefix, "prefix", "refs/heads/", "prefix for branch names")
	flags.BoolVar(&config.allowMissing, "allow_missing_branches", false, "allow missing branches")
	flags.BoolVar(&config.submodules, "submodules", true, "recurse into submodules")
	defaults := index.Options{IndexDir: index.DefaultDir}
	defaults.SetDefaults()
	flags.StringVar(&config.buildOptions.IndexDir, "index", defaults.IndexDir, "directory for search indices")
	flags.IntVar(&config.buildOptions.SizeMax, "file_limit", defaults.SizeMax, "maximum file size")
	flags.IntVar(&config.buildOptions.TrigramMax, "max_trigram_count", defaults.TrigramMax, "maximum number of trigrams per document")
	flags.IntVar(&config.buildOptions.ShardMax, "shard_limit", defaults.ShardMax, "maximum corpus size for a shard")
	flags.IntVar(&config.buildOptions.Parallelism, "parallelism", defaults.Parallelism, "maximum number of parallel indexing processes")
	flags.BoolVar(&config.buildOptions.CTagsMustSucceed, "require_ctags", false, "require ctags calls to succeed")
	flags.BoolVar(&config.buildOptions.DisableCTags, "disable_ctags", false, "disable ctags")
	flags.Var((*stringSliceFlag)(&config.buildOptions.LargeFiles), "large_file", "glob matching files to index regardless of size; may be repeated")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: %s [-f] [flags] <root> [<root>...]\n", name)
		flags.PrintDefaults()
	}
	return flags, config
}

func runSync(name string, args []string, out, errOut io.Writer) error {
	flags, config := newSyncFlagSet(name, errOut)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() == 0 {
		return errors.New("sync requires at least one root directory")
	}
	config.buildOptions.SetDefaults()

	indexDir, err := normalizeIndexDir(config.buildOptions.IndexDir)
	if err != nil {
		return err
	}
	config.buildOptions.IndexDir = indexDir

	if config.force {
		lock, err := acquireDirectoryLock(indexDir)
		if err != nil {
			return err
		}
		defer lock.Close() // best-effort unlock on process exit
	}

	repositories, err := discoverRepositories(flags.Args())
	if err != nil {
		return err
	}
	shards, err := readInventory(indexDir)
	if err != nil {
		return err
	}
	actions := planPrune(repositories, shards)
	if err := applyRemovals(actions, !config.force, out); err != nil {
		return err
	}

	if err := indexRepositories(repositories, gitindex.Options{
		BuildOptions:       config.buildOptions,
		Branches:           splitBranches(config.branches),
		BranchPrefix:       config.branchPrefix,
		AllowMissingBranch: config.allowMissing,
		Submodules:         config.submodules,
		Incremental:        true,
		DryRun:             !config.force,
	}, out); err != nil {
		return err
	}
	if !config.force {
		fmt.Fprintln(out, "Pass -f to apply these changes.")
	}
	return nil
}

func runList(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("zoekt-local-sync list", flag.ContinueOnError)
	flags.SetOutput(errOut)
	indexDir := flags.String("index", index.DefaultDir, "directory containing Zoekt index shards")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: zoekt-local-sync list [-index dir]\n")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("list does not accept root directories")
	}
	dir, err := normalizeIndexDir(*indexDir)
	if err != nil {
		return err
	}
	return listRepositories(dir, out)
}

func runRemove(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("zoekt-local-sync remove", flag.ContinueOnError)
	flags.SetOutput(errOut)
	indexDir := flags.String("index", index.DefaultDir, "directory containing Zoekt index shards")
	force := flags.Bool("f", false, "remove the selected repositories")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: zoekt-local-sync remove [-f] [-index dir] <name-or-source> [<name-or-source>...]\n")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() == 0 {
		return errors.New("remove requires at least one repository name or source path")
	}
	dir, err := normalizeIndexDir(*indexDir)
	if err != nil {
		return err
	}

	if *force {
		lock, err := acquireDirectoryLock(dir)
		if err != nil {
			return err
		}
		defer lock.Close() // best-effort unlock on process exit
	}
	if err := removeRepositories(dir, flags.Args(), !*force, out); err != nil {
		return err
	}
	if !*force {
		fmt.Fprintln(out, "Pass -f to apply these changes.")
	}
	return nil
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  zoekt-local-sync [-f] [flags] <root> [<root>...]")
	fmt.Fprintln(w, "  zoekt-local-sync sync [-f] [flags] <root> [<root>...]")
	fmt.Fprintln(w, "  zoekt-local-sync list [-index dir]")
	fmt.Fprintln(w, "  zoekt-local-sync remove [-f] [-index dir] <name-or-source> [<name-or-source>...]")
	fmt.Fprintln(w, "\nDefault sync options:")
	flags, _ := newSyncFlagSet("zoekt-local-sync", w)
	flags.PrintDefaults()
	fmt.Fprintln(w, "\nRun 'zoekt-local-sync list -h' or 'zoekt-local-sync remove -h' for command-specific help.")
}

func execute(args []string, out, errOut io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			printUsage(out)
			return nil
		case "sync":
			return runSync("zoekt-local-sync sync", args[1:], out, errOut)
		case "list":
			return runList(args[1:], out, errOut)
		case "remove":
			return runRemove(args[1:], out, errOut)
		}
	}
	return runSync("zoekt-local-sync", args, out, errOut)
}

func main() {
	if err := execute(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
