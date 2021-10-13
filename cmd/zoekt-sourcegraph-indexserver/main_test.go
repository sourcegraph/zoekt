package main

import (
	"context"
	"flag"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/go-retryablehttp"
)

func TestServer_defaultArgs(t *testing.T) {
	root, err := url.Parse("http://api.test")
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{
		Sourcegraph: &sourcegraphClient{
			Root: root,
		},
		IndexDir: "/testdata/index",
		CPUCount: 6,
	}
	want := &indexArgs{
		IndexOptions: IndexOptions{
			Name: "testName",
		},
		CloneURL:          "http://api.test/.internal/git/testName",
		IndexDir:          "/testdata/index",
		Parallelism:       6,
		Incremental:       true,
		FileLimit:         1 << 20,
		DownloadLimitMBPS: "1000",
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

		b, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		gotBody = string(b)

		_, err = w.Write([]byte(`{"RepoNames": ["foo", "bar", "baz"]}`))
		if err != nil {
			t.Fatal(err)
		}
	}))
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	s := &sourcegraphClient{
		Root:     u,
		Hostname: "test-indexed-search-1",
		Client:   retryablehttp.NewClient(),
	}

	gotRepos, err := s.ListRepoNames(context.Background(), []string{"foo", "bam"})
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"foo", "bar", "baz"}; !cmp.Equal(gotRepos, want) {
		t.Errorf("repos mismatch (-want +got):\n%s", cmp.Diff(want, gotRepos))
	}
	if want := `{"Hostname":"test-indexed-search-1","Indexed":["foo","bam"]}`; gotBody != want {
		t.Errorf("body mismatch (-want +got):\n%s", cmp.Diff(want, gotBody))
	}
	if want := "/.internal/repos/index"; gotURL.Path != want {
		t.Errorf("request path mismatch (-want +got):\n%s", cmp.Diff(want, gotURL.Path))
	}
}

func TestMain(m *testing.M) {
	flag.Parse()
	if !testing.Verbose() {
		log.SetOutput(ioutil.Discard)
	}
	os.Exit(m.Run())
}
