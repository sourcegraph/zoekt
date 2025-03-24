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

// Command zoekt-test compares the zoekt results with raw substring search.
package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/search"
)

func readTree(dir string) (map[string][]byte, error) {
	var fns []string

	add := func(path string, info os.FileInfo, err error) error {
		if !info.Mode().IsRegular() {
			return nil
		}

		fns = append(fns, path)
		return nil
	}
	if err := filepath.Walk(dir, add); err != nil {
		return nil, err
	}

	res := map[string][]byte{}
	for _, n := range fns {
		c, err := os.ReadFile(n)
		if err != nil {
			return nil, err
		}

		strip := strings.TrimPrefix(n, dir+"/")
		res[strip] = c
	}
	return res, nil
}

func compare(dir, patfile string, caseSensitive bool) error {
	indexDir, err := os.MkdirTemp("", "")
	if err != nil {
		return err
	}
	defer os.RemoveAll(indexDir)

	var opts index.Options
	opts.SetDefaults()
	opts.IndexDir = indexDir

	fileContents, err := readTree(dir)
	if err != nil {
		return err
	}
	if len(fileContents) == 0 {
		return fmt.Errorf("no contents")
	}

	builder, err := index.NewBuilder(opts)
	if err != nil {
		return err
	}
	for k, v := range fileContents {
		if err := builder.AddFile(k, v); err != nil {
			return err
		}
	}
	if err := builder.Finish(); err != nil {
		return err
	}

	if !caseSensitive {
		for k, v := range fileContents {
			fileContents[k] = toLower(v)
		}
	}

	f, err := os.Open(patfile)
	if err != nil {
		return err
	}
	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		t := scanner.Text()
		if len(t) < 3 {
			continue
		}
		q := &query.Substring{
			Pattern:       t,
			CaseSensitive: caseSensitive,
		}

		zFiles := map[string]struct{}{}
		rFiles := map[string]struct{}{}

		// search engine results
		var opts zoekt.SearchOptions
		res, err := searcher.Search(context.Background(), q, &opts)
		if err != nil {
			return err
		}

		for _, f := range res.Files {
			zFiles[f.FileName] = struct{}{}
		}

		// raw search
		needle := []byte(t)
		if !caseSensitive {
			needle = toLower(needle)
		}

		for k, v := range fileContents {
			if bytes.Contains(v, needle) {
				rFiles[k] = struct{}{}
			}
		}

		if !reflect.DeepEqual(zFiles, rFiles) {
			var add, del []string
			for k := range zFiles {
				if _, ok := rFiles[k]; !ok {
					del = append(del, k)
				}
			}
			for k := range rFiles {
				if _, ok := zFiles[k]; !ok {
					add = append(add, k)
				}
			}
			sort.Strings(add)
			sort.Strings(del)
			log.Printf("pattern %q, add %v, del %v", t, add, del)
		}
	}
	return nil
}

var (
	memprofile = flag.String("mem_profile", "", "write memory profile to `file`")
	cpuprofile = flag.String("cpu_profile", "", "write cpu profile to `file`")
)

func testLoadIndexDir(indexDir string) {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	start := time.Now()
	s, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		return
	}
	duration := time.Since(start)
	runtime.GC()
	runtime.ReadMemStats(&b)
	log.Printf("%s loaded in %d ms, additional memory consumption: %d MiB", s.String(), duration.Milliseconds(), (b.Alloc-a.Alloc)/1024/1024)

	if *memprofile != "" {
		f, err := os.Create(*memprofile)
		if err != nil {
			log.Fatal("could not create memory profile: ", err)
		}
		defer f.Close() // error handling omitted for example
		runtime.GC()    // get up-to-date statistics
		if err := pprof.WriteHeapProfile(f); err != nil {
			log.Fatal("could not write memory profile: ", err)
		}
	}
}

func main() {
	repo := flag.String("repo", "", "repository to search")
	indexDir := flag.String("indexDir", "", "indexDir to load and exit")
	caseSensitive := flag.Bool("case", false, "case sensitive")

	flag.Parse()

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal("could not create CPU profile: ", err)
		}
		defer f.Close() // error handling omitted for example
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal("could not start CPU profile: ", err)
		}
		defer pprof.StopCPUProfile()
	}

	if *indexDir != "" {
		testLoadIndexDir(*indexDir)
		return
	}
	if len(flag.Args()) == 0 {
		fmt.Fprintf(os.Stderr, "pattern file is missing.\n")
		flag.Usage()
		os.Exit(2)
	}
	input := flag.Arg(0)

	if err := compare(*repo, input, *caseSensitive); err != nil {
		log.Fatal(err)
	}
}

func toLower(in []byte) []byte {
	out := make([]byte, len(in))
	for i, c := range in {
		if c >= 'A' && c <= 'Z' {
			c = c - 'A' + 'a'
		}
		out[i] = c
	}
	return out
}
