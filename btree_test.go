package zoekt

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestBTree_unsorted(t *testing.T) {
	bt := newBtree(btreeOpts{bucketSize: 2, v: 2})
	insertMany(t, bt, []ngram{6, 2, 4, 3, 9, 8, 7, 5, 1})
	// inner nodes only
	//
	//         [6]
	//        /   \
	//    [3,4]  [8,9]
	//
	want := "{bucketSize:2 v:2}[6][3,4][8,9]"
	if s := bt.String(); s != want {
		t.Fatalf("want %s, got %s", want, s)
	}
}

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
		t.Fatalf("want %s, got %s", want, s)
	}
}

func TestSerialization(t *testing.T) {
	bt := newBtree(btreeOpts{bucketSize: 2, v: 2})
	insertMany(t, bt, []ngram{6, 2, 4, 3, 9, 8, 7, 5, 1})

	var buf bytes.Buffer

	if err := bt.write(&buf); err != nil {
		t.Fatal(err)
	}

	bt2, err := readBtree(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	if bt.String() != bt2.String() {
		t.Fatalf("\nin:%s\nout:%s", bt.String(), bt2.String())
	}
}

func TestFindBucket(t *testing.T) {
	bt := newBtree(btreeOpts{bucketSize: 2, v: 2})
	insertMany(t, bt, []ngram{6, 2, 4, 3, 9, 8, 7, 5, 1})

	buckets := 0
	offset := 0
	bt.visit(func(no node) {
		switch n := no.(type) {
		case *leaf:
			n.bucketIndex = uint64(buckets)
			buckets++
			n.postingIndexOffset = uint64(offset)
			offset += len(n.bucket)
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
			wantPostingIndexOffset: 5,
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
	offset := func(i int) uint32 {
		return uint32(i * ngramEncoding)
	}

	cases := []struct {
		opts        btreeOpts
		ngrams      []ngram
		wantOffsets []uint32
	}{
		{
			opts:        btreeOpts{v: 2, bucketSize: 4},
			ngrams:      []ngram{},
			wantOffsets: []uint32{0},
		},
		{
			opts:        btreeOpts{v: 2, bucketSize: 4},
			ngrams:      []ngram{1},
			wantOffsets: []uint32{0},
		},
		{
			opts:        btreeOpts{v: 2, bucketSize: 4},
			ngrams:      []ngram{1, 2},
			wantOffsets: []uint32{0},
		},
		{
			opts:        btreeOpts{v: 2, bucketSize: 4},
			ngrams:      []ngram{1, 2, 3},
			wantOffsets: []uint32{0},
		},
		{
			opts:        btreeOpts{v: 2, bucketSize: 4},
			ngrams:      []ngram{1, 2, 3, 4},
			wantOffsets: []uint32{0},
		},
		{
			opts:        btreeOpts{v: 2, bucketSize: 4},
			ngrams:      []ngram{1, 2, 3, 4, 5},
			wantOffsets: []uint32{0, offset(2)},
		},
		{
			opts:        btreeOpts{v: 2, bucketSize: 4},
			ngrams:      []ngram{1, 2, 3, 4, 5, 6},
			wantOffsets: []uint32{0, offset(2)},
		},
		{
			opts:        btreeOpts{v: 2, bucketSize: 4},
			ngrams:      []ngram{1, 2, 3, 4, 5, 6, 7},
			wantOffsets: []uint32{0, offset(2), offset(4)},
		},
		{
			opts:   btreeOpts{v: 2, bucketSize: 4},
			ngrams: []ngram{1, 2, 3, 4, 5, 6, 7, 8},
			//             ^     ^     ^
			wantOffsets: []uint32{0, offset(2), offset(4)},
		},
		{
			opts:   btreeOpts{v: 2, bucketSize: 4},
			ngrams: []ngram{1, 2, 3, 4, 5, 6, 7, 8, 9},
			//             ^     ^     ^     ^
			wantOffsets: []uint32{0, offset(2), offset(4), offset(6)},
		},
	}

	for _, tt := range cases {
		t.Run("", func(t *testing.T) {
			toc := &indexTOC{}
			toc.ngramText.sz = uint32(len(tt.ngrams) * ngramEncoding)
			createBucketsFromNgramText(toc, tt.opts.bucketSize)

			if d := cmp.Diff(tt.wantOffsets, toc.btreeBuckets.offsets); d != "" {
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
