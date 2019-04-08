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

type largeFilesFlag []string

func (f *largeFilesFlag) String() string {
	s := append([]string{""}, *f...)
	return strings.Join(s, "-large_file ")
}

func (f *largeFilesFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func main() {
	var cpuProfile = flag.String("cpu_profile", "", "write cpu profile to file")
	var sizeMax = flag.Int("file_limit", 128*1024, "maximum file size")
	var shardLimit = flag.Int("shard_limit", 100<<20, "maximum corpus size for a shard")
	var parallelism = flag.Int("parallelism", 4, "maximum number of parallel indexing processes.")

	largeFiles := largeFilesFlag{}
	flag.Var(&largeFiles, "large_file", "A glob pattern where matching files are to be index regardless of their size.")

	ignoreDirs := flag.String("ignore_dirs", ".git,.hg,.svn", "comma separated list of directories to ignore.")
	indexDir := flag.String("index", build.DefaultDir, "directory for search indices")
	flag.Parse()

	opts := build.Options{
		Parallelism: *parallelism,
		SizeMax:     *sizeMax,
		ShardMax:    *shardLimit,
		IndexDir:    *indexDir,
		LargeFiles:  largeFiles,
	}
	opts.SetDefaults()
	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
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
	for _, arg := range flag.Args() {
		opts.RepositoryDescription.Source = arg
		if err := indexArg(arg, opts, ignoreDirMap); err != nil {
			log.Fatal(err)
		}
	}
}

func indexArg(arg string, opts build.Options, ignore map[string]struct{}) error {
	dir, err := filepath.Abs(filepath.Clean(arg))
	if err != nil {
		return err
	}

	opts.RepositoryDescription.Name = filepath.Base(dir)
	builder, err := build.NewBuilder(opts)
	if err != nil {
		return err
	}

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
			builder.Add(zoekt.Document{
				Name:       displayName,
				SkipReason: fmt.Sprintf("document size %d larger than limit %d", f.size, opts.SizeMax),
			})
			continue
		}
		content, err := ioutil.ReadFile(f.name)
		if err != nil {
			return err
		}

		builder.AddFile(displayName, content)
	}

	return builder.Finish()
}
