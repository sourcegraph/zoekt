package grpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"testing"
	"testing/quick"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

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
				{FileName: "foo.go"},
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
	defer gs.Stop()

	v1.RegisterWebserverServiceServer(gs, NewServer(adapter{mock}))
	ts := httptest.NewServer(h2c.NewHandler(gs, &http2.Server{}))
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	cc, err := grpc.Dial(u.Host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()

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

	cs, err := client.StreamSearch(context.Background(), &v1.SearchRequest{Query: query.QToProto(mock.WantSearch)})
	if err != nil {
		t.Fatal(err)
	}

	allResponses := readAllStream(t, cs)

	for _, receivedResponse := range allResponses { // check to make sure that all responses have the same fields set
		opts := []cmp.Option{
			protocmp.Transform(),
			cmpopts.IgnoreFields(v1.SearchResponse{}, "Files"), // Files is tested separately
		}

		if diff := cmp.Diff(receivedResponse, mock.SearchResult.ToProto(), opts...); diff != "" {
			t.Fatalf("unexpected difference in response fields (-want +got):\n%s", diff)
		}
	}

	// check to make sure that we get the same set of file matches back

	var receivedFileMatches []*v1.FileMatch
	for _, r := range allResponses {
		receivedFileMatches = append(receivedFileMatches, r.GetFiles()...)
	}

	if diff := cmp.Diff(receivedFileMatches, mock.SearchResult.ToProto().GetFiles(), protocmp.Transform()); diff != "" {
		t.Fatalf("unexpected difference in file matches (-want +got):\n%s", diff)
	}
}

func TestFuzzStreamSearch(t *testing.T) {
	mock := &mockSearcher.MockSearcher{
		WantSearch:   query.NewAnd(mustParse("hello world|universe")),
		SearchResult: &zoekt.SearchResult{},
	}
	gs := grpc.NewServer()
	defer gs.Stop()

	v1.RegisterWebserverServiceServer(gs, NewServer(adapter{mock}))
	ts := httptest.NewServer(h2c.NewHandler(gs, &http2.Server{}))
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	cc, err := grpc.Dial(u.Host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}

	defer cc.Close()

	client := v1.NewWebserverServiceClient(cc)

	errString := "error"
	f := func(results zoekt.SearchResult) bool {
		mock.SearchResult = &results

		cs, err := client.StreamSearch(context.Background(), &v1.SearchRequest{Query: query.QToProto(mock.WantSearch)})
		if err != nil {
			t.Fatal(err)
		}

		allResponses := readAllStream(t, cs)
		for _, receivedResponse := range allResponses { // check to make sure that all responses have the same fields set
			opts := []cmp.Option{
				protocmp.Transform(),
				cmpopts.IgnoreFields(v1.SearchResponse{}, "Files"), // Files is tested separately
			}

			if diff := cmp.Diff(receivedResponse, mock.SearchResult.ToProto(), opts...); diff != "" {
				errString = fmt.Sprintf("unexpected difference in response fields (-want +got):\n%s", diff)
				return false
			}
		}

		// check to make sure that we get the same set of file matches back

		var receivedFileMatches []*v1.FileMatch
		for _, r := range allResponses {
			receivedFileMatches = append(receivedFileMatches, r.GetFiles()...)
		}

		if diff := cmp.Diff(receivedFileMatches, mock.SearchResult.ToProto().GetFiles(), protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
			errString = fmt.Sprintf("unexpected difference in file matches (-want +got):\n%s", diff)
			return false
		}

		return true

	}

	if err := quick.Check(f, nil); err != nil {
		t.Fatal(errString)
	}
}

func readAllStream(t *testing.T, cs v1.WebserverService_StreamSearchClient) []*v1.SearchResponse {
	var got []*v1.SearchResponse
	for { // collect all responses from the stream
		r, err := cs.Recv()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatal(err)
		}

		got = append(got, r)
	}

	return got
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
