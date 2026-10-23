// Package shardtest builds in-memory shards for tests.
//
// It imports index, so internal tests of package index cannot use it.
package shardtest

import (
	"bytes"
	"context"
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/query"
)

// Builder returns a ShardBuilder for repo with docs added.
func Builder(tb testing.TB, repo *zoekt.Repository, docs ...index.Document) *index.ShardBuilder {
	tb.Helper()

	b, err := index.NewShardBuilder(repo)
	if err != nil {
		tb.Fatalf("NewShardBuilder: %v", err)
	}
	for i, d := range docs {
		if err := b.Add(d); err != nil {
			tb.Fatalf("Add %d: %v", i, err)
		}
	}
	return b
}

// Searcher writes b to memory and returns a searcher over the shard.
func Searcher(tb testing.TB, b *index.ShardBuilder) zoekt.Searcher {
	tb.Helper()

	var buf bytes.Buffer
	if err := b.Write(&buf); err != nil {
		tb.Fatal(err)
	}
	s, err := index.NewSearcher(IndexFile(buf.Bytes()))
	if err != nil {
		tb.Fatalf("NewSearcher: %v", err)
	}
	return s
}

// IndexFile returns an index.IndexFile that reads from data without copying
// it.
func IndexFile(data []byte) index.IndexFile {
	return memIndexFile(data)
}

type memIndexFile []byte

func (f memIndexFile) Read(off, sz uint32) ([]byte, error) { return f[off : off+sz], nil }
func (f memIndexFile) Size() (uint32, error)               { return uint32(len(f)), nil }
func (f memIndexFile) Close()                              {}
func (f memIndexFile) Name() string                        { return "shardtest" }

// Streamer implements zoekt.Streamer for a Searcher by sending the result of
// Search as a single event.
type Streamer struct {
	zoekt.Searcher
}

// StreamSearch implements zoekt.Streamer.
func (s Streamer) StreamSearch(ctx context.Context, q query.Q, opts *zoekt.SearchOptions, sender zoekt.Sender) error {
	sr, err := s.Search(ctx, q, opts)
	if err != nil {
		return err
	}
	sender.Send(sr)
	return nil
}
