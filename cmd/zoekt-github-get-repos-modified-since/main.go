package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/go-github/v27/github"
	"golang.org/x/oauth2"
)

const iso8601Format = "2006-01-02T15:04:05Z07:00"

func main() {
	dest := flag.String("dest", "", "destination directory")
	githubURL := flag.String("url", "", "GitHub Enterprise url. If not set github.com will be used as the host.")
	org := flag.String("org", "", "organization to mirror")
	user := flag.String("user", "", "user to mirror")
	token := flag.String("token",
		filepath.Join(os.Getenv("HOME"), ".github-token"),
		"file holding API token.")
	// forks := flag.Bool("forks", true, "also mirror forks.")
	// deleteRepos := flag.Bool("delete", false, "delete missing repos")
	namePattern := flag.String("name", "", "only clone repos whose name matches the given regexp.")
	// excludePattern := flag.String("exclude", "", "don't mirror repos whose names match this regexp.")
	topics := topicsFlag{}
	flag.Var(&topics, "topic", "only clone repos whose have one of given topics. You can add multiple topics by setting this more than once.")
	excludeTopics := topicsFlag{}
	flag.Var(&excludeTopics, "exclude_topic", "don't clone repos whose have one of given topics. You can add multiple topics by setting this more than once.")
	noArchived := flag.Bool("no_archived", false, "mirror only projects that are not archived")

	since := flag.String("since", "", "an ISOxxx string. Repos returned will be updated at or after this time")

	// lastIndex := ""

	flag.Parse()

	// for this org or user, call ListReposBy[] until we see a repo
	// that has been updated before lastIndex
	if *dest == "" {
		log.Fatal("must set --dest")
	}
	if *githubURL == "" && *org == "" && *user == "" {
		log.Fatal("must set either --org or --user when github.com is used as host")
	}
	if *since == "" {
		log.Fatal("must set --since")
	}

	sinceTime, err := time.Parse(iso8601Format, *since)
	if err != nil {
		log.Fatal(err)
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
	if err = os.MkdirAll(destDir, 0o755); err != nil {
		log.Fatal(err)
	}

	if *token != "" {
		log.Printf("reading token from :%s", *token)
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
	var repos []github.Repository

	// this is the approximate time we do a search query for updated repos.
	// the next query will look for repos updated after this time. We only
	// write this time to file if the query is successfull, in that way we
	// won't miss updating

	// I think the parent function should provide the time. This function may be called
	// multiple times in succession, and we want each to run for the same time period
	// now := time.Now()
	// fmt.Println("now:", now)

	// threeMinsAgo := now.Add(time.Duration(-80) * time.Minute)
	if *org != "" {
		repos, err = getReposUpdatedAfterLastUpdate(client, "org", *org, *namePattern, reposFilters, sinceTime)
	} else if *user != "" {
		repos, err = getReposUpdatedAfterLastUpdate(client, "user", *user, *namePattern, reposFilters, sinceTime)
	} else {
		log.Fatal("must specify org or user")
	}

	if err != nil {
		return
	}

	// otherwise, print a newline delimited list of all the repos that have changed recently.
	for _, r := range repos {
		fmt.Println(filepath.Join(destDir, *r.FullName) + ".git")
	}
}

func callGithubRepoSearchConcurrently(intialResp *github.RepositoriesSearchResult) ([]github.Repository, error) {
	// not yet implemented
	return nil, nil
}

func getReposUpdatedAfterLastUpdate(client *github.Client, key string, orgOrUser string, namePattern string, reposFilters reposFilters, lastUpdate time.Time) ([]github.Repository, error) {
	searchQuery := fmt.Sprintf("%s:%s pushed:>=%s %s", key, orgOrUser, lastUpdate.Format("2006-01-02T15:04:05Z07:00"), namePattern)
	log.Printf("searchQuery=%s\n", searchQuery)
	result, resp, err := client.Search.Repositories(context.Background(), searchQuery, &github.SearchOptions{TextMatch: false,
		ListOptions: github.ListOptions{PerPage: 100},
	})

	// fmt.Printf("result=%v resp=%v err=%v\n", result, resp, err)
	if err != nil {
		return nil, err
	}

	if resp.FirstPage == resp.LastPage {
		return result.Repositories, nil
	}

	log.Printf("oh no, more than 100 were updated since query issue\n")
	return callGithubRepoSearchConcurrently(result)

}

// we're going to have to keep track of both updatedAt and pushedAt
// we could simply keep track of every repo in a file
// repoName updatedAt pushedAt

type reposFilters struct {
	topics        []string
	excludeTopics []string
	noArchived    *bool
}
type topicsFlag []string

func (f *topicsFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *topicsFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}
