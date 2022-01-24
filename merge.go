package zoekt

import (
	"crypto/sha1"
	"fmt"
	"io/ioutil"
	"log"
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

type TODO = interface{}

type Diff struct {
	Added, Modified, Deleted []TODO
}

var getDiff func(oldBranches, newBranches []RepositoryBranch) Diff
var setTombstones func([]*indexData, []TODO) []string
var createNewShard func(shardNum int, []TODO) string

// imagine ds is all the current shards for the repo we want to update. So it
// contains all content/computed indexes for the older commit(s). It also
// tells us what the older commits are.
func merge(ds []*indexData, newBranches []RepositoryBranch) (*IndexBuilder, error) {
	if len(ds) == 0 {
		return nil, fmt.Errorf("need 1 or more indexData to merge")
	}

	sort.Slice(ds, func(i, j int) bool {
		return ds[i].repoMetaData[0].priority > ds[j].repoMetaData[0].priority
	})

	ib := newIndexBuilder()
	ib.indexFormatVersion = NextIndexFormatVersion

	// Approach 1: use tombstones
	{
		old := ds[0].repoMetaData[0].Branches
		diff := getDiff(old, newBranches)

		// on disk before zoekt-git-index: foo.0.shard foo.1.shard
		// first step: zoekt-git-index creates: foo.0.shard.tmp, foo.1.shard.tmp which is the index
		//
		// once created all files, is os.Renames tmp into dst. Candidatefiles is
		// modelling the same concept to avoid synchronization between
		// zoekt-git-index and zoekt-webserver.

		var candidateFiles []string

		candidateFiles = append(candidateFiles, setTombstones(ds, diff.Deleted)...)
		candidateFiles = append(candidateFiles, setTombstones(ds, diff.Modified)...)

		// shards are numbers from 0 -> len(ds)-1. We want a new shard which only
		// contains the new and modified content.
		modifiedAndAddedShardNum := len(ds)
		candidateFiles = append(candidateFiles, createNewShard(modifiedAndAddedShardNum, append(append([]TODO, diff.Added...), diff.Modified...)))

		renameToDst(candidateFiles)

		// con: we are going to end up with a very small modifiedAndAddedShardNum most
		// of the time (ie much less than 100mb). We likely will have poor
		// behaviour if we have 150 modifiedAndAddedShards, and need some sort of
		// compaction like we do with periodic running of compound shard merging.
		//
		// con: right now when tombstoning a document, we tombstone _every_
		// document for that repo. Will need to audit/fix this assumption. I don't
		// think this is a big deal, both Stefan and Keegan have context to
		// quickly audit with you.
		//
		// con: unclear how to update metadata which contains branch information.
		// Right now we do an assert that the metadata for branches inside the
		// shard (immutable) is the same as the metadata in the mutable file.
		//
		//   shard -> foo.1.shard and this is immutable
		//   meta  -> foo.1.shard.meta and this is mutable
		//   on load we read in meta from shard, then overwrite the mutable data.
		//   We check some invariants to ensure the meta is valid for the shard
		//   (mainly branch versions).
		//
		// con: likely less clear interface for how we fetch diff data. IE this
		// probably wants to live in zoekt-git-index so may want soem fancy go-git
		// stuff. This might actually be a pro in the long term.
		//
		// pro: this is likely the fastest incremental approach since the only
		// IO it does is for files changed.
	}

	// Approach 2: do like zoekt-merge-index does, and just recreate the whole
	// shard but avoid communicating with gitserver. (but we will speak to it
	// for the diff).
	//
	// Approach 2 stubs will now follow inline.
	//
	// pro: simple implementation. Lots of possible ways to implement oracle.
	//
	// con: more file IO than (1) since we need to rewrite all the shards, not
	// just IO that is proportional to the changes.
	//
	// con: approach 1 is likely part of zoekt-git-index, while this will likely
	// be a new binary or improving zoekt-merge-index. This might not be that
	// bad, since we rely on zoekt-merge-index for compound shards.
	//
	// con: multiple shards make this more complicated, which we will have for
	// monorepos.

	for _, d := range ds {
		lastRepoID := -1
		for docID := uint32(0); int(docID) < len(d.fileBranchMasks); docID++ {
			repoID := int(d.repos[docID])

			if d.repoMetaData[repoID].Tombstone {
				continue
			}

			// SKIPPING If this document is changed or deleted, we need to either drop or
			// recompute later.
			if diff.Contains(d.fileName(docID), d.branchInfo(docID)) {
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

			// calculate branches - alternatively, instead of skipping we can
			// compute what the new branches if for the old version of the document.
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

	// 3 branches: HEAD, dev, release
	// old: main.go is the same on all branches
	// new: main.go has only changed in dev
	// diff must contains:
	// - main.go for HEAD and release
	// - main.go for dev
	// because we have skipped indexing it since its old branch information
	// changed, and that is immutable in the shard.

	for _, doc := range diff.Added {
		ib.Add(doc)
	}

	for _, doc := range diff.Modified {
		ib.Add(doc)
	}

	return ib, nil
}
