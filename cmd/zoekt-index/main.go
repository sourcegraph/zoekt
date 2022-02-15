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
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
	"github.com/google/zoekt/cmd"
	"github.com/google/zoekt/ignore"
	"go.uber.org/automaxprocs/maxprocs"
)

type fileInfo struct {
	name string
	size int64
}

type fileAggregator struct {
	ignoreDirs     map[string]struct{}
	ignoreMatchers []*ignore.Matcher
	sizeMax        int64
	baseDir        string
	sink           chan fileInfo
}

var SkipFile error = errors.New("Skipped this file")

func (a *fileAggregator) add(path string, info os.FileInfo, err error) error {
	if err != nil {
		return err
	}

	if info.IsDir() {
		base := filepath.Base(path)
		if _, ok := a.ignoreDirs[base]; ok {
			return filepath.SkipDir
		}
		relPath, err := filepath.Rel(a.baseDir, path)
		if err != nil {
			return err
		}
		for _, m := range a.ignoreMatchers {
			if m.Match(relPath) {
				return filepath.SkipDir
			}
		}
	}

	if info.Mode().IsRegular() {
		a.sink <- fileInfo{path, info.Size()}
	}
	return nil
}

func main() {
	cpuProfile := flag.String("cpu_profile", "", "write cpu profile to file")
	ignoreDirs := flag.String("ignore_dirs", ".git,.hg,.svn", "comma separated list of directories to ignore.")
	ignoreFiles := flag.String("ignore_files", ".gitignore,.ignore", "comma separated list of files containing "+
		"git-style ignore patterns. Paths matching the patterns in these files will not be indexed")
	flag.Parse()

	// Tune GOMAXPROCS to match Linux container CPU quota.
	_, _ = maxprocs.Set()

	opts := cmd.OptionsFromFlags()
	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			log.Fatal(err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal(err)
		}
		defer pprof.StopCPUProfile()
	}

	ignoreDirMap := map[string]struct{}{}
	if *ignoreDirs != "" {
		dirs := strings.Split(*ignoreDirs, ",")
		for _, d := range dirs {
			d = strings.TrimSpace(d)
			if d != "" {
				ignoreDirMap[d] = struct{}{}
			}
		}
	}
	var ignoreMatchers []*ignore.Matcher
	if *ignoreFiles != "" {
		files := strings.Split(*ignoreFiles, ",")
		for _, f := range files {
			content, err := ioutil.ReadFile(f)
			if err != nil {
				continue
			}
			m, err := ignore.ParseIgnoreFile(bytes.NewReader(content))
			if err != nil {
				continue
			}
			ignoreMatchers = append(ignoreMatchers, m)
		}
	}
	for _, arg := range flag.Args() {
		opts.RepositoryDescription.Source = arg
		if err := indexArg(arg, *opts, ignoreDirMap, ignoreMatchers); err != nil {
			log.Fatal(err)
		}
	}
}

func indexArg(arg string, opts build.Options, ignore map[string]struct{}, ignoreMatchers []*ignore.Matcher) error {
	dir, err := filepath.Abs(filepath.Clean(arg))
	if err != nil {
		return err
	}

	opts.RepositoryDescription.Name = filepath.Base(dir)
	builder, err := build.NewBuilder(opts)
	if err != nil {
		return err
	}
	// we don't need to check error, since we either already have an error, or
	// we returning the first call to builder.Finish.
	defer builder.Finish() // nolint:errcheck

	comm := make(chan fileInfo, 100)
	agg := fileAggregator{
		ignoreDirs:     ignore,
		ignoreMatchers: ignoreMatchers,
		baseDir:        dir,
		sink:           comm,
		sizeMax:        int64(opts.SizeMax),
	}

	go func() {
		if err := filepath.Walk(dir, agg.add); err != nil {
			log.Fatal(err)
		}
		close(comm)
	}()

	for f := range comm {
		relPath, err := filepath.Rel(dir, f.name)
		if err != nil {
			return err
		}
		skip := false
		for _, m := range ignoreMatchers {
			if m.Match(relPath) {
				log.Printf("Skipping %v", f.name)
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		displayName := strings.TrimPrefix(f.name, dir+"/")
		if f.size > int64(opts.SizeMax) && !opts.IgnoreSizeMax(displayName) {
			if err := builder.Add(zoekt.Document{
				Name:       displayName,
				SkipReason: fmt.Sprintf("document size %d larger than limit %d", f.size, opts.SizeMax),
			}); err != nil {
				return err
			}
			continue
		}
		content, err := ioutil.ReadFile(f.name)
		if err != nil {
			return err
		}

		if err := builder.AddFile(displayName, content); err != nil {
			return err
		}
	}

	return builder.Finish()
}
