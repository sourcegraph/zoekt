package stream

import (
	"context"
	"net/http/httptest"
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
	defer close(c)

	// Start consumer.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for res := range c {
			if res.Files == nil {
				continue
			}
			if res.Files[0].FileName != "bin.go" {
				t.Fatalf("got %s, wanted %s", res.Files[0].FileName, "bin.go")
			}
			return
		}
	}()

	err := cl.StreamSearch(context.Background(), q, nil, StreamerChan(c))
	if err != nil {
		t.Fatal(err)
	}
	<-done
}

func mustParse(s string) query.Q {
	q, err := query.Parse(s)
	if err != nil {
		panic(err)
	}
	return q
}
