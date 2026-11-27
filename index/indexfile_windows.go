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
	if off > off+sz || off+sz > f.size {
		return nil, fmt.Errorf("out of bounds: %d, len %d, name %s", off+sz, f.size, f.name)
	}
	return f.data[off : off+sz], nil
}

func (f *mmapedIndexFile) Size() (uint32, error) {
	return f.size, nil
}

func (f *mmapedIndexFile) Close() {
	if err := f.data.Unmap(); err != nil {
		log.Printf("WARN failed to unmap %s: %v", f.name, err)
	}
}

func (f *mmapedIndexFile) Name() string {
	return f.name
}

// NewIndexFile returns a new index file. The index file takes
// ownership of the passed in file, and may close it.
func NewIndexFile(f *os.File) (IndexFile, error) {
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}

	sz := fi.Size()
	if sz >= math.MaxUint32 {
		return nil, fmt.Errorf("file %s too large: %d", f.Name(), sz)
	}

	data, err := mmap.MapRegion(f, int(sz), mmap.RDONLY, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("memory mapping %s: %w", f.Name(), err)
	}

	return &mmapedIndexFile{
		name: f.Name(),
		size: uint32(sz),
		data: data,
	}, nil
}
