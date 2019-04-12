package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

func TestGetIndexOptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"LargeFiles": ["foo","bar"]}`))
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	opts, err := getIndexOptions(u, server.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(opts.LargeFiles) == 0 {
		t.Error("expected non-empty result from large files list")
	}

	want := []string{"-large_file", "foo", "-large_file", "bar"}
	if got := opts.toArgs(); !reflect.DeepEqual(got, want) {
		t.Errorf("got unexpected arguments from options\ngot: %v\nwant: %v\n", got, want)
	}
}
