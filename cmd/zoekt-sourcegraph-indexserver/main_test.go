package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	sglog "github.com/sourcegraph/log"
	"github.com/sourcegraph/log/logtest"
	"github.com/sourcegraph/zoekt"
	configv1 "github.com/sourcegraph/zoekt/cmd/zoekt-sourcegraph-indexserver/grpc/protos/sourcegraph/zoekt/configuration/v1"
	indexserverv1 "github.com/sourcegraph/zoekt/cmd/zoekt-sourcegraph-indexserver/grpc/protos/zoekt/indexserver/v1"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/internal/tenant"
	"github.com/stretchr/testify/require"
	"github.com/xeipuuv/gojsonschema"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestServer_defaultArgs(t *testing.T) {
	root, err := url.Parse("http://api.test")
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{
		Sourcegraph:      newSourcegraphClient(root, "", nil, WithBatchSize(0)),
		IndexDir:         "/testdata/index",
		CPUCount:         6,
		IndexConcurrency: 1,
	}
	want := &indexArgs{
		IndexOptions: IndexOptions{
			Name: "testName",
		},
		IndexDir:    "/testdata/index",
		Parallelism: 6,
		Incremental: true,
		FileLimit:   1 << 20,
	}
	got := s.indexArgs(IndexOptions{Name: "testName"})
	if !cmp.Equal(got, want) {
		t.Errorf("mismatch (-want +got):\n%s", cmp.Diff(want, got))
	}
}

func TestIndexNoTenant(t *testing.T) {
	s := &Server{}
	_, err := s.index(context.Background(), &indexArgs{})
	require.ErrorIs(t, err, tenant.ErrMissingTenant)
}

func TestServer_parallelism(t *testing.T) {
	root, err := url.Parse("http://api.test")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name             string
		cpuCount         int
		indexConcurrency int
		options          IndexOptions
		want             int
	}{
		{
			name:             "CPU count divides evenly",
			cpuCount:         16,
			indexConcurrency: 8,
			want:             2,
		},
		{
			name:             "no shard level parallelism",
			cpuCount:         4,
			indexConcurrency: 4,
			want:             1,
		},
		{
			name:             "index option overrides server flag",
			cpuCount:         2,
			indexConcurrency: 1,
			options: IndexOptions{
				ShardConcurrency: 1,
			},
			want: 1,
		},
		{
			name:             "ignore invalid index option",
			cpuCount:         8,
			indexConcurrency: 2,
			options: IndexOptions{
				ShardConcurrency: -1,
			},
			want: 4,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{
				Sourcegraph:      newSourcegraphClient(root, "", nil, WithBatchSize(0)),
				IndexDir:         "/testdata/index",
				CPUCount:         tt.cpuCount,
				IndexConcurrency: tt.indexConcurrency,
			}

			maxProcs := 16
			got := s.parallelism(tt.options, maxProcs)
			if tt.want != got {
				t.Errorf("mismatch, want: %d, got: %d", tt.want, got)
			}
		})
	}

	t.Run("index option is limited by available CPU", func(t *testing.T) {
		s := &Server{
			Sourcegraph:      newSourcegraphClient(root, "", nil, WithBatchSize(0)),
			IndexDir:         "/testdata/index",
			IndexConcurrency: 1,
		}

		got := s.indexArgs(IndexOptions{
			ShardConcurrency: 2048, // Some number that's way too high
		})

		if got.Parallelism >= 2048 {
			t.Errorf("parallelism should be limited by available CPUs, instead got %d", got.Parallelism)
		}
	})
}

func TestListRepoIDs(t *testing.T) {
	grpcClient := &mockGRPCClient{}

	clientOptions := []SourcegraphClientOption{
		WithBatchSize(0),
	}

	testURL := url.URL{Scheme: "http", Host: "does.not.matter"}
	testHostname := "test-hostname"
	s := newSourcegraphClient(&testURL, testHostname, grpcClient, clientOptions...)

	listCalled := false
	grpcClient.mockList = func(ctx context.Context, in *configv1.ListRequest, opts ...grpc.CallOption) (*configv1.ListResponse, error) {
		listCalled = true

		gotRepoIDs := in.GetIndexedIds()
		slices.Sort(gotRepoIDs)

		wantRepoIDs := []int32{1, 3}
		slices.Sort(wantRepoIDs)

		if diff := cmp.Diff(wantRepoIDs, gotRepoIDs); diff != "" {
			t.Errorf("indexed repoIDs mismatch (-want +got):\n%s", diff)
		}

		hostname := in.GetHostname()
		if diff := cmp.Diff(testHostname, hostname); diff != "" {
			t.Errorf("hostname mismatch (-want +got):\n%s", diff)
		}

		return &configv1.ListResponse{RepoIds: []int32{1, 2, 3}}, nil
	}

	ctx := context.Background()
	got, err := s.List(ctx, []uint32{1, 3})
	if err != nil {
		t.Fatal(err)
	}

	if !listCalled {
		t.Fatalf("List was not called")
	}

	receivedRepoIDs := got.IDs
	slices.Sort(receivedRepoIDs)

	expectedRepoIDs := []uint32{1, 2, 3}
	slices.Sort(expectedRepoIDs)

	if diff := cmp.Diff(expectedRepoIDs, receivedRepoIDs); diff != "" {
		t.Errorf("mismatch in list of all repoIDs (-want +got):\n%s", diff)
	}
}

func TestMain(m *testing.M) {
	flag.Parse()
	level := sglog.LevelInfo
	if !testing.Verbose() {
		log.SetOutput(io.Discard)
		debugLog.SetOutput(io.Discard)
		infoLog.SetOutput(io.Discard)
		errorLog.SetOutput(io.Discard)
		level = sglog.LevelNone
	}

	logtest.InitWithLevel(m, level)
	os.Exit(m.Run())
}

func TestCreateEmptyShard(t *testing.T) {
	dir := t.TempDir()

	args := &indexArgs{
		IndexOptions: IndexOptions{
			RepoID:   7,
			Name:     "empty-repo",
			CloneURL: "code/host",
		},
		Incremental: true,
		IndexDir:    dir,
		Parallelism: 1,
		FileLimit:   1,
	}

	if err := createEmptyShard(args); err != nil {
		t.Fatal(err)
	}

	bo := args.BuildOptions()
	bo.RepositoryDescription.Branches = []zoekt.RepositoryBranch{{Name: "HEAD", Version: "404aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}

	if got := bo.IncrementalSkipIndexing(); !got {
		t.Fatalf("wanted %t, got %t", true, got)
	}
}

func TestFormatListUint32(t *testing.T) {
	cases := []struct {
		in   []uint32
		want string
	}{
		{
			in:   []uint32{42, 8, 3},
			want: "42, 8, ...",
		},
		{
			in:   []uint32{42, 8},
			want: "42, 8",
		},
		{
			in:   []uint32{42},
			want: "42",
		},
		{
			in:   []uint32{},
			want: "",
		},
	}

	for _, tt := range cases {
		t.Run(fmt.Sprintf("%v", tt.in), func(t *testing.T) {
			out := formatListUint32(tt.in, 2)
			if out != tt.want {
				t.Fatalf("want %s, got %s", tt.want, out)
			}
		})
	}
}

func TestDefaultGRPCServiceConfigurationSyntax(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	schemaFile := filepath.Join(wd, "json_schemas", "ServiceConfig.json")
	schemaLoader := gojsonschema.NewReferenceLoader(fmt.Sprintf("file://%s", schemaFile))

	documentLoader := gojsonschema.NewStringLoader(defaultGRPCServiceConfigurationJSON)

	result, err := gojsonschema.Validate(schemaLoader, documentLoader)
	if err != nil {
		t.Fatalf("failed to validate default service config: %v", err)
	}

	if !result.Valid() {
		var errs strings.Builder
		for _, err := range result.Errors() {
			errs.WriteString(fmt.Sprintf("- %s\n", err))
		}

		t.Fatalf("default service config is invalid:\n%s", errs.String())
	}
}

func TestGetBoolFromEnvironmentVariables(t *testing.T) {
	testCases := []struct {
		name         string
		envVarsToSet map[string]string

		envVarNames []string
		defaultBool bool

		wantBool bool
		wantErr  bool
	}{
		{
			name: "respect default value: true",

			envVarsToSet: map[string]string{},

			envVarNames: []string{"FOO", "BAR"},
			defaultBool: true,

			wantBool: true,
		},
		{
			name: "respect default value: false",

			envVarsToSet: map[string]string{},

			envVarNames: []string{"FOO", "BAR"},
			defaultBool: false,

			wantBool: false,
		},
		{
			name: "read from environment",

			envVarsToSet: map[string]string{"FOO": "1"},

			envVarNames: []string{"FOO"},
			defaultBool: false,

			wantBool: true,
		},
		{
			name: "read from first env var that is set",

			envVarsToSet: map[string]string{
				"BAR": "false",
				"BAZ": "true",
			},

			envVarNames: []string{"FOO", "BAR", "BAZ"},
			defaultBool: true,

			wantBool: false,
		},

		{
			name: "should error for invalid input",

			envVarsToSet: map[string]string{"INVALID": "not a boolean"},

			envVarNames: []string{"INVALID"},
			defaultBool: false,

			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			// Prepare the environment by loading all the appropriate environment variables
			for _, v := range tc.envVarNames {
				_ = os.Unsetenv(v)
			}

			for k := range tc.envVarsToSet {
				_ = os.Unsetenv(k)
			}

			for k, v := range tc.envVarsToSet {
				t.Setenv(k, v)
			}

			// Run the test
			got, err := getBoolFromEnvironmentVariables(tc.envVarNames, tc.defaultBool)

			// Examine the results
			if tc.wantErr != (err != nil) {
				t.Fatalf("unexpected error (wantErr = %t): %v", tc.wantErr, err)
			}

			if got != tc.wantBool {
				t.Errorf("got %v, want %v", got, tc.wantBool)
			}
		})
	}
}

func TestAddDefaultPort(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "http no port",
			input: "http://example.com",
			want:  "http://example.com:80",
		},
		{
			name:  "http custom port",
			input: "http://example.com:90",
			want:  "http://example.com:90",
		},
		{
			name:  "https no port",
			input: "https://example.com",
			want:  "https://example.com:443",
		},
		{
			name:  "https custom port",
			input: "https://example.com:444",
			want:  "https://example.com:444",
		},
		{
			name:  "non-http scheme",
			input: "ftp://example.com",
			want:  "ftp://example.com",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "local file path",
			input: "/etc/hosts",
			want:  "/etc/hosts",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input, err := url.Parse(test.input)
			if err != nil {
				t.Fatalf("failed to parse test URL %q: %v", test.input, err)
			}

			got := addDefaultPort(input)
			if diff := cmp.Diff(test.want, got.String()); diff != "" {
				t.Errorf("addDefaultPort(%q) mismatch (-want +got):\n%s", test.input, diff)
			}
		})
	}
}

func TestIndexGRPC(t *testing.T) {
	indexDir := t.TempDir()

	// Minimal server setup
	s := &Server{
		logger:         logtest.NoOp(t),
		IndexDir:       indexDir,
		rootURL:        &url.URL{Scheme: "http", Host: "example.com"},
		indexSemaphore: make(chan struct{}, 1),
		timeout:        time.Hour, // no timeout
	}

	branches := []*configv1.ZoektRepositoryBranch{
		{
			Name:    "HEAD",
			Version: "abc123",
		},
	}

	req := &indexserverv1.IndexRequest{
		Options: &configv1.ZoektIndexOptions{
			RepoId:   42,
			Name:     "repo",
			TenantId: 1,
			Branches: branches,
		},
	}

	resp, err := s.indexGRPC(context.Background(), req, mockIndexFunc(t))
	require.NoError(t, err)
	require.Equal(t, &indexserverv1.IndexResponse{
		RepoId:        42,
		Branches:      branches,
		IndexTimeUnix: resp.IndexTimeUnix, // Hack: this changes every time so we don't check it
	}, resp)

	require.NotZero(t, resp.IndexTimeUnix)
}

func TestIndexGRPC_Timeout(t *testing.T) {
	indexDir := t.TempDir()

	s := &Server{
		logger:           logtest.NoOp(t),
		IndexDir:         indexDir,
		IndexConcurrency: 0, // impossible to acquire index slot
		timeout:          time.Millisecond,
	}

	req := &indexserverv1.IndexRequest{
		Options: &configv1.ZoektIndexOptions{
			RepoId: 42,
			Name:   "repo",
		},
	}

	// use context.Background() to make sure we don't return because of context cancellation
	_, err := s.indexGRPC(context.Background(), req, mockIndexFunc(t))
	require.Error(t, err)
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
}

func TestDelete(t *testing.T) {
	indexDir := t.TempDir()
	trashDir := filepath.Join(indexDir, ".trash")
	if err := os.MkdirAll(trashDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a simple shard
	createShard(t, indexDir)

	// Verify the shard exists in index dir
	shards := getShards(indexDir)
	if len(shards) != 1 {
		t.Fatalf("expected 1 shard, got %d", len(shards))
	}

	// Create server and call Delete
	s := &Server{
		logger:         logtest.NoOp(t),
		IndexDir:       indexDir,
		rootURL:        &url.URL{Scheme: "http", Host: "example.com"},
		indexSemaphore: make(chan struct{}, 1),
		timeout:        time.Hour, // no timeout
	}

	req := &indexserverv1.DeleteRequest{
		RepoIds: []uint32{42}, // matches the repo ID in createShard
	}

	// Test case: context is canceled
	cancledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.Delete(cancledCtx, req)
	require.Error(t, err)

	shards = getShards(indexDir)
	require.Len(t, shards, 1)

	// Test case: context is not canceled
	_, err = s.Delete(context.Background(), req)
	require.NoError(t, err)

	// Verify shard was moved to trash
	trashShards := getShards(trashDir)
	require.Len(t, trashShards, 1)

	// Verify shard is no longer in index dir
	shards = getShards(indexDir)
	require.Len(t, shards, 0)
}

func mockIndexFunc(t *testing.T) func(ctx context.Context, args *indexArgs) (indexState, error) {
	return func(ctx context.Context, args *indexArgs) (indexState, error) {
		createShard(t, args.IndexDir)
		return indexStateSuccess, nil
	}
}

func createShard(t *testing.T, dir string) {
	opts := index.Options{
		IndexDir: dir,
		RepositoryDescription: zoekt.Repository{
			ID:   42,
			Name: "repo",
			Branches: []zoekt.RepositoryBranch{
				{
					Name:    "HEAD",
					Version: "abc123",
				},
			},
		},
	}

	b, err := index.NewBuilder(opts)
	require.NoError(t, err)
	require.NoError(t, b.AddFile("test.txt", []byte("hello")))
	require.NoError(t, b.Finish())
}

func TestRecoverFromTrash(t *testing.T) {
	dir := t.TempDir()
	trashDir := filepath.Join(dir, ".trash")
	require.NoError(t, os.MkdirAll(trashDir, 0o755))

	// Create a simple shard in trash
	createTestShard(t, "repo1", 1, filepath.Join(trashDir, "repo1.zoekt"))

	// Create a compound shard with two repos, one of them tombstoned
	cs := createCompoundShard(t, dir, []uint32{2, 3})
	require.NoError(t, index.SetTombstone(cs, 2))

	s := &Server{
		IndexDir: dir,
	}

	// Test recovering from trash
	recovered := s.recoverFromTrash(1)
	require.True(t, recovered, "should have recovered repo1 from trash")

	// Verify shard was moved from trash to index
	indexShards := getShards(dir)
	trashShards := getShards(trashDir)

	require.Contains(t, indexShards, uint32(1), "repo1 should be in index")
	require.NotContains(t, trashShards, uint32(1), "repo1 should not be in trash")

	// Test unsetting tombstone
	recovered = s.recoverFromTrash(2)
	require.True(t, recovered, "should have recovered repo2 from tombstone")

	// Verify tombstone was unset
	repos, _, err := index.ReadMetadataPath(cs)
	require.NoError(t, err)

	for _, repo := range repos {
		if repo.ID == 2 {
			require.False(t, repo.Tombstone, "repo2 should not be tombstoned")
		}
	}

	// Test non-existent repo
	recovered = s.recoverFromTrash(99)
	require.False(t, recovered, "should not have recovered non-existent repo")
}
