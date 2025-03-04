package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sourcegraph/log/logtest"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/timestamppb"

	proto "github.com/sourcegraph/zoekt/cmd/zoekt-sourcegraph-indexserver/grpc/protos/sourcegraph/zoekt/configuration/v1"
	"github.com/sourcegraph/zoekt/internal/ctags"
	"github.com/sourcegraph/zoekt/internal/tenant/tenanttest"

	"github.com/sourcegraph/zoekt"
)

func TestIterateIndexOptions_Fingerprint(t *testing.T) {
	fingerprintV0 := &proto.Fingerprint{
		Identifier:  100,
		GeneratedAt: timestamppb.New(time.Unix(100, 0)),
	}

	fingerprintV1 := &proto.Fingerprint{
		Identifier:  101,
		GeneratedAt: timestamppb.New(time.Unix(101, 0)),
	}

	fingerprintV2 := &proto.Fingerprint{
		Identifier:  102,
		GeneratedAt: timestamppb.New(time.Unix(102, 0)),
	}

	mkSearchConfigurationResponse := func(fingerprint *proto.Fingerprint, repoIDs ...int32) *proto.SearchConfigurationResponse {
		repositories := make([]*proto.ZoektIndexOptions, 0, len(repoIDs))
		for _, repoID := range repoIDs {
			repositories = append(repositories, &proto.ZoektIndexOptions{
				RepoId: repoID,
			})
		}

		return &proto.SearchConfigurationResponse{
			UpdatedOptions: repositories,
			Fingerprint:    fingerprint,
		}
	}

	grpcClient := &mockGRPCClient{
		mockList: func(_ context.Context, in *proto.ListRequest, opts ...grpc.CallOption) (*proto.ListResponse, error) {
			return &proto.ListResponse{
				RepoIds: []int32{1, 2, 3},
			}, nil
		},
	}

	clientOpts := []SourcegraphClientOption{
		WithBatchSize(1),
	}

	testURL := url.URL{Scheme: "http", Host: "does.not.matter", Path: "/"}
	sg := newSourcegraphClient(&testURL, "", grpcClient, clientOpts...)

	type step struct {
		name string

		wantFingerprint     *proto.Fingerprint
		returnFingerprint   *proto.Fingerprint
		returnErr           error
		skipCheckingRepoIDs bool
	}

	for _, step := range []step{
		{
			name:              "first call",
			wantFingerprint:   nil,
			returnFingerprint: fingerprintV0,
		},
		{
			name:              "second call (should provide fingerprint from last time)",
			wantFingerprint:   fingerprintV0,
			returnFingerprint: fingerprintV1,
		},
		{
			name:              "error",
			wantFingerprint:   fingerprintV1,
			returnFingerprint: fingerprintV2,

			returnErr:           fmt.Errorf("boom"),
			skipCheckingRepoIDs: true, // don't bother checking repoIDs if we expect an error
		},
		{
			name:              "call after error (should ignore fingerprint from last time, and provide the older one)",
			wantFingerprint:   fingerprintV1,
			returnFingerprint: fingerprintV2,
		},
	} {
		t.Run(step.name, func(t *testing.T) {
			called := false
			grpcClient.mockSearchConfiguration = func(_ context.Context, in *proto.SearchConfigurationRequest, opts ...grpc.CallOption) (*proto.SearchConfigurationResponse, error) {
				called = true

				diff := cmp.Diff(step.wantFingerprint, in.GetFingerprint(), protocmp.Transform())
				if diff != "" {
					t.Fatalf("unexpected fingerprint (-want +got):\n%s", diff)
				}

				return mkSearchConfigurationResponse(step.returnFingerprint, in.RepoIds...), step.returnErr
			}

			result, err := sg.List(context.Background(), nil)
			if err != nil {
				t.Fatalf("unexpected error from List: %v", err)
			}

			var iteratedIDs []uint32
			result.IterateIndexOptions(func(options IndexOptions) {
				iteratedIDs = append(iteratedIDs, options.RepoID)
			})

			if !called {
				t.Fatal("expected SearchConfiguration to be called")
			}

			if step.skipCheckingRepoIDs {
				return
			}

			sort.Slice(iteratedIDs, func(i, j int) bool {
				return iteratedIDs[i] < iteratedIDs[j]
			})

			expectedIDs := []uint32{1, 2, 3}
			sort.Slice(expectedIDs, func(i, j int) bool {
				return expectedIDs[i] < expectedIDs[j]
			})

			if diff := cmp.Diff(expectedIDs, iteratedIDs); diff != "" {
				t.Fatalf("unexpected repo ids (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGetIndexOptions(t *testing.T) {

	type testCase struct {
		name     string
		response *proto.SearchConfigurationResponse
		want     *IndexOptions
		wantErr  string
	}

	for _, tc := range []testCase{
		{
			name: "symbols, large files",
			response: &proto.SearchConfigurationResponse{
				UpdatedOptions: []*proto.ZoektIndexOptions{
					{
						Symbols:    true,
						LargeFiles: []string{"foo", "bar"},
					},
				},
			},
			want: &IndexOptions{
				Symbols:    true,
				LargeFiles: []string{"foo", "bar"},
			},
		},
		{
			name: "no symbols , large files",
			response: &proto.SearchConfigurationResponse{
				UpdatedOptions: []*proto.ZoektIndexOptions{
					{
						Symbols:    true,
						LargeFiles: []string{"foo", "bar"},
					},
				},
			},
			want: &IndexOptions{
				Symbols:    true,
				LargeFiles: []string{"foo", "bar"},
			},
		},

		{
			name:     "empty",
			response: nil,
			want:     nil,
		},

		{
			name: "symbols",
			response: &proto.SearchConfigurationResponse{
				UpdatedOptions: []*proto.ZoektIndexOptions{
					{
						Symbols: true,
					},
				},
			},
			want: &IndexOptions{
				Symbols: true,
			},
		},
		{
			name: "repoID",
			response: &proto.SearchConfigurationResponse{
				UpdatedOptions: []*proto.ZoektIndexOptions{
					{
						RepoId: 123,
					},
				},
			},
			want: &IndexOptions{
				RepoID: 123,
			},
		},
		{
			name: "error",
			response: &proto.SearchConfigurationResponse{
				UpdatedOptions: []*proto.ZoektIndexOptions{
					{
						Error: "boom",
					},
				},
			},
			want:    nil,
			wantErr: "boom",
		},
	} {
		called := false
		mockClient := &mockGRPCClient{
			mockSearchConfiguration: func(_ context.Context, _ *proto.SearchConfigurationRequest, _ ...grpc.CallOption) (*proto.SearchConfigurationResponse, error) {
				called = true
				return tc.response, nil
			},
		}

		testURL := &url.URL{
			Scheme: "http",
			Host:   "does.not.matter",
			Path:   "/",
		}

		sg := newSourcegraphClient(
			testURL,
			"",
			mockClient,
		)

		var got IndexOptions
		var err error
		sg.ForceIterateIndexOptions(func(o IndexOptions) {
			got = o
		}, func(_ uint32, e error) {
			err = e
		}, 123)

		if !called {
			t.Fatal("expected mock to be called")
		}

		if err != nil {
			if tc.wantErr == "" || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("unexpected error: %v", err)
			}
		}

		if tc.want == nil {
			continue
		}

		tc.want.CloneURL = sg.getCloneURL(got.Name)

		if diff := cmp.Diff(tc.want, &got, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("mismatch (-want +got):\n%s", diff)
		}
	}

	// Mimic our fingerprint API, which doesn't return anything if the
	// repo hasn't changed.
	t.Run("unchanged", func(t *testing.T) {

		called := false
		mockClient := &mockGRPCClient{
			mockSearchConfiguration: func(_ context.Context, _ *proto.SearchConfigurationRequest, _ ...grpc.CallOption) (*proto.SearchConfigurationResponse, error) {
				called = true
				return nil, nil
			},
		}

		testURL := &url.URL{
			Scheme: "http",
			Host:   "does.not.matter",
			Path:   "/",
		}

		sg := newSourcegraphClient(
			testURL,
			"",
			mockClient,
		)
		gotAtLeastOneOption := false
		var err error
		sg.ForceIterateIndexOptions(func(_ IndexOptions) {
			gotAtLeastOneOption = true
		}, func(_ uint32, e error) {
			err = e
		}, 123)

		if !called {
			t.Fatal("expected mock to be called")
		}

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if gotAtLeastOneOption {
			t.Fatalf("expected no options, got %v", gotAtLeastOneOption)
		}
	})

	var response *proto.SearchConfigurationResponse
	mockClient := &mockGRPCClient{
		mockSearchConfiguration: func(_ context.Context, req *proto.SearchConfigurationRequest, _ ...grpc.CallOption) (*proto.SearchConfigurationResponse, error) {
			if len(req.GetRepoIds()) == 0 || req.GetRepoIds()[0] != 123 {
				return nil, errors.New("invalid repo id")
			}
			return response, nil
		},
	}

	sg := newSourcegraphClient(&url.URL{Path: "/"}, "", mockClient, WithBatchSize(0))

	cases := []struct {
		Response *proto.SearchConfigurationResponse
		*IndexOptions
	}{
		{
			Response: &proto.SearchConfigurationResponse{
				UpdatedOptions: []*proto.ZoektIndexOptions{
					{
						Symbols:    true,
						LargeFiles: []string{"foo", "bar"},
					},
				},
			},
			IndexOptions: &IndexOptions{
				Symbols:     true,
				LargeFiles:  []string{"foo", "bar"},
				Branches:    []zoekt.RepositoryBranch{},
				LanguageMap: map[string]ctags.CTagsParserType{},
			},
		},

		{
			Response: &proto.SearchConfigurationResponse{
				UpdatedOptions: []*proto.ZoektIndexOptions{
					{
						Symbols:    false,
						LargeFiles: []string{"foo", "bar"},
					},
				},
			},
			IndexOptions: &IndexOptions{
				LargeFiles:  []string{"foo", "bar"},
				Branches:    []zoekt.RepositoryBranch{},
				LanguageMap: map[string]ctags.CTagsParserType{},
			},
		},

		{
			Response: &proto.SearchConfigurationResponse{},
		},

		{
			Response: &proto.SearchConfigurationResponse{
				UpdatedOptions: []*proto.ZoektIndexOptions{
					{
						Symbols: true,
					},
				},
			},
			IndexOptions: &IndexOptions{
				Symbols:     true,
				Branches:    []zoekt.RepositoryBranch{},
				LanguageMap: map[string]ctags.CTagsParserType{},
			},
		},

		{
			Response: &proto.SearchConfigurationResponse{
				UpdatedOptions: []*proto.ZoektIndexOptions{
					{
						RepoId: 123,
					},
				},
			},
			IndexOptions: &IndexOptions{
				RepoID:      123,
				Branches:    []zoekt.RepositoryBranch{},
				LanguageMap: map[string]ctags.CTagsParserType{},
			},
		},

		{
			Response: &proto.SearchConfigurationResponse{
				UpdatedOptions: []*proto.ZoektIndexOptions{
					{
						Error: "boom",
					},
				},
			},
		},
	}

	for _, tc := range cases {
		response = tc.Response

		var got IndexOptions
		var err error
		sg.ForceIterateIndexOptions(func(o IndexOptions) {
			got = o
		}, func(_ uint32, e error) {
			err = e
		}, 123)

		if err != nil && tc.IndexOptions != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tc.IndexOptions == nil {
			continue
		}

		tc.IndexOptions.CloneURL = sg.getCloneURL(got.Name)

		if d := cmp.Diff(*tc.IndexOptions, got); d != "" {
			t.Errorf("mismatch (-want +got):\n%s", d)
		}
	}

	// Special case our fingerprint API which doesn't return anything if the
	// repo hasn't changed.
	t.Run("unchanged", func(t *testing.T) {
		response = &proto.SearchConfigurationResponse{}

		got := false
		var err error
		sg.ForceIterateIndexOptions(func(_ IndexOptions) {
			got = true
		}, func(_ uint32, e error) {
			err = e
		}, 123)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if got {
			t.Fatalf("expected no options, got %v", got)
		}
	})
}

func TestIndexTenant(t *testing.T) {
	tenanttest.MockEnforce(t)

	cases := []struct {
		name                   string
		args                   indexArgs
		mockRepositoryMetadata *zoekt.Repository
		want                   []string
	}{
		{
			name: "prefix",
			args: indexArgs{
				IndexOptions: IndexOptions{
					RepoID:   13,
					Name:     "test/repo",
					CloneURL: "http://api.test/.internal/git/test/repo",
					Branches: []zoekt.RepositoryBranch{{Name: "HEAD", Version: "deadbeef"}},
					TenantID: 42,
				},
			},
			want: []string{
				"git -c init.defaultBranch=nonExistentBranchBB0FOFCH32 init --bare $TMPDIR/test%2Frepo.git",
				"git -C $TMPDIR/test%2Frepo.git -c protocol.version=2 -c http.extraHeader=X-Sourcegraph-Actor-UID: internal -c http.extraHeader=X-Sourcegraph-Tenant-ID: 42 fetch --depth=1 --no-tags --filter=blob:limit=1m http://api.test/.internal/git/test/repo deadbeef",
				"git -C $TMPDIR/test%2Frepo.git update-ref HEAD deadbeef",
				"git -C $TMPDIR/test%2Frepo.git config zoekt.archived 0",
				"git -C $TMPDIR/test%2Frepo.git config zoekt.fork 0",
				"git -C $TMPDIR/test%2Frepo.git config zoekt.latestCommitDate 1",
				"git -C $TMPDIR/test%2Frepo.git config zoekt.name test/repo",
				"git -C $TMPDIR/test%2Frepo.git config zoekt.priority 0",
				"git -C $TMPDIR/test%2Frepo.git config zoekt.public 0",
				"git -C $TMPDIR/test%2Frepo.git config zoekt.repoid 13",
				"git -C $TMPDIR/test%2Frepo.git config zoekt.tenantID 42",
				"zoekt-git-index -submodules=false -branches HEAD -disable_ctags -shard_prefix 000000042_000000013 $TMPDIR/test%2Frepo.git",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			runCmd := func(c *exec.Cmd) error {
				cmd := strings.Join(c.Args, " ")
				cmd = strings.ReplaceAll(cmd, filepath.Clean(os.TempDir()), "$TMPDIR")
				got = append(got, cmd)
				return nil
			}

			findRepositoryMetadata := func(args *indexArgs) (repository *zoekt.Repository, metadata *zoekt.IndexMetadata, ok bool, err error) {
				if tc.mockRepositoryMetadata == nil {
					return args.BuildOptions().FindRepositoryMetadata()
				}

				return tc.mockRepositoryMetadata, &zoekt.IndexMetadata{}, true, nil
			}

			c := gitIndexConfig{
				runCmd:                 runCmd,
				findRepositoryMetadata: findRepositoryMetadata,
			}

			if err := gitIndex(c, &tc.args, sourcegraphNop{}, logtest.Scoped(t)); err != nil {
				t.Fatal(err)
			}
			if !cmp.Equal(got, tc.want) {
				t.Errorf("git mismatch (-want +got):\n%s", cmp.Diff(tc.want, got, splitargs))
			}
		})
	}
}

func TestIndex(t *testing.T) {
	cases := []struct {
		name                   string
		args                   indexArgs
		mockRepositoryMetadata *zoekt.Repository
		want                   []string
	}{{
		name: "minimal",
		args: indexArgs{
			IndexOptions: IndexOptions{
				Name:     "test/repo",
				CloneURL: "http://api.test/.internal/git/test/repo",
				Branches: []zoekt.RepositoryBranch{{Name: "HEAD", Version: "deadbeef"}},
				TenantID: 42,
			},
		},
		want: []string{
			"git -c init.defaultBranch=nonExistentBranchBB0FOFCH32 init --bare $TMPDIR/test%2Frepo.git",
			"git -C $TMPDIR/test%2Frepo.git -c protocol.version=2 -c http.extraHeader=X-Sourcegraph-Actor-UID: internal -c http.extraHeader=X-Sourcegraph-Tenant-ID: 42 fetch --depth=1 --no-tags --filter=blob:limit=1m http://api.test/.internal/git/test/repo deadbeef",
			"git -C $TMPDIR/test%2Frepo.git update-ref HEAD deadbeef",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.archived 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.fork 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.latestCommitDate 1",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.name test/repo",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.priority 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.public 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.repoid 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.tenantID 42",
			"zoekt-git-index -submodules=false -branches HEAD -disable_ctags $TMPDIR/test%2Frepo.git",
		},
	}, {
		name: "minimal-id",
		args: indexArgs{
			IndexOptions: IndexOptions{
				Name:     "test/repo",
				CloneURL: "http://api.test/.internal/git/test/repo",
				Branches: []zoekt.RepositoryBranch{{Name: "HEAD", Version: "deadbeef"}},
				RepoID:   123,
				TenantID: 1,
			},
		},
		want: []string{
			"git -c init.defaultBranch=nonExistentBranchBB0FOFCH32 init --bare $TMPDIR/test%2Frepo.git",
			"git -C $TMPDIR/test%2Frepo.git -c protocol.version=2 -c http.extraHeader=X-Sourcegraph-Actor-UID: internal -c http.extraHeader=X-Sourcegraph-Tenant-ID: 1 fetch --depth=1 --no-tags --filter=blob:limit=1m http://api.test/.internal/git/test/repo deadbeef",
			"git -C $TMPDIR/test%2Frepo.git update-ref HEAD deadbeef",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.archived 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.fork 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.latestCommitDate 1",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.name test/repo",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.priority 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.public 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.repoid 123",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.tenantID 1",
			"zoekt-git-index -submodules=false -branches HEAD -disable_ctags $TMPDIR/test%2Frepo.git",
		},
	}, {
		name: "all",
		args: indexArgs{
			Incremental: true,
			IndexDir:    "/data/index",
			Parallelism: 4,
			FileLimit:   123,
			IndexOptions: IndexOptions{
				Name:       "test/repo",
				CloneURL:   "http://api.test/.internal/git/test/repo",
				LargeFiles: []string{"foo", "bar"},
				Symbols:    true,
				Branches: []zoekt.RepositoryBranch{
					{Name: "HEAD", Version: "deadbeef"},
					{Name: "dev", Version: "feebdaed"}, // ignored for archive
				},
				TenantID: 1,
			},
		},
		want: []string{
			"git -c init.defaultBranch=nonExistentBranchBB0FOFCH32 init --bare $TMPDIR/test%2Frepo.git",
			"git -C $TMPDIR/test%2Frepo.git -c protocol.version=2 -c http.extraHeader=X-Sourcegraph-Actor-UID: internal -c http.extraHeader=X-Sourcegraph-Tenant-ID: 1 fetch --depth=1 --no-tags http://api.test/.internal/git/test/repo deadbeef feebdaed",
			"git -C $TMPDIR/test%2Frepo.git update-ref HEAD deadbeef",
			"git -C $TMPDIR/test%2Frepo.git update-ref refs/heads/dev feebdaed",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.archived 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.fork 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.latestCommitDate 1",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.name test/repo",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.priority 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.public 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.repoid 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.tenantID 1",
			"zoekt-git-index -submodules=false -incremental -branches HEAD,dev " +
				"-file_limit 123 -parallelism 4 -index /data/index -require_ctags -large_file foo -large_file bar " +
				"$TMPDIR/test%2Frepo.git",
		},
	}, {
		name: "delta",
		args: indexArgs{
			Incremental: true,
			IndexDir:    "/data/index",
			Parallelism: 4,
			FileLimit:   123,
			UseDelta:    true,
			IndexOptions: IndexOptions{
				RepoID:     0,
				Name:       "test/repo",
				CloneURL:   "http://api.test/.internal/git/test/repo",
				LargeFiles: []string{"foo", "bar"},
				Symbols:    true,
				Branches: []zoekt.RepositoryBranch{
					{Name: "HEAD", Version: "deadbeef"},
					{Name: "dev", Version: "feebdaed"},
					{Name: "release", Version: "12345678"},
				},
				TenantID: 1,
			},
			DeltaShardNumberFallbackThreshold: 22,
		},
		mockRepositoryMetadata: &zoekt.Repository{
			ID:   0,
			Name: "test/repo",
			Branches: []zoekt.RepositoryBranch{
				{Name: "HEAD", Version: "oldhead"},
				{Name: "dev", Version: "olddev"},
				{Name: "release", Version: "oldrelease"},
			},
		},
		want: []string{
			"git -c init.defaultBranch=nonExistentBranchBB0FOFCH32 init --bare $TMPDIR/test%2Frepo.git",
			"git -C $TMPDIR/test%2Frepo.git -c protocol.version=2 -c http.extraHeader=X-Sourcegraph-Actor-UID: internal -c http.extraHeader=X-Sourcegraph-Tenant-ID: 1 fetch --depth=1 --no-tags http://api.test/.internal/git/test/repo deadbeef feebdaed 12345678 oldhead olddev oldrelease",
			"git -C $TMPDIR/test%2Frepo.git update-ref HEAD deadbeef",
			"git -C $TMPDIR/test%2Frepo.git update-ref refs/heads/dev feebdaed",
			"git -C $TMPDIR/test%2Frepo.git update-ref refs/heads/release 12345678",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.archived 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.fork 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.latestCommitDate 1",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.name test/repo",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.priority 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.public 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.repoid 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.tenantID 1",
			"zoekt-git-index -submodules=false -incremental -branches HEAD,dev,release " +
				"-delta -delta_threshold 22 -file_limit 123 -parallelism 4 -index /data/index -require_ctags -large_file foo -large_file bar " +
				"$TMPDIR/test%2Frepo.git",
		},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			runCmd := func(c *exec.Cmd) error {
				cmd := strings.Join(c.Args, " ")
				cmd = strings.ReplaceAll(cmd, filepath.Clean(os.TempDir()), "$TMPDIR")
				got = append(got, cmd)
				return nil
			}

			findRepositoryMetadata := func(args *indexArgs) (repository *zoekt.Repository, metadata *zoekt.IndexMetadata, ok bool, err error) {
				if tc.mockRepositoryMetadata == nil {
					return args.BuildOptions().FindRepositoryMetadata()
				}

				return tc.mockRepositoryMetadata, &zoekt.IndexMetadata{}, true, nil
			}

			c := gitIndexConfig{
				runCmd:                 runCmd,
				findRepositoryMetadata: findRepositoryMetadata,
			}

			if err := gitIndex(c, &tc.args, sourcegraphNop{}, logtest.Scoped(t)); err != nil {
				t.Fatal(err)
			}
			if !cmp.Equal(got, tc.want) {
				t.Errorf("git mismatch (-want +got):\n%s", cmp.Diff(tc.want, got, splitargs))
			}
		})
	}
}

var splitargs = cmpopts.AcyclicTransformer("splitargs", func(cmd string) []string {
	return strings.Split(cmd, " ")
})

type mockGRPCClient struct {
	mockSearchConfiguration func(context.Context, *proto.SearchConfigurationRequest, ...grpc.CallOption) (*proto.SearchConfigurationResponse, error)
	mockList                func(context.Context, *proto.ListRequest, ...grpc.CallOption) (*proto.ListResponse, error)
	mockUpdateIndexStatus   func(context.Context, *proto.UpdateIndexStatusRequest, ...grpc.CallOption) (*proto.UpdateIndexStatusResponse, error)
}

func (m *mockGRPCClient) SearchConfiguration(ctx context.Context, in *proto.SearchConfigurationRequest, opts ...grpc.CallOption) (*proto.SearchConfigurationResponse, error) {
	if m.mockSearchConfiguration != nil {
		return m.mockSearchConfiguration(ctx, in, opts...)
	}

	return nil, fmt.Errorf("mock RPC SearchConfiguration not implemented")
}

func (m *mockGRPCClient) List(ctx context.Context, in *proto.ListRequest, opts ...grpc.CallOption) (*proto.ListResponse, error) {
	if m.mockList != nil {
		return m.mockList(ctx, in, opts...)
	}

	return nil, fmt.Errorf("mock RPC List not implemented")
}

func (m *mockGRPCClient) UpdateIndexStatus(ctx context.Context, in *proto.UpdateIndexStatusRequest, opts ...grpc.CallOption) (*proto.UpdateIndexStatusResponse, error) {
	if m.mockUpdateIndexStatus != nil {
		return m.mockUpdateIndexStatus(ctx, in, opts...)
	}

	return nil, fmt.Errorf("mock RPC UpdateIndexStatus not implemented")
}

var _ proto.ZoektConfigurationServiceClient = &mockGRPCClient{}

// Tests whether we can set git config values without error.
func TestSetZoektConfig(t *testing.T) {
	dir := t.TempDir()

	// init git dir
	script := `mkdir repo
cd repo
git init -b main
`
	cmd := exec.Command("/bin/sh", "-euxc", script)
	cmd.Dir = dir
	_, err := cmd.CombinedOutput()
	require.NoError(t, err)

	var out []byte
	c := gitIndexConfig{
		runCmd: func(cmd *exec.Cmd) error {
			var err error
			out, err = cmd.CombinedOutput()
			return err
		},
	}

	err = setZoektConfig(context.Background(), filepath.Join(dir, "repo"), &indexArgs{}, c)
	require.NoError(t, err, string(out))
}
