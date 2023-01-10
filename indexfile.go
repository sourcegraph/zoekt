// Copyright 2016 Google Inc. All rights reserved.
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

package zoekt

import (
	"fmt"
	"log"
	"os"

	// cross-platform memory-mapped file package.
	// Benchmarks the same speed as syscall/unix Mmap
	// see https://github.com/peterguy/benchmark-mmap
	mmap "github.com/edsrzf/mmap-go"
)

type mmapedIndexFile struct {
	name string
	size uint32
	data mmap.MMap
}

func (f *mmapedIndexFile) Read(off, sz uint32) ([]byte, error) {
	if off > off+sz || off+sz > uint32(len(f.data)) {
		return nil, fmt.Errorf("out of bounds: %d, len %d, name %s", off+sz, len(f.data), f.name)
	}
	return f.data[off : off+sz], nil
}

func (f *mmapedIndexFile) Name() string {
	return f.name
}

func (f *mmapedIndexFile) Size() (uint32, error) {
	return f.size, nil
}

func (f *mmapedIndexFile) Close() {
	if err := f.data.Unmap(); err != nil {
		log.Printf("WARN failed to memory unmap %s: %v", f.name, err)
	}
}

// NewIndexFileMmapGo returns a new index file. The index file takes
// ownership of the passed in file, and may close it.
func NewIndexFile(f *os.File) (IndexFile, error) {
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}

	sz := fi.Size()
	if sz >= maxUInt32 {
		return nil, fmt.Errorf("file %s too large: %d", f.Name(), sz)
	}
	r := &mmapedIndexFile{
		name: f.Name(),
		size: uint32(sz),
	}

	// round up to the OS page size because mmap likes to align on pages
	// mmap will zero-fill the extra bytes
	pagesize := uint32(os.Getpagesize() - 1)
	rounded := (r.size + pagesize) &^ pagesize

	r.data, err = mmap.MapRegion(f, int(rounded), mmap.RDONLY, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("NewIndexFile: unable to memory map %s: %w", f.Name(), err)
	}

	return r, nil
}
