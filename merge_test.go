package zoekt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
)

// We compare 2 simple shards before and after the transformation
// explode(merge(shard1, shard2)). We expect the input and output shards to be
// identical.
func TestExplode(t *testing.T) {
	simpleShards := []string{
		"./testdata/shards/repo_v16.00000.zoekt",
		"./testdata/shards/repo2_v16.00000.zoekt",
	}

	// repo name -> IndexMetadata
	m := make(map[string]*IndexMetadata, 2)

	// merge
	var files []IndexFile
	for _, fn := range simpleShards {
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

	overwriteIndexTimeAndID := func(ib *IndexBuilder) {
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

	for _, s := range simpleShards {
		checkSameShards(t, s, filepath.Join(tmpDir, filepath.Base(s)))
	}
}

// checkSameShards compares 2 shards byte by byte. The shards are expected to be
// small enough to be read in all at once.
func checkSameShards(t *testing.T, shard1, shard2 string) {
	t.Helper()
	b1, err := os.ReadFile(shard1)
	if err != nil {
		t.Fatal(err)
	}

	b2, err := os.ReadFile(shard2)
	if err != nil {
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

func TestRepoOrder(t *testing.T) {
	idNames := []struct {
		id   uint32
		name string
	}{
		{3, "foo"},
		{1, "bar"},
		{4, "baz"},
		{2, "bas"},
	}

	ds := make([]*indexData, 0, len(idNames))
	for _, repo := range idNames {
		ib := newIndexBuilder()
		ib.indexFormatVersion = NextIndexFormatVersion

		err := ib.setRepository(&Repository{Name: repo.name, ID: repo.id})
		require.NoError(t, err)

		// Add some documents to the index.
		for _, doc := range []Document{
			{Name: repo.name + ".txt", Content: []byte(repo.name + " content")},
			{Name: repo.name + ".2.txt", Content: []byte(repo.name + " content 2")},
		} {
			err := ib.Add(doc)
			require.NoError(t, err)
		}

		d := searcherForTest(t, ib)
		ds = append(ds, d.(*indexData))
	}

	ib, err := merge(ds...)
	require.NoError(t, err)

	require.Len(t, ib.repoList, len(idNames))
	for i := 1; i < len(ib.repoList); i++ {
		require.Less(t, ib.repoList[i-1].ID, ib.repoList[i].ID)
	}
}
