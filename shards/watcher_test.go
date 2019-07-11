// Copyright 2018 Google Inc. All rights reserved.
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

package shards

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type loggingLoader struct {
	loads chan string
	drops chan string
	sorts chan struct{}
}

func (l *loggingLoader) load(k string) {
	l.loads <- k
}

func (l *loggingLoader) drop(k string) {
	l.drops <- k
}

func (l *loggingLoader) sort() {
	l.sorts <- struct{}{}
}

func advanceFS() {
	time.Sleep(10 * time.Millisecond)
}

func TestDirWatcherUnloadOnce(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}

	logger := &loggingLoader{
		loads: make(chan string, 10),
		drops: make(chan string, 10),
		sorts: make(chan struct{}, 10),
	}
	// Upstream fails if empty. Sourcegraph does not
	// _, err := NewDirectoryWatcher(dir, logger)
	// if err == nil || !strings.Contains(err.Error(), "empty") {
	// 	t.Fatalf("got %v, want 'empty'", err)
	// }

	shard := filepath.Join(dir, "foo.zoekt")
	if err := ioutil.WriteFile(shard, []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	dw, err := NewDirectoryWatcher(dir, logger)
	if err != nil {
		t.Fatalf("NewDirectoryWatcher: %v", err)
	}
	defer dw.Close()

	if got := <-logger.loads; got != shard {
		t.Fatalf("got load event %v, want %v", got, shard)
	}

	// Must sleep because of FS timestamp resolution.
	advanceFS()
	if err := ioutil.WriteFile(shard, []byte("changed"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if got := <-logger.loads; got != shard {
		t.Fatalf("got load event %v, want %v", got, shard)
	}

	advanceFS()
	if err := os.Remove(shard); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if got := <-logger.drops; got != shard {
		t.Fatalf("got drops event %v, want %v", got, shard)
	}

	advanceFS()
	if err := ioutil.WriteFile(shard+".bla", []byte("changed"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	dw.Close()

	select {
	case k := <-logger.loads:
		t.Errorf("spurious load of %q", k)
	case k := <-logger.drops:
		t.Errorf("spurious drops of %q", k)
	default:
	}
}

func TestDirWatcherLoadEmpty(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}

	logger := &loggingLoader{
		loads: make(chan string, 10),
		drops: make(chan string, 10),
		sorts: make(chan struct{}, 10),
	}
	dw, err := NewDirectoryWatcher(dir, logger)
	if err != nil {
		t.Fatal(err)
	}
	advanceFS()
	dw.Close()

	select {
	case k := <-logger.loads:
		t.Errorf("spurious load of %q", k)
	case k := <-logger.drops:
		t.Errorf("spurious drops of %q", k)
	default:
	}
}
