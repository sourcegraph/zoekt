package main

import (
	"io"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/go-cmp/cmp"
)

func TestInfo(t *testing.T) {
	p, err := startGitCatFileBatch("")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	info, err := p.InfoString("HEAD")
	if err != nil {
		t.Fatal(err)
	}

	t.Log(info.Hash, info.Type, info.Size)

	// Test that we can recover from missing
	if info, err := p.InfoString("sdflkjsdfDoesNOTexist"); !isMissingError(err) {
		t.Fatalf("expected missing error got info=%v err=%v", info, err)
	}

	// Now lets fetch the object again via hash and see if it stays the same.
	info2, err := p.Info(info.Hash)
	if err != nil {
		t.Fatal(err)
	}

	if d := cmp.Diff(info, info2); d != "" {
		t.Fatalf("info changed (-first, +second):\n%s", d)
	}

	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestContents(t *testing.T) {
	p, err := startGitCatFileBatch("")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	info, err := p.ContentsString("HEAD")
	if err != nil {
		t.Fatal(err)
	}

	t.Log(info.Hash, info.Type, info.Size)

	b, err := io.ReadAll(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(b))

	if len(b) != int(info.Size) {
		t.Fatalf("amount read (%d) is different to object size (%d)", len(b), info.Size)
	}
	if info.Type != plumbing.CommitObject {
		t.Fatalf("expected HEAD to be a commit, got %s", info.Type)
	}

	// Test that we can recover from missing
	if info, err := p.ContentsString("sdflkjsdfDoesNOTexist"); !isMissingError(err) {
		t.Fatalf("expected missing error got info=%v err=%v", info, err)
	}

	// Now lets fetch the object again via hash and see if it stays the same.
	info2, err := p.Contents(info.Hash)
	if err != nil {
		t.Fatal(err)
	}

	if d := cmp.Diff(info, info2); d != "" {
		t.Fatalf("info changed (-first, +second):\n%s", d)
	}

	b2, err := io.ReadAll(p)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(b, b2); d != "" {
		t.Fatalf("content changed (-first, +second):\n%s", d)
	}

	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkInfo(b *testing.B) {
	p, err := startGitCatFileBatch("")
	if err != nil {
		b.Fatal(err)
	}
	defer p.Close()

	info, err := p.InfoString("HEAD")
	if err != nil {
		b.Fatal(err)
	}

	for i := 0; i < b.N; i++ {
		_, err := p.Info(info.Hash)
		if err != nil {
			b.Fatal(err)
		}
	}

	if err := p.Close(); err != nil {
		b.Fatal(err)
	}
}
