package stream

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"net/http"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/rpc"
)

type client struct {
	address string
	path    string
	conn    *http.Client
}

func NewClientAtAddress(address string) *client {
	rpc.RegisterGob()
	return &client{address, DefaultSSEPath, &http.Client{
		Transport: &http.Transport{
			MaxIdleConns: 500,
		},
	}}
}

type Streamer interface {
	Send(*zoekt.SearchResult)
}

// Use StreamerChan to cast a receiving channel of search results to a Streamer.
type StreamerChan chan<- *zoekt.SearchResult

func (c StreamerChan) Send(result *zoekt.SearchResult) {
	c <- result
}

// StreamSearch returns search results as stream via streamer.
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
	req, err := http.NewRequestWithContext(ctx, "POST", c.address+c.path, buf)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/x-gob-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Transfer-Encoding", "chunked")

	resp, err := c.conn.Do(req)
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
		if reply.Event == "done" {
			break
		}
		streamer.Send(reply.Result)
	}
	return nil
}
