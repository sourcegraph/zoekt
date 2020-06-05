// Command zoekt-sourcegraph-indexserver periodically reindexes enabled
// repositories on sourcegraph
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"go.uber.org/automaxprocs/maxprocs"
	"golang.org/x/net/trace"

	"github.com/google/zoekt/build"
	retryablehttp "github.com/hashicorp/go-retryablehttp"
	"github.com/keegancsmith/tmpfriend"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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

	metricIndexDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "index_repo_seconds",
		Help:    "A histogram of latencies for indexing a repository.",
		Buckets: prometheus.ExponentialBuckets(.1, 10, 7), // 100ms -> 27min
	}, []string{"state"}) // state is an indexState
)

type indexState string

const (
	indexStateFail    indexState = "fail"
	indexStateSuccess            = "success"
	indexStateNoop               = "noop"  // We didn't need to update index
	indexStateEmpty              = "empty" // index is empty (empty repo)
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

func (s *Server) loggedRun(tr trace.Trace, cmd *exec.Cmd) error {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cmd.Stdout = out
	cmd.Stderr = errOut

	tr.LazyPrintf("%s", cmd.Args)
	if err := cmd.Run(); err != nil {
		outS := out.String()
		errS := errOut.String()
		tr.LazyPrintf("failed: %v", err)
		tr.LazyPrintf("stdout: %s", outS)
		tr.LazyPrintf("stderr: %s", errS)
		tr.SetError()
		return fmt.Errorf("command %s failed: %v\nOUT: %s\nERR: %s",
			cmd.Args, err, outS, errS)
	}
	tr.LazyPrintf("success")
	debug.Printf("ran successfully %s", cmd.Args)
	return nil
}

// Run the sync loop. This blocks forever.
func (s *Server) Run() {
	removeIncompleteShards(s.IndexDir)
	waitForFrontend(s.Root)

	queue := &Queue{}

	// Start a goroutine which updates the queue with commits to index.
	go func() {
		t := time.NewTicker(s.Interval)
		for {
			repos, err := listRepos(context.Background(), s.Hostname, s.Root, listIndexed(s.IndexDir))
			if err != nil {
				log.Println(err)
				<-t.C
				continue
			}

			s.mu.Lock()
			s.lastListRepos = repos
			s.mu.Unlock()

			debug.Printf("updating index queue with %d repositories", len(repos))

			// ResolveRevision is IO bound on the gitserver service. So we do
			// them concurrently.
			sem := newSemaphore(32)

			// Cleanup job to trash unused shards
			sem.Acquire()
			go func() {
				defer sem.Release()
				cleanup(s.IndexDir, repos, time.Now())
			}()

			tr := trace.New("resolveRevisions", "")
			tr.LazyPrintf("resolving HEAD for %d repos", len(repos))
			start := time.Now()
			for _, name := range repos {
				sem.Acquire()
				go func(name string) {
					defer sem.Release()
					start := time.Now()
					commit, err := resolveRevision(s.Root, name, "HEAD")
					if err != nil && !os.IsNotExist(err) {
						metricResolveRevisionDuration.WithLabelValues("false").Observe(time.Since(start).Seconds())
						tr.LazyPrintf("failed resolving HEAD for %v: %v", name, err)
						tr.SetError()
						return
					}
					metricResolveRevisionDuration.WithLabelValues("true").Observe(time.Since(start).Seconds())
					queue.AddOrUpdate(name, commit)
				}(name)
			}
			sem.Wait()
			metricResolveRevisionsDuration.Observe(time.Since(start).Seconds())
			tr.Finish()

			<-t.C
		}
	}()

	// In the current goroutine process the queue forever.
	for {
		name, commit, ok := queue.Pop()
		if !ok {
			time.Sleep(time.Second)
			continue
		}

		start := time.Now()
		args := s.defaultArgs()
		args.Name = name
		args.Commit = commit
		state, err := s.Index(args)
		metricIndexDuration.WithLabelValues(string(state)).Observe(time.Since(start).Seconds())
		if err != nil {
			log.Printf("error indexing %s: %s", args.String(), err)
			continue
		}
		if state == indexStateSuccess {
			log.Printf("updated index %s in %v", args.String(), time.Since(start))
		}
		queue.SetIndexed(name, commit)
	}
}

// Index starts an index job for repo name at commit.
func (s *Server) Index(args *indexArgs) (state indexState, err error) {
	tr := trace.New("index", args.Name)

	defer func() {
		if err != nil {
			tr.SetError()
			tr.LazyPrintf("error: %v", err)
			state = indexStateFail
		}
		tr.LazyPrintf("state: %s", state)
		tr.Finish()
	}()

	tr.LazyPrintf("commit: %v", args.Commit)

	if args.Commit == "" {
		return indexStateEmpty, s.createEmptyShard(tr, args.Name)
	}

	if err := getIndexOptions(args); err != nil {
		return indexStateFail, err
	}

	if args.Incremental {
		bo := args.BuildOptions()
		bo.SetDefaults()
		if bo.IncrementalSkipIndexing() {
			debug.Printf("%s index already up to date", args.String())
			return indexStateNoop, nil
		}
	}

	runCmd := func(cmd *exec.Cmd) error { return s.loggedRun(tr, cmd) }
	f := s.Indexer
	if f == nil {
		f = archiveIndex
	}
	return indexStateSuccess, f(args, runCmd)
}

func (s *Server) defaultArgs() *indexArgs {
	return &indexArgs{
		Root:        s.Root,
		IndexDir:    s.IndexDir,
		Parallelism: s.CPUCount,

		Incremental: true,
		Branch:      "HEAD",

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

// forceIndex will run the index job for repo name now. It will return always
// return a string explaining what it did, even if it failed.
func (s *Server) forceIndex(name string) (string, error) {
	commit, err := resolveRevision(s.Root, name, "HEAD")
	if err != nil && !os.IsNotExist(err) {
		return fmt.Sprintf("Indexing %s failed: %v", name, err), err
	}
	args := s.defaultArgs()
	args.Name = name
	args.Commit = commit
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
	for name := range index {
		repoNames = append(repoNames, name)
	}
	sort.Strings(repoNames)
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

	return data.RepoNames, nil
}

func resolveRevision(root *url.URL, repo, spec string) (string, error) {
	u := root.ResolveReference(&url.URL{Path: fmt.Sprintf("/.internal/git/%s/resolve-revision/%s", repo, spec)})
	resp, err := client.Get(u.String())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", os.ErrNotExist
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to resolve revision %s@%s: status %s", repo, spec, resp.Status)
	}

	var b bytes.Buffer
	_, err = b.ReadFrom(resp.Body)
	if err != nil {
		return "", err
	}
	return b.String(), nil
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

	expGitIndex := flag.Bool("exp-git-index", os.Getenv("SRC_GIT_INDEX") != "", "use experimental indexing via shallow clones and zoekt-git-index")

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

	if *dbg || *debugList || *debugIndex != "" {
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
		Indexer:  archiveIndex,
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

	if *listen != "" {
		go func() {
			trace.AuthRequest = func(req *http.Request) (any, sensitive bool) {
				return true, true
			}
			prom := promhttp.Handler()
			h := func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/debug/requests":
					trace.Traces(w, r)
				case "/metrics":
					prom.ServeHTTP(w, r)
				default:
					s.ServeHTTP(w, r)
				}
			}
			debug.Printf("serving HTTP on %s", *listen)
			log.Fatal(http.ListenAndServe(*listen, http.HandlerFunc(h)))
		}()
	}

	s.Run()
}
