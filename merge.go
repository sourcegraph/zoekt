package zoekt

import (
	"crypto/sha1"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

// Merge files into a compound shard fn in the directory dstDir.
func Merge(dstDir string, files ...IndexFile) (fn string, _ error) {
	var ds []*indexData
	for _, f := range files {
		searcher, err := NewSearcher(f)
		if err != nil {
			return "", err
		}
		ds = append(ds, searcher.(*indexData))
	}

	ib, err := merge(ds...)
	if err != nil {
		return "", err
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

	fn = filepath.Join(dstDir, fmt.Sprintf("compound-%x_v%d.%05d.zoekt", hasher.Sum(nil), NextIndexFormatVersion, 0))
	if err := builderWriteAll(fn, ib); err != nil {
		return "", err
	}
	return fn, nil
}

func builderWriteAll(fn string, ib *IndexBuilder) error {
	dir := filepath.Dir(fn)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	f, err := ioutil.TempFile(dir, filepath.Base(fn)+".*.tmp")
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

	log.Printf("finished %s: %d index bytes (overhead %3.1f)", fn, fi.Size(),
		float64(fi.Size())/float64(ib.ContentSize()+1))

	return nil
}

func merge(ds ...*indexData) (*IndexBuilder, error) {
	if len(ds) == 0 {
		return nil, fmt.Errorf("need 1 or more indexData to merge")
	}

	sort.Slice(ds, func(i, j int) bool {
		return ds[i].repoMetaData[0].priority > ds[j].repoMetaData[0].priority
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
				return nil, err
			}

			if doc.Symbols, _, err = d.readDocSections(docID, nil); err != nil {
				return nil, err
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

			if err := ib.Add(doc); err != nil {
				return nil, err
			}
		}
	}

	return ib, nil
}

// Explode takes an IndexFile f and creates 1 simple shard per repository f
// contains. The new shard(s) will be returned with a ".tmp" suffix. It is the
// responsibility of the caller to rename the output shards and delete the input
// shard.
func Explode(dstDir string, f IndexFile) ([]string, error) {
	searcher, err := NewSearcher(f)
	if err != nil {
		return nil, err
	}

	ibs, err := explode(searcher.(*indexData))
	if err != nil {
		return nil, err
	}

	shardNames := make([]string, 0, len(ibs))
	for _, ib := range ibs {
		if len(ib.repoList) != 1 {
			return shardNames, fmt.Errorf("expected each IndexBuilder to contain exactly 1 repository")
		}
		fn := filepath.Join(dstDir, shardName(ib.repoList[0].Name, ib.indexFormatVersion, 0))
		fn = fn + ".tmp"
		shardNames = append(shardNames, fn)
		if err := builderWriteAll(fn, ib); err != nil {
			return shardNames, err
		}
	}
	return shardNames, nil
}

func explode(d *indexData) ([]*IndexBuilder, error) {
	var ib *IndexBuilder
	ibs := make([]*IndexBuilder, 0, len(d.repoMetaData))

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

			if ib != nil {
				ibs = append(ibs, ib)
			}

			ib = newIndexBuilder()
			ib.indexFormatVersion = IndexFormatVersion
			if err := ib.setRepository(&d.repoMetaData[repoID]); err != nil {
				return nil, err
			}
		}

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
			return nil, err
		}

		if doc.Symbols, _, err = d.readDocSections(docID, nil); err != nil {
			return nil, err
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

		if err := ib.Add(doc); err != nil {
			return nil, err
		}
	}

	if ib != nil {
		ibs = append(ibs, ib)
	}
	return ibs, nil
}

// copied from builder package to avoid circular imports.
func hashString(s string) string {
	h := sha1.New()
	_, _ = io.WriteString(h, s)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// copied from builder package to avoid circular imports.
func shardName(name string, version, n int) string {
	abs := url.QueryEscape(name)
	if len(abs) > 200 {
		abs = abs[:200] + hashString(abs)[:8]
	}
	return fmt.Sprintf("%s_v%d.%05d.zoekt", abs, version, n)
}
