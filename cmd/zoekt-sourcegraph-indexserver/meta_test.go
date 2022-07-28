package main

import (
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
)

func TestMergeMeta(t *testing.T) {
	dir := t.TempDir()

	repoNames := []string{"repo0", "repo1", "repo2", "repo3"}
	var repoFns []string

	for _, name := range repoNames {
		opts := build.Options{
			IndexDir: dir,
			RepositoryDescription: zoekt.Repository{
				Name: name,
				RawConfig: map[string]string{
					"public": "1",
				},
			},
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
		repoFns = append(repoFns, opts.FindAllShards()...)
	}

	// update meta on repo3 then test it changed
	opts := &build.Options{
		IndexDir: dir,
		RepositoryDescription: zoekt.Repository{
			Name: "repo3",
			RawConfig: map[string]string{
				"public": "0",
			},
		},
	}
	opts.SetDefaults()
	if err := mergeMeta(opts); err != nil {
		t.Fatal(err)
	}
	repos, _, _ := zoekt.ReadMetadataPath(repoFns[3])
	if got, want := repos[0].RawConfig["public"], "0"; got != want {
		t.Fatalf("failed to update metadata of repo3. Got public %q want %q", got, want)
	}

	// create a compound shard. Use a new indexdir to avoid the need to cleanup
	// old shards.
	dir = t.TempDir()
	tmpFn, dstFn, err := merge(t, dir, repoFns)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmpFn, dstFn); err != nil {
		t.Fatal(err)
	}

	readPublic := func() []string {
		var public []string
		repos, _, _ := zoekt.ReadMetadataPath(dstFn)
		for _, r := range repos {
			public = append(public, r.RawConfig["public"])
		}
		return public
	}

	if d := cmp.Diff([]string{"1", "1", "1", "0"}, readPublic()); d != "" {
		t.Fatalf("wanted only repo3 to be marked private:\n%s", d)
	}

	// Update a repo1 in compound shard to be private
	opts = &build.Options{
		IndexDir: dir,
		RepositoryDescription: zoekt.Repository{
			Name: "repo1",
			RawConfig: map[string]string{
				"public": "0",
			},
		},
	}
	opts.SetDefaults()
	if err := mergeMeta(opts); err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff([]string{"1", "0", "1", "0"}, readPublic()); d != "" {
		t.Fatalf("wanted only repo1 to be marked private:\n%s", d)
	}
}

func merge(t *testing.T, dstDir string, names []string) (string, string, error) {
	t.Helper()

	var files []zoekt.IndexFile
	for _, fn := range names {
		f, err := os.Open(fn)
		if err != nil {
			return "", "", err
		}
		defer f.Close()

		indexFile, err := zoekt.NewIndexFile(f)
		if err != nil {
			return "", "", err
		}
		defer indexFile.Close()

		files = append(files, indexFile)
	}

	return zoekt.Merge(dstDir, files...)
}
