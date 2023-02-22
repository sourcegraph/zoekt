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

// This program manages a zoekt dynamic indexing deployment:
// * listens to indexing commands
// * reindexes specified repositories

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func loggedRun(cmd *exec.Cmd) error {
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf

	log.Printf("run %v", cmd.Args)
	if err := cmd.Run(); err != nil {
		log.Printf("command %s failed: %v\nOUT: %s\nERR: %s",
			cmd.Args, err, outBuf.String(), errBuf.String())
		return fmt.Errorf("command %s failed: %v", cmd.Args, err)
	}

	return nil
}

type Options struct {
	indexTimeout time.Duration
	repoDir      string
	indexDir     string
	listen       string
}

func (o *Options) createMissingDirectories() {
	for _, s := range []string{o.repoDir, o.indexDir} {
		if err := os.MkdirAll(s, 0o755); err != nil {
			log.Fatalf("MkdirAll %s: %v", s, err)
		}
	}
}

type indexRequest struct {
	CloneURL string // TODO: Decide if tokens can be in the URL or if we should pass separately
	RepoID   uint32
}

// This function is declared as var so that we can stub it in test
var executeCmd = func(ctx context.Context, name string, arg ...string) error {
	cmd := exec.CommandContext(ctx, name, arg...)
	cmd.Stdin = &bytes.Buffer{}
	err := loggedRun(cmd)

	return err
}

func indexRepository(opts Options, req indexRequest) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opts.indexTimeout)
	defer cancel()

	args := []string{}
	args = append(args, "-dest", opts.repoDir)
	args = append(args, "-name", strconv.FormatUint(uint64(req.RepoID), 10))
	args = append(args, "-repoid", strconv.FormatUint(uint64(req.RepoID), 10))
	args = append(args, req.CloneURL)
	err := executeCmd(ctx, "zoekt-git-clone", args...)
	if err != nil {
		return nil, err
	}

	gitRepoPath, err := filepath.Abs(filepath.Join(opts.repoDir, fmt.Sprintf("%d.git", req.RepoID)))
	if err != nil {
		return nil, err
	}

	args = []string{
		"-C",
		gitRepoPath,
		"fetch",
	}
	err = executeCmd(ctx, "git", args...)
	if err != nil {
		return nil, err
	}

	args = []string{
		"-index", opts.indexDir,
		gitRepoPath,
	}
	err = executeCmd(ctx, "zoekt-git-index", args...)
	if err != nil {
		return nil, err
	}

	response := map[string]any{
		"Success": true,
	}

	return response, nil
}

type indexServer struct {
	opts                 Options
	promRegistry         *prometheus.Registry
	metricsRequestsTotal *prometheus.CounterVec
}

func (s *indexServer) serveHealthCheck(w http.ResponseWriter, r *http.Request) {
	// Nothing to do. Just return 200
}

func (s *indexServer) serveMetrics(w http.ResponseWriter, r *http.Request) {
	promhttp.HandlerFor(s.promRegistry, promhttp.HandlerOpts{Registry: s.promRegistry}).ServeHTTP(w, r)
}

func (s *indexServer) serveIndex(w http.ResponseWriter, r *http.Request) {
	route := "index"
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req indexRequest
	err := dec.Decode(&req)

	if err != nil {
		log.Printf("Error decoding index request: %v", err)
		http.Error(w, "JSON parser error", http.StatusBadRequest)
		return
	}

	response, err := indexRepository(s.opts, req)
	if err != nil {
		s.respondWithError(w, r.Method, route, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)

	s.incrementRequestsTotal(r.Method, route, http.StatusOK)
}

func (s *indexServer) serveTruncate(w http.ResponseWriter, r *http.Request) {
	route := "truncate"
	err := emptyDirectory(s.opts.repoDir)

	if err != nil {
		err = fmt.Errorf("Failed to empty repoDir repoDir: %v with error: %v", s.opts.repoDir, err)

		s.respondWithError(w, r.Method, route, err)
		return
	}

	err = emptyDirectory(s.opts.indexDir)

	if err != nil {
		err = fmt.Errorf("Failed to empty repoDir indexDir: %v with error: %v", s.opts.repoDir, err)

		s.respondWithError(w, r.Method, route, err)
		return
	}

	response := map[string]any{
		"Success": true,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)

	s.incrementRequestsTotal(r.Method, route, http.StatusOK)
}

func (s *indexServer) respondWithError(w http.ResponseWriter, method, route string, err error) {
	responseCode := http.StatusInternalServerError

	log.Print(err)
	s.incrementRequestsTotal(method, route, responseCode)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(responseCode)
	response := map[string]any{
		"Success": false,
		"Error":   err.Error(),
	}

	_ = json.NewEncoder(w).Encode(response)
}

func (s *indexServer) incrementRequestsTotal(method, route string, responseCode int) {
	s.metricsRequestsTotal.With(prometheus.Labels{"code": strconv.Itoa(responseCode), "method": method, "route": route}).Inc()
}

func (s *indexServer) initMetrics() {
	s.promRegistry = prometheus.NewRegistry()

	// Add go runtime metrics and process collectors.
	s.promRegistry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	s.metricsRequestsTotal = promauto.With(s.promRegistry).NewCounterVec(
		prometheus.CounterOpts{
			Name: "zoekt_dynamic_indexserver_requests_total",
			Help: "Total number of HTTP requests by status code, method, and route.",
		},
		[]string{"method", "route", "code"},
	)
}

func (s *indexServer) startIndexingApi() {
	http.HandleFunc("/", s.serveHealthCheck)
	http.HandleFunc("/metrics", s.serveMetrics)
	http.HandleFunc("/index", s.serveIndex)
	http.HandleFunc("/truncate", s.serveTruncate)

	if err := http.ListenAndServe(s.opts.listen, nil); err != nil {
		log.Fatal(err)
	}
}

func emptyDirectory(dir string) error {
	files, err := os.ReadDir(dir)

	if err != nil {
		return err
	}

	for _, file := range files {
		filePath := filepath.Join(dir, file.Name())
		err := os.RemoveAll(filePath)
		if err != nil {
			return err
		}
	}

	return nil
}

func parseOptions() Options {
	repoDir := flag.String("repo_dir", "", "directory holding cloned repos.")
	indexDir := flag.String("index_dir", "", "directory holding index shards.")
	timeout := flag.Duration("index_timeout", time.Hour, "kill index job after this much time.")
	listen := flag.String("listen", ":6060", "listen on this address.")
	flag.Parse()

	if *repoDir == "" {
		log.Fatal("must set -repo_dir")
	}

	if *indexDir == "" {
		log.Fatal("must set -index_dir")
		*indexDir = filepath.Join(*repoDir, "index")
	}

	return Options{
		repoDir:      *repoDir,
		indexDir:     *indexDir,
		indexTimeout: *timeout,
		listen:       *listen,
	}
}

func main() {
	opts := parseOptions()
	opts.createMissingDirectories()

	server := indexServer{
		opts: opts,
	}

	server.initMetrics()
	server.startIndexingApi()
}
