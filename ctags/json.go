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
	"log"
	"os"
	"strings"
	"sync"

	goctags "github.com/sourcegraph/go-ctags"
)

const debug = false

type Parser = goctags.Parser
type Entry = goctags.Entry

func newProcess(bin string) (Parser, error) {
	opts := goctags.Options{
		Bin:  bin,
		Info: log.New(os.Stderr, "CTAGS INF: ", log.LstdFlags),
	}
	if debug {
		opts.Debug = log.New(os.Stderr, "CTAGS DBG: ", log.LstdFlags)
	}
	return goctags.New(opts)
}

type lockedParser struct {
	p Parser
	l sync.Mutex
}

func (lp *lockedParser) Parse(name string, content []byte) ([]*Entry, error) {
	lp.l.Lock()
	defer lp.l.Unlock()
	return lp.p.Parse(name, content)
}

func (lp *lockedParser) Close() {
	lp.l.Lock()
	defer lp.l.Unlock()
	lp.p.Close()
}

// NewParser creates a parser that is implemented by the given
// universal-ctags binary. The parser is safe for concurrent use.
func NewParser(bin string) (Parser, error) {
	if strings.Contains(bin, "universal-ctags") {
		// todo: restart, parallelization.
		proc, err := newProcess(bin)
		if err != nil {
			return nil, err
		}
		return &lockedParser{p: proc}, nil
	}

	log.Fatal("not implemented")
	return nil, nil
}
