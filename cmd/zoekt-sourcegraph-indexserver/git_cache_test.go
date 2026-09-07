package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sourcegraph/log/logtest"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/sourcegraph/zoekt"
	indexserverv1 "github.com/sourcegraph/zoekt/cmd/zoekt-sourcegraph-indexserver/grpc/protos/zoekt/indexserver/v1"
	"github.com/sourcegraph/zoekt/gitindex"
	"github.com/sourcegraph/zoekt/internal/tenant"
	"github.com/sourcegraph/zoekt/search"
)

func writeCacheEntry(t *testing.T, args *indexArgs, entry gitRepoCacheEntry) string {
	t.Helper()
	dir := gitCache.cachedDir(args)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	b, err := json.Marshal(entry)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, gitRepoCacheMetadata), b, 0o600))
	return dir
}

func TestWithFetchedRepo(t *testing.T) {
	requireGitDaemon(t)
	fixture := newGitFetchFixture(t)
	for _, mode := range []string{"temporary", "cached"} {
		for _, outcome := range []string{"success", "fetch error", "callback error", "panic", "metadata error"} {
			if mode == "temporary" && outcome == "metadata error" {
				continue
			}
			t.Run(mode+"/"+outcome, func(t *testing.T) {
				t.Setenv("TMPDIR", t.TempDir())
				args := indexArgs{IndexOptions: IndexOptions{
					RepoID: 1, TenantID: 1, Name: "repo", CloneURL: fixture.cloneURL,
					CacheGitRepo: mode == "cached",
					Branches:     []zoekt.RepositoryBranch{{Name: "HEAD", Version: fixture.mainCommit}},
				}}
				dir, err := tmpGitDir(args.Name)
				require.NoError(t, err)
				created := time.Now().UTC().Add(-time.Hour)
				if args.CacheGitRepo {
					dir = writeCacheEntry(t, &args, gitRepoCacheEntry{
						Created: created, RepoID: args.RepoID, CloneURL: args.CloneURL, Filtered: true,
					})
				}
				marker := filepath.Join(dir, gitRepoCacheMetadata)
				failure := errors.New("injected failure")
				c := gitIndexConfig{runCmd: func(cmd *exec.Cmd) error {
					// The old completion marker must be gone before any mutation.
					require.NoFileExists(t, marker)
					if outcome == "fetch error" && slices.Contains(cmd.Args, "fetch") {
						return failure
					}
					return runIntegrationCommand(cmd)
				}}
				called := false
				run := func() error {
					return gitCache.withFetchedRepo(context.Background(), c, &args, logtest.Scoped(t), func(gitDir string) error {
						called = true
						require.Equal(t, dir, gitDir)
						require.Equal(t, fixture.mainCommit, strings.TrimSpace(runGitOutput(t, gitDir, "rev-parse", "HEAD")))
						require.NoFileExists(t, marker)
						switch outcome {
						case "callback error":
							return failure
						case "panic":
							panic(failure)
						case "metadata error":
							// Prevent the completion marker from being written.
							require.NoError(t, os.Mkdir(marker, 0o755))
						}
						return nil
					})
				}
				switch outcome {
				case "success":
					require.NoError(t, run())
				case "panic":
					require.PanicsWithValue(t, failure, func() { require.NoError(t, run()) })
				case "metadata error":
					var pathErr *os.PathError
					require.ErrorAs(t, run(), &pathErr)
					require.Equal(t, marker, pathErr.Path)
				default:
					require.ErrorIs(t, run(), failure)
				}
				require.Equal(t, outcome != "fetch error", called)
				if args.CacheGitRepo && outcome == "success" {
					entry, err := gitCache.readEntry(dir)
					require.NoError(t, err)
					require.Equal(t, created, entry.Created)
					require.Equal(t, args.Branches, entry.Branches)
				} else {
					require.NoDirExists(t, dir)
				}
			})
		}
	}
}

func TestPrepareCachedGitDir(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	now := time.Now().UTC()
	args := indexArgs{IndexOptions: IndexOptions{RepoID: 1, TenantID: 1, Name: "repo", CloneURL: "https://git/repo"}}
	for _, tc := range []struct {
		name   string
		change func(*gitRepoCacheEntry)
		reuse  bool
	}{
		{name: "reuse", reuse: true},
		{name: "expires at seven days", change: func(e *gitRepoCacheEntry) { e.Created = now.Add(-gitRepoCacheMaxAge) }},
		{name: "different source", change: func(e *gitRepoCacheEntry) { e.CloneURL += "-other" }},
		{name: "different filter", change: func(e *gitRepoCacheEntry) { e.Filtered = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := gitRepoCacheEntry{Created: now.Add(-time.Hour), RepoID: 1, CloneURL: args.CloneURL, Filtered: true}
			if tc.change != nil {
				tc.change(&entry)
			}
			dir := writeCacheEntry(t, &args, entry)
			sentinel := filepath.Join(dir, "retained-object")
			require.NoError(t, os.WriteFile(sentinel, nil, 0o600))
			// Access/mtime must not extend a busy repository's maximum lifetime.
			require.NoError(t, os.Chtimes(dir, now, now))
			gotDir, got, err := gitCache.prepare(&args, now)
			require.NoError(t, err)
			require.Equal(t, dir, gotDir)
			require.NoFileExists(t, filepath.Join(dir, gitRepoCacheMetadata))
			if tc.reuse {
				require.FileExists(t, sentinel)
				require.Equal(t, entry.Created, got.Created)
			} else {
				require.NoDirExists(t, dir)
				require.Equal(t, now, got.Created)
			}
		})
	}
	for _, contents := range []string{"", "invalid json"} {
		dir := gitCache.cachedDir(&args)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "shallow.lock"), nil, 0o600))
		if contents != "" {
			require.NoError(t, os.WriteFile(filepath.Join(dir, gitRepoCacheMetadata), []byte(contents), 0o600))
		}
		_, _, err := gitCache.prepare(&args, now)
		require.NoError(t, err)
		require.NoDirExists(t, dir)
	}
	other := args
	other.TenantID++
	require.NotEqual(t, gitCache.cachedDir(&args), gitCache.cachedDir(&other))
	other = args
	other.RepoID++
	require.NotEqual(t, gitCache.cachedDir(&args), gitCache.cachedDir(&other))
	other = args
	other.Name += "-renamed"
	require.NotEqual(t, gitCache.cachedDir(&args), gitCache.cachedDir(&other))
}

func TestCleanupGitRepoCache(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	now := time.Now().UTC()
	for i, tc := range []struct {
		name     string
		created  time.Time
		assigned bool
		keep     bool
	}{
		{"active", now.Add(-time.Hour), true, true},
		{"expired", now.Add(-gitRepoCacheMaxAge), true, false},
		{"unassigned", now, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := indexArgs{IndexOptions: IndexOptions{RepoID: uint32(i + 1), TenantID: 1, Name: tc.name}}
			dir := writeCacheEntry(t, &args, gitRepoCacheEntry{Created: tc.created, RepoID: args.RepoID})
			var assigned []uint32
			if tc.assigned {
				assigned = []uint32{args.RepoID}
			}
			require.NoError(t, gitCache.cleanup(assigned, now))
			if tc.keep {
				require.DirExists(t, dir)
			} else {
				require.NoDirExists(t, dir)
			}
		})
	}
	args := indexArgs{IndexOptions: IndexOptions{RepoID: 42, TenantID: 1, Name: "interrupted"}}
	dir := gitCache.cachedDir(&args)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, gitCache.cleanup([]uint32{42}, now))
	require.NoDirExists(t, dir)
}

func TestCachedGitIndexIntegration(t *testing.T) {
	requireGitDaemon(t)
	t.Setenv("TMPDIR", t.TempDir())
	fixture := newGitFetchFixture(t)
	args := indexArgs{
		IndexDir: t.TempDir(),
		IndexOptions: IndexOptions{
			RepoID: 123, TenantID: 1, Name: "test/repo", CloneURL: fixture.cloneURL, CacheGitRepo: true,
			Branches: []zoekt.RepositoryBranch{{Name: "HEAD", Version: fixture.mainCommit}, {Name: "old", Version: fixture.mainCommit}},
		},
	}
	logger := logtest.Scoped(t)
	c := gitIndexConfig{
		timeout: time.Minute,
		findRepositoryMetadata: func(args *indexArgs) (*zoekt.Repository, *zoekt.IndexMetadata, bool, error) {
			return args.BuildOptions().FindRepositoryMetadata()
		},
		runCmd: func(cmd *exec.Cmd) error {
			if cmd.Args[0] == "zoekt-git-index" {
				_, err := gitindex.IndexGitRepo(gitIndexOptionsForTest(&args, cmd.Args[len(cmd.Args)-1]))
				return err
			}
			return runIntegrationCommand(cmd)
		},
	}
	require.NoError(t, gitIndex(context.Background(), c, &args, sourcegraphNop{}, logger))
	dir := gitCache.cachedDir(&args)
	first, err := gitCache.readEntry(dir)
	require.NoError(t, err)
	packs, err := filepath.Glob(filepath.Join(dir, "objects", "pack", "*.pack"))
	require.NoError(t, err)
	require.NotEmpty(t, packs)
	smallBlob := strings.TrimSpace(runGitOutput(t, dir, "rev-parse", "HEAD:small.txt"))
	runGit(t, dir, "pack-refs", "--all")

	args.Branches = []zoekt.RepositoryBranch{{Name: "HEAD", Version: fixture.devCommit}}
	args.Branches = append(args.Branches, zoekt.RepositoryBranch{Name: "old/nested", Version: fixture.devCommit})
	require.NoError(t, gitIndex(context.Background(), c, &args, sourcegraphNop{}, logger))
	second, err := gitCache.readEntry(dir)
	require.NoError(t, err)
	require.Equal(t, first.Created, second.Created)
	require.Equal(t, args.Branches, second.Branches)
	for _, pack := range packs {
		// Reusing the original pack, not merely the path, is what saves transfers.
		require.FileExists(t, pack)
	}
	newPacks, err := filepath.Glob(filepath.Join(dir, "objects", "pack", "*.pack"))
	require.NoError(t, err)
	require.Greater(t, len(newPacks), len(packs))
	for _, pack := range newPacks {
		if !slices.Contains(packs, pack) {
			// The unchanged blob must not be downloaded into the new pack again.
			require.NotContains(t, runGitOutput(t, dir, "verify-pack", "-v", pack), smallBlob)
		}
	}
	require.Equal(t, fixture.devCommit, strings.TrimSpace(runGitOutput(t, dir, "rev-parse", "HEAD")))
	require.Equal(t, "refs/heads/old/nested", strings.TrimSpace(runGitOutput(t, dir, "for-each-ref", "--format=%(refname)", "refs/heads/old")))
	headers := strings.Split(strings.TrimSpace(runGitOutput(t, dir, "config", "--get-all", "http.extraHeader")), "\n")
	require.Equal(t, []string{"X-Sourcegraph-Actor-UID: internal", "X-Sourcegraph-Tenant-ID: 1"}, headers)
	searcher, err := search.NewDirectorySearcher(args.IndexDir)
	require.NoError(t, err)
	assertSearchContains(t, searcher, "devneedle", "dev.txt")
	searcher.Close()

	// Re-fetching the same commit with an expanded filter must supply blobs
	// that were omitted from the earlier partial clone.
	args.LargeFiles = []string{"big.bin"}
	require.NoError(t, gitIndex(context.Background(), c, &args, sourcegraphNop{}, logger))
	third, err := gitCache.readEntry(dir)
	require.NoError(t, err)
	require.True(t, third.Created.After(second.Created))
	require.False(t, third.Filtered)
	searcher, err = search.NewDirectorySearcher(args.IndexDir)
	require.NoError(t, err)
	assertSearchContains(t, searcher, "largeneedle", "big.bin")
	searcher.Close()

	// A failed delta fetch is retried in a fresh clone, not one containing a
	// partial fetch or lock file. The fallback still indexes the latest tip.
	args.UseDelta = true
	delta := c
	fetches := 0
	delta.runCmd = func(cmd *exec.Cmd) error {
		if slices.Contains(cmd.Args, "fetch") {
			fetches++
			if fetches == 1 {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "shallow.lock"), nil, 0o600))
				return errors.New("injected delta fetch failure")
			}
			require.NoFileExists(t, filepath.Join(dir, "shallow.lock"))
		}
		return c.runCmd(cmd)
	}
	require.NoError(t, gitIndex(context.Background(), delta, &args, sourcegraphNop{}, logger))
	require.Equal(t, 2, fetches)
	args.UseDelta = false

	// Failure at either stage discards the cache; the next job starts clean.
	for _, stage := range []string{"fetch", "zoekt-git-index"} {
		failing := c
		failing.runCmd = func(cmd *exec.Cmd) error {
			if slices.Contains(cmd.Args, stage) {
				return errors.New("injected " + stage + " failure")
			}
			return c.runCmd(cmd)
		}
		require.ErrorContains(t, gitIndex(context.Background(), failing, &args, sourcegraphNop{}, logger), "injected")
		require.NoDirExists(t, dir)
		require.NoError(t, gitIndex(context.Background(), c, &args, sourcegraphNop{}, logger))
	}

	// Turning caching off removes the clone even if the index is a no-op.
	args.CacheGitRepo = false
	args.Incremental = true
	server := &Server{logger: logger}
	state, err := server.index(context.Background(), &args)
	require.NoError(t, err)
	require.Equal(t, indexStateNoop, state)
	require.NoDirExists(t, dir)
	require.NoError(t, gitIndex(context.Background(), c, &args, sourcegraphNop{}, logger))
	require.NoDirExists(t, filepath.Join(os.TempDir(), "test%2Frepo.git"))
}

func TestGitCacheTenantDeletionAndRestart(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	logger := logtest.Scoped(t)
	server := &Server{IndexDir: t.TempDir(), logger: logger}
	require.NoError(t, os.MkdirAll(filepath.Join(server.IndexDir, ".trash"), 0o755))
	args := indexArgs{IndexOptions: IndexOptions{RepoID: 1, TenantID: 1, Name: "repo"}}
	one := writeCacheEntry(t, &args, gitRepoCacheEntry{Created: time.Now(), RepoID: 1})
	args.TenantID = 2
	two := writeCacheEntry(t, &args, gitRepoCacheEntry{Created: time.Now(), RepoID: 1})
	ctx, err := (tenant.Propagator{}).InjectContext(context.Background(), metadata.Pairs("X-Sourcegraph-Tenant-ID", "1"))
	require.NoError(t, err)
	_, err = server.DeleteAllData(ctx, &indexserverv1.DeleteAllDataRequest{})
	require.NoError(t, err)
	require.NoDirExists(t, one)
	require.DirExists(t, two)

	require.NoError(t, setupTmpDir(logger, true, root))
	dir := writeCacheEntry(t, &args, gitRepoCacheEntry{Created: time.Now(), RepoID: 1})
	require.NoError(t, setupTmpDir(logger, true, root))
	require.NoDirExists(t, dir)
}

func TestCacheGitRepoProtoAndIndexIdentity(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		item := indexOptionsItem{IndexOptions: IndexOptions{CacheGitRepo: enabled}}
		var got indexOptionsItem
		got.FromProto(item.ToProto())
		require.Equal(t, enabled, got.CacheGitRepo)
	}
	args := indexArgs{}
	before := args.BuildOptions()
	args.CacheGitRepo = true
	require.Equal(t, before, args.BuildOptions())
}
