package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/search"
)

func TestCheckDuplicateShardPrefixes(t *testing.T) {
	t.Setenv("WORKSPACES_API_URL", "")

	tests := []struct {
		name    string
		args    []string
		opts    index.Options
		wantErr string
	}{
		{
			name: "shared repository name",
			args: []string{"alpha", "beta"},
			opts: index.Options{RepositoryDescription: zoekt.Repository{
				Name: "repo",
			}},
			wantErr: `both use shard prefix "repo"`,
		},
		{
			name:    "shared directory basename",
			args:    []string{"alpha/src", "beta/src"},
			wantErr: `both use shard prefix "src"`,
		},
		{
			name: "shared prefix override",
			args: []string{"alpha", "beta"},
			opts: index.Options{
				ShardPrefixOverride: "shared",
			},
			wantErr: `both use shard prefix "shared"`,
		},
		{
			name: "distinct directory basenames",
			args: []string{"alpha", "beta"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := checkDuplicateShardPrefixes(test.args, test.opts)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("checkDuplicateShardPrefixes() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("checkDuplicateShardPrefixes() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestIndexArgAttachesConfiguredBranches(t *testing.T) {
	sourceDir := t.TempDir()
	indexDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("needle\n"), 0o644); err != nil {
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
