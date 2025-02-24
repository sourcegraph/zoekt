package index

import (
	"fmt"
	"testing"
)

func TestBTree_sorted(t *testing.T) {
	bt := newBtree(btreeOpts{bucketSize: 2, v: 2})
	insertMany(t, bt, []ngram{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
	// inner nodes only
	//
	//        [3,5,7]
	//    /   /   \   \
	//  [2] [4]  [6] [8,9]
	//
	want := "{bucketSize:2 v:2}[3,5,7][2][4][6][8,9]"
	if s := bt.String(); s != want {
		t.Fatalf("\nwant:%s\ngot: %s", want, s)
	}
}

func TestFindBucket(t *testing.T) {
	bt := newBtree(btreeOpts{bucketSize: 4, v: 2})
	insertMany(t, bt, []ngram{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})

	buckets := 0
	offset := 0
	bt.visit(func(no node) {
		switch n := no.(type) {
		case *leaf:
			n.bucketIndex = buckets
			buckets++
			n.postingIndexOffset = offset
			offset += n.bucketSize
		case *innerNode:
			return
		}
	})

	cases := []struct {
		ng                     ngram
		wantBucketIndex        int
		wantPostingIndexOffset int
	}{
		{
			ng:                     7,
			wantBucketIndex:        3,
			wantPostingIndexOffset: 6,
		},
	}

	for _, tt := range cases {
		t.Run(fmt.Sprintf("ngram: %d", tt.ng), func(t *testing.T) {
			haveBucketIndex, havePostingIndexOffset := bt.find(tt.ng)
			if tt.wantBucketIndex != haveBucketIndex {
				t.Fatalf("bucketIndex: want %d, got %d", tt.wantBucketIndex, haveBucketIndex)
			}

			if tt.wantPostingIndexOffset != havePostingIndexOffset {
				t.Fatalf("postingIndexOffset: want %d, got %d", tt.wantPostingIndexOffset, havePostingIndexOffset)
			}
		})
	}
}

func TestGetBucket(t *testing.T) {
	var off uint32 = 13
	bucketSize := 4

	cases := []struct {
		nNgrams     int
		bucketIndex int
		wantOff     uint32
		wantSz      uint32
	}{
		// tiny B-tree with just 1 bucket.
		{
			nNgrams:     1,
			bucketIndex: 0,
			wantOff:     off,
			wantSz:      8,
		},
		{
			nNgrams:     2,
			bucketIndex: 0,
			wantOff:     off,
			wantSz:      16,
		},
		{
			nNgrams:     3,
			bucketIndex: 0,
			wantOff:     off,
			wantSz:      24,
		},
		// B-tree with 10 ngrams, think 1,2,3,4,5,6,7,8,9,10
		{
			nNgrams:     10,
			bucketIndex: 0,
			wantOff:     off,
			wantSz:      16,
		},
		{
			nNgrams:     10,
			bucketIndex: 1,
			wantOff:     off + 16,
			wantSz:      16,
		},
		{
			nNgrams:     10,
			bucketIndex: 2,
			wantOff:     off + 32,
			wantSz:      16,
		},
		{
			nNgrams:     10,
			bucketIndex: 3,
			wantOff:     off + 48,
			wantSz:      32,
		},
		{
			nNgrams:     9,
			bucketIndex: 3,
			wantOff:     off + 48,
			wantSz:      24,
		},
	}

	for _, tt := range cases {
		t.Run("", func(t *testing.T) {
			bi := btreeIndex{
				ngramSec: simpleSection{off: off, sz: uint32(tt.nNgrams * ngramEncoding)},
			}

			bt := newBtree(btreeOpts{
				bucketSize: bucketSize,
				v:          2,
			})
			for i := range tt.nNgrams {
				bt.insert(ngram(i + 1))
			}
			bt.freeze()

			bi.bt = bt

			off, sz := bi.getBucket(tt.bucketIndex)
			if off != tt.wantOff {
				t.Fatalf("off: want %d, got %d", tt.wantOff, off)
			}
			if sz != tt.wantSz {
				t.Fatalf("sz: want %d, got %d", tt.wantSz, sz)
			}
		})
	}
}

func insertMany(t *testing.T, bt *btree, ngrams []ngram) {
	t.Helper()
	for _, ng := range ngrams {
		bt.insert(ng)
	}
}
