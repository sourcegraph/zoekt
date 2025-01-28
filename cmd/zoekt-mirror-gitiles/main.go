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

// This binary fetches all repos of a Gitiles host.  It does double
// duty for other "simple" web hosts
package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"

	"github.com/sourcegraph/zoekt/internal/gitindex"
)

type crawlTarget struct {
	cloneURL   string
	webURL     string
	webURLType string
}

type hostCrawler func(*url.URL, func(string) bool) (map[string]*crawlTarget, error)

func main() {
	dest := flag.String("dest", "", "destination directory")
	namePattern := flag.String("name", "", "only clone repos whose name matches the regexp.")
	excludePattern := flag.String("exclude", "", "don't mirror repos whose names match this regexp.")
	hostType := flag.String("type", "gitiles", "which webserver to crawl. Choices: gitiles, cgit")
	flag.Parse()

	if len(flag.Args()) < 1 {
		log.Fatal("must provide URL argument.")
	}

	var crawler hostCrawler
	switch *hostType {
	case "gitiles":
		crawler = getGitilesRepos
	case "cgit":
		crawler = getCGitRepos
	default:
		log.Fatalf("unknown host type %q", *hostType)
	}

	rootURL, err := url.Parse(flag.Arg(0))
	if err != nil {
		log.Fatalf("url.Parse(): %v", err)
	}

	if *dest == "" {
		log.Fatal("must set --dest")
	}

	if err := os.MkdirAll(filepath.Join(*dest, rootURL.Host, rootURL.Path), 0o755); err != nil {
		log.Fatal(err)
	}

	filter, err := gitindex.NewFilter(*namePattern, *excludePattern)
	if err != nil {
		log.Fatal(err)
	}

	repos, err := crawler(rootURL, filter.Include)
	if err != nil {
		log.Fatal(err)
	}

	for nm, target := range repos {
		// For git.savannah.gnu.org, this puts an ugly "CGit"
		// path component into the name. However, it's
		// possible that there are multiple, different CGit pages
		// on the host, so we have to keep it.
		fullName := filepath.Join(rootURL.Host, rootURL.Path, nm)
		config := map[string]string{
			"zoekt.web-url":      target.webURL,
			"zoekt.web-url-type": target.webURLType,
			"zoekt.name":         fullName,
		}

		dest, err := gitindex.CloneRepo(*dest, fullName, target.cloneURL, config)
		if err != nil {
			log.Fatal(err)
		}
		if dest != "" {
			fmt.Println(dest)
		}
	}
}
