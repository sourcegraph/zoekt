package main

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"testing/quick"
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
func genTestCompounds(t *testing.T, n uint8, targetSize int64) ([]compound, int64) {
	t.Helper()

	candidates := make([]candidate, 0, n)
	var totalSize int64
	var i uint8
	for i = 0; i < n; i++ {
		thisSize := rand.Int63n(targetSize) + 1
		candidates = append(candidates, candidate{"", thisSize})
		totalSize += thisSize
	}

	compounds := generateCompounds(candidates, targetSize)
	return compounds, totalSize
}

func TestCompoundsContainAllShards(t *testing.T) {
	// n is uint8 to keep the slices reasonably small and the tests performant.
	f := func(n uint8) bool {
		compounds, wantTotalSize := genTestCompounds(t, n, 10)
		shardCount := 0
		var gotTotalSize int64
		for _, c := range compounds {
			shardCount += len(c.shards)
			gotTotalSize += c.size
		}
		return shardCount == int(n) && gotTotalSize == wantTotalSize
	}

	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}

func TestFewerCompoundsThanShards(t *testing.T) {
	f := func(n uint8) bool {
		compounds, _ := genTestCompounds(t, n, 10)

		shardCount := 0
		for _, c := range compounds {
			shardCount += len(c.shards)
		}
		return shardCount >= len(compounds)
	}

	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCompoundsHaveSizeBelowTargetSize(t *testing.T) {
	f := func(n uint8, targetSize int64) bool {
		if targetSize <= 0 {
			return true
		}

		compounds, _ := genTestCompounds(t, n, targetSize)
		for _, c := range compounds {
			if c.size > targetSize {
				return false
			}
		}
		return true
	}

	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}
