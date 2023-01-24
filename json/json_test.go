package json_test

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/internal/mockSearcher"
	zjson "github.com/sourcegraph/zoekt/json"
	"github.com/sourcegraph/zoekt/query"
)

func TestClientServer(t *testing.T) {
	searchQuery := "\"hello world|universe\""
	mock := &mockSearcher.MockSearcher{
		WantSearch: mustParse(searchQuery),
		SearchResult: &zoekt.SearchResult{
			Files: []zoekt.FileMatch{
				{FileName: "bin.go"},
			},
		},

		WantList: &query.Const{Value: true},
		RepoList: &zoekt.RepoList{
			Repos: []*zoekt.RepoListEntry{
				{
					Repository: zoekt.Repository{
						ID:   2,
						Name: "foo/bar",
					},
				},
			},
		},
	}

	ts := httptest.NewServer(zjson.JSONServer(mock))
	defer ts.Close()

	searchBody, err := json.Marshal(struct{ Q string }{Q: searchQuery})
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.Post(ts.URL+"/search", "application/json", bytes.NewBuffer(searchBody))
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode != 200 {
		body, _ := io.ReadAll(r.Body)
		t.Fatalf("Got status code %d, err %s", r.StatusCode, string(body))
	}

	var searchResult struct{ Result *zoekt.SearchResult }
	err = json.NewDecoder(r.Body).Decode(&searchResult)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(searchResult.Result, mock.SearchResult) {
		t.Fatalf("\na %+v\nb %+v", searchResult.Result, mock.SearchResult)
	}

	listBody, err := json.Marshal(struct{ Q string }{})
	if err != nil {
		t.Fatal(err)
	}
	r, err = http.Post(ts.URL+"/list", "application/json", bytes.NewBuffer((listBody)))
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode != 200 {
		body, _ := io.ReadAll(r.Body)
		t.Fatalf("Got status code %d, err %s", r.StatusCode, string(body))
	}

	var listResult struct{ List *zoekt.RepoList }
	err = json.NewDecoder(r.Body).Decode(&listResult)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(listResult.List, mock.RepoList) {
		t.Fatalf("got %+v, want %+v", listResult, mock.RepoList)
	}
}

func TestProgressNotEncodedInSearch(t *testing.T) {
	searchQuery := "hello"
	mock := &mockSearcher.MockSearcher{
		WantSearch: mustParse(searchQuery),
		SearchResult: &zoekt.SearchResult{
			// Validate that Progress is ignored as we cannot encode -Inf
			Progress: zoekt.Progress{
				Priority:           math.Inf(-1),
				MaxPendingPriority: math.Inf(-1),
			},
			Files: []zoekt.FileMatch{},
		},
	}

	ts := httptest.NewServer(zjson.JSONServer(mock))
	defer ts.Close()

	searchBody, err := json.Marshal(struct{ Q string }{Q: searchQuery})
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.Post(ts.URL+"/search", "application/json", bytes.NewBuffer(searchBody))
	if err != nil {
		t.Fatal(err)
	}

	if r.StatusCode != 200 {
		body, _ := io.ReadAll(r.Body)
		t.Fatalf("Got status code %d, err %s", r.StatusCode, string(body))
	}
}

func mustParse(s string) query.Q {
	q, err := query.Parse(s)
	if err != nil {
		panic(err)
	}
	return q
}
