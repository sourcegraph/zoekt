package zoekt

import (
	"encoding/binary"
	"fmt"
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
	return &btree{&node{leaf: true, bucket: make([]ngram, 0, bucketSize)}, v, bucketSize}
}

func (bt *btree) insert(ng ngram) {
	if leftNode, rightNode, newKey, ok := maybeSplit(bt.root, bt.v, bt.bucketSize); ok {
		bt.root = &node{keys: []ngram{newKey}, children: []node{leftNode, rightNode}}
	}

	bt.root.insert(ng, bt.bucketSize, bt.v)
}

func (bt *btree) write(w io.Writer) (err error) {
	var enc [8]byte

	binary.BigEndian.PutUint64(enc[:], uint64(bt.v))
	if _, err := w.Write(enc[:]); err != nil {
		return err
	}

	binary.BigEndian.PutUint64(enc[:], uint64(bt.bucketSize))
	if _, err := w.Write(enc[:]); err != nil {
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
	bt.v = int(v64)

	bucketSize64, err := reader.u64()
	if err != nil {
		return nil, err
	}
	bt.bucketSize = int(bucketSize64)

	bt.root, err = readNode(reader)
	if err != nil {
		return nil, err
	}
	return &bt, nil
}

func (bt *btree) visit(f func(n *node)) {
	bt.root.visit(f)
}

// type records []record
//
// func (r records) encode() ([]byte, error) {
// var w bytes.Buffer
// var enc [binary.MaxVarintLen64]byte
// for _, rr := range r {
// m := binary.PutUvarint(enc[:], uint64(rr.key))
// _, err := w.Write(enc[:m])
// if err != nil {
// return nil, err
// }
// // TODO: can we do better?
// m = binary.PutUvarint(enc[:], uint64(rr.postingOffset))
// _, err = w.Write(enc[:m])
// if err != nil {
// return nil, err
// }
// }
// return w.Bytes(), nil
// }
//
// func recordsDecode(buf []byte) (records, error) {
// var bucket records
// bytesRead := 0
//
// next := func() (uint64, error) {
// val, i := binary.Uvarint(buf)
// if i <= 0 {
// return 0, fmt.Errorf("error decoding value: %d", i)
// }
// buf = buf[i:]
// bytesRead += i
// return val, nil
// }
//
// for len(buf) > 0 {
// key, err := next()
// if err != nil {
// return nil, err
// }
//
// postingOffset, err := next()
// if err != nil {
// return nil, err
// }
//
// bucket = append(bucket, record{key: ngram(key), postingOffset: uint32(postingOffset)})
// }
//
// return bucket, nil
// }

// TODO: store bucket index
// TODO: reanme postingIndexStart to postingIndex
type node struct {
	keys     []ngram
	children []node

	leaf bool
	// bucketOffset is set when we read the shard from disk.
	bucketOffset      uint32
	postingIndexStart uint64
	bucket            []ngram
}

// TODO: this should be split into 2 methods, 1 for leaf and 1 more inner nodes.
func (n *node) write(w io.Writer) error {
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

	if len(n.keys) == 0 {
		binary.BigEndian.PutUint64(buf[:], uint64(n.bucketOffset))
		_, err := w.Write(buf[:])
		if err != nil {
			return err
		}

		binary.BigEndian.PutUint64(buf[:], uint64(n.postingIndexStart))
		_, err = w.Write(buf[:])
		if err != nil {
			return err
		}
	}

	return nil
}

func readNode(reader *btreeReader) (*node, error) {
	var n node
	nKeys, err := reader.u64()
	if err != nil {
		return nil, err
	}

	// Leaf
	if nKeys == 0 {
		bo64, err := reader.u64()
		if err != nil {
			return nil, err
		}
		n.bucketOffset = uint32(bo64)

		n.postingIndexStart, err = reader.u64()
		if err != nil {
			return nil, err
		}

		n.leaf = true
		return &n, nil
	}

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
		n.children = append(n.children, *child)
	}

	return &n, nil
}

// // recordngram is a tuple of an ngram and the byte offset of the associated posting
// // list.
// type recordngram struct {
// key           ngram
// postingOffset uint32
// }

func (n *node) insert(ng ngram, bucketSize int, v int) {
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
			if ng >= n.keys[i] {
				i++
			}
		}
		n.children[i].insert(ng, bucketSize, v)
	}

	if n.leaf {
		// See invariant maintained by insertAt.
		n.bucket = append(n.bucket, ng)

		// Insert in ascending order. This is efficient in case we already deal with
		// sorted inputs.
		for i := len(n.bucket) - 1; i > 0; i-- {
			if n.bucket[i-1] < n.bucket[i] {
				break
			}
			n.bucket[i], n.bucket[i-1] = n.bucket[i-1], n.bucket[i]
		}
	} else {
		for i, k := range n.keys {
			if ng < k {
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
	return node{leaf: true, bucket: append(make([]ngram, 0, bucketSize), n.bucket[:bucketSize/2]...)},
		node{leaf: true, bucket: append(make([]ngram, 0, bucketSize), n.bucket[bucketSize/2:]...)},
		n.bucket[bucketSize/2],
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

func (bt *btree) String() string {
	s := ""
	s += fmt.Sprintf("{v=%d,bucketSize=%d}", bt.v, bt.bucketSize)
	bt.root.visit(func(n *node) {
		if n.leaf {
			return
		}
		s += fmt.Sprintf("[")
		for _, key := range n.keys {
			s += fmt.Sprintf("%d,", key)
		}
		s = s[:len(s)-1] // remove coma
		s += fmt.Sprintf("]")
	})
	return s
}
