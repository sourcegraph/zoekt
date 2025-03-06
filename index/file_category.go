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
	FileCategoryMissing FileCategory = iota
	FileCategoryDefault
	FileCategoryTest
	FileCategoryVendored
	FileCategoryGenerated
	FileCategoryConfig
	FileCategoryDotFile
	FileCategoryDocumentation
	// FileCategoryMissing is a sentinel value that indicates we never computed the file category during indexing
	// (which means we're reading from an old index version). This value can never be written to the index.
)

func DetermineFileCategory(name string, content []byte) FileCategory {
	// Note: the order of these checks is important.
	// - IsDotFile must come before IsVendor
	if enry.IsTest(name) {
		return FileCategoryTest
	} else if enry.IsDotFile(name) {
		return FileCategoryDotFile
	} else if enry.IsVendor(name) {
		return FileCategoryVendored
	} else if enry.IsGenerated(name, content) {
		return FileCategoryGenerated
	} else if enry.IsConfiguration(name) {
		return FileCategoryConfig
	} else if enry.IsDotFile(name) {
		return FileCategoryDotFile
	} else if enry.IsDocumentation(name) {
		return FileCategoryDocumentation
	}
	return FileCategoryDefault
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
