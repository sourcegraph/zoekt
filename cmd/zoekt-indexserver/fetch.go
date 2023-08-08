package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xvandish/zoekt/gitindex"
	"golang.org/x/sync/errgroup"
)

// finds all git repos available, and calls git fetch on them
// it does so in parallel, with opts.parallelFetches as the bound
func periodicFetch(repoDir, indexDir string, opts *Options, pendingRepos chan<- string) {
	t := time.NewTicker(opts.fetchInterval)
	lastBruteReindex := time.Now()
	for {
		fmt.Printf("starting periodicFetch\n")
		lastBruteReindex = gitFetchNeededRepos(repoDir, indexDir, opts, pendingRepos, lastBruteReindex)
		<-t.C
	}
}

func callGetReposModifiedSinceForCfgs(cfgs []ConfigEntry, lookbackInterval time.Time, repoDir string) []string {
	var reposToFetchAndIndex []string
	for _, c := range cfgs {
		var cmd *exec.Cmd
		cmd = exec.Command("zoekt-github-get-repos-modified-since",
			"-dest", repoDir)
		cmd.Args = append(cmd.Args, createGithubArgsMirrorAndFetchArgs(c)...)
		cmd.Args = append(cmd.Args, "-since", lookbackInterval.Format(iso8601Format))

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

	return reposToFetchAndIndex
}

func processReposToFetchAndIndex(reposToFetchAndIndex []string, parallelFetches int, pendingRepos chan<- string) {
	g, _ := errgroup.WithContext(context.Background())
	g.SetLimit(parallelFetches)
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

// I think I need to re-think this logic
// When we run periodic, we should use now-fetchInterval as our time
// when we finish a run, what time should we write? The time we used to query (now-fetchInterval)
// The next run, should read the previous run time. If the previous time is < (now-fetchInterval-fetchInterval)
// then we use it

func writeFetchTimeToFile(repoDir string, t time.Time) {
	f := filepath.Join(repoDir, "time-of-last-update.txt")
	err := os.WriteFile(f, []byte(t.Format(iso8601Format)), 0644)
	if err != nil {
		fmt.Printf("error writing time to file: %v\n", err)
	}
}

func readFetchTimeFromFile(repoDir string) (time.Time, error) {
	f := filepath.Join(repoDir, "time-of-last-update.txt")
	bytes, err := os.ReadFile(f)
	if err != nil {
		fmt.Printf("error reading fetchTime from file: %v\n", err)
		return time.Time{}, err
	}
	lastLookbackIntervalStart := strings.TrimSpace(string(bytes))
	p, err := time.Parse(iso8601Format, lastLookbackIntervalStart)
	if err != nil {
		fmt.Printf("error reading fetchTime from file: %v\n", err)
		return time.Time{}, err
	}
	return p, nil
}

const accetableLookbackDiffThreshold = 5 * time.Second
const dayAgo = 24 * time.Hour

func getLookbackWindowStart(repoDir string, fetchInterval time.Duration) time.Time {
	now := time.Now()
	fmt.Printf("now=%s\n", now.String())
	fmt.Printf("fetchInterval=%s\n", fetchInterval.String())
	lookbackIntervalStart := now.Add(-fetchInterval)

	// if there is an error reading the previousLookbackInterval
	prevLookbackIntervalStart, err := readFetchTimeFromFile(repoDir)
	if err != nil { // no file exists, or format wrong
		fmt.Printf("using a 24 hour lookback window.\n")
		return lookbackIntervalStart.Add(time.Duration(-24) * time.Hour)
	}

	diff := lookbackIntervalStart.Sub(prevLookbackIntervalStart)

	// this should never happen. If it does, we have a problem, most likely in the
	// file writing phase
	if diff < 0 {
		fmt.Printf("Diff of prevLookback=%s and lookback=%s is < 0. Using a 24 hour window.\n", prevLookbackIntervalStart.String(), lookbackIntervalStart.String())
		return lookbackIntervalStart.Add(time.Duration(-24) * time.Hour)
	}

	// if the prevLookbackIntervalStart happened longer ago than we're comfortable with
	// we use it, in the case that repos haven't been updated since that time
	if diff > accetableLookbackDiffThreshold {
		fmt.Printf("Diff of prevLookback=%s and lookback=%s > %s. Using prevLookbackIntervalStart\n", prevLookbackIntervalStart.Format(iso8601Format), lookbackIntervalStart.Format(iso8601Format), accetableLookbackDiffThreshold)
		return prevLookbackIntervalStart
	}

	return lookbackIntervalStart
}

func periodicSmartGHFetchV2(repoDir, indexDir string, opts *Options, pendingRepos chan<- string) {
	t := time.NewTicker(opts.fetchInterval)
	lastBruteReindex := time.Now()

	for {
		lookbackIntervalStart := getLookbackWindowStart(repoDir, opts.fetchInterval)
		fmt.Printf("lookbackIntervalStart=%s\n", lookbackIntervalStart.String())

		if time.Since(lastBruteReindex) >= opts.bruteReindexInterval {
			fmt.Printf("bruteReindexing\n")
			lastBruteReindex = gitFetchNeededRepos(repoDir, indexDir, opts, pendingRepos, lastBruteReindex)
			continue
		}

		cfg, err := readConfigURL(opts.mirrorConfigFile)
		if err != nil {
			// we'd have a lot of problems anyways, so just error out
			fmt.Printf("ERROR: can't read configUrl: %v\n", err)
			continue
		}

		// for every config, call github-thing
		reposToFetchAndIndex := callGetReposModifiedSinceForCfgs(cfg, lookbackIntervalStart, repoDir)
		processReposToFetchAndIndex(reposToFetchAndIndex, opts.parallelFetches, pendingRepos)

		writeFetchTimeToFile(repoDir, lookbackIntervalStart)
		<-t.C
	}

}

// ahhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhh

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
		// var shouldSkip bool
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
			// shouldSkip = true
			break
		}

		if plannedNextRun.Before(since) {
			fmt.Printf("the planned next time is behind the current time. Using it.\n")
			since = plannedNextRun
		}
		for _, c := range cfg {
			cmd = exec.Command("zoekt-github-get-repos-modified-since",
				"-dest", repoDir)
			cmd.Args = append(cmd.Args, createGithubArgsMirrorAndFetchArgs(c)...)
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
