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
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/pprof"
	"time"

	"github.com/felixge/fgprof"
	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/shards"
)

func displayMatches(files []zoekt.FileMatch, pat string, withRepo bool, list bool) {
	for _, f := range files {
		r := ""
		if withRepo {
			r = f.Repository + "/"
		}
		if list {
			fmt.Printf("%s%s\n", r, f.FileName)
			continue
		}

		for _, m := range f.LineMatches {
			fmt.Printf("%s%s:%d:%s\n", r, f.FileName, m.LineNumber, m.Line)
		}
	}
}

func loadShard(fn string, verbose bool) (zoekt.Searcher, error) {
	f, err := os.Open(fn)
	if err != nil {
		return nil, err
	}

	iFile, err := zoekt.NewIndexFile(f)
	if err != nil {
		return nil, err
	}

	s, err := zoekt.NewSearcher(iFile)
	if err != nil {
		iFile.Close()
		return nil, fmt.Errorf("NewSearcher(%s): %v", fn, err)
	}

	if verbose {
		repo, index, err := zoekt.ReadMetadata(iFile)
		if err != nil {
			iFile.Close()
			return nil, fmt.Errorf("ReadMetadata(%s): %v", fn, err)
		}
		log.Printf("repo metadata: %#v", repo)
		log.Printf("index metadata: %#v", index)
	}

	return s, nil
}

func main() {
	shard := flag.String("shard", "", "search in a specific shard")
	index := flag.String("index_dir",
		filepath.Join(os.Getenv("HOME"), ".zoekt"), "search for index files in `directory`")
	cpuProfile := flag.String("cpu_profile", "", "write cpu profile to `file`")
	fullProfile := flag.String("full_profile", "", "write full profile to `file`")
	profileTime := flag.Duration("profile_time", time.Second, "run this long to gather stats.")
	verbose := flag.Bool("v", false, "print some background data")
	withRepo := flag.Bool("r", false, "print the repo before the file name")
	list := flag.Bool("l", false, "print matching filenames only")

	flag.Usage = func() {
		name := os.Args[0]
		fmt.Fprintf(os.Stderr, "Usage:\n\n  %s [option] QUERY\n"+
			"for example\n\n  %s 'byte file:java -file:test'\n\n", name, name)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n")
	}
	flag.Parse()

	if len(flag.Args()) == 0 {
		fmt.Fprintf(os.Stderr, "Pattern is missing.\n")
		flag.Usage()
		os.Exit(2)
	}
	pat := flag.Arg(0)

	var searcher zoekt.Searcher
	var err error
	if *shard != "" {
		searcher, err = loadShard(*shard, *verbose)
	} else {
		searcher, err = shards.NewDirectorySearcher(*index)
	}

	if err != nil {
		log.Fatal(err)
	}

	query, err := query.Parse(pat)
	if err != nil {
		log.Fatal(err)
	}
	if *verbose {
		log.Println("query:", query)
	}

	var sOpts zoekt.SearchOptions
	sres, err := searcher.Search(context.Background(), query, &sOpts)
	if *cpuProfile != "" {
		// If profiling, do it another time so we measure with
		// warm caches.
		f, err := os.Create(*cpuProfile)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		if *verbose {
			log.Println("Displaying matches...")
		}

		t := time.Now()
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal(err)
		}
		for {
			sres, _ = searcher.Search(context.Background(), query, &sOpts)
			if time.Since(t) > *profileTime {
				break
			}
		}
		pprof.StopCPUProfile()
	}

	if *fullProfile != "" {
		// If profiling, do it another time so we measure with
		// warm caches.
		f, err := os.Create(*fullProfile)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		if *verbose {
			log.Println("Displaying matches...")
		}

		t := time.Now()
		stopProfile := fgprof.Start(f, fgprof.FormatPprof)
		for {
			sres, _ = searcher.Search(context.Background(), query, &sOpts)
			if time.Since(t) > *profileTime {
				break
			}
		}
		if err := stopProfile(); err != nil {
			log.Fatal(err)
		}
	}

	if err != nil {
		log.Fatal(err)
	}

	displayMatches(sres.Files, pat, *withRepo, *list)
	if *verbose {
		log.Printf("stats: %#v", sres.Stats)
	}
}
