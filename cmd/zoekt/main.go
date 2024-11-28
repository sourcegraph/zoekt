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
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
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
			fmt.Printf("%s%s%s\n", r, f.FileName, addTabIfNonEmpty(f.Debug))
			continue
		}

		for _, m := range f.LineMatches {
			fmt.Printf("%s%s:%d:%s%s\n", r, f.FileName, m.LineNumber, m.Line, addTabIfNonEmpty(f.Debug))
		}
	}
}

func addTabIfNonEmpty(s string) string {
	if s != "" {
		return "\t" + s
	}
	return s
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

func profile(path string, duration time.Duration, start func(io.Writer) (stop func())) func() bool {
	if path == "" {
		return func() bool { return false }
	}

	f, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}

	t := time.Now()
	stop := start(f)

	return func() bool {
		if time.Since(t) < duration {
			return true
		}
		stop()
		f.Close()
		return false
	}
}

func startCPUProfile(path string, duration time.Duration) func() bool {
	return profile(path, duration, func(w io.Writer) func() {
		if err := pprof.StartCPUProfile(w); err != nil {
			log.Fatal(err)
		}

		return pprof.StopCPUProfile
	})
}

func startFullProfile(path string, duration time.Duration) func() bool {
	return profile(path, duration, func(w io.Writer) func() {
		stop := fgprof.Start(w, fgprof.FormatPprof)

		return func() {
			if err := stop(); err != nil {
				log.Fatal(err)
			}
		}
	})
}

// experimental support for symbol queries. We just convert substring queries
// into symbol queries. Needs to run after query.ExpandFileContent
func toSymbolQuery(q query.Q) query.Q {
	return query.Map(q, func(q query.Q) query.Q {
		switch s := q.(type) {
		case *query.Substring:
			if s.Content {
				return &query.Symbol{Expr: s}
			}
		case *query.Regexp:
			if s.Content {
				return &query.Symbol{Expr: s}
			}
		}
		return q
	})
}

func main() {
	shard := flag.String("shard", "", "search in a specific shard")
	index := flag.String("index_dir",
		filepath.Join(os.Getenv("HOME"), ".zoekt"), "search for index files in `directory`")
	cpuProfile := flag.String("cpu_profile", "", "write cpu profile to `file`")
	fullProfile := flag.String("full_profile", "", "write full profile to `file`")
	profileTime := flag.Duration("profile_time", time.Second, "run this long to gather stats.")
	debug := flag.Bool("debug", false, "show debugscore output.")
	verbose := flag.Bool("v", false, "print some background data")
	withRepo := flag.Bool("r", false, "print the repo before the file name")
	list := flag.Bool("l", false, "print matching filenames only")
	sym := flag.Bool("sym", false, "do experimental symbol search")

	flag.Usage = func() {
		name := os.Args[0]
		fmt.Fprintf(os.Stderr, "Usage:\n\n  %s [option] QUERY\n"+
			"for example\n\n  %s byte file:java -file:test\n\n", name, name)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n")
	}
	flag.Parse()

	if len(flag.Args()) == 0 {
		fmt.Fprintf(os.Stderr, "Pattern is missing.\n")
		flag.Usage()
		os.Exit(2)
	}
	pat := strings.Join(flag.Args(), " ")

	if !*verbose {
		log.SetOutput(io.Discard)
	}

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

	q, err := query.Parse(pat)
	if err != nil {
		log.Fatal(err)
	}
	q = query.Map(q, query.ExpandFileContent)
	if *sym {
		q = toSymbolQuery(q)
	}
	q = query.Simplify(q)
	if *verbose {
		log.Println("query:", q)
	}

	sOpts := zoekt.SearchOptions{
		DebugScore: *debug,
	}
	sres, err := searcher.Search(context.Background(), q, &sOpts)
	if err != nil {
		log.Fatal(err)
	}

	// If profiling, do it another time so we measure with
	// warm caches.
	for run := startCPUProfile(*cpuProfile, *profileTime); run(); {
		sres, _ = searcher.Search(context.Background(), q, &sOpts)
	}
	for run := startFullProfile(*fullProfile, *profileTime); run(); {
		sres, _ = searcher.Search(context.Background(), q, &sOpts)
	}

	displayMatches(sres.Files, pat, *withRepo, *list)
	if *verbose {
		log.Printf("stats: %#v", sres.Stats)
	}
}
