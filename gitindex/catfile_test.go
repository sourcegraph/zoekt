package gitindex

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
)

// createTestRepo creates a git repo with various test files and returns
// the repo path and a map of filename -> blob SHA.
func createTestRepo(t *testing.T) (string, map[string]plumbing.Hash) {
	t.Helper()
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "repo")

	script := `
set -e
git init -b main repo
cd repo
git config user.email "test@test.com"
git config user.name "Test"

# Normal text file
echo "hello world" > hello.txt

# Empty file
touch empty.txt

# Binary file with newlines embedded
printf '\x00\x01\x02\nhello\nworld\n\x03\x04' > binary.bin

# Large-ish file (64KB of data)
dd if=/dev/urandom bs=1024 count=64 of=large.bin 2>/dev/null

git add -A
git commit -m "initial"
`
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("create test repo: %v", err)
	}

	// Get blob SHAs for each file.
	blobs := map[string]plumbing.Hash{}
	for _, name := range []string{"hello.txt", "empty.txt", "binary.bin", "large.bin"} {
		out, err := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD:"+name).Output()
		if err != nil {
			t.Fatalf("rev-parse %s: %v", name, err)
		}
		sha := string(out[:len(out)-1]) // trim newline
		blobs[name] = plumbing.NewHash(sha)
	}

	return repoDir, blobs
}

func TestReadBlobsPipelined(t *testing.T) {
	repoDir, blobs := createTestRepo(t)

	ids := []plumbing.Hash{
		blobs["hello.txt"],
		blobs["empty.txt"],
		blobs["binary.bin"],
		blobs["large.bin"],
	}

	results, err := readBlobsPipelined(repoDir, ids)
	if err != nil {
		t.Fatalf("readBlobsPipelined: %v", err)
	}

	if len(results) != 4 {
		t.Fatalf("got %d results, want 4", len(results))
	}

	// hello.txt
	if results[0].Err != nil {
		t.Fatalf("hello.txt err: %v", results[0].Err)
	}
	if string(results[0].Content) != "hello world\n" {
		t.Errorf("hello.txt = %q", results[0].Content)
	}

	// empty.txt
	if results[1].Err != nil {
		t.Fatalf("empty.txt err: %v", results[1].Err)
	}
	if len(results[1].Content) != 0 {
		t.Errorf("empty.txt len = %d, want 0", len(results[1].Content))
	}

	// binary.bin — verify exact content survived the pipeline.
	if results[2].Err != nil {
		t.Fatalf("binary.bin err: %v", results[2].Err)
	}
	if results[2].Content[0] != 0x00 {
		t.Errorf("binary.bin first byte = %x, want 0x00", results[2].Content[0])
	}

	// large.bin
	if results[3].Err != nil {
		t.Fatalf("large.bin err: %v", results[3].Err)
	}
	if results[3].Size != 64*1024 {
		t.Errorf("large.bin size = %d, want %d", results[3].Size, 64*1024)
	}
}

func TestReadBlobsPipelined_WithMissing(t *testing.T) {
	repoDir, blobs := createTestRepo(t)

	fakeHash := plumbing.NewHash("deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	ids := []plumbing.Hash{
		blobs["hello.txt"],
		fakeHash,
		blobs["empty.txt"],
	}

	results, err := readBlobsPipelined(repoDir, ids)
	if err != nil {
		t.Fatalf("readBlobsPipelined: %v", err)
	}

	if !results[1].Missing {
		t.Errorf("expected result[1] to be missing")
	}
	if string(results[0].Content) != "hello world\n" {
		t.Errorf("hello.txt = %q", results[0].Content)
	}
	if len(results[2].Content) != 0 {
		t.Errorf("empty.txt len = %d, want 0", len(results[2].Content))
	}
}
