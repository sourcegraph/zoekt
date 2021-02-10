package sse

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/rpc/mockSearcher"
)

func TestSSE(t *testing.T) {
	q := query.NewAnd(mustParse("hello world|universe"), query.NewRepoSet("foo/bar", "baz/bam"))
	searcher := &mockSearcher.MockSearcher{
		WantSearch: q,
		SearchResult: &zoekt.SearchResult{
			Files: []zoekt.FileMatch{
				{FileName: "bin.go"},
			},
		},
	}

	h := &streamHandler{Searcher: searcher}

	s := httptest.NewServer(h)
	defer s.Close()

	c := NewClientAtAddress(s.URL)

	stream := make(chan *zoekt.SearchResult)
	defer close(stream)

	// Start consumer.
	wg := sync.WaitGroup{}
	wg.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	go func(ctx context.Context) {
		defer wg.Done()
		select {
		case res := <-stream:
			if res.Files[0].FileName != "bin.go" {
				t.Fatalf("got %s, wanted %s", res.Files[0].FileName, "bin.go")
			}
			return
		case <-ctx.Done():
			return
		}
	}(ctx)

	err := c.Search(q, nil, stream)
	if err != nil {
		t.Fatal(err)
	}

	cancel()
	wg.Wait()
}

func mustParse(s string) query.Q {
	q, err := query.Parse(s)
	if err != nil {
		panic(err)
	}
	return q
}
