package gitindex

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
)

// --- Close lifecycle tests ---

// TestCatfileReader_DoubleClose verifies that Close is idempotent.
// Calling Close twice must not deadlock or panic.
func TestCatfileReader_DoubleClose(t *testing.T) {
	t.Parallel()

	repoDir, blobs := createTestRepo(t)
	ids := []plumbing.Hash{blobs["hello.txt"]}

	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Consume the entry so the process can exit cleanly.
	if _, _, _, err := cr.Next(); err != nil {
		t.Fatal(err)
	}

	if err := cr.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Second Close must not deadlock or panic.
	done := make(chan error, 1)
	go func() {
		done <- cr.Close()
	}()

	select {
	case <-done:
		// Success — whether err is nil or not, it didn't block.
	case <-time.After(5 * time.Second):
		t.Fatal("second Close() deadlocked — writeErr channel was never closed")
	}
}

// TestCatfileReader_ConcurrentClose verifies that calling Close from
// multiple goroutines simultaneously does not panic, deadlock, or
// corrupt state.
func TestCatfileReader_ConcurrentClose(t *testing.T) {
	t.Parallel()

	repoDir, blobs := createTestRepo(t)
	ids := []plumbing.Hash{
		blobs["hello.txt"],
		blobs["large.bin"],
		blobs["binary.bin"],
	}

	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Read one entry, leave two unconsumed.
	if _, _, _, err := cr.Next(); err != nil {
		t.Fatal(err)
	}

	const goroutines = 5
	var wg sync.WaitGroup
	wg.Add(goroutines)
	barrier := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			<-barrier // all start at once
			cr.Close()
		}()
	}

	done := make(chan struct{})
	go func() {
		close(barrier)
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All goroutines returned.
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent Close() deadlocked")
	}
}

// TestCatfileReader_CloseWithoutReading verifies that closing
// immediately after creation (without reading any entries) completes
// without hanging.
func TestCatfileReader_CloseWithoutReading(t *testing.T) {
	t.Parallel()

	repoDir, blobs := createTestRepo(t)
	ids := []plumbing.Hash{
		blobs["hello.txt"],
		blobs["large.bin"],
		blobs["binary.bin"],
		blobs["empty.txt"],
	}

	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cr.Close()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close() without reading any entries hung")
	}
}

// TestCatfileReader_CloseBeforeExhausted_ManyBlobs simulates early
// termination (e.g., builder.Add error) with many unconsumed blobs.
// Close should complete promptly — not drain the entire git output.
func TestCatfileReader_CloseBeforeExhausted_ManyBlobs(t *testing.T) {
	t.Parallel()

	// Create enough blobs to make a draining Close noticeable without spending
	// most of the test runtime on shelling out for fixture setup.
	repoDir, ids := createManyBlobRepo(t, 128, 4<<10)

	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Read only 1 of 200 entries.
	if _, _, _, err := cr.Next(); err != nil {
		t.Fatal(err)
	}

	// Close should be fast (kill, not drain). With drain it still works but
	// is slow — we enforce a generous bound.
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- cr.Close()
	}()

	select {
	case <-done:
		elapsed := time.Since(start)
		// With Kill: sub-millisecond. Draining 200×10KB is fast too, so we
		// use a generous 3s bound that still catches pathological stalls.
		if elapsed > 3*time.Second {
			t.Errorf("Close took %v after reading 1 of 200 entries — consider killing instead of draining", elapsed)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Close() deadlocked with many unconsumed blobs")
	}
}

func createManyBlobRepo(t *testing.T, fileCount, fileSize int) (string, []plumbing.Hash) {
	t.Helper()

	dir := t.TempDir()
	repoDir := filepath.Join(dir, "repo")

	runGit(t, dir, "init", "-b", "main", "repo")
	runGit(t, repoDir, "config", "user.email", "test@test.com")
	runGit(t, repoDir, "config", "user.name", "Test")

	for i := 0; i < fileCount; i++ {
		content := bytes.Repeat([]byte{byte(i)}, fileSize)
		name := filepath.Join(repoDir, fmt.Sprintf("file_%03d.bin", i))
		if err := os.WriteFile(name, content, 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", name, err)
		}
	}

	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "many files")

	out, err := exec.Command("git", "-C", repoDir, "ls-tree", "-r", "-z", "HEAD").Output()
	if err != nil {
		t.Fatalf("git ls-tree: %v", err)
	}

	ids := make([]plumbing.Hash, 0, fileCount)
	for _, entry := range bytes.Split(out, []byte{0}) {
		if len(entry) == 0 {
			continue
		}

		fields := bytes.Fields(entry)
		if len(fields) < 3 {
			t.Fatalf("unexpected ls-tree entry %q", entry)
		}

		ids = append(ids, plumbing.NewHash(string(fields[2])))
	}

	if len(ids) != fileCount {
		t.Fatalf("got %d blob IDs, want %d", len(ids), fileCount)
	}

	return repoDir, ids
}

// --- Read edge-case tests ---

// TestCatfileReader_ReadWithoutNext verifies that calling Read
// before calling Next returns io.EOF, not a panic or garbage data.
func TestCatfileReader_ReadWithoutNext(t *testing.T) {
	t.Parallel()

	repoDir, blobs := createTestRepo(t)
	ids := []plumbing.Hash{blobs["hello.txt"]}

	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()

	buf := make([]byte, 10)
	n, err := cr.Read(buf)
	if n != 0 || err != io.EOF {
		t.Fatalf("Read without Next: n=%d err=%v, want n=0 err=io.EOF", n, err)
	}
}

// TestCatfileReader_ReadAfterFullConsumption verifies that extra Read
// calls after a blob is fully consumed return io.EOF, not duplicate
// data or trailing LF bytes.
func TestCatfileReader_ReadAfterFullConsumption(t *testing.T) {
	t.Parallel()

	repoDir, blobs := createTestRepo(t)
	ids := []plumbing.Hash{blobs["hello.txt"]}

	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()

	size, _, _, _ := cr.Next()
	content := make([]byte, size)
	if _, err := io.ReadFull(cr, content); err != nil {
		t.Fatal(err)
	}

	// Blob is fully read — additional Reads must return EOF.
	for i := 0; i < 3; i++ {
		buf := make([]byte, 10)
		n, err := cr.Read(buf)
		if n != 0 || err != io.EOF {
			t.Fatalf("Read #%d after full consumption: n=%d err=%v, want n=0 err=io.EOF", i, n, err)
		}
	}
}

// TestCatfileReader_SmallBufferReads reads a blob one byte at a time
// and verifies the entire content is reconstructed correctly without
// any trailing LF leaking into user content.
func TestCatfileReader_SmallBufferReads(t *testing.T) {
	t.Parallel()

	repoDir, blobs := createTestRepo(t)
	ids := []plumbing.Hash{blobs["hello.txt"]}

	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()

	size, _, _, _ := cr.Next()

	var result []byte
	buf := make([]byte, 1)
	for {
		n, err := cr.Read(buf)
		if n > 0 {
			result = append(result, buf[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}

	if len(result) != size {
		t.Fatalf("read %d bytes, want %d", len(result), size)
	}
	if string(result) != "hello world\n" {
		t.Errorf("content = %q, want %q", result, "hello world\n")
	}
}

// TestCatfileReader_PartialReadThenNext reads only part of a blob's
// content, then advances to the next entry. Verifies that the discard
// of pending bytes doesn't corrupt the stream.
func TestCatfileReader_PartialReadThenNext(t *testing.T) {
	t.Parallel()

	repoDir, blobs := createTestRepo(t)
	ids := []plumbing.Hash{
		blobs["hello.txt"],  // 12 bytes: "hello world\n"
		blobs["binary.bin"], // variable, starts with 0x00
	}

	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()

	// Read only 5 of 12 bytes from hello.txt.
	size, _, _, _ := cr.Next()
	if size != 12 {
		t.Fatalf("hello.txt size = %d, want 12", size)
	}
	partial := make([]byte, 5)
	if _, err := io.ReadFull(cr, partial); err != nil {
		t.Fatal(err)
	}
	if string(partial) != "hello" {
		t.Fatalf("partial = %q, want %q", partial, "hello")
	}

	// Advance — must discard remaining 7 content bytes + trailing LF.
	size, _, _, err = cr.Next()
	if err != nil {
		t.Fatalf("Next binary.bin after partial read: %v", err)
	}

	// Verify binary.bin content is intact.
	content := make([]byte, size)
	if _, err := io.ReadFull(cr, content); err != nil {
		t.Fatal(err)
	}
	if content[0] != 0x00 {
		t.Errorf("binary.bin first byte = 0x%02x after partial-read skip, want 0x00", content[0])
	}
}

// TestCatfileReader_PartialReadExactlyOneByteShort reads size-1 bytes
// from a blob. The pending field should be exactly 2 (1 content byte +
// 1 trailing LF). This stresses the boundary between content and LF
// in the discard path.
func TestCatfileReader_PartialReadExactlyOneByteShort(t *testing.T) {
	t.Parallel()

	repoDir, blobs := createTestRepo(t)
	ids := []plumbing.Hash{
		blobs["hello.txt"],  // 12 bytes
		blobs["binary.bin"], // starts with 0x00
	}

	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()

	size, _, _, _ := cr.Next()
	// Read exactly size-1 bytes — leaves 1 content byte + trailing LF.
	buf := make([]byte, size-1)
	if _, err := io.ReadFull(cr, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello world" { // missing final \n
		t.Fatalf("partial = %q", buf)
	}

	// Advance — pending should be 2 (1 content byte + 1 LF). The
	// Discard call must handle this exact boundary correctly.
	size, missing, excluded, err := cr.Next()
	if err != nil {
		t.Fatalf("Next after size-1 partial read: %v", err)
	}
	if missing || excluded {
		t.Fatal("binary.bin unexpectedly missing")
	}

	// Read binary.bin to verify stream integrity.
	content := make([]byte, size)
	if _, err := io.ReadFull(cr, content); err != nil {
		t.Fatal(err)
	}
	if content[0] != 0x00 {
		t.Errorf("binary.bin[0] = 0x%02x after boundary skip, want 0x00", content[0])
	}
}

// --- Empty / degenerate input tests ---

// TestCatfileReader_EmptyIds verifies that an empty id slice produces
// immediate EOF without errors.
func TestCatfileReader_EmptyIds(t *testing.T) {
	t.Parallel()

	repoDir, _ := createTestRepo(t)

	cr, err := newCatfileReader(repoDir, nil, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()

	_, _, _, err = cr.Next()
	if err != io.EOF {
		t.Fatalf("expected io.EOF for empty ids, got %v", err)
	}
}

// TestCatfileReader_MultipleEmptyBlobs stresses the trailing-LF
// handling for size-0 blobs. Git still outputs a LF after a 0-byte
// blob body. Repeated empty blobs test the pending=1 discard path.
func TestCatfileReader_MultipleEmptyBlobs(t *testing.T) {
	t.Parallel()

	repoDir, blobs := createTestRepo(t)

	// Send the empty blob SHA 5 times — git outputs each independently.
	emptyID := blobs["empty.txt"]
	ids := []plumbing.Hash{emptyID, emptyID, emptyID, emptyID, emptyID}

	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()

	for i := range ids {
		size, missing, excluded, err := cr.Next()
		if err != nil {
			t.Fatalf("Next #%d: %v", i, err)
		}
		if missing || excluded {
			t.Fatalf("#%d unexpectedly missing", i)
		}
		if size != 0 {
			t.Fatalf("#%d size = %d, want 0", i, size)
		}
		// Don't read — Next should discard the trailing LF for us.
	}

	_, _, _, err = cr.Next()
	if err != io.EOF {
		t.Fatalf("expected EOF after %d empty blobs, got %v", len(ids), err)
	}
}

// TestCatfileReader_EmptyBlobRead verifies that reading a 0-byte blob
// through the io.Reader interface returns 0 bytes and io.EOF, and that
// the trailing LF is consumed transparently.
func TestCatfileReader_EmptyBlobRead(t *testing.T) {
	t.Parallel()

	repoDir, blobs := createTestRepo(t)
	ids := []plumbing.Hash{
		blobs["empty.txt"], // 0 bytes
		blobs["hello.txt"], // 12 bytes — sentinel
	}

	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()

	size, _, _, _ := cr.Next()
	if size != 0 {
		t.Fatalf("empty.txt size = %d", size)
	}

	// Explicitly Read on the 0-byte blob.
	buf := make([]byte, 10)
	n, err := cr.Read(buf)
	if n != 0 || err != io.EOF {
		t.Fatalf("Read empty blob: n=%d err=%v, want n=0 err=io.EOF", n, err)
	}

	// The trailing LF must have been consumed. Verify by reading the
	// next entry — if the LF leaked, the header parse would fail.
	size, _, _, err = cr.Next()
	if err != nil {
		t.Fatalf("Next hello.txt after empty blob Read: %v", err)
	}
	if size != 12 {
		t.Fatalf("hello.txt size = %d, want 12", size)
	}
	content := make([]byte, size)
	if _, err := io.ReadFull(cr, content); err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello world\n" {
		t.Errorf("hello.txt = %q", content)
	}
}

// --- Missing object edge cases ---

// TestCatfileReader_AllMissing verifies that a sequence of entirely
// missing objects is handled gracefully — no errors, no panics, just
// missing=true for each followed by EOF.
func TestCatfileReader_AllMissing(t *testing.T) {
	t.Parallel()

	repoDir, _ := createTestRepo(t)

	ids := []plumbing.Hash{
		plumbing.NewHash("deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"),
		plumbing.NewHash("1111111111111111111111111111111111111111"),
		plumbing.NewHash("2222222222222222222222222222222222222222"),
	}

	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()

	for i, id := range ids {
		_, missing, excluded, err := cr.Next()
		if err != nil {
			t.Fatalf("Next #%d (%s): %v", i, id, err)
		}
		if excluded {
			t.Errorf("expected #%d (%s) to be missing, not excluded", i, id)
		}
		if !missing {
			t.Errorf("expected #%d (%s) to be missing", i, id)
		}
	}

	_, _, _, err = cr.Next()
	if err != io.EOF {
		t.Fatalf("expected EOF after all missing, got %v", err)
	}
}

// TestCatfileReader_AlternatingMissingPresent interleaves missing and
// present objects, verifying that stream alignment is maintained.
func TestCatfileReader_AlternatingMissingPresent(t *testing.T) {
	t.Parallel()

	repoDir, blobs := createTestRepo(t)

	fake1 := plumbing.NewHash("deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	fake2 := plumbing.NewHash("1111111111111111111111111111111111111111")

	ids := []plumbing.Hash{
		fake1,
		blobs["hello.txt"],
		fake2,
		blobs["empty.txt"],
		blobs["binary.bin"],
	}

	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()

	// fake1 — missing
	_, missing, excluded, err := cr.Next()
	if err != nil || !missing || excluded {
		t.Fatalf("fake1: err=%v missing=%v excluded=%v", err, missing, excluded)
	}

	// hello.txt — present, read it
	size, missing, excluded, err := cr.Next()
	if err != nil || missing || excluded {
		t.Fatalf("hello.txt: err=%v missing=%v excluded=%v", err, missing, excluded)
	}
	content := make([]byte, size)
	if _, err := io.ReadFull(cr, content); err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello world\n" {
		t.Errorf("hello.txt = %q", content)
	}

	// fake2 — missing
	_, missing, excluded, err = cr.Next()
	if err != nil || !missing || excluded {
		t.Fatalf("fake2: err=%v missing=%v excluded=%v", err, missing, excluded)
	}

	// empty.txt — present, skip it
	size, missing, excluded, err = cr.Next()
	if err != nil || missing || excluded {
		t.Fatalf("empty.txt: err=%v missing=%v excluded=%v", err, missing, excluded)
	}
	if size != 0 {
		t.Errorf("empty.txt size = %d", size)
	}

	// binary.bin — present, read it
	size, missing, excluded, err = cr.Next()
	if err != nil || missing || excluded {
		t.Fatalf("binary.bin: err=%v missing=%v excluded=%v", err, missing, excluded)
	}
	binContent := make([]byte, size)
	if _, err := io.ReadFull(cr, binContent); err != nil {
		t.Fatal(err)
	}
	if binContent[0] != 0x00 {
		t.Errorf("binary.bin[0] = 0x%02x, want 0x00", binContent[0])
	}

	_, _, _, err = cr.Next()
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

// TestCatfileReader_MissingThenSkip verifies that a missing object
// followed by a present but skipped (unread) object doesn't corrupt
// the stream. Missing objects have no content body, so there must be
// no stale pending bytes interfering with the next header read.
func TestCatfileReader_MissingThenSkip(t *testing.T) {
	t.Parallel()

	repoDir, blobs := createTestRepo(t)

	fake := plumbing.NewHash("deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	ids := []plumbing.Hash{
		fake,
		blobs["large.bin"], // 64KB — skip without reading
		blobs["hello.txt"], // sentinel — read to verify integrity
	}

	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()

	// missing
	_, missing, excluded, _ := cr.Next()
	if !missing || excluded {
		t.Fatal("expected missing")
	}

	// large.bin — skip
	size, missing, excluded, err := cr.Next()
	if err != nil || missing || excluded {
		t.Fatalf("large.bin: err=%v missing=%v excluded=%v", err, missing, excluded)
	}
	if size != 64*1024 {
		t.Fatalf("large.bin size = %d", size)
	}
	// deliberately don't read

	// hello.txt — read after missing+skip
	size, missing, excluded, err = cr.Next()
	if err != nil || missing || excluded {
		t.Fatalf("hello.txt: err=%v missing=%v excluded=%v", err, missing, excluded)
	}
	content := make([]byte, size)
	if _, err := io.ReadFull(cr, content); err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello world\n" {
		t.Errorf("hello.txt = %q", content)
	}
}

// --- Next() edge cases ---

// TestCatfileReader_RepeatedNextAfterEOF verifies that calling Next
// after EOF keeps returning EOF — not a panic, not a different error.
func TestCatfileReader_RepeatedNextAfterEOF(t *testing.T) {
	t.Parallel()

	repoDir, blobs := createTestRepo(t)
	ids := []plumbing.Hash{blobs["hello.txt"]}

	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()

	// Consume and skip the only entry.
	if _, _, _, err := cr.Next(); err != nil {
		t.Fatal(err)
	}

	// First EOF.
	_, _, _, err = cr.Next()
	if err != io.EOF {
		t.Fatalf("first post-exhaust Next: %v, want io.EOF", err)
	}

	// Second and third EOF — must be stable.
	for i := 0; i < 2; i++ {
		_, _, _, err = cr.Next()
		if err != io.EOF {
			t.Fatalf("Next #%d after EOF: %v, want io.EOF", i+2, err)
		}
	}
}

// --- Large blob precision tests ---

// TestCatfileReader_LargeBlobBytePrecision verifies that a 64KB blob
// is read with byte-exact precision — no off-by-one from trailing LF
// handling, no truncation, no extra bytes.
func TestCatfileReader_LargeBlobBytePrecision(t *testing.T) {
	t.Parallel()

	repoDir, blobs := createTestRepo(t)
	ids := []plumbing.Hash{blobs["large.bin"]}

	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()

	size, _, _, err := cr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if size != 64*1024 {
		t.Fatalf("size = %d, want %d", size, 64*1024)
	}

	// Read the full blob content.
	content := make([]byte, size)
	n, err := io.ReadFull(cr, content)
	if err != nil {
		t.Fatalf("ReadFull: %v (read %d of %d)", err, n, size)
	}
	if n != size {
		t.Fatalf("read %d bytes, want %d", n, size)
	}

	// Verify git agrees on the content via cat-file -p.
	expected, err := exec.Command("git", "-C", repoDir, "cat-file", "-p", blobs["large.bin"].String()).Output()
	if err != nil {
		t.Fatalf("git cat-file -p: %v", err)
	}
	if !bytes.Equal(content, expected) {
		t.Errorf("content mismatch: got %d bytes, git says %d bytes", len(content), len(expected))
		// Find first divergence.
		for i := range content {
			if i >= len(expected) || content[i] != expected[i] {
				t.Errorf("first diff at byte %d: got 0x%02x, want 0x%02x", i, content[i], expected[i])
				break
			}
		}
	}
}

// TestCatfileReader_LargeBlobChunkedRead reads a 64KB blob in 997-byte
// chunks (a prime number that doesn't align with any power-of-2 buffer)
// to verify no byte is lost or duplicated across read boundaries.
func TestCatfileReader_LargeBlobChunkedRead(t *testing.T) {
	t.Parallel()

	repoDir, blobs := createTestRepo(t)
	ids := []plumbing.Hash{blobs["large.bin"]}

	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()

	size, _, _, _ := cr.Next()
	if size != 64*1024 {
		t.Fatalf("size = %d", size)
	}

	var result bytes.Buffer
	buf := make([]byte, 997) // prime-sized chunks
	for {
		n, err := cr.Read(buf)
		if n > 0 {
			result.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}

	if result.Len() != size {
		t.Fatalf("total read = %d, want %d", result.Len(), size)
	}

	// Cross-check with git.
	expected, _ := exec.Command("git", "-C", repoDir, "cat-file", "-p", blobs["large.bin"].String()).Output()
	if !bytes.Equal(result.Bytes(), expected) {
		t.Error("chunked read content differs from git cat-file -p output")
	}
}

// --- Duplicate SHA test ---

// TestCatfileReader_DuplicateSHAs verifies that requesting the same
// SHA multiple times works — git cat-file --batch outputs the object
// for each request independently.
func TestCatfileReader_DuplicateSHAs(t *testing.T) {
	t.Parallel()

	repoDir, blobs := createTestRepo(t)

	sha := blobs["hello.txt"]
	ids := []plumbing.Hash{sha, sha, sha}

	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()

	for i := 0; i < 3; i++ {
		size, missing, excluded, err := cr.Next()
		if err != nil {
			t.Fatalf("Next #%d: %v", i, err)
		}
		if missing || excluded {
			t.Fatalf("#%d unexpectedly missing", i)
		}
		if size != 12 {
			t.Fatalf("#%d size = %d, want 12", i, size)
		}
		content := make([]byte, size)
		if _, err := io.ReadFull(cr, content); err != nil {
			t.Fatal(err)
		}
		if string(content) != "hello world\n" {
			t.Errorf("#%d content = %q", i, content)
		}
	}

	_, _, _, err = cr.Next()
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}
