package zoekt

import (
	"fmt"
	"testing"
)

func f(n *node) {
	if n.leaf {
		fmt.Println("bucket >>>>")
		for _, r := range n.bucket {
			fmt.Printf("%d, ", r.key)
		}
		fmt.Println("\n<<<< bucket")
	}
}

func TestBTree(t *testing.T) {
	bt := newBtree(2, 4)
	bt.insert(record{ngram(9), 0})
	bt.root.visit(f)
	bt.insert(record{ngram(3), 0})
	bt.root.visit(f)
	bt.insert(record{ngram(4), 0})
	bt.root.visit(f)
	bt.insert(record{ngram(2), 0})
	bt.root.visit(f)
	bt.insert(record{ngram(6), 0})
	bt.root.visit(f)
	bt.insert(record{ngram(8), 0})
	bt.root.visit(f)
	bt.insert(record{ngram(7), 0})
	bt.root.visit(f)
	bt.insert(record{ngram(5), 0})
	bt.root.visit(f)
	bt.insert(record{ngram(1), 0})
	bt.root.visit(f)
	// bt.insert(record{ngram(11), 0})
	// bt.root.visit(f)
	// bt.insert(record{ngram(13), 0})
	// bt.root.visit(f)
	// bt.insert(record{ngram(0), 0})
	// bt.root.visit(f)
	// bt.insert(record{ngram(14), 0})
	// bt.root.visit(f)
	// bt.insert(record{ngram(15), 0})
	// bt.root.visit(f)
	// bt.insert(record{ngram(10), 0})
	// bt.root.visit(f)
	// bt.insert(record{ngram(22), 0})
	// bt.root.visit(f)
	// bt.insert(record{ngram(23), 0})
	// bt.root.visit(f)
	// bt.insert(record{ngram(24), 0})
	// bt.root.visit(f)
	fmt.Println("---------------------------")
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
