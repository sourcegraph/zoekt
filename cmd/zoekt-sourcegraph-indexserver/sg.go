package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path"
	"time"

	retryablehttp "github.com/hashicorp/go-retryablehttp"
)

var client = retryablehttp.NewClient()

func init() {
	client.Logger = debug
}

// indexOptionsItem wraps IndexOptions to also include an error returned by
// the API.
type indexOptionsItem struct {
	IndexOptions
	Error string
}

func getIndexOptions(root *url.URL, repos ...string) ([]indexOptionsItem, error) {
	u := root.ResolveReference(&url.URL{
		Path: "/.internal/search/configuration",
	})

	resp, err := client.PostForm(u.String(), url.Values{"repo": repos})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, err := ioutil.ReadAll(io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		return nil, &url.Error{
			Op:  "Get",
			URL: u.String(),
			Err: fmt.Errorf("%s: %s", resp.Status, string(b)),
		}
	}

	opts := make([]indexOptionsItem, len(repos))
	dec := json.NewDecoder(resp.Body)
	for i := range opts {
		if err := dec.Decode(&opts[i]); err != nil {
			return nil, fmt.Errorf("error decoding body: %w", err)
		}
	}

	return opts, nil
}

func getCloneURL(root *url.URL, name string) string {
	return root.ResolveReference(&url.URL{Path: path.Join("/.internal/git", name)}).String()
}

func waitForFrontend(root *url.URL) {
	warned := false
	lastWarn := time.Now()
	for {
		err := ping(root)
		if err == nil {
			break
		}

		if time.Since(lastWarn) > 15*time.Second {
			warned = true
			lastWarn = time.Now()
			log.Printf("frontend or gitserver API not available, will try again: %s", err)
		}

		time.Sleep(250 * time.Millisecond)
	}

	if warned {
		log.Println("frontend API is now reachable. Starting indexing...")
	}
}

func listRepos(ctx context.Context, hostname string, root *url.URL, indexed []string) ([]string, error) {
	body, err := json.Marshal(&struct {
		Hostname string
		Indexed  []string
	}{
		Hostname: hostname,
		Indexed:  indexed,
	})
	if err != nil {
		return nil, err
	}

	u := root.ResolveReference(&url.URL{Path: "/.internal/repos/index"})
	resp, err := client.Post(u.String(), "application/json; charset=utf8", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list repositories: status %s", resp.Status)
	}

	var data struct {
		RepoNames []string
	}
	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		return nil, err
	}

	countsByHost := make(map[string]int)
	for _, name := range data.RepoNames {
		codeHost := codeHostFromName(name)
		countsByHost[codeHost] += 1
	}
	for codeHost, count := range countsByHost {
		metricNumAssigned.WithLabelValues(codeHost).Set(float64(count))
	}
	return data.RepoNames, nil
}

func ping(root *url.URL) error {
	u := root.ResolveReference(&url.URL{Path: "/.internal/ping", RawQuery: "service=gitserver"})
	resp, err := client.Get(u.String())
	if err != nil {
		return err
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ping: bad HTTP response status %d: %s", resp.StatusCode, string(body))
	}
	if !bytes.Equal(body, []byte("pong")) {
		return fmt.Errorf("ping: did not receive pong: %s", string(body))
	}
	return nil
}
