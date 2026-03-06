// Copyright 2025 Embark Studios. Windows mmap-based IndexFile implementation.
//
// Uses edsrzf/mmap-go which wraps CreateFileMapping/MapViewOfFile on Windows,
// providing the same zero-copy read performance as unix mmap.

//go:build windows

package index

import (
	"fmt"
	"log"
	"math"
	"os"

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
		log.Printf("WARN failed to Unmap %s: %v", f.name, err)
	}
}

// NewIndexFile returns a new index file backed by mmap on Windows.
// Uses CreateFileMapping/MapViewOfFile via the mmap-go library.
func NewIndexFile(f *os.File) (IndexFile, error) {
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	sz := fi.Size()
	if sz >= math.MaxUint32 {
		f.Close()
		return nil, fmt.Errorf("file %s too large: %d", f.Name(), sz)
	}

	if sz == 0 {
		f.Close()
		return nil, fmt.Errorf("file %s is empty", f.Name())
	}

	data, err := mmap.Map(f, mmap.RDONLY, 0)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("mmap %s: %w", f.Name(), err)
	}

	// Close the file handle — the mapping keeps the data accessible.
	f.Close()

	return &mmapedIndexFile{
		name: f.Name(),
		size: uint32(sz),
		data: data,
	}, nil
}
