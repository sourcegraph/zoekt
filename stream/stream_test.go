package stream

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/google/zoekt"
	"github.com/google/zoekt/internal/mockSearcher"
	"github.com/google/zoekt/query"
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

	h := &handler{Searcher: adapter{searcher}}

	s := httptest.NewServer(h)
	defer s.Close()

	cl := NewClient(s.URL, nil)

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

func TestStreamSearchJustStats(t *testing.T) {
	wantStats := zoekt.Stats{
		Crashes: 1,
	}
	q := query.NewAnd(mustParse("hello world|universe"), query.NewRepoSet("foo/bar", "baz/bam"))
	searcher := &mockSearcher.MockSearcher{
		WantSearch: q,
		SearchResult: &zoekt.SearchResult{
			Files: []zoekt.FileMatch{},
			Stats: wantStats,
		},
	}

	h := &handler{Searcher: adapter{searcher}}

	s := httptest.NewServer(h)
	defer s.Close()

	cl := NewClient(s.URL, nil)

	c := make(chan *zoekt.SearchResult)

	// Start consumer.
	done := make(chan struct{})
	go func() {
		defer close(done)
		count := 0
		for res := range c {
			count += 1
			if count > 1 {
				t.Fatal("expected exactly 1 result, got at least 2")
			}
			if d := cmp.Diff(wantStats, res.Stats); d != "" {
				t.Fatalf("zoekt.Stats mismatch (-want +got): %s\n", d)
			}
		}
		if count != 1 {
			t.Fatal("expected exactly 1 result, got 0")
		}
	}()

	err := cl.StreamSearch(context.Background(), q, nil, streamerChan(c))
	if err != nil {
		t.Fatal(err)
	}
	close(c)
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
		event eventType
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
		t.Run(tt.event.string(), func(t *testing.T) {
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
				t.Fatalf("got %s, want %s", reply.Event.string(), tt.event.string())
			}
			if d := cmp.Diff(tt.data, reply.Data); d != "" {
				t.Fatalf("mismatch for event type %s (-want +got):\n%s", tt.event.string(), d)
			}
		})
	}
}

func TestContextError(t *testing.T) {
	var serverError error
	h := func(w http.ResponseWriter, r *http.Request) {
		esw, err := newEventStreamWriter(w)
		if err != nil {
			t.Fatal(err)
		}
		err = esw.event(eventError, serverError)
		if err != nil {
			t.Fatal(err)
		}
	}
	s := httptest.NewServer(http.HandlerFunc(h))

	cl := NewClient(s.URL, nil)

	c := streamerChan(make(chan *zoekt.SearchResult))
	ctx := context.Background()

	serverError = context.Canceled
	err := cl.StreamSearch(ctx, nil, nil, c)
	if err != context.Canceled {
		t.Fatalf("got %+v, want %s", err, context.Canceled)
	}

	serverError = context.DeadlineExceeded
	err = cl.StreamSearch(ctx, nil, nil, c)
	if err != context.DeadlineExceeded {
		t.Fatalf("got %+v, want %s", err, context.DeadlineExceeded)
	}

	serverError = fmt.Errorf("other error")
	err = cl.StreamSearch(ctx, nil, nil, c)
	if err == nil || err.Error() != serverError.Error() {
		t.Fatalf("got %s, want %s", err, serverError)
	}
}

// TestContextErrorClientSide tests if we return a proper context error if the
// context was canceled on the client-side.
func TestContextErrorClientSide(t *testing.T) {
	done := make(chan struct{})
	defer close(done)
	h := func(w http.ResponseWriter, r *http.Request) {
		esw, err := newEventStreamWriter(w)
		if err != nil {
			t.Fatal(err)
		}
		err = esw.event(eventMatches, zoekt.SearchResult{})
		if err != nil {
			t.Fatal(err)
		}
		// Block until the test is finished.
		done <- struct{}{}
	}
	s := httptest.NewServer(http.HandlerFunc(h))

	c := make(chan *zoekt.SearchResult)
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel the context once we received the first event.
	go func() {
		<-c
		cancel()
	}()

	cl := NewClient(s.URL, nil)
	err := cl.StreamSearch(ctx, nil, nil, streamerChan(c))
	if err != context.Canceled {
		t.Fatalf("got %+v, want %s", err, context.Canceled)
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
