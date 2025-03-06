package main

import (
	"crypto/sha1"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/internal/tenant/tenanttest"
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
	opts := index.Options{
		IndexDir:              dir,
		RepositoryDescription: zoekt.Repository{Name: "test-repo"},
	}
	opts.SetDefaults()
	b, err := index.NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	if err := b.AddFile("F", []byte(strings.Repeat("abc", 100))); err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	if err := b.Finish(); err != nil {
		t.Errorf("Finish: %v", err)
	}

	s := &Server{IndexDir: dir, mergeOpts: mergeOpts{targetSizeBytes: 2000 * 1024 * 1024}}
	s.merge(helperCallMerge)

	_, err = os.Stat(filepath.Join(dir, "test-repo_v16.00000.zoekt"))
	if err != nil {
		t.Fatal(err)
	}
}

func helperCallMerge(s ...string) *exec.Cmd {
	cs := []string{"-test.run=TestCallMerge", "--"}
	cs = append(cs, s...)
	env := []string{
		"GO_TEST_WANT_CALL_MERGE=1",
	}
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = append(env, os.Environ()...)
	return cmd
}

func TestCallMerge(t *testing.T) {
	if os.Getenv("GO_TEST_WANT_CALL_MERGE") != "1" {
		return
	}
	defer os.Exit(0)

	args := os.Args
	for len(args) > 0 {
		if args[0] == "--" {
			args = args[1:]
			break
		}
		args = args[1:]
	}

	// We mock the merge process by deleting the input shards and creating an empty
	// compound shard with a proper name.
	h := sha1.New()
	for _, a := range args {
		h.Write([]byte(filepath.Base(a)))
		h.Write([]byte{0})
		_ = os.Remove(a)
	}

	compoundShardName := filepath.Join(filepath.Dir(args[1]), fmt.Sprintf("compound-%x_v%d.%05d.zoekt", h.Sum(nil), 17, 0))
	f, _ := os.Create(compoundShardName)
	_ = f.Close()

	// Just like zoekt-merge-index, we write the name of the compound shard to
	// stdout.
	_, _ = fmt.Fprint(os.Stdout, compoundShardName)
}

func TestMerge(t *testing.T) {
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

			s := &Server{
				IndexDir:  dir,
				mergeOpts: mergeOpts{targetSizeBytes: tc.targetSizeBytes},
			}

			s.merge(helperCallMerge)

			checkCount(dir, "compound-*", tc.wantCompound)
			checkCount(dir, "*_v16.00000.zoekt", tc.wantSimple)
		})
	}
}

func TestExplodeTenantCompoundShards(t *testing.T) {
	tenanttest.MockEnforce(t)
	dir := t.TempDir()
	s := &Server{IndexDir: dir}

	// Create two compound shards:
	// 1. One with repos from tenant 1 and 2
	// 2. One with repos from tenant 2 and 3
	cs1 := createCompoundShard(t, dir, []uint32{1, 2}, func(in *zoekt.Repository) {
		if in.ID == 1 {
			in.TenantID = 1
		} else {
			in.TenantID = 2
		}
	})

	cs2 := createCompoundShard(t, dir, []uint32{3, 4}, func(in *zoekt.Repository) {
		if in.ID == 3 {
			in.TenantID = 2
		} else {
			in.TenantID = 3
		}
	})

	// Create context with tenant 1
	ctx := tenanttest.NewTestContext()

	// Explode shards for tenant 1
	err := s.explodeTenantCompoundShards(ctx, func(path string) error {
		// For this test we call explode directly instead of calling it in a
		// separate process.
		return index.Explode(dir, path)
	})
	require.NoError(t, err)

	// Check that only cs1 was exploded (since it contained a repo from tenant
	// 1) and cs2 remains untouched
	require.NoFileExists(t, cs1)
	require.FileExists(t, cs2)

	// Check that we have 2 simple shards (from cs1) and 1 compound shard (cs2)
	simpleShards, err := filepath.Glob(filepath.Join(dir, "*_v16.00000.zoekt"))
	require.NoError(t, err)
	require.Len(t, simpleShards, 2, "expected 2 simple shards")

	// check that the simple shards are from tenant 1 and 2
	for _, shard := range simpleShards {
		repos, _, err := index.ReadMetadataPath(shard)
		require.NoError(t, err)
		for _, repo := range repos {
			require.Contains(t, []int{1, 2}, repo.TenantID, "expected tenant 1 or 2, but got %d", repo.TenantID)
		}
	}

	compoundShards, err := filepath.Glob(filepath.Join(dir, "compound-*"))
	require.NoError(t, err)
	require.Len(t, compoundShards, 1, "expected 1 compound shard")
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
