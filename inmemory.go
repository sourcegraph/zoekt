package zoekt

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
)

// This file contains a Sourcegraph specific extension which allows loading
// parts of a shard into memory. This is to experiment with avoiding the OS
// page cache misses and drops.

func sourcegraphInMemoryContent(toc *indexTOC, file IndexFile) (IndexFile, error) {
	if len(inMemoryContentFields) == 0 {
		return file, nil
	}

	return newCachedIndexFile(toc, inMemoryContentFields, file)
}

var inMemoryContentFields []string

func init() {
	var logs []string
	inMemoryContentFields, logs = parseInMemoryContentVar(os.Getenv("IN_MEMORY_CONTENT"))
	for _, l := range logs {
		log.Println(l)
	}
}

func parseInMemoryContentVar(s string) (fields, logs []string) {
	if s == "" {
		return nil, nil
	}

	valid := allSimpleSections(&indexTOC{})
	var invalid []string
	for _, field := range strings.Split(s, ",") {
		if _, ok := valid[field]; ok {
			fields = append(fields, field)
		} else {
			invalid = append(invalid, field)
		}
	}

	var validSlice []string
	for field := range valid {
		validSlice = append(validSlice, field)
	}
	sort.Strings(validSlice)

	logs = []string{
		fmt.Sprintf("INFO: IN_MEMORY_CONTENT environment variable enabled for %s.", strings.Join(fields, ",")),
		`INFO: IN_MEMORY_CONTENT will pin parts of the search database into memory to avoid paging out to disk in case of an OS page cache miss (at increased memory cost).`,
		`INFO: IN_MEMORY_CONTENT some fields are always pinned in, notably "filecontents" and "postings" are not.`,
		`INFO: IN_MEMORY_CONTENT is a "," joined list of fields.`,
		fmt.Sprintf("INFO: IN_MEMORY_CONTENT valid fields are %s.", strings.Join(validSlice, ",")),
	}

	if len(invalid) > 0 {
		logs = append(logs, fmt.Sprintf("WARN: IN_MEMORY_CONTENT ignoring invalid fields: %s.", strings.Join(invalid, ",")))
	}

	return fields, logs
}

func allSimpleSections(t *indexTOC) map[string]simpleSection {
	return map[string]simpleSection{
		"filecontents":     t.fileContents.data,
		"filenames":        t.fileNames.data,
		"filesections":     t.fileSections.data,
		"postings":         t.postings.data,
		"newlines":         t.newlines.data,
		"ngramtext":        t.ngramText,
		"runeoffsets":      t.runeOffsets,
		"fileendrunes":     t.fileEndRunes,
		"languages":        t.languages,
		"fileendsymbol":    t.fileEndSymbol,
		"symbolmap":        t.symbolMap.data,
		"symbolkindmap":    t.symbolKindMap.data,
		"symbolmetadata":   t.symbolMetaData,
		"branchmasks":      t.branchMasks,
		"subrepos":         t.subRepos,
		"namengramtext":    t.nameNgramText,
		"namepostings":     t.namePostings.data,
		"nameruneoffsets":  t.nameRuneOffsets,
		"metadata":         t.metaData,
		"repometadata":     t.repoMetaData,
		"nameendrunes":     t.nameEndRunes,
		"contentchecksums": t.contentChecksums,
		"runedocsections":  t.runeDocSections,
	}
}

func newCachedIndexFile(toc *indexTOC, fields []string, file IndexFile) (IndexFile, error) {
	sections := allSimpleSections(toc)
	c := &cachedIndexFile{file: file}
	for _, field := range fields {
		section := sections[field]
		if err := c.cache(section); err != nil {
			return nil, fmt.Errorf("failed to load section %s=%v for %s: %w", field, section, file.Name(), err)
		}
		log.Printf("DBUG: %s: pinned into memory section %s (%2.fMB)", file.Name(), field, float64(section.sz)/1024/1024)
	}
	return c, nil
}

type cachedBlock struct {
	off  uint32
	data []byte
}

type cachedIndexFile struct {
	file   IndexFile
	blocks []cachedBlock
}

func (c *cachedIndexFile) cache(ss simpleSection) error {
	b, err := c.file.Read(ss.off, ss.sz)
	if err != nil {
		return err
	}

	// copy to ensure in memory (likely pointing into mmap region)
	data := make([]byte, ss.sz)
	copy(data, b)

	c.blocks = append(c.blocks, cachedBlock{
		off:  ss.off,
		data: data,
	})
	return nil
}

func (c *cachedIndexFile) Read(off uint32, sz uint32) ([]byte, error) {
	for _, block := range c.blocks {
		if off < block.off {
			continue
		}
		relOff := off - block.off
		relEnd := relOff + sz
		if relEnd <= uint32(len(block.data)) {
			return block.data[relOff:relEnd], nil
		}
	}
	return c.file.Read(off, sz)
}
func (c *cachedIndexFile) Size() (uint32, error) {
	return c.file.Size()
}
func (c *cachedIndexFile) Close() {
	c.blocks = nil
	c.file.Close()
}

func (c *cachedIndexFile) Name() string {
	var b strings.Builder
	b.WriteString("Cached{")
	b.WriteString(c.file.Name())
	b.WriteString(", Blocks: ")
	for i, block := range c.blocks {
		if i != 0 {
			b.WriteString(", ")
		}
		_, _ = fmt.Fprintf(&b, "{%d %d}", block.off, len(block.data))
	}
	b.WriteString("}")
	return b.String()
}
