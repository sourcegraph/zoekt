package zoekt

import (
	"fmt"
)

type node interface {
	insert(r record, bucketSize int, v int)
	String() string
}

type inner struct {
	// offset uint32
	parent   *inner
	keys     []ngram
	children []node
}

func (l *inner) String() string {
	s := ""
	s += fmt.Sprintln("INNER")
	if l.parent == nil {
		s += fmt.Sprintf("This is the ROOT node\n")
	} else {
		s += fmt.Sprintf("parent: %p\n", l.parent)
	}
	s += fmt.Sprintf("KEYS: ")
	for _, k := range l.keys {
		s += fmt.Sprintf("%d, ", k)
	}
	s += fmt.Sprintf("\n")

	for _, c := range l.children {
		s += c.String()
	}

	return s
}

func (in *inner) insert(r record, bucketSize int, v int) {
	fmt.Printf("inner.insert: %+v, %d, %d\n", r, bucketSize, v)
	if len(in.keys) == 0 {
		fmt.Printf("empty root node\n")
		in.keys = []ngram{r.key}
		in.children = []node{&leaf{in, []record{r}}}
		return
	}

	inserted := false
	var i = 0
	for i, k := range in.keys {
		if r.key < k {
			inserted = true
			if in.maybeSplit(i, v, bucketSize) {
				switch {
				case r.key < in.keys[i]:
				default:
					i++
				}
			}
			in.children[i].insert(r, bucketSize, v)
		}
	}

	if !inserted {
		if in.maybeSplit(len(in.children)-1, v, bucketSize) {
			switch {
			case r.key < in.keys[len(in.keys)-1]:
			default:
				i++
			}
		}
		in.children[i].insert(r, bucketSize, v)
	}
}

func (in *inner) maybeSplit(i int, v int, bucketSize int) bool {
	fmt.Printf("inner.maybeSplit: %d %d, %d\n", i, v, bucketSize)
	var leftNode, rightNode node
	var newKey ngram
	switch c := in.children[i].(type) {
	case *inner:
		fmt.Printf("splitting inner\n")
		if len(c.children) == 2*v {
			leftNode = &inner{in, c.keys[0:v], c.children[0 : v+1]}
			rightNode = &inner{in, c.keys[v+1:], c.children[v+1:]}
			newKey = c.keys[v+1]
		}
	case *leaf:
		fmt.Printf("splitting leaf\n")
		if len(c.bucket) == bucketSize {
			leftNode = &leaf{in, c.bucket[0 : bucketSize/2]}
			rightNode = &leaf{in, c.bucket[bucketSize/2:]}
			newKey = c.bucket[bucketSize/2].key
		}
	default:
		panic("this should never happen")
	}

	if leftNode == nil {
		return false
	}

	fmt.Printf("New key: %d\n", newKey)
	in.children = append(in.children[0:i], append([]node{leftNode, rightNode}, in.children[i+1:]...)...)
	in.keys = append(in.keys[0:i], append([]ngram{newKey}, in.keys[i:]...)...)
	return true
}

type record struct {
	key    ngram
	offset uint32
}

type leaf struct {
	// offset uint32
	parent *inner
	bucket []record
}

func (l *leaf) node() {}

func (l *leaf) insert(r record, bucketSize int, v int) {
	fmt.Printf("leaf.insert %+v, %d, %d\n", r, bucketSize, v)
	if len(l.bucket) == bucketSize {
		parent := &inner{}
		parent.children = []node{&leaf{parent, l.bucket[0 : bucketSize/2]}, &leaf{parent, l.bucket[bucketSize/2:]}}
		parent.keys = []ngram{l.bucket[bucketSize/2].key}
	}
	l.bucket = append(l.bucket, r)
}

func (l *leaf) String() string {
	s := ""
	s += fmt.Sprintf("LEAF\n")
	s += fmt.Sprintf("parent: %p\n", l.parent)
	s += fmt.Sprintf("BUCKET: ")
	for _, r := range l.bucket {
		s += fmt.Sprintf("%d, ", r.key)
	}
	s += fmt.Sprintf("\n")
	return s
}

type btree struct {
	root       node
	v          int
	bucketSize int
}

func (bt *btree) insert(r record) {
	fmt.Printf("btree.insert: %+v\n", r)
	bt.root.insert(r, bt.bucketSize, bt.v)
}

func newBtree(v, bucketSize int) *btree {
	return &btree{&leaf{}, v, bucketSize}
}

func (bt *btree) String() string {
	return bt.root.String()
}
