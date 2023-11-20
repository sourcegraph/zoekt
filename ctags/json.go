// Copyright 2017 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ctags

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	goctags "github.com/sourcegraph/go-ctags"
)

const debug = false

type Parser = goctags.Parser
type Entry = goctags.Entry

type parseReq struct {
	Name    string
	Content []byte
}

type parseResp struct {
	Entries []*Entry
	Err     error
}

type lockedParser struct {
	mu   sync.Mutex
	opts goctags.Options
	p    Parser
	send chan<- parseReq
	recv <-chan parseResp
}

// parseTimeout is how long we wait for a response for parsing a single file
// in ctags. 1 minute is a very conservative timeout which we should only hit
// if ctags hangs.
const parseTimeout = time.Minute

// Parse wraps go-ctags Parse. It lazily starts the process and adds a timeout
// around parse requests. Additionally it serializes access to the parsing
// process. The timeout is important since we occasionally come across
// documents which hang universal-ctags.
func (lp *lockedParser) Parse(name string, content []byte) ([]*Entry, error) {
	lp.mu.Lock()
	defer lp.mu.Unlock()

	if lp.p == nil {
		p, err := goctags.New(lp.opts)
		if err != nil {
			return nil, err
		}
		send := make(chan parseReq)
		// buf of 1 so we avoid blocking sends in the parser if we exit early.
		recv := make(chan parseResp, 1)

		go func() {
			defer close(recv)
			for req := range send {
				entries, err := p.Parse(req.Name, req.Content)
				recv <- parseResp{Entries: entries, Err: err}
			}
		}()

		lp.p = p
		lp.send = send
		lp.recv = recv
	}

	lp.send <- parseReq{Name: name, Content: content}

	deadline := time.NewTimer(parseTimeout)
	defer deadline.Stop()

	select {
	case resp := <-lp.recv:
		return resp.Entries, resp.Err
	case <-deadline.C:
		// Error out since ctags hanging is a sign something bad is happening.
		lp.close()
		return nil, fmt.Errorf("ctags timedout after %s parsing %s", parseTimeout, name)
	}
}

func (lp *lockedParser) Close() {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	lp.close()
}

// close assumes lp.mu is held.
func (lp *lockedParser) close() {
	if lp.p == nil {
		return
	}

	lp.p.Close()
	lp.p = nil
	close(lp.send)
	lp.send = nil
	lp.recv = nil
}

// NewParser creates a parser that is implemented by the given
// universal-ctags binary. The parser is safe for concurrent use.
func NewParser(bin string) Parser {
	opts := goctags.Options{
		Bin: bin,
	}
	if debug {
		opts.Info = log.New(os.Stderr, "CTAGS INF: ", log.LstdFlags)
		opts.Debug = log.New(os.Stderr, "CTAGS DBG: ", log.LstdFlags)
	}
	return &lockedParser{opts: opts,}
}
