package gitindex

import (
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
)

// Set ZOEKT_BENCH_REPO to a git checkout to enable these benchmarks.
//
//	git clone --depth=1 https://github.com/kubernetes/kubernetes /tmp/k8s
//	ZOEKT_BENCH_REPO=/tmp/k8s go test ./gitindex/ -bench=BenchmarkBlobRead -benchmem -count=5 -timeout=600s

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
	if _, err := rw.CollectFiles(tree, "HEAD", nil); err != nil {
		b.Fatalf("CollectFiles: %v", err)
	}

	return rw.Files, repoDir
}

// sortedBlobKeys returns fileKeys for deterministic iteration.
func sortedBlobKeys(files map[fileKey]BlobLocation) []fileKey {
	keys := make([]fileKey, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
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

// BenchmarkBlobRead_CatfilePipelined measures the pipelined approach:
// all SHAs written to stdin at once via --buffer, then all responses read.
// This is the production path used by indexGitRepo.
func BenchmarkBlobRead_CatfilePipelined(b *testing.B) {
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
				results, err := readBlobsPipelined(gitDir, subset)
				if err != nil {
					b.Fatalf("readBlobsPipelined: %v", err)
				}
				for _, r := range results {
					if r.Err != nil {
						b.Fatalf("blob %s: %v", r.ID, r.Err)
					}
					totalBytes += int64(len(r.Content))
				}
			}
			b.ReportMetric(float64(totalBytes), "content-bytes/op")
			b.ReportMetric(float64(len(subset)), "files/op")
		})
	}
}
