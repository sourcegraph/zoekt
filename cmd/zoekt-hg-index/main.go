// Copyright 2020 Google Inc. All rights reserved.
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

// zoekt-hg-index provides bare-bones Mercurial indexing
package main

import (
	"flag"
	"fmt"
	"log"
	"path/filepath"

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
	"github.com/google/zoekt/cmd"

	"go.uber.org/automaxprocs/maxprocs"
	"humungus.tedunangst.com/r/gerc"
)

func main() {
	revisionStr := flag.String("revision", "", "hg revision to index")
	flag.Parse()
	_, _ = maxprocs.Set()
	opts := cmd.OptionsFromFlags()

	if len(flag.Args()) < 1 {
		log.Fatal("hg repo directory argument missing")
	}
	dir, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	opts.RepositoryDescription.Name = dir

	if err := indexHg(dir, *revisionStr, opts); err != nil {
		log.Fatal(err)
	}
}

func indexHg(dir, rev string, opts *build.Options) error {
	r, err := gerc.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	builder, err := build.NewBuilder(*opts)
	if err != nil {
		return err
	}
	// we don't need to check error, since we either already have an error, or
	// we returning the first call to builder.Finish.
	defer builder.Finish() // nolint:errcheck

	mfs, err := r.GetFiles(gerc.FilesArgs{
		Revision: rev,
	})
	if err != nil {
		return fmt.Errorf("GetFiles %v", err)
	}

	for _, mf := range mfs {
		fd := gerc.FileDataArgs{
			Filename: mf.Name,
			Revision: rev,
		}
		content, err := r.GetFileData(fd)
		if err != nil {
			return fmt.Errorf("GetFileData %v", err)
		}
		if err := builder.Add(zoekt.Document{
			Name:    mf.Name,
			Content: content,
		}); err != nil {
			return err
		}
	}
	return builder.Finish()
}
