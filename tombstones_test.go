package zoekt

import (
	"path/filepath"
	"testing"
)

func TestSetTombstone(t *testing.T) {
	dir := t.TempDir()

	wantRepo := "r1"
	shardPath := filepath.Join(dir, "test.zoekt")

	err := SetTombstone(shardPath, wantRepo)
	if err != nil {
		t.Fatal(err)
	}

	m, err := LoadTombstones(shardPath)
	if err != nil {
		t.Fatal(m)
	}
	if len(m) != 1 {
		t.Fatalf("wanted 1 tombstone, got %d", len(m))
	}
	if _, ok := m[wantRepo]; !ok {
		t.Fatalf("%s should have been tombstoned", wantRepo)
	}

	err = SetTombstone(shardPath, wantRepo)
	if err != nil {
		t.Fatal(err)
	}
	wantRepo2 := "r2"
	err = SetTombstone(shardPath, wantRepo2)
	if err != nil {
		t.Fatal(err)
	}

	m, err = LoadTombstones(shardPath)
	if err != nil {
		t.Fatal(m)
	}
	if len(m) != 2 {
		t.Fatalf("wanted tombstones [%s, %s], got %v", wantRepo, wantRepo2, m)
	}

	if _, ok := m[wantRepo]; !ok {
		t.Fatalf("%s should have been tombstoned", wantRepo)
	}

	if _, ok := m[wantRepo2]; !ok {
		t.Fatalf("%s should have been tombstoned", wantRepo2)
	}
}
