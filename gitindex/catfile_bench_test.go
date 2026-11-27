package gitindex

import (
	"bufio"
	"bytes"
	"cmp"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
)

// Set ZOEKT_BENCH_REPO to a git checkout to enable these benchmarks.
//
//	git clone --depth=1 https://github.com/kubernetes/kubernetes /tmp/k8s
//	ZOEKT_BENCH_REPO=/tmp/k8s go test ./gitindex/ -bench=BenchmarkBlobRead -benchmem -count=5 -timeout=600s
//	ZOEKT_BENCH_REPO=/tmp/k8s go test ./gitindex/ -run='^$' -bench=BenchmarkCatfileReader_Replay -benchmem -count=10

func requireBenchGitRepo(b *testing.B) string {
	b.Helper()
	dir := os.Getenv("ZOEKT_BENCH_REPO")
	if dir == "" {
		b.Skip("ZOEKT_BENCH_REPO not set")
	}
	return dir
}

// collectBlobKeys opens the repo, walks HEAD, and returns all fileKeys with
// their BlobLocations plus the repo directory path.
func collectBlobKeys(b *testing.B, repoDir string) (map[fileKey]BlobLocation, string) {
	b.Helper()

	repo, closer, err := openRepo(repoDir)
	if err != nil {
		b.Fatalf("openRepo: %v", err)
	}
	b.Cleanup(func() { closer.Close() })

	head, err := repo.Head()
	if err != nil {
		b.Fatalf("Head: %v", err)
	}

	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		b.Fatalf("CommitObject: %v", err)
	}

	tree, err := commit.Tree()
	if err != nil {
		b.Fatalf("Tree: %v", err)
	}

	rw := NewRepoWalker(repo, "https://example.com/repo", nil)
	if _, err := rw.CollectFiles(tree, "HEAD"); err != nil {
		b.Fatalf("CollectFiles: %v", err)
	}

	return rw.Files, repoDir
}

// sortedBlobKeys returns fileKeys for deterministic iteration, so the
// files=N subsets read the same blobs on every run.
func sortedBlobKeys(files map[fileKey]BlobLocation) []fileKey {
	keys := make([]fileKey, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b fileKey) int {
		return cmp.Or(cmp.Compare(a.SubRepoPath, b.SubRepoPath), cmp.Compare(a.Path, b.Path))
	})
	return keys
}

// BenchmarkBlobRead_GoGit measures the current go-git BlobObject approach:
// sequential calls to repo.GitRepo.BlobObject(hash) for each file.
func BenchmarkBlobRead_GoGit(b *testing.B) {
	repoDir := requireBenchGitRepo(b)
	files, _ := collectBlobKeys(b, repoDir)
	keys := sortedBlobKeys(files)
	b.Logf("collected %d blob keys", len(keys))

	for _, n := range []int{1_000, 5_000, len(keys)} {
		n = min(n, len(keys))
		subset := keys[:n]

		b.Run(fmt.Sprintf("files=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			var totalBytes int64
			for b.Loop() {
				totalBytes = 0
				for _, key := range subset {
					loc := files[key]
					blob, err := loc.GitRepo.BlobObject(key.ID)
					if err != nil {
						b.Fatalf("BlobObject(%s): %v", key.ID, err)
					}
					r, err := blob.Reader()
					if err != nil {
						b.Fatalf("Reader: %v", err)
					}
					n, err := io.Copy(io.Discard, r)
					r.Close()
					if err != nil {
						b.Fatalf("Read: %v", err)
					}
					totalBytes += n
				}
			}
			b.ReportMetric(float64(totalBytes), "content-bytes/op")
			b.ReportMetric(float64(len(subset)), "files/op")
		})
	}
}

// BenchmarkBlobRead_CatfileReader measures the streaming catfileReader approach:
// all SHAs written to stdin at once via --buffer, responses read one at a time.
// This is the production path used by indexGitRepo.
func BenchmarkBlobRead_CatfileReader(b *testing.B) {
	repoDir := requireBenchGitRepo(b)
	files, gitDir := collectBlobKeys(b, repoDir)
	keys := sortedBlobKeys(files)
	b.Logf("collected %d blob keys", len(keys))

	ids := make([]plumbing.Hash, len(keys))
	for i, k := range keys {
		ids[i] = k.ID
	}

	for _, n := range []int{1_000, 5_000, len(keys)} {
		n = min(n, len(keys))
		subset := ids[:n]

		b.Run(fmt.Sprintf("files=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			var totalBytes int64
			for b.Loop() {
				totalBytes = 0
				cr, err := newCatfileReader(gitDir, subset, catfileReaderOptions{})
				if err != nil {
					b.Fatalf("newCatfileReader: %v", err)
				}
				for range subset {
					size, missing, excluded, err := cr.Next()
					if err != nil {
						cr.Close()
						b.Fatalf("Next: %v", err)
					}
					if missing || excluded {
						continue
					}
					content := make([]byte, size)
					if _, err := io.ReadFull(cr, content); err != nil {
						cr.Close()
						b.Fatalf("ReadFull: %v", err)
					}
					totalBytes += int64(len(content))
				}
				cr.Close()
			}
			b.ReportMetric(float64(totalBytes), "content-bytes/op")
			b.ReportMetric(float64(len(subset)), "files/op")
		})
	}
}

// BenchmarkCatfileReader_Replay measures catfileReader's own per-entry work,
// header parsing and validation plus content reads, without git in the loop.
// It records the real "git cat-file --batch" output for every blob at HEAD
// once, then replays it from memory. Content is read into a reused buffer so
// allocations reported here come from the reader itself.
func BenchmarkCatfileReader_Replay(b *testing.B) {
	repoDir := requireBenchGitRepo(b)
	files, _ := collectBlobKeys(b, repoDir)
	keys := sortedBlobKeys(files)

	ids := make([]plumbing.Hash, len(keys))
	var stdin bytes.Buffer
	for i, k := range keys {
		ids[i] = k.ID
		stdin.WriteString(k.ID.String())
		stdin.WriteByte('\n')
	}
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Dir = repoDir
	cmd.Stdin = &stdin
	stream, err := cmd.Output()
	if err != nil {
		b.Fatalf("git cat-file: %v", err)
	}
	b.Logf("recorded %d entries, %d bytes", len(ids), len(stream))

	b.ReportAllocs()
	b.SetBytes(int64(len(stream)))
	var buf []byte
	for b.Loop() {
		cr := &catfileReader{
			reader: bufio.NewReaderSize(bytes.NewReader(stream), 512*1024),
			ids:    ids,
		}
		for range ids {
			size, missing, excluded, err := cr.Next()
			if err != nil {
				b.Fatalf("Next: %v", err)
			}
			if missing || excluded {
				continue
			}
			if cap(buf) < size {
				buf = make([]byte, size)
			}
			if _, err := io.ReadFull(cr, buf[:size]); err != nil {
				b.Fatalf("ReadFull: %v", err)
			}
		}
	}
	b.ReportMetric(float64(len(ids)), "files/op")
}
