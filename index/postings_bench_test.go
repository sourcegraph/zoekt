package index

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// Set ZOEKT_BENCH_REPO to a source tree (e.g. a kubernetes checkout) to enable.
//
//	git clone --depth=1 https://github.com/kubernetes/kubernetes /tmp/k8s
//	ZOEKT_BENCH_REPO=/tmp/k8s go test ./index/ -bench=BenchmarkPostings -benchmem -count=5 -timeout=600s

func requireBenchRepo(b *testing.B) string {
	b.Helper()
	dir := os.Getenv("ZOEKT_BENCH_REPO")
	if dir == "" {
		b.Skip("ZOEKT_BENCH_REPO not set")
	}
	return dir
}

// loadRepoFiles walks dir and returns file contents, skipping binary files,
// empty files, and anything over 1 MB. Returns at most maxFiles entries.
func loadRepoFiles(b *testing.B, dir string, maxFiles int) [][]byte {
	b.Helper()
	var files [][]byte
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if len(files) >= maxFiles {
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil || info.Size() == 0 || info.Size() > 1<<20 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return nil // binary
		}
		files = append(files, data)
		return nil
	})
	if err != nil {
		b.Fatalf("walking repo: %v", err)
	}
	if len(files) == 0 {
		b.Fatal("no files found in repo")
	}
	return files
}

func totalSize(files [][]byte) int64 {
	var n int64
	for _, f := range files {
		n += int64(len(f))
	}
	return n
}

// BenchmarkPostings_NewSearchableString measures the core hot path: trigram
// extraction, map lookups, delta encoding, and per-trigram slice growth.
// Sub-benchmarks vary corpus size to show scaling with map size.
func BenchmarkPostings_NewSearchableString(b *testing.B) {
	dir := requireBenchRepo(b)
	allFiles := loadRepoFiles(b, dir, 50_000)
	b.Logf("loaded %d files, %.1f MB", len(allFiles), float64(totalSize(allFiles))/(1<<20))

	for _, n := range []int{1_000, 5_000, len(allFiles)} {
		n = min(n, len(allFiles))
		files := allFiles[:n]
		size := totalSize(files)

		b.Run(fmt.Sprintf("files=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				pb := newPostingsBuilder(defaultShardMax)
				for _, data := range files {
					_, _, _ = pb.newSearchableString(data, nil)
				}
			}
			b.ReportMetric(float64(size), "input-bytes/op")
		})
	}
}

// BenchmarkPostings_Reuse measures the warm path: building postings with a
// reset (pooled) postingsBuilder that retains its map and slice allocations
// from a previous shard build.
func BenchmarkPostings_Reuse(b *testing.B) {
	dir := requireBenchRepo(b)
	allFiles := loadRepoFiles(b, dir, 50_000)
	size := totalSize(allFiles)
	b.Logf("loaded %d files, %.1f MB", len(allFiles), float64(size)/(1<<20))

	// Warm up the builder so it has allocated map entries and slices.
	pb := newPostingsBuilder(defaultShardMax)
	for _, data := range allFiles {
		_, _, _ = pb.newSearchableString(data, nil)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		pb.reset()
		for _, data := range allFiles {
			_, _, _ = pb.newSearchableString(data, nil)
		}
	}
	b.ReportMetric(float64(size), "input-bytes/op")
}

// BenchmarkPostings_WritePostings measures the marshaling path: sorting ngram
// keys and writing varint-encoded posting lists.
func BenchmarkPostings_WritePostings(b *testing.B) {
	dir := requireBenchRepo(b)
	allFiles := loadRepoFiles(b, dir, 50_000)

	pb := newPostingsBuilder(defaultShardMax)
	for _, data := range allFiles {
		_, _, _ = pb.newSearchableString(data, nil)
	}
	b.Logf("built %d unique ngrams from %d files, %.1f MB", len(pb.postings), len(allFiles), float64(totalSize(allFiles))/(1<<20))

	buf := &bytes.Buffer{}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		buf.Reset()
		w := &writer{w: buf}
		var ngramText, charOffsets, endRunes simpleSection
		var postings compoundSection
		writePostings(w, pb, &ngramText, &charOffsets, &postings, &endRunes)
	}
}
