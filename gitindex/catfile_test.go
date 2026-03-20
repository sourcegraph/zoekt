package gitindex

import (
	"io"
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

func TestCatfileReader(t *testing.T) {
	repoDir, blobs := createTestRepo(t)

	ids := []plumbing.Hash{
		blobs["hello.txt"],
		blobs["empty.txt"],
		blobs["binary.bin"],
		blobs["large.bin"],
	}

	cr, err := newCatfileReader(repoDir, ids)
	if err != nil {
		t.Fatalf("newCatfileReader: %v", err)
	}
	defer cr.Close()

	// hello.txt
	size, missing, err := cr.Next()
	if err != nil {
		t.Fatalf("Next hello.txt: %v", err)
	}
	if missing {
		t.Fatal("hello.txt unexpectedly missing")
	}
	if size != 12 {
		t.Errorf("hello.txt size = %d, want 12", size)
	}
	content := make([]byte, size)
	if _, err := io.ReadFull(cr, content); err != nil {
		t.Fatalf("ReadFull hello.txt: %v", err)
	}
	if string(content) != "hello world\n" {
		t.Errorf("hello.txt content = %q", content)
	}

	// empty.txt
	size, missing, err = cr.Next()
	if err != nil {
		t.Fatalf("Next empty.txt: %v", err)
	}
	if size != 0 {
		t.Errorf("empty.txt size = %d, want 0", size)
	}

	// binary.bin — read content and verify binary data survives.
	size, missing, err = cr.Next()
	if err != nil {
		t.Fatalf("Next binary.bin: %v", err)
	}
	binContent := make([]byte, size)
	if _, err := io.ReadFull(cr, binContent); err != nil {
		t.Fatalf("ReadFull binary.bin: %v", err)
	}
	if binContent[0] != 0x00 || binContent[3] != '\n' {
		t.Errorf("binary.bin unexpected leading bytes: %x", binContent[:5])
	}

	// large.bin
	size, missing, err = cr.Next()
	if err != nil {
		t.Fatalf("Next large.bin: %v", err)
	}
	if size != 64*1024 {
		t.Errorf("large.bin size = %d, want %d", size, 64*1024)
	}
	largeContent := make([]byte, size)
	if _, err := io.ReadFull(cr, largeContent); err != nil {
		t.Fatalf("ReadFull large.bin: %v", err)
	}

	// EOF after all entries.
	_, _, err = cr.Next()
	if err != io.EOF {
		t.Errorf("expected io.EOF after last entry, got %v", err)
	}
}

func TestCatfileReader_Skip(t *testing.T) {
	repoDir, blobs := createTestRepo(t)

	ids := []plumbing.Hash{
		blobs["hello.txt"],
		blobs["large.bin"],
		blobs["binary.bin"],
	}

	cr, err := newCatfileReader(repoDir, ids)
	if err != nil {
		t.Fatalf("newCatfileReader: %v", err)
	}
	defer cr.Close()

	// Skip hello.txt by calling Next again without reading.
	_, _, err = cr.Next()
	if err != nil {
		t.Fatalf("Next hello.txt: %v", err)
	}

	// Skip large.bin too.
	size, _, err := cr.Next()
	if err != nil {
		t.Fatalf("Next large.bin: %v", err)
	}
	if size != 64*1024 {
		t.Errorf("large.bin size = %d, want %d", size, 64*1024)
	}

	// Read binary.bin after skipping two entries.
	size, _, err = cr.Next()
	if err != nil {
		t.Fatalf("Next binary.bin: %v", err)
	}
	content := make([]byte, size)
	if _, err := io.ReadFull(cr, content); err != nil {
		t.Fatalf("ReadFull binary.bin: %v", err)
	}
	if content[0] != 0x00 {
		t.Errorf("binary.bin first byte = %x, want 0x00", content[0])
	}
}

func TestCatfileReader_Missing(t *testing.T) {
	repoDir, blobs := createTestRepo(t)

	fakeHash := plumbing.NewHash("deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	ids := []plumbing.Hash{
		blobs["hello.txt"],
		fakeHash,
		blobs["empty.txt"],
	}

	cr, err := newCatfileReader(repoDir, ids)
	if err != nil {
		t.Fatalf("newCatfileReader: %v", err)
	}
	defer cr.Close()

	// hello.txt — read normally.
	size, missing, err := cr.Next()
	if err != nil || missing {
		t.Fatalf("Next hello.txt: err=%v missing=%v", err, missing)
	}
	content := make([]byte, size)
	if _, err := io.ReadFull(cr, content); err != nil {
		t.Fatalf("ReadFull hello.txt: %v", err)
	}
	if string(content) != "hello world\n" {
		t.Errorf("hello.txt = %q", content)
	}

	// fakeHash — missing.
	_, missing, err = cr.Next()
	if err != nil {
		t.Fatalf("Next fakeHash: %v", err)
	}
	if !missing {
		t.Error("expected fakeHash to be missing")
	}

	// empty.txt — still works after missing entry.
	size, missing, err = cr.Next()
	if err != nil || missing {
		t.Fatalf("Next empty.txt: err=%v missing=%v", err, missing)
	}
	if size != 0 {
		t.Errorf("empty.txt size = %d, want 0", size)
	}
}
