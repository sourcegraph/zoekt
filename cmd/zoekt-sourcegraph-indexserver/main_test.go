package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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
		Name:              "testName",
		CloneURL:          "http://api.test/.internal/git/testName",
		IndexDir:          "/testdata/index",
		Parallelism:       6,
		Incremental:       true,
		FileLimit:         1 << 20,
		DownloadLimitMBPS: "1000",
	}
	got := s.indexArgs("testName", IndexOptions{})
	if !cmp.Equal(got, want) {
		t.Errorf("mismatch (-want +got):\n%s", cmp.Diff(want, got))
	}
}

func TestListRepos(t *testing.T) {
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

	gotRepos, err := s.ListRepos(context.Background(), []string{"foo", "bam"})
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

func TestPing(t *testing.T) {
	var response []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.internal/ping" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("service") != "gitserver" {
			http.Error(w, "expected service gitserver in request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write(response)
	}))
	defer server.Close()

	root, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	// Ping fails
	response = []byte("hello")
	err = ping(root)
	if got, want := fmt.Sprintf("%v", err), "did not receive pong"; !strings.Contains(got, want) {
		t.Errorf("wanted ping to fail,\ngot:  %q\nwant: %q", got, want)
	}

	response = []byte("pong")
	err = ping(root)
	if err != nil {
		t.Errorf("wanted ping to succeed, got: %v", err)
	}

	// We expect waitForFrontend to just work now
	done := make(chan struct{})
	go func() {
		(&sourcegraphClient{Root: root}).WaitForFrontend()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("waitForFrontend blocking")
	}
}

func TestCodeHostFromName(t *testing.T) {
	cases := map[string]string{
		// no codehost
		"foo":     "unknown",
		"foo/bar": "unknown",
		"/foo":    "unknown",
		"/":       "unknown",
		"":        "unknown",

		"foo.com":     "foo.com",
		"foo.com/bar": "foo.com",
	}

	for repoName, want := range cases {
		got := codeHostFromName(repoName)
		if got != want {
			t.Errorf("codeHostFromName(%q): got %q want %q", repoName, got, want)
		}
	}
}
