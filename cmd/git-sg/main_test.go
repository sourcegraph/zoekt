package main

import (
	"io"
	"testing"
)

func TestDo(t *testing.T) {
	err := do(io.Discard)
	if err != nil {
		t.Fatal(err)
	}
}
