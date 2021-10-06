package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
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

func TestGenerateCompoundShards(t *testing.T) {
	shards := []candidate{
		{
			path:      "r1",
			sizeBytes: 2,
		},
		{
			path:      "r2",
			sizeBytes: 3,
		},
		{
			path:      "r3",
			sizeBytes: 9,
		},
		{
			path:      "r4",
			sizeBytes: 10,
		},
		{
			path:      "r5",
			sizeBytes: 5,
		},
		{
			path:      "r6",
			sizeBytes: 1,
		},
	}

	// Expected compounds
	// compound 1: r3 + r6 (total size  10)
	// compound 2: r5 + r2 + r1 (total size 10)
	//
	// r4 -> already max size

	compounds := generateCompounds(shards, 10)

	if len(compounds) != 2 {
		t.Fatalf("expected 2 compound shards, but got %d", len(compounds))
	}

	totalShards := 0
	for _, c := range compounds {
		totalShards += len(c.shards)
	}
	if totalShards != 5 {
		t.Fatalf("shards mismatch: wanted %d, got %d", 5, totalShards)
	}

	want := []candidate{{"r3", 9}, {"r6", 1}}
	if diff := cmp.Diff(want, compounds[0].shards, cmp.Options{cmp.AllowUnexported(candidate{})}); diff != "" {
		t.Fatalf("-want,+got\n%s", diff)
	}

	want = []candidate{{"r5", 5}, {"r2", 3}, {"r1", 2}}
	if diff := cmp.Diff(want, compounds[1].shards, cmp.Options{cmp.AllowUnexported(candidate{})}); diff != "" {
		t.Fatalf("-want,+got\n%s", diff)
	}
}
