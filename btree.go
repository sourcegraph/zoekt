package zoekt

import (
	"encoding/binary"
	"fmt"
	"io"
	"sort"
)

// NOTE: getconf PAGESSIZE returns the number of bytes in a memory page, where "page"
// is a fixed-length block, the unit for memory allocation and file mapping
// performed by mmap(2).

type btree struct {
	root node
	opts btreeOpts
}

type btreeOpts struct {
	// bucketSize = (pageSize * 2)/ngramEncoding
	//
	// Why? bucketSize should be chosen such that in the final tree the buckets
	// are as close to the page size as possible, but not above. We insert
	// ngrams in order(!), which means after a split of a leaf, the left leaf
	// is not affected by further inserts and its size is fixed to
	// bucketSize/2. The right-most leaf might have any size in bytes, but the
	// expected size is the size of a page, too.
	//
	bucketSize int
	// all inner nodes, except root, have [v, 2v] children. Note: In the
	// literature, b-trees are inconsistenlty categorized either by the number
	// of children or by the number of keys. We choose the former.
	v int
}

func newBtree(opts btreeOpts) *btree {
	return &btree{&leaf{bucket: make([]ngram, 0, opts.bucketSize)}, opts}
}

func (bt *btree) insert(ng ngram) {
	if leftNode, rightNode, newKey, ok := bt.root.maybeSplit(bt.opts); ok {
		bt.root = &innerNode{keys: []ngram{newKey}, children: []node{leftNode, rightNode}}
	}
	bt.root.insert(ng, bt.opts)
}

func (bt *btree) write(w io.Writer) (err error) {
	var enc [8]byte

	binary.BigEndian.PutUint64(enc[:], uint64(bt.opts.v))
	if _, err := w.Write(enc[:]); err != nil {
		return err
	}

	binary.BigEndian.PutUint64(enc[:], uint64(bt.opts.bucketSize))
	if _, err := w.Write(enc[:]); err != nil {
		return err
	}

	bt.root.visit(func(n node) {
		if err != nil {
			return
		}
		err = n.write(w)
	})
	return
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

// A stateful blob reader. This is just for convenience and to declutter the
// code in readBtree and readNode.
type btreeReader struct {
	blob []byte
	off  int
}

func (r *btreeReader) u64() (uint64, error) {
	if r.off+8 > len(r.blob) {
		return 0, fmt.Errorf("out of bounds")
	}
	v := binary.BigEndian.Uint64(r.blob[r.off : r.off+8])
	r.off += 8
	return v, nil
}

func readBtree(blob []byte) (*btree, error) {
	var bt btree
	reader := &btreeReader{blob: blob}

	v64, err := reader.u64()
	if err != nil {
		return nil, err
	}
	bt.opts.v = int(v64)

	bucketSize64, err := reader.u64()
	if err != nil {
		return nil, err
	}
	bt.opts.bucketSize = int(bucketSize64)

	bt.root, err = readNode(reader)
	if err != nil {
		return nil, err
	}
	return &bt, nil
}

type node interface {
	insert(ng ngram, opts btreeOpts)
	maybeSplit(opts btreeOpts) (left node, right node, newKey ngram, ok bool)
	find(ng ngram) (int, int)
	visit(func(n node))

	// serialize
	write(w io.Writer) error
}

type innerNode struct {
	keys     []ngram
	children []node
}

type leaf struct {
	bucketIndex        uint64
	postingIndexOffset uint64
	bucket             []ngram
}

func (n *innerNode) write(w io.Writer) error {
	var buf [8]byte

	binary.BigEndian.PutUint64(buf[:], uint64(len(n.keys)))
	_, err := w.Write(buf[:])
	if err != nil {
		return err
	}

	for _, key := range n.keys {
		binary.BigEndian.PutUint64(buf[:], uint64(key))
		_, err := w.Write(buf[:])
		if err != nil {
			return err
		}
	}

	return nil
}

func (n *leaf) write(w io.Writer) error {
	var buf [8]byte

	// 0 keys signals that this is a leaf.
	binary.BigEndian.PutUint64(buf[:], uint64(0))
	_, err := w.Write(buf[:])
	if err != nil {
		return err
	}
	binary.BigEndian.PutUint64(buf[:], n.bucketIndex)
	_, err = w.Write(buf[:])
	if err != nil {
		return err
	}

	binary.BigEndian.PutUint64(buf[:], n.postingIndexOffset)
	_, err = w.Write(buf[:])
	if err != nil {
		return err
	}

	return nil
}

func readNode(reader *btreeReader) (node, error) {
	nKeys, err := reader.u64()
	if err != nil {
		return nil, err
	}

	// Leaf
	if nKeys == 0 {
		var n leaf
		n.bucketIndex, err = reader.u64()
		if err != nil {
			return nil, err
		}

		n.postingIndexOffset, err = reader.u64()
		if err != nil {
			return nil, err
		}

		return &n, nil
	}

	var n innerNode
	// Inner node: first read the keys then traverse the children depth-frist.
	n.keys = make([]ngram, 0, nKeys)
	for i := 0; uint64(i) < nKeys; i++ {
		key, err := reader.u64()
		if err != nil {
			return nil, err
		}
		n.keys = append(n.keys, ngram(key))
	}

	n.children = make([]node, 0, nKeys+1)
	for i := 0; uint64(i) < nKeys+1; i++ {
		child, err := readNode(reader)
		if err != nil {
			return nil, err
		}
		n.children = append(n.children, child)
	}

	return &n, nil
}

func (n *leaf) insert(ng ngram, opts btreeOpts) {
	// Insert in ascending order. This is efficient in case we already deal with
	// sorted inputs.
	n.bucket = append(n.bucket, ng)

	for i := len(n.bucket) - 1; i > 0; i-- {
		if n.bucket[i-1] < n.bucket[i] {
			break
		}
		n.bucket[i], n.bucket[i-1] = n.bucket[i-1], n.bucket[i]
	}
}

func (n *innerNode) insert(ng ngram, opts btreeOpts) {
	insertAt := func(i int) {
		// Invariant: Nodes always have a free slot.
		//
		// We split full nodes on the the way down to the leaf. This has the
		// advantage that inserts are handled in a single pass.
		if leftNode, rightNode, newKey, ok := n.children[i].maybeSplit(opts); ok {
			n.children = append(append([]node{}, n.children[0:i]...), append([]node{leftNode, rightNode}, n.children[i+1:]...)...)
			n.keys = append(append([]ngram{}, n.keys[0:i]...), append([]ngram{newKey}, n.keys[i:]...)...)

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
	if len(n.bucket) < opts.bucketSize {
		return
	}
	// Optimization: The left leaf is not mutated after this split because we
	// insert ngrams in order. Hence, we can already allocate the final size
	// which equals bucketSize/2.
	return &leaf{bucket: append(make([]ngram, 0, opts.bucketSize/2), n.bucket[:opts.bucketSize/2]...)},
		&leaf{bucket: append(make([]ngram, 0, opts.bucketSize), n.bucket[opts.bucketSize/2:]...)},
		n.bucket[opts.bucketSize/2],
		true
}

func (n *innerNode) maybeSplit(opts btreeOpts) (left node, right node, newKey ngram, ok bool) {
	if len(n.children) < 2*opts.v {
		return
	}
	return &innerNode{keys: append([]ngram{}, n.keys[0:opts.v-1]...), children: append([]node{}, n.children[:opts.v]...)},
		&innerNode{keys: append([]ngram{}, n.keys[opts.v:]...), children: append([]node{}, n.children[opts.v:]...)},
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

	// We need access to the index to read the bucket into Memory.
	file                 IndexFile
	bucketOffsets        []uint32
	bucketSentinelOffset uint32

	postingOffsets            []uint32
	postingDataSentinelOffset uint32

	debug bool
}

// Get returns the simple section of the posting list associated with the
// ngram. The logic is as follows:
// 1. Search the inner nodes to find the bucket that may contain ng (in MEM)
// 2. Read the bucket from disk (1 disk access)
// 3. Binary search the bucket (in MEM)
// 4. Use the offsets stored in the leaf to return the simple section (in MEM)
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
			for off := 0; off < len(bucket); off = off + 8 {
				gram := ngram(binary.BigEndian.Uint64(bucket[off:]))
				m[gram] = b.getPostingList(int(n.postingIndexOffset) + off)
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

func (b btreeIndex) SizeBytes() int {
	return 0
}
