package main

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/quick"

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
)

func TestHasMultipleShards(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		file                  string
		wantHasMultipleShards bool
	}{
		{"large.00000.zoekt", true},
		{"large.00001.zoekt", true},
		{"small.00000.zoekt", false},
		{"compound-foo.00000.zoekt", false},
		{"else", false},
	}

	for _, c := range cases {
		_, err := os.Create(filepath.Join(dir, c.file))
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, tt := range cases {
		t.Run(tt.file, func(t *testing.T) {
			if got := hasMultipleShards(filepath.Join(dir, tt.file)); got != tt.wantHasMultipleShards {
				t.Fatalf("want %t, got %t", tt.wantHasMultipleShards, got)
			}
		})
	}
}

// genTestCompounds is a helper that generates compounds from n shards with sizes
// in (0, targetSize].
func genTestCompounds(t *testing.T, n uint8, targetSize int64) ([]compound, []candidate, int64) {
	t.Helper()

	candidates := make([]candidate, 0, n)
	var totalSize int64
	var i uint8
	for i = 0; i < n; i++ {
		thisSize := rand.Int63n(targetSize) + 1
		candidates = append(candidates, candidate{"", thisSize})
		totalSize += thisSize
	}

	compounds, excluded := generateCompounds(candidates, targetSize)
	return compounds, excluded, totalSize
}

func TestEitherMergedOrExcluded(t *testing.T) {
	// n is uint8 to keep the slices reasonably small and the tests performant.
	f := func(n uint8) bool {
		compounds, excluded, wantTotalSize := genTestCompounds(t, n, 10)
		shardCount := len(excluded)
		var gotTotalSize int64
		for _, c := range compounds {
			shardCount += len(c.shards)
			gotTotalSize += c.size
		}
		for _, c := range excluded {
			gotTotalSize += c.sizeBytes
		}
		if shardCount != int(n) {
			t.Logf("shards: want %d, got %d", int(n), shardCount)
			return false
		}
		if gotTotalSize != wantTotalSize {
			t.Logf("total size: want %d, got %d", wantTotalSize, gotTotalSize)
			return false
		}
		return true
	}

	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCompoundsHaveSizeAboveTargetSize(t *testing.T) {
	f := func(n uint8, targetSize int64) bool {
		if targetSize <= 0 {
			return true
		}

		compounds, _, _ := genTestCompounds(t, n, targetSize)
		for _, c := range compounds {
			if c.size < targetSize {
				return false
			}
		}
		return true
	}

	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}

func TestDoNotDeleteSingleShards(t *testing.T) {
	dir := t.TempDir()

	// Create a test shard.
	opts := build.Options{
		IndexDir:              dir,
		RepositoryDescription: zoekt.Repository{Name: "test-repo"},
	}
	opts.SetDefaults()
	b, err := build.NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	if err := b.AddFile("F", []byte(strings.Repeat("abc", 100))); err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	if err := b.Finish(); err != nil {
		t.Errorf("Finish: %v", err)
	}

	err = doMerge(dir, 2000*1024*1024, 1800*1024*1024, false)
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(dir, "test-repo_v16.00000.zoekt"))
	if err != nil {
		t.Fatal(err)
	}
}
