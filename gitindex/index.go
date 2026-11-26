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
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/ignore"
	"github.com/sourcegraph/zoekt/index"

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
	case "bitbucket-cloud":
		// https://bitbucket.org/<workspace>/<repo_slug>/commits/<version>
		// https://bitbucket.org/<workspace>/<repo_slug>/src/<version>/<path>
		repo.CommitURLTemplate = urlJoinPath("commits", varVersion)
		repo.FileURLTemplate = urlJoinPath("src", varVersion, varPath)
		repo.LineFragmentTemplate = "#{{.LineNumber}}"
	case "azuredevops":
		// https://dev.azure.com/<organization>/<project>/_git/<repo>/commit/<version>
		// https://dev.azure.com/<organization>/<project>/_git/<repo>?path=/<path>&version=GC<version>
		repo.CommitURLTemplate = urlJoinPath("commit", varVersion)
		repo.FileURLTemplate = urlJoinPath() + "?path=/{{.Path}}&version=GC{{.Version}}&_a=contents"
		repo.LineFragmentTemplate = "&line={{.LineNumber}}&lineEnd={{.LineNumber}}&lineStartColumn=1&lineEndColumn=200"
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

// getCommit returns the commit for a branch name or revision. With a branch
// prefix, the branch in that namespace wins. The git binary resolves the name,
// unless ZOEKT_DISABLE_CATFILE_BATCH=true selects the go-git fallback.
func getCommit(repo *git.Repository, prefix, ref string) (*object.Commit, error) {
	if gitDir, ok := repoGitDir(repo); ok && !gitBinaryDisabled() {
		id, err := resolveCommit(gitDir, prefix, ref)
		if err != nil {
			return nil, err
		}
		return repo.CommitObject(id)
	}

	// go-git tries a name made of hex digits as an abbreviated object ID before
	// it tries the name as a ref, while git prefers the ref. So look up a branch
	// with such a name, like a ticket number, before go-git can resolve it to an
	// unrelated commit. A full object ID is still resolved as an object ID.
	if prefix != "" && len(ref) < len(plumbing.ZeroHash.String()) && isHex(ref) {
		if r, err := repo.Reference(plumbing.ReferenceName(filepath.Join(prefix, ref)), true); err == nil {
			if commit, err := repo.CommitObject(r.Hash()); err == nil {
				return commit, nil
			}
		}
	}

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

// resolveCommit resolves ref to a commit with git rev-parse in gitDir.
//
// With a prefix, ref names a branch in that namespace: <prefix>/<ref> wins over
// any other ref or object ID of the same name, and a short name missing from
// the namespace does not fall back to another branch of that name. Other
// revisions, such as tags, HEAD, object IDs, qualified refs and revision
// expressions, resolve as git resolves them.
func resolveCommit(gitDir, prefix, ref string) (plumbing.Hash, error) {
	if prefix != "" {
		name := path.Join(prefix, ref)
		// git would also expand the shorthand heads/x to refs/heads/heads/x.
		if kind, _, _ := strings.Cut(name, "/"); kind == "heads" || kind == "tags" || kind == "remotes" {
			name = "refs/" + name
		}
		id, err := revParseCommit(gitDir, name)
		if !errors.Is(err, plumbing.ErrReferenceNotFound) {
			return id, err
		}

		branch, err := isShortBranchName(gitDir, ref)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		if branch {
			return plumbing.ZeroHash, plumbing.ErrReferenceNotFound
		}
	}
	return revParseCommit(gitDir, ref)
}

// isShortBranchName reports whether ref is the short name of a local or
// remote-tracking branch, rather than a tag, a root ref like HEAD, a ref under
// refs/, an object ID or an expression. A tag passed as a branch name still
// resolves when the namespace has no such branch.
func isShortBranchName(gitDir, ref string) (bool, error) {
	if strings.HasPrefix(ref, "refs/") || strings.Trim(ref, "ABCDEFGHIJKLMNOPQRSTUVWXYZ_") == "" {
		return false, nil
	}
	expansions := []string{"refs/" + ref, "refs/heads/" + ref, "refs/remotes/" + ref, "refs/remotes/" + ref + "/HEAD"}
	cmd := gitCommand(gitDir, append([]string{"for-each-ref", "--format=%(refname)"}, expansions...)...)
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git for-each-ref: %w", err)
	}
	// for-each-ref also lists refs below the patterns, so match whole names.
	existing := strings.Split(string(out), "\n")
	if slices.Contains(existing, expansions[0]) {
		// git prefers refs/<ref>, such as refs/meta/config for meta/config.
		return false, nil
	}
	for _, e := range expansions[1:] {
		if slices.Contains(existing, e) {
			return true, nil
		}
	}
	return false, nil
}

func revParseCommit(gitDir, rev string) (plumbing.Hash, error) {
	// Without --end-of-options, which needs git 2.30, a leading dash would be an
	// option. No ref name starts with one.
	if strings.HasPrefix(rev, "-") {
		return plumbing.ZeroHash, plumbing.ErrReferenceNotFound
	}
	cmd := gitCommand(gitDir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		// Callers that allow missing branches check for this error.
		return plumbing.ZeroHash, plumbing.ErrReferenceNotFound
	}
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git rev-parse %q: %w: %s", rev, err, bytes.TrimSpace(stderr.Bytes()))
	}
	hex := strings.TrimSpace(string(out))
	if !plumbing.IsHash(hex) {
		return plumbing.ZeroHash, fmt.Errorf("git rev-parse %q: unexpected output %q", rev, out)
	}
	return plumbing.NewHash(hex), nil
}

// gitCommand returns a git command that runs in the git directory gitDir.
// GIT_DIR=. keeps git from using an inherited GIT_DIR or searching parent
// directories for another repository, as Sourcegraph's gitserver does. Like
// go-git, git never fetches missing objects from a promisor remote here.
func gitCommand(gitDir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = gitDir
	cmd.Env = append(os.Environ(), "GIT_DIR=.", "GIT_NO_LAZY_FETCH=1")
	return cmd
}

// repoGitDir returns the git directory of a repository stored on disk.
func repoGitDir(repo *git.Repository) (string, bool) {
	s, ok := repo.Storer.(interface{ Filesystem() billy.Filesystem })
	if !ok {
		return "", false
	}
	return s.Filesystem().Root(), true
}

// gitBinaryDisabled reports whether ZOEKT_DISABLE_CATFILE_BATCH=true asks
// for go-git instead of the git binary, as a kill switch.
func gitBinaryDisabled() bool {
	disabled, _ := strconv.ParseBool(os.Getenv("ZOEKT_DISABLE_CATFILE_BATCH"))
	return disabled
}

// isHex reports whether s is a non-empty string of hex digits.
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return false
		}
	}
	return true
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
	// The caller-supplied name is a fallback for repos without zoekt.name. It
	// still identifies the repository and shard names better than an inferred
	// origin URL, but an explicit repo config should win.
	name := desc.Name

	cfg, err := repo.Config()
	if err == nil {
		return setTemplatesFromRepoConfigPreservingName(desc, cfg, name)
	}

	// Some repositories, notably worktrees with .git files, need the plainOpenRepo
	// fallback to resolve their common dir before go-git can read config.
	repo, err = plainOpenRepo(repoDir)
	if err == nil {
		cfg, err = repo.Config()
	}
	if err != nil {
		// go-git cannot parse every config git accepts, so index without it.
		slog.Warn("reading git config failed", "path", repoDir, "error", err)
		return nil
	}

	return setTemplatesFromRepoConfigPreservingName(desc, cfg, name)
}

func setTemplatesFromRepoConfigPreservingName(desc *zoekt.Repository, cfg *config.Config, name string) error {
	if name != "" && cfg.Raw.Section("zoekt").Options.Get("name") == "" {
		defer func() {
			// A caller-supplied name identifies the repository and its shards, so it
			// takes precedence over names derived from the origin URL. Other repository
			// fields are intentionally refreshed from config.
			desc.Name = name
		}()
	}
	if err := setTemplatesFromRepoConfig(desc, cfg); err != nil {
		return err
	}
	return nil
}

// setNameAndURLFromRepoConfig sets desc's name and URL templates from the
// zoekt.name, zoekt.web-url and zoekt.web-url-type git config, falling back to
// the origin remote.
func setNameAndURLFromRepoConfig(desc *zoekt.Repository, cfg *config.Config) error {
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

	return nil
}

// setTemplatesFromRepoConfig applies the zoekt section of a repository's git
// config to desc. It returns an error only for config that a shard must not be
// written with, such as an invalid zoekt.repoid. The name and URL templates
// only affect how results are displayed and linked, so problems with them are
// logged and the rest of the config is still applied.
func setTemplatesFromRepoConfig(desc *zoekt.Repository, cfg *config.Config) error {
	sec := cfg.Raw.Section("zoekt")

	if v := sec.Options.Get("repoid"); v != "" {
		id, err := zoekt.ParseRepoID(v)
		if err != nil {
			return fmt.Errorf("zoekt.repoid: %w", err)
		}
		desc.ID = id
	}

	// Tenant 0 means no tenant, and with strict tenant enforcement no tenant
	// can search such a shard. So a malformed ID fails instead of becoming 0.
	if v := sec.Options.Get("tenantID"); v != "" {
		id, err := strconv.Atoi(v)
		if err != nil || id < 0 {
			return fmt.Errorf("zoekt.tenantID: invalid tenant ID %q", v)
		}
		desc.TenantID = id
	}

	if err := setNameAndURLFromRepoConfig(desc, cfg); err != nil {
		slog.Warn("setting repository name and URL templates from git config failed", "repo", desc.Name, "error", err)
	}

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

	// DryRun reports whether indexing is needed without writing an index.
	DryRun bool

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

	// DeltaShardNumberFallbackThreshold defines an upper limit (inclusive) on the number of preexisting shards
	// that can exist before attempting another delta build. If the number of preexisting shards exceeds this threshold,
	// then a normal build will be performed instead.
	//
	// If DeltaShardNumberFallbackThreshold is 0, then this fallback behavior is disabled:
	// a delta build will always be performed regardless of the number of preexisting shards.
	DeltaShardNumberFallbackThreshold uint64
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
// The returned bool indicates whether the index was updated, or would be
// updated when DryRun is set. This can be informative if doing incremental
// indexing.
func IndexGitRepo(opts Options) (bool, error) {
	return indexGitRepo(opts, gitIndexConfig{})
}

// indexGitRepo indexes the git repository as specified by the options and the provided gitIndexConfig.
// The returned bool indicates whether the index was updated, or would be
// updated when DryRun is set. This can be informative if doing incremental
// indexing.
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
		return false, fmt.Errorf("setTemplatesFromRepo(%s): %w", opts.RepoDir, err)
	}

	branches, err := expandBranches(repo, opts.Branches, opts.BranchPrefix)
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
	if opts.DryRun {
		return true, nil
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
		repos, branchVersions, changedOrRemovedFiles, err = prepareDeltaBuild(opts, repo)
		if err != nil {
			slog.Warn("delta build failed, falling back to normal build", "repo", opts.BuildOptions.RepositoryDescription.Name, "error", err)
			opts.BuildOptions.IsDelta = false
		}
	}

	if !opts.BuildOptions.IsDelta {
		repos, branchVersions, err = prepareNormalBuild(opts, repo)
		if err != nil {
			return false, fmt.Errorf("preparing normal build: %w", err)
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
					slog.Warn("setTemplatesFromOrigin failed", "path", path, "url", info.URL.String(), "error", err)
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

	// Only Finish after all input has been read. On an input error, discard
	// staged shards instead of publishing an incomplete repository.
	defer builder.Abort()

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
	// Cat-file was briefly disabled by default after git grew past 9GB of
	// memory in production. The cause was promisor lazy fetching of blobs
	// that the size filter would skip, which GIT_NO_LAZY_FETCH=1 avoids (see
	// doc/indexing.md).
	useCatfileBatch := true
	if gitBinaryDisabled() {
		useCatfileBatch = false
		slog.Info("cat-file batch disabled via ZOEKT_DISABLE_CATFILE_BATCH=true")
	}
	filterSpec := catfileFilterSpec(opts)
	if useCatfileBatch && filterSpec != "" {
		if err := checkCatfileFilterSupport(opts.RepoDir, filterSpec); err != nil {
			useCatfileBatch = false
			slog.Warn("git cat-file does not support --filter, using go-git", "error", err)
		}
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

	slog.Info("attempting to index files", "count", totalFiles, "catfile_count", len(mainRepoIDs), "gogit_count", len(submoduleKeys))

	// Stream main-repo blobs via pipelined cat-file --batch --buffer.
	// Large blobs are skipped without reading content into memory.
	if len(mainRepoIDs) > 0 {
		crOpts := catfileReaderOptions{
			filterSpec: filterSpec,
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
	missingFiles := 0

	for idx, key := range keys {
		size, missing, excluded, err := cr.Next()
		if err != nil {
			return fmt.Errorf("cat-file next for %s: %w", key.FullPath(), err)
		}

		branches := repos[key].Branches
		var doc index.Document

		if missing {
			// Size-filtered partial clones intentionally omit large blobs. Match
			// createDocument's assumption that missing blobs were filtered out,
			// so switching readers preserves the user-visible skip reason. This
			// is not a size check: other causes of missing objects get the same
			// explanation. Keep a missing-object count for diagnostics below.
			missingFiles++
			doc = skippedDoc(key, branches, index.SkipReasonTooLarge)
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

	// Partial clones may intentionally omit many blobs. Summarize once instead
	// of emitting a warning for every skipped file.
	if missingFiles > 0 {
		slog.Info("skipped files with missing git objects", "count", missingFiles, "path", opts.RepoDir)
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

	objCache := cache.NewObjectLRUDefault()
	s := newAlternatesStorage(filesystem.NewStorageWithOptions(fs, objCache, filesystem.Options{
		// Cache the packfile handles, preventing the packfile from being opened then closed on every object access
		KeepDescriptors: true,
	}), fs, objCache)

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
	if errors.Is(err, object.ErrFileNotFound) {
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

	// Delta shards stack on top of the repository's simple shards. Builder.Finish
	// rejects a repository merged into a compound shard, which would fail every
	// retry, so fall back to a normal build that moves it into a simple shard.
	oldShards := options.BuildOptions.FindAllShards()
	for _, shard := range oldShards {
		if strings.HasPrefix(filepath.Base(shard), "compound-") {
			return nil, nil, nil, fmt.Errorf("delta builds don't support repositories contained in compound shards (shard %q)", shard)
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

		if uint64(len(oldShards)) > options.DeltaShardNumberFallbackThreshold {
			return nil, nil, nil, fmt.Errorf("number of existing shards (%d) > requested shard threshold (%d)", len(oldShards), options.DeltaShardNumberFallbackThreshold)
		}
	}

	// Check to see if the set of branch names is consistent with what we last indexed.
	// If it isn't consistent, that we can't proceed with a delta build (and the caller should fall back to a
	// normal one).

	if !index.BranchNamesEqual(existingRepository.Branches, options.BuildOptions.RepositoryDescription.Branches) {
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

	// Check if the build options hash does not match the repository metadata's hash
	// If it does not index then one or more index options has changed and will require a normal build instead of a delta build
	if options.BuildOptions.GetHash() != existingRepository.IndexOptions {
		return nil, nil, nil, fmt.Errorf("one or more index options previously stored for repository %s (ID: %d) does not match the index options for this requested build; These index option updates are incompatible with delta build. new index options: %+v", existingRepository.Name, existingRepository.ID, options.BuildOptions.HashOptions())
	}

	// branch => (path, sha1) => repo.
	repos = map[fileKey]BlobLocation{}

	branches, err := expandBranches(repository, options.Branches, options.BranchPrefix)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("expandBranches: %w", err)
	}

	// branch name -> git worktree at most current commit
	branchToCurrentTree := make(map[string]*object.Tree, len(branches))
	branchToCurrentCommit := make(map[string]plumbing.Hash, len(branches))

	// branch name -> ignore file of the current tree. Delta builds fall back
	// when an ignore file changes, so this also matches the last indexed tree.
	branchToIgnore := make(map[string]*ignore.Matcher, len(branches))

	for _, b := range branches {
		commit, err := getCommit(repository, options.BranchPrefix, b)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("getting last current commit for branch %q: %w", b, err)
		}

		tree, err := commit.Tree()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("getting current git tree for branch %q: %w", b, err)
		}

		ig, err := newIgnoreMatcher(tree)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("reading ignore file for branch %q: %w", b, err)
		}

		branchToCurrentTree[b] = tree
		branchToCurrentCommit[b] = commit.Hash
		branchToIgnore[b] = ig
	}

	rawURL := options.BuildOptions.RepositoryDescription.URL
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parsing repository URL %q: %w", rawURL, err)
	}

	// The git binary diffs the trees unless ZOEKT_DISABLE_CATFILE_BATCH=true
	// selects the go-git fallback.
	gitDir, useGit := repoGitDir(repository)
	useGit = useGit && !gitBinaryDisabled()

	// TODO: Support repository submodules for delta builds

	// calculate the diff between our last indexed commit and the current
	// commit of each branch
	branchToChanges := make(map[string][]fileChange, len(existingRepository.Branches))
	oldPaths := map[string]struct{}{}
	for _, branch := range existingRepository.Branches {
		lastIndexedCommit, err := getCommit(repository, "", branch.Version)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("getting last indexed commit for branch %q: %w", branch.Name, err)
		}

		var changes []fileChange
		if useGit {
			changes, err = diffTrees(gitDir, lastIndexedCommit.Hash, branchToCurrentCommit[branch.Name])
		} else {
			var lastIndexedTree *object.Tree
			lastIndexedTree, err = lastIndexedCommit.Tree()
			if err != nil {
				return nil, nil, nil, fmt.Errorf("getting lasted indexed git tree for branch %q: %w", branch.Name, err)
			}
			changes, err = diffTreesGoGit(lastIndexedTree, branchToCurrentTree[branch.Name])
		}
		if err != nil {
			return nil, nil, nil, fmt.Errorf("generating changeset for branch %q: %w", branch.Name, err)
		}

		branchToChanges[branch.Name] = changes
		for _, c := range changes {
			if c.From != "" {
				oldPaths[c.From] = struct{}{}
			}
		}
	}

	// currentFileID returns the ID of the file at p in branch b's current
	// tree, if it has a file there.
	branchToFiles := map[string]map[string]plumbing.Hash{}
	currentFileID := func(b, p string) (plumbing.Hash, bool, error) {
		if !useGit {
			f, err := branchToCurrentTree[b].File(p)
			if errors.Is(err, object.ErrFileNotFound) {
				return plumbing.ZeroHash, false, nil
			}
			if err != nil {
				return plumbing.ZeroHash, false, err
			}
			return f.ID(), true, nil
		}
		files, ok := branchToFiles[b]
		if !ok {
			var err error
			if files, err = lsTreeFiles(gitDir, branchToCurrentCommit[b], oldPaths); err != nil {
				return plumbing.ZeroHash, false, err
			}
			branchToFiles[b] = files
		}
		id, ok := files[p]
		return id, ok, nil
	}

	// add files mentioned in the diff
	for _, branch := range existingRepository.Branches {
		for _, c := range branchToChanges[branch.Name] {
			if c.To != "" {
				// TODO@ggilmore: HACK - remove once ignore files are supported in delta builds
				if c.To == ignore.IgnoreFile {
					return nil, nil, nil, fmt.Errorf("%q file is not yet supported in delta builds", ignore.IgnoreFile)
				}

				// either file is added or renamed, so we need to add the new version to the build,
				// unless a normal build would skip it because of the ignore file.
				if !branchToIgnore[branch.Name].Match(c.To) {
					file := fileKey{Path: c.To, ID: c.ToID}
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
			}

			if c.From == "" {
				// file added - nothing more to do
				continue
			}

			if c.From == ignore.IgnoreFile {
				return nil, nil, nil, fmt.Errorf("%q file is not yet supported in delta builds", ignore.IgnoreFile)
			}

			// The file is either modified or deleted. So, we need to add ALL versions
			// of the old file (across all branches) to the build.
			for b := range branchToCurrentTree {
				if branchToIgnore[b].Match(c.From) {
					continue
				}

				id, ok, err := currentFileID(b, c.From)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("getting hash for file %q in branch %q: %w", c.From, b, err)
				}
				if !ok {
					// the file doesn't exist in this branch
					continue
				}

				file := fileKey{Path: c.From, ID: id}
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

			changedOrDeletedPaths = append(changedOrDeletedPaths, c.From)
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

// fileChange is a file that differs between two trees, by its path from the
// repository root. From or To is empty when that side has no file there, such
// as when the file was added or deleted, or the entry is a submodule.
type fileChange struct {
	From, To string
	ToID     plumbing.Hash
}

// diffTrees lists the files that differ between two commits with git
// diff-tree, which reads trees only.
func diffTrees(gitDir string, from, to plumbing.Hash) ([]fileChange, error) {
	cmd := gitCommand(gitDir, "diff-tree", "-r", "-z", "--no-renames", from.String(), to.String())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff-tree %s %s: %w: %s", from, to, err, bytes.TrimSpace(stderr.Bytes()))
	}

	// Each change is ":<old mode> <new mode> <old ID> <new ID> <status>\0<path>\0".
	var changes []fileChange
	fields := strings.Split(string(out), "\x00")
	if len(fields)%2 != 1 || fields[len(fields)-1] != "" {
		return nil, fmt.Errorf("git diff-tree %s %s: truncated output", from, to)
	}
	for i := 0; i+1 < len(fields); i += 2 {
		meta := strings.Fields(strings.TrimPrefix(fields[i], ":"))
		if len(meta) != 5 {
			return nil, fmt.Errorf("git diff-tree %s %s: unexpected output %q", from, to, fields[i])
		}
		var c fileChange
		if isFileMode(meta[0]) {
			c.From = fields[i+1]
		}
		if isFileMode(meta[1]) {
			c.To = fields[i+1]
			c.ToID = plumbing.NewHash(meta[3])
		}
		if c != (fileChange{}) {
			changes = append(changes, c)
		}
	}
	return changes, nil
}

// diffTreesGoGit is diffTrees for the go-git fallback.
func diffTreesGoGit(from, to *object.Tree) ([]fileChange, error) {
	changes, err := object.DiffTreeWithOptions(context.Background(), from, to, &object.DiffTreeOptions{DetectRenames: false})
	if err != nil {
		return nil, err
	}
	var files []fileChange
	for i, c := range changes {
		oldFile, newFile, err := c.Files()
		if err != nil {
			return nil, fmt.Errorf("change #%d: getting files before and after change: %w", i, err)
		}
		// note: the files' Name fields may not be relative to the repository root,
		// so use the change's Name fields.
		var f fileChange
		if oldFile != nil {
			f.From = c.From.Name
		}
		if newFile != nil {
			f.To = c.To.Name
			f.ToID = newFile.Hash
		}
		if f != (fileChange{}) {
			files = append(files, f)
		}
	}
	return files, nil
}

// lsTreeFiles returns the IDs of the files at paths in commit, using git
// ls-tree. Paths with no file in commit are missing from the result.
func lsTreeFiles(gitDir string, commit plumbing.Hash, paths map[string]struct{}) (map[string]plumbing.Hash, error) {
	files := map[string]plumbing.Hash{}
	err := lsTree(gitDir, commit, func(p string, e object.TreeEntry) {
		if _, ok := paths[p]; ok && e.Mode.IsFile() {
			files[p] = e.Hash
		}
	})
	return files, err
}

// lsTree calls fn with the path and entry of every non-tree entry below
// treeish, such as files and submodules, using git ls-tree -r. It fails if a
// tree is missing.
func lsTree(gitDir string, treeish plumbing.Hash, fn func(path string, e object.TreeEntry)) error {
	cmd := gitCommand(gitDir, "ls-tree", "-r", "-z", "--full-tree", treeish.String())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start git ls-tree: %w", err)
	}

	// Each entry is "<mode> <type> <ID>\t<path>\0".
	var parseErr error
	r := bufio.NewReaderSize(out, 64*1024)
	for {
		line, err := r.ReadString(0)
		if err == io.EOF && line == "" {
			break
		}
		if err != nil {
			parseErr = err
			break
		}
		meta, p, ok := strings.Cut(line[:len(line)-1], "\t")
		fields := strings.Fields(meta)
		var mode filemode.FileMode
		if ok && len(fields) == 3 {
			mode, err = filemode.New(fields[0])
		}
		if !ok || len(fields) != 3 || err != nil || !plumbing.IsHash(fields[2]) {
			parseErr = fmt.Errorf("unexpected entry %q", line)
			break
		}
		fn(p, object.TreeEntry{Name: path.Base(p), Mode: mode, Hash: plumbing.NewHash(fields[2])})
	}
	// Drain stdout so git can exit before we wait for it.
	_, _ = io.Copy(io.Discard, r)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git ls-tree %s: %w: %s", treeish, err, bytes.TrimSpace(stderr.Bytes()))
	}
	if parseErr != nil {
		return fmt.Errorf("git ls-tree %s: %w", treeish, parseErr)
	}
	return nil
}

// isFileMode reports whether a git tree entry mode, in octal, is a file:
// regular, executable or a symlink.
func isFileMode(mode string) bool {
	m, err := filemode.New(mode)
	return err == nil && m.IsFile()
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

	branches, err := expandBranches(repository, options.Branches, options.BranchPrefix)
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

		subVersions, err := rw.CollectFiles(tree, b)
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
				slog.Warn("failed to open submodule repository", "submodule", submodule.Config().Name, "error", err)
				continue
			}

			sw, subVersions, err := prepareNormalBuildRecurse(options, subRepository, repoCache, true)
			if err != nil {
				slog.Warn("failed to index submodule repository", "submodule", submodule.Config().Name, "error", err)
				continue
			}

			slog.Info("adding subrepository files", "submodule", submodule.Config().Name)

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
