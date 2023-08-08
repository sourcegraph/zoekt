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
	"github.com/xvandish/zoekt/gitindex"
	"golang.org/x/oauth2"
	"golang.org/x/sync/errgroup"
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
	forks := flag.Bool("forks", true, "whether to allow forks or not. This DOES NOT mean that only forks are allowed")
	// deleteRepos := flag.Bool("delete", false, "delete missing repos")
	namePattern := flag.String("name", "", "only return repos whose name matches the given regexp.")
	excludePattern := flag.String("exclude", "", "don't return repos whose names match this regexp.")
	topics := topicsFlag{}
	flag.Var(&topics, "topic", "only clone repos whose have one of given topics. You can add multiple topics by setting this more than once.")
	excludeTopics := topicsFlag{}
	flag.Var(&excludeTopics, "exclude_topic", "don't clone repos whose have one of given topics. You can add multiple topics by setting this more than once.")
	noArchived := flag.Bool("no_archived", false, "search and return for only projects that are not archived")
	parallelSearchReqs := flag.Int("parallel_search_api_reqs", 1, "Number of search requests that can be in flight at the same time. Used to fetch multiple pages of large results at once.")

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
		repos, err = getReposUpdatedAfterLastUpdate(client, "org", *org, *namePattern, reposFilters, sinceTime, *parallelSearchReqs, *forks)
	} else if *user != "" {
		repos, err = getReposUpdatedAfterLastUpdate(client, "user", *user, *namePattern, reposFilters, sinceTime, *parallelSearchReqs, *forks)
	} else {
		log.Fatal("must specify org or user")
	}

	if err != nil {
		return
	}

	filter, err := gitindex.NewFilter(*namePattern, *excludePattern)
	if err != nil {
		log.Fatal(err)
	}

	// remove repos that don't match our naming pattern, or contain a topic
	// we want to exclude. The inclusion logic for topics is in the search query
	// as GitHub supports it there (but not exclusion)
	{
		trimmed := repos[:0]
		for _, r := range repos {
			if filter.Include(*r.Name) && !hasIntersection(reposFilters.excludeTopics, r.Topics) {
				trimmed = append(trimmed, r)
			}
		}
		repos = trimmed
	}

	// otherwise, print a newline delimited list of all the repos that have changed recently.
	for _, r := range repos {
		fmt.Println(filepath.Join(destDir, *r.FullName) + ".git")
	}
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

// fetches a specific page of github repository search results
// TODO: how to handle a specific page failing
func fetchPage(client *github.Client, searchQuery string, page int, results chan<- []github.Repository) error {
	opts := &github.SearchOptions{TextMatch: false, ListOptions: github.ListOptions{PerPage: 100, Page: page}}
	result, _, err := client.Search.Repositories(context.Background(), searchQuery, opts)

	if err != nil {
		log.Printf("ERROR: query=%s error getting page=%d\n", searchQuery, page)
		return err
	}

	// TODO: investigate the incomplete results thing
	results <- result.Repositories
	return nil
}

func callGithubRepoSearchConcurrently(initialResp *github.Response, concurrencyLimit int, firstResult *github.RepositoriesSearchResult, gClient *github.Client, reposFilters reposFilters, searchQuery string) ([]github.Repository, error) {
	pagesToCall := initialResp.LastPage - 1

	var reposToUpdate []github.Repository
	// buffered channel so we don't block without a requisite send
	results := make(chan []github.Repository, pagesToCall)

	g, _ := errgroup.WithContext(context.Background())
	g.SetLimit(concurrencyLimit)
	for i := 1; i <= pagesToCall; i++ {
		i := i
		g.Go(func() error {
			return fetchPage(gClient, searchQuery, i+1, results)
		})
	}

	go func() {
		if err := g.Wait(); err != nil {
			log.Printf("Error fetching pages %v", err)
		} else {
			log.Printf("finished waiting for g\n")
		}
		close(results)
	}()

	for res := range results {
		reposToUpdate = append(reposToUpdate, res...)
	}
	reposToUpdate = append(reposToUpdate, firstResult.Repositories...)

	return reposToUpdate, nil
}

func getReposUpdatedAfterLastUpdate(client *github.Client, key string, orgOrUser string, namePattern string, reposFilters reposFilters, lastUpdate time.Time, maxParallelSearchReqs int, forksPresent bool) ([]github.Repository, error) {
	forkString := "true"
	if !forksPresent {
		forkString = "false"
	}

	// we can't use the `x in:name` qualifier for namePattern, as unfortunately regex isn't yet supported, and our config
	// allows regex in the name pattern. We instead filter later.
	searchQuery := fmt.Sprintf("%s:%s pushed:>=%s fork:%s", key, orgOrUser, lastUpdate.Format(iso8601Format), forkString)
	if *reposFilters.noArchived {
		searchQuery = searchQuery + fmt.Sprintf(" archived:false")
	}
	// we can include topics but can't exclude them. We exclude them at the end
	for _, topic := range reposFilters.topics {
		searchQuery = searchQuery + fmt.Sprintf(" topic:%s", topic)
	}

	result, resp, err := client.Search.Repositories(context.Background(), searchQuery, &github.SearchOptions{TextMatch: false,
		ListOptions: github.ListOptions{PerPage: 100},
	})

	if err != nil {
		return nil, err
	}

	if resp.FirstPage == resp.LastPage {
		return result.Repositories, nil
	}

	return callGithubRepoSearchConcurrently(resp, maxParallelSearchReqs, result, client, reposFilters, searchQuery)

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
