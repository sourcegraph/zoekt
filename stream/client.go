package stream

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
)

// NewClient returns a client which implements StreamSearch. If httpClient is
// nil, http.DefaultClient is used.
func NewClient(address string, httpClient *http.Client) *client {
	registerGob()
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &client{
		address:    address,
		httpClient: httpClient,
	}
}

type client struct {
	// HTTP address of zoekt-webserver. Will query against address + "/stream".
	address string

	// httpClient when set is used instead of http.DefaultClient
	httpClient *http.Client
}

// Streamer is the interface that wraps the basic Send method.
type Streamer interface {
	Send(*zoekt.SearchResult)
}

type Searcher interface {
	StreamSearch(ctx context.Context, q query.Q, opts *zoekt.SearchOptions, sender Streamer) (err error)
}

// StreamerFunc is an adapter to allow the use of ordinary functions as Streamer.
// If f is a function with the appropriate signature, StreamerFunc(f) is a Streamer
// that calls f.
type StreamerFunc func(result *zoekt.SearchResult)

func (f StreamerFunc) Send(result *zoekt.SearchResult) {
	f(result)
}

// StreamSearch returns search results as stream by calling streamer.Send(event)
// for each event returned by the server.
//
// Error events returned by the server are returned as error. Context errors are
// recreated and returned on a best-efforts basis.
func (c *client) StreamSearch(ctx context.Context, q query.Q, opts *zoekt.SearchOptions, streamer Streamer) error {
	// Encode query and opts.
	buf := new(bytes.Buffer)
	args := &searchArgs{
		q, opts,
	}
	enc := gob.NewEncoder(buf)
	err := enc.Encode(args)
	if err != nil {
		return fmt.Errorf("error during encoding: %w", err)
	}

	// Send request.
	req, err := http.NewRequestWithContext(ctx, "POST", c.address+DefaultSSEPath, buf)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/x-gob-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Transfer-Encoding", "chunked")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	dec := gob.NewDecoder(resp.Body)
	for {
		reply := &searchReply{}
		err := dec.Decode(reply)
		if err != nil {
			return fmt.Errorf("error during decoding: %w", err)
		}
		switch reply.Event {
		case eventMatches:
			if res, ok := reply.Data.(*zoekt.SearchResult); ok {
				streamer.Send(res)
			} else {
				return fmt.Errorf("event of type %s could not be converted to *zoekt.SearchResult", eventMatches.string())
			}
		case eventError:
			if errString, ok := reply.Data.(string); ok {
				if strings.Contains(errString, "context canceled") {
					return context.Canceled
				} else if strings.Contains(errString, "context deadline exceeded") {
					return context.DeadlineExceeded
				} else {
					return fmt.Errorf(errString)
				}
			} else {
				return fmt.Errorf("data for event of type %s could not be converted to string", eventError.string())
			}
		case eventDone:
			return nil
		default:
			return fmt.Errorf("unknown event type")
		}
	}
}
