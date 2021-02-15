package stream

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/rpc/mockSearcher"
)

func TestStreamSearch(t *testing.T) {
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

	cl := NewClientAtAddress(s.URL)

	c := make(chan *zoekt.SearchResult)

	// Start consumer.
	wg := sync.WaitGroup{}
	wg.Add(1)
	seen := false
	go func() {
		defer wg.Done()
		for res := range c {
			if res.Files != nil {
				seen = true
				if res.Files[0].FileName != "bin.go" {
					t.Fatalf("got %s, wanted %s", res.Files[0].FileName, "bin.go")
				}
			}
		}
	}()

	err := cl.StreamSearch(context.Background(), q, nil, StreamerChan(c))
	close(c)
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("Did not receive event with res.Files != nil")
	}
	wg.Wait()
}

func mustParse(s string) query.Q {
	q, err := query.Parse(s)
	if err != nil {
		panic(err)
	}
	return q
}
