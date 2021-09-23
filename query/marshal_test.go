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
	b.Run("Set", benchmarkRepoBranchesEncode(genRepoBranches(false)))
	b.Run("IDs", benchmarkRepoBranchesEncode(genRepoBranches(true)))
}

func benchmarkRepoBranchesEncode(repoBranches *RepoBranches) func(*testing.B) {
	return func(b *testing.B) {
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
}

func BenchmarkRepoBranches_Decode(b *testing.B) {
	b.Run("Set", benchmarkRepoBranchesDecode(genRepoBranches(false)))
	b.Run("IDs", benchmarkRepoBranchesDecode(genRepoBranches(true)))
}

func benchmarkRepoBranchesDecode(repoBranches *RepoBranches) func(*testing.B) {
	return func(b *testing.B) {
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
}

func TestRepoBranches_Marshal(t *testing.T) {
	want := genRepoBranches(false)

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(want); err != nil {
		t.Fatal(err)
	}

	var got RepoBranches
	if err := gob.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(want.Set, got.Set); diff != "" {
		t.Fatalf("mismatch Set (-want +got):\n%s", diff)
	}
}

func TestRepoBranches_MarshalIDs(t *testing.T) {
	want := genRepoBranches(true)

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(want); err != nil {
		t.Fatal(err)
	}

	var got RepoBranches
	if err := gob.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&got); err != nil {
		t.Fatal(err)
	}

	wantIDs := make(map[string][]uint32, len(want.IDs))
	for branch, ids := range want.IDs {
		wantIDs[branch] = ids.ToArray()
	}

	gotIDs := make(map[string][]uint32, len(got.IDs))
	for branch, ids := range got.IDs {
		gotIDs[branch] = ids.ToArray()
	}

	if diff := cmp.Diff(wantIDs, gotIDs); diff != "" {
		t.Fatalf("mismatch IDs (-want +got):\n%s", diff)
	}
}

func genRepoBranches(ids bool) *RepoBranches {
	genName := func(n int) string {
		bs := make([]byte, 8)
		binary.LittleEndian.PutUint64(bs, uint64(n))
		return fmt.Sprintf("%x", sha256.Sum256(bs))[:10]
	}

	repoBranches := &RepoBranches{
		Set: map[string][]string{},
		IDs: map[string]*roaring.Bitmap{},
	}

	id := uint32(1)
	for i := 0; i < 100; i++ {
		org := genName(i)
		for j := 0; j < 100; j++ {
			name := "github.com/" + org + "/" + genName(i*2+j)
			branches := []string{"HEAD"}
			if j%50 == 0 {
				branches = append(branches, "more", "branches")
			}

			if !ids {
				repoBranches.Set[name] = branches
				continue
			}

			for _, branch := range branches {
				ids, ok := repoBranches.IDs[branch]
				if !ok {
					ids = roaring.New()
					repoBranches.IDs[branch] = ids
				}
				ids.Add(id)
				id++
			}
		}
	}

	return repoBranches
}

type countWriter struct {
	n int
}

func (w *countWriter) Write(b []byte) (int, error) {
	w.n += len(b)
	return len(b), nil
}
