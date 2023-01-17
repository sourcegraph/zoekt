package zoekt

import (
	"bytes"
	"reflect"
	"sort"
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
	if s := bt.String(); s != want {
		t.Fatalf("want %s, got %s", want, s)
	}
}

func TestBTree_hw(t *testing.T) {
	bt := newBtree(2, 2)
	text := []byte("hello world\n")
	var ngrams ngramSlice

	for i := 0; i < len(text)-2; i++ {
		ngrams = append(ngrams, bytesToNGram(text[i:i+3]))
	}

	sort.Sort(ngrams)
	insertMany(t, bt, ngrams)

	want := "{v=2,bucketSize=2}[474989232914442,488183229841527][444202924114028,457397048967276][474989249691759,474989255983136][488183401807980,501377528758372]"
	if s := bt.String(); s != want {
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

	if bt.String() != bt2.String() {
		t.Fatalf("(in) %s != (out) %s", bt.String(), bt2.String())
	}
}

func TestRecordsEncodeDecode(t *testing.T) {
	var r records
	r = append(r, record{key: 1, postingOffset: 2})
	r = append(r, record{key: 3, postingOffset: 4})

	b, err := r.encode()
	if err != nil {
		t.Fatal(err)
	}

	r2, err := recordsDecode(b)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(r, r2) {
		t.Fatalf("%+v!=%+v\n", r, r2)
	}
}

func insertMany(t *testing.T, bt *btree, ngrams []ngram) {
	t.Helper()
	for _, ng := range ngrams {
		bt.insert(record{ng, 0})
	}
}
