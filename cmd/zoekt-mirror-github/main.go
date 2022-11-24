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
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/google/go-github/v27/github"
	"golang.org/x/oauth2"

	"github.com/sourcegraph/zoekt/gitindex"
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

// globally scoped flags
var (
	flagMaxConcurrentGHRequests = flag.Int("max-concurrent-gh-requests", 1, "Number of pages of user/org repos that can be fetched concurrently. 1, the default, is syncronous.")
	flagParallelClone           = flag.Int("parallel_clone", 1, "Number of concurrent git clones ops")
)

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
		content, err := os.ReadFile(*token)
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

type IndexedResponse struct {
	Page  int
	Org   string
	Repos []*github.Repository
	err   error
}

func callGithubConcurrently(initialResp *github.Response, concurrencyLimit int, firstResult []*github.Repository, gClient *github.Client, reposFilters reposFilters, method, org, user string) ([]*github.Repository, error) {
	pagesToCall := initialResp.LastPage - 1

	// create the matrix of results and add the first one, this is so we can maintain order
	// which unfortunately takes an extra O(n) pass
	// technically we could exactly size an array, but that requires more accurate bookkeeping,
	// and this is fine for now
	resultsMatrix := make([][]*github.Repository, pagesToCall+1)
	resultsMatrix[0] = firstResult

	semaphores := make(chan bool, concurrencyLimit)
	resStream := make(chan *IndexedResponse, pagesToCall)

	var wg sync.WaitGroup

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 1; i <= pagesToCall; i++ {
		wg.Add(1)

		go func(ctx context.Context, page int, c chan *IndexedResponse, s chan bool, w *sync.WaitGroup) {
			s <- true
			defer w.Done()

			var repos []*github.Repository
			var err error
			if method == "org" {
				repos, _, err = gClient.Repositories.ListByOrg(ctx, org, &github.RepositoryListByOrgOptions{
					ListOptions: github.ListOptions{PerPage: 100, Page: page},
				})
			} else if method == "user" {
				repos, _, err = gClient.Repositories.List(ctx, user, &github.RepositoryListOptions{
					ListOptions: github.ListOptions{PerPage: 100, Page: page},
				})
			}
			repos = filterRepositories(repos, reposFilters.topics, reposFilters.excludeTopics, *reposFilters.noArchived)
			c <- &IndexedResponse{
				Page:  page,
				Repos: repos,
				Org:   org,
				err:   err,
			}
			<-s // release semaphore
		}(ctx, i+1, resStream, semaphores, &wg) // +1 becase pages are 1 based
	}

	// close the channel in the background
	go func() {
		wg.Wait()
		close(resStream)
		close(semaphores)
	}()
	for res := range resStream {
		if res.err != nil {
			return nil, res.err // cancel will be called after this early return
		}
		resultsMatrix[res.Page-1] = res.Repos // Page index is 1 based
	}

	// Now flatten the matrix and return it
	var buf []*github.Repository
	for _, res := range resultsMatrix {
		buf = append(buf, res...)
	}

	return buf, nil

}

func getOrgRepos(client *github.Client, org string, reposFilters reposFilters) ([]*github.Repository, error) {
	log.Printf("Fetching repositories for org: %s", org)
	opt := &github.RepositoryListByOrgOptions{ListOptions: github.ListOptions{PerPage: 100}}
	repos, resp, err := client.Repositories.ListByOrg(context.Background(), org, opt)

	if err != nil {
		return nil, err
	}
	if resp.FirstPage == resp.LastPage { // if no more pages, return early
		return repos, nil
	}
	return callGithubConcurrently(resp, *flagMaxConcurrentGHRequests, repos, client, reposFilters, "org", org, "")
}

func getUserRepos(client *github.Client, user string, reposFilters reposFilters) ([]*github.Repository, error) {
	log.Printf("Fetching repositories for user: %s", user)
	opt := &github.RepositoryListOptions{ListOptions: github.ListOptions{PerPage: 100}}
	repos, resp, err := client.Repositories.List(context.Background(), user, opt)

	if err != nil {
		return nil, err
	}
	if resp.FirstPage == resp.LastPage { // if no more pages, return early
		return repos, nil
	}
	return callGithubConcurrently(resp, *flagMaxConcurrentGHRequests, repos, client, reposFilters, "user", "", user)
}

func itoa(p *int) string {
	if p != nil {
		return strconv.Itoa(*p)
	}
	return ""
}

func cloneRepos(destDir string, repos []*github.Repository) error {
	g, _ := errgroup.WithContext(context.Background())
	g.SetLimit(*flagParallelClone)

	for _, r := range repos {
		g.Go(func() error {
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
				"zoekt.archived":           marshalBool(r.Archived != nil && *r.Archived),
				"zoekt.fork":               marshalBool(r.Fork != nil && *r.Fork),
				"zoekt.public":             marshalBool(r.Private == nil || !*r.Private),
			}
			dest, err := gitindex.CloneRepo(destDir, *r.FullName, *r.CloneURL, config)
			if err != nil {
				return err
			}
			if dest != "" {
				fmt.Println(dest)
			}
			return nil
		})
	}

	g.Wait()

	return nil
}

func marshalBool(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
