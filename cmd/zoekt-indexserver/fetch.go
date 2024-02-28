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
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

// finds all git repos available, and calls git fetch on them
// it does so in parallel, with opts.parallelFetches as the bound
func periodicFetch(ctx context.Context, repoDir, indexDir string, opts *Options, pendingRepos chan<- string) {
	commonAttrs := []attribute.KeyValue{
		attribute.String("repoDir", repoDir),
		attribute.String("indexDir", indexDir),
	}
	// create new span
	ctx, span := tracer.Start(
		ctx,
		"periodicFetch",
		trace.WithAttributes(commonAttrs...))

	// end span once done with func
	defer span.End()

	t := time.NewTicker(opts.fetchInterval)
	lastBruteReindex := time.Now()
	for {
		fmt.Printf("starting periodicFetch\n")
		lastBruteReindex = gitFetchNeededRepos(ctx, repoDir, indexDir, opts, pendingRepos, lastBruteReindex)
		<-t.C
	}
}

func callGetReposModifiedSinceForCfgs(ctx context.Context, cfgs []ConfigEntry, lookbackInterval time.Time, repoDir string) []string {
	commonAttrs := []attribute.KeyValue{
		attribute.String("repoDir", repoDir),
		attribute.String("lookbackInterval", lookbackInterval.String()),
	}
	// create new span
	ctx, span := tracer.Start(
		ctx,
		"callGetReposModifiedSinceForCfgs",
		trace.WithAttributes(commonAttrs...))

	// end span once done with func
	defer span.End()

	var reposToFetchAndIndex []string
	for _, c := range cfgs {
		var cmd *exec.Cmd
		cmd = exec.Command("zoekt-github-get-repos-modified-since",
			"-dest", repoDir)
		cmd.Args = append(cmd.Args, createGithubArgsMirrorAndFetchArgs(ctx, c)...)
		cmd.Args = append(cmd.Args, "-since", lookbackInterval.Format(iso8601Format))

		stdout, _ := loggedRun(ctx, cmd)
		reposPushed := 0
		for _, fn := range bytes.Split(stdout, []byte{'\n'}) {
			if len(fn) == 0 {
				continue
			}
			reposToFetchAndIndex = append(reposToFetchAndIndex, string(fn))
			reposPushed += 1
		}

		fmt.Printf("%v - there are %d repos to fetch and index\n", cmd.Args, reposPushed)
	}

	// add list of repos to span
	span.SetAttributes(attribute.StringSlice("reposToFetchAndIndex", reposToFetchAndIndex))

	return reposToFetchAndIndex
}

func processReposToFetchAndIndex(ctx context.Context, reposToFetchAndIndex []string, parallelFetches int, pendingRepos chan<- string) {
	commonAttrs := []attribute.KeyValue{
		attribute.StringSlice("reposToFetchAndIndex", reposToFetchAndIndex),
		attribute.Int("parallelFetches", parallelFetches),
	}
	// create new span
	ctx, span := tracer.Start(
		ctx,
		"processReposToFetchAndIndex",
		trace.WithAttributes(commonAttrs...))

	// end span once done with func
	defer span.End()

	g, _ := errgroup.WithContext(context.Background())
	g.SetLimit(parallelFetches)
	for _, dir := range reposToFetchAndIndex {
		dir := dir
		g.Go(func() error {
			ran := muIndexAndDataDirs.With(dir, func() {
				if hasUpdate := fetchGitRepo(ctx, dir); !hasUpdate {
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

func writeFetchTimeToFile(ctx context.Context, repoDir string, t time.Time) {
	commonAttrs := []attribute.KeyValue{
		attribute.String("repoDir", repoDir),
		attribute.String("time", t.String()),
	}
	// create new span
	_, span := tracer.Start(
		ctx,
		"writeFetchTimeToFile",
		trace.WithAttributes(commonAttrs...))

	// end span once done with func
	defer span.End()

	f := filepath.Join(repoDir, "time-of-last-update.txt")

	span.SetAttributes(attribute.String("file-path", f))
	err := os.WriteFile(f, []byte(t.Format(iso8601Format)), 0644)
	if err != nil {
		span.SetAttributes(attribute.Key("err").String(err.Error()))
		fmt.Printf("error writing time to file: %v\n", err)
	}
}

func readFetchTimeFromFile(ctx context.Context, repoDir string) (time.Time, error) {
	commonAttrs := []attribute.KeyValue{
		attribute.String("repoDir", repoDir),
	}
	// create new span
	_, span := tracer.Start(
		ctx,
		"readFetchTimeFromFile",
		trace.WithAttributes(commonAttrs...))

	// end span once done with func
	defer span.End()

	f := filepath.Join(repoDir, "time-of-last-update.txt")
	span.SetAttributes(attribute.String("file-path", f))
	bytes, err := os.ReadFile(f)
	if err != nil {
		span.SetAttributes(attribute.Key("err").String(err.Error()))
		fmt.Printf("error reading fetchTime from file: %v\n", err)
		return time.Time{}, err
	}
	lastLookbackIntervalStart := strings.TrimSpace(string(bytes))
	p, err := time.Parse(iso8601Format, lastLookbackIntervalStart)
	if err != nil {
		span.SetAttributes(attribute.Key("err").String(err.Error()))
		fmt.Printf("error reading fetchTime from file: %v\n", err)
		return time.Time{}, err
	}
	span.SetAttributes(attribute.Key("time").String(p.String()))
	return p, nil
}

const accetableLookbackDiffThreshold = 5 * time.Second
const dayAgo = 24 * time.Hour

// this function determines the "lookback" period -
// i.e. the time that github will use to find all repos that
// have been updated since. In the case that that time is > fetchInterval ago,
// we also return a newer timeToWrite that will be written to the file. This prevents an
// endless loop, which I will explain later...
func getLookbackWindowStart(ctx context.Context, repoDir string, fetchInterval time.Duration) (time.Time, time.Time) {
	commonAttrs := []attribute.KeyValue{
		attribute.String("repoDir", repoDir),
		attribute.String("fetchInterval", fetchInterval.String()),
	}
	// create new span
	_, span := tracer.Start(
		ctx,
		"getLookbackWindowStart",
		trace.WithAttributes(commonAttrs...))

	// end span once done with func
	defer span.End()

	now := time.Now()
	lookbackIntervalStart := now.Add(-fetchInterval)
	span.SetAttributes(attribute.Key("lookbackIntervalStart").String(lookbackIntervalStart.String()))

	// if there is an error reading the previousLookbackInterval
	prevLookbackIntervalStart, err := readFetchTimeFromFile(ctx, repoDir)
	if err != nil { // no file exists, or format wrong
		span.SetAttributes(attribute.Key("err").String(err.Error()))
		fmt.Printf("using a 24 hour lookback window.\n")
		return now, lookbackIntervalStart.Add(time.Duration(-24) * time.Hour)
	}

	diff := lookbackIntervalStart.Sub(prevLookbackIntervalStart)
	span.SetAttributes(attribute.Key("diff").String(diff.String()))

	// this should never happen. If it does, we have a problem, most likely in the
	// file writing phase
	if diff < 0 {
		fmt.Printf("Diff of prevLookback=%s and lookback=%s is < 0. Using current time.\n", prevLookbackIntervalStart.String(), lookbackIntervalStart.String())
		return now, lookbackIntervalStart
	}

	// if the prevLookbackIntervalStart happened longer ago than we're comfortable with
	// we use it, in the case that repos haven't been updated since that time
	if diff > accetableLookbackDiffThreshold {
		fmt.Printf("Diff of prevLookback=%s and lookback=%s > %s. Using prevLookbackIntervalStart\n", prevLookbackIntervalStart.Format(iso8601Format), lookbackIntervalStart.Format(iso8601Format), accetableLookbackDiffThreshold)
		return now, prevLookbackIntervalStart
	}

	return now, lookbackIntervalStart
}

func isDuringWorkHours(ctx context.Context, timeToCheck time.Time, startHour, endHour int, zone *time.Location) bool {
	commonAttrs := []attribute.KeyValue{
		attribute.String("timeToCheck", timeToCheck.String()),
		attribute.Int("startHour", startHour),
		attribute.Int("endHour", endHour),
		attribute.String("zone", zone.String()),
	}
	// create new span
	_, span := tracer.Start(
		ctx,
		"isDuringWorkHours",
		trace.WithAttributes(commonAttrs...))

	// end span once done with func
	defer span.End()
	currHour := timeToCheck.In(zone).Hour()
	span.SetAttributes(attribute.Key("currHour").Int(currHour))
	return currHour >= startHour && currHour <= endHour
}
func workingHoursEnabled(ctx context.Context, opts *Options) bool {
	commonAttrs := []attribute.KeyValue{
		attribute.Int("workingHoursStart", opts.workingHoursStart),
	}
	// create new span
	_, span := tracer.Start(
		ctx,
		"workingHoursEnabled",
		trace.WithAttributes(commonAttrs...))

	// end span once done with func
	defer span.End()

	return opts.workingHoursStart >= 0
}

func periodicSmartGHFetchV2(ctx context.Context, repoDir, indexDir string, opts *Options, pendingRepos chan<- string) {
	commonAttrs := []attribute.KeyValue{
		attribute.String("repoDir", repoDir),
	}
	// create new span
	_, span := tracer.Start(
		ctx,
		"periodicSmartGHFetchV2",
		trace.WithAttributes(commonAttrs...))

	// end span once done with func
	defer span.End()

	currInterval := opts.fetchInterval
	if workingHoursEnabled(ctx, opts) && !isDuringWorkHours(ctx, time.Now(), opts.workingHoursStart, opts.workingHoursEnd, opts.workingHoursZone) {
		currInterval = opts.fetchIntervalSlow
		fmt.Printf("not during working hours. Starting interval is %s\n", opts.fetchIntervalSlow)
	}

	t := time.NewTicker(currInterval)
	lastBruteReindex := time.Now()

	for {
		timeToWrite, lookbackIntervalStart := getLookbackWindowStart(ctx, repoDir, currInterval)
		fmt.Printf("lookbackIntervalStart=%s\n", lookbackIntervalStart.String())

		if time.Since(lastBruteReindex) >= opts.bruteReindexInterval {
			fmt.Printf("bruteReindexing\n")
			lastBruteReindex = gitFetchNeededRepos(ctx, repoDir, indexDir, opts, pendingRepos, lastBruteReindex)
			continue
		}

		cfg, err := readConfigURL(ctx, opts.mirrorConfigFile)
		if err != nil {
			// we'd have a lot of problems anyways, so just error out
			span.SetAttributes(attribute.Key("err").String(err.Error()))
			fmt.Printf("ERROR: can't read configUrl: %v\n", err)
			continue
		}

		// for every config, call github-thing
		reposToFetchAndIndex := callGetReposModifiedSinceForCfgs(ctx, cfg, lookbackIntervalStart, repoDir)
		processReposToFetchAndIndex(ctx, reposToFetchAndIndex, opts.parallelFetches, pendingRepos)

		writeFetchTimeToFile(ctx, repoDir, timeToWrite)

		// this code has a bit of an issue. If fetchIntervalSlow is much slower, than it's possible
		// that the entire fetchIntervalSlow elapses before we switch back to the faster fetchInterval.
		// As I'm planning on using only a 10min slow interval, this is a problem for later.
		if workingHoursEnabled(ctx, opts) {
			if isDuringWorkHours(ctx, time.Now(), opts.workingHoursStart, opts.workingHoursEnd, opts.workingHoursZone) {
				t.Reset(opts.fetchInterval)
				currInterval = opts.fetchInterval
			} else {
				fmt.Printf("not during working hours. Setting interval to=%s\n", opts.fetchIntervalSlow)
				t.Reset(opts.fetchIntervalSlow)
				currInterval = opts.fetchIntervalSlow
			}
		}

		<-t.C
	}

}

func gitFetchNeededRepos(ctx context.Context, repoDir, indexDir string, opts *Options, pendingRepos chan<- string, lastBruteReindex time.Time) time.Time {
	commonAttrs := []attribute.KeyValue{
		attribute.String("repoDir", repoDir),
		attribute.String("indexDir", indexDir),
	}
	// create new span
	_, span := tracer.Start(
		ctx,
		"gitFetchNeededRepos",
		trace.WithAttributes(commonAttrs...))

	// end span once done with func
	defer span.End()

	fmt.Printf("running gitFetchNeededRepos\n")
	repos, err := gitindex.FindGitRepos(repoDir)
	if err != nil {
		span.SetAttributes(attribute.Key("err").String(err.Error()))
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
				if hasUpdate := fetchGitRepo(ctx, dir); !hasUpdate {
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
func fetchGitRepo(ctx context.Context, dir string) bool {
	commonAttrs := []attribute.KeyValue{
		attribute.String("dir", dir),
	}
	// create new span
	_, span := tracer.Start(
		ctx,
		"fetchGitRepo",
		trace.WithAttributes(commonAttrs...))

	// end span once done with func
	defer span.End()

	cmd := exec.Command("git", "--git-dir", dir, "fetch", "origin")
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}

	// Prevent prompting
	cmd.Stdin = &bytes.Buffer{}
	cmd.Stderr = errBuf
	cmd.Stdout = outBuf
	if err := cmd.Run(); err != nil {
		span.SetAttributes(attribute.Key("err").String(err.Error()))
		log.Printf("command %s failed: %v\nOUT: %s\nERR: %s",
			cmd.Args, err, outBuf.String(), errBuf.String())
	} else {
		return len(errBuf.Bytes()) != 0
	}
	return false
}
