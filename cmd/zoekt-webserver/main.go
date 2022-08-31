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

// Command zoekt-webserver responds to search queries, using an index generated
// by another program such as zoekt-indexserver.

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/build"
	"github.com/sourcegraph/zoekt/debugserver"
	"github.com/sourcegraph/zoekt/internal/profiler"
	"github.com/sourcegraph/zoekt/internal/tracer"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/shards"
	"github.com/sourcegraph/zoekt/stream"
	"github.com/sourcegraph/zoekt/web"

	"github.com/opentracing/opentracing-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	sglog "github.com/sourcegraph/log"
	"github.com/uber/jaeger-client-go"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/automaxprocs/maxprocs"
)

const logFormat = "2006-01-02T15-04-05.999999999Z07"

func divertLogs(dir string, interval time.Duration) {
	t := time.NewTicker(interval)
	var last *os.File
	for {
		nm := filepath.Join(dir, fmt.Sprintf("zoekt-webserver.%s.%d.log", time.Now().Format(logFormat), os.Getpid()))
		fmt.Fprintf(os.Stderr, "writing logs to %s\n", nm)

		f, err := os.Create(nm)
		if err != nil {
			// There is not much we can do now.
			fmt.Fprintf(os.Stderr, "can't create output file %s: %v\n", nm, err)
			os.Exit(2)
		}

		log.SetOutput(f)
		last.Close()

		last = f

		<-t.C
	}
}

const templateExtension = ".html.tpl"

func loadTemplates(tpl *template.Template, dir string) error {
	fs, err := filepath.Glob(dir + "/*" + templateExtension)
	if err != nil {
		log.Fatalf("Glob: %v", err)
	}

	log.Printf("loading templates: %v", fs)
	for _, fn := range fs {
		content, err := os.ReadFile(fn)
		if err != nil {
			return err
		}

		base := filepath.Base(fn)
		base = strings.TrimSuffix(base, templateExtension)
		if _, err := tpl.New(base).Parse(string(content)); err != nil {
			return fmt.Errorf("template.Parse(%s): %v", fn, err)
		}
	}
	return nil
}

func writeTemplates(dir string) error {
	if dir == "" {
		return fmt.Errorf("must set --template_dir")
	}

	for k, v := range web.TemplateText {
		nm := filepath.Join(dir, k+templateExtension)
		if err := os.WriteFile(nm, []byte(v), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	logDir := flag.String("log_dir", "", "log to this directory rather than stderr.")
	logRefresh := flag.Duration("log_refresh", 24*time.Hour, "if using --log_dir, start writing a new file this often.")

	listen := flag.String("listen", ":6070", "listen on this address.")
	index := flag.String("index", build.DefaultDir, "set index directory to use")
	html := flag.Bool("html", true, "enable HTML interface")
	enableRPC := flag.Bool("rpc", false, "enable go/net RPC")
	print := flag.Bool("print", false, "enable local result URLs")
	enablePprof := flag.Bool("pprof", false, "set to enable remote profiling.")
	sslCert := flag.String("ssl_cert", "", "set path to SSL .pem holding certificate.")
	sslKey := flag.String("ssl_key", "", "set path to SSL .pem holding key.")
	hostCustomization := flag.String(
		"host_customization", "",
		"specify host customization, as HOST1=QUERY,HOST2=QUERY")

	templateDir := flag.String("template_dir", "", "set directory from which to load custom .html.tpl template files")
	dumpTemplates := flag.Bool("dump_templates", false, "dump templates into --template_dir and exit.")
	version := flag.Bool("version", false, "Print version number")

	flag.Parse()

	if *version {
		fmt.Printf("zoekt-webserver version %q\n", zoekt.Version)
		os.Exit(0)
	}

	if *dumpTemplates {
		if err := writeTemplates(*templateDir); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}

	liblog := sglog.Init(sglog.Resource{
		Name:       "zoekt-webserver",
		Version:    zoekt.Version,
		InstanceID: os.Getenv("HOSTNAME"),
	})
	defer liblog.Sync()
	tracer.Init("zoekt-webserver", zoekt.Version)
	profiler.Init("zoekt-webserver", zoekt.Version, -1)

	if *logDir != "" {
		if fi, err := os.Lstat(*logDir); err != nil || !fi.IsDir() {
			log.Fatalf("%s is not a directory", *logDir)
		}
		// We could do fdup acrobatics to also redirect
		// stderr, but it is simpler and more portable for the
		// caller to divert stderr output if necessary.
		go divertLogs(*logDir, *logRefresh)
	}

	// Tune GOMAXPROCS to match Linux container CPU quota.
	_, _ = maxprocs.Set()

	if err := os.MkdirAll(*index, 0o755); err != nil {
		log.Fatal(err)
	}

	mustRegisterDiskMonitor(*index)

	// Do not block on loading shards so we can become partially available
	// sooner. Otherwise on large instances zoekt can be unavailable on the
	// order of minutes.
	searcher, err := shards.NewDirectorySearcherFast(*index)
	if err != nil {
		log.Fatal(err)
	}

	searcher = &loggedSearcher{
		Streamer: searcher,
		Logger:   sglog.Scoped("searcher", ""),
	}

	s := &web.Server{
		Searcher: searcher,
		Top:      web.Top,
		Version:  zoekt.Version,
	}

	if *templateDir != "" {
		if err := loadTemplates(s.Top, *templateDir); err != nil {
			log.Fatalf("loadTemplates: %v", err)
		}
	}

	s.Print = *print
	s.HTML = *html
	s.RPC = *enableRPC

	if *hostCustomization != "" {
		s.HostCustomQueries = map[string]string{}
		for _, h := range strings.SplitN(*hostCustomization, ",", -1) {
			if len(h) == 0 {
				continue
			}
			fields := strings.SplitN(h, "=", 2)
			if len(fields) < 2 {
				log.Fatalf("invalid host_customization %q", h)
			}

			s.HostCustomQueries[fields[0]] = fields[1]
		}
	}

	handler, err := web.NewMux(s)
	if err != nil {
		log.Fatal(err)
	}

	debugserver.AddHandlers(handler, *enablePprof)

	// Sourcegraph: We use environment variables to configure watchdog since
	// they are more convenient than flags in containerized environments.
	watchdogTick := 30 * time.Second
	if v := os.Getenv("ZOEKT_WATCHDOG_TICK"); v != "" {
		watchdogTick, _ = time.ParseDuration(v)
		log.Printf("custom ZOEKT_WATCHDOG_TICK=%v", watchdogTick)
	}

	watchdogErrCount := 3
	if v := os.Getenv("ZOEKT_WATCHDOG_ERRORS"); v != "" {
		watchdogErrCount, _ = strconv.Atoi(v)
		log.Printf("custom ZOEKT_WATCHDOG_ERRORS=%d", watchdogErrCount)
	}

	watchdogAddr := "http://" + *listen
	if *sslCert != "" || *sslKey != "" {
		watchdogAddr = "https://" + *listen
	}
	watchdogAddr += "/healthz"

	if watchdogErrCount > 0 && watchdogTick > 0 {
		go watchdog(watchdogTick, watchdogErrCount, watchdogAddr)
	} else {
		log.Println("watchdog disabled")
	}

	srv := &http.Server{
		Addr:    *listen,
		Handler: handler,
	}

	go func() {
		sglog.Scoped("server", "").Info("starting server", sglog.Stringp("address", listen))
		var err error
		if *sslCert != "" || *sslKey != "" {
			err = srv.ListenAndServeTLS(*sslCert, *sslKey)
		} else {
			err = srv.ListenAndServe()
		}

		if err != http.ErrServerClosed {
			// Fatal otherwise shutdownOnSignal will block
			log.Fatalf("ListenAndServe: %v", err)
		}
	}()

	if err := shutdownOnSignal(srv); err != nil {
		log.Fatalf("http.Server.Shutdown: %v", err)
	}
}

// shutdownOnSignal will listen for SIGINT or SIGTERM and call
// srv.Shutdown. Note it doesn't call anything else for shutting down. Notably
// our RPC framework doesn't allow us to drain connections, so it when
// Shutdown is called all inflight RPC requests will be closed.
func shutdownOnSignal(srv *http.Server) error {
	c := make(chan os.Signal, 3)
	signal.Notify(c, os.Interrupt)    // terminal C-c and goreman
	signal.Notify(c, syscall.SIGTERM) // Kubernetes

	<-c

	// If we receive another signal, immediate shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
		case sig := <-c:
			log.Printf("received another signal (%v), immediate shutdown", sig)
			cancel()
		}
	}()

	// Wait for 10s to drain ongoing requests. Kubernetes gives us 30s to
	// shutdown, we have already used 15s waiting for our endpoint removal to
	// propagate.
	ctx, cancel2 := context.WithTimeout(ctx, 10*time.Second)
	defer cancel2()

	log.Printf("shutting down")
	return srv.Shutdown(ctx)
}

func watchdogOnce(ctx context.Context, client *http.Client, addr string) error {
	defer metricWatchdogTotal.Inc()

	ctx, cancel := context.WithDeadline(ctx, time.Now().Add(30*time.Second))
	defer cancel()

	req, err := http.NewRequest("GET", addr, nil)
	if err != nil {
		return err
	}

	req = req.WithContext(ctx)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("watchdog: status %v", resp.StatusCode)
	}
	return nil
}

func watchdog(dt time.Duration, maxErrCount int, addr string) {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{
		Transport: tr,
	}
	tick := time.NewTicker(dt)

	errCount := 0
	for range tick.C {
		err := watchdogOnce(context.Background(), client, addr)
		if err != nil {
			errCount++
			metricWatchdogErrors.Set(float64(errCount))
			metricWatchdogErrorsTotal.Inc()
			if errCount >= maxErrCount {
				log.Panicf("watchdog: %v", err)
			} else {
				log.Printf("watchdog: failed, will try %d more times: %v", maxErrCount-errCount, err)
			}
		} else if errCount > 0 {
			errCount = 0
			metricWatchdogErrors.Set(float64(errCount))
			log.Printf("watchdog: success, resetting error count")
		}
	}
}

func mustRegisterDiskMonitor(path string) {
	prometheus.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name:        "src_disk_space_available_bytes",
		Help:        "Amount of free space disk space.",
		ConstLabels: prometheus.Labels{"path": path},
	}, func() float64 {
		var stat syscall.Statfs_t
		_ = syscall.Statfs(path, &stat)
		return float64(stat.Bavail * uint64(stat.Bsize))
	}))

	prometheus.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name:        "src_disk_space_total_bytes",
		Help:        "Amount of total disk space.",
		ConstLabels: prometheus.Labels{"path": path},
	}, func() float64 {
		var stat syscall.Statfs_t
		_ = syscall.Statfs(path, &stat)
		return float64(stat.Blocks * uint64(stat.Bsize))
	}))
}

type loggedSearcher struct {
	zoekt.Streamer
	Logger sglog.Logger
}

func (s *loggedSearcher) Search(
	ctx context.Context,
	q query.Q,
	opts *zoekt.SearchOptions,
) (sr *zoekt.SearchResult, err error) {
	defer func() {
		var stats *zoekt.Stats
		if sr != nil {
			stats = &sr.Stats
		}
		s.log(ctx, q, opts, stats, err)
	}()

	return s.Streamer.Search(ctx, q, opts)
}

func (s *loggedSearcher) StreamSearch(
	ctx context.Context,
	q query.Q,
	opts *zoekt.SearchOptions,
	sender zoekt.Sender,
) error {
	var (
		stats zoekt.Stats
	)
	err := s.Streamer.StreamSearch(ctx, q, opts, stream.SenderFunc(func(event *zoekt.SearchResult) {
		stats.Add(event.Stats)
		sender.Send(event)
	}))

	s.log(ctx, q, opts, &stats, err)

	return err
}

func (s *loggedSearcher) log(ctx context.Context, q query.Q, opts *zoekt.SearchOptions, st *zoekt.Stats, err error) {
	logger := s.Logger.
		WithTrace(traceContext(ctx)).
		With(
			sglog.String("query", q.String()),
			sglog.Bool("opts.EstimateDocCount", opts.EstimateDocCount),
			sglog.Bool("opts.Whole", opts.Whole),
			sglog.Int("opts.ShardMaxMatchCount", opts.ShardMaxMatchCount),
			sglog.Int("opts.TotalMaxMatchCount", opts.TotalMaxMatchCount),
			sglog.Int("opts.ShardMaxImportantMatch", opts.ShardMaxImportantMatch),
			sglog.Int("opts.TotalMaxImportantMatch", opts.TotalMaxImportantMatch),
			sglog.Duration("opts.MaxWallTime", opts.MaxWallTime),
			sglog.Int("opts.MaxDocDisplayCount", opts.MaxDocDisplayCount),
		)
	if err != nil {
		logger.Error("search failed", sglog.Error(err))
		return
	}

	if st == nil {
		return
	}

	logger.Debug("search",
		sglog.Int64("stat.ContentBytesLoaded", st.ContentBytesLoaded),
		sglog.Int64("stat.IndexBytesLoaded", st.IndexBytesLoaded),
		sglog.Int("stat.Crashes", st.Crashes),
		sglog.Duration("stat.Duration", st.Duration),
		sglog.Int("stat.FileCount", st.FileCount),
		sglog.Int("stat.ShardFilesConsidered", st.ShardFilesConsidered),
		sglog.Int("stat.FilesConsidered", st.FilesConsidered),
		sglog.Int("stat.FilesLoaded", st.FilesLoaded),
		sglog.Int("stat.FilesSkipped", st.FilesSkipped),
		sglog.Int("stat.ShardsScanned", st.ShardsScanned),
		sglog.Int("stat.ShardsSkipped", st.ShardsSkipped),
		sglog.Int("stat.ShardsSkippedFilter", st.ShardsSkippedFilter),
		sglog.Int("stat.MatchCount", st.MatchCount),
		sglog.Int("stat.NgramMatches", st.NgramMatches),
		sglog.Duration("stat.Wait", st.Wait),
	)
}

func traceContext(ctx context.Context) sglog.TraceContext {
	otSpan := opentracing.SpanFromContext(ctx)
	if otSpan != nil {
		if jaegerSpan, ok := otSpan.Context().(jaeger.SpanContext); ok {
			return sglog.TraceContext{
				TraceID: jaegerSpan.TraceID().String(),
				SpanID:  jaegerSpan.SpanID().String(),
			}
		}
	}

	if otelSpan := oteltrace.SpanFromContext(ctx).SpanContext(); otelSpan.IsValid() {
		return sglog.TraceContext{
			TraceID: otelSpan.TraceID().String(),
			SpanID:  otelSpan.SpanID().String(),
		}
	}

	return sglog.TraceContext{}
}

var (
	metricWatchdogErrors = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zoekt_webserver_watchdog_errors",
		Help: "The current error count for zoekt watchdog.",
	})
	metricWatchdogTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "zoekt_webserver_watchdog_total",
		Help: "The total number of requests done by zoekt watchdog.",
	})
	metricWatchdogErrorsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "zoekt_webserver_watchdog_errors_total",
		Help: "The total number of errors from zoekt watchdog.",
	})
)
