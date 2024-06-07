package server

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
	"github.com/sourcegraph/zoekt/grpc/protos/zoekt/webserver/v1"
	"go.uber.org/atomic"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/sourcegraph/zoekt"
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

	request := v1.StreamSearchRequest{
		Request: &v1.SearchRequest{Query: query.QToProto(mock.WantSearch)},
	}

	cs, err := client.StreamSearch(context.Background(), &request)
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

func TestFuzzGRPCChunkSender(t *testing.T) {
	validateResult := func(input zoekt.SearchResult) error {
		clientStream, serverStream := newPairedSearchStream(t)
		sender := gRPCChunkSender(serverStream)

		sender.Send(&input)

		allResponses := readAllStream(t, clientStream)
		if len(allResponses) == 0 {
			return errors.New("received no responses")
		}

		expectedResult := input.ToProto()

		for i, receivedResponse := range allResponses {
			// First, check some invariants about the progress field

			if i == len(allResponses)-1 {
				// The last response should have the same progress as the original search result
				if diff := cmp.Diff(expectedResult.GetProgress(), receivedResponse.GetProgress(), protocmp.Transform()); diff != "" {
					return fmt.Errorf("unexpected difference in progress (-want +got):\n%s", diff)
				}
			} else {
				// All other responses should ensure that the progress' priority is less than the max-pending priority, to
				// ensure that the client consumes the entire set of chunks

				if receivedResponse.GetProgress().GetPriority() > receivedResponse.GetProgress().GetMaxPendingPriority() {
					return fmt.Errorf(
						"received response %d (%s) has priority %.6f, which is greater than the max pending priority %.6f",
						i, receivedResponse,
						receivedResponse.GetProgress().GetPriority(), receivedResponse.GetProgress().GetMaxPendingPriority(),
					)
				}
			}

			// Safety, ensure that all other fields are echoed back correctly if the schema ever changes
			opts := []cmp.Option{
				protocmp.Transform(),
				protocmp.IgnoreFields(&v1.SearchResponse{},
					"progress", // progress is tested above
					"stats",    // aggregated stats are tested below
					"files",    // files are tested separately
				),
			}

			if diff := cmp.Diff(expectedResult, receivedResponse, opts...); diff != "" {
				return fmt.Errorf("unexpected difference in response fields (-want +got):\n%s", diff)
			}
		}

		receivedStats := &zoekt.Stats{}

		var receivedFileMatches []*v1.FileMatch
		for _, r := range allResponses {
			receivedStats.Add(zoekt.StatsFromProto(r.GetStats()))
			receivedFileMatches = append(receivedFileMatches, r.GetFiles()...)
		}

		// Check to make sure that we get one set of stats back
		if diff := cmp.Diff(expectedResult.GetStats(), receivedStats.ToProto(),
			protocmp.Transform(),
			protocmp.IgnoreFields(&v1.Stats{},
				"duration", // for whatever the duration field isn't updated when zoekt.Stats.Add is called
			),
		); diff != "" {
			return fmt.Errorf("unexpected difference in stats (-want +got):\n%s", diff)
		}

		// Check to make sure that we get the same set of file matches back
		if diff := cmp.Diff(expectedResult.GetFiles(), receivedFileMatches,
			protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
			return fmt.Errorf("unexpected difference in file matches (-want +got):\n%s", diff)
		}

		return nil
	}

	var lastErr error
	if err := quick.Check(func(r zoekt.SearchResult) bool {
		lastErr = validateResult(r)

		return lastErr == nil
	}, nil); err != nil {
		t.Fatal(lastErr.Error())
	}
}

// newPairedSearchStream returns a pair of client and server search streams that are connected to each other.
func newPairedSearchStream(t *testing.T) (v1.WebserverService_StreamSearchClient, v1.WebserverService_StreamSearchServer) {
	client := &mockSearchStreamClient{t: t}
	server := &mockSearchStreamServer{t: t, pairedClient: client}

	return client, server
}

type mockSearchStreamClient struct {
	t *testing.T

	storedResponses []*v1.StreamSearchResponse
	index           int

	startedReading atomic.Bool

	grpc.ClientStream
}

func (m *mockSearchStreamClient) Recv() (*v1.StreamSearchResponse, error) {
	m.startedReading.Store(true)

	if m.index >= len(m.storedResponses) {
		return nil, io.EOF
	}

	r := m.storedResponses[m.index]
	m.index++
	return r, nil
}

func (m *mockSearchStreamClient) storeResponse(r *v1.StreamSearchResponse) {
	if m.startedReading.Load() {
		m.t.Fatalf("cannot store additional responses after starting to read from stream")
	}

	m.storedResponses = append(m.storedResponses, r)
}

type mockSearchStreamServer struct {
	t *testing.T

	pairedClient *mockSearchStreamClient

	grpc.ServerStream
}

func (m *mockSearchStreamServer) Send(r *v1.StreamSearchResponse) error {
	m.pairedClient.storeResponse(r)
	return nil
}

var (
	_ v1.WebserverService_StreamSearchServer = &mockSearchStreamServer{}
	_ v1.WebserverService_StreamSearchClient = &mockSearchStreamClient{}
)

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

		got = append(got, r.GetResponseChunk())
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
