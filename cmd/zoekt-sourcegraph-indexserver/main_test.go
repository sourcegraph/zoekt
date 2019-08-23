package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
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
