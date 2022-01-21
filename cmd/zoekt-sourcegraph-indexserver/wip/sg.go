package wipindexserver

import "context"

type Sourcegraph interface {
	List(ctx context.Context, indexed []uint32) (*SourcegraphListResult, error)

	// ForceIterateIndexOptions will best-effort calculate the index options for
	// all of ids. If any repository fails it internally logs.
	ForceIterateIndexOptions(func(IndexOptions), ...uint32)

	// GetIndexOptions is deprecated but kept around until we improve our
	// forceIndex code.
	GetIndexOptions(repos ...uint32) ([]IndexOptionsItem, error)
}

type SourcegraphListResult struct {
	// IDs is the set of Sourcegraph repository IDs that this replica needs
	// to index.
	IDs []uint32

	// IterateIndexOptions best effort resolves the IndexOptions for RepoIDs. If
	// any repository fails it internally logs.
	//
	// Note: this has a side-effect of setting a the "config fingerprint". The
	// config fingerprint means we only calculate index options for repositories
	// that have changed since the last call to IterateIndexOptions. If you want
	// to force calculation of index options use Sourcegraph.GetIndexOptions.
	IterateIndexOptions func(func(IndexOptions))
}

// IndexOptionsItem wraps IndexOptions to also include an error returned by
// the API.
type IndexOptionsItem struct {
	IndexOptions
	Error string
}
