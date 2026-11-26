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

func TestIndexArgFollowsSymlinkedRoot(t *testing.T) {
	sourceDir := t.TempDir()
	indexDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "repo")
	if err := os.Symlink(sourceDir, link); err != nil {
		t.Fatal(err)
	}

	opts := index.Options{
		IndexDir:     indexDir,
		DisableCTags: true,
	}
	opts.SetDefaults()
	if err := indexArg(link, opts, nil); err != nil {
		t.Fatal(err)
	}

	searcher, err := search.NewDirectorySearcher(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	defer searcher.Close()

	result, err := searcher.Search(context.Background(), &query.Const{Value: true}, &zoekt.SearchOptions{Whole: true})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range result.Files {
		got = append(got, f.Repository+":"+f.FileName+":"+string(f.Content))
	}
	if want := []string{"repo:file.txt:needle\n"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("indexed %q, want %q", got, want)
	}
}

func TestIndexArgInputFailurePreservesIndex(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read files without read permission")
	}
	sourceDir := t.TempDir()
	indexDir := t.TempDir()
	writeFiles := func(content string) {
		for _, name := range []string{"a.txt", "b.txt"} {
			if err := os.WriteFile(filepath.Join(sourceDir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	searchFiles := func() map[string]string {
		searcher, err := search.NewDirectorySearcher(indexDir)
		if err != nil {
			t.Fatal(err)
		}
		defer searcher.Close()
		result, err := searcher.Search(context.Background(), &query.Const{Value: true}, &zoekt.SearchOptions{Whole: true})
		if err != nil {
			t.Fatal(err)
		}
		files := map[string]string{}
		for _, f := range result.Files {
			files[f.FileName] = string(f.Content)
		}
		return files
	}

	opts := index.Options{
		IndexDir:              indexDir,
		DisableCTags:          true,
		RepositoryDescription: zoekt.Repository{Name: "repo"},
	}
	opts.SetDefaults()
	writeFiles("old\n")
	if err := indexArg(sourceDir, opts, nil); err != nil {
		t.Fatal(err)
	}
	want := searchFiles()

	// a.txt is read and buffered before reading b.txt fails.
	writeFiles("new\n")
	if err := os.Chmod(filepath.Join(sourceDir, "b.txt"), 0); err != nil {
		t.Fatal(err)
	}
	if err := indexArg(sourceDir, opts, nil); err == nil {
		t.Fatal("indexArg succeeded, want read error")
	}
	if got := searchFiles(); !reflect.DeepEqual(got, want) {
		t.Fatalf("search after input failure = %v, want %v", got, want)
	}
}
