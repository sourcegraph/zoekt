package index

import (
	"encoding/binary"
	"fmt"
	"testing"
)

func TestNgramIndex(t *testing.T) {
	for _, n := range []int{0, 1, 2, ngramBucketSize - 1, ngramBucketSize, ngramBucketSize + 1, 2 * ngramBucketSize, 3*ngramBucketSize + 7} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			// Use the even numbers as ngrams, so that we can look up absent
			// ngrams in between them.
			ngramOf := func(i int) ngram { return ngram(2*i + 2) }

			// The layout is [ngramText][posting lists][posting index]. The
			// posting list of ngram i has size i+1.
			var data []byte
			for i := range n {
				data = binary.BigEndian.AppendUint64(data, uint64(ngramOf(i)))
			}
			ngramSec := simpleSection{off: 0, sz: uint32(len(data))}

			var offsets []uint32
			for i := range n {
				offsets = append(offsets, uint32(len(data)))
				data = append(data, make([]byte, i+1)...)
			}
			postingIndex := simpleSection{off: uint32(len(data)), sz: uint32(4 * n)}
			for _, o := range offsets {
				data = binary.BigEndian.AppendUint32(data, o)
			}

			file := &memSeeker{data: data}
			ngramText, _ := file.Read(ngramSec.off, ngramSec.sz)
			ni := newNgramIndex(file, ngramSec, postingIndex, ngramText)

			if want := max(0, (n-1)/ngramBucketSize); len(ni.splits) != want {
				t.Fatalf("got %d splits, want %d", len(ni.splits), want)
			}

			for i := range n {
				want := simpleSection{off: offsets[i], sz: uint32(i + 1)}
				if got := ni.Get(ngramOf(i)); got != want {
					t.Fatalf("Get(%d): got %+v, want %+v", ngramOf(i), got, want)
				}
				if got := ni.Get(ngramOf(i) + 1); got != (simpleSection{}) {
					t.Fatalf("Get(%d): got %+v, want empty section", ngramOf(i)+1, got)
				}
			}
			if got := ni.Get(0); got != (simpleSection{}) {
				t.Fatalf("Get(0): got %+v, want empty section", got)
			}

			m := ni.DumpMap()
			if len(m) != n {
				t.Fatalf("DumpMap: got %d ngrams, want %d", len(m), n)
			}
			for ng, sec := range m {
				if got := ni.Get(ng); got != sec {
					t.Fatalf("DumpMap: got %+v for %d, Get returns %+v", sec, ng, got)
				}
			}
		})
	}
}

func TestGetBucket(t *testing.T) {
	var off uint32 = 13
	bucketBytes := uint32(ngramBucketSize * ngramEncoding)

	cases := []struct {
		nNgrams     int
		bucketIndex int
		wantOff     uint32
		wantSz      uint32
	}{
		{nNgrams: 1, bucketIndex: 0, wantOff: off, wantSz: 8},
		{nNgrams: 3, bucketIndex: 0, wantOff: off, wantSz: 24},
		{nNgrams: ngramBucketSize, bucketIndex: 0, wantOff: off, wantSz: bucketBytes},
		{nNgrams: 2*ngramBucketSize + 3, bucketIndex: 0, wantOff: off, wantSz: bucketBytes},
		{nNgrams: 2*ngramBucketSize + 3, bucketIndex: 1, wantOff: off + bucketBytes, wantSz: bucketBytes},
		{nNgrams: 2*ngramBucketSize + 3, bucketIndex: 2, wantOff: off + 2*bucketBytes, wantSz: 24},
	}

	for _, tt := range cases {
		t.Run("", func(t *testing.T) {
			ni := ngramIndex{
				ngramSec: simpleSection{off: off, sz: uint32(tt.nNgrams * ngramEncoding)},
			}

			off, sz := ni.getBucket(tt.bucketIndex)
			if off != tt.wantOff {
				t.Fatalf("off: want %d, got %d", tt.wantOff, off)
			}
			if sz != tt.wantSz {
				t.Fatalf("sz: want %d, got %d", tt.wantSz, sz)
			}
		})
	}
}
