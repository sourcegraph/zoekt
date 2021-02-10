package stream

import (
	"bufio"
	"bytes"
	"encoding/gob"
	"fmt"
	"net/http"

	"github.com/google/zoekt/rpc"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
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

// StreamSearch sends search results down stream. The caller is responsible to
// close stream after StreamSearch returns.
func (c *client) StreamSearch(q query.Q, opts *zoekt.SearchOptions, stream chan<- *zoekt.SearchResult) error {
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
	req, err := http.NewRequest("POST", c.address+c.path, buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Transfer-Encoding", "chunked")

	resp, err := c.conn.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)

	dec := gob.NewDecoder(reader)
	for {
		reply := &searchReply{}
		err := dec.Decode(reply)
		if err != nil {
			return fmt.Errorf("error during decoding: %w", err)
		}
		if reply.Event == "done" {
			break
		}
		stream <- reply.Result
	}
	return nil
}
