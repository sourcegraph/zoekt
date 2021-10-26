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
			path:      "r2",
			sizeBytes: 2,
		},
		{
			path:      "r3",
			sizeBytes: 3,
		},
		{
			path:      "r8",
			sizeBytes: 8,
		},
		{
			path:      "r9",
			sizeBytes: 9,
		},
		{
			path:      "r5",
			sizeBytes: 5,
		},
		{
			path:      "r1",
			sizeBytes: 1,
		},
	}

	// Expected compounds
	// compound 0: r1 + r5 + r3 (total size 9)
	// compound 1: r9 (total size  9)
	// compound 3: r8 + r2 (total size  10)

	compounds := generateCompounds(shards, 10)

	wantCompounds := 3
	if len(compounds) != wantCompounds {
		t.Fatalf("expected %d compound shards, but got %d", wantCompounds, len(compounds))
	}

	wantTotalShards := 6
	totalShards := 0
	for _, c := range compounds {
		totalShards += len(c.shards)
	}
	if totalShards != wantTotalShards {
		t.Fatalf("shards mismatch: wanted %d, got %d", wantTotalShards, totalShards)
	}

	want := []candidate{{"r1", 1}, {"r5", 5}, {"r3", 3}}
	if diff := cmp.Diff(want, compounds[0].shards, cmp.Options{cmp.AllowUnexported(candidate{})}); diff != "" {
		t.Fatalf("-want,+got\n%s", diff)
	}

	want = []candidate{{"r9", 9}}
	if diff := cmp.Diff(want, compounds[1].shards, cmp.Options{cmp.AllowUnexported(candidate{})}); diff != "" {
		t.Fatalf("-want,+got\n%s", diff)
	}

	want = []candidate{{"r8", 8}, {"r2", 2}}
	if diff := cmp.Diff(want, compounds[2].shards, cmp.Options{cmp.AllowUnexported(candidate{})}); diff != "" {
		t.Fatalf("-want,+got\n%s", diff)
	}

}
