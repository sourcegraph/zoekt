package main

import (
	"io"
	"path/filepath"
	"testing"
)

func TestDo(t *testing.T) {
	dir, err := filepath.Abs("../../.git")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", dir)
	err = do(io.Discard)
	if err != nil {
		t.Fatal(err)
	}
}
