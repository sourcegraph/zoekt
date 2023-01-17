package zoekt

import (
	"bytes"
	"fmt"
	"testing"
)

func insertMany(t *testing.T, bt *btree, ngrams []ngram) {
	t.Helper()
	for _, ng := range ngrams {
		bt.insert(record{ng, 0})
	}
}

func (bt *btree) Print() string {
	s := ""
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

func TestBTree(t *testing.T) {
	f := func(n *node) {
		if n.leaf {
			t.Log("bucket >>>>")
			for _, r := range n.bucket {
				t.Logf("%d, ", r.key)
			}
			t.Logf("\n<<<< bucket")
		}
	}

	bt := newBtree(2, 4)
	insertMany(t, bt, []ngram{9, 3, 4, 2, 6, 8, 7, 5, 1})
	bt.root.visit(f)
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

	//
	//       8
	//     /   \
	//  3,4,6   9
	//
	want := "[8][3,4,6][9]"
	if s := bt2.Print(); s != want {
		t.Fatalf("want %s, got %s", want, s)
	}

	if bt.Print() != bt2.Print() {
		t.Fatalf("(in) %s != (out) %s", bt.Print(), bt2.Print())
	}
}
