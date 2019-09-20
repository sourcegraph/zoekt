// Command zoekt-sourcegraph-indexserver periodically reindexes enabled
// repositories on sourcegraph
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"go.uber.org/automaxprocs/maxprocs"
	"golang.org/x/net/trace"

	"github.com/google/zoekt/build"
	retryablehttp "github.com/hashicorp/go-retryablehttp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	resolveRevisionsDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "resolve_revisions_seconds",
		Help:    "A histogram of latencies for resolving all repository revisions.",
		Buckets: prometheus.ExponentialBuckets(1, 10, 6), // 1s -> 27min
	})

	resolveRevisionDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "resolve_revision_seconds",
		Help:    "A histogram of latencies for resolving a repository revision.",
		Buckets: prometheus.ExponentialBuckets(.25, 2, 4), // 250ms -> 2s
	}, []string{"success"}) // success=true|false

	indexDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "index_repo_seconds",
		Help:    "A histogram of latencies for indexing a repository.",
		Buckets: prometheus.ExponentialBuckets(.1, 10, 7), // 100ms -> 27min
	}, []string{"state"}) // state=fail|success
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

	// CPUCount is the amount of parallelism to use when indexing a
	// repository.
	CPUCount int
}

var debug = log.New(ioutil.Discard, "", log.LstdFlags)

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
	queue := &Queue{}

	// Start a goroutine which updates the queue with commits to index.
	go func() {
		t := time.NewTicker(s.Interval)
		for {
			hostname, err := os.Hostname()
			if err != nil {
				log.Println(err)
				<-t.C
				continue
			}

			repos, err := listRepos(hostname, s.Root)
			if err != nil {
				log.Println(err)
				<-t.C
				continue
			}

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
						resolveRevisionDuration.WithLabelValues("false").Observe(time.Since(start).Seconds())
						tr.LazyPrintf("failed resolving HEAD for %v: %v", name, err)
						tr.SetError()
						return
					}
					resolveRevisionDuration.WithLabelValues("true").Observe(time.Since(start).Seconds())
					queue.AddOrUpdate(name, commit)
				}(name)
			}
			sem.Wait()
			resolveRevisionsDuration.Observe(time.Since(start).Seconds())
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
		err := s.Index(name, commit)
		if err != nil {
			indexDuration.WithLabelValues("fail").Observe(time.Since(start).Seconds())
			log.Printf("error indexing %s@%s: %s", name, commit, err)
			continue
		}
		indexDuration.WithLabelValues("success").Observe(time.Since(start).Seconds())
		queue.SetIndexed(name, commit)
	}
}

type IndexOptions struct {
	// LargeFiles is a slice of glob patterns where matching files are
	// indexed regardless of their size.
	LargeFiles []string

	// Symbols is a boolean that indicates whether to generate ctags metadata or not
	Symbols bool
}

func (o *IndexOptions) toArgs() []string {
	args := make([]string, 0, len(o.LargeFiles)*2+1)
	if o.Symbols {
		args = append(args, "-require_ctags")
	} else {
		args = append(args, "-disable_ctags")
	}
	for _, a := range o.LargeFiles {
		args = append(args, "-large_file", a)
	}
	return args
}

func getIndexOptions(root *url.URL, client *http.Client) (*IndexOptions, error) {
	if client == nil {
		client = http.DefaultClient
	}
	u := root.ResolveReference(&url.URL{Path: "/.internal/search/configuration"})
	resp, err := client.Get(u.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, os.ErrNotExist
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Println(resp.StatusCode)
		return nil, errors.New("failed to get configuration options")
	}

	var opts IndexOptions

	err = json.NewDecoder(resp.Body).Decode(&opts)
	if err != nil {
		return nil, fmt.Errorf("error decoding body: %v", err)
	}

	return &opts, nil
}

// Index starts an index job for repo name at commit.
func (s *Server) Index(name, commit string) error {
	tr := trace.New("index", name)
	defer tr.Finish()

	tr.LazyPrintf("commit: %v", commit)

	if commit == "" {
		return s.createEmptyShard(tr, name)
	}

	opts, err := getIndexOptions(s.Root, nil)
	if err != nil {
		return err
	}

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
	downloadLimitMbps := "1000" // 1 Gbps

	args := []string{
		fmt.Sprintf("-parallelism=%d", s.CPUCount),
		"-index", s.IndexDir,
		"-file_limit", strconv.Itoa(1 << 20), // 1 MB; match https://sourcegraph.sgdev.org/github.com/sourcegraph/sourcegraph/-/blob/cmd/symbols/internal/symbols/search.go#L22
		"-incremental",
		"-branch", "HEAD",
		"-commit", commit,
		"-name", name,
		"-download-limit-mbps", downloadLimitMbps,
	}
	args = append(args, opts.toArgs()...)
	args = append(args, tarballURL(s.Root, name, commit))

	cmd := exec.Command("zoekt-archive-index", args...)
	// Prevent prompting
	cmd.Stdin = &bytes.Buffer{}
	return s.loggedRun(tr, cmd)
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
		index := func() error {
			commit, err := resolveRevision(s.Root, name, "HEAD")
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			return s.Index(name, commit)
		}
		err := index()
		if err != nil {
			data.IndexMsg = fmt.Sprintf("Indexing %s failed: %s", name, err)
		} else {
			data.IndexMsg = "Indexed " + name
		}
	}

	hostname, err := os.Hostname()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data.Repos, err = listRepos(hostname, s.Root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	repoTmpl.Execute(w, data)
}

func listRepos(hostname string, root *url.URL) ([]string, error) {
	c := retryablehttp.NewClient()
	c.Logger = debug

	body, err := json.Marshal(&struct {
		Hostname string
		Enabled  bool
		Index    bool
	}{
		Hostname: hostname,
		Enabled:  true,
		Index:    true,
	})
	if err != nil {
		return nil, err
	}

	u := root.ResolveReference(&url.URL{Path: "/.internal/repos/list"})
	resp, err := c.Post(u.String(), "application/json; charset=utf8", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list repositories: status %s", resp.Status)
	}

	var data []struct {
		URI string
	}
	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		return nil, err
	}

	repos := make([]string, len(data))
	for i, r := range data {
		repos[i] = r.URI
	}
	return repos, nil
}

func resolveRevision(root *url.URL, repo, spec string) (string, error) {
	u := root.ResolveReference(&url.URL{Path: fmt.Sprintf("/.internal/git/%s/resolve-revision/%s", repo, spec)})
	resp, err := http.Get(u.String())
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

func tarballURL(root *url.URL, repo, commit string) string {
	return root.ResolveReference(&url.URL{Path: fmt.Sprintf("/.internal/git/%s/tar/%s", repo, commit)}).String()
}

func main() {
	root := flag.String("sourcegraph_url", "", "http://sourcegraph-frontend-internal or http://localhost:3090")
	interval := flag.Duration("interval", 10*time.Minute, "sync with sourcegraph this often")
	index := flag.String("index", build.DefaultDir, "set index directory to use")
	listen := flag.String("listen", "", "listen on this address.")
	cpuFraction := flag.Float64("cpu_fraction", 0.25,
		"use this fraction of the cores for indexing.")
	dbg := flag.Bool("debug", false,
		"turn on more verbose logging.")
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

	if *dbg {
		debug = log.New(os.Stderr, "", log.LstdFlags)
	}

	cpuCount := int(math.Round(float64(runtime.GOMAXPROCS(0)) * (*cpuFraction)))
	if cpuCount < 1 {
		cpuCount = 1
	}
	s := &Server{
		Root:     rootURL,
		IndexDir: *index,
		Interval: *interval,
		CPUCount: cpuCount,
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
