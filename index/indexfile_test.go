package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewIndexFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.zoekt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	indexFile, err := NewIndexFile(f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(indexFile.Close)

	if got := indexFile.Name(); got != path {
		t.Errorf("Name() = %q, want %q", got, path)
	}
	if got, err := indexFile.Size(); err != nil || got != 5 {
		t.Errorf("Size() = %d, %v; want 5, nil", got, err)
	}
	if got, err := indexFile.Read(1, 3); err != nil || string(got) != "ell" {
		t.Errorf("Read(1, 3) = %q, %v; want %q, nil", got, err, "ell")
	}
	if _, err := indexFile.Read(3, 3); err == nil {
		t.Error("Read(3, 3) unexpectedly succeeded")
	}
}
