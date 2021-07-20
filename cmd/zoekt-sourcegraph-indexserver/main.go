// Command zoekt-sourcegraph-indexserver periodically reindexes enabled
// repositories on sourcegraph
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/profiler"
	"github.com/google/zoekt"
	"github.com/google/zoekt/debugserver"
	"go.uber.org/automaxprocs/maxprocs"
	"golang.org/x/net/trace"

	"github.com/google/zoekt/build"
	retryablehttp "github.com/hashicorp/go-retryablehttp"
	"github.com/keegancsmith/tmpfriend"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricResolveRevisionsDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "resolve_revisions_seconds",
		Help:    "A histogram of latencies for resolving all repository revisions.",
		Buckets: prometheus.ExponentialBuckets(1, 10, 6), // 1s -> 27min
	})

	metricResolveRevisionDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "resolve_revision_seconds",
		Help:    "A histogram of latencies for resolving a repository revision.",
		Buckets: prometheus.ExponentialBuckets(.25, 2, 4), // 250ms -> 2s
	}, []string{"success"}) // success=true|false

	metricGetIndexOptionsError = promauto.NewCounter(prometheus.CounterOpts{
		Name: "get_index_options_error_total",
		Help: "The total number of times we failed to get index options for a repository.",
	})

	metricIndexDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "index_repo_seconds",
		Help:    "A histogram of latencies for indexing a repository.",
		Buckets: prometheus.ExponentialBuckets(.1, 10, 7), // 100ms -> 27min
	}, []string{"state"}) // state is an indexState

	metricIndexIncrementalIndexState = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "index_incremental_index_state",
		Help: "A count of the state on disk vs what we want to build. See zoekt/build.IndexState.",
	}, []string{"state"}) // state is build.IndexState

	metricNumIndexed = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "index_num_indexed",
		Help: "Number of indexed repos by code host",
	}, []string{"codehost"})

	metricNumAssigned = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "index_num_assigned",
		Help: "Number of repos assigned to this indexer by code host",
	}, []string{"codehost"})

	metricNumPriorities = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "index_priorities_total",
		Help: "Number of indexed repos with non-zero priorities",
	})

	metricNumPriorityUpdates = promauto.NewCounter(prometheus.CounterOpts{
		Name: "index_priorities_update_total",
		Help: "Total number of times repo ranking has been written to priority.json",
	})

	metricFailingTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "index_failing_total",
		Help: "Counts failures to index (indexing activity, should be used with rate())",
	})

	metricIndexingTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "index_indexing_total",
		Help: "Counts indexings (indexing activity, should be used with rate())",
	})

	metricsEnqueueRepoForIndex = promauto.NewCounter(prometheus.CounterOpts{
		Name: "enqueue_repo_for_index_total",
		Help: "Counts the number of time /enqueueforindex is called",
	})
)

type indexState string

const (
	indexStateFail        indexState = "fail"
	indexStateSuccess                = "success"
	indexStateSuccessMeta            = "success_meta" // We only updated metadata
	indexStateNoop                   = "noop"         // We didn't need to update index
	indexStateEmpty                  = "empty"        // index is empty (empty repo)
)

// Server is the main functionality of zoekt-sourcegraph-indexserver. It
// exists to conveniently use all the options passed in via func main.
type Server struct {
	// Root is the base URL for the Sourcegraph instance to index. Normally
	// http://sourcegraph-frontend-internal or http://localhost:3090.
	Root *url.URL

	// IndexDir is the index directory to use.
	IndexDir string

	// Interval is how often we sync with Sourcegraph.
	Interval time.Duration

	// Hostname is the name we advertise to Sourcegraph when asking for the
	// list of repositories to index.
	Hostname string

	// CPUCount is the amount of parallelism to use when indexing a
	// repository.
	CPUCount int

	// Indexer is the indexer to use. Either archiveIndex (default) or the
	// experimental gitIndex.
	Indexer func(*indexArgs, func(*exec.Cmd) error) error

	mu            sync.Mutex
	lastListRepos []string
}

var client = retryablehttp.NewClient()
var debug = log.New(ioutil.Discard, "", log.LstdFlags)

func init() {
	client.Logger = debug
}

// our index commands should output something every 100mb they process.
//
// 2020-11-24 Keegan. "This should be rather quick so 5m is more than enough
// time."  famous last words. A client was indexing a monorepo with 42
// cores... 5m was not enough.
const noOutputTimeout = 30 * time.Minute

func (s *Server) loggedRun(tr trace.Trace, cmd *exec.Cmd) (err error) {
	out := &synchronizedBuffer{}
	cmd.Stdout = out
	cmd.Stderr = out

	tr.LazyPrintf("%s", cmd.Args)

	defer func() {
		if err != nil {
			outS := out.String()
			tr.LazyPrintf("failed: %v", err)
			tr.LazyPrintf("output: %s", out)
			tr.SetError()
			err = fmt.Errorf("command %s failed: %v\nOUT: %s", cmd.Args, err, outS)
		}
	}()

	if err := cmd.Start(); err != nil {
		return err
	}

	errC := make(chan error)
	go func() {
		errC <- cmd.Wait()
	}()

	lastLen := 0
	for {
		select {
		case <-time.After(noOutputTimeout):
			// Periodically check if we have had output. If not kill the process.
			if out.Len() != lastLen {
				lastLen = out.Len()
				log.Printf("still running %s", cmd.Args)
			} else {
				log.Printf("no output for %s, killing %s", noOutputTimeout, cmd.Args)
				if err := cmd.Process.Kill(); err != nil {
					log.Println("kill failed:", err)
				}
			}

		case err := <-errC:
			if err != nil {
				return err
			}

			tr.LazyPrintf("success")
			debug.Printf("ran successfully %s", cmd.Args)
			return nil
		}
	}
}

// synchronizedBuffer wraps a strings.Builder with a mutex. Used so we can
// monitor the buffer while it is being written to.
type synchronizedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (sb *synchronizedBuffer) Write(p []byte) (int, error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.b.Write(p)
}

func (sb *synchronizedBuffer) Len() int {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.b.Len()
}

func (sb *synchronizedBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.b.String()
}

func codeHostFromName(repoName string) string {
	if i := strings.Index(repoName, "/"); i >= 0 {
		repoName = repoName[:i]
	}

	// basic check that codehost is a domain. We want to avoid returning high
	// cardinality fields (for example if Sourcegraph is configured to not
	// include the hostname in repoName).
	if !strings.Contains(repoName, ".") {
		return "unknown"
	}

	return repoName
}

// Run the sync loop. This blocks forever.
func (s *Server) Run(queue *Queue) {
	removeIncompleteShards(s.IndexDir)
	waitForFrontend(s.Root)

	// Start a goroutine which updates the queue with commits to index.
	go func() {
		for range jitterTicker(s.Interval) {
			repos, err := listRepos(context.Background(), s.Hostname, s.Root, listIndexed(s.IndexDir))
			if err != nil {
				log.Println(err)
				continue
			}

			s.mu.Lock()
			s.lastListRepos = repos
			s.mu.Unlock()

			debug.Printf("updating index queue with %d repositories", len(repos))

			// Stop indexing repos we don't need to track anymore
			count := queue.MaybeRemoveMissing(repos)
			if count > 0 {
				log.Printf("stopped tracking %d repositories", count)
			}

			cleanupDone := make(chan struct{})
			go func() {
				defer close(cleanupDone)
				cleanup(s.IndexDir, repos, time.Now())
			}()

			start := time.Now()
			tr := trace.New("getIndexOptions", "")
			tr.LazyPrintf("getting index options for %d repos", len(repos))

			newPriorities := make(map[string]float64)

			// We ask the frontend to get index options in batches.
			for repos := range batched(repos, 1000) {
				start := time.Now()
				opts, err := getIndexOptions(s.Root, repos...)
				if err != nil {
					metricResolveRevisionDuration.WithLabelValues("false").Observe(time.Since(start).Seconds())
					tr.LazyPrintf("failed fetching options batch: %v", err)
					tr.SetError()
					continue
				}
				metricResolveRevisionDuration.WithLabelValues("true").Observe(time.Since(start).Seconds())
				for i, opt := range opts {
					name := repos[i]
					if opt.Error != "" {
						metricGetIndexOptionsError.Inc()
						tr.LazyPrintf("failed fetching options for %v: %v", name, opt.Error)
						tr.SetError()
						continue
					}
					if opt.Priority != 0 {
						newPriorities[name] = opt.Priority
					}
					queue.AddOrUpdate(name, opt.IndexOptions)
				}
			}
			s.maybeUpdatePriorities(repos, newPriorities)

			metricResolveRevisionsDuration.Observe(time.Since(start).Seconds())
			tr.Finish()

			<-cleanupDone
		}
	}()

	// In the current goroutine process the queue forever.
	for {
		name, opts, ok := queue.Pop()
		if !ok {
			time.Sleep(time.Second)
			continue
		}
		start := time.Now()
		args := s.defaultArgs()
		args.Name = name
		args.IndexOptions = opts
		state, err := s.Index(args)
		metricIndexDuration.WithLabelValues(string(state)).Observe(time.Since(start).Seconds())
		if err != nil {
			log.Printf("error indexing %s: %s", args.String(), err)
		}
		switch state {
		case indexStateSuccess:
			log.Printf("updated index %s in %v", args.String(), time.Since(start))
		case indexStateSuccessMeta:
			log.Printf("updated meta %s in %v", args.String(), time.Since(start))
		}
		queue.SetIndexed(name, opts, state)
	}
}

// Update priority.json given new entries, and remove no longer tracked repos.
// This doesn't simply write newPriorities because a transient getIndexOptions failure
// would cause the associated repo to get deprioritized.
func (s *Server) maybeUpdatePriorities(names []string, newPriorities map[string]float64) {
	priorityPath := path.Join(s.IndexDir, "priority.json")
	priorities := map[string]float64{}
	buf, err := ioutil.ReadFile(priorityPath)
	if err == nil {
		err = json.Unmarshal(buf, &priorities)
		if err != nil {
			log.Printf("warning: error loading old priority.json, treating as empty: %v", err)
		}
	}

	// maybe remove no-longer-tracked repos from the priorities list
	if len(names) != len(priorities) {
		set := make(map[string]struct{}, len(names))
		for _, name := range names {
			set[name] = struct{}{}
		}
		for name := range priorities {
			if _, ok := set[name]; !ok {
				delete(priorities, name)
			}
		}
	}

	for name, priority := range newPriorities {
		priorities[name] = priority
	}

	metricNumPriorities.Set(float64(len(priorities)))

	newBuf, err := json.Marshal(priorities)
	if err != nil {
		log.Printf("error marshaling new priority.json: %v", err)
	}
	newBuf = append(newBuf, '\n') // prettier

	if bytes.Equal(buf, newBuf) {
		return // no need to rewrite priority.json
	}

	err = ioutil.WriteFile(priorityPath+".tmp", newBuf, 0644)
	if err != nil {
		log.Printf("error writing new priority.json: %v", err)
		return
	}

	err = os.Rename(priorityPath+".tmp", priorityPath)
	if err != nil {
		log.Printf("error renaming new priority.json into place: %v", err)
	}

	metricNumPriorityUpdates.Inc()
}

func batched(slice []string, size int) <-chan []string {
	c := make(chan []string)
	go func() {
		for len(slice) > 0 {
			if size > len(slice) {
				size = len(slice)
			}
			c <- slice[:size]
			slice = slice[size:]
		}
		close(c)
	}()
	return c
}

// jitterTicker returns a ticker which ticks with a jitter. Each tick is
// uniformly selected from the range (d/2, d + d/2). It will tick on creation.
func jitterTicker(d time.Duration) <-chan struct{} {
	ticker := make(chan struct{})

	go func() {
		for {
			ticker <- struct{}{}
			ns := int64(d)
			jitter := rand.Int63n(ns)
			time.Sleep(time.Duration(ns/2 + jitter))
		}
	}()

	return ticker
}

// Index starts an index job for repo name at commit.
func (s *Server) Index(args *indexArgs) (state indexState, err error) {
	tr := trace.New("index", args.Name)

	defer func() {
		if err != nil {
			tr.SetError()
			tr.LazyPrintf("error: %v", err)
			state = indexStateFail
			metricFailingTotal.Inc()
		}
		tr.LazyPrintf("state: %s", state)
		tr.Finish()
	}()

	tr.LazyPrintf("branches: %v", args.Branches)

	if len(args.Branches) == 0 {
		return indexStateEmpty, s.createEmptyShard(tr, args.Name)
	}

	if args.Incremental {
		bo := args.BuildOptions()
		bo.SetDefaults()
		incrementalState := bo.IndexState()
		metricIndexIncrementalIndexState.WithLabelValues(string(incrementalState)).Inc()
		switch incrementalState {
		case build.IndexStateEqual:
			debug.Printf("%s index already up to date", args.String())
			return indexStateNoop, nil

		case build.IndexStateMeta:
			log.Printf("updating index.meta %s", args.String())

			if err := mergeMeta(bo); err != nil {
				log.Printf("falling back to full update: failed to update index.meta %s: %s", args.String(), err)
			} else {
				return indexStateSuccessMeta, nil
			}

		case build.IndexStateCorrupt:
			log.Printf("falling back to full update: corrupt index: %s", args.String())
		}
	}

	log.Printf("updating index %s", args.String())

	runCmd := func(cmd *exec.Cmd) error { return s.loggedRun(tr, cmd) }
	f := s.Indexer
	if f == nil && len(args.Branches) > 1 {
		f = gitIndex
	}
	if f == nil {
		f = archiveIndex
	}
	metricIndexingTotal.Inc()
	return indexStateSuccess, f(args, runCmd)
}

func (s *Server) defaultArgs() *indexArgs {
	return &indexArgs{
		Root:        s.Root,
		IndexDir:    s.IndexDir,
		Parallelism: s.CPUCount,

		Incremental: true,

		// 1 MB; match https://sourcegraph.sgdev.org/github.com/sourcegraph/sourcegraph/-/blob/cmd/symbols/internal/symbols/search.go#L22
		FileLimit: 1 << 20,

		// We are downloading archives from within the same network from
		// another Sourcegraph service (gitserver). This can end up being
		// so fast that we harm gitserver's network connectivity and our
		// own. In the case of zoekt-indexserver and gitserver running on
		// the same host machine, we can even reach up to ~100 Gbps and
		// effectively DoS the Docker network, temporarily disrupting other
		// containers running on the host.
		//
		// Google Compute Engine has a network bandwidth of about 1.64 Gbps
		// between nodes, and AWS varies widely depending on instance type.
		// We play it safe and default to 1 Gbps here (~119 MiB/s), which
		// means we can fetch a 1 GiB archive in ~8.5 seconds.
		DownloadLimitMBPS: "1000", // 1 Gbps
	}
}

func (s *Server) createEmptyShard(tr trace.Trace, name string) error {
	cmd := exec.Command("zoekt-archive-index",
		"-index", s.IndexDir,
		"-incremental",
		"-branch", "HEAD",
		// dummy commit
		"-commit", "404aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"-name", name,
		"-")
	// Empty archive
	cmd.Stdin = bytes.NewBuffer(bytes.Repeat([]byte{0}, 1024))
	return s.loggedRun(tr, cmd)
}

var repoTmpl = template.Must(template.New("name").Parse(`
<html><body>
<a href="debug/requests">Traces</a><br>
{{.IndexMsg}}<br />
<br />
<h3>Re-index repository</h3>
<form action="/" method="post">
{{range .Repos}}
<input type="submit" name="repo" value="{{ . }}" /> <br />
{{end}}
</form>
</body></html>
`))

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var data struct {
		Repos    []string
		IndexMsg string
	}

	if r.Method == "POST" {
		r.ParseForm()
		name := r.Form.Get("repo")
		data.IndexMsg, _ = s.forceIndex(name)
	}

	s.mu.Lock()
	data.Repos = s.lastListRepos
	s.mu.Unlock()

	repoTmpl.Execute(w, data)
}

// enqueueForIndex is expected to be called by other services in order to trigger an index.
// We expect repo-updater to call this endpoint when a new repo has been added to an instance that
// we wish to index and don't want to wait for polling to happen.
func (s *Server) enqueueForIndex(queue *Queue) func(rw http.ResponseWriter, r *http.Request) {
	return func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(rw, "not found", http.StatusNotFound)
			return
		}
		metricsEnqueueRepoForIndex.Inc()
		err := r.ParseForm()
		if err != nil {
			http.Error(rw, "error parsing form", http.StatusBadRequest)
			return
		}
		name := r.Form.Get("repo")
		if name == "" {
			http.Error(rw, "missing repo", http.StatusBadRequest)
			return
		}
		debug.Printf("enqueueRepoForIndex called with repo: %q", name)
		opts, err := getIndexOptions(s.Root, name)
		if err != nil || opts[0].Error != "" {
			http.Error(rw, "fetching index options", http.StatusInternalServerError)
			return
		}
		queue.AddOrUpdate(name, opts[0].IndexOptions)
	}
}

// forceIndex will run the index job for repo name now. It will return always
// return a string explaining what it did, even if it failed.
func (s *Server) forceIndex(name string) (string, error) {
	opts, err := getIndexOptions(s.Root, name)
	if err != nil {
		return fmt.Sprintf("Indexing %s failed: %v", name, err), err
	}
	if errS := opts[0].Error; errS != "" {
		return fmt.Sprintf("Indexing %s failed: %s", name, errS), errors.New(errS)
	}

	args := s.defaultArgs()
	args.Name = name
	args.IndexOptions = opts[0].IndexOptions
	args.Incremental = false // force re-index
	state, err := s.Index(args)
	if err != nil {
		return fmt.Sprintf("Indexing %s failed: %s", args.String(), err), err
	}
	return fmt.Sprintf("Indexed %s with state %s", args.String(), state), nil
}

func listIndexed(indexDir string) []string {
	index := getShards(indexDir)
	repoNames := make([]string, 0, len(index))
	countsByHost := make(map[string]int)
	for name := range index {
		repoNames = append(repoNames, name)
		codeHost := codeHostFromName(name)
		countsByHost[codeHost] += 1
	}
	sort.Strings(repoNames)
	for codeHost, count := range countsByHost {
		metricNumIndexed.WithLabelValues(codeHost).Set(float64(count))
	}
	return repoNames
}

func listRepos(ctx context.Context, hostname string, root *url.URL, indexed []string) ([]string, error) {
	body, err := json.Marshal(&struct {
		Hostname string
		Indexed  []string
	}{
		Hostname: hostname,
		Indexed:  indexed,
	})
	if err != nil {
		return nil, err
	}

	u := root.ResolveReference(&url.URL{Path: "/.internal/repos/index"})
	resp, err := client.Post(u.String(), "application/json; charset=utf8", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list repositories: status %s", resp.Status)
	}

	var data struct {
		RepoNames []string
	}
	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		return nil, err
	}

	countsByHost := make(map[string]int)
	for _, name := range data.RepoNames {
		codeHost := codeHostFromName(name)
		countsByHost[codeHost] += 1
	}
	for codeHost, count := range countsByHost {
		metricNumAssigned.WithLabelValues(codeHost).Set(float64(count))
	}
	return data.RepoNames, nil
}

func ping(root *url.URL) error {
	u := root.ResolveReference(&url.URL{Path: "/.internal/ping", RawQuery: "service=gitserver"})
	resp, err := client.Get(u.String())
	if err != nil {
		return err
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ping: bad HTTP response status %d: %s", resp.StatusCode, string(body))
	}
	if !bytes.Equal(body, []byte("pong")) {
		return fmt.Errorf("ping: did not receive pong: %s", string(body))
	}
	return nil
}

func waitForFrontend(root *url.URL) {
	warned := false
	lastWarn := time.Now()
	for {
		err := ping(root)
		if err == nil {
			break
		}

		if time.Since(lastWarn) > 15*time.Second {
			warned = true
			lastWarn = time.Now()
			log.Printf("frontend or gitserver API not available, will try again: %s", err)
		}

		time.Sleep(250 * time.Millisecond)
	}

	if warned {
		log.Println("frontend API is now reachable. Starting indexing...")
	}
}

func hostnameBestEffort() string {
	if h := os.Getenv("NODE_NAME"); h != "" {
		return h
	}
	if h := os.Getenv("HOSTNAME"); h != "" {
		return h
	}
	hostname, _ := os.Hostname()
	return hostname
}

// setupTmpDir sets up a temporary directory on the same volume as the
// indexes.
func setupTmpDir(index string) error {
	tmpRoot := filepath.Join(index, ".indexserver.tmp")
	if err := os.MkdirAll(tmpRoot, 0755); err != nil {
		return err
	}
	if !tmpfriend.IsTmpFriendDir(tmpRoot) {
		_, err := tmpfriend.RootTempDir(tmpRoot)
		return err
	}
	return nil
}

func printShardStats(fn string) error {
	f, err := os.Open(fn)
	if err != nil {
		return err
	}

	iFile, err := zoekt.NewIndexFile(f)
	if err != nil {
		return err
	}

	return zoekt.PrintNgramStats(iFile)
}

func initializeGoogleCloudProfiler() {
	// Google cloud profiler is opt-in since we only want to run it on
	// Sourcegraph.com.
	if os.Getenv("GOOGLE_CLOUD_PROFILER_ENABLED") == "" {
		return
	}

	err := profiler.Start(profiler.Config{
		Service:        "zoekt-sourcegraph-indexserver",
		ServiceVersion: zoekt.Version,
		MutexProfiling: true,
		AllocForceGC:   true,
	})
	if err != nil {
		log.Printf("could not initialize google cloud profiler: %s", err.Error())
	}
}

func main() {
	defaultIndexDir := os.Getenv("DATA_DIR")
	if defaultIndexDir == "" {
		defaultIndexDir = build.DefaultDir
	}

	root := flag.String("sourcegraph_url", os.Getenv("SRC_FRONTEND_INTERNAL"), "http://sourcegraph-frontend-internal or http://localhost:3090")
	interval := flag.Duration("interval", time.Minute, "sync with sourcegraph this often")
	index := flag.String("index", defaultIndexDir, "set index directory to use")
	listen := flag.String("listen", ":6072", "listen on this address.")
	hostname := flag.String("hostname", hostnameBestEffort(), "the name we advertise to Sourcegraph when asking for the list of repositories to index. Can also be set via the NODE_NAME environment variable.")
	cpuFraction := flag.Float64("cpu_fraction", 1.0, "use this fraction of the cores for indexing.")
	dbg := flag.Bool("debug", false, "turn on more verbose logging.")

	// non daemon mode for debugging/testing
	debugList := flag.Bool("debug-list", false, "do not start the indexserver, rather list the repositories owned by this indexserver then quit.")
	debugIndex := flag.String("debug-index", "", "do not start the indexserver, rather index the repositories then quit.")
	debugShard := flag.String("debug-shard", "", "do not start the indexserver, rather print shard stats then quit.")

	expGitIndex := flag.Bool("exp-git-index", os.Getenv("DISABLE_GIT_INDEX") == "", "use experimental indexing via shallow clones and zoekt-git-index")

	flag.Parse()

	if *cpuFraction <= 0.0 || *cpuFraction > 1.0 {
		log.Fatal("cpu_fraction must be between 0.0 and 1.0")
	}
	if *index == "" {
		log.Fatal("must set -index")
	}
	if *root == "" {
		log.Fatal("must set -sourcegraph_url")
	}
	rootURL, err := url.Parse(*root)
	if err != nil {
		log.Fatalf("url.Parse(%v): %v", *root, err)
	}

	// Tune GOMAXPROCS to match Linux container CPU quota.
	maxprocs.Set()

	// Automatically prepend our own path at the front, to minimize
	// required configuration.
	if l, err := os.Readlink("/proc/self/exe"); err == nil {
		os.Setenv("PATH", filepath.Dir(l)+":"+os.Getenv("PATH"))
	}

	if _, err := os.Stat(*index); err != nil {
		if err := os.MkdirAll(*index, 0755); err != nil {
			log.Fatalf("MkdirAll %s: %v", *index, err)
		}
	}

	if err := setupTmpDir(*index); err != nil {
		log.Fatalf("failed to setup TMPDIR under %s: %v", *index, err)
	}

	if *dbg || *debugList || *debugIndex != "" || *debugShard != "" {
		debug = log.New(os.Stderr, "", log.LstdFlags)
	}
	client.Logger = debug

	cpuCount := int(math.Round(float64(runtime.GOMAXPROCS(0)) * (*cpuFraction)))
	if cpuCount < 1 {
		cpuCount = 1
	}
	s := &Server{
		Root:     rootURL,
		IndexDir: *index,
		Interval: *interval,
		CPUCount: cpuCount,
		Hostname: *hostname,
	}

	if *expGitIndex {
		s.Indexer = gitIndex
	}

	if *debugList {
		repos, err := listRepos(context.Background(), s.Hostname, s.Root, listIndexed(s.IndexDir))
		if err != nil {
			log.Fatal(err)
		}
		for _, r := range repos {
			fmt.Println(r)
		}
		os.Exit(0)
	}

	if *debugIndex != "" {
		msg, err := s.forceIndex(*debugIndex)
		log.Println(msg)
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	if *debugShard != "" {
		err = printShardStats(*debugShard)
		if err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}

	initializeGoogleCloudProfiler()

	queue := &Queue{}

	if *listen != "" {
		go func() {
			mux := http.NewServeMux()
			debugserver.AddHandlers(mux, true)
			mux.Handle("/", s)
			mux.HandleFunc("/enqueueforindex", s.enqueueForIndex(queue))
			debug.Printf("serving HTTP on %s", *listen)
			log.Fatal(http.ListenAndServe(*listen, mux))
		}()
	}

	s.Run(queue)
}
