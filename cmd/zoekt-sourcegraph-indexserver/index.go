package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
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
	"strings"
	"time"

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
)

// IndexOptions are the options that Sourcegraph can set via it's search
// configuration endpoint.
type IndexOptions struct {
	// LargeFiles is a slice of glob patterns where matching file paths should
	// be indexed regardless of their size. The pattern syntax can be found
	// here: https://golang.org/pkg/path/filepath/#Match.
	LargeFiles []string

	// Symbols if true will make zoekt index the output of ctags.
	Symbols bool

	// Branches is a slice of branches to index.
	Branches []zoekt.RepositoryBranch

	// RepoID is the Sourcegraph Repository ID.
	RepoID int32
}

// indexArgs represents the arguments we pass to zoekt-archive-index
type indexArgs struct {
	IndexOptions

	// Root is the base URL for the Sourcegraph instance to index. Normally
	// http://sourcegraph-frontend-internal or http://localhost:3090.
	Root *url.URL

	// Name is the name of the repository.
	Name string

	// Incremental indicates to skip indexing if already indexed.
	Incremental bool

	// IndexDir is the index directory to store the shards.
	IndexDir string

	// Parallelism is the number of shards to compute in parallel.
	Parallelism int

	// FileLimit is the maximum size of a file
	FileLimit int

	// DownloadLimitMBPS is the maximum MB/s to use when downloading the
	// archive.
	DownloadLimitMBPS string
}

// BuildOptions returns a build.Options represented by indexArgs. Note: it
// doesn't set fields like repository/branch.
func (o *indexArgs) BuildOptions() *build.Options {
	rawConfig := map[string]string{}
	if o.IndexOptions.RepoID > 0 {
		// NOTE(keegan): 2020-08-13 This is currently not read anywhere. We are
		// setting it so in a few releases all indexes should have it set.
		rawConfig["repoid"] = strconv.Itoa(int(o.IndexOptions.RepoID))
	}

	return &build.Options{
		// It is important that this RepositoryDescription exactly matches
		// what the indexer we call will produce. This is to ensure that
		// IncrementalSkipIndexing returns true if nothing needs to be done.
		RepositoryDescription: zoekt.Repository{
			Name:      o.Name,
			Branches:  o.Branches,
			RawConfig: rawConfig,
		},
		IndexDir:         o.IndexDir,
		Parallelism:      o.Parallelism,
		SizeMax:          o.FileLimit,
		LargeFiles:       o.LargeFiles,
		CTagsMustSucceed: o.Symbols,
		DisableCTags:     !o.Symbols,
	}
}

func (o *indexArgs) String() string {
	s := o.Name
	for i, b := range o.Branches {
		if i == 0 {
			s = fmt.Sprintf("%s@%s=%s", s, b.Name, b.Version)
		} else {
			s = fmt.Sprintf("%s,%s=%s", s, b.Name, b.Version)
		}
	}
	return s
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

func archiveIndex(o *indexArgs, runCmd func(*exec.Cmd) error) error {
	// An index should never take longer than an hour.
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	if len(o.Branches) != 1 {
		return fmt.Errorf("zoekt-archive-index only supports 1 branch, got %v", o.Branches)
	}

	commit := o.Branches[0].Version
	args := []string{
		"-name", o.Name,
		"-commit", commit,
		"-branch", o.Branches[0].Name,
	}

	// Even though we check for incremental in this process, we still pass it
	// in just in case we regress in how we check in process. We will still
	// notice thanks to metrics and increased load on gitserver.
	if o.Incremental {
		args = append(args, "-incremental")
	}

	if o.DownloadLimitMBPS != "" {
		args = append(args, "-download-limit-mbps", o.DownloadLimitMBPS)
	}

	args = append(args, o.BuildOptions().Args()...)

	args = append(args, o.Root.ResolveReference(&url.URL{Path: fmt.Sprintf("/.internal/git/%s/tar/%s", o.Name, commit)}).String())

	cmd := exec.CommandContext(ctx, "zoekt-archive-index", args...)
	// Prevent prompting
	cmd.Stdin = &bytes.Buffer{}
	return runCmd(cmd)
}

func gitIndex(o *indexArgs, runCmd func(*exec.Cmd) error) error {
	if len(o.Branches) == 0 {
		return errors.New("zoekt-git-index requires 1 or more branches")
	}

	buildOptions := o.BuildOptions()

	// An index should never take longer than an hour.
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	gitDir, err := tmpGitDir(o.Name)
	if err != nil {
		return err
	}
	// We intentionally leave behind gitdir if indexing failed so we can
	// investigate. This is only during the experimental phase of indexing a
	// clone. So don't defer os.RemoveAll here

	// Create a repo to fetch into
	cmd := exec.CommandContext(ctx, "git",
		// use a random default branch. This is so that HEAD isn't a symref to a
		// branch that is indexed. For example if you are indexing
		// HEAD,master. Then HEAD would be pointing to master by default.
		"-c", "init.defaultBranch=BB0FOFCH32",
		"init",
		// we don't need a working copy
		"--bare",
		gitDir)
	cmd.Stdin = &bytes.Buffer{}
	if err := runCmd(cmd); err != nil {
		return err
	}

	// We shallow fetch each commit specified in zoekt.Branches. This requires
	// the server to have configured both uploadpack.allowAnySHA1InWant and
	// uploadpack.allowFilter. (See gitservice.go in the Sourcegraph repository)
	cloneURL := o.Root.ResolveReference(&url.URL{Path: path.Join("/.internal/git", o.Name)}).String()
	fetchArgs := []string{"-C", gitDir, "-c", "protocol.version=2", "fetch", "--depth=1", cloneURL}
	for _, b := range o.Branches {
		fetchArgs = append(fetchArgs, b.Version)
	}
	cmd = exec.CommandContext(ctx, "git", fetchArgs...)
	cmd.Stdin = &bytes.Buffer{}
	if err := runCmd(cmd); err != nil {
		return err
	}

	// We then create the relevant refs for each fetched commit.
	for _, b := range o.Branches {
		ref := b.Name
		if ref != "HEAD" {
			ref = "refs/heads/" + ref
		}
		cmd = exec.CommandContext(ctx, "git", "-C", gitDir, "update-ref", ref, b.Version)
		cmd.Stdin = &bytes.Buffer{}
		if err := runCmd(cmd); err != nil {
			return fmt.Errorf("failed update-ref %s to %s: %w", ref, b.Version, err)
		}
	}

	// zoekt.name is used by zoekt-git-index to set the repository name.
	cmd = exec.CommandContext(ctx, "git", "-C", gitDir, "config", "zoekt.name", o.Name)
	cmd.Stdin = &bytes.Buffer{}
	if err := runCmd(cmd); err != nil {
		return err
	}

	for key, value := range buildOptions.RepositoryDescription.RawConfig {
		cmd = exec.CommandContext(ctx, "git", "-C", gitDir, "config", "zoekt."+key, value)
		cmd.Stdin = &bytes.Buffer{}
		if err := runCmd(cmd); err != nil {
			return err
		}
	}

	args := []string{
		"-submodules=false",
	}

	// Even though we check for incremental in this process, we still pass it
	// in just in case we regress in how we check in process. We will still
	// notice thanks to metrics and increased load on gitserver.
	if o.Incremental {
		args = append(args, "-incremental")
	}

	var branches []string
	for _, b := range o.Branches {
		branches = append(branches, b.Name)
	}
	args = append(args, "-branches", strings.Join(branches, ","))

	args = append(args, buildOptions.Args()...)
	args = append(args, gitDir)

	cmd = exec.CommandContext(ctx, "zoekt-git-index", args...)
	cmd.Stdin = &bytes.Buffer{}
	if err := runCmd(cmd); err != nil {
		return err
	}

	// Do not return error, since we have successfully indexed. Just log it
	if err := os.RemoveAll(gitDir); err != nil {
		log.Printf("WARN: failed to cleanup %s after successfully indexing %s: %v", gitDir, o.String(), err)
	}

	return nil
}

func tmpGitDir(name string) (string, error) {
	abs := url.QueryEscape(name)
	if len(abs) > 200 {
		h := sha1.New()
		io.WriteString(h, abs)
		abs = abs[:200] + fmt.Sprintf("%x", h.Sum(nil))[:8]
	}
	dir := filepath.Join(os.TempDir(), abs+".git")
	if _, err := os.Stat(dir); err == nil {
		if err := os.RemoveAll(dir); err != nil {
			return "", err
		}
	}
	return dir, nil
}
