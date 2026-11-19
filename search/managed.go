package search

import (
	"runtime"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
)

// ManagedSearcher searches shards that its caller loads and retires, instead
// of shards found in a watched directory. It lets a caller publish a set of
// shards, such as every shard of one repository, in a single step.
type ManagedSearcher struct {
	zoekt.Streamer
	ss *shardedSearcher
}

// NewManagedSearcher returns a ManagedSearcher with no shards. It reports
// itself ready from the start: the caller decides when its shards are
// complete.
func NewManagedSearcher() *ManagedSearcher {
	ss := newShardedSearcher(int64(runtime.GOMAXPROCS(0)))
	ss.markReady()
	return &ManagedSearcher{
		Streamer: &typeRepoSearcher{Streamer: ss},
		ss:       ss,
	}
}

// Replace adds, replaces and removes shards in one step: a search sees either
// all of the changes or none of them. Keys identify shards, such as their
// paths; a nil searcher removes the shard with that key. A replaced or removed
// shard is closed once no search uses it anymore.
func (m *ManagedSearcher) Replace(shards map[string]zoekt.Searcher) {
	m.ss.replace(shards)
}

// LoadShard opens the shard file at path for searching. It reads the shard's
// ".meta" sidecar, if any, at the same time.
func LoadShard(path string) (zoekt.Searcher, error) {
	return loadShard(path, index.NewIndexFile)
}
