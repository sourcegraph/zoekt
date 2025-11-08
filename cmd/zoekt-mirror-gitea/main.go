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

// Command zoekt-mirror-gerrit fetches all repos of a gitea user or organization
// and clones them. It is strongly recommended to get a personal API token from
// https://gitea.com/user/settings/applications, save the token in a file, and point
// the --token option to it.
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

	"code.gitea.io/sdk/gitea"

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
	noArchived *bool
}

func main() {
	dest := flag.String("dest", "", "destination directory")
	giteaURL := flag.String("url", "https://gitea.com/", "Gitea url. If not set gitea.com will be used as the host.")
	org := flag.String("org", "", "organization to mirror")
	user := flag.String("user", "", "user to mirror")
	token := flag.String("token",
		filepath.Join(os.Getenv("HOME"), ".gitea-token"),
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
	if *giteaURL == "" && *org == "" && *user == "" {
		log.Fatal("must set either --org or --user when gitea.com is used as host")
	}

	var host string
	var client *gitea.Client
	clientOptions := []gitea.ClientOption{}

	destDir := filepath.Join(*dest, host)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		log.Fatal(err)
	}

	if *token != "" {
		content, err := os.ReadFile(*token)
		if err != nil {
			log.Fatal(err)
		}
		contentStr := string(content)
		if strings.Contains(contentStr, "\n") {
			// The user has to pass in the token via a file, so catch this common bug.
			log.Fatal("Invalid token - remove the EOL from the file")
		}
		clientOptions = append(clientOptions, gitea.SetToken(string(content)))
	}
	client, err := gitea.NewClient(*giteaURL, clientOptions...)
	if err != nil {
		log.Fatal(err)
	}

	reposFilters := reposFilters{
		noArchived: noArchived,
	}
	var repos []*gitea.Repository
	switch {
	case *org != "":
		log.Printf("fetch repos for org: %s", *org)
		repos, err = getOrgRepos(client, *org, reposFilters)
	case *user != "":
		log.Printf("fetch repos for user: %s", *user)
		repos, err = getUserRepos(client, *user, reposFilters)
	default:
		log.Printf("no user or org specified, cloning all repos.")
		repos, err = getUserRepos(client, "", reposFilters)
	}

	if err != nil {
		log.Fatal(err)
	}

	if !*forks {
		trimmed := []*gitea.Repository{}
		for _, r := range repos {
			if r.Fork {
				continue
			}
			trimmed = append(trimmed, r)
		}
		repos = trimmed
	}

	filter, err := gitindex.NewFilter(*namePattern, *excludePattern)
	if err != nil {
		log.Fatal(err)
	}

	{
		trimmed := []*gitea.Repository{}
		for _, r := range repos {
			if !filter.Include(r.Name) {
				log.Println(r.Name)
				continue
			}
			trimmed = append(trimmed, r)
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

func deleteStaleRepos(destDir string, filter *gitindex.Filter, repos []*gitea.Repository, user string) error {
	var baseURL string
	if len(repos) > 0 {
		baseURL = repos[0].HTMLURL
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
		u, err := url.Parse(r.HTMLURL)
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

func filterRepositories(repos []*gitea.Repository, noArchived bool) (filteredRepos []*gitea.Repository) {
	for _, repo := range repos {
		if noArchived && repo.Archived {
			continue
		}
		filteredRepos = append(filteredRepos, repo)
	}
	return
}

func getOrgRepos(client *gitea.Client, org string, reposFilters reposFilters) ([]*gitea.Repository, error) {
	var allRepos []*gitea.Repository
	searchOptions := &gitea.SearchRepoOptions{}
	// OwnerID
	organization, _, err := client.GetOrg(org)
	if err != nil {
		return nil, err
	}

	searchOptions.OwnerID = organization.ID

	for {
		repos, resp, err := client.SearchRepos(*searchOptions)
		if err != nil {
			return nil, err
		}
		if len(repos) == 0 {
			break
		}

		searchOptions.Page = resp.NextPage
		repos = filterRepositories(repos, *reposFilters.noArchived)
		allRepos = append(allRepos, repos...)
		if resp.NextPage == 0 {
			break
		}
	}
	return allRepos, nil
}

func getUserRepos(client *gitea.Client, user string, reposFilters reposFilters) ([]*gitea.Repository, error) {
	var allRepos []*gitea.Repository
	searchOptions := &gitea.SearchRepoOptions{}
	u, _, err := client.GetUserInfo(user)
	if err != nil {
		return nil, err
	}
	searchOptions.OwnerID = u.ID
	for {
		repos, resp, err := client.SearchRepos(*searchOptions)
		if err != nil {
			return nil, err
		}
		if len(repos) == 0 {
			break
		}
		repos = filterRepositories(repos, *reposFilters.noArchived)
		allRepos = append(allRepos, repos...)
		searchOptions.Page = resp.NextPage
		if resp.NextPage == 0 {
			break
		}
	}
	return allRepos, nil
}

func cloneRepos(destDir string, repos []*gitea.Repository) error {
	for _, r := range repos {
		host, err := url.Parse(r.HTMLURL)
		if err != nil {
			return err
		}
		log.Printf("cloning %s", r.HTMLURL)

		config := map[string]string{
			"zoekt.web-url-type": "gitea",
			"zoekt.web-url":      r.HTMLURL,
			"zoekt.name":         filepath.Join(host.Hostname(), r.FullName),

			"zoekt.gitea-stars":       strconv.Itoa(r.Stars),
			"zoekt.gitea-watchers":    strconv.Itoa(r.Watchers),
			"zoekt.gitea-subscribers": strconv.Itoa(r.Watchers), // FIXME: Get repo subscribers from API
			"zoekt.gitea-forks":       strconv.Itoa(r.Forks),

			"zoekt.archived": marshalBool(r.Archived),
			"zoekt.fork":     marshalBool(r.Fork),
			"zoekt.public":   marshalBool(r.Private || r.Internal), // count internal repos as private
		}
		dest, err := gitindex.CloneRepo(destDir, r.FullName, r.CloneURL, config)
		if err != nil {
			return err
		}
		if dest != "" {
			fmt.Println(dest)
		}

	}

	return nil
}

func marshalBool(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
