package zoekt

type btree struct {
	root *node
	// all inner nodes, except root, have [v, 2v] keys.
	v int
	// the max number of values attached to a leaf node
	bucketSize int
}

func newBtree(v, bucketSize int) *btree {
	return &btree{&node{leaf: true}, v, bucketSize}
}

func (bt *btree) insert(r record) {
	if leftNode, rightNode, newKey, ok := maybeSplit(bt.root, bt.v, bt.bucketSize); ok {
		bt.root = &node{keys: []ngram{newKey}, children: []node{leftNode, rightNode}}
	}

	bt.root.insert(r, bt.bucketSize, bt.v)
}

type node struct {
	keys     []ngram
	children []node

	leaf   bool
	bucket []record
}

// record is a tuple of an ngram and the byte offset of the associated posting
// list.
type record struct {
	key    ngram
	offset uint32
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
	return node{leaf: true, bucket: append([]record{}, n.bucket[:bucketSize/2]...)},
		node{leaf: true, bucket: append([]record{}, n.bucket[bucketSize/2:]...)},
		n.bucket[bucketSize/2].key,
		true
}

func maybeSplitInner(n *node, v int) (left node, right node, newKey ngram, ok bool) {
	if len(n.keys) < 2*v {
		return
	}
	return node{keys: append([]ngram{}, n.keys[0:v]...), children: append([]node{}, n.children[:v+1]...)},
		node{keys: append([]ngram{}, n.keys[v+1:]...), children: append([]node{}, n.children[v+1:]...)},
		n.keys[v],
		true
}
