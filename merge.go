package zoekt

import (
	"crypto/sha1"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/sourcegraph/zoekt/internal/tenant"
)

// Merge files into a compound shard in dstDir. Merge returns tmpName and a
// dstName. It is the responsibility of the caller to delete the input shards and
// rename the temporary compound shard from tmpName to dstName.
func Merge(dstDir string, files ...IndexFile) (tmpName, dstName string, _ error) {
	var ds []*indexData
	for _, f := range files {
		searcher, err := NewSearcher(f)
		if err != nil {
			return "", "", err
		}
		ds = append(ds, searcher.(*indexData))
	}

	ib, err := merge(ds...)
	if err != nil {
		return "", "", err
	}

	hasher := sha1.New()
	for _, d := range ds {
		for i, md := range d.repoMetaData {
			if d.repoMetaData[i].Tombstone {
				continue
			}
			hasher.Write([]byte(md.Name))
			hasher.Write([]byte{0})
		}
	}

	dstName = filepath.Join(dstDir, fmt.Sprintf("compound-%x_v%d.%05d.zoekt", hasher.Sum(nil), NextIndexFormatVersion, 0))
	tmpName = dstName + ".tmp"
	if err := builderWriteAll(tmpName, ib); err != nil {
		return "", "", err
	}
	return tmpName, dstName, nil
}

func builderWriteAll(fn string, ib *IndexBuilder) error {
	dir := filepath.Dir(fn)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, filepath.Base(fn)+".*.tmp")
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		// umask?
		if err := f.Chmod(0o666); err != nil {
			return err
		}
	}

	defer f.Close()
	if err := ib.Write(f); err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	if err := os.Rename(f.Name(), fn); err != nil {
		return err
	}

	log.Printf("finished shard %s: %d index bytes (overhead %3.1f)", fn, fi.Size(),
		float64(fi.Size())/float64(ib.ContentSize()+1))

	return nil
}

func merge(ds ...*indexData) (*IndexBuilder, error) {
	if len(ds) == 0 {
		return nil, fmt.Errorf("need 1 or more indexData to merge")
	}
	// We sort shards in compound shards by repository ID so that we can use binary
	// search instead of looping through the entire list of repos when calling
	// `build.findShard`.
	//
	// We have seen that for very large compound shards (5k+ repos) it can take a
	// long time to find a specific repo which can slow down initial indexing even
	// for no-op indexes after a restart of indexserver.
	//
	// The sort order will impact ranking of non-bm25 search, because doc-order acts
	// as a tie-breaker. However, the actual impact is very small, because
	// "LastCommitDate" is a stronger signal.
	sort.Slice(ds, func(i, j int) bool {
		return ds[i].repoMetaData[0].ID < ds[j].repoMetaData[0].ID
	})

	ib := newIndexBuilder()
	ib.indexFormatVersion = NextIndexFormatVersion

	for _, d := range ds {
		lastRepoID := -1
		for docID := uint32(0); int(docID) < len(d.fileBranchMasks); docID++ {
			repoID := int(d.repos[docID])

			if d.repoMetaData[repoID].Tombstone {
				continue
			}

			if repoID != lastRepoID {
				if lastRepoID > repoID {
					return nil, fmt.Errorf("non-contiguous repo ids in %s for document %d: old=%d current=%d", d.String(), docID, lastRepoID, repoID)
				}
				lastRepoID = repoID

				// TODO we are losing empty repos on merging since we only get here if
				// there is an associated document.

				if err := ib.setRepository(&d.repoMetaData[repoID]); err != nil {
					return nil, err
				}
			}

			if err := addDocument(d, ib, repoID, docID); err != nil {
				return nil, err
			}
		}
	}

	return ib, nil
}

// Explode takes an IndexFile f and creates 1 simple shard per repository
// contained in f. Explode returns a map of tmpName -> dstName. It is the
// responsibility of the caller to rename the temporary shard(s) and delete the
// input shard.
func Explode(dstDir string, f IndexFile) (map[string]string, error) {
	return explode(dstDir, f)
}

type indexBuilderFunc func(ib *IndexBuilder)

// explode offers a richer signature compared to Explode for testing. You
// probably want to call Explode instead.
func explode(dstDir string, f IndexFile, ibFuncs ...indexBuilderFunc) (map[string]string, error) {
	searcher, err := NewSearcher(f)
	if err != nil {
		return nil, err
	}
	d := searcher.(*indexData)

	shardNames := make(map[string]string, len(d.repoMetaData))

	writeShard := func(ib *IndexBuilder) error {
		if len(ib.repoList) != 1 {
			return fmt.Errorf("expected ib to contain exactly 1 repository")
		}
		for _, ibFunc := range ibFuncs {
			ibFunc(ib)
		}

		prefix := ""
		if tenant.EnforceTenant() {
			prefix = tenant.SrcPrefix(ib.repoList[0].TenantID, ib.repoList[0].ID)
		} else {
			prefix = ib.repoList[0].Name
		}

		shardName := ShardName(dstDir, prefix, ib.indexFormatVersion, 0)
		shardNameTmp := shardName + ".tmp"
		shardNames[shardNameTmp] = shardName
		return builderWriteAll(shardNameTmp, ib)
	}

	var ib *IndexBuilder
	lastRepoID := -1
	for docID := uint32(0); int(docID) < len(d.fileBranchMasks); docID++ {
		repoID := int(d.repos[docID])

		if d.repoMetaData[repoID].Tombstone {
			continue
		}

		if repoID != lastRepoID {
			if lastRepoID > repoID {
				return shardNames, fmt.Errorf("non-contiguous repo ids in %s for document %d: old=%d current=%d", d.String(), docID, lastRepoID, repoID)
			}
			lastRepoID = repoID

			if ib != nil {
				if err := writeShard(ib); err != nil {
					return shardNames, err
				}
			}

			ib = newIndexBuilder()
			ib.indexFormatVersion = IndexFormatVersion
			if err := ib.setRepository(&d.repoMetaData[repoID]); err != nil {
				return shardNames, err
			}
		}

		err := addDocument(d, ib, repoID, docID)
		if err != nil {
			return shardNames, err
		}
	}

	if ib != nil {
		if err := writeShard(ib); err != nil {
			return shardNames, err
		}
	}

	return shardNames, nil
}

func addDocument(d *indexData, ib *IndexBuilder, repoID int, docID uint32) error {
	doc := Document{
		Name: string(d.fileName(docID)),
		// Content set below since it can return an error
		// Branches set below since it requires lookups
		SubRepositoryPath: d.subRepoPaths[repoID][d.subRepos[docID]],
		Language:          d.languageMap[d.getLanguage(docID)],
		// SkipReason not set, will be part of content from original indexer.
	}

	var err error
	if doc.Content, err = d.readContents(docID); err != nil {
		return err
	}

	if doc.Symbols, _, err = d.readDocSections(docID, nil); err != nil {
		return err
	}

	doc.SymbolsMetaData = make([]*Symbol, len(doc.Symbols))
	for i := range doc.SymbolsMetaData {
		doc.SymbolsMetaData[i] = d.symbols.data(d.fileEndSymbol[docID] + uint32(i))
	}

	// calculate branches
	{
		mask := d.fileBranchMasks[docID]
		id := uint32(1)
		for mask != 0 {
			if mask&0x1 != 0 {
				doc.Branches = append(doc.Branches, d.branchNames[repoID][uint(id)])
			}
			id <<= 1
			mask >>= 1
		}
	}
	return ib.Add(doc)
}

// copied from builder package to avoid circular imports.
func hashString(s string) string {
	h := sha1.New()
	_, _ = io.WriteString(h, s)
	return fmt.Sprintf("%x", h.Sum(nil))
}
