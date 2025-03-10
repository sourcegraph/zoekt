package zoekt

import (
	"bytes"
	"encoding/gob"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func BenchmarkRepoList_Encode(b *testing.B) {
	set := genRepoList(1000)

	// do one write to amortize away the cost of gob registration
	w := &countWriter{}
	enc := gob.NewEncoder(w)
	if err := enc.Encode(set); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.ReportMetric(float64(w.n), "bytes")

	for n := 0; n < b.N; n++ {
		if err := enc.Encode(set); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRepoList_Decode(b *testing.B) {
	set := genRepoList(1000)

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(set); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for n := 0; n < b.N; n++ {
		// We need to include gob.NewDecoder cost to avoid measuring encoding.
		var repoBranches RepoList
		if err := gob.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&repoBranches); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRepoList_Marshal(t *testing.T) {
	for i := range []int{0, 1, 10, 100} {
		want := genRepoList(i)

		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(want); err != nil {
			t.Fatal(err)
		}

		var got RepoList
		if err := gob.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&got); err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff(want, &got); diff != "" {
			t.Fatalf("mismatch for reposmap size %d (-want +got):\n%s", i, diff)
		}
	}
}

func genRepoList(size int) *RepoList {
	m := make(ReposMap, size)
	indexTime := time.Now().Unix()
	for i := range size {
		m[uint32(i)] = MinimalRepoListEntry{
			HasSymbols:    true,
			IndexTimeUnix: indexTime,
			Branches: []RepositoryBranch{{
				Name:    "HEAD",
				Version: "c301e5c82b6e1632dce5c39902691c359559852e",
			}},
		}
	}
	return &RepoList{ReposMap: m}
}

type countWriter struct {
	n int
}

func (w *countWriter) Write(b []byte) (int, error) {
	w.n += len(b)
	return len(b), nil
}
