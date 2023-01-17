package zoekt

import (
	"bytes"
	"fmt"
	"testing"
)

func TestBTree(t *testing.T) {
	bt := newBtree(2, 2)
	insertMany(t, bt, []ngram{6, 2, 4, 3, 9, 8, 7, 5, 1})
	// inner nodes only
	//
	//       8      (root)
	//     /   \
	//  3,4,6   9   (lvl 1)
	//
	want := "{v=2,bucketSize=2}[8][3,4,6][9]"
	if s := bt.Print(); s != want {
		t.Fatalf("want %s, got %s", want, s)
	}

}

func TestSerialization(t *testing.T) {
	bt := newBtree(2, 2)
	insertMany(t, bt, []ngram{6, 2, 4, 3, 9, 8, 7, 5, 1})

	var buf bytes.Buffer

	if err := bt.write(&buf); err != nil {
		t.Fatal(err)
	}

	bt2, err := readBtree(&buf)
	if err != nil {
		t.Fatal(err)
	}

	if bt.Print() != bt2.Print() {
		t.Fatalf("(in) %s != (out) %s", bt.Print(), bt2.Print())
	}
}

func insertMany(t *testing.T, bt *btree, ngrams []ngram) {
	t.Helper()
	for _, ng := range ngrams {
		bt.insert(record{ng, 0})
	}
}

func (bt *btree) Print() string {
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
