package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
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

func TestDoNotDeleteSingleShards(t *testing.T) {
	dir := t.TempDir()

	// Create a test shard.
	opts := build.Options{
		IndexDir:              dir,
		RepositoryDescription: zoekt.Repository{Name: "test-repo"},
	}
	opts.SetDefaults()
	b, err := build.NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	if err := b.AddFile("F", []byte(strings.Repeat("abc", 100))); err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	if err := b.Finish(); err != nil {
		t.Errorf("Finish: %v", err)
	}

	s := &Server{IndexDir: dir, TargetSizeBytes: 2000 * 1024 * 1024}
	s.doMerge()

	_, err = os.Stat(filepath.Join(dir, "test-repo_v16.00000.zoekt"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestDoMerge(t *testing.T) {

	// A fixed set of shards gives us reliable shard sizes which makes it easy to
	// define a cutoff with targetSizeBytes.
	m := []string{
		"../../testdata/shards/repo_v16.00000.zoekt",
		"../../testdata/shards/repo2_v16.00000.zoekt",
		"../../testdata/shards/ctagsrepo_v16.00000.zoekt",
	}

	testCases := []struct {
		name            string
		targetSizeBytes int64
		wantCompound    int
		wantSimple      int
	}{
		{
			name:            "3 shards",
			targetSizeBytes: 6 * 1024,
			wantCompound:    1,
			wantSimple:      0,
		},
		{
			name:            "2 shards",
			targetSizeBytes: 4 * 1024,
			wantCompound:    1,
			wantSimple:      1,
		},
		{
			// This is a pathological case where the target size of a compound shard is
			// smaller than the size of a simple shard. In realistic scenarios,
			// targetSizeBytes should be 100x or more of a typical shard size.
			name:            "target size too small",
			targetSizeBytes: 2 * 1024,
			wantCompound:    0,
			wantSimple:      3,
		},
		{
			name:            "target size too big",
			targetSizeBytes: 10 * 1024,
			wantCompound:    0,
			wantSimple:      3,
		},
		{
			name:            "target size 0",
			targetSizeBytes: 0,
			wantCompound:    0,
			wantSimple:      3,
		},
	}

	checkCount := func(dir string, pattern string, want int) {
		have, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			t.Fatal(err)
		}
		if len(have) != want {
			t.Fatalf("want %d, have %d", want, len(have))
		}
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			_, err := copyTestShards(dir, m)
			if err != nil {
				t.Fatal(err)
			}

			s := &Server{IndexDir: dir, TargetSizeBytes: tc.targetSizeBytes}
			s.doMerge()

			checkCount(dir, "compound-*", tc.wantCompound)
			checkCount(dir, "*_v16.00000.zoekt", tc.wantSimple)
		})
	}

}

func copyTestShards(dstDir string, srcShards []string) ([]string, error) {
	var tmpShards []string
	for _, s := range srcShards {
		dst := filepath.Join(dstDir, filepath.Base(s))
		tmpShards = append(tmpShards, dst)
		if err := copyFile(s, dst); err != nil {
			return nil, err
		}
	}
	return tmpShards, nil
}

func copyFile(src, dst string) (err error) {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()

	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(d, s); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}
