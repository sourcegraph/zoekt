package index

import "github.com/sourcegraph/zoekt"

// Document holds a document (file) to index.
type Document struct {
	Name              string
	Content           []byte
	Branches          []string
	SubRepositoryPath string
	Language          string
	Category          FileCategory

	SkipReason SkipReason

	// Document sections for symbols. Offsets should use bytes.
	Symbols         []DocumentSection
	SymbolsMetaData []*zoekt.Symbol
}

type SkipReason int

const (
	SkipReasonNone SkipReason = iota
	SkipReasonTooLarge
	SkipReasonTooSmall
	SkipReasonBinary
	SkipReasonTooManyTrigrams
	SkipReasonMissing
)

func (s SkipReason) explanation() string {
	switch s {
	case SkipReasonNone:
		return ""
	case SkipReasonTooLarge:
		return "exceeds the maximum size limit"
	case SkipReasonTooSmall:
		return "contains too few trigrams"
	case SkipReasonBinary:
		return "contains binary content"
	case SkipReasonTooManyTrigrams:
		return "contains too many trigrams"
	case SkipReasonMissing:
		return "object missing from repository"
	default:
		return "unknown skip reason"
	}
}

type DocumentSection struct {
	Start, End uint32
}
