package index

import (
	"errors"

	"github.com/go-enry/go-enry/v2"
)

// FileCategory represents the category of a file, as determined by go-enry. It is non-exhaustive
// but tries to the major cases like whether the file is a test, generated, etc.
//
// A file's category is used in search scoring to determine the weight of a file match.
type FileCategory byte

const (
	// FileCategoryMissing is a sentinel value that indicates we never computed the file category during indexing
	// (which means we're reading from an old index version). This value can never be written to the index.
	FileCategoryMissing FileCategory = iota
	FileCategoryDefault
	FileCategoryTest
	FileCategoryVendored
	FileCategoryGenerated
	FileCategoryConfig
	FileCategoryDotFile
	FileCategoryDocumentation
)

func DetermineFileCategory(doc *Document) {
	name := doc.Name
	content := doc.Content

	// If this document has been skipped, it's likely very large. In this case, we just guess the category based
	// on the filename to avoid examining the contents. Note: passing nil content is allowed by the go-enry contract.
	if doc.SkipReason != "" {
		content = nil
	}

	category := FileCategoryDefault
	if enry.IsTest(name) {
		category = FileCategoryTest
	} else if enry.IsDotFile(name) {
		category = FileCategoryDotFile
	} else if enry.IsVendor(name) {
		category = FileCategoryVendored
	} else if enry.IsGenerated(name, content) {
		category = FileCategoryGenerated
	} else if enry.IsConfiguration(name) {
		category = FileCategoryConfig
	} else if enry.IsDocumentation(name) {
		category = FileCategoryDocumentation
	}

	doc.Category = category
}

// lowPriority returns true if this file category is considered 'low priority'. This is used
// in search scoring to down-weight matches in these files.
func (c FileCategory) lowPriority() bool {
	return c == FileCategoryTest || c == FileCategoryVendored || c == FileCategoryGenerated
}

func (c FileCategory) encode() (byte, error) {
	switch c {
	case FileCategoryMissing:
		return 0, errors.New("cannot encode missing file category")
	case FileCategoryDefault:
		return 1, nil
	case FileCategoryTest:
		return 2, nil
	case FileCategoryVendored:
		return 3, nil
	case FileCategoryGenerated:
		return 4, nil
	case FileCategoryConfig:
		return 5, nil
	case FileCategoryDotFile:
		return 6, nil
	case FileCategoryDocumentation:
		return 7, nil
	default:
		return 0, errors.New("unrecognized file category")
	}
}

func decodeCategory(c byte) (FileCategory, error) {
	switch c {
	case 1:
		return FileCategoryDefault, nil
	case 2:
		return FileCategoryTest, nil
	case 3:
		return FileCategoryVendored, nil
	case 4:
		return FileCategoryGenerated, nil
	case 5:
		return FileCategoryConfig, nil
	case 6:
		return FileCategoryDotFile, nil
	case 7:
		return FileCategoryDocumentation, nil
	default:
		return FileCategoryMissing, errors.New("unrecognized file category")
	}
}
