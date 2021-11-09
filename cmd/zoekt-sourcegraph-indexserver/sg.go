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
	"strconv"

	"github.com/google/zoekt"
	retryablehttp "github.com/hashicorp/go-retryablehttp"
)

type Sourcegraph interface {
	GetIndexOptions(repos ...uint32) ([]indexOptionsItem, error)
	ListRepoIDs(ctx context.Context, indexed []uint32) ([]uint32, error)
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

func (s *sourcegraphClient) GetIndexOptions(repos ...uint32) ([]indexOptionsItem, error) {
	u := s.Root.ResolveReference(&url.URL{
		Path: "/.internal/search/configuration",
	})

	repoIDs := make([]string, len(repos))
	for i, id := range repos {
		repoIDs[i] = strconv.Itoa(int(id))
	}
	resp, err := s.Client.PostForm(u.String(), url.Values{"repoID": repoIDs})
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
		if opts[i].Name != "" {
			opts[i].CloneURL = s.getCloneURL(opts[i].Name)
		}
	}

	return opts, nil
}

func (s *sourcegraphClient) getCloneURL(name string) string {
	return s.Root.ResolveReference(&url.URL{Path: path.Join("/.internal/git", name)}).String()
}

func (s *sourcegraphClient) ListRepoIDs(ctx context.Context, indexed []uint32) ([]uint32, error) {
	body, err := json.Marshal(&struct {
		Hostname   string
		IndexedIDs []uint32
	}{
		Hostname:   s.Hostname,
		IndexedIDs: indexed,
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
		RepoIDs []uint32
	}
	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		return nil, err
	}

	metricNumAssigned.Set(float64(len(data.RepoIDs)))

	return data.RepoIDs, nil
}

type sourcegraphFake struct {
	RootDir string
	Log     *log.Logger
}

func (sf sourcegraphFake) GetIndexOptions(repos ...uint32) ([]indexOptionsItem, error) {
	reposIdx := map[uint32]int{}
	for i, id := range repos {
		reposIdx[id] = i
	}

	items := make([]indexOptionsItem, len(repos))
	err := sf.visitRepos(func(name string) {
		idx, ok := reposIdx[fakeID(name)]
		if !ok {
			return
		}
		opts, err := sf.getIndexOptions(name)
		if err != nil {
			items[idx] = indexOptionsItem{Error: err.Error()}
		} else {
			items[idx] = indexOptionsItem{IndexOptions: opts}
		}
	})

	if err != nil {
		return nil, err
	}

	for i := range items {
		if items[i].Error == "" && items[i].RepoID == 0 {
			items[i].Error = "not found"
		}
	}

	return items, nil
}

func (sf sourcegraphFake) getIndexOptions(name string) (IndexOptions, error) {
	dir := filepath.Join(sf.RootDir, filepath.FromSlash(name))
	exists := func(p string) bool {
		_, err := os.Stat(filepath.Join(dir, "SG_PRIVATE"))
		return err == nil
	}

	opts := IndexOptions{
		RepoID:   fakeID(name),
		Name:     name,
		CloneURL: sf.getCloneURL(name),
		Symbols:  true,

		Public:   !exists("SG_PRIVATE"),
		Fork:     exists("SG_FORK"),
		Archived: exists("SG_ARCHIVED"),
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

	return opts, nil
}

func (sf sourcegraphFake) getCloneURL(name string) string {
	return filepath.Join(sf.RootDir, filepath.FromSlash(name))
}

func (sf sourcegraphFake) ListRepoIDs(ctx context.Context, indexed []uint32) ([]uint32, error) {
	var repos []uint32
	err := sf.visitRepos(func(name string) {
		repos = append(repos, fakeID(name))
	})
	return repos, err
}

func (sf sourcegraphFake) visitRepos(visit func(name string)) error {
	return filepath.Walk(sf.RootDir, func(path string, fi os.FileInfo, fileErr error) error {
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
		visit(name)

		return filepath.SkipDir
	})
}

// fakeID returns a deterministic ID based on name. Used for fakes and tests.
func fakeID(name string) uint32 {
	// magic at the end is to ensure we get a positive number when casting.
	return uint32(crc32.ChecksumIEEE([]byte(name))%(1<<31-1) + 1)
}
