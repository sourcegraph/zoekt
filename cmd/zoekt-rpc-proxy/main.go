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

// Command zoekt-webserver responds to search queries, using an index generated
// by another program such as zoekt-indexserver.

package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/rpc"
)

func main() {
	listen := flag.String("listen", ":8080", "listen on this address")
	upstream := flag.String("upstream", ":6070", "zoekt-webserver RPC upstream address")

	flag.Parse()

	// Same options as the sourcegraph frontend.
	opts := zoekt.SearchOptions{
		MaxWallTime:            1500 * time.Millisecond,
		ShardMaxMatchCount:     100,
		TotalMaxMatchCount:     100,
		ShardMaxImportantMatch: 15,
		TotalMaxImportantMatch: 25,
		MaxDocDisplayCount:     60,
	}

	cli := rpc.Client(*upstream)
	defer cli.Close()

	http.ListenAndServe(*listen, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := query.Substring{Pattern: r.URL.Query().Get("q")}
		began := time.Now()
		res, err := cli.Search(r.Context(), &q, &opts)
		took := time.Since(began)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		log.Printf("q: %q, took: %s, stats: %+v", q.Pattern, took, &res.Stats)
	}))
}
