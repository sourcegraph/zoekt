// Package stream provides a client and a server to consume search results as
// stream.
package stream

import (
	"encoding/gob"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/rpc"
)

// DefaultSSEPath is the path used by zoekt-webserver.
const DefaultSSEPath = "/stream"

const fileLimit = 100

type eventType int

const (
	eventMatches eventType = iota
	eventError
	eventDone
)

func (e eventType) string() string {
	return []string{"eventMatches", "eventError", "eventDone"}[e]
}

// Server returns an http.Handler which is the server side of StreamSearch.
func Server(searcher zoekt.Streamer) http.Handler {
	registerGob()
	return &handler{Searcher: searcher}
}

type searchArgs struct {
	Q    query.Q
	Opts *zoekt.SearchOptions
}

type searchReply struct {
	Event eventType
	Data  interface{}
}

type handler struct {
	Searcher zoekt.Streamer
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Decode payload.
	args := new(searchArgs)
	err := gob.NewDecoder(r.Body).Decode(args)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	eventWriter, err := newEventStreamWriter(w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Always send a done event in the end.
	defer func() {
		err = eventWriter.event(eventDone, nil)
		if err != nil {
			_ = eventWriter.event(eventError, err)
		}
	}()

	// mu protects the aggregator `a` and concurrent writes to the stream.
	mu := sync.Mutex{}
	a := &zoekt.SearchResult{}
	first := true
	update := func(event *zoekt.SearchResult) {
		a.Stats.Add(event.Stats)
		a.Files = append(a.Files, event.Files...)
	}
	send := func(zsr *zoekt.SearchResult) {
		err := eventWriter.event(eventMatches, zsr)
		if err != nil {
			_ = eventWriter.event(eventError, err)
			return
		}
	}
	flush := func() {
		if len(a.Files) == 0 && a.Stats.Zero() {
			return
		}
		send(a)
		// Reset aggregator.
		a.Files = a.Files[:0]
		a.Stats = zoekt.Stats{}
	}

	flushTicker := time.NewTicker(50 * time.Millisecond)
	defer flushTicker.Stop()

	err = h.Searcher.StreamSearch(ctx, args.Q, args.Opts, SenderFunc(func(event *zoekt.SearchResult) {
		mu.Lock()
		defer mu.Unlock()

		update(event)

		// We stream back the first non-empty result we get. After that, we send events
		// in regular time intervals or whenever we have reached the file limit.
		if first {
			if len(a.Files) > 0 {
				first = false
				flush()
			}
			return
		}

		select {
		case <-flushTicker.C:
			flush()
			return
		default:
		}

		if len(a.Files) > fileLimit {
			flush()
		}
	}))
	if err != nil {
		_ = eventWriter.event(eventError, err)
		return
	}

	flush()
}

type eventStreamWriter struct {
	enc   *gob.Encoder
	flush func()
}

func newEventStreamWriter(w http.ResponseWriter) (*eventStreamWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("http flushing not supported")
	}

	w.Header().Set("Content-Type", "application/x-gob-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	// This informs nginx to not buffer. With buffering search responses will
	// be delayed until buffers get full, leading to worst case latency of the
	// full time a search takes to complete.
	w.Header().Set("X-Accel-Buffering", "no")

	return &eventStreamWriter{
		enc:   gob.NewEncoder(w),
		flush: flusher.Flush,
	}, nil
}

func (e *eventStreamWriter) event(event eventType, data interface{}) error {
	// Because gob does not support serializing errors, we send error.Error() and
	// recreate the error on the client-side.
	if event == eventError {
		if err, isError := data.(error); isError {
			data = err.Error()
		}
	}
	err := e.enc.Encode(searchReply{Event: event, Data: data})
	if err != nil {
		return err
	}
	e.flush()
	return nil
}

var once sync.Once

func registerGob() {
	once.Do(func() {
		gob.Register(&zoekt.SearchResult{})
	})
	rpc.RegisterGob()
}
