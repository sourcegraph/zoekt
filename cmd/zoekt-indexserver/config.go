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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type ConfigEntry struct {
	GithubUser             string
	GithubOrg              string
	BitBucketServerProject string
	GitHubURL              string
	GitilesURL             string
	CGitURL                string
	BitBucketServerURL     string
	DisableTLS             bool
	CredentialPath         string
	ProjectType            string
	Name                   string
	Exclude                string
	GitLabURL              string
	OnlyPublic             bool
	GerritApiURL           string
	Topics                 []string
	ExcludeTopics          []string
	Active                 bool
	NoArchived             bool
}

func (c ConfigEntry) IsGithubConfig() bool {
	if c.GitHubURL != "" || c.GithubUser != "" || c.GithubOrg != "" {
		return true
	}

	return false
}

func randomize(entries []ConfigEntry) []ConfigEntry {
	perm := rand.Perm(len(entries))

	var shuffled []ConfigEntry
	for _, i := range perm {
		shuffled = append(shuffled, entries[i])
	}

	return shuffled
}

func isHTTP(ctx context.Context, u string) bool {
	commonAttrs := []attribute.KeyValue{
		attribute.String("url", u),
	}

	ctx, span := tracer.Start(
		ctx,
		"isHTTP",
		trace.WithAttributes(commonAttrs...))

	defer span.End()

	asURL, err := url.Parse(u)
	return err == nil && (asURL.Scheme == "http" || asURL.Scheme == "https")
}

func readConfigURL(ctx context.Context, u string) ([]ConfigEntry, error) {
	var body []byte
	var readErr error
	commonAttrs := []attribute.KeyValue{
		attribute.String("url", u),
	}

	ctx, span := tracer.Start(
		ctx,
		"readConfigURL",
		trace.WithAttributes(commonAttrs...))

	defer span.End()

	if isHTTP(ctx, u) {
		rep, err := http.Get(u)
		if err != nil {
			span.SetAttributes(attribute.Key("err").String(err.Error()))
			return nil, err
		}
		defer rep.Body.Close()

		body, readErr = io.ReadAll(rep.Body)
	} else {
		body, readErr = os.ReadFile(u)
	}

	if readErr != nil {
		return nil, readErr
	}

	var result []ConfigEntry
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func watchFile(ctx context.Context, path string) (<-chan struct{}, error) {
	commonAttrs := []attribute.KeyValue{
		attribute.String("path", path),
	}

	ctx, span := tracer.Start(
		ctx,
		"watchFile",
		trace.WithAttributes(commonAttrs...))

	defer span.End()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		span.SetAttributes(attribute.Key("err").String(err.Error()))
		return nil, err
	}

	if err := watcher.Add(filepath.Dir(path)); err != nil {
		span.SetAttributes(attribute.Key("err").String(err.Error()))
		return nil, err
	}

	out := make(chan struct{}, 1)
	go func() {
		var last time.Time
		for {
			select {
			case <-watcher.Events:
				fi, err := os.Stat(path)
				if err == nil && fi.ModTime() != last {
					out <- struct{}{}
					last = fi.ModTime()
				}
			case err := <-watcher.Errors:
				if err != nil {
					log.Printf("watcher error: %v", err)
				}
			}
		}
	}()
	return out, nil
}

func periodicMirrorFile(ctx context.Context, repoDir string, opts *Options, pendingRepos chan<- string) {
	commonAttrs := []attribute.KeyValue{
		attribute.String("repo-dir", repoDir),
	}

	// work begins
	ctx, span := tracer.Start(
		ctx,
		"periodicMirrorFile",
		trace.WithAttributes(commonAttrs...))

	// end span once done with func
	defer span.End()
	ticker := time.NewTicker(opts.mirrorInterval)

	var watcher <-chan struct{}
	if !isHTTP(ctx, opts.mirrorConfigFile) {
		var err error

		watcher, err = watchFile(ctx, opts.mirrorConfigFile)
		if err != nil {
			// add error message
			span.SetAttributes(attribute.Key("err").String(err.Error()))
			log.Printf("watchFile(%q): %v", opts.mirrorConfigFile, err)
		}
	}

	var lastCfg []ConfigEntry
	for {
		cfg, err := readConfigURL(ctx, opts.mirrorConfigFile)
		if err != nil {
			span.SetAttributes(attribute.Key("err").String(err.Error()))
			log.Printf("readConfig(%s): %v", opts.mirrorConfigFile, err)
		} else {
			lastCfg = cfg
		}

		executeMirror(ctx, lastCfg, repoDir, opts.parallelListApiReqs, opts.parallelClones, pendingRepos)

		select {
		case <-watcher:
			log.Printf("mirror config %s changed", opts.mirrorConfigFile)
		case <-ticker.C:
		}
	}
}

func createGithubArgsMirrorAndFetchArgs(ctx context.Context, c ConfigEntry) []string {
	// work begins
	ctx, span := tracer.Start(ctx, "createGithubArgsMirrorAndFetchArgs")

	// end span once done with func
	defer span.End()

	args := make([]string, 0)
	if c.GitHubURL != "" {
		args = append(args, "-url", c.GitHubURL)
		span.SetAttributes(attribute.Key("url").String(c.GitHubURL))
	}
	if c.GithubUser != "" {
		args = append(args, "-user", c.GithubUser)
		span.SetAttributes(attribute.Key("user").String(c.GithubUser))
	} else if c.GithubOrg != "" {
		args = append(args, "-org", c.GithubOrg)
		span.SetAttributes(attribute.Key("org").String(c.GithubOrg))
	}
	if c.Name != "" {
		args = append(args, "-name", c.Name)
		span.SetAttributes(attribute.Key("name").String(c.Name))
	}
	if c.Exclude != "" {
		args = append(args, "-exclude", c.Exclude)
		span.SetAttributes(attribute.Key("exclude").String(c.Exclude))
	}
	if c.CredentialPath != "" {
		args = append(args, "-token", c.CredentialPath)
		span.SetAttributes(attribute.Key("token-path").String(c.CredentialPath))
	}
	span.SetAttributes(attribute.Key("topic").StringSlice(c.Topics))
	for _, topic := range c.Topics {
		args = append(args, "-topic", topic)

	}
	span.SetAttributes(attribute.Key("exclude_topic").StringSlice(c.ExcludeTopics))
	for _, topic := range c.ExcludeTopics {
		args = append(args, "-exclude_topic", topic)
	}
	if c.NoArchived {
		args = append(args, "-no_archived")
		span.SetAttributes(attribute.Key("no_archived").Bool(c.NoArchived))
	}

	return args
}

func executeMirror(ctx context.Context, cfg []ConfigEntry, repoDir string, parallelListApiReqs, parallelClones int, pendingRepos chan<- string) {
	commonAttrs := []attribute.KeyValue{
		attribute.String("repo-dir", repoDir),
		attribute.Int("parallelListApiReqs", parallelListApiReqs),
		attribute.Int("parallelClones", parallelClones),
	}

	// work begins
	ctx, span := tracer.Start(
		ctx,
		"executeMirror",
		trace.WithAttributes(commonAttrs...))

	// end span once done with func
	defer span.End()

	// Randomize the ordering in which we query
	// things. This is to ensure that quota limits don't
	// always hit the last one in the list.
	cfg = randomize(cfg)
	for _, c := range cfg {
		var cmd *exec.Cmd
		if c.GitHubURL != "" || c.GithubUser != "" || c.GithubOrg != "" {
			cmd = exec.Command("zoekt-mirror-github",
				"-dest", repoDir, "-delete")
			cmd.Args = append(cmd.Args, createGithubArgsMirrorAndFetchArgs(ctx, c)...)
			cmd.Args = append(cmd.Args, "--parallel_clone", strconv.Itoa(parallelClones))
			cmd.Args = append(cmd.Args, "--max-concurrent-gh-requests", strconv.Itoa(parallelListApiReqs))
		} else if c.GitilesURL != "" {
			cmd = exec.Command("zoekt-mirror-gitiles",
				"-dest", repoDir, "-name", c.Name)
			if c.Exclude != "" {
				cmd.Args = append(cmd.Args, "-exclude", c.Exclude)
			}
			cmd.Args = append(cmd.Args, c.GitilesURL)
		} else if c.CGitURL != "" {
			cmd = exec.Command("zoekt-mirror-gitiles",
				"-type", "cgit",
				"-dest", repoDir, "-name", c.Name)
			if c.Exclude != "" {
				cmd.Args = append(cmd.Args, "-exclude", c.Exclude)
			}
			cmd.Args = append(cmd.Args, c.CGitURL)
		} else if c.BitBucketServerURL != "" {
			cmd = exec.Command("zoekt-mirror-bitbucket-server",
				"-dest", repoDir, "-url", c.BitBucketServerURL, "-delete")
			if c.BitBucketServerProject != "" {
				cmd.Args = append(cmd.Args, "-project", c.BitBucketServerProject)
			}
			if c.DisableTLS {
				cmd.Args = append(cmd.Args, "-disable-tls")
			}
			if c.ProjectType != "" {
				cmd.Args = append(cmd.Args, "-type", c.ProjectType)
			}
			if c.Name != "" {
				cmd.Args = append(cmd.Args, "-name", c.Name)
			}
			if c.Exclude != "" {
				cmd.Args = append(cmd.Args, "-exclude", c.Exclude)
			}
			if c.CredentialPath != "" {
				cmd.Args = append(cmd.Args, "-credentials", c.CredentialPath)
			}
		} else if c.GitLabURL != "" {
			cmd = exec.Command("zoekt-mirror-gitlab",
				"-dest", repoDir, "-url", c.GitLabURL)
			if c.Name != "" {
				cmd.Args = append(cmd.Args, "-name", c.Name)
			}
			if c.Exclude != "" {
				cmd.Args = append(cmd.Args, "-exclude", c.Exclude)
			}
			if c.OnlyPublic {
				cmd.Args = append(cmd.Args, "-public")
			}
			if c.CredentialPath != "" {
				cmd.Args = append(cmd.Args, "-token", c.CredentialPath)
			}
		} else if c.GerritApiURL != "" {
			cmd = exec.Command("zoekt-mirror-gerrit",
				"-dest", repoDir, "-delete")
			if c.CredentialPath != "" {
				cmd.Args = append(cmd.Args, "-http-credentials", c.CredentialPath)
			}
			if c.Name != "" {
				cmd.Args = append(cmd.Args, "-name", c.Name)
			}
			if c.Exclude != "" {
				cmd.Args = append(cmd.Args, "-exclude", c.Exclude)
			}
			if c.Active {
				cmd.Args = append(cmd.Args, "-active")
			}
			cmd.Args = append(cmd.Args, c.GerritApiURL)
		}

		stdout, stderr := loggedRun(ctx, cmd)

		fmt.Printf("cmd %v - logs=%s\n", cmd.Args, string(stderr))
		// stdout contains the repos. stderr contains every other log
		reposPushed := 0
		for _, fn := range bytes.Split(stdout, []byte{'\n'}) {
			if len(fn) == 0 {
				continue
			}

			pendingRepos <- string(fn)
			reposPushed += 1
		}

		fmt.Printf("finished %v - pushed %d repos\n", cmd.Args, reposPushed)
	}
}
