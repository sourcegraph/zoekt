package sse

import (
	"bytes"
	"encoding/gob"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/rpc"
)

// TODO: register handler with zoekt

const (
	DefaultSSEPath = "/stream"

	FieldEvent   = "event: "
	FieldData    = "data: "
	FieldComment = ":"
)

func init() {
	rpc.RegisterGob()
}

type searchArgs struct {
	Q    query.Q
	Opts *zoekt.SearchOptions
}

type searchReply struct {
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

	// Kick-off search (in batch-mode for now).
	searchResults, err := h.Searcher.Search(ctx, args.Q, args.Opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Send event.
	err = eventWriter.Event("results", &searchReply{searchResults})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

type eventStreamWriterStat struct {
	Event    string
	Bytes    int
	Duration time.Duration
	Error    error
}

type eventStreamWriter struct {
	w     io.Writer
	flush func()

	StatHook func(eventStreamWriterStat)
}

func newEventStreamWriter(w http.ResponseWriter) (*eventStreamWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("http flushing not supported")
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	// TODO: do we need this for frontend <---> zoekt?
	// This informs nginx to not buffer. With buffering search responses will
	// be delayed until buffers get full, leading to worst case latency of the
	// full time a search takes to complete.
	w.Header().Set("X-Accel-Buffering", "no")

	return &eventStreamWriter{
		w:     w,
		flush: flusher.Flush,
	}, nil
}

func (e *eventStreamWriter) Event(event string, data interface{}) (err error) {
	// write is a helper to avoid error handling. Additionally it counts the
	// number of bytes written.
	start := time.Now()
	nbytes := 0
	write := func(b []byte) {
		if err != nil {
			return
		}
		var n int
		n, err = e.w.Write(b)
		nbytes += n
	}

	defer func() {
		if hook := e.StatHook; hook != nil {
			hook(eventStreamWriterStat{
				Event:    event,
				Bytes:    nbytes,
				Duration: time.Since(start),
				Error:    err,
			})
		}
	}()

	encoded := new(bytes.Buffer)
	enc := gob.NewEncoder(encoded)
	err = enc.Encode(data)
	if err != nil {
		return err
	}
	if event != "" {
		// event: $event\n
		write([]byte(FieldEvent))
		write([]byte(event))
		write([]byte("\n"))
	}

	// data: <multiline gob-encoded-data>\n\n
	for {
		// TODO: Why does the gob encoder not add a newline in the end?
		line, lErr := encoded.ReadBytes('\n')
		if err != nil {
			if lErr == io.EOF {
				line = append(line, '\n')
			} else {
				return err
			}
		}
		write([]byte(FieldData))
		write(line)
		if lErr == io.EOF {
			break
		}
	}
	if err != nil {
		return err
	}
	write([]byte("\n"))

	e.flush()
	return nil
}
