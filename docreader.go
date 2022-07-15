package zoekt

import (
	"fmt"
	"log"
	"os"
	"sync"
)

// DocReader provides a non-search based api
// for reading documents from shards.
//
// Close() should be called on DocReader to close opened shards
//
// TODO(jac): Support compound shards
type DocReader struct {
	indexData []*indexData
}

func NewDocReader(shards []string) (*DocReader, error) {
	dr := &DocReader{
		indexData: make([]*indexData, 0, len(shards)),
	}
	// Open each shard and read indexData
	for _, shard := range shards {
		f, err := os.Open(shard)
		if err != nil {
			dr.Close()
			return nil, fmt.Errorf("coudln't open shard %s; %s", shard, err)
		}

		r, err := NewIndexFile(f)
		if err != nil {
			f.Close()
			dr.Close()
			return nil, fmt.Errorf("couldn't create index file; %s", err)
		}
		rd := &reader{r: r}

		var toc indexTOC
		if err := rd.readTOC(&toc); err != nil {
			r.Close()
			dr.Close()
			return nil, err
		}

		indexData, err := rd.readIndexData(&toc)
		if err != nil {
			indexData.Close()
			dr.Close()
			return nil, err
		}
		dr.indexData = append(dr.indexData, indexData)
	}

	return dr, nil
}

func (d *DocReader) ReadDocs(filename string) []Document {
	// Can have at most one document per branch
	docs := make([]Document, 0, len(d.indexData[0].branchNames))

	var wg sync.WaitGroup
	c := make(chan Document)
	for i := 0; i < len(d.indexData); i++ {
		i := i
		// Check if filename is tombstoned in this shard
		if _, t := d.indexData[i].repoMetaData[0].FileTombstones[filename]; t {
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			findDocsInIndexData(filename, d.indexData[i], c)
		}()
	}

	go func() {
		wg.Wait()
		close(c)
	}()

	for {
		doc, ok := <-c
		if ok {
			docs = append(docs, doc)
		} else {
			break
		}
	}

	return docs
}

func findDocsInIndexData(name string, d *indexData, c chan Document) {
	for i := 0; i < len(d.fileNameIndex)-1; i++ {
		filename := d.fileName(uint32(i))
		// compiler does not allocate for the comparison
		if len(name) == len(filename) && name == string(filename) {
			name := string(filename)
			content, err := d.readContents(uint32(i))
			if err != nil {
				log.Panicf("Couldn't read document %s in shard %s", name, d)
			}

			// Parse individual branch names from branch mask
			branches := branchNamesFromMask(uint32(i), d)

			d := Document{
				Name:     name,
				Content:  content,
				Branches: branches,
			}

			c <- d
		}
	}
}

func branchNamesFromMask(fileIndex uint32, d *indexData) []string {
	branches := make([]string, 0, len(d.branchNames))
	mask := d.fileBranchMasks[fileIndex]
	id := uint32(1)
	for mask != 0 {
		if mask&0x1 != 0 {
			branches = append(branches, d.branchNames[0][uint(id)])
		}
		id <<= 1
		mask >>= 1
	}
	return branches
}

// Close shards opened by the RepoDocReader. Should only be called once
func (d *DocReader) Close() {
	for _, shard := range d.indexData {
		shard.Close()
	}
}

// ListDocs lists all documents and their branches across all loaded shards
//
// For debugging purposes
func (d *DocReader) ListDocs() {
	for _, shard := range d.indexData {
		fmt.Println(shard.file.Name())
		for i := 0; i < len(shard.fileNameIndex)-1; i++ {
			filename := shard.fileName(uint32(i))
			branches := branchNamesFromMask(uint32(i), shard)
			fmt.Printf("%s %s\n", filename, branches)
		}
		fmt.Println()
	}
}
