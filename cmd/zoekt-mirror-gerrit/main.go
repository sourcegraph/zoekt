// Copyright 2017 Google Inc. All rights reserved.
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

// This binary fetches all repos of a Gerrit host.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	gerrit "github.com/andygrunwald/go-gerrit"
	"github.com/sourcegraph/zoekt/gitindex"
)

type loggingRT struct {
	http.RoundTripper
}

type closeBuffer struct {
	*bytes.Buffer
}

func (b *closeBuffer) Close() error { return nil }

const debug = false

func (rt *loggingRT) RoundTrip(req *http.Request) (rep *http.Response, err error) {
	if debug {
		log.Println("Req: ", req)
	}
	rep, err = rt.RoundTripper.RoundTrip(req)
	if debug {
		log.Println("Rep: ", rep, err)
	}
	if err == nil {
		body, _ := io.ReadAll(rep.Body)

		rep.Body.Close()
		if debug {
			log.Println("body: ", string(body))
		}
		rep.Body = &closeBuffer{bytes.NewBuffer(body)}
	}
	return rep, err
}

func newLoggingClient() *http.Client {
	return &http.Client{
		Transport: &loggingRT{
			RoundTripper: http.DefaultTransport,
		},
	}
}

func main() {
	dest := flag.String("dest", "", "destination directory")
	namePattern := flag.String("name", "", "only clone repos whose name matches the regexp.")
	excludePattern := flag.String("exclude", "", "don't mirror repos whose names match this regexp.")
	deleteRepos := flag.Bool("delete", false, "delete missing repos")
	httpCrendentialsPath := flag.String("http-credentials", "", "path to a file containing http credentials stored like 'user:password'.")
	active := flag.Bool("active", false, "mirror only active projects")
	flag.Parse()

	if len(flag.Args()) < 1 {
		log.Fatal("must provide URL argument.")
	}

	rootURL, err := url.Parse(flag.Arg(0))
	if err != nil {
		log.Fatalf("url.Parse(): %v", err)
	}

	if *httpCrendentialsPath != "" {
		creds, err := os.ReadFile(*httpCrendentialsPath)
		if err != nil {
			log.Print("Cannot read gerrit http credentials, going Anonymous")
		} else {
			splitCreds := strings.Split(strings.TrimSpace(string(creds)), ":")
			rootURL.User = url.UserPassword(splitCreds[0], splitCreds[1])
		}
	}

	if *dest == "" {
		log.Fatal("must set --dest")
	}

	filter, err := gitindex.NewFilter(*namePattern, *excludePattern)
	if err != nil {
		log.Fatal(err)
	}

	client, err := gerrit.NewClient(rootURL.String(), newLoggingClient())
	if err != nil {
		log.Fatalf("NewClient(%s): %v", rootURL, err)
	}

	info, _, err := client.Config.GetServerInfo()
	if err != nil {
		log.Fatalf("GetServerInfo: %v", err)
	}

	var projectURL string
	for _, s := range []string{"http", "anonymous http"} {
		if schemeInfo, ok := info.Download.Schemes[s]; ok {
			projectURL = schemeInfo.URL
			if s == "http" && schemeInfo.IsAuthRequired {
				projectURL = addPassword(projectURL, rootURL.User)
				// remove "/a/" prefix needed for API call with basic auth but not with git command → cleaner repo name
				projectURL = strings.Replace(projectURL, "/a/${project}", "/${project}", 1)
			}
			break
		}
	}
	if projectURL == "" {
		log.Fatalf("project URL is empty, got Schemes %#v", info.Download.Schemes)
	}

	projects := make(map[string]gerrit.ProjectInfo)
	skip := 0
	for {
		page, _, err := client.Projects.ListProjects(&gerrit.ProjectOptions{Skip: strconv.Itoa(skip)})
		if err != nil {
			log.Fatalf("ListProjects: %v", err)
		}

		if len(*page) == 0 {
			break
		}

		for k, v := range *page {
			if !*active || "ACTIVE" == v.State {
				projects[k] = v
			}
			skip = skip + 1
		}
	}

	for k, v := range projects {
		if !filter.Include(k) {
			continue
		}

		cloneURL, err := url.Parse(strings.Replace(projectURL, "${project}", k, 1))
		if err != nil {
			log.Fatalf("url.Parse: %v", err)
		}

		name := filepath.Join(cloneURL.Host, cloneURL.Path)
		config := map[string]string{
			"zoekt.name":           name,
			"zoekt.gerrit-project": k,
			"zoekt.gerrit-host":    anonymousURL(rootURL),
			"zoekt.archived":       marshalBool(v.State == "READ_ONLY"),
			"zoekt.public":         marshalBool(v.State != "HIDDEN"),
		}

		for _, wl := range v.WebLinks {
			// default gerrit gitiles config is named browse, and does not include
			// root domain name in it. Cheating.
			switch wl.Name {
			case "browse":
				config["zoekt.web-url"] = fmt.Sprintf("%s://%s%s", rootURL.Scheme,
					rootURL.Host, wl.URL)
				config["zoekt.web-url-type"] = "gitiles"
			default:
				config["zoekt.web-url"] = wl.URL
				config["zoekt.web-url-type"] = wl.Name
			}
		}

		if dest, err := gitindex.CloneRepo(*dest, name, cloneURL.String(), config); err != nil {
			log.Fatalf("CloneRepo: %v", err)
		} else {
			fmt.Println(dest)
		}
	}
	if *deleteRepos {
		if err := deleteStaleRepos(*dest, filter, projects, projectURL); err != nil {
			log.Fatalf("deleteStaleRepos: %v", err)
		}
	}
}

func deleteStaleRepos(destDir string, filter *gitindex.Filter, repos map[string]gerrit.ProjectInfo, projectURL string) error {
	u, err := url.Parse(strings.Replace(projectURL, "${project}", "", 1))
	if err != nil {
		return err
	}

	names := map[string]struct{}{}
	for name := range repos {
		u, err := url.Parse(strings.Replace(projectURL, "${project}", name, 1))
		if err != nil {
			return err
		}
		names[filepath.Join(u.Host, u.Path)+".git"] = struct{}{}
	}

	if err := gitindex.DeleteRepos(destDir, u, names, filter); err != nil {
		log.Fatalf("deleteRepos: %v", err)
	}
	return nil
}

func marshalBool(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func anonymousURL(u *url.URL) string {
	anon := *u
	anon.User = nil
	return anon.String()
}

func addPassword(u string, user *url.Userinfo) string {
	password, _ := user.Password()
	username := user.Username()
	return strings.Replace(u, fmt.Sprintf("://%s@", username), fmt.Sprintf("://%s:%s@", username, password), 1)
}
