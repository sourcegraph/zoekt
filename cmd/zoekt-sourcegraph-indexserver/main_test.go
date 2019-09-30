package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGetIndexOptions(t *testing.T) {
	var response []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(response)
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string][]string{
		`{"Symbols": true, "LargeFiles": ["foo","bar"]}`: []string{"-require_ctags", "-large_file", "foo", "-large_file", "bar"},

		`{"Symbols": false, "LargeFiles": ["foo","bar"]}`: []string{"-disable_ctags", "-large_file", "foo", "-large_file", "bar"},

		`{}`: []string{"-disable_ctags"},

		`{"Symbols": true}`: []string{"-require_ctags"},
	}

	for r, want := range cases {
		response = []byte(r)

		opts, err := getIndexOptions(u, server.Client())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if got := opts.toArgs(); !reflect.DeepEqual(got, want) {
			t.Errorf("got unexpected arguments from options\ngot: %v\nwant: %v\n", got, want)
		}
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

		_, err = w.Write([]byte(`[{"uri":"foo"}, {"uri":"bar"}, {"uri":"baz"}]`))
		if err != nil {
			t.Fatal(err)
		}
	}))
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	gotRepos, err := listRepos("test-indexed-search-1", u)
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"foo", "bar", "baz"}; !reflect.DeepEqual(gotRepos, want) {
		t.Fatalf("unexpected repos. got %v, want %v", gotRepos, want)
	}
	if want := `{"Hostname":"test-indexed-search-1","Enabled":true,"Index":true}`; gotBody != want {
		t.Fatalf("unexpected request body. got %q, want %q", gotBody, want)
	}
	if want := "/.internal/repos/list"; gotURL.Path != want {
		t.Fatalf("unexpected request path. got %q, want %q", gotURL.Path, want)
	}
}

func TestPing(t *testing.T) {
	var response []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.internal/ping" {
			http.Error(w, "not found", http.StatusNotFound)
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
	if got, want := fmt.Sprintf("%v", err), "bad HTTP response body"; !strings.Contains(got, want) {
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
		waitForFrontend(root)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("waitForFrontend blocking")
	}
}
