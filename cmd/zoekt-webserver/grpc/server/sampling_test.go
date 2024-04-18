package server

import (
	"testing"

	"github.com/sourcegraph/zoekt"
)

func TestSamplingStream(t *testing.T) {
	nonZeroStats := zoekt.Stats{
		ContentBytesLoaded: 10,
	}
	filesEvent := &zoekt.SearchResult{
		Files: make([]zoekt.FileMatch, 10),
		Stats: nonZeroStats,
	}
	fileEvents := func(n int) []*zoekt.SearchResult {
		res := make([]*zoekt.SearchResult, n)
		for i := 0; i < n; i++ {
			res[i] = filesEvent
		}
		return res
	}
	statsEvent := &zoekt.SearchResult{
		Stats: nonZeroStats,
	}
	statsEvents := func(n int) []*zoekt.SearchResult {
		res := make([]*zoekt.SearchResult, n)
		for i := 0; i < n; i++ {
			res[i] = statsEvent
		}
		return res
	}
	cases := []struct {
		events           []*zoekt.SearchResult
		beforeFlushCount int
		afterFlushCount  int
	}{
		// These test cases assume that the sampler only forwards
		// every 100 stats-only event. In case the sampling logic
		// changes, these tests are not valuable.
		{nil, 0, 0},
		{fileEvents(1), 1, 1},
		{fileEvents(2), 2, 2},
		{fileEvents(200), 200, 200},
		{append(fileEvents(1), statsEvents(1)...), 1, 2},
		{append(fileEvents(1), statsEvents(2)...), 1, 2},
		{append(fileEvents(1), statsEvents(99)...), 1, 2},
		{append(fileEvents(1), statsEvents(100)...), 2, 2},
		{statsEvents(500), 5, 5},
		{statsEvents(501), 5, 6},
	}

	for _, tc := range cases {
		count := 0
		ss := newSamplingSender(zoekt.SenderFunc(func(*zoekt.SearchResult) {
			count += 1
		}))

		for _, event := range tc.events {
			ss.Send(event)
		}
		if count != tc.beforeFlushCount {
			t.Fatalf("expected %d events, got %d", tc.beforeFlushCount, count)
		}
		ss.Flush()

		if count != tc.afterFlushCount {
			t.Fatalf("expected %d events, got %d", tc.afterFlushCount, count)
		}
	}
}
