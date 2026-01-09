package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/sourcegraph/zoekt"
)

// We compare 2 simple shards before and after the transformation
// explode(merge(shard1, shard2)). We expect the input and output shards to be
// identical.
func TestExplode(t *testing.T) {
	// Use v16 source files but expect v17 output after merge+explode
	v16Shards := []string{
		".././testdata/shards/repo_v16.00000.zoekt",
		".././testdata/shards/repo2_v16.00000.zoekt",
	}
	v17Shards := []string{
		".././testdata/shards/repo_v17.00000.zoekt",
		".././testdata/shards/repo2_v17.00000.zoekt",
	}

	// repo name -> IndexMetadata
	m := make(map[string]*zoekt.IndexMetadata, 2)

	// merge
	var files []IndexFile
	for _, fn := range v16Shards {
		f, err := os.Open(fn)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()

		indexFile, err := NewIndexFile(f)
		if err != nil {
			t.Fatal(err)
		}
		defer indexFile.Close()

		// We save indexMeta because the fields ID and IndexTime are the 2 sources of
		// non-determinism when building a new shard.
		repoMeta, indexMeta, err := ReadMetadata(indexFile)
		if err != nil {
			t.Fatal(err)
		}
		if len(repoMeta) != 1 {
			t.Fatal("this test assumes that indexFile contains only 1 repo")
		}
		m[repoMeta[0].Name] = indexMeta

		files = append(files, indexFile)
	}

	tmpDir := t.TempDir()
	tmpName, dstName, err := Merge(tmpDir, files...)
	if err != nil {
		t.Fatal(err)
	}
	err = os.Rename(tmpName, dstName)
	if err != nil {
		t.Fatal(err)
	}

	// explode
	f, err := os.Open(dstName)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	indexFile, err := NewIndexFile(f)
	if err != nil {
		t.Fatal(err)
	}
	defer indexFile.Close()

	overwriteIndexTimeAndID := func(ib *ShardBuilder) {
		ib.ID = m[ib.repoList[0].Name].ID
		ib.IndexTime = m[ib.repoList[0].Name].IndexTime
	}
	exploded, err := explode(tmpDir, indexFile, overwriteIndexTimeAndID)
	if err != nil {
		t.Fatal(err)
	}
	for tmp, final := range exploded {
		err = os.Rename(tmp, final)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Compare exploded v17 shards with expected v17 golden files
	for _, s := range v17Shards {
		checkSameShards(t, s, filepath.Join(tmpDir, filepath.Base(s)))
	}
}

// checkSameShards compares 2 shards byte by byte. The shards are expected to be
// small enough to be read in all at once.
func checkSameShards(t *testing.T, shard1, shard2 string) {
	t.Helper()

	b2, err := os.ReadFile(shard2)
	if err != nil {
		t.Fatal(err)
	}

	b1, err := os.ReadFile(shard1)
	if err != nil {
		if os.IsNotExist(err) && *update {
			// Golden file doesn't exist, create it in update mode
			t.Logf("creating new golden file %s", shard1)
			err := os.WriteFile(shard1, b2, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			return
		}
		t.Fatal(err)
	}

	// We could also use bytes.Equal, but the output of cmd.Diff is very helpful for
	// differences in metadata.
	d := cmp.Diff(b1, b2)
	if d == "" {
		return
	}

	if *update {
		t.Logf("updating %s", shard1)
		err := os.WriteFile(shard1, b2, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		return
	}

	t.Fatalf("-%s\n+%s:\n%s", shard1, shard2, d)
}
