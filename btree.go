package zoekt

type btree struct {
	root       *node
	v          int
	bucketSize int
}

func newBtree(v, bucketSize int) *btree {
	return &btree{&node{leaf: true}, v, bucketSize}
}

func (bt *btree) insert(r record) {
	if leftNode, rightNode, newKey, ok := split(bt.root, bt.v, bt.bucketSize); ok {
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

type record struct {
	key    ngram
	offset uint32
}

func (n *node) insert(r record, bucketSize int, v int) {
	maybeSplitAndInsert := func(i int) {
		if n.maybeSplit(i, v, bucketSize) && r.key >= n.keys[i] {
			i++
		}
		n.children[i].insert(r, bucketSize, v)
	}

	if n.leaf {
		// We rely on the invariant that buckets always have a space free to insert.
		n.bucket = append(n.bucket, r)

		// Insert in ascending order.
		for i := len(n.bucket) - 1; i > 0; i-- {
			if n.bucket[i-1].key < n.bucket[i].key {
				break
			}
			n.bucket[i], n.bucket[i-1] = n.bucket[i-1], n.bucket[i]
		}
	} else {
		for i, k := range n.keys {
			if r.key < k {
				maybeSplitAndInsert(i)
				return
			}
		}
		maybeSplitAndInsert(len(n.children) - 1)
	}
}

func split(n *node, v, bucketSize int) (left node, right node, newKey ngram, ok bool) {
	if n.leaf {
		if len(n.bucket) == bucketSize {
			ok = true
			left = node{leaf: true, bucket: append([]record{}, n.bucket[:bucketSize/2]...)}
			right = node{leaf: true, bucket: append([]record{}, n.bucket[bucketSize/2:]...)}
			newKey = right.bucket[0].key
		}
	} else {
		if len(n.keys) == 2*v {
			ok = true
			left = node{keys: append([]ngram{}, n.keys[0:v]...), children: append([]node{}, n.children[:v+1]...)}
			right = node{keys: append([]ngram{}, n.keys[v+1:]...), children: append([]node{}, n.children[v+1:]...)}
			newKey = n.keys[v]
		}
	}

	return
}

func (n *node) maybeSplit(i int, v int, bucketSize int) bool {
	leftNode, rightNode, newKey, ok := split(&n.children[i], v, bucketSize)

	if !ok {
		return false
	}

	n.children = append(append([]node{}, n.children[0:i]...), append([]node{leftNode, rightNode}, n.children[i+1:]...)...)
	n.keys = append(append([]ngram{}, n.keys[0:i]...), append([]ngram{newKey}, n.keys[i:]...)...)
	return true
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
