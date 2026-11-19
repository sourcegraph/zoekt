package search

import (
	"context"
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/internal/shardtest"
	"github.com/sourcegraph/zoekt/internal/tenant/systemtenant"
	"github.com/sourcegraph/zoekt/query"
)

func TestManagedSearcherReplacesShardSetsAtOnce(t *testing.T) {
	ctx := systemtenant.WithUnsafeContext(context.Background())
	shard := func(id uint32, version string, files ...string) zoekt.Searcher {
		var docs []index.Document
		for _, f := range files {
			docs = append(docs, index.Document{Name: f, Content: []byte("needle " + version)})
		}
		return shardtest.Searcher(t, shardtest.Builder(t, &zoekt.Repository{ID: id, Name: "repo"}, docs...))
	}
	search := func(m *ManagedSearcher) []string {
		t.Helper()
		res, err := m.Search(ctx, &query.Substring{Pattern: "needle"}, &zoekt.SearchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, f := range res.Files {
			names = append(names, f.FileName)
		}
		return names
	}

	m := NewManagedSearcher()
	defer m.Close()
	if got := search(m); len(got) != 0 {
		t.Fatalf("empty searcher found %v", got)
	}

	// A generation of two shards becomes visible together.
	m.Replace(map[string]zoekt.Searcher{
		"g1/0": shard(1, "v1", "a.go"),
		"g1/1": shard(1, "v1", "b.go"),
	})
	if got := search(m); len(got) != 2 {
		t.Fatalf("after adding generation 1: got %v, want a.go and b.go", got)
	}

	// Replacing it with a one-shard generation removes both old shards in the
	// same step, so no search sees old and new documents together.
	m.Replace(map[string]zoekt.Searcher{
		"g1/0": nil,
		"g1/1": nil,
		"g2/0": shard(1, "v2", "c.go"),
	})
	got := search(m)
	if len(got) != 1 || got[0] != "c.go" {
		t.Fatalf("after replacing with generation 2: got %v, want only c.go", got)
	}
}
