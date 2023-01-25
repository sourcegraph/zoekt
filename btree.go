// B+-tree
//
// The tree we implement here is a B+-tree based on a paper by Ceylan and
// Mihalcea [1].
//
// B+-trees store all values in the leaves. In our case we store trigrams with
// the goal to quickly retrieve a pointer to the posting list for a given
// trigram. We choose the number of trigrams to store at each leaf based on the
// page size, IE we make sure we are able to load a bucket of ngrams with a
// single disk access.
//
// Here is an example of how our B+-tree looks like for a simple input:
//
// input: "hello world", bucketSize=2, v=2
//
// legend: ()=inner node, []=leaf
//
// (level 1)                        (hel, lo_)
//
// (level 2)          (ell)           (llo)           (o_w, irl, red)
//
// (level 3)      [_wo]  [ell]    [hel]  [llo]    [lo_] [o_w] [orl] [rld, wor]
//
// The leafs are stored as part of the index on disk (mmaped) while all inner
// nodes are loaded into memory when we load the shard.
//
// [1] H. Ceylan and R. Mihalcea. 2011. An Efficient Indexer for Large N-Gram
// Corpora, Proceedings of the ACL-HLT 2011 System Demonstrations, pages
// 103-108

package zoekt

import (
	"encoding/binary"
	"fmt"
	"sort"
)

// btreeBucketSize should be chosen such that in the final tree the buckets are
// as close to the page size as possible, but not above. We insert ngrams in
// order(!), which means after a split of a leaf, the left leaf is not affected
// by further inserts and its size is fixed to bucketSize/2. The rightmost leaf
// might store up to btreeBucketSize ngrams, but the expected size is
// btreeBucketSize/2, too.
//
// On linux "getconf PAGESSIZE" returns the number of bytes in a memory page.
const btreeBucketSize = (4096 * 2) / ngramEncoding

type btree struct {
	root node
	opts btreeOpts
}

type btreeOpts struct {
	// How many ngrams can be stored at a leaf node.
	bucketSize int
	// all inner nodes, except root, have [v, 2v] children. In the literature,
	// b-trees are inconsistently categorized either by the number of children
	// or by the number of keys. We choose the former.
	v int
}

func newBtree(opts btreeOpts) *btree {
	return &btree{&leaf{}, opts}
}

func (bt *btree) insert(ng ngram) {
	if leftNode, rightNode, newKey, ok := bt.root.maybeSplit(bt.opts); ok {
		bt.root = &innerNode{keys: []ngram{newKey}, children: []node{leftNode, rightNode}}
	}
	bt.root.insert(ng, bt.opts)
}

// find returns the tuple (bucketIndex, postingIndexOffset), both of which are
// stored at the leaf level. They are effectively pointers to the bucket and
// the posting lists for ngrams stored in the bucket. Since ngrams and their
// posting lists are stored in order, knowing the index of the posting list of
// the first item in the bucket is sufficient.
func (bt *btree) find(ng ngram) (int, int) {
	if bt.root == nil {
		return -1, -1
	}
	return bt.root.find(ng)
}

func (bt *btree) visit(f func(n node)) {
	bt.root.visit(f)
}

func (bt *btree) sizeBytes() int {
	sz := 2 * 8 // opts

	bt.visit(func(n node) {
		sz += n.sizeBytes()
	})

	return sz
}

type node interface {
	insert(ng ngram, opts btreeOpts)
	maybeSplit(opts btreeOpts) (left node, right node, newKey ngram, ok bool)
	find(ng ngram) (int, int)
	visit(func(n node))
	sizeBytes() int
}

type innerNode struct {
	keys     []ngram
	children []node
}

type leaf struct {
	bucketIndex int
	// postingIndexOffset is the index of the posting list of the first ngram
	// in the bucket. This is enough to determine the index of the posting list
	// for every other key in the bucket.
	postingIndexOffset int

	// Optimization: Because we insert ngrams in order, we don't actually have
	// to fill the buckets. We just have to keep track of the size of the
	// bucket, so we know when to split, and the key that we have to propagate
	// up to the parent node when we split.
	//
	// If in the future we decide to mutate buckets, we have to replace
	// bucketSize and splitKey by []ngram.
	bucketSize int
	splitKey   ngram
}

func (n *innerNode) sizeBytes() int {
	return len(n.keys)*ngramEncoding + len(n.children)*int(interfaceBytes)
}

func (n *leaf) sizeBytes() int {
	return 4 * 8
}

func (n *leaf) insert(ng ngram, opts btreeOpts) {
	n.bucketSize++

	if n.bucketSize == (opts.bucketSize/2)+1 {
		n.splitKey = ng
	}
}

func (n *innerNode) insert(ng ngram, opts btreeOpts) {
	insertAt := func(i int) {
		// Invariant: Nodes always have a free slot.
		//
		// We split full nodes on the the way down to the leaf. This has the
		// advantage that inserts are handled in a single pass.
		if leftNode, rightNode, newKey, ok := n.children[i].maybeSplit(opts); ok {
			n.keys = append(n.keys[0:i], append([]ngram{newKey}, n.keys[i:]...)...)
			n.children = append(n.children[0:i], append([]node{leftNode, rightNode}, n.children[i+1:]...)...)

			// A split might shift the target index by 1.
			if ng >= n.keys[i] {
				i++
			}
		}
		n.children[i].insert(ng, opts)
	}

	for i, k := range n.keys {
		if ng < k {
			insertAt(i)
			return
		}
	}
	insertAt(len(n.children) - 1)
}

// See btree.find
func (n *innerNode) find(ng ngram) (int, int) {
	for i, k := range n.keys {
		if ng < k {
			return n.children[i].find(ng)
		}
	}
	return n.children[len(n.children)-1].find(ng)
}

// See btree.find
func (n *leaf) find(ng ngram) (int, int) {
	return int(n.bucketIndex), int(n.postingIndexOffset)
}

func (n *leaf) maybeSplit(opts btreeOpts) (left node, right node, newKey ngram, ok bool) {
	if n.bucketSize < opts.bucketSize {
		return
	}
	return &leaf{bucketSize: opts.bucketSize / 2},
		&leaf{bucketSize: opts.bucketSize / 2},
		n.splitKey,
		true
}

func (n *innerNode) maybeSplit(opts btreeOpts) (left node, right node, newKey ngram, ok bool) {
	if len(n.children) < 2*opts.v {
		return
	}
	return &innerNode{
			keys:     append(make([]ngram, 0, opts.v-1), n.keys[0:opts.v-1]...),
			children: append(make([]node, 0, opts.v), n.children[:opts.v]...)},
		&innerNode{
			keys:     append(make([]ngram, 0, (2*opts.v)-1), n.keys[opts.v:]...),
			children: append(make([]node, 0, 2*opts.v), n.children[opts.v:]...)},
		n.keys[opts.v-1],
		true
}

func (n *leaf) visit(f func(n node)) {
	f(n)
	return
}

func (n *innerNode) visit(f func(n node)) {
	f(n)
	for _, child := range n.children {
		child.visit(f)
	}
}

func (bt *btree) String() string {
	s := ""
	s += fmt.Sprintf("%+v", bt.opts)
	bt.root.visit(func(n node) {
		switch nd := n.(type) {
		case *leaf:
			return
		case *innerNode:
			s += fmt.Sprintf("[")
			for _, key := range nd.keys {
				s += fmt.Sprintf("%d,", key)
			}
			s = s[:len(s)-1] // remove coma
			s += fmt.Sprintf("]")

		}
	})
	return s
}

type btreeIndex struct {
	bt *btree

	// We need the index to read buckets into memory.
	file IndexFile

	bucketOffsets        []uint32
	bucketSentinelOffset uint32

	postingOffsets            []uint32
	postingDataSentinelOffset uint32
}

func (b btreeIndex) SizeBytes() int {
	return b.bt.sizeBytes() + 2*int(sliceHeaderBytes) + 4*len(b.bucketOffsets) + 4*len(b.postingOffsets) + 2*4
}

// Get returns the simple section of the posting list associated with the
// ngram. The logic is as follows:
// 1. Search the inner nodes to find the bucket that may contain ng (in MEM)
// 2. Read the bucket from disk (1 disk access)
// 3. Binary search the bucket (in MEM)
// 4. Return the simple section pointing to the posting list (in MEM)
func (b btreeIndex) Get(ng ngram) (ss simpleSection) {
	// find bucket
	bucketIndex, postingIndexOffset := b.bt.find(ng)

	// read bucket into memory
	var sz, off uint32
	off, sz = b.bucketOffsets[bucketIndex], 0
	if bucketIndex+1 < len(b.bucketOffsets) {
		sz = b.bucketOffsets[bucketIndex+1] - off
	} else {
		sz = b.bucketSentinelOffset - off
	}

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

	return b.getPostingList(postingIndexOffset + x)
}

func (b btreeIndex) DumpMap() map[ngram]simpleSection {
	m := make(map[ngram]simpleSection, len(b.bucketOffsets)*b.bt.opts.bucketSize)

	b.bt.visit(func(no node) {
		switch n := no.(type) {
		case *leaf:
			// read bucket into memory
			var sz, off uint32
			off, sz = b.bucketOffsets[n.bucketIndex], 0
			if int(n.bucketIndex)+1 < len(b.bucketOffsets) {
				sz = b.bucketOffsets[n.bucketIndex+1] - off
			} else {
				sz = b.bucketSentinelOffset - off
			}
			bucket, _ := b.file.Read(off, sz)

			// decode all ngrams in the bucket and fill map
			for i := 0; i < len(bucket)/ngramEncoding; i++ {
				gram := ngram(binary.BigEndian.Uint64(bucket[i*8:]))
				m[gram] = b.getPostingList(int(n.postingIndexOffset) + i)
			}
		case *innerNode:
			return
		}
	})

	return m
}

func (b btreeIndex) getPostingList(postingIndex int) simpleSection {
	if postingIndex+1 < len(b.postingOffsets) {
		return simpleSection{
			off: b.postingOffsets[postingIndex],
			sz:  b.postingOffsets[postingIndex+1] - b.postingOffsets[postingIndex],
		}
	} else {
		return simpleSection{
			off: b.postingOffsets[postingIndex],
			sz:  b.postingDataSentinelOffset - b.postingOffsets[postingIndex],
		}
	}
}
