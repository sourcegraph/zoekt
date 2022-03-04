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

// This binary fetches all repos of a user or organization and clones
// them.  It is strongly recommended to get a personal API token from
// https://github.com/settings/tokens, save the token in a file, and
// point the --token option to it.
package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/go-github/v27/github"
	"golang.org/x/oauth2"

	"github.com/google/zoekt/gitindex"
)

type topicsFlag []string

func (f *topicsFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *topicsFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

type reposFilters struct {
	topics        []string
	excludeTopics []string
	noArchived    *bool
}

func main() {
	dest := flag.String("dest", "", "destination directory")
	githubURL := flag.String("url", "", "GitHub Enterprise url. If not set github.com will be used as the host.")
	org := flag.String("org", "", "organization to mirror")
	user := flag.String("user", "", "user to mirror")
	token := flag.String("token",
		filepath.Join(os.Getenv("HOME"), ".github-token"),
		"file holding API token.")
	forks := flag.Bool("forks", false, "also mirror forks.")
	deleteRepos := flag.Bool("delete", false, "delete missing repos")
	namePattern := flag.String("name", "", "only clone repos whose name matches the given regexp.")
	excludePattern := flag.String("exclude", "", "don't mirror repos whose names match this regexp.")
	topics := topicsFlag{}
	flag.Var(&topics, "topic", "only clone repos whose have one of given topics. You can add multiple topics by setting this more than once.")
	excludeTopics := topicsFlag{}
	flag.Var(&excludeTopics, "exclude_topic", "don't clone repos whose have one of given topics. You can add multiple topics by setting this more than once.")
	noArchived := flag.Bool("no_archived", false, "mirror only projects that are not archived")

	flag.Parse()

	if *dest == "" {
		log.Fatal("must set --dest")
	}
	if *githubURL == "" && *org == "" && *user == "" {
		log.Fatal("must set either --org or --user when github.com is used as host")
	}

	var host string
	var apiBaseURL string
	var client *github.Client
	if *githubURL != "" {
		rootURL, err := url.Parse(*githubURL)
		if err != nil {
			log.Fatal(err)
		}
		host = rootURL.Host
		apiPath, err := url.Parse("/api/v3/")
		if err != nil {
			log.Fatal(err)
		}
		apiBaseURL = rootURL.ResolveReference(apiPath).String()
		client, err = github.NewEnterpriseClient(apiBaseURL, apiBaseURL, nil)
		if err != nil {
			log.Fatal(err)
		}
	} else {
		host = "github.com"
		apiBaseURL = "https://github.com/"
		client = github.NewClient(nil)
	}
	destDir := filepath.Join(*dest, host)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		log.Fatal(err)
	}

	if *token != "" {
		content, err := ioutil.ReadFile(*token)
		if err != nil {
			log.Fatal(err)
		}

		ts := oauth2.StaticTokenSource(
			&oauth2.Token{
				AccessToken: strings.TrimSpace(string(content)),
			})
		tc := oauth2.NewClient(context.Background(), ts)
		if *githubURL != "" {
			client, err = github.NewEnterpriseClient(apiBaseURL, apiBaseURL, tc)
			if err != nil {
				log.Fatal(err)
			}
		} else {
			client = github.NewClient(tc)
		}
	}

	reposFilters := reposFilters{
		topics:        topics,
		excludeTopics: excludeTopics,
		noArchived:    noArchived,
	}
	var repos []*github.Repository
	var err error
	if *org != "" {
		repos, err = getOrgRepos(client, *org, reposFilters)
	} else if *user != "" {
		repos, err = getUserRepos(client, *user, reposFilters)
	} else {
		log.Printf("no user or org specified, cloning all repos.")
		repos, err = getUserRepos(client, "", reposFilters)
	}

	if err != nil {
		log.Fatal(err)
	}

	if !*forks {
		trimmed := repos[:0]
		for _, r := range repos {
			if r.Fork == nil || !*r.Fork {
				trimmed = append(trimmed, r)
			}
		}
		repos = trimmed
	}

	filter, err := gitindex.NewFilter(*namePattern, *excludePattern)
	if err != nil {
		log.Fatal(err)
	}

	{
		trimmed := repos[:0]
		for _, r := range repos {
			if filter.Include(*r.Name) {
				trimmed = append(trimmed, r)
			}
		}
		repos = trimmed
	}

	if err := cloneRepos(destDir, repos); err != nil {
		log.Fatalf("cloneRepos: %v", err)
	}

	if *deleteRepos {
		if err := deleteStaleRepos(*dest, filter, repos, *org+*user); err != nil {
			log.Fatalf("deleteStaleRepos: %v", err)
		}
	}
}

func deleteStaleRepos(destDir string, filter *gitindex.Filter, repos []*github.Repository, user string) error {
	var baseURL string
	if len(repos) > 0 {
		baseURL = *repos[0].HTMLURL
	} else {
		return nil
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return err
	}
	u.Path = user

	names := map[string]struct{}{}
	for _, r := range repos {
		u, err := url.Parse(*r.HTMLURL)
		if err != nil {
			return err
		}

		names[filepath.Join(u.Host, u.Path+".git")] = struct{}{}
	}
	if err := gitindex.DeleteRepos(destDir, u, names, filter); err != nil {
		log.Fatalf("deleteRepos: %v", err)
	}
	return nil
}

func hasIntersection(s1, s2 []string) bool {
	hash := make(map[string]bool)
	for _, e := range s1 {
		hash[e] = true
	}
	for _, e := range s2 {
		if hash[e] {
			return true
		}
	}
	return false
}

func filterRepositories(repos []*github.Repository, include []string, exclude []string, noArchived bool) (filteredRepos []*github.Repository) {
	for _, repo := range repos {
		if noArchived && *repo.Archived {
			continue
		}
		if (len(include) == 0 || hasIntersection(include, repo.Topics)) &&
			!hasIntersection(exclude, repo.Topics) {
			filteredRepos = append(filteredRepos, repo)
		}
	}
	return
}

func getOrgRepos(client *github.Client, org string, reposFilters reposFilters) ([]*github.Repository, error) {
	var allRepos []*github.Repository
	opt := &github.RepositoryListByOrgOptions{}
	for {
		repos, resp, err := client.Repositories.ListByOrg(context.Background(), org, opt)
		if err != nil {
			return nil, err
		}
		if len(repos) == 0 {
			break
		}

		opt.Page = resp.NextPage
		repos = filterRepositories(repos, reposFilters.topics, reposFilters.excludeTopics, *reposFilters.noArchived)
		allRepos = append(allRepos, repos...)
		if resp.NextPage == 0 {
			break
		}
	}
	return allRepos, nil
}

func getUserRepos(client *github.Client, user string, reposFilters reposFilters) ([]*github.Repository, error) {
	var allRepos []*github.Repository
	opt := &github.RepositoryListOptions{}
	for {
		repos, resp, err := client.Repositories.List(context.Background(), user, opt)
		if err != nil {
			return nil, err
		}
		if len(repos) == 0 {
			break
		}

		opt.Page = resp.NextPage
		repos = filterRepositories(repos, reposFilters.topics, reposFilters.excludeTopics, *reposFilters.noArchived)
		allRepos = append(allRepos, repos...)
		if resp.NextPage == 0 {
			break
		}
	}
	return allRepos, nil
}

func itoa(p *int) string {
	if p != nil {
		return strconv.Itoa(*p)
	}
	return ""
}

func cloneRepos(destDir string, repos []*github.Repository) error {
	for _, r := range repos {
		host, err := url.Parse(*r.HTMLURL)
		if err != nil {
			return err
		}
		config := map[string]string{
			"zoekt.web-url-type": "github",
			"zoekt.web-url":      *r.HTMLURL,
			"zoekt.name":         filepath.Join(host.Hostname(), *r.FullName),

			"zoekt.github-stars":       itoa(r.StargazersCount),
			"zoekt.github-watchers":    itoa(r.WatchersCount),
			"zoekt.github-subscribers": itoa(r.SubscribersCount),
			"zoekt.github-forks":       itoa(r.ForksCount),
		}
		dest, err := gitindex.CloneRepo(destDir, *r.FullName, *r.CloneURL, config)
		if err != nil {
			return err
		}
		if dest != "" {
			fmt.Println(dest)
		}

	}

	return nil
}
