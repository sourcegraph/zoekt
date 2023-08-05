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
const iso8601Format = "2006-01-02T15:04:05Z07:00"

var (
	// we use this for 3 things:
	// 1. prevent the same git repo from being indexed concurrently
	// 2. prevent a repo from being indexed and fetched concurrently
	// 3. stop all indexing/fetching while the periodic backup happens
	muIndexAndDataDirs indexMutex
)

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
	cpuFraction          float64
	cpuCount             int
	fetchInterval        time.Duration
	mirrorInterval       time.Duration
	bruteReindexInterval time.Duration
	backupInterval       time.Duration
	indexFlagsStr        string
	indexFlags           []string
	mirrorConfigFile     string
	maxLogAge            time.Duration
	indexTimeout         time.Duration
	parallelListApiReqs  int
	parallelClones       int
	parallelFetches      int
	parallelIndexes      int
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
	flag.DurationVar(&o.bruteReindexInterval, "brute_reindex_interval", 24*time.Hour, "re-index all repos even if they had no update. Still runs with -incremental by default.")
	flag.DurationVar(&o.backupInterval, "backup_interval", 24*time.Hour, "backup indices and git repos at this interval. Uses gsutil and backs up to codesearch_backup bucket")
	flag.Float64Var(&o.cpuFraction, "cpu_fraction", 0.25,
		"use this fraction of the cores for indexing.")
	flag.StringVar(&o.indexFlagsStr, "git_index_flags", "", "space separated list of flags passed through to zoekt-git-index (e.g. -git_index_flags='-symbols=false -submodules=false'")
	flag.IntVar(&o.parallelListApiReqs, "parallel_list_api_reqs", 1, "number of concurrent list apis reqs to fetch org/user repos. Not all mirrors support this flag")
	flag.IntVar(&o.parallelClones, "parallel_clones", 1, "number of concurrent gitindex/clone operations. Not all mirrors support this flag")
	flag.IntVar(&o.parallelFetches, "parallel_fetches", 1, "number of concurrent git fetch ops")
	flag.IntVar(&o.parallelIndexes, "parallel_indexes", 1, "number of concurrent zoekt-git-index ops")
}

func periodicBackup(dataDir, indexDir string, opts *Options) {
	t := time.NewTicker(opts.backupInterval)
	for {
		// lock the index and git directories from being written to
		muIndexAndDataDirs.GlobalWaitForPending(func() {
			fmt.Printf("starting backup...\n")
			idxSyncCmd := exec.Command("rsync", "-ruv", indexDir+"/", "zoekt-backup/indices/")
			err := idxSyncCmd.Run()
			if err != nil {
				fmt.Printf("ERROR: error backup up index shards %v\n", err)
			}

			gitSyncCmd := exec.Command("rsync", "-ruv", dataDir+"/", "zoekt-backup/repos/")
			err = gitSyncCmd.Run()
			if err != nil {
				fmt.Printf("ERROR: error backing up git repos %v\n", err)
			}
			fmt.Printf("finished backup\n")
		})
		<-t.C
	}
}

func timeTaken(msg string, start time.Time) {
	fmt.Printf("%v: %v\n", msg, time.Since(start))
}

// GitHub ONLY
// rather than running git-fetch in a brute manner, we ask GitHub which repos have
// been updated since the last time peridicFetch was run
// periodicFetch runs git-fetch every once in a while. Results are
// posted on pendingRepos.
func periodicSmartGHFetch(repoDir, indexDir string, opts *Options, pendingRepos chan<- string) {
	t := time.NewTicker(opts.fetchInterval)
	lastBruteReindex := time.Now()

	for {
		fmt.Printf("starting periodicSmartGHFetch\n")
		start := time.Now()

		// if it's time to brute re-index, use the standard gitFetch method
		if time.Since(lastBruteReindex) >= opts.bruteReindexInterval {
			fmt.Printf("bruteReindexing\n")
			lastBruteReindex = gitFetchNeededRepos(repoDir, indexDir, opts, pendingRepos, lastBruteReindex)
			continue
		}

		// otherwise, we ask GH which repos to update
		fmt.Printf("using smart method\n")
		var cmd *exec.Cmd

		cfg, err := readConfigURL(opts.mirrorConfigFile)

		if err != nil {
			fmt.Printf("err=%v\n", err)
			continue
		}

		var reposToFetchAndIndex []string
		var shouldSkip bool
		// now... what the hell is the appropriate logic for since?
		// the easy one is at the time of this run. In the case of the program
		// stopping and not running for a while though, we need to persist the run time
		since := time.Now()
		now := time.Now()

		// read the time we were supposed to run on
		// if we're running later than that time, use it as since
		// otherwise, just use the current time
		db, _ := os.ReadFile(filepath.Join(repoDir, "time-of-last-update.txt"))
		s := string(db)
		s = strings.TrimSpace(s)
		plannedNextRun, err := time.Parse(iso8601Format, s)

		if err != nil {
			fmt.Printf("error reading timeFile or parsing date in it %v. Falling back to brute index\n", err)
			// write the current time to a file
			file := filepath.Join(repoDir, "time-of-last-update.txt")
			err = os.WriteFile(file, []byte(since.Format(iso8601Format)), 0644)
			if err != nil {
				fmt.Printf("error writing time to file: %v\n", err)
			}

			lastBruteReindex = gitFetchNeededRepos(repoDir, indexDir, opts, pendingRepos, lastBruteReindex)
			shouldSkip = true
			break
		}

		if plannedNextRun.Before(since) {
			fmt.Printf("the planned next time is behind the current time. Using it.\n")
			since = plannedNextRun
		}
		for _, c := range cfg {
			// make sure this is a github cfg
			if c.GitHubURL == "" && c.GithubUser == "" && c.GithubOrg == "" {
				fmt.Printf("periodicSmartGHFetch can only be used for GitHub only indexservers. Falling back to normal periodicFetch\n")
				lastBruteReindex = gitFetchNeededRepos(repoDir, indexDir, opts, pendingRepos, lastBruteReindex)
				shouldSkip = true

				// how do I jump to the top of this for loop?
				break
			}

			cmd = exec.Command("zoekt-github-get-repos-modified-since",
				"-dest", repoDir)
			if c.GitHubURL != "" {
				cmd.Args = append(cmd.Args, "-url", c.GitHubURL)
			}
			if c.GithubUser != "" {
				cmd.Args = append(cmd.Args, "-user", c.GithubUser)
			} else if c.GithubOrg != "" {
				cmd.Args = append(cmd.Args, "-org", c.GithubOrg)
			}
			if c.Name != "" {
				cmd.Args = append(cmd.Args, "-name", c.Name)
			}
			if c.Exclude != "" {
				cmd.Args = append(cmd.Args, "-exclude", c.Exclude)
			}
			if c.CredentialPath != "" {
				cmd.Args = append(cmd.Args, "-token", c.CredentialPath)
			}
			for _, topic := range c.Topics {
				cmd.Args = append(cmd.Args, "-topic", topic)
			}
			for _, topic := range c.ExcludeTopics {
				cmd.Args = append(cmd.Args, "-exclude_topic", topic)
			}
			if c.NoArchived {
				cmd.Args = append(cmd.Args, "-no_archived")
			}

			cmd.Args = append(cmd.Args, "-since", since.Format(iso8601Format))

			// cmd.Args = append(cmd.Args, "--max-concurrent-gh-requests", strconv.Itoa(opts.parallelListApiReqs))

			stdout, stderr := loggedRun(cmd)

			fmt.Printf("cmd %v - logs=%s\n", cmd.Args, string(stderr))
			reposPushed := 0
			for _, fn := range bytes.Split(stdout, []byte{'\n'}) {
				if len(fn) == 0 {
					continue
				}
				reposToFetchAndIndex = append(reposToFetchAndIndex, string(fn))
				// pendingRepos <- string(fn)
				reposPushed += 1
			}

			fmt.Printf("%v - there are %d repos to fetch and index\n", cmd.Args, reposPushed)
		}

		fmt.Printf("there are %d total repos to fetch and index\n", len(reposToFetchAndIndex))

		if !shouldSkip {
			g, _ := errgroup.WithContext(context.Background())
			g.SetLimit(opts.parallelFetches)
			for _, dir := range reposToFetchAndIndex {
				dir := dir
				g.Go(func() error {
					ran := muIndexAndDataDirs.With(dir, func() {
						if hasUpdate := fetchGitRepo(dir); !hasUpdate {
							fmt.Printf("ERROR: we mistakenly thought %s had an update. Check smartGH logic\n", dir)
						} else {
							fmt.Printf("dir=%s has update\n", dir)
							pendingRepos <- dir
						}
					})

					if !ran {
						fmt.Printf("either an index or fetch job for repo=%s already running\n", dir)
					}
					return nil
				})
			}
			g.Wait()

		}

		fmt.Printf("finished periodicSmartGHFetch. took %s\n", time.Since(start))

		// write the time that the next run should happen on
		// we fetched all repos with updates between since--->now
		// so now "now+fetchInterval" is our theoretical next run
		nextRunTime := now.Add(opts.fetchInterval)
		file := filepath.Join(repoDir, "time-of-last-update.txt")
		err = os.WriteFile(file, []byte(nextRunTime.Format(iso8601Format)), 0644)
		if err != nil {
			fmt.Printf("error writing time to file: %v\n", err)
		}

		<-t.C
	}
}

func gitFetchNeededRepos(repoDir, indexDir string, opts *Options, pendingRepos chan<- string, lastBruteReindex time.Time) time.Time {
	fmt.Printf("running gitFetchNeededRepos\n")
	repos, err := gitindex.FindGitRepos(repoDir)
	if err != nil {
		log.Println(err)
		return lastBruteReindex
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
	count := 0
	for _, dir := range repos {
		dir := dir
		g.Go(func() error {
			ran := muIndexAndDataDirs.With(dir, func() {
				if hasUpdate := fetchGitRepo(dir); !hasUpdate {
					mu.Lock()
					later[dir] = struct{}{}
					mu.Unlock()
				} else {
					fmt.Printf("dir=%s has update\n", dir)
					pendingRepos <- dir
					count += 1
				}
			})
			if !ran {
				fmt.Printf("either an index or fetch job for repo=%s already running\n", dir)
			}
			return nil
		})
	}
	g.Wait()
	fmt.Printf("%d repos had git updates\n", count)

	if time.Since(lastBruteReindex) >= opts.bruteReindexInterval {
		fmt.Printf("re-indexing the %d repos that had no update\n", len(later))
		for r := range later {
			pendingRepos <- r
		}
		lastBruteReindex = time.Now()
	} else {
		fmt.Printf("not re-indexing the %d repos that had no update\n", len(later))
	}

	return lastBruteReindex
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
		return len(errBuf.Bytes()) != 0
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
				ran := muIndexAndDataDirs.With(dir, func() {
					indexPendingRepo(dir, indexDir, repoDir, opts)
				})
				if !ran {
					fmt.Printf("index job for repository: %s already running\n", dir)
				}

				// TODO: handle failures better. For now, as this is causing
				// problems with parallel indexing, so we don't make an effor to
				// clean up. We can have zoekt-git-index be the one to clean up,
				// or we can propgate exit status and still handle it here

				// Failures (eg. timeout) will leave temp files
				// around. We have to clean them, or they will fill up the indexing volume.
				// Okay, I think what's going on here is indexPendingRepos isn't cleanly working - when
				// one index finishes (but another is still running and has temp files), the finished
				// index triggers the filepath.Glob(), and then removes the indexes from the indexer that
				// hasn't finished!
				// if failures, err := filepath.Glob(filepath.Join(indexDir, "*.tmp")); err != nil {
				// 	log.Printf("Glob: %v", err)
				// } else {
				// 	for _, f := range failures {
				// 		os.Remove(f)
				// 	}
				// }
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
	go periodicBackup(repoDir, *indexDir, &opts)
	go indexPendingRepos(*indexDir, repoDir, &opts, pendingRepos)

	// if github only configs, run periodic smart GH fetch
	periodicSmartGHFetch(repoDir, *indexDir, &opts, pendingRepos)
}
