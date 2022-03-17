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

// Package gitindex provides functions for indexing Git repositories.
package gitindex

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
	"github.com/google/zoekt/ignore"

	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	git "github.com/go-git/go-git/v5"
)

// RepoModTime returns the time of last fetch of a git repository.
func RepoModTime(dir string) (time.Time, error) {
	var last time.Time
	refDir := filepath.Join(dir, "refs")
	if _, err := os.Lstat(refDir); err == nil {
		if err := filepath.Walk(refDir,
			func(_ string, fi os.FileInfo, _ error) error {
				if !fi.IsDir() && last.Before(fi.ModTime()) {
					last = fi.ModTime()
				}
				return nil
			}); err != nil {
			return last, err
		}
	}

	// git gc compresses refs into the following file:
	for _, fn := range []string{"info/refs", "packed-refs"} {
		if fi, err := os.Lstat(filepath.Join(dir, fn)); err == nil && !fi.IsDir() && last.Before(fi.ModTime()) {
			last = fi.ModTime()
		}
	}

	return last, nil
}

// FindGitRepos finds directories holding git repositories below the
// given directory. It will find both bare and the ".git" dirs in
// non-bare repositories. It returns the full path including the dir
// passed in.
func FindGitRepos(dir string) ([]string, error) {
	arg, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	var dirs []string
	if err := filepath.Walk(arg, func(name string, fi os.FileInfo, err error) error {
		// Best-effort, ignore filepath.Walk failing
		if err != nil {
			return nil
		}

		if fi, err := os.Lstat(filepath.Join(name, ".git")); err == nil && fi.IsDir() {
			dirs = append(dirs, filepath.Join(name, ".git"))
			return filepath.SkipDir
		}

		if !strings.HasSuffix(name, ".git") || !fi.IsDir() {
			return nil
		}

		fi, err = os.Lstat(filepath.Join(name, "objects"))
		if err != nil || !fi.IsDir() {
			return nil
		}

		dirs = append(dirs, name)
		return filepath.SkipDir
	}); err != nil {
		return nil, err
	}

	return dirs, nil
}

// setTemplates fills in URL templates for known git hosting
// sites.
func setTemplates(repo *zoekt.Repository, u *url.URL, typ string) error {
	repo.URL = u.String()
	switch typ {
	case "gitiles":
		/// eg. https://gerrit.googlesource.com/gitiles/+/master/tools/run_dev.sh#20
		repo.CommitURLTemplate = u.String() + "/+/{{.Version}}"
		repo.FileURLTemplate = u.String() + "/+/{{.Version}}/{{.Path}}"
		repo.LineFragmentTemplate = "#{{.LineNumber}}"
	case "github":
		// eg. https://github.com/hanwen/go-fuse/blob/notify/genversion.sh#L10
		repo.CommitURLTemplate = u.String() + "/commit/{{.Version}}"
		repo.FileURLTemplate = u.String() + "/blob/{{.Version}}/{{.Path}}"
		repo.LineFragmentTemplate = "#L{{.LineNumber}}"
	case "cgit":
		// http://git.savannah.gnu.org/cgit/lilypond.git/tree/elisp/lilypond-mode.el?h=dev/philh&id=b2ca0fefe3018477aaca23b6f672c7199ba5238e#n100
		repo.CommitURLTemplate = u.String() + "/commit/?id={{.Version}}"
		repo.FileURLTemplate = u.String() + "/tree/{{.Path}}/?id={{.Version}}"
		repo.LineFragmentTemplate = "#n{{.LineNumber}}"
	case "gitweb":
		// https://gerrit.libreoffice.org/gitweb?p=online.git;a=blob;f=Makefile.am;h=cfcfd7c36fbae10e269653dc57a9b68c92d4c10b;hb=848145503bf7b98ce4a4aa0a858a0d71dd0dbb26#l10
		repo.FileURLTemplate = u.String() + ";a=blob;f={{.Path}};hb={{.Version}}"
		repo.CommitURLTemplate = u.String() + ";a=commit;h={{.Version}}"
		repo.LineFragmentTemplate = "#l{{.LineNumber}}"
	case "source.bazel.build":
		// https://source.bazel.build/bazel/+/57bc201346e61c62a921c1cbf32ad24f185c10c9
		// https://source.bazel.build/bazel/+/57bc201346e61c62a921c1cbf32ad24f185c10c9:tools/cpp/BUILD.empty;l=10
		repo.CommitURLTemplate = u.String() + "/+/{{.Version}}"
		repo.FileURLTemplate = u.String() + "/+/{{.Version}}:{{.Path}}"
		repo.LineFragmentTemplate = ";l={{.LineNumber}}"
	case "bitbucket-server":
		// https://<bitbucketserver-host>/projects/<project>/repos/<repo>/commits/5be7ca73b898bf17a08e607918accfdeafe1e0bc
		// https://<bitbucketserver-host>/projects/<project>/repos/<repo>/browse/<file>?at=5be7ca73b898bf17a08e607918accfdeafe1e0bc
		repo.CommitURLTemplate = u.String() + "/commits/{{.Version}}"
		repo.FileURLTemplate = u.String() + "/{{.Path}}?at={{.Version}}"
		repo.LineFragmentTemplate = "#{{.LineNumber}}"
	case "gitlab":
		repo.CommitURLTemplate = u.String() + "/commit/{{.Version}}"
		repo.FileURLTemplate = u.String() + "/blob/{{.Version}}/{{.Path}}"
		repo.LineFragmentTemplate = "#L{{.LineNumber}}"
	default:
		return fmt.Errorf("URL scheme type %q unknown", typ)
	}
	return nil
}

// getCommit returns a tree object for the given reference.
func getCommit(repo *git.Repository, prefix, ref string) (*object.Commit, error) {
	sha1, err := repo.ResolveRevision(plumbing.Revision(ref))
	// ref might be a branch name (e.g. "master") add branch prefix and try again.
	if err != nil {
		sha1, err = repo.ResolveRevision(plumbing.Revision(filepath.Join(prefix, ref)))
	}
	if err != nil {
		return nil, err
	}

	commitObj, err := repo.CommitObject(*sha1)
	if err != nil {
		return nil, err
	}
	return commitObj, nil
}

func configLookupRemoteURL(cfg *config.Config, key string) string {
	rc := cfg.Remotes[key]
	if rc == nil || len(rc.URLs) == 0 {
		return ""
	}
	return rc.URLs[0]
}

func setTemplatesFromConfig(desc *zoekt.Repository, repoDir string) error {
	repo, err := git.PlainOpen(repoDir)
	if err != nil {
		return err
	}

	cfg, err := repo.Config()
	if err != nil {
		return err
	}

	sec := cfg.Raw.Section("zoekt")

	webURLStr := sec.Options.Get("web-url")
	webURLType := sec.Options.Get("web-url-type")

	if webURLType != "" && webURLStr != "" {
		webURL, err := url.Parse(webURLStr)
		if err != nil {
			return err
		}
		if err := setTemplates(desc, webURL, webURLType); err != nil {
			return err
		}
	} else if webURLStr != "" {
		desc.URL = webURLStr
	}

	name := sec.Options.Get("name")
	if name != "" {
		desc.Name = name
	} else {
		remoteURL := configLookupRemoteURL(cfg, "origin")
		if remoteURL == "" {
			return nil
		}
		u, err := url.Parse(remoteURL)
		if err != nil {
			return err
		}
		if err := SetTemplatesFromOrigin(desc, u); err != nil {
			return err
		}
	}

	id, _ := strconv.ParseUint(sec.Options.Get("repoid"), 10, 32)
	desc.ID = uint32(id)

	if desc.RawConfig == nil {
		desc.RawConfig = map[string]string{}
	}
	for _, o := range sec.Options {
		desc.RawConfig[o.Key] = o.Value
	}

	// Ranking info.

	// Github:
	traction := 0
	for _, s := range []string{"github-stars", "github-forks", "github-watchers", "github-subscribers"} {
		f, err := strconv.Atoi(sec.Options.Get(s))
		if err == nil {
			traction += f
		}
	}

	if strings.Contains(desc.Name, "googlesource.com/") && traction == 0 {
		// Pretend everything on googlesource.com has 1000
		// github stars.
		traction = 1000
	}

	if traction > 0 {
		l := math.Log(float64(traction))
		desc.Rank = uint16((1.0 - 1.0/math.Pow(1+l, 0.6)) * 10000)
	}

	return nil
}

// SetTemplatesFromOrigin fills in templates based on the origin URL.
func SetTemplatesFromOrigin(desc *zoekt.Repository, u *url.URL) error {
	desc.Name = filepath.Join(u.Host, strings.TrimSuffix(u.Path, ".git"))

	if strings.HasSuffix(u.Host, ".googlesource.com") {
		return setTemplates(desc, u, "gitiles")
	} else if u.Host == "github.com" {
		u.Path = strings.TrimSuffix(u.Path, ".git")
		return setTemplates(desc, u, "github")
	} else {
		return fmt.Errorf("unknown git hosting site %q", u)
	}
}

// The Options structs controls details of the indexing process.
type Options struct {
	// The repository to be indexed.
	RepoDir string

	// If set, follow submodule links. This requires RepoCacheDir to be set.
	Submodules bool

	// If set, skip indexing if the existing index shard is newer
	// than the refs in the repository.
	Incremental bool

	// Don't error out if some branch is missing
	AllowMissingBranch bool

	// Specifies the root of a Repository cache. Needed for submodule indexing.
	RepoCacheDir string

	// Indexing options.
	BuildOptions build.Options

	// Prefix of the branch to index, e.g. `remotes/origin`.
	BranchPrefix string

	// List of branch names to index, e.g. []string{"HEAD", "stable"}
	Branches []string
}

func expandBranches(repo *git.Repository, bs []string, prefix string) ([]string, error) {
	var result []string
	for _, b := range bs {
		// Sourcegraph: We disable resolving refs. We want to return the exact ref
		// requested so we can match it up.
		if b == "HEAD" && false {
			ref, err := repo.Head()
			if err != nil {
				return nil, err
			}

			result = append(result, strings.TrimPrefix(ref.Name().String(), prefix))
			continue
		}

		if strings.Contains(b, "*") {
			iter, err := repo.Branches()
			if err != nil {
				return nil, err
			}

			defer iter.Close()
			for {
				ref, err := iter.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					return nil, err
				}

				name := ref.Name().Short()
				if matched, err := filepath.Match(b, name); err != nil {
					return nil, err
				} else if !matched {
					continue
				}

				result = append(result, strings.TrimPrefix(name, prefix))
			}
			continue
		}

		result = append(result, b)
	}

	return result, nil
}

// IndexGitRepo indexes the git repository as specified by the options.
func IndexGitRepo(opts Options) error {
	// Set max thresholds, since we use them in this function.
	opts.BuildOptions.SetDefaults()
	if opts.RepoDir == "" {
		return fmt.Errorf("gitindex: must set RepoDir")
	}

	opts.BuildOptions.RepositoryDescription.Source = opts.RepoDir
	repo, err := git.PlainOpen(opts.RepoDir)
	if err != nil {
		return fmt.Errorf("git.PlainOpen: %w", err)
	}

	if err := setTemplatesFromConfig(&opts.BuildOptions.RepositoryDescription, opts.RepoDir); err != nil {
		log.Printf("setTemplatesFromConfig(%s): %s", opts.RepoDir, err)
	}

	branches, err := expandBranches(repo, opts.Branches, opts.BranchPrefix)
	if err != nil {
		return fmt.Errorf("expandBranches: %w", err)
	}
	for _, b := range branches {
		commit, err := getCommit(repo, opts.BranchPrefix, b)
		if err != nil {
			if opts.AllowMissingBranch && err.Error() == "reference not found" {
				continue
			}

			return fmt.Errorf("getCommit: %w", err)
		}

		opts.BuildOptions.RepositoryDescription.Branches = append(opts.BuildOptions.RepositoryDescription.Branches, zoekt.RepositoryBranch{
			Name:    b,
			Version: commit.Hash.String(),
		})

		if when := commit.Committer.When; when.After(opts.BuildOptions.RepositoryDescription.LatestCommitDate) {
			opts.BuildOptions.RepositoryDescription.LatestCommitDate = when
		}
	}

	if opts.Incremental && opts.BuildOptions.IncrementalSkipIndexing() {
		return nil
	}

	// branch => (path, sha1) => repo.
	var repos map[fileKey]BlobLocation

	// fileKey => branches
	var branchMap map[fileKey][]string

	// Branch => Repo => SHA1
	var branchVersions map[string]map[string]plumbing.Hash

	if opts.BuildOptions.IsDelta {
		repos, branchMap, branchVersions, opts.BuildOptions.ChangedOrRemovedFiles, err = prepareDeltaBuild(opts, repo)
		if err != nil {
			return fmt.Errorf("preparing delta build: %w", err)
		}
	} else {
		repos, branchMap, branchVersions, err = prepareNormalBuild(opts, repo)
		if err != nil {
			return fmt.Errorf("preparing normal build: %w", err)
		}
	}

	reposByPath := map[string]BlobLocation{}
	for key, location := range repos {
		reposByPath[key.SubRepoPath] = location
	}

	opts.BuildOptions.SubRepositories = map[string]*zoekt.Repository{}
	for path, location := range reposByPath {
		tpl := opts.BuildOptions.RepositoryDescription
		if path != "" {
			tpl = zoekt.Repository{URL: location.URL.String()}
			if err := SetTemplatesFromOrigin(&tpl, location.URL); err != nil {
				log.Printf("setTemplatesFromOrigin(%s, %s): %s", path, location.URL, err)
			}
		}
		opts.BuildOptions.SubRepositories[path] = &tpl
	}

	for _, br := range opts.BuildOptions.RepositoryDescription.Branches {
		for path, repo := range opts.BuildOptions.SubRepositories {
			id := branchVersions[br.Name][path]
			repo.Branches = append(repo.Branches, zoekt.RepositoryBranch{
				Name:    br.Name,
				Version: id.String(),
			})
		}
	}

	builder, err := build.NewBuilder(opts.BuildOptions)
	if err != nil {
		return fmt.Errorf("build.NewBuilder: %w", err)
	}
	// we don't need to check error, since we either already have an error, or
	// we returning the first call to builder.Finish.
	defer builder.Finish() // nolint:errcheck

	var names []string
	fileKeys := map[string][]fileKey{}
	for key := range repos {
		n := key.FullPath()
		fileKeys[n] = append(fileKeys[n], key)
		names = append(names, n)
	}

	sort.Strings(names)
	names = uniq(names)

	for _, name := range names {
		keys := fileKeys[name]

		for _, key := range keys {
			brs := branchMap[key]
			blob, err := repos[key].Repo.BlobObject(key.ID)
			if err != nil {
				return err
			}

			if blob.Size > int64(opts.BuildOptions.SizeMax) && !opts.BuildOptions.IgnoreSizeMax(key.FullPath()) {
				if err := builder.Add(zoekt.Document{
					SkipReason:        fmt.Sprintf("file size %d exceeds maximum size %d", blob.Size, opts.BuildOptions.SizeMax),
					Name:              key.FullPath(),
					Branches:          brs,
					SubRepositoryPath: key.SubRepoPath,
				}); err != nil {
					return err
				}
				continue
			}

			contents, err := blobContents(blob)
			if err != nil {
				return err
			}
			if err := builder.Add(zoekt.Document{
				SubRepositoryPath: key.SubRepoPath,
				Name:              key.FullPath(),
				Content:           contents,
				Branches:          brs,
			}); err != nil {
				return fmt.Errorf("error adding document with name %s: %w", key.FullPath(), err)
			}
		}
	}
	return builder.Finish()
}

func newIgnoreMatcher(tree *object.Tree) (*ignore.Matcher, error) {
	ignoreFile, err := tree.File(ignore.IgnoreFile)
	if err == object.ErrFileNotFound {
		return &ignore.Matcher{}, nil
	}
	if err != nil {
		return nil, err
	}
	content, err := ignoreFile.Contents()
	if err != nil {
		return nil, err
	}
	return ignore.ParseIgnoreFile(strings.NewReader(content))
}

func prepareDeltaBuild(options Options, repository *git.Repository) (repos map[fileKey]BlobLocation, branchMap map[fileKey][]string, branchVersions map[string]map[string]plumbing.Hash, changedOrDeletedPaths []string, err error) {
	// discover what commits we indexed during our last build

	existingRepository, err := options.BuildOptions.RepositoryMetadata()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to get repository metadata: %w", err)
	}

	if existingRepository == nil {
		return nil, nil, nil, nil, fmt.Errorf("no existing shards found for repository")
	}

	// Check to see if the branch set is consistent with what we last indexed.
	// If it isn't consistent, that we can't proceed with a delta build (and the caller should fall back to a
	// normal one).

	indexState := build.CompareBranches(existingRepository.Branches, options.BuildOptions.RepositoryDescription.Branches)
	if indexState == build.IndexStateBranchSet {
		var existingBranchNames []string
		for _, b := range existingRepository.Branches {
			existingBranchNames = append(existingBranchNames, b.Name)
		}

		var optionsBranchNames []string
		for _, b := range options.BuildOptions.RepositoryDescription.Branches {
			optionsBranchNames = append(optionsBranchNames, b.Name)
		}

		existingBranchList := strings.Join(existingBranchNames, ", ")
		optionsBranchList := strings.Join(optionsBranchNames, ", ")

		return nil, nil, nil, nil, fmt.Errorf("requested branch set in build options (%q) != branch set found on disk (%q) - branch set must be the same for delta shards", optionsBranchList, existingBranchList)
	}

	// branch => (path, sha1) => repo.
	repos = map[fileKey]BlobLocation{}

	// fileKey => branches
	branchMap = map[fileKey][]string{}

	// Branch => Repo => SHA1
	//branchVersions := map[string]map[string]plumbing.Hash{}

	// branch name -> git worktree at most current commit
	branchToCurrentTree := make(map[string]*object.Tree, len(options.Branches))

	for _, b := range options.Branches {
		commit, err := getCommit(repository, options.BranchPrefix, b)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("getting last current commit for branch %q: %w", b, err)
		}

		tree, err := commit.Tree()
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("getting current git tree for branch %q: %w", b, err)
		}

		branchToCurrentTree[b] = tree
	}

	rawURL := options.BuildOptions.RepositoryDescription.URL
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("paring repository URL %q: %w", rawURL, err)
	}

	// TODO: Support repository submodules for delta builds
	// For this prototype, we are ignoring repository submodules, which means that we can use the same
	// blob location for all files
	hackSharedBlobLocation := BlobLocation{
		Repo: repository,
		URL:  u,
	}

	// loop over all branches, calculate the diff between our
	// last indexed commit and the current commit, and add files mentioned in the diff
	for _, branch := range existingRepository.Branches {
		lastIndexedCommit, err := getCommit(repository, "", branch.Version)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("getting last indexed commit for branch %q: %w", branch.Name, err)
		}

		lastIndexedTree, err := lastIndexedCommit.Tree()
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("getting lasted indexed git tree for branch %q: %w", branch.Name, err)
		}

		changes, err := lastIndexedTree.Diff(branchToCurrentTree[branch.Name])
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("generating changeset for branch %q: %w", branch.Name, err)
		}

		patch, err := changes.Patch()
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("generating patch from changeset for branch %q: %w", branch.Name, err)
		}

		for _, fp := range patch.FilePatches() {
			fromFile, toFile := fp.Files()

			if fromFile == nil {
				// file added
				file := fileKey{Path: toFile.Path(), ID: toFile.Hash()}
				repos[file] = hackSharedBlobLocation
				branchMap[file] = append(branchMap[file], branch.Name)
				continue
			}

			changedOrDeletedFile := fileKey{Path: fromFile.Path(), ID: fromFile.Hash()}

			// If the file is either modified or deleted, we need to add ALL versions
			// of this file (across all branches) to the build
			for b, currentTree := range branchToCurrentTree {
				f, err := currentTree.File(changedOrDeletedFile.Path)
				if err != nil {
					// the file doesn't exist in this branch
					if errors.Is(err, object.ErrFileNotFound) {
						continue
					}

					return nil, nil, nil, nil, fmt.Errorf("getting hash for file %q in branch %q: %w", changedOrDeletedFile.Path, b, err)
				}

				file := fileKey{Path: changedOrDeletedFile.Path, ID: f.ID()}

				repos[file] = hackSharedBlobLocation
				branchMap[file] = append(branchMap[file], b)
			}

			changedOrDeletedPaths = append(changedOrDeletedPaths, changedOrDeletedFile.Path)
		}
	}

	// we need to de-duplicate the branch map before returning it - it's possible for the same
	// branch to have been added multiple times if a file has been modified across multiple commits

	for file, branches := range branchMap {
		sort.Strings(branches)
		branchMap[file] = uniq(branches)
	}

	// we also need to de-duplicate the list of changed or deleted file paths, it's also possible to have duplicates
	// for the same reasoning as above

	sort.Strings(changedOrDeletedPaths)
	changedOrDeletedPaths = uniq(changedOrDeletedPaths)

	return repos, branchMap, nil, changedOrDeletedPaths, nil
}

func prepareNormalBuild(options Options, repository *git.Repository) (repos map[fileKey]BlobLocation, branchMap map[fileKey][]string, branchVersions map[string]map[string]plumbing.Hash, err error) {
	var repoCache *RepoCache
	if options.Submodules {
		repoCache = NewRepoCache(options.RepoCacheDir)
	}

	// branch => (path, sha1) => repo.
	repos = map[fileKey]BlobLocation{}

	// fileKey => branches
	branchMap = map[fileKey][]string{}

	// Branch => Repo => SHA1
	branchVersions = map[string]map[string]plumbing.Hash{}

	branches, err := expandBranches(repository, options.Branches, options.BranchPrefix)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("expandBranches: %w", err)
	}

	for _, b := range branches {
		commit, err := getCommit(repository, options.BranchPrefix, b)
		if err != nil {
			if options.AllowMissingBranch && err.Error() == "reference not found" {
				continue
			}

			return nil, nil, nil, fmt.Errorf("getCommit: %w", err)
		}

		tree, err := commit.Tree()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("commit.Tree: %w", err)
		}

		ig, err := newIgnoreMatcher(tree)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("newIgnoreMatcher: %w", err)
		}

		files, subVersions, err := TreeToFiles(repository, tree, options.BuildOptions.RepositoryDescription.URL, repoCache)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("TreeToFiles: %w", err)
		}
		for k, v := range files {
			if ig.Match(k.Path) {
				continue
			}
			repos[k] = v
			branchMap[k] = append(branchMap[k], b)
		}

		branchVersions[b] = subVersions
	}

	return repos, branchMap, branchVersions, nil
}

func blobContents(blob *object.Blob) ([]byte, error) {
	r, err := blob.Reader()
	if err != nil {
		return nil, err
	}
	defer r.Close()

	var buf bytes.Buffer
	buf.Grow(int(blob.Size))
	_, err = buf.ReadFrom(r)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func uniq(ss []string) []string {
	result := ss[:0]
	var last string
	for i, s := range ss {
		if i == 0 || s != last {
			result = append(result, s)
		}
		last = s
	}
	return result
}
