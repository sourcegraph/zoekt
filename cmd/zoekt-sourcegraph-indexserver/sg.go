package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	retryablehttp "github.com/hashicorp/go-retryablehttp"
	"golang.org/x/net/trace"

	"github.com/sourcegraph/zoekt"
)

// SourcegraphListResult is the return value of Sourcegraph.List. It is its
// own type since internally we batch the calculation of index options. This
// is exposed via IterateIndexOptions.
//
// This type has state and is coupled to the Sourcegraph implementation.
type SourcegraphListResult struct {
	// IDs is the set of Sourcegraph repository IDs that this replica needs
	// to index.
	IDs []uint32

	// IterateIndexOptions best effort resolves the IndexOptions for RepoIDs. If
	// any repository fails it internally logs. It uses the "config fingerprint"
	// to reduce the amount of work done. This means we only resolve options for
	// repositories which have been mutated since the last Sourcegraph.List
	// call.
	//
	// Note: this has a side-effect of setting a the "config fingerprint". The
	// config fingerprint means we only calculate index options for repositories
	// that have changed since the last call to IterateIndexOptions. If you want
	// to force calculation of index options use
	// Sourcegraph.ForceIterateIndexOptions.
	//
	// Note: This should not be called concurrently with the Sourcegraph client.
	IterateIndexOptions func(func(IndexOptions))
}

// Sourcegraph represents the Sourcegraph service. It informs the indexserver
// what to index and which options to use.
type Sourcegraph interface {
	// List returns a list of repository IDs to index as well as a facility to
	// fetch the indexing options.
	//
	// Note: The return value is not safe to use concurrently with future calls
	// to List.
	List(ctx context.Context, indexed []uint32) (*SourcegraphListResult, error)

	// ForceIterateIndexOptions will best-effort calculate the index options for
	// all repos. For each repo it will call either onSuccess or onError. This
	// is the forced version of IterateIndexOptions, so will always calculate
	// options for each id in repos.
	ForceIterateIndexOptions(onSuccess func(IndexOptions), onError func(uint32, error), repos ...uint32)

	// GetRepoRank returns a score vector for the given repository. Repositories are
	// assumed to be ordered by each pairwise component of the resulting vector,
	// lower scores coming earlier.
	GetRepoRank(ctx context.Context, repoName string) ([]float64, error)

	// GetDocumentRanks returns a map from paths within the given repo to their
	// score vectors. Paths are assumed to be ordered by each pairwise component of
	// the resulting vector, lower scores coming earlier
	GetDocumentRanks(ctx context.Context, repoName string) (map[string][]float64, error)
}

func newSourcegraphClient(rootURL *url.URL, hostname string, batchSize int) *sourcegraphClient {

	client := retryablehttp.NewClient()
	client.Logger = debug

	// Sourcegraph might return an error message in the body if StatusCode==500. The
	// default behavior of the go-retryablehttp client is to drain the body and not
	// to propagate the error. Hence, we call ErrorPropagatedRetryPolicy instead of
	// DefaultRetryPolicy and augment the error with the response body if possible.
	client.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		shouldRetry, checkErr := retryablehttp.ErrorPropagatedRetryPolicy(ctx, resp, err)

		if resp != nil && resp.StatusCode == http.StatusInternalServerError {
			if b, e := io.ReadAll(resp.Body); e == nil {
				checkErr = fmt.Errorf("%w: body=%q", checkErr, string(b))
			}
		}

		return shouldRetry, checkErr
	}

	return &sourcegraphClient{
		Root:      rootURL,
		Client:    client,
		Hostname:  hostname,
		BatchSize: batchSize,
	}
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
	// zero a value of 10000 is used.
	BatchSize int

	// Client is used to make requests to the Sourcegraph instance. Prefer to
	// use .doRequest() to ensure the appropriate headers are set.
	Client *retryablehttp.Client

	// configFingerprint is the last config fingerprint returned from
	// Sourcegraph. It can be used for future calls to the configuration
	// endpoint.
	configFingerprint string

	// configFingerprintReset tracks when we should zero out the
	// configFingerprint. We want to periodically do this just in case our
	// configFingerprint logic is faulty. When it is cleared out, we fallback to
	// calculating everything.
	configFingerprintReset time.Time
}

// GetRepoRank asks Sourcegraph for the score vector of repoName.
func (s *sourcegraphClient) GetRepoRank(ctx context.Context, repoName string) ([]float64, error) {
	u := s.Root.ResolveReference(&url.URL{
		Path: "/.internal/ranks/" + strings.Trim(repoName, "/"),
	})

	b, err := s.get(ctx, u)
	if err != nil {
		return nil, err
	}

	var ranks []float64
	err = json.Unmarshal(b, &ranks)
	if err != nil {
		return nil, err
	}

	return ranks, nil
}

// GetDocumentRanks asks Sourcegraph for a mapping of file paths to score
// vectors.
func (s *sourcegraphClient) GetDocumentRanks(ctx context.Context, repoName string) (map[string][]float64, error) {
	u := s.Root.ResolveReference(&url.URL{
		Path: "/.internal/ranks/" + strings.Trim(repoName, "/") + "/documents",
	})

	b, err := s.get(ctx, u)
	if err != nil {
		return nil, err
	}

	ranks := make(map[string][]float64)
	err = json.Unmarshal(b, &ranks)
	if err != nil {
		return nil, err
	}

	// Invariant: All score vectors have the same length.
	first := true
	wantLen := -1
	for _, v := range ranks {
		if first {
			first = false
			wantLen = len(v)
			continue
		}
		if len(v) != wantLen {
			return nil, fmt.Errorf("found a document with a different length of scores %d<>%d\n", wantLen, len(v))
		}
	}

	return ranks, nil
}

func (s *sourcegraphClient) get(ctx context.Context, u *url.URL) ([]byte, error) {
	req, err := retryablehttp.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
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

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return b, nil
}

func (s *sourcegraphClient) List(ctx context.Context, indexed []uint32) (*SourcegraphListResult, error) {
	repos, err := s.listRepoIDs(ctx, indexed)
	if err != nil {
		return nil, fmt.Errorf("listRepoIDs: %w", err)
	}

	batchSize := s.BatchSize
	if batchSize == 0 {
		batchSize = 10_000
	}

	// Check if we should recalculate everything.
	if time.Now().After(s.configFingerprintReset) {
		// for every 500 repos we wait a minute. 2021-12-15 on sourcegraph.com
		// this works out to every 100 minutes.
		next := time.Duration(len(indexed)) * time.Minute / 500
		if min := 5 * time.Minute; next < min {
			next = min
		}
		next += time.Duration(rand.Int63n(int64(next) / 4)) // jitter
		s.configFingerprintReset = time.Now().Add(next)
		s.configFingerprint = ""
	}

	// We want to use a consistent fingerprint for each call. Next time list is
	// called we want to use the first fingerprint returned from the
	// configuration endpoint. However, if any of our configuration calls fail,
	// we need to fallback to our last value.
	lastFingerprint := s.configFingerprint
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
				s.configFingerprint = lastFingerprint

				metricResolveRevisionDuration.WithLabelValues("false").Observe(time.Since(start).Seconds())
				tr.LazyPrintf("failed fetching options batch: %v", err)
				tr.SetError()
				continue
			}

			if first {
				first = false
				tr.LazyPrintf("new fingerprint: %s", fingerprint)
				s.configFingerprint = fingerprint
			}

			metricResolveRevisionDuration.WithLabelValues("true").Observe(time.Since(start).Seconds())
			for _, opt := range opts {
				metricGetIndexOptions.Inc()
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

func (s *sourcegraphClient) ForceIterateIndexOptions(onSuccess func(IndexOptions), onError func(uint32, error), repos ...uint32) {
	batchSize := s.BatchSize
	if batchSize == 0 {
		batchSize = 10_000
	}

	for repos := range batched(repos, batchSize) {
		opts, _, err := s.getIndexOptions("", repos...)
		if err != nil {
			for _, id := range repos {
				onError(id, err)
			}
			continue
		}
		for _, o := range opts {
			if o.RepoID > 0 && o.Error != "" {
				onError(o.RepoID, errors.New(o.Error))
			}
			if o.Error == "" {
				onSuccess(o.IndexOptions)
			}
		}
	}
}

// indexOptionsItem wraps IndexOptions to also include an error returned by
// the API.
type indexOptionsItem struct {
	IndexOptions
	Error string
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

	resp, err := s.doRequest(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
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

	dec := json.NewDecoder(resp.Body)
	var opts []indexOptionsItem
	for {
		var opt indexOptionsItem
		err := dec.Decode(&opt)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("error decoding body: %w", err)
		}
		opt.CloneURL = s.getCloneURL(opt.Name)
		opts = append(opts, opt)
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
	req, err := retryablehttp.NewRequest(http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf8")

	resp, err := s.doRequest(req)
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

// doRequest executes the provided request after adding the appropriate headers
// for interacting with a Sourcegraph instance.
func (s *sourcegraphClient) doRequest(req *retryablehttp.Request) (*http.Response, error) {
	// Make all requests as an internal user.
	//
	// Should match github.com/sourcegraph/sourcegraph/internal/actor.headerKeyActorUID
	// and github.com/sourcegraph/sourcegraph/internal/actor.headerValueInternalActor
	req.Header.Set("X-Sourcegraph-Actor-UID", "internal")
	return s.Client.Do(req)
}

type sourcegraphFake struct {
	RootDir string
	Log     *log.Logger
}

// GetRepoRank expects a file with exactly 1 line containing a comma separated
// list of float64 as ranks.
func (sf sourcegraphFake) GetRepoRank(ctx context.Context, repoName string) ([]float64, error) {
	dir := filepath.Join(sf.RootDir, filepath.FromSlash(repoName))

	b, err := os.ReadFile(filepath.Join(dir, "SG_REPO_RANKS"))
	if err != nil {
		return nil, err
	}

	return floats64(string(b)), nil
}

// GetDocumentRanks expects a file where each line has the following format:
// path<tab>f... . where f=1-rank is a float64 score in the interval [0,1].
// Multiple f are separated by a comma. Each line has to have the same number of
// ranks.
func (sf sourcegraphFake) GetDocumentRanks(ctx context.Context, repoName string) (map[string][]float64, error) {
	dir := filepath.Join(sf.RootDir, filepath.FromSlash(repoName))

	fd, err := os.Open(filepath.Join(dir, "SG_DOCUMENT_RANKS"))
	if err != nil {
		return nil, err
	}

	ranks := make(map[string][]float64)

	scanner := bufio.NewScanner(fd)
	for scanner.Scan() {
		s := scanner.Text()
		pathRanks := strings.Split(s, "\t")
		ranks[pathRanks[0]] = floats64(pathRanks[1])
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return ranks, nil
}

func floats64(s string) []float64 {
	parts := strings.Split(s, ",")

	var r []float64
	for _, rank := range parts {
		f, err := strconv.ParseFloat(rank, 64)
		if err != nil {
			return nil
		}
		r = append(r, f)
	}

	return r
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
				sf.Log.Printf("WARN: ignoring GetIndexOptions error for %s: %v", opt.Name, opt.Error)
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

func (sf sourcegraphFake) ForceIterateIndexOptions(onSuccess func(IndexOptions), onError func(uint32, error), repos ...uint32) {
	opts, err := sf.GetIndexOptions(repos...)
	if err != nil {
		for _, id := range repos {
			onError(id, err)
		}
		return
	}
	for _, o := range opts {
		if o.RepoID > 0 && o.Error != "" {
			onError(o.RepoID, errors.New(o.Error))
		}
		if o.Error == "" {
			onSuccess(o.IndexOptions)
		}
	}
}

func (sf sourcegraphFake) GetIndexOptions(repos ...uint32) ([]indexOptionsItem, error) {
	reposIdx := map[uint32]int{}
	for i, id := range repos {
		reposIdx[id] = i
	}

	items := make([]indexOptionsItem, len(repos))
	err := sf.visitRepos(func(name string) {
		idx, ok := reposIdx[sf.id(name)]
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
		_, err := os.Stat(filepath.Join(dir, p))
		return err == nil
	}
	float := func(p string) float64 {
		b, _ := os.ReadFile(filepath.Join(dir, p))
		f, _ := strconv.ParseFloat(string(bytes.TrimSpace(b)), 64)
		return f
	}

	opts := IndexOptions{
		RepoID:   sf.id(name),
		Name:     name,
		CloneURL: sf.getCloneURL(name),
		Symbols:  true,

		Public:   !exists("SG_PRIVATE"),
		Fork:     exists("SG_FORK"),
		Archived: exists("SG_ARCHIVED"),

		Priority: float("SG_PRIORITY"),
	}

	branches, err := sf.getBranches(name)
	if err != nil {
		return opts, err
	}
	opts.Branches = branches

	return opts, nil
}

func (sf sourcegraphFake) getBranches(name string) ([]zoekt.RepositoryBranch, error) {
	dir := filepath.Join(sf.RootDir, filepath.FromSlash(name))
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return nil, err
	}

	cfg, err := repo.Config()
	if err != nil {
		return nil, err
	}

	sec := cfg.Raw.Section("zoekt")
	branches := sec.Options.GetAll("branch")
	if len(branches) == 0 {
		branches = append(branches, "HEAD")
	}

	rBranches := make([]zoekt.RepositoryBranch, 0, len(branches))
	for _, branch := range branches {
		cmd := exec.Command("git", "rev-parse", branch)
		cmd.Dir = dir
		if b, err := cmd.Output(); err != nil {
			sf.Log.Printf("WARN: Could not get branch %s/%s", name, branch)
		} else {
			version := string(bytes.TrimSpace(b))
			rBranches = append(rBranches, zoekt.RepositoryBranch{
				Name:    branch,
				Version: version,
			})
		}
	}

	if len(rBranches) == 0 {
		return nil, fmt.Errorf("WARN: Could not get any branch revisions for repo %s", name)
	}

	return rBranches, nil
}

func (sf sourcegraphFake) id(name string) uint32 {
	// allow overriding the ID.
	idPath := filepath.Join(sf.RootDir, filepath.FromSlash(name), "SG_ID")
	if b, _ := os.ReadFile(idPath); len(b) > 0 {
		id, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err == nil {
			return uint32(id)
		}
	}
	return fakeID(name)
}

func (sf sourcegraphFake) getCloneURL(name string) string {
	return filepath.Join(sf.RootDir, filepath.FromSlash(name))
}

func (sf sourcegraphFake) ListRepoIDs(ctx context.Context, indexed []uint32) ([]uint32, error) {
	var repos []uint32
	err := sf.visitRepos(func(name string) {
		repos = append(repos, sf.id(name))
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

type sourcegraphNop struct{}

func (s sourcegraphNop) List(ctx context.Context, indexed []uint32) (*SourcegraphListResult, error) {
	return nil, nil
}

func (s sourcegraphNop) ForceIterateIndexOptions(onSuccess func(IndexOptions), onError func(uint32, error), repos ...uint32) {
	return
}

func (s sourcegraphNop) GetRepoRank(ctx context.Context, repoName string) ([]float64, error) {
	return nil, nil
}

func (s sourcegraphNop) GetDocumentRanks(ctx context.Context, repoName string) (map[string][]float64, error) {
	return nil, nil
}
