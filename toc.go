// Copyright 2017 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package zoekt

// IndexFormatVersion is a version number. It is increased every time the
// on-disk index format is changed.
// 5: subrepositories.
// 6: remove size prefix for posting varint list.
// 7: move subrepos into Repository struct.
// 8: move repoMetaData out of indexMetadata
// 9: use bigendian uint64 for trigrams.
// 10: sections for rune offsets.
// 11: file ends in rune offsets.
// 12: 64-bit branchmasks.
// 13: content checksums
// 14: languages
// 15: rune based symbol sections
// 16: ctags metadata
const IndexFormatVersion = 16

// FeatureVersion is increased if a feature is added that requires reindexing data
// without changing the format version
// 2: Rank field for shards.
// 3: Rank documents within shards
// 4: Dedup file bugfix
// 5: Remove max line size limit
// 6: Include '#' into the LineFragment template
// 7: Record skip reasons in the index.
// 8: Record source path in the index.
// 9: Store ctags metadata
const FeatureVersion = 9

func init() {
	ensureSourcegraphSymbolsHack()
}

func ensureSourcegraphSymbolsHack() {
	if IndexFormatVersion != 16 {
		panic(`Sourcegraph: While we are on version 16 we have added code into
	read.go which supports reading IndexFormatVersion 15. If you change the
	IndexFormatVersion please reach out to Kevin and Keegan.`)
	}
	if FeatureVersion != 9 {
		panic(`Sourcegraph: While we are on FeatureVersion 9 we have added code into
	read.go which supports reading FeatureVersion 8. If you change the
	FeatureVersion please reach out to Kevin and Keegan.`)
	}
}

type indexTOC struct {
	fileContents compoundSection
	fileNames    compoundSection
	fileSections compoundSection
	postings     compoundSection
	newlines     compoundSection
	ngramText    simpleSection
	runeOffsets  simpleSection
	fileEndRunes simpleSection
	languages    simpleSection

	fileEndSymbol  simpleSection
	symbolMap      compoundSection
	symbolKindMap  compoundSection
	symbolMetaData simpleSection

	branchMasks simpleSection
	subRepos    simpleSection

	nameNgramText    simpleSection
	namePostings     compoundSection
	nameRuneOffsets  simpleSection
	metaData         simpleSection
	repoMetaData     simpleSection
	nameEndRunes     simpleSection
	contentChecksums simpleSection
	runeDocSections  simpleSection
}

func (t *indexTOC) sectionsHACK(expectedSectionCount uint32) []section {
	ensureSourcegraphSymbolsHack()

	// Sourcegraph hack for v15.
	if expectedSectionCount == 19 {
		return []section{
			// This must be first, so it can be reliably read across
			// file format versions.
			&t.metaData,
			&t.repoMetaData,
			&t.fileContents,
			&t.fileNames,
			&t.fileSections,
			&t.newlines,
			&t.ngramText,
			&t.postings,
			&t.nameNgramText,
			&t.namePostings,
			&t.branchMasks,
			&t.subRepos,
			&t.runeOffsets,
			&t.nameRuneOffsets,
			&t.fileEndRunes,
			&t.nameEndRunes,
			&t.contentChecksums,
			&t.languages,
			&t.runeDocSections,
		}
	}

	return t.sections()
}

func (t *indexTOC) sections() []section {
	return []section{
		// This must be first, so it can be reliably read across
		// file format versions.
		&t.metaData,
		&t.repoMetaData,
		&t.fileContents,
		&t.fileNames,
		&t.fileSections,
		&t.fileEndSymbol,
		&t.symbolMap,
		&t.symbolKindMap,
		&t.symbolMetaData,
		&t.newlines,
		&t.ngramText,
		&t.postings,
		&t.nameNgramText,
		&t.namePostings,
		&t.branchMasks,
		&t.subRepos,
		&t.runeOffsets,
		&t.nameRuneOffsets,
		&t.fileEndRunes,
		&t.nameEndRunes,
		&t.contentChecksums,
		&t.languages,
		&t.runeDocSections,
	}
}
