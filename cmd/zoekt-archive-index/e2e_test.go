package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
	"github.com/google/zoekt/ignore"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/shards"
)

func writeArchive(w io.Writer, format string, files map[string]string) (err error) {
	if format == "zip" {
		zw := zip.NewWriter(w)
		for name, body := range files {
			f, err := zw.Create(name)
			if err != nil {
				return err
			}
			if _, err := f.Write([]byte(body)); err != nil {
				return err
			}
		}
		return zw.Close()
	}

	if format == "tgz" {
		gw := gzip.NewWriter(w)
		defer func() {
			err2 := gw.Close()
			if err == nil {
				err = err2
			}
		}()
		w = gw
		format = "tar"
	}

	if format != "tar" {
		return errors.New("expected tar")
	}

	tw := tar.NewWriter(w)

	for name, body := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0600,
			Size: int64(len(body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}

	return nil
}

// TestIndexArg tests zoekt-archive-index by creating an archive and then
// indexing and executing searches and checking we get expected results.
// Additionally, we test that the index is properly updated with the
// -incremental=true option changing the options between indexes and ensuring
// the results change as expected.
func TestIndexIncrementally(t *testing.T) {
	for _, format := range []string{"tar", "tgz", "zip"} {
		t.Run(format, func(t *testing.T) {
			testIndexIncrementally(t, format)
		})
	}
}

func testIndexIncrementally(t *testing.T, format string) {
	indexdir, err := ioutil.TempDir("", "TestIndexArg-index")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}
	defer func() {
		advanceFS()
		os.RemoveAll(indexdir)
	}()
	archive, err := ioutil.TempFile("", "TestIndexArg-archive")
	if err != nil {
		t.Fatalf("TempFile: %v", err)
	}
	defer os.Remove(archive.Name())

	fileSize := 1000

	files := map[string]string{}
	for i := 0; i < 4; i++ {
		s := fmt.Sprintf("%d", i)
		files["F"+s] = strings.Repeat("a", fileSize)
	}

	err = writeArchive(archive, format, files)
	if err != nil {
		t.Fatalf("unable to create archive %v", err)
	}
	archive.Close()

	// tests contain options used to build an index and the expected number of
	// files in the result set based on the options.
	tests := []struct {
		largeFiles   []string
		wantNumFiles int
	}{
		{
			largeFiles:   []string{},
			wantNumFiles: 0,
		},
		{
			largeFiles:   []string{"F0", "F2"},
			wantNumFiles: 2,
		},
	}

	for _, test := range tests {
		largeFiles, wantNumFiles := test.largeFiles, test.wantNumFiles

		bopts := build.Options{
			SizeMax:    fileSize - 1,
			IndexDir:   indexdir,
			LargeFiles: largeFiles,
		}
		opts := Options{
			Incremental: true,
			Archive:     archive.Name(),
			Name:        "repo",
			Branch:      "master",
			Commit:      "cccccccccccccccccccccccccccccccccccccccc",
			Strip:       0,
		}

		if err := do(opts, bopts); err != nil {
			t.Fatalf("error creating index: %v", err)
		}

		ss, err := shards.NewDirectorySearcher(indexdir)
		if err != nil {
			t.Fatalf("NewDirectorySearcher(%s): %v", indexdir, err)
		}
		defer ss.Close()

		q, err := query.Parse("aaa")
		if err != nil {
			t.Fatalf("Parse(aaa): %v", err)
		}

		var sOpts zoekt.SearchOptions
		result, err := ss.Search(context.Background(), q, &sOpts)
		if err != nil {
			t.Fatalf("Search(%v): %v", q, err)
		}

		if len(result.Files) != wantNumFiles {
			t.Errorf("got %v, want %d files.", result.Files, wantNumFiles)
		}
	}
}

type ignoreTest struct {
	ignorePattern string
	wantFiles     int
}

func TestIgnoreArchive(t *testing.T) {
	tests := []ignoreTest{
		{
			ignorePattern: "D0",
			wantFiles:     5,
		},
		{
			ignorePattern: "D3",
			wantFiles:     3,
		},
		{
			ignorePattern: "/D3",
			wantFiles:     3,
		},
		{
			ignorePattern: "/D3/",
			wantFiles:     3,
		},
		{
			ignorePattern: "D3/",
			wantFiles:     3,
		},
		{
			ignorePattern: "D30",
			wantFiles:     6,
		},
		{
			ignorePattern: "D1\n # D3",
			wantFiles:     5,
		},
		{
			ignorePattern: "D1\nD3",
			wantFiles:     2,
		},
		{
			ignorePattern: "D1\n\nD3",
			wantFiles:     2,
		},
		{
			ignorePattern: "D0\nD1\nD2\nD3",
			wantFiles:     0,
		},
	}
	for _, format := range []string{"tar", "tgz", "zip"} {
		t.Run(format, func(t *testing.T) {
			for _, tt := range tests {
				testIgnore(t, format, tt)
			}
		})
	}
}

func testIgnore(t *testing.T, format string, test ignoreTest) {
	fileSize := 10

	indexdir, err := ioutil.TempDir("", "ignore_index")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}
	defer func() {
		advanceFS()
		os.RemoveAll(indexdir)
	}()

	archive, err := ioutil.TempFile("", "ignore_archive")
	if err != nil {
		t.Fatalf("TempFile: %v", err)
	}
	defer os.Remove(archive.Name())

	content := strings.Repeat("a", fileSize)
	files := map[string]string{
		"D0/F0":          content,
		"D1/F1":          content,
		"D2/F2":          content,
		"D3/F3.1":        content,
		"D3/F3.2":        content,
		"D3/D3.1/F3.1.1": content,
	}
	files[ignore.IgnoreFile] = test.ignorePattern
	err = writeArchive(archive, format, files)
	if err != nil {
		t.Fatalf("unable to create archive %v", err)
	}
	archive.Close()

	bopts := build.Options{
		IndexDir: indexdir,
	}
	opts := Options{
		Incremental: true,
		Archive:     archive.Name(),
		Name:        "repo",
		Branch:      "master",
		Commit:      "cccccccccccccccccccccccccccccccccccccccc",
		Strip:       0,
	}

	if err := do(opts, bopts); err != nil {
		t.Fatalf("error creating index: %v", err)
	}

	q, err := query.Parse("aaa")
	if err != nil {
		t.Fatalf("Parse(aaa): %v", err)
	}

	ss, err := shards.NewDirectorySearcher(indexdir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %v", indexdir, err)
	}
	defer ss.Close()

	var sOpts zoekt.SearchOptions
	result, err := ss.Search(context.Background(), q, &sOpts)
	if err != nil {
		t.Fatalf("Search(%v): %v", q, err)
	}

	if len(result.Files) != test.wantFiles {
		t.Errorf("got %v, want %d files.", result.Files, test.wantFiles)
	}
}

func advanceFS() {
	time.Sleep(10 * time.Millisecond)
}
