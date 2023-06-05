package grpc

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	"github.com/sourcegraph/zoekt"
	v1 "github.com/sourcegraph/zoekt/grpc/v1"
	"github.com/sourcegraph/zoekt/internal/mockSearcher"
	"github.com/sourcegraph/zoekt/query"
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

	gs := grpc.NewServer()
	v1.RegisterWebserverServiceServer(gs, NewServer(adapter{mock}))
	ts := httptest.NewServer(h2c.NewHandler(gs, &http2.Server{}))

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	cc, err := grpc.Dial(u.Host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	client := v1.NewWebserverServiceClient(cc)

	r, err := client.Search(context.Background(), &v1.SearchRequest{Query: query.QToProto(mock.WantSearch)})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(r, mock.SearchResult.ToProto()) {
		t.Fatalf("got %+v, want %+v", r, mock.SearchResult.ToProto())
	}

	l, err := client.List(context.Background(), &v1.ListRequest{Query: query.QToProto(mock.WantList)})
	if err != nil {
		t.Fatal(err)
	}

	if !proto.Equal(l, mock.RepoList.ToProto()) {
		t.Fatalf("got %+v, want %+v", l, mock.RepoList.ToProto())
	}
}

func mustParse(s string) query.Q {
	q, err := query.Parse(s)
	if err != nil {
		panic(err)
	}
	return q
}

type adapter struct {
	zoekt.Searcher
}

func (a adapter) StreamSearch(ctx context.Context, q query.Q, opts *zoekt.SearchOptions, sender zoekt.Sender) (err error) {
	sr, err := a.Searcher.Search(ctx, q, opts)
	if err != nil {
		return err
	}
	sender.Send(sr)
	return nil
}
