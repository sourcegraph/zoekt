package zoekt

import (
	"bytes"
	"fmt"
	"sort"
	"testing"
)

func TestBTree(t *testing.T) {
	bt := newBtree(2, 2)
	insertMany(t, bt, []ngram{6, 2, 4, 3, 9, 8, 7, 5, 1})
	// inner nodes only
	//
	//       8      (root)
	//     /   \
	//  3,4,6   9   (lvl 1)
	//
	want := "{v=2,bucketSize=2}[8][3,4,6][9]"
	if s := bt.String(); s != want {
		t.Fatalf("want %s, got %s", want, s)
	}
}

func TestBTree_hw(t *testing.T) {
	bt := newBtree(2, 2)
	text := []byte("hello world\n")
	var ngrams ngramSlice

	for i := 0; i < len(text)-2; i++ {
		ngrams = append(ngrams, bytesToNGram(text[i:i+3]))
	}

	sort.Sort(ngrams)
	insertMany(t, bt, ngrams)

	want := "{v=2,bucketSize=2}[474989232914442,488183229841527][444202924114028,457397048967276][474989249691759,474989255983136][488183401807980,501377528758372]"
	if s := bt.String(); s != want {
		t.Fatalf("want %s, got %s", want, s)
	}
}

func TestSerialization(t *testing.T) {
	bt := newBtree(2, 2)
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
	bt := newBtree(2, 2)
	insertMany(t, bt, []ngram{6, 2, 4, 3, 9, 8, 7, 5, 1})

	buckets := 0
	offset := 0
	bt.visit(func(n *node) {
		if n.leaf {
			n.bucketIndex = uint64(buckets)
			buckets++
			n.postingIndexOffset = uint64(offset)
			offset += len(n.bucket)
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
			haveBucketIndex, havePostingIndexOffset := bt.findBucket(tt.ng)
			if tt.wantBucketIndex != haveBucketIndex {
				t.Fatalf("bucketIndex: want %d, got %d", tt.wantBucketIndex, haveBucketIndex)
			}

			if tt.wantPostingIndexOffset != havePostingIndexOffset {
				t.Fatalf("postingIndexOffset: want %d, got %d", tt.wantPostingIndexOffset, havePostingIndexOffset)
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
