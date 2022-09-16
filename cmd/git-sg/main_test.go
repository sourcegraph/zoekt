package main

import "testing"

func TestDo(t *testing.T) {
	err := do()
	if err != nil {
		t.Fatal(err)
	}
}
