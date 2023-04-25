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

// This program manages a zoekt indexing deployment:
// * recycling logs
// * periodically fetching new data.
// * periodically reindexing all git repos.

package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/xvandish/zoekt"
	"github.com/xvandish/zoekt/gitindex"
	"golang.org/x/sync/errgroup"
)

const day = time.Hour * 24

func loggedRun(cmd *exec.Cmd) (out, err []byte) {
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf

	log.Printf("run %v", cmd.Args)
	if err := cmd.Run(); err != nil {
		log.Printf("command %s failed: %v\nOUT: %s\nERR: %s",
			cmd.Args, err, outBuf.String(), errBuf.String())
	}

	return outBuf.Bytes(), errBuf.Bytes()
}

type Options struct {
	cpuFraction         float64
	cpuCount            int
	fetchInterval       time.Duration
	mirrorInterval      time.Duration
	indexFlagsStr       string
	indexFlags          []string
	mirrorConfigFile    string
	maxLogAge           time.Duration
	indexTimeout        time.Duration
	parallelListApiReqs int
	parallelClones      int
	parallelFetches     int
	parallelIndexes     int
}

func (o *Options) validate() {
	if o.cpuFraction <= 0.0 || o.cpuFraction > 1.0 {
		log.Fatal("cpu_fraction must be between 0.0 and 1.0")
	}

	o.cpuCount = int(math.Trunc(float64(runtime.GOMAXPROCS(0)) * o.cpuFraction))
	if o.cpuCount < 1 {
		o.cpuCount = 1
	}
	if o.indexFlagsStr != "" {
		o.indexFlags = strings.Split(o.indexFlagsStr, " ")
	}
}

func (o *Options) defineFlags() {
	flag.DurationVar(&o.indexTimeout, "index_timeout", time.Hour, "kill index job after this much time")
	flag.DurationVar(&o.maxLogAge, "max_log_age", 3*day, "recycle index logs after this much time")
	flag.DurationVar(&o.fetchInterval, "fetch_interval", time.Hour, "run fetches this often")
	flag.StringVar(&o.mirrorConfigFile, "mirror_config",
		"", "JSON file holding mirror configuration.")
	flag.DurationVar(&o.mirrorInterval, "mirror_duration", 24*time.Hour, "find and clone new repos at this frequency.")
	flag.Float64Var(&o.cpuFraction, "cpu_fraction", 0.25,
		"use this fraction of the cores for indexing.")
	flag.StringVar(&o.indexFlagsStr, "git_index_flags", "", "space separated list of flags passed through to zoekt-git-index (e.g. -git_index_flags='-symbols=false -submodules=false'")
	flag.IntVar(&o.parallelListApiReqs, "parallel_list_api_reqs", 1, "number of concurrent list apis reqs to fetch org/user repos. Not all mirrors support this flag")
	flag.IntVar(&o.parallelClones, "parallel_clones", 1, "number of concurrent gitindex/clone operations. Not all mirrors support this flag")
	flag.IntVar(&o.parallelFetches, "parallel_fetches", 1, "number of concurrent git fetch ops")
	flag.IntVar(&o.parallelIndexes, "parallel_indexes", 1, "number of concurrent zoekt-git-index ops")
}

// periodicFetch runs git-fetch every once in a while. Results are
// posted on pendingRepos.
func periodicFetch(repoDir, indexDir string, opts *Options, pendingRepos chan<- string) {
	t := time.NewTicker(opts.fetchInterval)
	for {
		fmt.Printf("starting periodicFetch\n")
		repos, err := gitindex.FindGitRepos(repoDir)
		if err != nil {
			log.Println(err)
			continue
		}
		if len(repos) == 0 {
			log.Printf("no repos found under %s", repoDir)
		} else {
			fmt.Printf("found %d repos to fetch with %d workers\n", len(repos), opts.parallelFetches)
		}

		g, _ := errgroup.WithContext(context.Background())
		g.SetLimit(opts.parallelFetches)

		// TODO: Randomize to make sure quota throttling hits everyone.
		var mu sync.Mutex
		later := map[string]struct{}{}
		for _, dir := range repos {
			dir := dir
			g.Go(func() error {
				if hasUpdate := fetchGitRepo(dir); !hasUpdate {
					mu.Lock()
					later[dir] = struct{}{}
					mu.Unlock()
				} else {
					pendingRepos <- dir
				}
				return nil
			})
		}

		for r := range later {
			pendingRepos <- r
		}

		<-t.C
	}
}

// fetchGitRepo runs git-fetch, and returns true if there was an
// update.
func fetchGitRepo(dir string) bool {
	cmd := exec.Command("git", "--git-dir", dir, "fetch", "origin")
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}

	// Prevent prompting
	cmd.Stdin = &bytes.Buffer{}
	cmd.Stderr = errBuf
	cmd.Stdout = outBuf
	if err := cmd.Run(); err != nil {
		log.Printf("command %s failed: %v\nOUT: %s\nERR: %s",
			cmd.Args, err, outBuf.String(), errBuf.String())
	} else {
		return len(outBuf.Bytes()) != 0
	}
	return false
}

// indexPendingRepos consumes the directories on the repos channel and
// indexes them, sequentially.
func indexPendingRepos(indexDir, repoDir string, opts *Options, repos <-chan string) {
	// set up n listeners on the channel
	for i := 0; i < opts.parallelIndexes; i++ {
		go func(r <-chan string) {
			for dir := range r {
				// TODO: protect against duplicate operations. E.g, the index
				// being indexed by some other process already. Use
				// sourcegraph-indexserver/mutex?
				indexPendingRepo(dir, indexDir, repoDir, opts)

				// Failures (eg. timeout) will leave temp files
				// around. We have to clean them, or they will fill up the indexing volume.
				if failures, err := filepath.Glob(filepath.Join(indexDir, "*.tmp")); err != nil {
					log.Printf("Glob: %v", err)
				} else {
					for _, f := range failures {
						os.Remove(f)
					}
				}
			}
		}(repos)
	}
}

func indexPendingRepo(dir, indexDir, repoDir string, opts *Options) {
	ctx, cancel := context.WithTimeout(context.Background(), opts.indexTimeout)
	defer cancel()
	args := []string{
		"-require_ctags",
		fmt.Sprintf("-parallelism=%d", opts.cpuCount),
		"-repo_cache", repoDir,
		"-index", indexDir,
		"-incremental",
	}
	args = append(args, opts.indexFlags...)
	args = append(args, dir)
	cmd := exec.CommandContext(ctx, "zoekt-git-index", args...)
	loggedRun(cmd)
}

// deleteLogs deletes old logs.
func deleteLogs(logDir string, maxAge time.Duration) {
	fs, err := filepath.Glob(filepath.Join(logDir, "*"))
	if err != nil {
		log.Fatalf("filepath.Glob(%s): %v", logDir, err)
	}

	threshold := time.Now().Add(-maxAge)
	for _, fn := range fs {
		if fi, err := os.Lstat(fn); err == nil && fi.ModTime().Before(threshold) {
			os.Remove(fn)
		}
	}
}

func deleteLogsLoop(logDir string, maxAge time.Duration) {
	tick := time.NewTicker(maxAge / 100)
	for {
		deleteLogs(logDir, maxAge)
		<-tick.C
	}
}

// Delete the shard if its corresponding git repo can't be found.
func deleteIfOrphan(repoDir string, fn string) error {
	f, err := os.Open(fn)
	if err != nil {
		return nil
	}
	defer f.Close()

	ifile, err := zoekt.NewIndexFile(f)
	if err != nil {
		return nil
	}
	defer ifile.Close()

	repos, _, err := zoekt.ReadMetadata(ifile)
	if err != nil {
		return nil
	}

	// TODO support compound shards in zoekt-indexserver
	if len(repos) != 1 {
		return nil
	}
	repo := repos[0]

	_, err = os.Stat(repo.Source)
	if os.IsNotExist(err) {
		log.Printf("deleting orphan shard %s; source %q not found", fn, repo.Source)
		return os.Remove(fn)
	}

	return err
}

func deleteOrphanIndexes(indexDir, repoDir string, watchInterval time.Duration) {
	t := time.NewTicker(watchInterval)

	expr := indexDir + "/*"
	for {
		fs, err := filepath.Glob(expr)
		if err != nil {
			log.Printf("Glob(%q): %v", expr, err)
		}

		for _, f := range fs {
			if err := deleteIfOrphan(repoDir, f); err != nil {
				log.Printf("deleteIfOrphan(%q): %v", f, err)
			}
		}
		<-t.C
	}
}

func main() {
	var opts Options
	opts.defineFlags()
	dataDir := flag.String("data_dir",
		filepath.Join(os.Getenv("HOME"), "zoekt-serving"), "directory holding all data.")
	indexDir := flag.String("index_dir", "", "directory holding index shards. Defaults to $data_dir/index/")
	flag.Parse()
	opts.validate()

	if *dataDir == "" {
		log.Fatal("must set --data_dir")
	}

	// Automatically prepend our own path at the front, to minimize
	// required configuration.
	if l, err := os.Readlink("/proc/self/exe"); err == nil {
		os.Setenv("PATH", filepath.Dir(l)+":"+os.Getenv("PATH"))
	}

	logDir := filepath.Join(*dataDir, "logs")
	if *indexDir == "" {
		*indexDir = filepath.Join(*dataDir, "index")
	}
	repoDir := filepath.Join(*dataDir, "repos")
	for _, s := range []string{logDir, *indexDir, repoDir} {
		if _, err := os.Stat(s); err == nil {
			continue
		}

		if err := os.MkdirAll(s, 0o755); err != nil {
			log.Fatalf("MkdirAll %s: %v", s, err)
		}
	}

	_, err := readConfigURL(opts.mirrorConfigFile)
	if err != nil {
		log.Fatalf("readConfigURL(%s): %v", opts.mirrorConfigFile, err)
	}

	pendingRepos := make(chan string, 6000)
	go periodicMirrorFile(repoDir, &opts, pendingRepos)
	go deleteLogsLoop(logDir, opts.maxLogAge)
	go deleteOrphanIndexes(*indexDir, repoDir, opts.fetchInterval)
	go indexPendingRepos(*indexDir, repoDir, &opts, pendingRepos)
	periodicFetch(repoDir, *indexDir, &opts, pendingRepos)
}
