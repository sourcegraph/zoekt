// Package stream provides a client and a server to consume search results as
// stream.
package stream

import (
	"encoding/gob"
	"errors"
	"net/http"
	"sync"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/rpc"
)

// DefaultSSEPath is the path used by zoekt-webserver.
const DefaultSSEPath = "/stream"

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

	// mu protects aggStats and concurrent writes to the stream.
	mu := sync.Mutex{}
	var aggStats = zoekt.Stats{}
	send := func(zsr *zoekt.SearchResult) {
		err := eventWriter.event(eventMatches, zsr)
		if err != nil {
			_ = eventWriter.event(eventError, err)
			return
		}
	}

	err = h.Searcher.StreamSearch(ctx, args.Q, args.Opts, SenderFunc(func(event *zoekt.SearchResult) {
		mu.Lock()
		defer mu.Unlock()

		// We don't want to send events over the wire if they just contain stats and no
		// file matches. Hence, in case we didn't find any results, we will just
		// aggregate the stats.
		if len(event.Files) == 0 {
			aggStats.Add(event.Stats)
			return
		}

		// If we have aggregate stats, we merge them with the new event before sending
		// it, and reset aggStats afterwards.
		if !aggStats.Zero() {
			defer func() { aggStats = zoekt.Stats{} }() // reset stats
			event.Stats.Add(aggStats)
		}
		send(event)
		return
	}))

	// In case the last shard we searched didn't return matches, aggStats.Zero() = false.
	if !aggStats.Zero() {
		finalEvent := new(zoekt.SearchResult)
		finalEvent.Stats = aggStats
		send(finalEvent)
	}

	if err != nil {
		_ = eventWriter.event(eventError, err)
		return
	}
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
