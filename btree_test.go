package zoekt

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
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

func TestCreateBucketsFromNgramText(t *testing.T) {
	var ngramTextOff uint32 = 7

	offset := func(i int) uint32 {
		return ngramTextOff + uint32((i * ngramEncoding))
	}

	cases := []struct {
		opts        btreeOpts
		ngrams      []ngram
		wantOffsets []uint32
	}{
		{
			opts:        btreeOpts{v: 2, bucketSize: 4},
			ngrams:      []ngram{},
			wantOffsets: []uint32{offset(0), offset(0)},
		},
		{
			opts:        btreeOpts{v: 2, bucketSize: 4},
			ngrams:      []ngram{1},
			wantOffsets: []uint32{offset(0), offset(1)},
		},
		{
			opts:        btreeOpts{v: 2, bucketSize: 4},
			ngrams:      []ngram{1, 2},
			wantOffsets: []uint32{offset(0), offset(2)},
		},
		{
			opts:        btreeOpts{v: 2, bucketSize: 4},
			ngrams:      []ngram{1, 2, 3},
			wantOffsets: []uint32{offset(0), offset(3)},
		},
		{
			opts:        btreeOpts{v: 2, bucketSize: 4},
			ngrams:      []ngram{1, 2, 3, 4},
			wantOffsets: []uint32{offset(0), offset(4)},
		},
		{
			opts:        btreeOpts{v: 2, bucketSize: 4},
			ngrams:      []ngram{1, 2, 3, 4, 5},
			wantOffsets: []uint32{offset(0), offset(2), offset(5)},
		},
		{
			opts:        btreeOpts{v: 2, bucketSize: 4},
			ngrams:      []ngram{1, 2, 3, 4, 5, 6},
			wantOffsets: []uint32{offset(0), offset(2), offset(6)},
		},
		{
			opts:        btreeOpts{v: 2, bucketSize: 4},
			ngrams:      []ngram{1, 2, 3, 4, 5, 6, 7},
			wantOffsets: []uint32{offset(0), offset(2), offset(4), offset(7)},
		},
		{
			opts:        btreeOpts{v: 2, bucketSize: 4},
			ngrams:      []ngram{1, 2, 3, 4, 5, 6, 7, 8},
			wantOffsets: []uint32{offset(0), offset(2), offset(4), offset(8)},
		},
		{
			opts:        btreeOpts{v: 2, bucketSize: 4},
			ngrams:      []ngram{1, 2, 3, 4, 5, 6, 7, 8, 9},
			wantOffsets: []uint32{offset(0), offset(2), offset(4), offset(6), offset(9)},
		},
	}

	for _, tt := range cases {
		t.Run("", func(t *testing.T) {
			toc := &indexTOC{}
			toc.ngramText.sz = uint32(len(tt.ngrams) * ngramEncoding)
			toc.ngramText.off = ngramTextOff
			haveOffsets := createBucketOffsets(toc.ngramText, tt.opts.bucketSize)

			if d := cmp.Diff(tt.wantOffsets, haveOffsets); d != "" {
				t.Fatalf("-want,+got\n%s", d)
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
