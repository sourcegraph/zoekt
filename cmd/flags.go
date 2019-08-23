// Copyright 2019 Google Inc. All rights reserved.
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

package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
)

type largeFilesFlag []string

func (f *largeFilesFlag) String() string {
	s := append([]string{""}, *f...)
	return strings.Join(s, "-large_file ")
}

func (f *largeFilesFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

var (
	sizeMax      = flag.Int("file_limit", 128*1024, "maximum file size")
	trigramMax   = flag.Int("max_trigram_count", 20000, "maximum number of trigrams per document")
	shardLimit   = flag.Int("shard_limit", 100<<20, "maximum corpus size for a shard")
	parallelism  = flag.Int("parallelism", 4, "maximum number of parallel indexing processes.")
	indexDir     = flag.String("index", build.DefaultDir, "directory for search indices")
	version      = flag.Bool("version", false, "Print version number")
	disableCTags = flag.Bool("disable_ctags", false, "If set, ctags will not be called.")
	ctags        = flag.Bool("require_ctags", false, "If set, ctags calls must succeed.")
	largeFiles   = largeFilesFlag{}
)

func init() {
	flag.Var(&largeFiles, "large_file", "A glob pattern where matching files are to be index regardless of their size. You can add multiple patterns by setting this more than once.")
}

func OptionsFromFlags() *build.Options {
	if *version {
		name := filepath.Base(os.Args[0])
		fmt.Printf("%s version %q\n", name, zoekt.Version)
		os.Exit(0)
	}

	opts := &build.Options{
		Parallelism:      *parallelism,
		SizeMax:          *sizeMax,
		ShardMax:         *shardLimit,
		IndexDir:         *indexDir,
		DisableCTags:     *disableCTags,
		CTagsMustSucceed: *ctags,
		LargeFiles:       largeFiles,
		TrigramMax:       *trigramMax,
	}
	opts.SetDefaults()
	return opts
}
