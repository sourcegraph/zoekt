package main

import (
	"io"
	"testing"
)

func TestDo(t *testing.T) {
	t.Setenv("GIT_DIR", "../../.git")
	err := do(io.Discard)
	if err != nil {
		t.Fatal(err)
	}
}
