package stream

import (
	"encoding/gob"
	"errors"
	"net/http"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/rpc"
)

const DefaultSSEPath = "/stream"

func Server(searcher zoekt.Searcher) http.Handler {
	rpc.RegisterGob()
	return &streamHandler{Searcher: searcher}
}

type searchArgs struct {
	Q    query.Q
	Opts *zoekt.SearchOptions
}

type searchReply struct {
	Event  string
	Result *zoekt.SearchResult
}

type streamHandler struct {
	Searcher zoekt.Searcher
}

func (h *streamHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		err = eventWriter.Event("done", nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}()

	// Kick-off search (in batch-mode for now).
	searchResults, err := h.Searcher.Search(ctx, args.Q, args.Opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// We simulate streaming by sending searchResults in chunks over the wire. Later
	// we want Searcher.Search to take a channel, buffer here and send chunks over
	// the network.
	var chunk *zoekt.SearchResult
	// We send stats first. We don't send RepoURLs or LineFragments because we don't
	// use them in Sourcegraph.
	chunk = &zoekt.SearchResult{
		Stats: searchResults.Stats,
	}
	// Send event.
	err = eventWriter.Event("matches", chunk)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	chunkSize := 100
	numFiles := len(searchResults.Files)
	for i := 0; i < numFiles; i = i + chunkSize {
		right := i + chunkSize
		if right >= numFiles {
			right = numFiles
		}
		chunk = &zoekt.SearchResult{
			Files: searchResults.Files[i:right],
		}
		// Send event.
		err = eventWriter.Event("matches", chunk)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
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

func (e *eventStreamWriter) Event(event string, data *zoekt.SearchResult) error {
	err := e.enc.Encode(searchReply{Event: event, Result: data})
	if err != nil {
		return err
	}
	e.flush()
	return nil
}
