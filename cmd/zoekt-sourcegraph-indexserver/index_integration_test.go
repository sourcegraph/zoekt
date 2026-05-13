package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sourcegraph/log/logtest"
	"github.com/stretchr/testify/require"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/gitindex"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/search"
)

func TestFetchRepoAndIndex_Integration(t *testing.T) {
	requireGitDaemon(t)

	for _, tc := range []struct {
		name                     string
		disableGoGitOptimization bool
	}{
		{name: "optimized repo open"},
		{name: "legacy repo open", disableGoGitOptimization: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require := require.New(t)

			ctx := context.Background()
			fixture := newGitFetchFixture(t)

			if tc.disableGoGitOptimization {
				t.Setenv("ZOEKT_DISABLE_GOGIT_OPTIMIZATION", "true")
			} else {
				t.Setenv("ZOEKT_DISABLE_GOGIT_OPTIMIZATION", "false")
			}

			sg := &recordingSourcegraph{
				opts: IndexOptions{
					RepoID:   123,
					Name:     "test/repo",
					CloneURL: fixture.cloneURL,
					Symbols:  false,
					Branches: []zoekt.RepositoryBranch{
						{Name: "HEAD", Version: fixture.mainCommit},
						{Name: "dev", Version: fixture.devCommit},
					},
					TenantID: 1,
				},
			}

			indexDir := t.TempDir()
			server := &Server{
				Sourcegraph:      sg,
				IndexDir:         indexDir,
				CPUCount:         1,
				IndexConcurrency: 1,
			}

			result, err := sg.List(ctx, nil)
			require.NoError(err)

			var args *indexArgs
			result.IterateIndexOptions(func(opts IndexOptions) {
				args = server.indexArgs(opts)
			})
			require.NotNil(args)

			gitDir := filepath.Join(t.TempDir(), "fetch.git")
			c := gitIndexConfig{
				runCmd: runIntegrationCommand,
				findRepositoryMetadata: func(args *indexArgs) (*zoekt.Repository, *zoekt.IndexMetadata, bool, error) {
					return args.BuildOptions().FindRepositoryMetadata()
				},
				timeout: time.Minute,
			}

			require.NoError(fetchRepo(ctx, gitDir, args, c, logtest.Scoped(t)))
			assertPartialBareFetch(t, gitDir, fixture)

			require.NoError(setZoektConfig(ctx, gitDir, args, c))

			updated, err := gitindex.IndexGitRepo(gitIndexOptionsForTest(args, gitDir))
			require.NoError(err)
			require.True(updated)

			repository, metadata, ok, err := args.BuildOptions().FindRepositoryMetadata()
			require.NoError(err)
			require.True(ok)
			require.Equal(args.Name, repository.Name)
			require.Equal(args.RepoID, repository.ID)
			require.Equal(args.TenantID, repository.TenantID)
			if diff := cmp.Diff(args.Branches, repository.Branches); diff != "" {
				t.Fatalf("branches mismatch (-want +got):\n%s", diff)
			}
			require.Equal("123", repository.RawConfig["repoid"])
			require.Equal("1", repository.RawConfig["tenantID"])

			searcher, err := search.NewDirectorySearcher(indexDir)
			require.NoError(err)
			defer searcher.Close()

			assertSearchContains(t, searcher, "smallneedle", "small.txt")
			assertSearchContains(t, searcher, "devneedle", "dev.txt")
			assertSearchEmpty(t, searcher, "largeneedle")

			require.NoError(updateIndexStatusOnSourcegraph(c, args, sg))
			require.Len(sg.updates, 1)
			require.Len(sg.updates[0], 1)
			require.Equal(args.RepoID, sg.updates[0][0].RepoID)
			require.Equal(metadata.IndexTime.Unix(), sg.updates[0][0].IndexTimeUnix)
			if diff := cmp.Diff(args.Branches, sg.updates[0][0].Branches); diff != "" {
				t.Fatalf("status branches mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func requireGitDaemon(t *testing.T) {
	t.Helper()

	cmd := exec.Command("git", "daemon", "-h")
	cmd.Env = gitTestEnv()
	output, err := cmd.CombinedOutput()
	text := string(output)

	if strings.Contains(text, "usage: git daemon") {
		return
	}

	if strings.Contains(text, "git: 'daemon' is not a git command") {
		t.Skipf("skipping integration test: git daemon is unavailable: %s", strings.TrimSpace(text))
	}

	if err == nil {
		return
	}

	t.Fatalf("failed to probe git daemon availability: %v\n%s", err, text)
}

type recordingSourcegraph struct {
	opts    IndexOptions
	updates [][]indexStatus
}

func (s *recordingSourcegraph) List(ctx context.Context, indexed []uint32) (*SourcegraphListResult, error) {
	return &SourcegraphListResult{
		IDs: []uint32{s.opts.RepoID},
		IterateIndexOptions: func(yield func(IndexOptions)) {
			yield(s.opts)
		},
	}, nil
}

func (s *recordingSourcegraph) ForceIterateIndexOptions(onSuccess func(IndexOptions), onError func(uint32, error), repos ...uint32) {
	onSuccess(s.opts)
}

func (s *recordingSourcegraph) UpdateIndexStatus(repositories []indexStatus) error {
	cp := make([]indexStatus, len(repositories))
	copy(cp, repositories)
	s.updates = append(s.updates, cp)
	return nil
}

type gitFetchFixture struct {
	cloneURL   string
	mainCommit string
	devCommit  string
	bigBlob    string
	daemon     *gitDaemon
}

func newGitFetchFixture(t *testing.T) *gitFetchFixture {
	t.Helper()

	root := t.TempDir()
	worktree := filepath.Join(root, "worktree")
	serveRoot := filepath.Join(root, "serve")
	remoteDir := filepath.Join(serveRoot, "repo")

	require.NoError(t, os.MkdirAll(worktree, 0o755))
	require.NoError(t, os.MkdirAll(serveRoot, 0o755))

	runGit(t, root, "init", "-b", "main", worktree)
	runGit(t, worktree, "config", "user.name", "Test User")
	runGit(t, worktree, "config", "user.email", "test@example.com")

	require.NoError(t, os.WriteFile(filepath.Join(worktree, "small.txt"), []byte("smallneedle\n"), 0o644))
	large := strings.Repeat("x", MaxFileSize+1024)
	require.NoError(t, os.WriteFile(filepath.Join(worktree, "big.bin"), []byte("largeneedle\n"+large), 0o644))
	runGit(t, worktree, "add", "small.txt", "big.bin")
	runGit(t, worktree, "commit", "-m", "main commit")

	mainCommit := strings.TrimSpace(runGitOutput(t, worktree, "rev-parse", "HEAD"))
	bigBlob := strings.TrimSpace(runGitOutput(t, worktree, "rev-parse", "HEAD:big.bin"))

	runGit(t, worktree, "checkout", "-b", "dev")
	require.NoError(t, os.WriteFile(filepath.Join(worktree, "dev.txt"), []byte("devneedle\n"), 0o644))
	runGit(t, worktree, "add", "dev.txt")
	runGit(t, worktree, "commit", "-m", "dev commit")

	devCommit := strings.TrimSpace(runGitOutput(t, worktree, "rev-parse", "HEAD"))
	runGit(t, root, "clone", "--bare", worktree, remoteDir)
	runGit(t, remoteDir, "config", "uploadpack.allowFilter", "true")
	runGit(t, remoteDir, "config", "uploadpack.allowAnySHA1InWant", "true")

	daemon := startGitDaemon(t, serveRoot)

	return &gitFetchFixture{
		cloneURL:   fmt.Sprintf("git://127.0.0.1:%d/repo", daemon.port),
		mainCommit: mainCommit,
		devCommit:  devCommit,
		bigBlob:    bigBlob,
		daemon:     daemon,
	}
}

type gitDaemon struct {
	cmd     *exec.Cmd
	logPath string
	port    int
}

func startGitDaemon(t *testing.T, serveRoot string) *gitDaemon {
	t.Helper()

	port := allocatePort(t)
	logFile, err := os.CreateTemp(t.TempDir(), "git-daemon-*.log")
	require.NoError(t, err)
	logPath := logFile.Name()
	cmd := exec.Command("git", "daemon",
		"--verbose",
		"--export-all",
		"--reuseaddr",
		"--base-path="+serveRoot,
		"--listen=127.0.0.1",
		fmt.Sprintf("--port=%d", port),
		serveRoot,
	)
	cmd.Env = gitTestEnv()
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	require.NoError(t, cmd.Start())
	require.NoError(t, logFile.Close())
	waitForGitDaemon(t, port, logPath)

	daemon := &gitDaemon{cmd: cmd, logPath: logPath, port: port}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		waitDone := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(waitDone)
		}()

		select {
		case <-waitDone:
		case <-time.After(5 * time.Second):
		}
	})

	return daemon
}

func waitForGitDaemon(t *testing.T, port int, logPath string) {
	t.Helper()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}

		time.Sleep(50 * time.Millisecond)
	}

	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("git daemon did not start listening on %s within 5s (failed to read log: %v)", addr, err)
	}

	t.Fatalf("git daemon did not start listening on %s within 5s\n%s", addr, contents)
}

func allocatePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	return listener.Addr().(*net.TCPAddr).Port
}

func gitIndexOptionsForTest(args *indexArgs, repoDir string) gitindex.Options {
	buildOptions := *args.BuildOptions()
	buildOptions.RepositoryDescription.Branches = nil

	branches := make([]string, 0, len(args.Branches))
	for _, branch := range args.Branches {
		branches = append(branches, branch.Name)
	}

	return gitindex.Options{
		RepoDir:                           repoDir,
		Submodules:                        false,
		Incremental:                       args.Incremental,
		BuildOptions:                      buildOptions,
		BranchPrefix:                      "refs/heads/",
		Branches:                          branches,
		DeltaShardNumberFallbackThreshold: args.DeltaShardNumberFallbackThreshold,
	}
}

func assertPartialBareFetch(t *testing.T, gitDir string, fixture *gitFetchFixture) {
	t.Helper()
	require := require.New(t)

	require.Equal(fixture.mainCommit, strings.TrimSpace(runGitOutput(t, gitDir, "rev-parse", "HEAD")))
	require.Equal(fixture.devCommit, strings.TrimSpace(runGitOutput(t, gitDir, "rev-parse", "refs/heads/dev")))

	promisors, err := filepath.Glob(filepath.Join(gitDir, "objects", "pack", "*.promisor"))
	require.NoError(err)
	require.NotEmpty(promisors)

	objects := runGitOutput(t, gitDir, "rev-list", "--objects", "--missing=print", "HEAD", "refs/heads/dev")
	require.Contains(objects, fixture.mainCommit)
	require.Contains(objects, fixture.devCommit)
	require.Contains(objects, "?"+fixture.bigBlob)
}

func assertSearchContains(t *testing.T, searcher zoekt.Searcher, pattern string, wantFile string) {
	t.Helper()
	require := require.New(t)

	result, err := searcher.Search(context.Background(), &query.Substring{Pattern: pattern}, &zoekt.SearchOptions{})
	require.NoError(err)
	require.Len(result.Files, 1)
	require.Equal(wantFile, result.Files[0].FileName)
}

func assertSearchEmpty(t *testing.T, searcher zoekt.Searcher, pattern string) {
	t.Helper()
	require := require.New(t)

	result, err := searcher.Search(context.Background(), &query.Substring{Pattern: pattern}, &zoekt.SearchOptions{})
	require.NoError(err)
	require.Empty(result.Files)
}

func runIntegrationCommand(cmd *exec.Cmd) error {
	cmd.Env = gitTestEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", strings.Join(cmd.Args, " "), err, output)
	}
	return nil
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = runGitOutput(t, dir, args...)
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitTestEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}

	return string(output)
}

func gitTestEnv() []string {
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL=",
		"GIT_CONFIG_SYSTEM=",
	)
}
