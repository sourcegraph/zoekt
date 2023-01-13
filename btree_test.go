package zoekt

import (
	"fmt"
	"testing"
)

func TestBTree(t *testing.T) {
	bt := newBtree(2, 2)
	fmt.Printf("root: %p\n", bt.root)
	bt.insert(record{ngram(1), 0})
	bt.insert(record{ngram(2), 0})
	bt.insert(record{ngram(3), 0})
	bt.insert(record{ngram(4), 0})
	fmt.Println("---------------------------")
	fmt.Println(bt.String())
}
