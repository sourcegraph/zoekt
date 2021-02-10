package sse

import (
	"bufio"
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
)

type client struct {
	address string
	path    string
	conn    *http.Client
}

func NewClientAtAddress(address string) *client {
	return &client{address, DefaultSSEPath, http.DefaultClient}
}

func (c *client) Search(q query.Q, opts *zoekt.SearchOptions, stream chan<- *zoekt.SearchResult) error {
	// Encode query and opts.
	buf := new(bytes.Buffer)
	args := &searchArgs{
		q, opts,
	}
	enc := gob.NewEncoder(buf)
	err := enc.Encode(args)
	if err != nil {
		return err
	}

	// Send request.
	req, err := http.NewRequest("POST", c.address+c.path, buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Transfer-Encoding", "chunked")

	resp, err := c.conn.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)

	// Blocking.
	return decode(reader, stream)
}

// decode decodes events from reader and puts them on stream.
func decode(reader *bufio.Reader, stream chan<- *zoekt.SearchResult) error {
	hasPrefix := func(s []byte, prefix string) bool {
		return bytes.HasPrefix(s, []byte(prefix))
	}
	var eventType string
	data := bytes.Buffer{}
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF && len(line) == 0 {
				return nil
			}
			return err
		}
		switch {
		case hasPrefix(line, FieldComment):
		case hasPrefix(line, FieldEvent):
			eventType = string(bytes.TrimRight(line[len(FieldEvent):], "\n"))
		case hasPrefix(line, FieldData):
			data.Write(line[len(FieldData):])
		case bytes.Equal(line, []byte("\n")):
			switch eventType {
			case "results":
				dec := gob.NewDecoder(bytes.NewReader(data.Bytes()))
				reply := new(searchReply)
				err := dec.Decode(reply)
				if err != nil {
					return err
				}
				stream <- reply.Result
				eventType = ""
				data = bytes.Buffer{}
			case "error":
				return errors.New(string(data.Bytes()))
			default:
				return fmt.Errorf("unknown event type %s", eventType)
			}
		default:
			return fmt.Errorf("unexpected end of stream")
		}
	}
}
