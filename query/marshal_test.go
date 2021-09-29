package query

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"testing"

	"github.com/RoaringBitmap/roaring"
	"github.com/google/go-cmp/cmp"
)

// We benchmark via Gob since that allows us to compare to no custom
// marshalling.

func BenchmarkRepoBranches_Encode(b *testing.B) {
	repoBranches := genRepoBranches(5_500_000)

	// do one write to amortize away the cost of gob registration
	w := &countWriter{}
	enc := gob.NewEncoder(w)
	if err := enc.Encode(repoBranches); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.ReportMetric(float64(w.n), "bytes")

	for n := 0; n < b.N; n++ {
		if err := enc.Encode(repoBranches); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRepoBranches_Decode(b *testing.B) {
	repoBranches := genRepoBranches(5_500_000)

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(repoBranches); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for n := 0; n < b.N; n++ {
		// We need to include gob.NewDecoder cost to avoid measuring encoding.
		var repoBranches RepoBranches
		if err := gob.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&repoBranches); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRepoBranches_Marshal(t *testing.T) {
	want := genRepoBranches(1000)

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(want); err != nil {
		t.Fatal(err)
	}

	var got RepoBranches
	if err := gob.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(want, &got); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func BenchmarkBranchRepos_Encode(b *testing.B) {
	repoBranches := genBranchRepos(5_500_000)

	// do one write to amortize away the cost of gob registration
	w := &countWriter{}
	enc := gob.NewEncoder(w)
	if err := enc.Encode(repoBranches); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.ReportMetric(float64(w.n), "bytes")

	for n := 0; n < b.N; n++ {
		if err := enc.Encode(repoBranches); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBranchRepos_Decode(b *testing.B) {
	repoBranches := genBranchRepos(5_500_000)

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(repoBranches); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for n := 0; n < b.N; n++ {
		// We need to include gob.NewDecoder cost to avoid measuring encoding.
		var repoBranches RepoBranches
		if err := gob.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&repoBranches); err != nil {
			b.Fatal(err)
		}
	}
}

func TestBranchRepos_Marshal(t *testing.T) {
	want := genBranchRepos(1000)

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(want); err != nil {
		t.Fatal(err)
	}

	var got BranchRepos
	if err := gob.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&got); err != nil {
		t.Fatal(err)
	}

	tr := cmp.Transformer("", func(b *roaring.Bitmap) []uint32 { return b.ToArray() })
	if diff := cmp.Diff(want, &got, tr); diff != "" {
		t.Fatalf("mismatch IDs (-want +got):\n%s", diff)
	}
}

// Generating 5.5M repos slows down the benchmark setup time, so we cache things.
var genCache = map[string]interface{}{}

func genRepoSet(n int) *RepoSet {
	key := fmt.Sprintf("RepoSet:%d", n)
	val, ok := genCache[key]
	if ok {
		return val.(*RepoSet)
	}

	rb := genRepoBranches(n)
	set := make(map[string]bool, len(rb.Set))

	for repo := range rb.Set {
		set[repo] = true
	}

	rs := &RepoSet{Set: set}
	genCache[key] = rs
	return rs
}

func genRepoBranches(n int) *RepoBranches {
	key := fmt.Sprintf("RepoBranches:%d", n)
	val, ok := genCache[key]
	if ok {
		return val.(*RepoBranches)
	}

	genName := func(n int) string {
		bs := make([]byte, 8)
		binary.LittleEndian.PutUint64(bs, uint64(n))
		return fmt.Sprintf("%x", sha256.Sum256(bs))[:10]
	}

	repoBranches := &RepoBranches{Set: map[string][]string{}}
	orgIndex := 0
	repoIndex := 0

	for i := 0; i < n; i++ {
		org := genName(orgIndex)
		name := "github.com/" + org + "/" + genName(orgIndex*2+repoIndex)
		repoBranches.Set[name] = []string{"HEAD"}
		if repoIndex%50 == 0 {
			repoBranches.Set[name] = append(repoBranches.Set[name], "more", "branches")
		}

		if i%1000 == 0 {
			orgIndex++
			repoIndex = 0
		}

		repoIndex++
	}

	genCache[key] = repoBranches
	return repoBranches
}

func genBranchRepos(n int) *BranchRepos {
	key := fmt.Sprintf("BranchRepos:%d", n)
	val, ok := genCache[key]
	if ok {
		return val.(*BranchRepos)
	}

	set := genRepoBranches(n).Set
	br := &BranchRepos{Set: map[string]*roaring.Bitmap{}}

	id := uint32(1)
	for _, branches := range set {
		for _, branch := range branches {
			ids, ok := br.Set[branch]
			if !ok {
				ids = roaring.New()
				br.Set[branch] = ids
			}
			ids.Add(id)
		}
		id++
	}

	for _, ids := range br.Set {
		ids.RunOptimize()
	}

	genCache[key] = br

	return br
}

type countWriter struct {
	n int
}

func (w *countWriter) Write(b []byte) (int, error) {
	w.n += len(b)
	return len(b), nil
}
