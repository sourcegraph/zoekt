package zoekt

import "fmt"

type btree struct {
	root       *node
	v          int
	bucketSize int
}

type record struct {
	key    ngram
	offset uint32
}

type node struct {
	keys     []ngram
	children []node

	leaf   bool
	bucket []record
}

func (n *node) insert(r record, bucketSize int, v int) {
	if n.leaf {
		fmt.Println("leaf")
		// TODO could be a somple append
		n.bucket = append(append([]record{}, n.bucket...), r)
		i := len(n.bucket) - 1
		for i > 0 && n.bucket[i].key < n.bucket[i-1].key {
			n.bucket[i], n.bucket[i-1] = n.bucket[i-1], n.bucket[i]
			i--
		}
		// if len(n.bucket) > bucketSize {
		// 	n.leaf = false
		// 	n.children = []node{
		// 		{bucket: append([]record{}, n.bucket[:bucketSize/2]...), leaf: true},
		// 		{bucket: append([]record{}, n.bucket[bucketSize/2:]...), leaf: true},
		// 	}
		//
		// 	n.keys = []ngram{n.bucket[bucketSize/2].key}
		// 	n.bucket = nil
		// }
	} else {
		fmt.Println("inner")
		inserted := false
		var i = 0
		for i, k := range n.keys {
			if r.key < k {
				if n.maybeSplit(i, v, bucketSize) {
					switch {
					case r.key < n.keys[i]:
					default:
						i++
					}
				}
				n.children[i].insert(r, bucketSize, v)
				inserted = true
				break
			}
		}

		if !inserted {
			fmt.Println("not inserted")
			i = len(n.children) - 1
			if n.maybeSplit(len(n.children)-1, v, bucketSize) {
				switch {
				case r.key < n.keys[len(n.keys)-1]:
				default:
					i++
				}
			}
			n.children[i].insert(r, bucketSize, v)
		}
	}
}

func split(n *node, v, bucketSize int) (leftNode node, rightNode node, newKey ngram, ok bool) {
	if n.leaf && len(n.bucket) == bucketSize {
		ok = true
		fmt.Println("split leaf")
		leftNode = node{leaf: true, bucket: append([]record{}, n.bucket[:bucketSize/2]...)}
		rightNode = node{leaf: true, bucket: append([]record{}, n.bucket[bucketSize/2:]...)}
		newKey = rightNode.bucket[0].key
	} else if len(n.keys) == 2*v {
		ok = true
		fmt.Println("split inner node")
		leftNode = node{keys: append([]ngram{}, n.keys[0:v]...), children: append([]node{}, n.children[:v+1]...)}
		rightNode = node{keys: append([]ngram{}, n.keys[v+1:]...), children: append([]node{}, n.children[v+1:]...)}
		newKey = n.keys[v]
	}
	return
}

func (n *node) maybeSplit(i int, v int, bucketSize int) bool {
	fmt.Println("maybe split")
	leftNode, rightNode, newKey, ok := split(&n.children[i], v, bucketSize)

	if !ok {
		return false
	}

	n.children = append(append([]node{}, n.children[0:i]...), append([]node{leftNode, rightNode}, n.children[i+1:]...)...)
	n.keys = append(append([]ngram{}, n.keys[0:i]...), append([]ngram{newKey}, n.keys[i:]...)...)
	return true
}

func (bt *btree) insert(r record) {
	fmt.Printf("INSERT %d\n", r.key)
	leftNode, rightNode, newKey, ok := split(bt.root, bt.v, bt.bucketSize)
	if ok {
		bt.root = &node{keys: []ngram{newKey}, children: []node{leftNode, rightNode}}
	}

	bt.root.insert(r, bt.bucketSize, bt.v)
}

func newBtree(v, bucketSize int) *btree {
	return &btree{&node{leaf: true}, v, bucketSize}
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
