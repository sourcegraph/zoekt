package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/search"
)

func TestIndexArgAttachesConfiguredBranches(t *testing.T) {
	sourceDir := t.TempDir()
	indexDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(sourceDir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "subdir", "file.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := index.Options{
		IndexDir:     indexDir,
		DisableCTags: true,
		RepositoryDescription: zoekt.Repository{
			Name: "repo",
			Branches: []zoekt.RepositoryBranch{{
				Name:    "main",
				Version: "0123456789abcdef0123456789abcdef01234567",
			}},
		},
	}
	opts.SetDefaults()
	if err := indexArg(sourceDir, opts, nil); err != nil {
		t.Fatal(err)
	}

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	defer searcher.Close()

	result, err := searcher.Search(
		context.Background(),
		query.NewAnd(
			&query.Substring{Pattern: "needle", Content: true},
			&query.Branch{Pattern: "main"},
		),
		&zoekt.SearchOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("main branch returned %d files, want 1", len(result.Files))
	}
	if got := result.Files[0].FileName; got != "subdir/file.txt" {
		t.Fatalf("file name = %q, want %q", got, "subdir/file.txt")
	}
	if got := result.Files[0].Branches; !reflect.DeepEqual(got, []string{"main"}) {
		t.Fatalf("branches = %v, want [main]", got)
	}

	result, err = searcher.Search(
		context.Background(),
		&query.Branch{Pattern: "release"},
		&zoekt.SearchOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 0 {
		t.Fatalf("unconfigured branch returned %d files, want 0", len(result.Files))
	}
}

func TestIndexArgIndexesSymlinkTarget(t *testing.T) {
	sourceDir := t.TempDir()
	indexDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "target.txt"), []byte("file content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(sourceDir, "link")); err != nil {
		t.Fatal(err)
	}

	opts := index.Options{
		IndexDir:     indexDir,
		DisableCTags: true,
		RepositoryDescription: zoekt.Repository{
			Name: "repo",
		},
	}
	opts.SetDefaults()
	if err := indexArg(sourceDir, opts, nil); err != nil {
		t.Fatal(err)
	}

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	defer searcher.Close()

	result, err := searcher.Search(
		context.Background(),
		&query.Substring{Pattern: "target.txt", Content: true},
		&zoekt.SearchOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("search returned %d files, want 1", len(result.Files))
	}
	if got := result.Files[0].FileName; got != "link" {
		t.Fatalf("file name = %q, want %q", got, "link")
	}
}
