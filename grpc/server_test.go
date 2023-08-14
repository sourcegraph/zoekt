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

	errString := "no error"
	f := func(results zoekt.SearchResult) bool {

		mock.SearchResult = &results
		expectedResult := mock.SearchResult.ToProto()

		cs, err := client.StreamSearch(context.Background(), &v1.SearchRequest{Query: query.QToProto(mock.WantSearch)})
		if err != nil {
			t.Fatal(err)
		}

		allResponses := readAllStream(t, cs)
		if len(allResponses) == 0 {
			errString = "received no responses"
			return false
		}

		for i, receivedResponse := range allResponses {

			// First, check some invariants about the progress field

			if i == len(allResponses)-1 {
				// The last response should have the same progress as the original search result

				if diff := cmp.Diff(expectedResult.GetProgress(), receivedResponse.GetProgress(), protocmp.Transform()); diff != "" {
					errString = fmt.Sprintf("unexpected difference in progress (-want +got):\n%s", diff)
					return false
				}
			} else {
				// All other responses should ensure that the progress' priority is less than the max-pending priority, to
				// ensure that the client consumes the entire set of chunks

				if receivedResponse.GetProgress().GetPriority() > receivedResponse.GetProgress().GetMaxPendingPriority() {
					errString = fmt.Sprintf("received response %d has priority %.6f, which is greater than the max pending priority %.6f", i, receivedResponse.GetProgress().GetPriority(), receivedResponse.GetProgress().GetMaxPendingPriority())
					return false
				}
			}

			// Safety, ensure that all other fields are echoed back correctly if the schema ever changes
			opts := []cmp.Option{
				protocmp.Transform(),
				protocmp.IgnoreFields(&v1.SearchResponse{},
					"progress", // progress is tested above
					"stats",    // aggregated stats are tested below
					"files",    // files are tested separately

					"repo_urls",      // We no longer send repo_urls to the client
					"line_fragments", // We no longer send line_fragments to the client
				),
			}

			if diff := cmp.Diff(expectedResult, receivedResponse, opts...); diff != "" {
				errString = fmt.Sprintf("unexpected difference in response fields (-want +got):\n%s", diff)
				return false
			}
		}

		allStats := &zoekt.Stats{}
		var receivedFileMatches []*v1.FileMatch
		for _, r := range allResponses {
			allStats.Add(zoekt.StatsFromProto(r.GetStats()))
			receivedFileMatches = append(receivedFileMatches, r.GetFiles()...)
		}

		// Check to make sure that we get one set of stats back
		if diff := cmp.Diff(expectedResult.GetStats(), allStats.ToProto(),
			protocmp.Transform(),
			protocmp.IgnoreFields(&v1.Stats{},
				"duration", // for whatever the duration field isn't updated when zoekt.Stats.Add is called
			),
		); diff != "" {
			errString = fmt.Sprintf("unexpected difference in stats (-want +got):\n%s", diff)
			return false
		}

		// Check to make sure that we get the same set of file matches back
		if diff := cmp.Diff(expectedResult.GetFiles(), receivedFileMatches,
			protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
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
