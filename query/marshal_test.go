package query

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"hash/fnv"
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

func BenchmarkRepoBranchesIDs_Encode(b *testing.B) {
	repoBranches := genRepoBranchesIDs(5_500_000)

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

func BenchmarkRepoBranchesIDs_Decode(b *testing.B) {
	repoBranches := genRepoBranchesIDs(5_500_000)

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

func TestRepoBranchesIDs_Marshal(t *testing.T) {
	want := genRepoBranchesIDs(1000)

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(want); err != nil {
		t.Fatal(err)
	}

	var got RepoBranches
	if err := gob.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&got); err != nil {
		t.Fatal(err)
	}

	tr := cmp.Transformer("", func(b *roaring.Bitmap) []uint32 { return b.ToArray() })
	if diff := cmp.Diff(want, &got, tr); diff != "" {
		t.Fatalf("mismatch IDs (-want +got):\n%s", diff)
	}
}

// Generating 5.5M repos slows down the benchmark setup time, so we cache things.
var (
	repoBranchesCache    = map[int]map[string][]string{}
	repoBranchesIDsCache = map[int]map[string]*roaring.Bitmap{}
)

func genRepoBranches(n int) *RepoBranches {
	set, ok := repoBranchesCache[n]
	if ok {
		return &RepoBranches{Set: set}
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

	repoBranchesCache[n] = repoBranches.Set

	return repoBranches
}

func genRepoBranchesIDs(n int) *RepoBranches {
	branchIDs, ok := repoBranchesIDsCache[n]
	if ok {
		return &RepoBranches{IDs: branchIDs}
	}

	set := genRepoBranches(n).Set
	branchIDs = map[string]*roaring.Bitmap{}

	for repo, branches := range set {
		for _, branch := range branches {
			ids, ok := branchIDs[branch]
			if !ok {
				ids = roaring.New()
				branchIDs[branch] = ids
			}
			ids.Add(hash(repo))
		}
	}

	for _, ids := range branchIDs {
		ids.RunOptimize()
	}

	return &RepoBranches{IDs: branchIDs}
}

func hash(name string) uint32 {
	h := fnv.New32()
	h.Write([]byte(name))
	return h.Sum32()
}

type countWriter struct {
	n int
}

func (w *countWriter) Write(b []byte) (int, error) {
	w.n += len(b)
	return len(b), nil
}
