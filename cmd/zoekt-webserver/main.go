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
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
	"github.com/google/zoekt/debugserver"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/shards"
	"github.com/google/zoekt/web"
	"github.com/opentracing/opentracing-go"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/automaxprocs/maxprocs"

	"github.com/uber/jaeger-client-go"
	jaegercfg "github.com/uber/jaeger-client-go/config"
	jaegerlog "github.com/uber/jaeger-client-go/log"
	jaegermetrics "github.com/uber/jaeger-lib/metrics"
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
		content, err := ioutil.ReadFile(fn)
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
		if err := ioutil.WriteFile(nm, []byte(v), 0644); err != nil {
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
	useJaeger := flag.Bool("jaeger", false, "Collect and send traces to Jaeger. Jaeger configuration set via environment variables. Only traces for RPC requests that explicitly opt into traces will be collected.")
	flag.Parse()

	if *useJaeger {
		log.Printf("connecting to Jaeger")
		cfg, err := jaegercfg.FromEnv()
		cfg.ServiceName = "zoekt"
		if err != nil {
			log.Printf("could not initialize jaeger tracer from env, error: %v", err.Error())
			return
		}
		if reflect.DeepEqual(cfg.Sampler, &jaegercfg.SamplerConfig{}) {
			// Default sampler configuration for when it is not specified via
			// JAEGER_SAMPLER_* env vars. In most cases, this is sufficient
			// enough to connect to Jaeger without any env vars.
			cfg.Sampler.Type = jaeger.SamplerTypeConst
			cfg.Sampler.Param = 1
		}
		tracer, _, err := cfg.NewTracer(
			jaegercfg.Logger(jaegerlog.StdLogger),
			jaegercfg.Metrics(jaegermetrics.NullFactory),
		)
		if err != nil {
			log.Printf("could not initialize jaeger tracer, error: %v", err.Error())
		}
		opentracing.SetGlobalTracer(tracer)
	}

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

	if err := os.MkdirAll(*index, 0755); err != nil {
		log.Fatal(err)
	}

	mustRegisterDiskMonitor(*index)

	searcher, err := shards.NewDirectorySearcher(*index)
	if err != nil {
		log.Fatal(err)
	}

	// Sourcegraph: Add logging if debug logging enabled
	logLvl := os.Getenv("SRC_LOG_LEVEL")
	debug := logLvl == "" || strings.EqualFold(logLvl, "dbug")
	if debug {
		searcher = &loggedSearcher{Searcher: searcher}
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
	handler.HandleFunc("/healthz", healthz)

	watchdogAddr := "http://" + *listen
	if *sslCert != "" || *sslKey != "" {
		watchdogAddr = "https://" + *listen
	}
	go watchdog(30*time.Second, watchdogAddr)

	if debug {
		log.Printf("listening on %v", *listen)
	}
	if *sslCert != "" || *sslKey != "" {
		err = http.ListenAndServeTLS(*listen, *sslCert, *sslKey, handler)
	} else {
		err = http.ListenAndServe(*listen, handler)
	}
	log.Printf("ListenAndServe: %v", err)
}

// Always returns 200 OK.
// Used for kubernetes liveness and readiness checks.
// https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-probes/
func healthz(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte("OK"))
}

func watchdogOnce(ctx context.Context, client *http.Client, addr string) error {
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

func watchdog(dt time.Duration, addr string) {
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
		} else {
			errCount = 0
		}
		if errCount == 3 {
			log.Panicf("watchdog: %v", err)
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
	zoekt.Searcher
}

func (s *loggedSearcher) Search(ctx context.Context, q query.Q, opts *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	sr, err := s.Searcher.Search(ctx, q, opts)
	if err != nil {
		log.Printf("EROR: search failed q=%s: %s", q.String(), err.Error())
	}
	if sr != nil {
		log.Printf("DBUG: search q=%s Options{EstimateDocCount=%v Whole=%v ShardMaxMatchCount=%v TotalMaxMatchCount=%v ShardMaxImportantMatch=%v TotalMaxImportantMatch=%v MaxWallTime=%v MaxDocDisplayCount=%v} Stats{ContentBytesLoaded=%v IndexBytesLoaded=%v Crashes=%v Duration=%v FileCount=%v ShardFilesConsidered=%v FilesConsidered=%v FilesLoaded=%v FilesSkipped=%v ShardsSkipped=%v MatchCount=%v NgramMatches=%v Wait=%v}", q.String(), opts.EstimateDocCount, opts.Whole, opts.ShardMaxMatchCount, opts.TotalMaxMatchCount, opts.ShardMaxImportantMatch, opts.TotalMaxImportantMatch, opts.MaxWallTime, opts.MaxDocDisplayCount, sr.Stats.ContentBytesLoaded, sr.Stats.IndexBytesLoaded, sr.Stats.Crashes, sr.Stats.Duration, sr.Stats.FileCount, sr.Stats.ShardFilesConsidered, sr.Stats.FilesConsidered, sr.Stats.FilesLoaded, sr.Stats.FilesSkipped, sr.Stats.ShardsSkipped, sr.Stats.MatchCount, sr.Stats.NgramMatches, sr.Stats.Wait)
	}
	return sr, err
}
