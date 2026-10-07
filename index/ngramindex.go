package index

import (
	"encoding/binary"
	"sort"
)

// ngramBucketSize is the number of ngrams per bucket. A bucket of 512 ngrams
// is 4 KiB, the page size on most systems, so reading a bucket from disk
// usually takes a single disk access.
const ngramBucketSize = 512

// ngramIndex finds the posting list of an ngram.
//
// The sorted ngrams and the offsets of their posting lists stay on disk
// (mmaped). We split the sorted ngrams into buckets of ngramBucketSize ngrams
// and only keep the first ngram of every bucket in memory. A lookup binary
// searches those in memory, reads a single bucket from disk and binary searches
// the bucket.
type ngramIndex struct {
	// We need the index file to read buckets into memory.
	file IndexFile

	// splits[i] is the first ngram of bucket i+1.
	splits []ngram

	// buckets
	ngramSec simpleSection

	postingIndex simpleSection
}

// newNgramIndex returns an ngramIndex for the sorted ngrams in ngramText,
// which is the content of ngramSec.
func newNgramIndex(file IndexFile, ngramSec simpleSection, postingIndex simpleSection, ngramText []byte) ngramIndex {
	n := len(ngramText) / ngramEncoding
	var splits []ngram
	if n > 0 {
		splits = make([]ngram, 0, (n-1)/ngramBucketSize)
	}
	for i := ngramBucketSize; i < n; i += ngramBucketSize {
		splits = append(splits, ngram(binary.BigEndian.Uint64(ngramText[i*ngramEncoding:])))
	}

	return ngramIndex{
		file:         file,
		splits:       splits,
		ngramSec:     ngramSec,
		postingIndex: postingIndex,
	}
}

// SizeBytes returns how much memory this structure uses in the heap.
func (b ngramIndex) SizeBytes() int {
	// file (interface) + splits (slice header) + ngramSec + postingIndex
	sz := 16 + 24 + 8 + 8
	sz += cap(b.splits) * ngramEncoding
	return sz
}

// Get returns the simple section of the posting list associated with the
// ngram. The logic is as follows:
// 1. Binary search the first ngram of every bucket to find the bucket that may contain ng (in MEM)
// 2. Read the bucket from disk (1 disk access)
// 3. Binary search the bucket (in MEM)
// 4. Return the simple section pointing to the posting list (1 disk access)
func (b ngramIndex) Get(ng ngram) simpleSection {
	if b.ngramSec.sz == 0 {
		return simpleSection{}
	}

	// find bucket
	bucketIndex := sort.Search(len(b.splits), func(i int) bool {
		return ng < b.splits[i]
	})

	// read bucket into memory
	off, sz := b.getBucket(bucketIndex)
	bucket, err := b.file.Read(off, sz)
	if err != nil {
		return simpleSection{}
	}

	// find ngram in bucket
	getNGram := func(i int) ngram {
		i *= ngramEncoding
		return ngram(binary.BigEndian.Uint64(bucket[i : i+ngramEncoding]))
	}

	bucketSize := len(bucket) / ngramEncoding
	x := sort.Search(bucketSize, func(i int) bool {
		return ng <= getNGram(i)
	})

	// return associated posting list
	if x >= bucketSize || getNGram(x) != ng {
		return simpleSection{}
	}

	return b.getPostingList(bucketIndex*ngramBucketSize + x)
}

// getPostingList returns the simple section pointing to the posting list of
// the ngram at ngramIndex.
//
// Assumming we don't hit a page boundary, which should be rare given that we
// only read 8 bytes, we need 1 disk access to read the posting offset.
func (b ngramIndex) getPostingList(ngramIndex int) simpleSection {
	relativeOffsetBytes := uint32(ngramIndex) * 4

	if relativeOffsetBytes+8 <= b.postingIndex.sz {
		// read 2 offsets
		o, err := b.file.Read(b.postingIndex.off+relativeOffsetBytes, 8)
		if err != nil {
			return simpleSection{}
		}

		start := binary.BigEndian.Uint32(o[0:4])
		end := binary.BigEndian.Uint32(o[4:8])
		return simpleSection{
			off: start,
			sz:  end - start,
		}
	} else {
		// last ngram => read 1 offset and calculate the size of the posting
		// list from the offset of index section.
		o, err := b.file.Read(b.postingIndex.off+relativeOffsetBytes, 4)
		if err != nil {
			return simpleSection{}
		}

		start := binary.BigEndian.Uint32(o[0:4])
		return simpleSection{
			off: start,
			// The layout of the posting list compound section on disk is
			//
			//                      start       b.postingIndex.off
			//                      v           v
			// [[posting lists (simple section)][index (simple section)]]
			//                      <---------->
			//                    last posting list
			//
			sz: b.postingIndex.off - start,
		}
	}
}

// getBucket returns the location of bucket bucketIndex in the index file. All
// but the last bucket have exactly ngramBucketSize ngrams.
func (b ngramIndex) getBucket(bucketIndex int) (off uint32, sz uint32) {
	sz = ngramBucketSize * ngramEncoding
	off = b.ngramSec.off + uint32(bucketIndex)*sz

	// The last bucket has size up to the end of the ngramSec.
	end := b.ngramSec.off + b.ngramSec.sz
	if off+sz > end {
		sz = end - off
	}

	return
}

// DumpMap is a debug method which returns the ngram index as an in-memory
// representation. This is how zoekt represents the ngram index in
// google/zoekt.
func (b ngramIndex) DumpMap() map[ngram]simpleSection {
	if b.ngramSec.sz == 0 {
		return nil
	}

	ngramText, err := b.file.Read(b.ngramSec.off, b.ngramSec.sz)
	if err != nil {
		return nil
	}

	n := len(ngramText) / ngramEncoding
	m := make(map[ngram]simpleSection, n)
	for i := range n {
		gram := ngram(binary.BigEndian.Uint64(ngramText[i*ngramEncoding:]))
		m[gram] = b.getPostingList(i)
	}

	return m
}
