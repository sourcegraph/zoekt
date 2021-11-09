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
	"time"

	"github.com/google/zoekt"
	retryablehttp "github.com/hashicorp/go-retryablehttp"
	"go.uber.org/atomic"
	"golang.org/x/net/trace"
)

type SourcegraphListResult struct {
	// IDs is the set of Sourcegraph repository IDs that this replica needs
	// to index.
	IDs []uint32

	// IterateIndexOptions best effort resolves the IndexOptions for RepoIDs. If
	// any repository fails it internally logs.
	IterateIndexOptions func(func(IndexOptions))
}

type Sourcegraph interface {
	List(ctx context.Context, indexed []uint32) (*SourcegraphListResult, error)

	// GetIndexOptions is deprecated but kept around until we improve our
	// forceIndex code.
	GetIndexOptions(repos ...uint32) ([]indexOptionsItem, error)
}

// sourcegraphClient contains methods which interact with the sourcegraph API.
type sourcegraphClient struct {
	// Root is the base URL for the Sourcegraph instance to index. Normally
	// http://sourcegraph-frontend-internal or http://localhost:3090.
	Root *url.URL

	// Hostname is the name we advertise to Sourcegraph when asking for the
	// list of repositories to index.
	Hostname string

	// BatchSize is how many repository configurations we request at once. If
	// zero a value of 1000 is used.
	BatchSize int

	Client *retryablehttp.Client

	// configFingerprint is the last config fingerprint returned from
	// Sourcegraph. It can be used for future calls to the configuration
	// endpoint.
	configFingerprint atomic.String
}

func (s *sourcegraphClient) List(ctx context.Context, indexed []uint32) (*SourcegraphListResult, error) {
	repos, err := s.listRepoIDs(ctx, indexed)
	if err != nil {
		return nil, err
	}

	batchSize := s.BatchSize
	if batchSize == 0 {
		batchSize = 1000
	}

	// We want to use a consistent fingerprint for each call. Next time list is
	// called we want to use the first fingerprint returned from the
	// configuration endpoint. However, if any of our configuration calls fail,
	// we need to fallback to our last value.
	lastFingerprint := s.configFingerprint.Load()
	first := true

	iterate := func(f func(IndexOptions)) {
		start := time.Now()
		tr := trace.New("getIndexOptions", "")
		tr.LazyPrintf("getting index options for %d repos", len(repos))
		tr.LazyPrintf("fingerprint: %s", lastFingerprint)

		defer func() {
			metricResolveRevisionsDuration.Observe(time.Since(start).Seconds())
			tr.Finish()
		}()

		// We ask the frontend to get index options in batches.
		for repos := range batched(repos, batchSize) {
			start := time.Now()
			opts, fingerprint, err := s.getIndexOptions(lastFingerprint, repos...)
			if err != nil {
				// Call failed, restore old fingerprint for next call to List.
				first = false
				s.configFingerprint.Store(lastFingerprint)

				metricResolveRevisionDuration.WithLabelValues("false").Observe(time.Since(start).Seconds())
				tr.LazyPrintf("failed fetching options batch: %v", err)
				tr.SetError()
				continue
			}

			if first {
				first = false
				tr.LazyPrintf("new fingerprint: %s", fingerprint)
				s.configFingerprint.Store(fingerprint)
			}

			metricResolveRevisionDuration.WithLabelValues("true").Observe(time.Since(start).Seconds())
			for _, opt := range opts {
				if opt.Error != "" {
					metricGetIndexOptionsError.Inc()
					tr.LazyPrintf("failed fetching options for %v: %v", opt.Name, opt.Error)
					tr.SetError()
					continue
				}
				f(opt.IndexOptions)
			}
		}
	}

	return &SourcegraphListResult{
		IDs:                 repos,
		IterateIndexOptions: iterate,
	}, nil
}

// indexOptionsItem wraps IndexOptions to also include an error returned by
// the API.
type indexOptionsItem struct {
	IndexOptions
	Error string
}

func (s *sourcegraphClient) GetIndexOptions(repos ...uint32) ([]indexOptionsItem, error) {
	opts, _, err := s.getIndexOptions("", repos...)
	return opts, err
}

func (s *sourcegraphClient) getIndexOptions(fingerprint string, repos ...uint32) ([]indexOptionsItem, string, error) {
	u := s.Root.ResolveReference(&url.URL{
		Path: "/.internal/search/configuration",
	})

	repoIDs := make([]string, len(repos))
	for i, id := range repos {
		repoIDs[i] = strconv.Itoa(int(id))
	}
	data := url.Values{"repoID": repoIDs}
	req, err := retryablehttp.NewRequest("POST", u.String(), []byte(data.Encode()))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if fingerprint != "" {
		req.Header.Set("X-Sourcegraph-Config-Fingerprint", fingerprint)
	}

	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, err := ioutil.ReadAll(io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
		if err != nil {
			return nil, "", err
		}
		return nil, "", &url.Error{
			Op:  "Get",
			URL: u.String(),
			Err: fmt.Errorf("%s: %s", resp.Status, string(b)),
		}
	}

	opts := make([]indexOptionsItem, len(repos))
	dec := json.NewDecoder(resp.Body)
	for i := range opts {
		if err := dec.Decode(&opts[i]); err != nil {
			return nil, "", fmt.Errorf("error decoding body: %w", err)
		}
		if opts[i].Name != "" {
			opts[i].CloneURL = s.getCloneURL(opts[i].Name)
		}
	}

	return opts, resp.Header.Get("X-Sourcegraph-Config-Fingerprint"), nil
}

func (s *sourcegraphClient) getCloneURL(name string) string {
	return s.Root.ResolveReference(&url.URL{Path: path.Join("/.internal/git", name)}).String()
}

func (s *sourcegraphClient) listRepoIDs(ctx context.Context, indexed []uint32) ([]uint32, error) {
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

func (sf sourcegraphFake) List(ctx context.Context, indexed []uint32) (*SourcegraphListResult, error) {
	repos, err := sf.ListRepoIDs(ctx, indexed)
	if err != nil {
		return nil, err
	}

	iterate := func(f func(IndexOptions)) {
		opts, err := sf.GetIndexOptions(repos...)
		if err != nil {
			sf.Log.Printf("WARN: ignoring GetIndexOptions error: %v", err)
		}
		for _, opt := range opts {
			if opt.Error != "" {
				sf.Log.Printf("WARN: ignoring GetIndexOptions error for %s: %v", opt.Name, err)
				continue
			}
			f(opt.IndexOptions)
		}
	}

	return &SourcegraphListResult{
		IDs:                 repos,
		IterateIndexOptions: iterate,
	}, nil
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
