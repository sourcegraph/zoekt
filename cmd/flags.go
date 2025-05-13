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
	"strconv"

	"github.com/sourcegraph/zoekt/index"
)

var (
	version = flag.Bool("version", false, "Print version number")
	opts    = &index.Options{}
)

func init() {
	opts.Flags(flag.CommandLine)
}

func OptionsFromFlags() *index.Options {
	if *version {
		name := filepath.Base(os.Args[0])
		fmt.Printf("%s version %q\n", name, index.Version)
		os.Exit(0)
	}

	opts.SetDefaults()
	
	// Ensure any tenant ID and repo ID from environment variables are set
	if tenantID := os.Getenv("SRC_TENANT_ID"); tenantID != "" {
		id, err := strconv.Atoi(tenantID)
		if err == nil {
			opts.TenantID = id
		}
	}
	
	if repoID := os.Getenv("SRC_REPO_ID"); repoID != "" {
		id, err := strconv.ParseUint(repoID, 10, 32)
		if err == nil {
			opts.RepoID = uint32(id)
		}
	}
	
	return opts
}
