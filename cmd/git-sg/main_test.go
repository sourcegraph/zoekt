package main

import (
	"path/filepath"
	"testing"
)

func TestDo(t *testing.T) {
	dir, err := filepath.Abs("../../.git")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", dir)

	for _, envvar := range []string{"", "GIT_SG_BUFFER", "GIT_SG_FILTER", "GIT_SG_CATFILE", "GIT_SG_LSTREE"} {
		name := envvar
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			if envvar != "" {
				t.Setenv(envvar, "1")
			}
			var w countingWriter
			err = do(&w)
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
