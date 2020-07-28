package query

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// We benchmark via Gob since that allows us to compare to no custom
// marshalling.

func BenchmarkRepoBranches_Encode(b *testing.B) {
	repoBranches := genRepoBranches()

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
	repoBranches := genRepoBranches()

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
	want := genRepoBranches()

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

func genRepoBranches() *RepoBranches {
	genName := func(n int) string {
		bs := make([]byte, 8)
		binary.LittleEndian.PutUint64(bs, uint64(n))
		return fmt.Sprintf("%x", sha256.Sum256(bs))[:10]
	}

	repoBranches := &RepoBranches{Set: map[string][]string{}}
	for i := 0; i < 100; i++ {
		org := genName(i)
		for j := 0; j < 100; j++ {
			name := "github.com/" + org + "/" + genName(i*2+j)
			repoBranches.Set[name] = []string{"HEAD"}
			if j%50 == 0 {
				repoBranches.Set[name] = append(repoBranches.Set[name], "more", "branches")
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
