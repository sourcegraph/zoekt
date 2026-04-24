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

// Command zoekt-index indexes a directory of files.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"

	"go.uber.org/automaxprocs/maxprocs"

	"github.com/sourcegraph/zoekt/cmd"
	"github.com/sourcegraph/zoekt/index"
)

type fileInfo struct {
	name string
	size int64
}

type fileAggregator struct {
	ignoreDirs map[string]struct{}
	sizeMax    int64
	sink       chan fileInfo
}

func (a *fileAggregator) add(path string, info os.FileInfo, err error) error {
	if err != nil {
		return err
	}

	if info.IsDir() {
		base := filepath.Base(path)
		if _, ok := a.ignoreDirs[base]; ok {
			return filepath.SkipDir
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
	metaFile := flag.String("meta", "", "path to .meta JSON file with repository description")
	flag.Parse()

	if flag.NArg() == 0 {
		fmt.Fprintf(flag.CommandLine.Output(), "USAGE: %s [options] PATHS...\n", filepath.Base(os.Args[0]))
		fmt.Fprintln(flag.CommandLine.Output(), "Options:")
		flag.PrintDefaults()
		os.Exit(1)
	}

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

	if *metaFile != "" {
		// Read and parse the .meta JSON file into opts.RepositoryDescription
		data, err := os.ReadFile(*metaFile)
		if err != nil {
			log.Fatalf("failed to read .meta file %s: %v", *metaFile, err)
		}
		if err := json.Unmarshal(data, &opts.RepositoryDescription); err != nil {
			log.Fatalf("failed to decode .meta file %s: %v", *metaFile, err)
		}
	}

	for _, arg := range flag.Args() {
		opts.RepositoryDescription.Source = arg
		if err := indexArg(arg, *opts, ignoreDirMap); err != nil {
			log.Fatal(err)
		}
	}
}

func indexArg(arg string, opts index.Options, ignore map[string]struct{}) error {
	dir, err := filepath.Abs(filepath.Clean(arg))
	if err != nil {
		return err
	}

	if opts.RepositoryDescription.Name == "" {
		opts.RepositoryDescription.Name = filepath.Base(dir)
	}
	builder, err := index.NewBuilder(opts)
	if err != nil {
		return err
	}
	// we don't need to check error, since we either already have an error, or
	// we returning the first call to builder.Finish.
	defer builder.Finish() // nolint:errcheck

	comm := make(chan fileInfo, 100)
	agg := fileAggregator{
		ignoreDirs: ignore,
		sink:       comm,
		sizeMax:    int64(opts.SizeMax),
	}

	go func() {
		if err := filepath.Walk(dir, agg.add); err != nil {
			log.Fatal(err)
		}
		close(comm)
	}()

	for f := range comm {
		displayName := strings.TrimPrefix(f.name, dir+"/")
		if f.size > int64(opts.SizeMax) && !opts.IgnoreSizeMax(displayName) {
			if err := builder.Add(index.Document{
				Name:       displayName,
				SkipReason: index.SkipReasonTooLarge,
			}); err != nil {
				return err
			}
			continue
		}
		content, err := os.ReadFile(f.name)
		if err != nil {
			return err
		}

		if err := builder.AddFile(displayName, content); err != nil {
			return err
		}
	}

	return builder.Finish()
}
