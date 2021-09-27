package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"

	"github.com/google/zoekt"
	retryablehttp "github.com/hashicorp/go-retryablehttp"
)

type Sourcegraph interface {
	GetIndexOptions(repos ...string) ([]indexOptionsItem, error)
	GetCloneURL(name string) string
	ListRepos(ctx context.Context, indexed []string) ([]string, error)
}

// sourcegraphClient contains methods which interact with the sourcegraph API.
type sourcegraphClient struct {
	// Root is the base URL for the Sourcegraph instance to index. Normally
	// http://sourcegraph-frontend-internal or http://localhost:3090.
	Root *url.URL

	// Hostname is the name we advertise to Sourcegraph when asking for the
	// list of repositories to index.
	Hostname string

	Client *retryablehttp.Client
}

// indexOptionsItem wraps IndexOptions to also include an error returned by
// the API.
type indexOptionsItem struct {
	IndexOptions
	Error string
}

func (s *sourcegraphClient) GetIndexOptions(repos ...string) ([]indexOptionsItem, error) {
	u := s.Root.ResolveReference(&url.URL{
		Path: "/.internal/search/configuration",
	})

	resp, err := s.Client.PostForm(u.String(), url.Values{"repo": repos})
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

func (s *sourcegraphClient) GetCloneURL(name string) string {
	return s.Root.ResolveReference(&url.URL{Path: path.Join("/.internal/git", name)}).String()
}

func (s *sourcegraphClient) ListRepos(ctx context.Context, indexed []string) ([]string, error) {
	body, err := json.Marshal(&struct {
		Hostname string
		Indexed  []string
	}{
		Hostname: s.Hostname,
		Indexed:  indexed,
	})
	if err != nil {
		return nil, err
	}

	u := s.Root.ResolveReference(&url.URL{Path: "/.internal/repos/index"})
	resp, err := s.Client.Post(u.String(), "application/json; charset=utf8", bytes.NewReader(body))
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

	metricNumAssigned.Set(float64(len(data.RepoNames)))

	return data.RepoNames, nil
}

type sourcegraphFake struct {
	RootDir string
	Log     *log.Logger
}

func (sf sourcegraphFake) GetIndexOptions(repos ...string) ([]indexOptionsItem, error) {
	var items []indexOptionsItem
	for _, name := range repos {
		opts, err := sf.getIndexOptions(name)
		if err != nil {
			items = append(items, indexOptionsItem{Error: err.Error()})
		} else {
			items = append(items, indexOptionsItem{IndexOptions: opts})
		}
	}
	return items, nil
}

func (sf sourcegraphFake) getIndexOptions(name string) (IndexOptions, error) {
	dir := filepath.Join(sf.RootDir, filepath.FromSlash(name))

	opts := IndexOptions{
		// magic at the end is to ensure we get a positive number when casting.
		RepoID:  int32(crc32.ChecksumIEEE([]byte(name))%(1<<31-1) + 1),
		Symbols: true,
	}

	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	if b, err := cmd.Output(); err != nil {
		return opts, err
	} else {
		head := string(bytes.TrimSpace(b))
		opts.Branches = []zoekt.RepositoryBranch{{
			Name:    "HEAD",
			Version: head,
		}}
	}

	if _, err := os.Stat(filepath.Join(dir, "SG_PRIVATE")); err == nil {
		opts.Public = false
	}
	if _, err := os.Stat(filepath.Join(dir, "SG_FORK")); err == nil {
		opts.Fork = true
	}
	if _, err := os.Stat(filepath.Join(dir, "SG_ARCHIVED")); err == nil {
		opts.Archived = true
	}
	return opts, nil
}

func (sf sourcegraphFake) GetCloneURL(name string) string {
	return filepath.Join(sf.RootDir, filepath.FromSlash(name))
}
func (sf sourcegraphFake) ListRepos(ctx context.Context, indexed []string) ([]string, error) {
	var repos []string
	err := filepath.Walk(sf.RootDir, func(path string, fi os.FileInfo, fileErr error) error {
		if fileErr != nil {
			sf.Log.Printf("WARN: ignoring error searching %s: %v", path, fileErr)
			return nil
		}
		if !fi.IsDir() {
			return nil
		}

		gitdir := filepath.Join(path, ".git")
		if fi, err := os.Stat(gitdir); err != nil || !fi.IsDir() {
			return nil
		}

		subpath, err := filepath.Rel(sf.RootDir, path)
		if err != nil {
			// According to WalkFunc docs, path is always filepath.Join(root,
			// subpath). So Rel should always work.
			return fmt.Errorf("filepath.Walk returned %s which is not relative to %s: %w", path, sf.RootDir, err)
		}

		name := filepath.ToSlash(subpath)
		repos = append(repos, name)

		return filepath.SkipDir
	})
	return repos, err
}
