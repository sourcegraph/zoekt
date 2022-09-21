package main

import "testing"

func TestInfo(t *testing.T) {
	p, err := startGitCatFileBatch("")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	info, err := p.Info("HEAD")
	if err != nil {
		t.Fatal(err)
	}

	t.Log(info.Hash, info.Type, info.Size)

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

	for i := 0; i < b.N; i++ {
		_, err := p.Info("HEAD")
		if err != nil {
			b.Fatal(err)
		}
	}

	if err := p.Close(); err != nil {
		b.Fatal(err)
	}
}
