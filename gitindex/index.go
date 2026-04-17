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
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/ignore"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/query"

	git "github.com/go-git/go-git/v5"
)

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
	if u.Scheme == "ssh+git" {
		u.Scheme = "https"
		u.User = nil
	}

	// helper to generate u.JoinPath as a template
	varVersion := ".Version"
	varPath := ".Path"
	urlJoinPath := func(elem ...string) string {
		elem = append([]string{u.String()}, elem...)
		var parts []string
		for _, e := range elem {
			if e == varVersion || e == varPath {
				parts = append(parts, e)
			} else {
				parts = append(parts, strconv.Quote(e))
			}
		}
		return fmt.Sprintf("{{URLJoinPath %s}}", strings.Join(parts, " "))
	}

	repo.URL = u.String()
	switch typ {
	case "gitiles":
		// eg. https://gerrit.googlesource.com/gitiles/+/master/tools/run_dev.sh#20
		repo.CommitURLTemplate = urlJoinPath("+", varVersion)
		repo.FileURLTemplate = urlJoinPath("+", varVersion, varPath)
		repo.LineFragmentTemplate = "#{{.LineNumber}}"
	case "github":
		// eg. https://github.com/hanwen/go-fuse/blob/notify/genversion.sh#L10
		repo.CommitURLTemplate = urlJoinPath("commit", varVersion)
		repo.FileURLTemplate = urlJoinPath("blob", varVersion, varPath)
		repo.LineFragmentTemplate = "#L{{.LineNumber}}"
	case "cgit":
		// http://git.savannah.gnu.org/cgit/lilypond.git/tree/elisp/lilypond-mode.el?h=dev/philh&id=b2ca0fefe3018477aaca23b6f672c7199ba5238e#n100
		repo.CommitURLTemplate = urlJoinPath("commit") + "/?id={{.Version}}"
		repo.FileURLTemplate = urlJoinPath("tree", varPath) + "/?id={{.Version}}"
		repo.LineFragmentTemplate = "#n{{.LineNumber}}"
	case "gitweb":
		// https://gerrit.libreoffice.org/gitweb?p=online.git;a=blob;f=Makefile.am;h=cfcfd7c36fbae10e269653dc57a9b68c92d4c10b;hb=848145503bf7b98ce4a4aa0a858a0d71dd0dbb26#l10
		repo.FileURLTemplate = u.String() + ";a=blob;f={{.Path}};hb={{.Version}}"
		repo.CommitURLTemplate = u.String() + ";a=commit;h={{.Version}}"
		repo.LineFragmentTemplate = "#l{{.LineNumber}}"
	case "source.bazel.build":
		// https://source.bazel.build/bazel/+/57bc201346e61c62a921c1cbf32ad24f185c10c9
		// https://source.bazel.build/bazel/+/57bc201346e61c62a921c1cbf32ad24f185c10c9:tools/cpp/BUILD.empty;l=10
		repo.CommitURLTemplate = u.String() + "/%2B/{{.Version}}"
		repo.FileURLTemplate = u.String() + "/%2B/{{.Version}}:{{.Path}}"
		repo.LineFragmentTemplate = ";l={{.LineNumber}}"
	case "bitbucket-server":
		// https://<bitbucketserver-host>/projects/<project>/repos/<repo>/commits/5be7ca73b898bf17a08e607918accfdeafe1e0bc
		// https://<bitbucketserver-host>/projects/<project>/repos/<repo>/browse/<file>?at=5be7ca73b898bf17a08e607918accfdeafe1e0bc
		repo.CommitURLTemplate = urlJoinPath("commits", varVersion)
		repo.FileURLTemplate = urlJoinPath(varPath) + "?at={{.Version}}"
		repo.LineFragmentTemplate = "#{{.LineNumber}}"
	case "gitlab":
		// https://gitlab.com/gitlab-org/omnibus-gitlab/-/commit/b152c864303dae0e55377a1e2c53c9592380ffed
		// https://gitlab.com/gitlab-org/omnibus-gitlab/-/blob/aad04155b3f6fc50ede88aedaee7fc624d481149/files/gitlab-config-template/gitlab.rb.template
		repo.CommitURLTemplate = urlJoinPath("-/commit", varVersion)
		repo.FileURLTemplate = urlJoinPath("-/blob", varVersion, varPath)
		repo.LineFragmentTemplate = "#L{{.LineNumber}}"
	case "gitea":
		repo.CommitURLTemplate = urlJoinPath("commit", varVersion)
		// NOTE The `display=source` query parameter is required to disable file rendering.
		// Since line numbers are disabled in rendered files, you wouldn't be able to jump to
		// a line without `display=source`. This is supported since gitea 1.17.0.
		// When /src/{{.Version}} is used it will redirect to /src/commit/{{.Version}},
		// but the query  parameters are obmitted.
		repo.FileURLTemplate = urlJoinPath("src/commit", varVersion, varPath) + "?display=source"
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

func plainOpenRepo(repoDir string) (*git.Repository, error) {
	// Try repoDir as the repository root first so bare repositories open
	// correctly. If repoDir itself is not a repository, fall back to searching
	// for a .git entry to preserve compatibility with worktree paths.
	repo, err := git.PlainOpenWithOptions(repoDir, &git.PlainOpenOptions{
		EnableDotGitCommonDir: true,
	})
	if err == nil || !errors.Is(err, git.ErrRepositoryNotExists) {
		return repo, err
	}

	return git.PlainOpenWithOptions(repoDir, &git.PlainOpenOptions{
		DetectDotGit:          true,
		EnableDotGitCommonDir: true,
	})
}

func configLookupRemoteURL(cfg *config.Config, key string) string {
	rc := cfg.Remotes[key]
	if rc == nil || len(rc.URLs) == 0 {
		return ""
	}
	return rc.URLs[0]
}

var sshRelativeURLRegexp = regexp.MustCompile(`^([^@]+)@([^:]+):(.*)$`)

func setTemplatesFromConfig(desc *zoekt.Repository, repoDir string) error {
	repo, err := plainOpenRepo(repoDir)
	if err != nil {
		return err
	}

	cfg, err := repo.Config()
	if err != nil {
		return err
	}

	return setTemplatesFromRepoConfig(desc, cfg)
}

func setTemplatesFromRepo(desc *zoekt.Repository, repo *git.Repository, repoDir string) error {
	cfg, err := repo.Config()
	if err == nil {
		return setTemplatesFromRepoConfig(desc, cfg)
	}

	return setTemplatesFromConfig(desc, repoDir)
}

func setTemplatesFromRepoConfig(desc *zoekt.Repository, cfg *config.Config) error {
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
		if sm := sshRelativeURLRegexp.FindStringSubmatch(remoteURL); sm != nil {
			user := sm[1]
			host := sm[2]
			path := sm[3]

			remoteURL = fmt.Sprintf("ssh+git://%s@%s/%s", user, host, path)
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

	desc.TenantID, _ = strconv.Atoi(sec.Options.Get("tenantID"))

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

// This attempts to get a repo URL similar to the main repository template processing as in setTemplatesFromConfig()
func normalizeSubmoduleRemoteURL(cfg *config.Config) (string, error) {
	sec := cfg.Raw.Section("zoekt")
	remoteURL := sec.Options.Get("web-url")
	if remoteURL == "" {
		// fall back to "origin" remote
		remoteURL = configLookupRemoteURL(cfg, "origin")
		if remoteURL == "" {
			return "", nil
		}
	}

	if sm := sshRelativeURLRegexp.FindStringSubmatch(remoteURL); sm != nil {
		user := sm[1]
		host := sm[2]
		path := sm[3]

		remoteURL = fmt.Sprintf("ssh+git://%s@%s/%s", user, host, path)
	}

	u, err := url.Parse(remoteURL)
	if err != nil {
		return "", fmt.Errorf("unable to parse remote URL %q: %w", remoteURL, err)
	}

	if u.Scheme == "ssh+git" {
		u.Scheme = "https"
		u.User = nil
	}

	// Assume we cannot build templates for this URL, leave it empty
	if u.Scheme == "" {
		return "", nil
	}

	return u.String(), nil
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
	BuildOptions index.Options

	// Prefix of the branch to index, e.g. `remotes/origin`.
	BranchPrefix string

	// List of branch names to index, e.g. []string{"HEAD", "stable"}
	Branches []string

	// ResolveHEADToBranch resolves an attached HEAD to its short branch name in
	// indexed metadata. Detached HEAD remains HEAD.
	ResolveHEADToBranch bool

	// AllowDeltaBranchSetChange allows delta indexing when the requested branch
	// set differs from the existing index metadata. The conservative
	// implementation rewrites old shard metadata and tombstones all old live
	// paths before adding a full live delta layer for the new branch set.
	AllowDeltaBranchSetChange bool

	// DeltaShardNumberFallbackThreshold defines an upper limit (inclusive) on the number of preexisting shards
	// that can exist before attempting another delta build. If the number of preexisting shards exceeds this threshold,
	// then a normal build will be performed instead.
	//
	// If DeltaShardNumberFallbackThreshold is 0, then this fallback behavior is disabled:
	// a delta build will always be performed regardless of the number of preexisting shards.
	DeltaShardNumberFallbackThreshold uint64

	// DeltaAdmissionMode controls experimental cost-based delta admission. The
	// empty value preserves the historical delta behavior exactly.
	DeltaAdmissionMode string

	// DeltaAdmissionThresholds configures the stats-v1 admission mode. Zero
	// values are replaced with conservative defaults.
	DeltaAdmissionThresholds DeltaAdmissionThresholds

	// DeltaAdmissionLogPath, if non-empty, receives one JSON object per
	// stats-v1 delta admission decision.
	DeltaAdmissionLogPath string
}

const DeltaAdmissionModeStatsV1 = "stats-v1"

// DeltaAdmissionThresholds are experimental cost gates for stats-v1 delta
// admission. These are performance/debt heuristics only; correctness gates such
// as branch-set compatibility and option hashes are checked separately before
// these thresholds are considered.
//
// A delta is accepted only when every configured gate is satisfied. The first
// failed gate becomes the logged rejection reason, but later gates may also
// have failed.
type DeltaAdmissionThresholds struct {
	// MaxDeltaIndexedBytesRatio limits write mass:
	//
	//	candidate_indexed_bytes / live_indexed_bytes
	//
	// This is the primary delta-vs-full signal. Delta indexing rewrites whole
	// changed documents, so a tiny edit to a large file can still be expensive.
	MaxDeltaIndexedBytesRatio float64

	// MaxPhysicalLiveBytesRatio limits accumulated stacked-shard debt:
	//
	//	next_physical_indexed_bytes / live_indexed_bytes
	//
	// Physical bytes include old documents hidden by file tombstones. Higher
	// values mean more disk and more query-time index data than a clean full
	// rebuild would need.
	MaxPhysicalLiveBytesRatio float64

	// MaxTombstonePathRatio limits accumulated stale-path metadata:
	//
	//	next_tombstone_path_count / live_path_count
	//
	// This can force a rebuild even when the current candidate delta is small,
	// because the stack has accumulated enough deleted/changed paths.
	MaxTombstonePathRatio float64

	// MaxShardFanoutRatio limits shard fanout relative to a clean rebuild
	// estimate:
	//
	//	next_shard_count / ceil(live_indexed_bytes / shard_max)
	//
	// It is intentionally approximate. It protects query fanout and file-system
	// overhead when many small delta shards accumulate. Unlike the layer-count
	// cap, this scales with repos whose clean full index naturally spans many
	// shards.
	MaxShardFanoutRatio float64
}

func (t DeltaAdmissionThresholds) withDefaults() DeltaAdmissionThresholds {
	if t.MaxDeltaIndexedBytesRatio == 0 {
		// A conservative write-mass default: once a candidate delta approaches
		// one fifth of the live corpus, a clean rebuild is often competitive and
		// avoids adding read debt.
		t.MaxDeltaIndexedBytesRatio = 0.20
	}
	if t.MaxPhysicalLiveBytesRatio == 0 {
		// Allow at most roughly 25% extra physical indexed bytes before forcing
		// a full rebuild to compact stale documents.
		t.MaxPhysicalLiveBytesRatio = 1.25
	}
	if t.MaxTombstonePathRatio == 0 {
		// Rebuild once about one fifth of live paths have stale entries in old
		// shards. This keeps tombstone metadata and skip checks bounded.
		t.MaxTombstonePathRatio = 0.20
	}
	if t.MaxShardFanoutRatio == 0 {
		// Allow up to 20x the shard count of a clean rebuild. This directly caps
		// query fanout without forcing large repos to rebuild after an arbitrary
		// number of tiny deltas.
		t.MaxShardFanoutRatio = 20
	}
	return t
}

func validateDeltaAdmissionMode(mode string) error {
	switch mode {
	case "", DeltaAdmissionModeStatsV1:
		return nil
	default:
		return fmt.Errorf("unknown delta admission mode %q", mode)
	}
}

func expandBranchesForOptions(repo *git.Repository, opts Options) ([]string, error) {
	branches, err := expandBranches(repo, opts.Branches, opts.BranchPrefix)
	if err != nil {
		return nil, err
	}
	if opts.ResolveHEADToBranch {
		headBranch, ok, err := resolveHEADBranchName(repo)
		if err != nil {
			return nil, err
		}
		if ok {
			for i, branch := range branches {
				if branch == "HEAD" {
					branches[i] = headBranch
				}
			}
		}
	}
	if opts.ResolveHEADToBranch || opts.AllowDeltaBranchSetChange {
		return uniqPreserveOrder(branches), nil
	}
	return branches, nil
}

func resolveHEADBranchName(repo *git.Repository) (string, bool, error) {
	ref, err := repo.Head()
	if err != nil {
		return "", false, err
	}
	if !ref.Name().IsBranch() {
		return "", false, nil
	}
	return ref.Name().Short(), true, nil
}

func uniqPreserveOrder(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := values[:0]
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func expandBranches(repo *git.Repository, bs []string, prefix string) ([]string, error) {
	var result []string
	for _, b := range bs {
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
// The returned bool indicates whether the index was updated as a result. This
// can be informative if doing incremental indexing.
func IndexGitRepo(opts Options) (bool, error) {
	return indexGitRepo(opts, gitIndexConfig{})
}

// indexGitRepo indexes the git repository as specified by the options and the provided gitIndexConfig.
// The returned bool indicates whether the index was updated as a result. This
// can be informative if doing incremental indexing.
func indexGitRepo(opts Options, config gitIndexConfig) (bool, error) {
	prepareDeltaBuild := prepareDeltaBuild
	if config.prepareDeltaBuild != nil {
		prepareDeltaBuild = config.prepareDeltaBuild
	}

	prepareNormalBuild := prepareNormalBuild
	if config.prepareNormalBuild != nil {
		prepareNormalBuild = config.prepareNormalBuild
	}

	// Set max thresholds, since we use them in this function.
	opts.BuildOptions.SetDefaults()
	if opts.RepoDir == "" {
		return false, fmt.Errorf("gitindex: must set RepoDir")
	}
	if err := validateDeltaAdmissionMode(opts.DeltaAdmissionMode); err != nil {
		return false, err
	}

	opts.BuildOptions.RepositoryDescription.Source = opts.RepoDir

	var repo *git.Repository
	legacyRepoOpen := cmp.Or(os.Getenv("ZOEKT_DISABLE_GOGIT_OPTIMIZATION"), "false")
	if b, err := strconv.ParseBool(legacyRepoOpen); b || err != nil {
		repo, err = plainOpenRepo(opts.RepoDir)
		if err != nil {
			return false, fmt.Errorf("plainOpenRepo: %w", err)
		}
	} else {
		var repoCloser io.Closer
		repo, repoCloser, err = openRepo(opts.RepoDir)
		if err != nil {
			return false, fmt.Errorf("openRepo: %w", err)
		}
		defer repoCloser.Close()
	}

	if err := setTemplatesFromRepo(&opts.BuildOptions.RepositoryDescription, repo, opts.RepoDir); err != nil {
		log.Printf("setTemplatesFromRepo(%s): %s", opts.RepoDir, err)
	}

	branches, err := expandBranchesForOptions(repo, opts)
	if err != nil {
		return false, fmt.Errorf("expandBranches: %w", err)
	}
	for _, b := range branches {
		commit, err := getCommit(repo, opts.BranchPrefix, b)
		if err != nil {
			if opts.AllowMissingBranch && err.Error() == "reference not found" {
				continue
			}

			return false, fmt.Errorf("getCommit(%q, %q): %w", opts.BranchPrefix, b, err)
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
		return false, nil
	}

	// branch => (path, sha1) => repo.
	var repos map[fileKey]BlobLocation

	// Branch => Repo => SHA1
	var branchVersions map[string]map[string]plumbing.Hash

	// set of file paths that have been changed or deleted since
	// the last indexed commit
	//
	// These only have an effect on delta builds
	var changedOrRemovedFiles []string

	if opts.BuildOptions.IsDelta {
		allowDeltaBranchSetChange := false
		if existingRepository, _, ok, err := opts.BuildOptions.FindRepositoryMetadata(); err == nil && ok {
			allowDeltaBranchSetChange = opts.AllowDeltaBranchSetChange && !index.BranchNamesEqual(existingRepository.Branches, opts.BuildOptions.RepositoryDescription.Branches)
		}
		repos, branchVersions, changedOrRemovedFiles, err = prepareDeltaBuild(opts, repo)
		if err != nil {
			log.Printf("delta build: falling back to normal build since delta build failed, repository=%q, err=%s", opts.BuildOptions.RepositoryDescription.Name, err)
			opts.BuildOptions.IsDelta = false
		} else if opts.DeltaAdmissionMode == DeltaAdmissionModeStatsV1 {
			stats, err := prepareDeltaStatsV1(opts, repo, repos, changedOrRemovedFiles)
			if err != nil {
				log.Printf("delta build: falling back to normal build since stats-v1 admission rejected delta, repository=%q, err=%s", opts.BuildOptions.RepositoryDescription.Name, err)
				opts.BuildOptions.IsDelta = false
			} else {
				opts.BuildOptions.RepositoryDescription.DeltaStats = stats
			}
		}
		if opts.BuildOptions.IsDelta && allowDeltaBranchSetChange {
			opts.BuildOptions.AllowDeltaBranchSetChange = true
		}
	}

	if !opts.BuildOptions.IsDelta {
		repos, branchVersions, err = prepareNormalBuild(opts, repo)
		if err != nil {
			return false, fmt.Errorf("preparing normal build: %w", err)
		}
		if opts.DeltaAdmissionMode == DeltaAdmissionModeStatsV1 {
			stats, err := repositoryStatsForFiles(repos, opts.BuildOptions)
			if err != nil {
				return false, fmt.Errorf("computing stats-v1 full build stats: %w", err)
			}
			stats.PhysicalIndexedBytes = stats.LiveIndexedBytes
			stats.PhysicalDocumentCount = stats.LiveDocumentCount
			stats.TombstonePathCount = 0
			stats.DeltaLayerCount = 0
			stats.LastFullIndexTimeUnix = time.Now().Unix()
			opts.BuildOptions.RepositoryDescription.DeltaStats = stats
		}
	}

	reposByPath := map[string]BlobLocation{}
	for key, info := range repos {
		reposByPath[key.SubRepoPath] = info
	}

	opts.BuildOptions.SubRepositories = map[string]*zoekt.Repository{}
	for path, info := range reposByPath {
		tpl := opts.BuildOptions.RepositoryDescription
		if path != "" {
			tpl = zoekt.Repository{URL: info.URL.String()}
			if info.URL.String() != "" {
				if err := SetTemplatesFromOrigin(&tpl, info.URL); err != nil {
					log.Printf("setTemplatesFromOrigin(%s, %s): %s", path, info.URL, err)
				}
			}
			if tpl.Name == "" {
				tpl.Name = path
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

	builder, err := index.NewBuilder(opts.BuildOptions)
	if err != nil {
		return false, fmt.Errorf("build.NewBuilder: %w", err)
	}

	// Preparing the build can consume substantial memory, so check usage before starting to index.
	builder.CheckMemoryUsage()

	// we don't need to check error, since we either already have an error, or
	// we returning the first call to builder.Finish.
	defer builder.Finish() // nolint:errcheck

	for _, f := range changedOrRemovedFiles {
		builder.MarkFileAsChangedOrRemoved(f)
	}

	var names []string
	fileKeys := map[string][]fileKey{}
	totalFiles := 0

	for key := range repos {
		n := key.FullPath()
		fileKeys[n] = append(fileKeys[n], key)
		names = append(names, n)
		totalFiles++
	}

	sort.Strings(names)
	names = uniq(names)

	// Separate main-repo keys from submodule keys, collecting blob SHAs
	// for the main repo so we can stream them via git cat-file --batch.
	// ZOEKT_DISABLE_CATFILE_BATCH=true falls back to the go-git path for
	// all files, useful as a kill switch if the cat-file path causes issues.
	//
	// 2026-04-02(keegan) we are regularly seeing git growing to over 9GB in
	// memory usage in our production cluster. Disabling by default until the
	// issue is resolved.
	catfileBatchDisabled := cmp.Or(os.Getenv("ZOEKT_DISABLE_CATFILE_BATCH"), "true")
	useCatfileBatch := true
	if disabled, _ := strconv.ParseBool(catfileBatchDisabled); disabled {
		useCatfileBatch = false
		log.Printf("cat-file batch disabled via ZOEKT_DISABLE_CATFILE_BATCH, using go-git")
	}

	mainRepoKeys := make([]fileKey, 0, totalFiles)
	mainRepoIDs := make([]plumbing.Hash, 0, totalFiles)
	var submoduleKeys []fileKey

	for _, name := range names {
		for _, key := range fileKeys[name] {
			if useCatfileBatch && key.SubRepoPath == "" {
				mainRepoKeys = append(mainRepoKeys, key)
				mainRepoIDs = append(mainRepoIDs, key.ID)
			} else {
				submoduleKeys = append(submoduleKeys, key)
			}
		}
	}

	log.Printf("attempting to index %d total files (%d via cat-file, %d via go-git)", totalFiles, len(mainRepoIDs), len(submoduleKeys))

	// Stream main-repo blobs via pipelined cat-file --batch --buffer.
	// Large blobs are skipped without reading content into memory.
	if len(mainRepoIDs) > 0 {
		crOpts := catfileReaderOptions{
			filterSpec: catfileFilterSpec(opts),
		}
		cr, err := newCatfileReader(opts.RepoDir, mainRepoIDs, crOpts)
		if err != nil {
			return false, fmt.Errorf("newCatfileReader: %w", err)
		}

		if err := indexCatfileBlobs(cr, mainRepoKeys, repos, opts, builder); err != nil {
			return false, err
		}
	}

	// Index submodule blobs via go-git.
	for idx, key := range submoduleKeys {
		doc, err := createDocument(key, repos, opts.BuildOptions)
		if err != nil {
			return false, err
		}

		if err := builder.Add(doc); err != nil {
			return false, fmt.Errorf("error adding document with name %s: %w", key.FullPath(), err)
		}

		if idx%10_000 == 0 {
			builder.CheckMemoryUsage()
		}
	}

	return true, builder.Finish()
}

// indexCatfileBlobs streams main-repo blobs from the catfileReader into the
// builder. Large blobs are skipped without reading content into memory.
// keys must correspond 1:1 (in order) with the ids passed to newCatfileReader.
// The reader is always closed when this function returns.
func indexCatfileBlobs(cr *catfileReader, keys []fileKey, repos map[fileKey]BlobLocation, opts Options, builder *index.Builder) error {
	defer cr.Close()

	slab := newContentSlab(16 << 20) // 16 MB per slab

	for idx, key := range keys {
		size, missing, excluded, err := cr.Next()
		if err != nil {
			return fmt.Errorf("cat-file next for %s: %w", key.FullPath(), err)
		}

		branches := repos[key].Branches
		var doc index.Document

		if missing {
			// Unexpected for local repos — may indicate corruption, shallow
			// clone, or a race with git gc. Log a warning and skip.
			log.Printf("warning: blob %s missing for %s", key.ID, key.FullPath())
			doc = skippedDoc(key, branches, index.SkipReasonMissing)
		} else if excluded {
			doc = skippedDoc(key, branches, index.SkipReasonTooLarge)
		} else {
			keyFullPath := key.FullPath()
			if size > opts.BuildOptions.SizeMax && !opts.BuildOptions.IgnoreSizeMax(keyFullPath) {
				// Skip without reading content into memory.
				doc = skippedDoc(key, branches, index.SkipReasonTooLarge)
			} else {
				content := slab.alloc(size)
				if _, err := io.ReadFull(cr, content); err != nil {
					return fmt.Errorf("read blob %s: %w", keyFullPath, err)
				}
				doc = index.Document{
					SubRepositoryPath: key.SubRepoPath,
					Name:              keyFullPath,
					Content:           content,
					Branches:          branches,
				}
			}
		}

		if err := builder.Add(doc); err != nil {
			return fmt.Errorf("error adding document with name %s: %w", key.FullPath(), err)
		}

		if idx%10_000 == 0 {
			builder.CheckMemoryUsage()
		}
	}

	return nil
}

// openRepo opens a git repository in a way that's optimized for indexing.
//
// It copies the relevant logic from git.PlainOpen, and tweaks certain filesystem options.
func openRepo(repoDir string) (*git.Repository, io.Closer, error) {
	fs := osfs.New(repoDir)

	// Check if the root directory exists.
	if _, err := fs.Stat(""); err != nil {
		if os.IsNotExist(err) {
			return nil, nil, git.ErrRepositoryNotExists
		}
		return nil, nil, err
	}

	fi, err := fs.Stat(git.GitDirName)
	if err == nil && !fi.IsDir() {
		return openCompatibleRepo(repoDir)
	}

	return openOptimizedRepo(repoDir)
}

func openCompatibleRepo(repoDir string) (*git.Repository, io.Closer, error) {
	repo, err := plainOpenRepo(repoDir)
	if err != nil {
		return nil, nil, err
	}

	return repo, noopCloser{}, nil
}

func openOptimizedRepo(repoDir string) (*git.Repository, io.Closer, error) {
	fs := osfs.New(repoDir)
	wt := fs

	// If there's a .git directory, use that as the new root.
	if fi, err := fs.Stat(git.GitDirName); err == nil && fi.IsDir() {
		if fs, err = fs.Chroot(git.GitDirName); err != nil {
			return nil, nil, fmt.Errorf("fs.Chroot: %w", err)
		}
	}

	s := filesystem.NewStorageWithOptions(fs, cache.NewObjectLRUDefault(), filesystem.Options{
		// Cache the packfile handles, preventing the packfile from being opened then closed on every object access
		KeepDescriptors: true,
	})

	// Because we're keeping descriptors open, we need to close the storage object when we're done.
	repo, err := git.Open(s, wt)
	return repo, s, err
}

type noopCloser struct{}

func (noopCloser) Close() error { return nil }

func catfileFilterSpec(opts Options) string {
	// Can't filter by size if we have large file exceptions
	if len(opts.BuildOptions.LargeFiles) > 0 {
		return ""
	}

	if opts.BuildOptions.SizeMax <= 0 {
		return ""
	}

	// Git's blob:limit filter excludes blobs whose size is >= the given limit,
	// while zoekt indexes files up to and including SizeMax bytes.
	return fmt.Sprintf("blob:limit=%d", int64(opts.BuildOptions.SizeMax)+1)
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

// prepareDeltaBuildFunc is a function that calculates the necessary metadata for preparing
// a build.Builder instance for generating a delta build.
type prepareDeltaBuildFunc func(options Options, repository *git.Repository) (repos map[fileKey]BlobLocation, branchVersions map[string]map[string]plumbing.Hash, changedOrDeletedPaths []string, err error)

// prepareNormalBuildFunc is a function that calculates the necessary metadata for preparing
// a build.Builder instance for generating a normal build.
type prepareNormalBuildFunc func(options Options, repository *git.Repository) (repos map[fileKey]BlobLocation, branchVersions map[string]map[string]plumbing.Hash, err error)

type gitIndexConfig struct {
	// prepareDeltaBuild, if not nil, is the function that is used to calculate the metadata that will be used to
	// prepare the build.Builder instance for generating a delta build.
	//
	// If prepareDeltaBuild is nil, gitindex.prepareDeltaBuild will be used instead.
	prepareDeltaBuild prepareDeltaBuildFunc

	// prepareNormalBuild, if not nil, is the function that is used to calculate the metadata that will be used to
	// prepare the build.Builder instance for generating a normal build.
	//
	// If prepareNormalBuild is nil, gitindex.prepareNormalBuild will be used instead.
	prepareNormalBuild prepareNormalBuildFunc
}

type deltaAdmissionFallbackError struct {
	reason string
}

func (e deltaAdmissionFallbackError) Error() string {
	return "stats-v1 delta admission rejected delta: " + e.reason
}

type deltaAdmissionDecisionLogEntry struct {
	Time       time.Time `json:"time"`
	Repo       string    `json:"repo"`
	Mode       string    `json:"mode"`
	Accepted   bool      `json:"accepted"`
	Reason     string    `json:"reason"`
	BranchName string    `json:"branch_name,omitempty"`

	ExistingStatsPresent bool `json:"existing_stats_present"`

	OldBranchCount uint64                      `json:"old_branch_count"`
	NewBranchCount uint64                      `json:"new_branch_count"`
	BranchMapping  []deltaAdmissionBranchEntry `json:"branch_mapping,omitempty"`

	CandidateIndexedBytes  uint64 `json:"candidate_indexed_bytes"`
	CandidateDocumentCount uint64 `json:"candidate_document_count"`
	CandidatePathCount     uint64 `json:"candidate_path_count"`
	ChangedOrDeletedPaths  uint64 `json:"changed_or_deleted_paths"`

	LiveIndexedBytes  uint64 `json:"live_indexed_bytes"`
	LiveDocumentCount uint64 `json:"live_document_count"`
	LivePathCount     uint64 `json:"live_path_count"`

	NextLiveIndexedBytes  uint64 `json:"next_live_indexed_bytes,omitempty"`
	NextLiveDocumentCount uint64 `json:"next_live_document_count,omitempty"`
	NextLivePathCount     uint64 `json:"next_live_path_count,omitempty"`

	NextPhysicalIndexedBytes  uint64  `json:"next_physical_indexed_bytes"`
	NextPhysicalDocumentCount uint64  `json:"next_physical_document_count"`
	NextTombstonePathCount    uint64  `json:"next_tombstone_path_count"`
	NextDeltaLayerCount       uint64  `json:"next_delta_layer_count"`
	ShardFanoutRatio          float64 `json:"shard_fanout_ratio"`

	WriteBytesRatio    *float64 `json:"write_bytes_ratio,omitempty"`
	PhysicalLiveRatio  *float64 `json:"physical_live_ratio,omitempty"`
	TombstonePathRatio *float64 `json:"tombstone_path_ratio,omitempty"`

	Thresholds DeltaAdmissionThresholds `json:"thresholds"`
}

type deltaAdmissionBranchEntry struct {
	Old  string `json:"old,omitempty"`
	New  string `json:"new,omitempty"`
	Kind string `json:"kind"`
}

func prepareDeltaStatsV1(options Options, repository *git.Repository, candidateRepos map[fileKey]BlobLocation, changedOrDeletedPaths []string) (*zoekt.RepositoryDeltaStats, error) {
	existingRepository, _, ok, err := options.BuildOptions.FindRepositoryMetadata()
	if err != nil {
		return nil, fmt.Errorf("loading existing repository metadata: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("loading existing repository metadata: not found")
	}

	candidateStats, err := repositoryStatsForFiles(candidateRepos, options.BuildOptions)
	if err != nil {
		return nil, fmt.Errorf("computing delta candidate stats: %w", err)
	}

	var currentStats *zoekt.RepositoryDeltaStats
	liveStatsForDecision := existingRepository.DeltaStats
	if liveStatsForDecision == nil {
		currentStats, err = computeCurrentLiveStats(options, repository)
		if err != nil {
			return nil, err
		}
		liveStatsForDecision = currentStats
	}

	existingPhysicalIndexedBytes := liveStatsForDecision.LiveIndexedBytes
	existingPhysicalDocumentCount := liveStatsForDecision.LiveDocumentCount
	existingDeltaLayerCount := uint64(0)
	lastFullIndexTimeUnix := int64(0)
	if stats := existingRepository.DeltaStats; stats != nil {
		existingPhysicalIndexedBytes = stats.PhysicalIndexedBytes
		existingPhysicalDocumentCount = stats.PhysicalDocumentCount
		existingDeltaLayerCount = stats.DeltaLayerCount
		lastFullIndexTimeUnix = stats.LastFullIndexTimeUnix
	} else if stats, err := existingPhysicalStats(options.BuildOptions); err == nil {
		existingPhysicalIndexedBytes = stats.PhysicalIndexedBytes
		existingPhysicalDocumentCount = stats.PhysicalDocumentCount
	} else {
		log.Printf("stats-v1: failed to compute physical stats from old index for repository=%q: %s", options.BuildOptions.RepositoryDescription.Name, err)
	}

	tombstones, err := existingFileTombstones(options.BuildOptions)
	if err != nil {
		return nil, fmt.Errorf("computing existing tombstones: %w", err)
	}
	for _, path := range changedOrDeletedPaths {
		tombstones[path] = struct{}{}
	}

	nextDeltaLayerCount := existingDeltaLayerCount
	if candidateStats.LiveDocumentCount > 0 || len(changedOrDeletedPaths) > 0 {
		nextDeltaLayerCount++
	}

	nextPhysicalIndexedBytes := existingPhysicalIndexedBytes + candidateStats.LiveIndexedBytes
	nextPhysicalDocumentCount := existingPhysicalDocumentCount + candidateStats.LiveDocumentCount
	if nextPhysicalIndexedBytes < liveStatsForDecision.LiveIndexedBytes {
		nextPhysicalIndexedBytes = liveStatsForDecision.LiveIndexedBytes
	}
	if nextPhysicalDocumentCount < liveStatsForDecision.LiveDocumentCount {
		nextPhysicalDocumentCount = liveStatsForDecision.LiveDocumentCount
	}

	thresholds := options.DeltaAdmissionThresholds.withDefaults()
	shardFanoutRatio := shardFanoutRatio(options.BuildOptions, liveStatsForDecision.LiveIndexedBytes, candidateStats.LiveIndexedBytes)
	decision := deltaAdmissionDecisionLogEntry{
		Time:       time.Now().UTC(),
		Repo:       options.BuildOptions.RepositoryDescription.Name,
		Mode:       DeltaAdmissionModeStatsV1,
		Accepted:   false,
		BranchName: singleBranchName(options.BuildOptions.RepositoryDescription.Branches),

		ExistingStatsPresent: existingRepository.DeltaStats != nil,

		OldBranchCount: uint64(len(existingRepository.Branches)),
		NewBranchCount: uint64(len(options.BuildOptions.RepositoryDescription.Branches)),
		BranchMapping:  deltaAdmissionBranchMapping(existingRepository.Branches, options.BuildOptions.RepositoryDescription.Branches),

		CandidateIndexedBytes:  candidateStats.LiveIndexedBytes,
		CandidateDocumentCount: candidateStats.LiveDocumentCount,
		CandidatePathCount:     candidateStats.LivePathCount,
		ChangedOrDeletedPaths:  uint64(len(changedOrDeletedPaths)),

		LiveIndexedBytes:  liveStatsForDecision.LiveIndexedBytes,
		LiveDocumentCount: liveStatsForDecision.LiveDocumentCount,
		LivePathCount:     liveStatsForDecision.LivePathCount,

		NextPhysicalIndexedBytes:  nextPhysicalIndexedBytes,
		NextPhysicalDocumentCount: nextPhysicalDocumentCount,
		NextTombstonePathCount:    uint64(len(tombstones)),
		NextDeltaLayerCount:       nextDeltaLayerCount,
		ShardFanoutRatio:          shardFanoutRatio,

		WriteBytesRatio:    finiteRatio(candidateStats.LiveIndexedBytes, liveStatsForDecision.LiveIndexedBytes),
		PhysicalLiveRatio:  finiteRatio(nextPhysicalIndexedBytes, liveStatsForDecision.LiveIndexedBytes),
		TombstonePathRatio: finiteRatio(uint64(len(tombstones)), liveStatsForDecision.LivePathCount),

		Thresholds: thresholds,
	}
	reject := func(reason string) (*zoekt.RepositoryDeltaStats, error) {
		decision.Reason = reason
		writeDeltaAdmissionDecisionLog(options, decision)
		return nil, deltaAdmissionFallbackError{reason: reason}
	}

	// Evaluate write-mass gates before read-debt gates. The write-mass gates
	// answer "is this individual delta worth doing?", while the read-debt gates
	// answer "has the existing stack become expensive enough to compact?" The
	// logged reason is the first failed gate in this order.
	if r := ratio(candidateStats.LiveIndexedBytes, liveStatsForDecision.LiveIndexedBytes); r > thresholds.MaxDeltaIndexedBytesRatio {
		return reject(fmt.Sprintf("write indexed bytes ratio %.4f exceeds %.4f", r, thresholds.MaxDeltaIndexedBytesRatio))
	}
	if r := ratio(nextPhysicalIndexedBytes, liveStatsForDecision.LiveIndexedBytes); r > thresholds.MaxPhysicalLiveBytesRatio {
		return reject(fmt.Sprintf("physical/live bytes ratio %.4f exceeds %.4f", r, thresholds.MaxPhysicalLiveBytesRatio))
	}
	if r := ratio(uint64(len(tombstones)), liveStatsForDecision.LivePathCount); r > thresholds.MaxTombstonePathRatio {
		return reject(fmt.Sprintf("tombstone path ratio %.4f exceeds %.4f", r, thresholds.MaxTombstonePathRatio))
	}
	if shardFanoutRatio > thresholds.MaxShardFanoutRatio {
		return reject(fmt.Sprintf("shard fanout ratio %.4f exceeds %.4f", shardFanoutRatio, thresholds.MaxShardFanoutRatio))
	}

	nextLiveStats := currentStats
	if nextLiveStats == nil {
		nextLiveStats, err = nextLiveStatsFromDelta(options, repository, existingRepository, candidateStats, candidateRepos, changedOrDeletedPaths)
		if err != nil {
			return nil, err
		}
	}

	if nextPhysicalIndexedBytes < nextLiveStats.LiveIndexedBytes {
		nextPhysicalIndexedBytes = nextLiveStats.LiveIndexedBytes
	}
	if nextPhysicalDocumentCount < nextLiveStats.LiveDocumentCount {
		nextPhysicalDocumentCount = nextLiveStats.LiveDocumentCount
	}

	stats := &zoekt.RepositoryDeltaStats{
		LiveIndexedBytes:      nextLiveStats.LiveIndexedBytes,
		LiveDocumentCount:     nextLiveStats.LiveDocumentCount,
		LivePathCount:         nextLiveStats.LivePathCount,
		PhysicalIndexedBytes:  nextPhysicalIndexedBytes,
		PhysicalDocumentCount: nextPhysicalDocumentCount,
		TombstonePathCount:    uint64(len(tombstones)),
		DeltaLayerCount:       nextDeltaLayerCount,
		LastFullIndexTimeUnix: lastFullIndexTimeUnix,
	}

	decision.Accepted = true
	decision.Reason = "accepted"
	decision.NextLiveIndexedBytes = nextLiveStats.LiveIndexedBytes
	decision.NextLiveDocumentCount = nextLiveStats.LiveDocumentCount
	decision.NextLivePathCount = nextLiveStats.LivePathCount
	decision.NextPhysicalIndexedBytes = nextPhysicalIndexedBytes
	decision.NextPhysicalDocumentCount = nextPhysicalDocumentCount
	writeDeltaAdmissionDecisionLog(options, decision)

	return stats, nil
}

func writeDeltaAdmissionDecisionLog(options Options, decision deltaAdmissionDecisionLogEntry) {
	if options.DeltaAdmissionLogPath == "" {
		return
	}

	f, err := os.OpenFile(options.DeltaAdmissionLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		log.Printf("stats-v1: failed to open delta admission log %q: %s", options.DeltaAdmissionLogPath, err)
		return
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(decision); err != nil {
		log.Printf("stats-v1: failed to write delta admission log %q: %s", options.DeltaAdmissionLogPath, err)
	}
}

func singleBranchName(branches []zoekt.RepositoryBranch) string {
	if len(branches) != 1 {
		return ""
	}
	return branches[0].Name
}

func deltaAdmissionBranchMapping(oldBranches, newBranches []zoekt.RepositoryBranch) []deltaAdmissionBranchEntry {
	oldNames := repositoryBranchNamesForDelta(oldBranches)
	newNames := repositoryBranchNamesForDelta(newBranches)
	usedOld := make([]bool, len(oldNames))
	usedNew := make([]bool, len(newNames))
	mapping := make([]deltaAdmissionBranchEntry, 0, max(len(oldNames), len(newNames)))

	for i, oldName := range oldNames {
		for j, newName := range newNames {
			if usedNew[j] || oldName != newName {
				continue
			}
			usedOld[i] = true
			usedNew[j] = true
			mapping = append(mapping, deltaAdmissionBranchEntry{
				Old:  oldName,
				New:  newName,
				Kind: "exact",
			})
			break
		}
	}

	for i, oldName := range oldNames {
		if usedOld[i] {
			continue
		}
		newIndex := -1
		for j := range newNames {
			if !usedNew[j] {
				newIndex = j
				break
			}
		}
		if newIndex == -1 {
			mapping = append(mapping, deltaAdmissionBranchEntry{
				Old:  oldName,
				Kind: "removed",
			})
			continue
		}
		usedOld[i] = true
		usedNew[newIndex] = true
		mapping = append(mapping, deltaAdmissionBranchEntry{
			Old:  oldName,
			New:  newNames[newIndex],
			Kind: "renamed",
		})
	}

	for j, newName := range newNames {
		if usedNew[j] {
			continue
		}
		mapping = append(mapping, deltaAdmissionBranchEntry{
			New:  newName,
			Kind: "added",
		})
	}

	return mapping
}

func finiteRatio(n, d uint64) *float64 {
	r := ratio(n, d)
	if math.IsInf(r, 0) || math.IsNaN(r) {
		return nil
	}
	return &r
}

func computeCurrentLiveStats(options Options, repository *git.Repository) (*zoekt.RepositoryDeltaStats, error) {
	currentRepos, _, err := prepareNormalBuild(options, repository)
	if err != nil {
		return nil, fmt.Errorf("computing current live stats: %w", err)
	}
	currentStats, err := repositoryStatsForFiles(currentRepos, options.BuildOptions)
	if err != nil {
		return nil, fmt.Errorf("computing current live stats: %w", err)
	}
	return currentStats, nil
}

func nextLiveStatsFromDelta(options Options, repository *git.Repository, existingRepository *zoekt.Repository, candidateStats *zoekt.RepositoryDeltaStats, candidateRepos map[fileKey]BlobLocation, changedOrDeletedPaths []string) (*zoekt.RepositoryDeltaStats, error) {
	if existingRepository.DeltaStats == nil {
		return nil, fmt.Errorf("existing DeltaStats missing")
	}

	affectedPaths := make(map[string]struct{}, len(changedOrDeletedPaths)+len(candidateRepos))
	for _, path := range changedOrDeletedPaths {
		affectedPaths[path] = struct{}{}
	}
	for key := range candidateRepos {
		affectedPaths[key.FullPath()] = struct{}{}
	}

	oldAffectedRepos, err := oldLiveFilesForPaths(repository, existingRepository.Branches, affectedPaths)
	if err != nil {
		return nil, err
	}
	oldAffectedStats, err := repositoryStatsForFiles(oldAffectedRepos, options.BuildOptions)
	if err != nil {
		return nil, fmt.Errorf("computing old live stats for changed paths: %w", err)
	}

	oldStats := existingRepository.DeltaStats
	return &zoekt.RepositoryDeltaStats{
		LiveIndexedBytes:      addAfterSubtract(oldStats.LiveIndexedBytes, oldAffectedStats.LiveIndexedBytes, candidateStats.LiveIndexedBytes),
		LiveDocumentCount:     addAfterSubtract(oldStats.LiveDocumentCount, oldAffectedStats.LiveDocumentCount, candidateStats.LiveDocumentCount),
		LivePathCount:         addAfterSubtract(oldStats.LivePathCount, oldAffectedStats.LivePathCount, candidateStats.LivePathCount),
		PhysicalIndexedBytes:  oldStats.PhysicalIndexedBytes,
		PhysicalDocumentCount: oldStats.PhysicalDocumentCount,
		TombstonePathCount:    oldStats.TombstonePathCount,
		DeltaLayerCount:       oldStats.DeltaLayerCount,
		LastFullIndexTimeUnix: oldStats.LastFullIndexTimeUnix,
	}, nil
}

func oldLiveFilesForPaths(repository *git.Repository, branches []zoekt.RepositoryBranch, paths map[string]struct{}) (map[fileKey]BlobLocation, error) {
	repos := make(map[fileKey]BlobLocation)
	if len(paths) == 0 {
		return repos, nil
	}

	for _, branch := range branches {
		commit, err := getCommit(repository, "", branch.Version)
		if err != nil {
			return nil, fmt.Errorf("getting last indexed commit for branch %q: %w", branch.Name, err)
		}

		tree, err := commit.Tree()
		if err != nil {
			return nil, fmt.Errorf("getting last indexed git tree for branch %q: %w", branch.Name, err)
		}

		ig, err := newIgnoreMatcher(tree)
		if err != nil {
			return nil, fmt.Errorf("newIgnoreMatcher for branch %q: %w", branch.Name, err)
		}

		for path := range paths {
			if ig.Match(path) {
				continue
			}

			f, err := tree.File(path)
			if err != nil {
				if errors.Is(err, object.ErrFileNotFound) {
					continue
				}
				return nil, fmt.Errorf("getting old file %q in branch %q: %w", path, branch.Name, err)
			}

			key := fileKey{Path: path, ID: f.ID()}
			if existing, ok := repos[key]; ok {
				existing.Branches = append(existing.Branches, branch.Name)
				repos[key] = existing
			} else {
				repos[key] = BlobLocation{
					GitRepo:  repository,
					Branches: []string{branch.Name},
				}
			}
		}
	}

	return repos, nil
}

func addAfterSubtract(base, subtract, add uint64) uint64 {
	if subtract > base {
		return add
	}
	return base - subtract + add
}

func repositoryStatsForFiles(files map[fileKey]BlobLocation, opts index.Options) (*zoekt.RepositoryDeltaStats, error) {
	stats := &zoekt.RepositoryDeltaStats{
		LiveDocumentCount: uint64(len(files)),
	}
	paths := make(map[string]struct{}, len(files))
	for key, location := range files {
		name := key.FullPath()
		paths[name] = struct{}{}

		indexedBytes := uint64(len(name))
		blob, err := location.GitRepo.BlobObject(key.ID)
		if err == nil {
			size := uint64(blob.Size)
			if blob.Size <= int64(opts.SizeMax) || opts.IgnoreSizeMax(name) {
				indexedBytes += size
			}
		} else if !errors.Is(err, plumbing.ErrObjectNotFound) {
			return nil, fmt.Errorf("loading blob %s for %q: %w", key.ID, name, err)
		}
		stats.LiveIndexedBytes += indexedBytes
	}
	stats.LivePathCount = uint64(len(paths))
	return stats, nil
}

func existingPhysicalStats(opts index.Options) (*zoekt.RepositoryDeltaStats, error) {
	shards := opts.FindAllShards()
	if len(shards) == 0 {
		return nil, fmt.Errorf("no existing shards")
	}

	stats := &zoekt.RepositoryDeltaStats{}
	found := false
	for _, shard := range shards {
		f, err := os.Open(shard)
		if err != nil {
			return nil, err
		}
		indexFile, err := index.NewIndexFile(f)
		if err != nil {
			return nil, err
		}
		searcher, err := index.NewSearcher(indexFile)
		if err != nil {
			indexFile.Close()
			return nil, err
		}
		list, err := searcher.List(context.Background(), &query.Const{Value: true}, &zoekt.ListOptions{})
		searcher.Close()
		if err != nil {
			return nil, err
		}
		for _, entry := range list.Repos {
			if sameRepository(&entry.Repository, &opts.RepositoryDescription) {
				found = true
				stats.PhysicalIndexedBytes += uint64(max(entry.Stats.ContentBytes, 0))
				stats.PhysicalDocumentCount += uint64(max(entry.Stats.Documents, 0))
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("repository %q not found in existing shards", opts.RepositoryDescription.Name)
	}
	return stats, nil
}

func existingFileTombstones(opts index.Options) (map[string]struct{}, error) {
	tombstones := map[string]struct{}{}
	for _, shard := range opts.FindAllShards() {
		repositories, _, err := index.ReadMetadataPathAlive(shard)
		if err != nil {
			return nil, err
		}
		for _, repository := range repositories {
			if !sameRepository(repository, &opts.RepositoryDescription) {
				continue
			}
			for path := range repository.FileTombstones {
				tombstones[path] = struct{}{}
			}
		}
	}
	return tombstones, nil
}

func prepareBranchSetDeltaBuild(options Options, repository *git.Repository, existingRepository *zoekt.Repository) (repos map[fileKey]BlobLocation, branchVersions map[string]map[string]plumbing.Hash, changedOrDeletedPaths []string, err error) {
	if err := validateBranchSetDelta(options, existingRepository); err != nil {
		return nil, nil, nil, err
	}

	repos, branchVersions, err = prepareNormalBuildRecurse(options, repository, nil, false)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("preparing branch-set delta live files: %w", err)
	}

	changedOrDeletedPaths, err = indexedFilePaths(options.BuildOptions)
	if err != nil {
		return nil, nil, nil, err
	}
	return repos, branchVersions, changedOrDeletedPaths, nil
}

func validateBranchSetDelta(options Options, existingRepository *zoekt.Repository) error {
	newBranches := repositoryBranchNamesForDelta(options.BuildOptions.RepositoryDescription.Branches)
	if len(newBranches) != len(uniqPreserveOrder(append([]string(nil), newBranches...))) {
		return fmt.Errorf("ambiguous branch mapping: duplicate requested branch names %q", strings.Join(newBranches, ", "))
	}

	oldBranches := repositoryBranchNamesForDelta(existingRepository.Branches)
	if len(oldBranches) != len(uniqPreserveOrder(append([]string(nil), oldBranches...))) {
		return fmt.Errorf("ambiguous branch mapping: duplicate existing branch names %q", strings.Join(oldBranches, ", "))
	}

	if branchNameSetOverlap(oldBranches, newBranches) {
		return nil
	}
	if options.ResolveHEADToBranch && len(oldBranches) == 1 && len(newBranches) == 1 {
		return nil
	}

	return fmt.Errorf("ambiguous branch mapping: requested branch set %q has no stable branch in common with existing branch set %q", strings.Join(newBranches, ", "), strings.Join(oldBranches, ", "))
}

func repositoryBranchNamesForDelta(branches []zoekt.RepositoryBranch) []string {
	names := make([]string, 0, len(branches))
	for _, branch := range branches {
		names = append(names, branch.Name)
	}
	return names
}

func branchNameSetOverlap(a, b []string) bool {
	seen := make(map[string]struct{}, len(a))
	for _, name := range a {
		seen[name] = struct{}{}
	}
	for _, name := range b {
		if _, ok := seen[name]; ok {
			return true
		}
	}
	return false
}

func indexedFilePaths(opts index.Options) ([]string, error) {
	paths := make(map[string]struct{})
	for _, shard := range opts.FindAllShards() {
		f, err := os.Open(shard)
		if err != nil {
			return nil, fmt.Errorf("opening shard %q: %w", shard, err)
		}
		indexFile, err := index.NewIndexFile(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("opening index file %q: %w", shard, err)
		}
		searcher, err := index.NewSearcher(indexFile)
		if err != nil {
			indexFile.Close()
			return nil, fmt.Errorf("opening searcher for %q: %w", shard, err)
		}
		result, err := searcher.Search(context.Background(), &query.Const{Value: true}, &zoekt.SearchOptions{Whole: true})
		searcher.Close()
		if err != nil {
			return nil, fmt.Errorf("listing indexed paths from shard %q: %w", shard, err)
		}
		for _, file := range result.Files {
			paths[file.FileName] = struct{}{}
		}
	}

	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result, nil
}

func ensureStatsV1DeltaShardsSupported(opts index.Options) error {
	for _, shard := range opts.FindAllShards() {
		if strings.HasPrefix(filepath.Base(shard), "compound-") {
			return fmt.Errorf("stats-v1 delta builds don't support repositories contained in compound shards (shard %q)", shard)
		}
		repositories, _, err := index.ReadMetadataPathAlive(shard)
		if err != nil {
			return err
		}
		if len(repositories) > 1 {
			return fmt.Errorf("stats-v1 delta builds don't support compound shards (shard %q contains %d repositories)", shard, len(repositories))
		}
	}
	return nil
}

func sameRepository(a, b *zoekt.Repository) bool {
	if a.ID != 0 || b.ID != 0 {
		return a.ID == b.ID
	}
	return a.Name == b.Name
}

func ratio(n, d uint64) float64 {
	if d == 0 {
		if n == 0 {
			return 0
		}
		return math.Inf(1)
	}
	return float64(n) / float64(d)
}

func shardFanoutRatio(opts index.Options, liveIndexedBytes, candidateIndexedBytes uint64) float64 {
	existingShardCount := uint64(len(opts.FindAllShards()))
	nextShardCount := existingShardCount
	if candidateIndexedBytes > 0 {
		nextShardCount += max(ceilDiv(candidateIndexedBytes, uint64(opts.ShardMax)), 1)
	}

	cleanShardCount := max(ceilDiv(liveIndexedBytes, uint64(opts.ShardMax)), 1)
	return ratio(nextShardCount, cleanShardCount)
}

func ceilDiv(n, d uint64) uint64 {
	if d == 0 {
		return 0
	}
	return (n + d - 1) / d
}

func prepareDeltaBuild(options Options, repository *git.Repository) (repos map[fileKey]BlobLocation, branchVersions map[string]map[string]plumbing.Hash, changedOrDeletedPaths []string, err error) {
	if options.Submodules {
		return nil, nil, nil, fmt.Errorf("delta builds currently don't support submodule indexing")
	}

	// discover what commits we indexed during our last build
	existingRepository, _, ok, err := options.BuildOptions.FindRepositoryMetadata()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get repository metadata: %w", err)
	}

	if !ok {
		return nil, nil, nil, fmt.Errorf("no existing shards found for repository")
	}
	if options.DeltaAdmissionMode == DeltaAdmissionModeStatsV1 {
		if err := ensureStatsV1DeltaShardsSupported(options.BuildOptions); err != nil {
			return nil, nil, nil, err
		}
	}

	if options.DeltaShardNumberFallbackThreshold > 0 {
		// HACK: For our interim compaction strategy, we force a full normal index once
		// the number of shards on disk for this repository exceeds the provided threshold.
		//
		// This strategy obviously isn't optimal (as an example: we currently can't differentiate
		// between "normal" and "delta" shards, so repositories like the gigarepo that generate a large number of shards per
		// build would be disproportionately affected by this), but it'll allow us to continue experimenting on real workloads
		// while we create a better compaction strategy).

		oldShards := options.BuildOptions.FindAllShards()
		if uint64(len(oldShards)) > options.DeltaShardNumberFallbackThreshold {
			return nil, nil, nil, fmt.Errorf("number of existing shards (%d) > requested shard threshold (%d)", len(oldShards), options.DeltaShardNumberFallbackThreshold)
		}
	}

	// Check if the build options hash does not match the repository metadata's hash.
	// If it does not match, one or more index options changed and require a
	// normal build instead of a delta build.
	if options.BuildOptions.GetHash() != existingRepository.IndexOptions {
		return nil, nil, nil, fmt.Errorf("one or more index options previously stored for repository %s (ID: %d) does not match the index options for this requested build; These index option updates are incompatible with delta build. new index options: %+v", existingRepository.Name, existingRepository.ID, options.BuildOptions.HashOptions())
	}

	if !index.BranchNamesEqual(existingRepository.Branches, options.BuildOptions.RepositoryDescription.Branches) {
		if !options.AllowDeltaBranchSetChange {
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

			return nil, nil, nil, fmt.Errorf("requested branch set in build options (%q) != branch set found on disk (%q) - branch set must be the same for delta shards", optionsBranchList, existingBranchList)
		}
		return prepareBranchSetDeltaBuild(options, repository, existingRepository)
	}

	// branch => (path, sha1) => repo.
	repos = map[fileKey]BlobLocation{}

	branches, err := expandBranchesForOptions(repository, options)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("expandBranches: %w", err)
	}

	// branch name -> git worktree at most current commit
	branchToCurrentTree := make(map[string]*object.Tree, len(branches))

	for _, b := range branches {
		commit, err := getCommit(repository, options.BranchPrefix, b)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("getting last current commit for branch %q: %w", b, err)
		}

		tree, err := commit.Tree()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("getting current git tree for branch %q: %w", b, err)
		}

		branchToCurrentTree[b] = tree
	}

	rawURL := options.BuildOptions.RepositoryDescription.URL
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parsing repository URL %q: %w", rawURL, err)
	}

	// TODO: Support repository submodules for delta builds

	// loop over all branches, calculate the diff between our
	// last indexed commit and the current commit, and add files mentioned in the diff
	for _, branch := range existingRepository.Branches {
		lastIndexedCommit, err := getCommit(repository, "", branch.Version)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("getting last indexed commit for branch %q: %w", branch.Name, err)
		}

		lastIndexedTree, err := lastIndexedCommit.Tree()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("getting lasted indexed git tree for branch %q: %w", branch.Name, err)
		}

		changes, err := object.DiffTreeWithOptions(context.Background(), lastIndexedTree, branchToCurrentTree[branch.Name], &object.DiffTreeOptions{DetectRenames: false})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("generating changeset for branch %q: %w", branch.Name, err)
		}

		for i, c := range changes {
			oldFile, newFile, err := c.Files()
			if err != nil {
				return nil, nil, nil, fmt.Errorf("change #%d: getting files before and after change: %w", i, err)
			}

			if newFile != nil {
				// note: newFile.Name could be a path that isn't relative to the repository root - using the
				// change's Name field is the only way that @ggilmore saw to get the full path relative to the root
				newFileRelativeRootPath := c.To.Name

				// TODO@ggilmore: HACK - remove once ignore files are supported in delta builds
				if newFileRelativeRootPath == ignore.IgnoreFile {
					return nil, nil, nil, fmt.Errorf("%q file is not yet supported in delta builds", ignore.IgnoreFile)
				}

				// either file is added or renamed, so we need to add the new version to the build
				file := fileKey{Path: newFileRelativeRootPath, ID: newFile.Hash}
				if existing, ok := repos[file]; ok {
					existing.Branches = append(existing.Branches, branch.Name)
					repos[file] = existing
				} else {
					repos[file] = BlobLocation{
						GitRepo:  repository,
						URL:      u,
						Branches: []string{branch.Name},
					}
				}
			}

			if oldFile == nil {
				// file added - nothing more to do
				continue
			}

			// Note: oldFile.Name could be a path that isn't relative to the repository root - using the
			// change's "Name" field is the only way that ggilmore saw to get the full path relative to the root
			oldFileRelativeRootPath := c.From.Name

			if oldFileRelativeRootPath == ignore.IgnoreFile {
				return nil, nil, nil, fmt.Errorf("%q file is not yet supported in delta builds", ignore.IgnoreFile)
			}

			// The file is either modified or deleted. So, we need to add ALL versions
			// of the old file (across all branches) to the build.
			for b, currentTree := range branchToCurrentTree {
				f, err := currentTree.File(oldFileRelativeRootPath)
				if err != nil {
					// the file doesn't exist in this branch
					if errors.Is(err, object.ErrFileNotFound) {
						continue
					}

					return nil, nil, nil, fmt.Errorf("getting hash for file %q in branch %q: %w", oldFile.Name, b, err)
				}

				file := fileKey{Path: oldFileRelativeRootPath, ID: f.ID()}
				if existing, ok := repos[file]; ok {
					existing.Branches = append(existing.Branches, b)
					repos[file] = existing
				} else {
					repos[file] = BlobLocation{
						GitRepo:  repository,
						URL:      u,
						Branches: []string{b},
					}
				}
			}

			changedOrDeletedPaths = append(changedOrDeletedPaths, oldFileRelativeRootPath)
		}
	}

	// we need to de-duplicate the branch map before returning it - it's possible for the same
	// branch to have been added multiple times if a file has been modified across multiple commits
	for _, info := range repos {
		sort.Strings(info.Branches)
		info.Branches = uniq(info.Branches)
	}

	// we also need to de-duplicate the list of changed or deleted file paths, it's also possible to have duplicates
	// for the same reasoning as above
	sort.Strings(changedOrDeletedPaths)
	changedOrDeletedPaths = uniq(changedOrDeletedPaths)

	return repos, nil, changedOrDeletedPaths, nil
}

func prepareNormalBuild(options Options, repository *git.Repository) (repos map[fileKey]BlobLocation, branchVersions map[string]map[string]plumbing.Hash, err error) {
	var repoCache *RepoCache
	if options.Submodules && options.RepoCacheDir != "" {
		repoCache = NewRepoCache(options.RepoCacheDir)
	}
	return prepareNormalBuildRecurse(options, repository, repoCache, false)
}

func prepareNormalBuildRecurse(options Options, repository *git.Repository, repoCache *RepoCache, isSubrepo bool) (repos map[fileKey]BlobLocation, branchVersions map[string]map[string]plumbing.Hash, err error) {
	// Branch => Repo => SHA1
	branchVersions = map[string]map[string]plumbing.Hash{}

	branches, err := expandBranchesForOptions(repository, options)
	if err != nil {
		return nil, nil, fmt.Errorf("expandBranches: %w", err)
	}

	repoURL := options.BuildOptions.RepositoryDescription.URL

	if isSubrepo {
		cfg, err := repository.Config()
		if err != nil {
			return nil, nil, fmt.Errorf("unable to get repository config: %w", err)
		}

		u, err := normalizeSubmoduleRemoteURL(cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to identify subrepository URL: %w", err)
		}
		repoURL = u
	}

	rw := NewRepoWalker(repository, repoURL, repoCache)
	for _, b := range branches {
		commit, err := getCommit(repository, options.BranchPrefix, b)
		if err != nil {
			if options.AllowMissingBranch && err.Error() == "reference not found" {
				continue
			}

			return nil, nil, fmt.Errorf("getCommit: %w", err)
		}

		tree, err := commit.Tree()
		if err != nil {
			return nil, nil, fmt.Errorf("commit.Tree: %w", err)
		}

		ig, err := newIgnoreMatcher(tree)
		if err != nil {
			return nil, nil, fmt.Errorf("newIgnoreMatcher: %w", err)
		}

		subVersions, err := rw.CollectFiles(tree, b, ig)
		if err != nil {
			return nil, nil, fmt.Errorf("CollectFiles: %w", err)
		}

		branchVersions[b] = subVersions
	}

	// Index submodules using go-git if we didn't do so using the repo cache
	if options.Submodules && options.RepoCacheDir == "" {
		worktree, err := repository.Worktree()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get repository worktree: %w", err)
		}

		submodules, err := worktree.Submodules()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get submodules: %w", err)
		}

		for _, submodule := range submodules {
			subRepository, err := submodule.Repository()
			if err != nil {
				log.Printf("failed to open submodule repository: %s, %s", submodule.Config().Name, err)
				continue
			}

			sw, subVersions, err := prepareNormalBuildRecurse(options, subRepository, repoCache, true)
			if err != nil {
				log.Printf("failed to index submodule repository: %s, %s", submodule.Config().Name, err)
				continue
			}

			log.Printf("adding subrepository files from: %s", submodule.Config().Name)

			for k, repo := range sw {
				rw.Files[fileKey{
					SubRepoPath: filepath.Join(submodule.Config().Path, k.SubRepoPath),
					Path:        k.Path,
					ID:          k.ID,
				}] = repo
			}

			for k, v := range subVersions {
				branchVersions[filepath.Join(submodule.Config().Path, k)] = v
			}
		}
	}

	return rw.Files, branchVersions, nil
}

func createDocument(key fileKey,
	repos map[fileKey]BlobLocation,
	opts index.Options,
) (index.Document, error) {
	repo := repos[key]
	blob, err := repo.GitRepo.BlobObject(key.ID)
	branches := repos[key].Branches

	// We filter out large documents when fetching the repo. So if an object is too large, it will not be found.
	if errors.Is(err, plumbing.ErrObjectNotFound) {
		return skippedDoc(key, branches, index.SkipReasonTooLarge), nil
	}

	if err != nil {
		return index.Document{}, err
	}

	keyFullPath := key.FullPath()
	if blob.Size > int64(opts.SizeMax) && !opts.IgnoreSizeMax(keyFullPath) {
		return skippedDoc(key, branches, index.SkipReasonTooLarge), nil
	}

	contents, err := blobContents(blob)
	if err != nil {
		return index.Document{}, err
	}

	return index.Document{
		SubRepositoryPath: key.SubRepoPath,
		Name:              keyFullPath,
		Content:           contents,
		Branches:          branches,
	}, nil
}

// skippedDoc creates a Document placeholder for a blob that was not indexed.
func skippedDoc(key fileKey, branches []string, reason index.SkipReason) index.Document {
	return index.Document{
		SkipReason:        reason,
		Name:              key.FullPath(),
		Branches:          branches,
		SubRepositoryPath: key.SubRepoPath,
	}
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
