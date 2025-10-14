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

package gitindex

import (
	"fmt"
	"io"
	"log"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/sourcegraph/zoekt/ignore"
)

// RepoWalker walks one or more commit trees, collecting the files to index in its Files map.
//
// It also recurses into submodules if Options.Submodules is enabled.
type RepoWalker struct {
	Files map[fileKey]BlobLocation

	repo    *git.Repository
	repoURL *url.URL

	// Path => SubmoduleEntry
	submodules map[string]*SubmoduleEntry
	repoCache  *RepoCache
}

// subURL returns the URL for a submodule.
func (rw *RepoWalker) subURL(relURL string) (*url.URL, error) {
	if rw.repoURL == nil {
		return nil, fmt.Errorf("no URL for base repo")
	}
	if strings.HasPrefix(relURL, "../") {
		u := *rw.repoURL
		u.Path = path.Join(u.Path, relURL)
		return &u, nil
	}

	return url.Parse(relURL)
}

// NewRepoWalker creates a new RepoWalker.
func NewRepoWalker(r *git.Repository, repoURL string, repoCache *RepoCache) *RepoWalker {
	u, _ := url.Parse(repoURL)
	return &RepoWalker{
		repo:      r,
		repoURL:   u,
		Files:     map[fileKey]BlobLocation{},
		repoCache: repoCache,
	}
}

// parseModuleMap initializes rw.submodules.
func (rw *RepoWalker) parseModuleMap(t *object.Tree) error {
	if rw.repoCache == nil {
		return nil
	}
	modEntry, _ := t.File(".gitmodules")
	if modEntry != nil {
		c, err := blobContents(&modEntry.Blob)
		if err != nil {
			return fmt.Errorf("blobContents: %w", err)
		}
		mods, err := ParseGitModules(c)
		if err != nil {
			return fmt.Errorf("ParseGitModules: %w", err)
		}
		rw.submodules = map[string]*SubmoduleEntry{}
		for _, entry := range mods {
			rw.submodules[entry.Path] = entry
		}
	}
	return nil
}

// CollectFiles fetches the blob SHA1s for the tree. If repoCache is
// non-nil, recurse into submodules. In addition, it returns a mapping
// that indicates in which repo each SHA1 can be found.
//
// The collected files are available through the RepoWalker.Files map.
func (rw *RepoWalker) CollectFiles(t *object.Tree, branch string, ig *ignore.Matcher) (map[string]plumbing.Hash, error) {
	if err := rw.parseModuleMap(t); err != nil {
		return nil, fmt.Errorf("parseModuleMap: %w", err)
	}

	ig, err := newIgnoreMatcher(t)
	if err != nil {
		return nil, fmt.Errorf("newIgnoreMatcher: %w", err)
	}

	tw := object.NewTreeWalker(t, true, make(map[plumbing.Hash]bool))
	defer tw.Close()

	// Path => commit SHA1
	subRepoVersions := make(map[string]plumbing.Hash)
	for {
		name, entry, err := tw.Next()
		if err == io.EOF {
			break
		}
		if err := rw.handleEntry(name, &entry, branch, subRepoVersions, ig); err != nil {
			return nil, fmt.Errorf("handleEntry: %w", err)
		}
	}
	return subRepoVersions, nil
}

func (rw *RepoWalker) tryHandleSubmodule(p string, id *plumbing.Hash, branch string, subRepoVersions map[string]plumbing.Hash, ig *ignore.Matcher) error {
	if err := rw.handleSubmodule(p, id, branch, subRepoVersions, ig); err != nil {
		log.Printf("submodule %s: ignoring error %v", p, err)
	}
	return nil
}

func (rw *RepoWalker) handleSubmodule(p string, id *plumbing.Hash, branch string, subRepoVersions map[string]plumbing.Hash, ig *ignore.Matcher) error {
	submod := rw.submodules[p]
	if submod == nil {
		return fmt.Errorf("no entry for submodule path %q", rw.repoURL)
	}

	subURL, err := rw.subURL(submod.URL)
	if err != nil {
		return err
	}

	subRepo, err := rw.repoCache.Open(subURL)
	if err != nil {
		return err
	}

	obj, err := subRepo.CommitObject(*id)
	if err != nil {
		return err
	}
	tree, err := subRepo.TreeObject(obj.TreeHash)
	if err != nil {
		return err
	}

	subRepoVersions[p] = *id

	sw := NewRepoWalker(subRepo, subURL.String(), rw.repoCache)
	subVersions, err := sw.CollectFiles(tree, branch, ig)
	if err != nil {
		return err
	}
	for k, repo := range sw.Files {
		rw.Files[fileKey{
			SubRepoPath: filepath.Join(p, k.SubRepoPath),
			Path:        k.Path,
			ID:          k.ID,
		}] = repo
	}
	for k, v := range subVersions {
		subRepoVersions[filepath.Join(p, k)] = v
	}
	return nil
}

func (rw *RepoWalker) handleEntry(p string, e *object.TreeEntry, branch string, subRepoVersions map[string]plumbing.Hash, ig *ignore.Matcher) error {
	if e.Mode == filemode.Submodule {
		if rw.repoCache != nil {
			// Index the submodule using repo cache
			if err := rw.tryHandleSubmodule(p, &e.Hash, branch, subRepoVersions, ig); err != nil {
				return fmt.Errorf("submodule %s: %v", p, err)
			}
		} else {
			// Record the commit ID for the submodule path
			// This will be the submodule's commit hash, not the parent's
			subRepoVersions[p] = e.Hash
		}
	}

	switch e.Mode {
	case filemode.Regular, filemode.Executable, filemode.Symlink:
	default:
		return nil
	}

	// Skip ignored files
	if ig.Match(p) {
		return nil
	}

	key := fileKey{Path: p, ID: e.Hash}
	if existing, ok := rw.Files[key]; ok {
		existing.Branches = append(existing.Branches, branch)
		rw.Files[key] = existing
	} else {
		rw.Files[key] = BlobLocation{GitRepo: rw.repo, URL: rw.repoURL, Branches: []string{branch}}
	}

	return nil
}

// fileKey describes a blob at a location in the final tree. We also
// record the subrepository from where it came.
type fileKey struct {
	SubRepoPath string
	Path        string
	ID          plumbing.Hash
}

func (k *fileKey) FullPath() string {
	return filepath.Join(k.SubRepoPath, k.Path)
}

// BlobLocation holds the repo where the blob can be found, plus other information
// needed for indexing like its branches.
type BlobLocation struct {
	GitRepo *git.Repository
	URL     *url.URL

	// Branches is the list of branches that contain the blob.
	Branches []string
}

func (l *BlobLocation) Blob(id *plumbing.Hash) ([]byte, error) {
	blob, err := l.GitRepo.BlobObject(*id)
	if err != nil {
		return nil, err
	}
	return blobContents(blob)
}
