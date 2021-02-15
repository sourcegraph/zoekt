package rpc_test

import (
	"context"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/rpc"
	"github.com/google/zoekt/rpc/mockSearcher"
)

func TestClientServer(t *testing.T) {
	mock := &mockSearcher.MockSearcher{
		WantSearch: query.NewAnd(mustParse("hello world|universe"), query.NewRepoSet("foo/bar", "baz/bam")),
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
						Name: "foo/bar",
					},
				},
			},
		},
	}

	ts := httptest.NewServer(rpc.Server(mock))
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := rpc.Client(u.Host)

	r, err := client.Search(context.Background(), mock.WantSearch, &zoekt.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r, mock.SearchResult) {
		t.Fatalf("got %+v, want %+v", r, mock.SearchResult)
	}

	l, err := client.List(context.Background(), mock.WantList)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(l, mock.RepoList) {
		t.Fatalf("got %+v, want %+v", l, mock.RepoList)
	}
}

func mustParse(s string) query.Q {
	q, err := query.Parse(s)
	if err != nil {
		panic(err)
	}
	return q
}
