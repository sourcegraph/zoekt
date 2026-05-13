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

// Command zoekt-git-clone fetches all repos of a user or organization and clones
// them.  It is strongly recommended to get a personal API token from
// https://github.com/settings/tokens, save the token in a file, and
// point the --token option to it.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sourcegraph/zoekt/gitindex"
)

func main() {
	dest := flag.String("dest", "", "destination directory")
	nameFlag := flag.String("name", "", "name of repository")
	repoIDFlag := flag.Uint("repoid", 0, "id of repository")
	flag.Parse()

	if *dest == "" {
		log.Fatal("must set --dest")
	}
	if len(flag.Args()) == 0 {
		log.Fatal("must provide URL")
	}
	u, err := url.Parse(flag.Arg(0))
	if err != nil {
		log.Fatalf("url.Parse: %v", err)
	}

	name := *nameFlag
	if name == "" {
		name = filepath.Join(u.Host, u.Path)
		name = strings.TrimSuffix(name, ".git")
	}

	destDir := filepath.Dir(filepath.Join(*dest, name))
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		log.Fatal(err)
	}

	config := map[string]string{
		"zoekt.name": name,
	}

	repoID := *repoIDFlag
	if repoID != 0 {
		config["zoekt.repoid"] = strconv.FormatUint(uint64(repoID), 10)
	}

	destRepo, err := gitindex.CloneRepo(destDir, filepath.Base(name), u.String(), config)
	if err != nil {
		log.Fatalf("CloneRepo: %v", err)
	}
	if destRepo != "" {
		fmt.Println(destRepo)
	}
}
