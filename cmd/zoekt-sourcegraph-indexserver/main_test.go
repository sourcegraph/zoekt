package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestGetIndexOptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"LargeFiles": ["test"]}`))
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
	if opts.LargeFiles[0] != "test" {
		t.Error("decoded wrong results")
	}
}
