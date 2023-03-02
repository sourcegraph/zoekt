package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"

	sglog "github.com/sourcegraph/log"
	"github.com/sourcegraph/log/logtest"
	proto "github.com/sourcegraph/sourcegraph/protos/frontend/indexedsearch/v1"
	"google.golang.org/grpc"

	"github.com/google/go-cmp/cmp"

	"github.com/sourcegraph/zoekt"
)

func TestServer_defaultArgs(t *testing.T) {
	root, err := url.Parse("http://api.test")
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{
		Sourcegraph: newSourcegraphClient(root, "", WithBatchSize(0)),
		IndexDir:    "/testdata/index",
		CPUCount:    6,
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

func TestListRepoIDs(t *testing.T) {
	t.Run("gRPC", func(t *testing.T) {

		grpcClient := &mockGRPCClient{}

		clientOptions := []SourcegraphClientOption{
			WithShouldUseGRPCFunc(func() bool { return true }),
			WithGRPCClient(grpcClient),
			WithBatchSize(0),
		}

		testURL := url.URL{Scheme: "http", Host: "does.not.matter"}
		testHostname := "test-hostname"
		s := newSourcegraphClient(&testURL, testHostname, clientOptions...)

		listCalled := false
		grpcClient.mockList = func(ctx context.Context, in *proto.ListRequest, opts ...grpc.CallOption) (*proto.ListResponse, error) {
			listCalled = true

			gotRepoIDs := in.GetIndexedIds()
			sort.Slice(gotRepoIDs, func(i, j int) bool {
				return gotRepoIDs[i] < gotRepoIDs[j]
			})

			wantRepoIDs := []int32{1, 3}
			sort.Slice(wantRepoIDs, func(i, j int) bool {
				return wantRepoIDs[i] < wantRepoIDs[j]
			})

			if diff := cmp.Diff(wantRepoIDs, gotRepoIDs); diff != "" {
				t.Errorf("indexed repoIDs mismatch (-want +got):\n%s", diff)
			}

			hostname := in.GetHostname()
			if diff := cmp.Diff(testHostname, hostname); diff != "" {
				t.Errorf("hostname mismatch (-want +got):\n%s", diff)
			}

			return &proto.ListResponse{RepoIds: []int32{1, 2, 3}}, nil
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
		sort.Slice(receivedRepoIDs, func(i, j int) bool {
			return receivedRepoIDs[i] < receivedRepoIDs[j]
		})

		expectedRepoIDs := []uint32{1, 2, 3}
		sort.Slice(expectedRepoIDs, func(i, j int) bool {
			return expectedRepoIDs[i] < expectedRepoIDs[j]
		})

		if diff := cmp.Diff(expectedRepoIDs, receivedRepoIDs); diff != "" {
			t.Errorf("mismatch in list of all repoIDs (-want +got):\n%s", diff)
		}
	})

	t.Run("REST", func(t *testing.T) {
		var gotBody string
		var gotURL *url.URL
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotURL = r.URL

			b, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			gotBody = string(b)

			_, err = w.Write([]byte(`{"RepoIDs": [1, 2, 3]}`))
			if err != nil {
				t.Fatal(err)
			}
		}))
		defer ts.Close()

		u, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatal(err)
		}

		s := newSourcegraphClient(u, "test-indexed-search-1", WithBatchSize(0))

		gotRepos, err := s.List(context.Background(), []uint32{1, 3})
		if err != nil {
			t.Fatal(err)
		}

		if want := []uint32{1, 2, 3}; !cmp.Equal(gotRepos.IDs, want) {
			t.Errorf("repos mismatch (-want +got):\n%s", cmp.Diff(want, gotRepos.IDs))
		}
		if want := `{"Hostname":"test-indexed-search-1","IndexedIDs":[1,3]}`; gotBody != want {
			t.Errorf("body mismatch (-want +got):\n%s", cmp.Diff(want, gotBody))
		}
		if want := "/.internal/repos/index"; gotURL.Path != want {
			t.Errorf("request path mismatch (-want +got):\n%s", cmp.Diff(want, gotURL.Path))
		}
	})
}

func TestListRepoIDs_Error_REST(t *testing.T) {
	// Note: There is no gRPC equivalent to this test because gRPC errors are
	// always returned as an error to the caller.

	msg := "deadbeaf deadbeaf"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		// This is how Sourcegraph returns error messages to the caller.
		http.Error(w, msg, http.StatusInternalServerError)
	}))
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	s := newSourcegraphClient(u, "test-indexed-search-1", WithBatchSize(0))
	s.restClient.RetryMax = 0

	_, err = s.List(context.Background(), []uint32{1, 3})

	if !strings.Contains(err.Error(), msg) {
		t.Fatalf("%s does not contain %s", err.Error(), msg)
	}
}

func TestMain(m *testing.M) {
	flag.Parse()
	level := sglog.LevelInfo
	if !testing.Verbose() {
		log.SetOutput(io.Discard)
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
