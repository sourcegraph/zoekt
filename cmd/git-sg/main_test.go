package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDo(t *testing.T) {
	setGitDir(t)

	for _, envvar := range []string{"", "GIT_SG_BUFFER", "GIT_SG_FILTER", "GIT_SG_CATFILE", "GIT_SG_LSTREE", "GIT_SG_GITOBJ"} {
		name := envvar
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			if envvar != "" {
				t.Setenv(envvar, "1")
			}
			var w countingWriter
			err := do(&w)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("wrote %d bytes", w.N)
			if w.N == 0 {
				t.Fatal("wrote no bytes")
			}
		})
	}
}

type countingWriter struct {
	N int
}

func (w *countingWriter) Write(b []byte) (int, error) {
	w.N += len(b)
	return len(b), nil
}

func setGitDir(t *testing.T) {
	t.Helper()

	dir, err := filepath.Abs("../../.git")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", dir)

	if _, err := os.Stat(dir); os.Getenv("CI") != "" && os.IsNotExist(err) {
		t.Skipf("skipping since on CI and this is not a git checkout: %v", err)
	}
}
