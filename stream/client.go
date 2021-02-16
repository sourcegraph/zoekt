package stream

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"net/http"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
)

type Client struct {
	// HTTP address of zoekt-webserver. Will query against Address + "/stream".
	Address string

	// HTTPClient when set is used instead of http.DefaultClient
	HTTPClient *http.Client
}

type Streamer interface {
	Send(*zoekt.SearchResult)
}

// StreamSearch returns search results as stream via streamer.
func (c *Client) StreamSearch(ctx context.Context, q query.Q, opts *zoekt.SearchOptions, streamer Streamer) error {
	registerGob()

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
	req, err := http.NewRequestWithContext(ctx, "POST", c.Address+DefaultSSEPath, buf)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/x-gob-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Transfer-Encoding", "chunked")

	resp, err := c.HTTPClient.Do(req)
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
				return fmt.Errorf("event of type %s could not be converted to *zoekt.SearchResult", eventMatches)
			}
		case eventError:
			if errString, ok := reply.Data.(string); ok {
				return fmt.Errorf(errString)
			} else {
				return fmt.Errorf("data for event of type %s could not be converted to string", eventError)
			}
		case eventDone:
			return nil
		default:
			return fmt.Errorf("unknown event type: %s", reply.Event)
		}
	}
}
