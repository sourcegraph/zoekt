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

// This binary fetches all repos of a project, and of a specific type, in case
// these are specified, and clones them. By default it fetches and clones all
// existing repos.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gfleury/go-bitbucket-v1"

	"github.com/google/zoekt/gitindex"
)

func main() {
	dest := flag.String("dest", "", "destination directory")
	serverUrl := flag.String("url", "", "BitBucket Server url")
	disableTLS := flag.Bool("disable-tls", false, "disables TLS verification")
	credentialsFile := flag.String("credentials", ".bitbucket-credentials", "file holding BitBucket Server credentials")
	project := flag.String("project", "", "project to mirror")
	deleteRepos := flag.Bool("delete", false, "delete missing repos")
	namePattern := flag.String("name", "", "only clone repos whose name matches the given regexp.")
	excludePattern := flag.String("exclude", "", "don't mirror repos whose names match this regexp.")
	projectType := flag.String("type", "", "only clone repos whose type matches the given string. "+
		"Type can be either NORMAl or PERSONAL. Clones projects of both types if not set.")
	flag.Parse()

	if *serverUrl == "" {
		log.Fatal("must set --url")
	}

	rootURL, err := url.Parse(*serverUrl)
	if err != nil {
		log.Fatalf("url.Parse(): %v", err)
	}

	if *dest == "" {
		log.Fatal("must set --dest")
	}

	if *projectType != "" && !IsValidProjectType(*projectType) {
		log.Fatal("type should be either NORMAL or PERSONAL")
	}

	destDir := filepath.Join(*dest, rootURL.Host)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		log.Fatal(err)
	}

	username := ""
	password := ""
	if *credentialsFile == "" {
		log.Fatal("must set --credentials")
	} else {
		content, err := ioutil.ReadFile(*credentialsFile)
		if err != nil {
			log.Fatal(err)
		}
		credentials := strings.Fields(string(content))
		username, password = credentials[0], credentials[1]
	}

	basicAuth := bitbucketv1.BasicAuth{UserName: username, Password: password}
	ctx, cancel := context.WithTimeout(context.Background(), 120000*time.Millisecond)
	ctx = context.WithValue(ctx, bitbucketv1.ContextBasicAuth, basicAuth)
	defer cancel()

	apiPath, err := url.Parse("/rest")
	if err != nil {
		log.Fatal(err)
	}

	apiBaseURL := rootURL.ResolveReference(apiPath).String()

	var config *bitbucketv1.Configuration
	if *disableTLS {
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		httpClient := &http.Client{
			Transport: tr,
		}
		httpClientConfig := func(configs *bitbucketv1.Configuration) {
			configs.HTTPClient = httpClient
		}
		config = bitbucketv1.NewConfiguration(apiBaseURL, httpClientConfig)
	} else {
		config = bitbucketv1.NewConfiguration(apiBaseURL)
	}
	client := bitbucketv1.NewAPIClient(ctx, config)

	var repos []bitbucketv1.Repository

	if *project != "" {
		repos, err = getProjectRepos(*client, *project)
	} else {
		repos, err = getAllRepos(*client)
	}

	if err != nil {
		log.Fatal(err)
	}

	filter, err := gitindex.NewFilter(*namePattern, *excludePattern)
	if err != nil {
		log.Fatal(err)
	}

	trimmed := repos[:0]
	for _, r := range repos {
		if filter.Include(r.Slug) && (*projectType == "" || r.Project.Type == *projectType) {
			trimmed = append(trimmed, r)
		}
	}
	repos = trimmed

	if err := cloneRepos(destDir, rootURL.Host, repos, password); err != nil {
		log.Fatalf("cloneRepos: %v", err)
	}

	if *deleteRepos {
		if err := deleteStaleRepos(*dest, filter, repos); err != nil {
			log.Fatalf("deleteStaleRepos: %v", err)
		}
	}
}

func deleteStaleRepos(destDir string, filter *gitindex.Filter, repos []bitbucketv1.Repository) error {
	var baseURL string
	if len(repos) > 0 {
		baseURL = repos[0].Links.Self[0].Href
	} else {
		return nil
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return err
	}
	u.Path = ""

	names := map[string]struct{}{}
	for _, r := range repos {
		names[filepath.Join(u.Host, r.Project.Key, r.Slug+".git")] = struct{}{}
	}

	if err := gitindex.DeleteRepos(destDir, u, names, filter); err != nil {
		log.Fatalf("deleteRepos: %v", err)
	}
	return nil
}

func IsValidProjectType(projectType string) bool {
	switch projectType {
	case
		"NORMAL",
		"PERSONAL":
		return true
	}
	return false
}

func getAllRepos(client bitbucketv1.APIClient) ([]bitbucketv1.Repository, error) {
	var allRepos []bitbucketv1.Repository
	opts := map[string]interface{}{
		"limit": 1000,
		"start": 0,
	}

	for {
		resp, err := client.DefaultApi.GetRepositories_19(opts)

		if err != nil {
			return nil, err
		}

		repos, err := bitbucketv1.GetRepositoriesResponse(resp)

		if err != nil {
			return nil, err
		}

		if len(repos) == 0 {
			break
		}

		opts["start"] = opts["start"].(int) + opts["limit"].(int)

		allRepos = append(allRepos, repos...)
	}
	return allRepos, nil
}

func getProjectRepos(client bitbucketv1.APIClient, projectName string) ([]bitbucketv1.Repository, error) {
	var allRepos []bitbucketv1.Repository
	opts := map[string]interface{}{
		"limit": 1000,
		"start": 0,
	}

	for {
		resp, err := client.DefaultApi.GetRepositoriesWithOptions(projectName, opts)
		if err != nil {
			return nil, err
		}

		repos, err := bitbucketv1.GetRepositoriesResponse(resp)
		if err != nil {
			return nil, err
		}

		if len(repos) == 0 {
			break
		}

		opts["start"] = opts["start"].(int) + opts["limit"].(int)

		allRepos = append(allRepos, repos...)
	}
	return allRepos, nil
}

func cloneRepos(destDir string, host string, repos []bitbucketv1.Repository, password string) error {
	for _, r := range repos {
		fullName := filepath.Join(r.Project.Key, r.Slug)
		config := map[string]string{
			"zoekt.web-url-type": "bitbucket-server",
			"zoekt.web-url":      r.Links.Self[0].Href,
			"zoekt.name":         filepath.Join(host, fullName),
		}

		httpsCloneUrl := ""
		for _, cloneUrl := range r.Links.Clone {
			// In fact, this is an https url, i.e. there's no separate Name for https.
			if cloneUrl.Name == "http" {
				s := strings.Split(cloneUrl.Href, "@")
				httpsCloneUrl = s[0] + ":" + password + "@" + s[1]
			}
		}

		if httpsCloneUrl != "" {
			dest, err := gitindex.CloneRepo(destDir, fullName, httpsCloneUrl, config)
			if err != nil {
				return err
			}
			if dest != "" {
				fmt.Println(dest)
			}
		}
	}

	return nil
}
