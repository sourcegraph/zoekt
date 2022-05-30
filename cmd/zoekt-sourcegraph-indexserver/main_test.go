package main

import (
	"context"
	"flag"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/google/zoekt"
)

func TestServer_defaultArgs(t *testing.T) {
	root, err := url.Parse("http://api.test")
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{
		Sourcegraph: newSourcegraphClient(root, "", 0),
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

	s := newSourcegraphClient(u, "test-indexed-search-1", 0)

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
}

func TestListRepoIDs_Error(t *testing.T) {
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

	s := newSourcegraphClient(u, "test-indexed-search-1", 0)
	s.Client.RetryMax = 0

	_, err = s.List(context.Background(), []uint32{1, 3})

	if !strings.Contains(err.Error(), msg) {
		t.Fatalf("%s does not contain %s", err.Error(), msg)
	}
}

func TestMain(m *testing.M) {
	flag.Parse()
	if !testing.Verbose() {
		log.SetOutput(io.Discard)
	}
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
