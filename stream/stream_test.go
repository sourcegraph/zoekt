package stream

import (
	"bytes"
	"context"
	"encoding/gob"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
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

	cl := &Client{s.URL, http.DefaultClient}

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

	err := cl.StreamSearch(context.Background(), q, nil, streamerChan(c))
	if err != nil {
		t.Fatal(err)
	}
	<-done
}

func TestEventStreamWriter(t *testing.T) {
	registerGob()
	network := new(bytes.Buffer)
	enc := gob.NewEncoder(network)
	dec := gob.NewDecoder(network)

	esw := eventStreamWriter{
		enc:   enc,
		flush: func() {},
	}

	tests := []struct {
		event string
		data  interface{}
	}{
		{
			eventDone,
			nil,
		},
		{
			eventMatches,
			&zoekt.SearchResult{
				Files: []zoekt.FileMatch{
					{FileName: "bin.go"},
				},
			},
		},
		{
			eventError,
			"test error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			err := esw.event(tt.event, tt.data)
			if err != nil {
				t.Fatal(err)
			}
			reply := new(searchReply)
			err = dec.Decode(reply)
			if err != nil {
				t.Fatal(err)
			}
			if reply.Event != tt.event {
				t.Fatalf("got %s, want %s", reply.Event, tt.event)
			}
			if d := cmp.Diff(tt.data, reply.Data); d != "" {
				t.Fatalf("mismatch for event type %s (-want +got):\n%s", tt.event, d)
			}
		})
	}
}

func mustParse(s string) query.Q {
	q, err := query.Parse(s)
	if err != nil {
		panic(err)
	}
	return q
}

type streamerChan chan<- *zoekt.SearchResult

func (c streamerChan) Send(result *zoekt.SearchResult) {
	c <- result
}
