package zoekt

import (
	"encoding/binary"
	"io"
)

// NOTE: getconf PAGESSIZE returns the number of bytes in a memory page, where "page"
// is a fixed-length block, the unit for memory allocation and file mapping
// performed by mmap(2).

// TODO: When writing the buckets to disk, we have to make sure we perfix the
// compound section with enough bytes to match the page boundaries of 4096
// bytes.

// TODO: Refactor with interface and two nodes types.

// TODO: Use bucketOpts instead of v and blockSize

// b-tree properties
// - all leaves are at the same level
// - all inner nodes, except root, have [v, 2v] keys
type btree struct {
	root *node
	// all inner nodes, except root, have [v, 2v] keys.
	v int
	// the max number of values attached to a leaf node. The bucketSize should
	// be chosen based on the page size.
	bucketSize int
}

func newBtree(v, bucketSize int) *btree {
	return &btree{&node{leaf: true, bucket: make([]record, 0, bucketSize)}, v, bucketSize}
}

func (bt *btree) insert(r record) {
	if leftNode, rightNode, newKey, ok := maybeSplit(bt.root, bt.v, bt.bucketSize); ok {
		bt.root = &node{keys: []ngram{newKey}, children: []node{leftNode, rightNode}}
	}

	bt.root.insert(r, bt.bucketSize, bt.v)
}

func (bt *btree) write(w io.Writer) (err error) {
	var enc [binary.MaxVarintLen64]byte

	m := binary.PutVarint(enc[:], int64(bt.v))
	_, err = w.Write(enc[:m])
	if err != nil {
		return err
	}

	m = binary.PutVarint(enc[:], int64(bt.bucketSize))
	_, err = w.Write(enc[:m])
	if err != nil {
		return err
	}

	bt.root.visit(func(n *node) {
		if err != nil {
			return
		}
		err = n.write(w)
	})
	return
}

func readBtree(r io.ByteReader) (*btree, error) {
	var bt btree

	v64, err := binary.ReadVarint(r)
	if err != nil {
		return nil, err
	}
	bt.v = int(v64)

	bucketSize64, err := binary.ReadVarint(r)
	if err != nil {
		return nil, err
	}
	bt.bucketSize = int(bucketSize64)

	bt.root, err = readNode(r)
	if err != nil {
		return nil, err
	}

	return &bt, nil
}

type node struct {
	keys     []ngram
	children []node

	leaf bool
	// bucketOffset is set when we read the shard from disk.
	bucketOffset uint32
	bucket       []record
}

func (n *node) write(w io.Writer) error {
	var enc [binary.MaxVarintLen64]byte

	// #keys
	m := binary.PutUvarint(enc[:], uint64(len(n.keys)))
	_, err := w.Write(enc[:m])
	if err != nil {
		return err
	}

	for _, key := range n.keys {
		m := binary.PutUvarint(enc[:], uint64(key))
		_, err := w.Write(enc[:m])
		if err != nil {
			return err
		}
	}

	if len(n.keys) == 0 {
		m := binary.PutUvarint(enc[:], uint64(n.bucketOffset))
		_, err := w.Write(enc[:m])
		if err != nil {
			return err
		}
	}

	return nil
}

func readNode(r io.ByteReader) (*node, error) {
	var n node
	nKeys, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, err
	}

	// Leaf
	if nKeys == 0 {
		bucketOffset64, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, err
		}
		n.bucketOffset = uint32(bucketOffset64)
		n.leaf = true
		return &n, nil

	}

	// Inner node: first read the keys then traverse the children depth-frist.
	n.keys = make([]ngram, 0, nKeys)
	for i := 0; uint64(i) < nKeys; i++ {
		key, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, err
		}
		n.keys = append(n.keys, ngram(key))
	}

	n.children = make([]node, 0, nKeys+1)
	for i := 0; uint64(i) < nKeys+1; i++ {
		child, err := readNode(r)
		if err != nil {
			return nil, err
		}

		n.children = append(n.children, *child)
	}
	return &n, nil
}

// record is a tuple of an ngram and the byte offset of the associated posting
// list.
type record struct {
	key           ngram
	postingOffset uint32
}

func (n *node) insert(r record, bucketSize int, v int) {
	insertAt := func(i int) {
		// Invariant: Leaf nodes always have a free slot.
		//
		// We split full nodes on the the way down to the leaf. This has the
		// advantage that inserts are handled in a single pass and that leaf
		// nodes always have enough space to insert a new item.
		if leftNode, rightNode, newKey, ok := maybeSplit(&n.children[i], v, bucketSize); ok {
			n.children = append(append([]node{}, n.children[0:i]...), append([]node{leftNode, rightNode}, n.children[i+1:]...)...)
			n.keys = append(append([]ngram{}, n.keys[0:i]...), append([]ngram{newKey}, n.keys[i:]...)...)

			// A split might shift the target index by 1.
			if r.key >= n.keys[i] {
				i++
			}
		}
		n.children[i].insert(r, bucketSize, v)
	}

	if n.leaf {
		// See invariant maintained by insertAt.
		n.bucket = append(n.bucket, r)

		// Insert in ascending order. This is efficient in case we already deal with
		// sorted inputs.
		for i := len(n.bucket) - 1; i > 0; i-- {
			if n.bucket[i-1].key < n.bucket[i].key {
				break
			}
			n.bucket[i], n.bucket[i-1] = n.bucket[i-1], n.bucket[i]
		}
	} else {
		for i, k := range n.keys {
			if r.key < k {
				insertAt(i)
				return
			}
		}
		insertAt(len(n.children) - 1)
	}
}

func maybeSplit(n *node, v, bucketSize int) (left node, right node, newKey ngram, ok bool) {
	if n.leaf {
		return maybeSplitLeaf(n, bucketSize)
	} else {
		return maybeSplitInner(n, v)
	}
}

func maybeSplitLeaf(n *node, bucketSize int) (left node, right node, newKey ngram, ok bool) {
	if len(n.bucket) < bucketSize {
		return
	}
	return node{leaf: true, bucket: append(make([]record, 0, bucketSize), n.bucket[:bucketSize/2]...)},
		node{leaf: true, bucket: append(make([]record, 0, bucketSize), n.bucket[bucketSize/2:]...)},
		n.bucket[bucketSize/2].key,
		true
}

// TODO: handle v=1
func maybeSplitInner(n *node, v int) (left node, right node, newKey ngram, ok bool) {
	if len(n.keys) < 2*v {
		return
	}
	return node{keys: append([]ngram{}, n.keys[0:v]...), children: append([]node{}, n.children[:v+1]...)},
		node{keys: append([]ngram{}, n.keys[v+1:]...), children: append([]node{}, n.children[v+1:]...)},
		n.keys[v],
		true
}

func (n *node) visit(f func(n *node)) {
	f(n)
	if n.leaf {
		return
	}
	for _, child := range n.children {
		child.visit(f)
	}
}
