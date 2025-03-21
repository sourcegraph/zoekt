package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/shards"
	"github.com/sourcegraph/zoekt/query"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	flag.Parse()
	if !testing.Verbose() {
		log.SetOutput(io.Discard)
	}
	os.Exit(m.Run())
}

var modTime = time.Date(2024, 9, 26, 0, 0, 0, 0, time.UTC)

func writeArchive(w io.Writer, format string, files map[string]string) (err error) {
	if format == "zip" {
		zw := zip.NewWriter(w)
		for name, body := range files {
			header := &zip.FileHeader{
				Name:     name,
				Method:   zip.Deflate,
				Modified: modTime,
			}
			f, err := zw.CreateHeader(header)
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
			Name:    name,
			Mode:    0o600,
			Size:    int64(len(body)),
			ModTime: modTime,
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
	indexDir := t.TempDir()

	archive, err := os.CreateTemp("", "TestIndexArg-archive")
	if err != nil {
		t.Fatalf("TempFile: %v", err)
	}
	defer os.Remove(archive.Name())

	fileSize := 1000

	files := map[string]string{}
	for i := range 4 {
		s := fmt.Sprintf("%d", i)
		files["F"+s] = strings.Repeat("a", fileSize)
		files["!F"+s] = strings.Repeat("a", fileSize)
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
		{
			largeFiles:   []string{"F?", "!F2"},
			wantNumFiles: 3,
		},
		{
			largeFiles:   []string{"F?", "!F2", "\\!F0"},
			wantNumFiles: 4,
		},
		{
			largeFiles:   []string{"F?", "!F2", "\\!F0", "F2"},
			wantNumFiles: 5,
		},
	}

	for _, test := range tests {
		largeFiles, wantNumFiles := test.largeFiles, test.wantNumFiles

		bopts := index.Options{
			SizeMax:    fileSize - 1,
			IndexDir:   indexDir,
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

		if err := Index(opts, bopts); err != nil {
			t.Fatalf("error creating index: %v", err)
		}

		ss, err := shards.NewDirectorySearcher(indexDir)
		if err != nil {
			t.Fatalf("NewDirectorySearcher(%s): %v", indexDir, err)
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

// TestLatestCommitDate tests that the latest commit date is set correctly if
// the mod time of the files has been set during the archive creation.
func TestLatestCommitDate(t *testing.T) {
	for _, format := range []string{"tar", "tgz", "zip"} {
		t.Run(format, func(t *testing.T) {
			testLatestCommitDate(t, format)
		})
	}
}

func testLatestCommitDate(t *testing.T, format string) {
	// Create an archive
	archive, err := os.CreateTemp("", "TestLatestCommitDate")
	require.NoError(t, err)
	defer os.Remove(archive.Name())

	fileSize := 10
	files := map[string]string{}
	for i := range 4 {
		s := fmt.Sprintf("%d", i)
		files["F"+s] = strings.Repeat("a", fileSize)
		files["!F"+s] = strings.Repeat("a", fileSize)
	}

	err = writeArchive(archive, format, files)
	if err != nil {
		t.Fatalf("unable to create archive %v", err)
	}
	archive.Close()

	// Index
	indexDir := t.TempDir()
	bopts := index.Options{
		IndexDir: indexDir,
	}
	opts := Options{
		Archive: archive.Name(),
		Name:    "repo",
		Branch:  "master",
		Commit:  "cccccccccccccccccccccccccccccccccccccccc",
	}

	err = Index(opts, bopts)
	require.NoError(t, err)

	// Read the metadata of the index we just created and check the latest commit date.
	f, err := os.Open(indexDir)
	require.NoError(t, err)

	indexFiles, err := f.Readdirnames(1)
	require.Len(t, indexFiles, 1)

	repos, _, err := index.ReadMetadataPath(filepath.Join(indexDir, indexFiles[0]))
	require.NoError(t, err)
	require.Len(t, repos, 1)
	require.True(t, repos[0].LatestCommitDate.Equal(modTime))
}
