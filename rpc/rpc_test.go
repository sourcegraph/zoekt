package rpc_test

import (
	"context"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/xvandish/zoekt"
	"github.com/xvandish/zoekt/internal/mockSearcher"
	"github.com/xvandish/zoekt/query"
	"github.com/xvandish/zoekt/rpc"
)

func TestClientServer(t *testing.T) {
	mock := &mockSearcher.MockSearcher{
		WantSearch: query.NewAnd(mustParse("hello world|universe"), query.NewSingleBranchesRepos("HEAD", 1, 2)),
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

	ts := httptest.NewServer(rpc.Server(mock))
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := rpc.Client(u.Host)
	defer client.Close()

	var cached query.Q = &query.GobCache{
		Q: mock.WantSearch,
	}

	r, err := client.Search(context.Background(), cached, &zoekt.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r, mock.SearchResult) {
		t.Fatalf("got %+v, want %+v", r, mock.SearchResult)
	}

	l, err := client.List(context.Background(), mock.WantList, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(mock.RepoList, l, cmpopts.IgnoreUnexported(zoekt.Repository{})); d != "" {
		t.Fatalf("unexpected RepoList (-want, +got):\n%s", d)
	}

	// Test closing a client we never dial.
	noopClient := rpc.Client(u.Host)
	noopClient.Close()
}

func mustParse(s string) query.Q {
	q, err := query.Parse(s)
	if err != nil {
		panic(err)
	}
	return q
}
