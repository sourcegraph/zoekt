package main

import (
	"path/filepath"
	"sort"
	"testing"
)

func TestMerge(t *testing.T) {
	shards, err := filepath.Glob("../../testdata/shards/*_v16.*.zoekt")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(shards)
	t.Log(shards)

	err = merge(t.TempDir(), shards)
	if err != nil {
		t.Fatal(err)
	}
}
