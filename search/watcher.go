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

package search

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/sourcegraph/zoekt/index"
)

type shardLoader interface {
	// Load a new file.
	load(filenames ...string)
	drop(filenames ...string)
}

type DirectoryWatcher struct {
	dir    string
	files  map[string]watchedFile
	loader shardLoader

	// closed once ready
	ready    chan struct{}
	readyErr error

	closeOnce sync.Once
	// quit is closed by Close to signal the directory watcher to stop.
	quit chan struct{}
	// stopped is closed once the directory watcher has stopped.
	stopped chan struct{}
}

type watchedFile struct {
	shard   os.FileInfo
	meta    os.FileInfo
	modTime time.Time
}

func (f watchedFile) equal(other watchedFile) bool {
	if !f.modTime.Equal(other.modTime) || !os.SameFile(f.shard, other.shard) {
		return false
	}
	if (f.meta == nil) != (other.meta == nil) {
		return false
	}
	return f.meta == nil || os.SameFile(f.meta, other.meta)
}

func (sw *DirectoryWatcher) Stop() {
	sw.closeOnce.Do(func() {
		close(sw.quit)
		<-sw.stopped
	})
}

func newDirectoryWatcher(dir string, loader shardLoader) (*DirectoryWatcher, error) {
	sw := &DirectoryWatcher{
		dir:     dir,
		files:   map[string]watchedFile{},
		loader:  loader,
		ready:   make(chan struct{}),
		quit:    make(chan struct{}),
		stopped: make(chan struct{}),
	}

	go func() {
		defer close(sw.ready)

		if err := sw.scan(); err != nil {
			sw.readyErr = err
			return
		}

		if err := sw.watch(); err != nil {
			sw.readyErr = err
			return
		}
	}()

	return sw, nil
}

func (s *DirectoryWatcher) WaitUntilReady() error {
	<-s.ready
	return s.readyErr
}

func (s *DirectoryWatcher) String() string {
	return fmt.Sprintf("shardWatcher(%s)", s.dir)
}

// versionFromPath extracts url encoded repository name and
// index format version from a shard name from builder.
func versionFromPath(path string) (string, int) {
	und := strings.LastIndex(path, "_")
	if und < 0 {
		return path, 0
	}

	dot := strings.Index(path[und:], ".")
	if dot < 0 {
		return path, 0
	}
	dot += und

	version, err := strconv.Atoi(path[und+2 : dot])
	if err != nil {
		return path, 0
	}

	return path[:und], version
}

func (s *DirectoryWatcher) scan() error {
	// NOTE: if you change which file extensions are read, please update the
	// watch implementation.
	fs, err := filepath.Glob(filepath.Join(s.dir, "*.zoekt"))
	if err != nil {
		return err
	}

	latest := map[string]int{}
	for _, fn := range fs {
		name, version := versionFromPath(fn)

		// In the case of downgrades, avoid reading
		// newer index formats.
		if version > index.IndexFormatVersion && version > index.NextIndexFormatVersion {
			continue
		}

		if latest[name] < version {
			latest[name] = version
		}
	}

	files := map[string]watchedFile{}
	for _, fn := range fs {
		if name, version := versionFromPath(fn); latest[name] != version {
			continue
		}

		fi, err := os.Lstat(fn)
		if err != nil {
			continue
		}

		current := watchedFile{
			shard:   fi,
			modTime: fi.ModTime(),
		}

		fiMeta, err := os.Lstat(fn + ".meta")
		if err == nil {
			current.meta = fiMeta
			if fiMeta.ModTime().After(current.modTime) {
				current.modTime = fiMeta.ModTime()
			}
		}
		files[fn] = current
	}

	var toLoad []string
	for k, current := range files {
		if previous, ok := s.files[k]; !ok || !previous.equal(current) {
			toLoad = append(toLoad, k)
			s.files[k] = current
		}
	}

	var toDrop []string
	// Unload deleted shards.
	for k := range s.files {
		if _, ok := files[k]; !ok {
			toDrop = append(toDrop, k)
			delete(s.files, k)
		}
	}

	if len(toDrop) > 0 {
		log.Printf("[INFO] unloading %d shard(s): %s", len(toDrop), humanTruncateList(toDrop, 5))
	}

	s.loader.drop(toDrop...)
	s.loader.load(toLoad...)

	return nil
}

func humanTruncateList(paths []string, max int) string {
	sort.Strings(paths)
	var b strings.Builder
	for i, p := range paths {
		if i >= max {
			fmt.Fprintf(&b, "... %d more", len(paths)-i)
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(filepath.Base(p))
	}
	return b.String()
}

func (s *DirectoryWatcher) watch() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := watcher.Add(s.dir); err != nil {
		return err
	}

	// intermediate signal channel so if there are multiple watcher.Events we
	// only call scan once.
	signal := make(chan struct{}, 1)

	go func() {
		notify := func() {
			select {
			case signal <- struct{}{}:
			default:
			}
		}

		ticker := time.NewTicker(time.Minute)

		for {
			select {
			case event := <-watcher.Events:
				// Only notify if a file we read in has changed. This is important to
				// avoid all the events writing to temporary files.
				if strings.HasSuffix(event.Name, ".zoekt") || strings.HasSuffix(event.Name, ".meta") {
					notify()
				}

			case <-ticker.C:
				// Periodically just double check the disk
				notify()

			case err := <-watcher.Errors:
				// Ignore ErrEventOverflow since we rely on the presence of events so
				// safe to ignore.
				if err != nil && !errors.Is(err, fsnotify.ErrEventOverflow) {
					log.Println("[ERROR] watcher error:", err)
				}

			case <-s.quit:
				watcher.Close()
				ticker.Stop()
				close(signal)
				return
			}
		}
	}()

	go func() {
		defer close(s.stopped)
		for range signal {
			if err := s.scan(); err != nil {
				log.Println("[ERROR] watcher error:", err)
			}
		}
	}()

	return nil
}
