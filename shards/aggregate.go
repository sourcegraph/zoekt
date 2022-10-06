package shards

import (
	"github.com/sourcegraph/zoekt"
)

// collectSender is a sender that will aggregate results. Once sending is
// done, you call Done to returned the aggregated result which are ranked.
//
// Note: It aggregates Progress as well, and expects that the
// MaxPendingPriority it receives are monotonically decreasing.
type collectSender struct {
	aggregate          *zoekt.SearchResult
	maxDocDisplayCount int
}

func newCollectSender(opts *zoekt.SearchOptions) *collectSender {
	return &collectSender{
		maxDocDisplayCount: opts.MaxDocDisplayCount,
	}
}

func (c *collectSender) Send(r *zoekt.SearchResult) {
	if c.aggregate == nil {
		c.aggregate = &zoekt.SearchResult{
			RepoURLs:      map[string]string{},
			LineFragments: map[string]string{},
		}
	}

	c.aggregate.Stats.Add(r.Stats)

	if len(r.Files) > 0 {
		c.aggregate.Files = append(c.aggregate.Files, r.Files...)

		for k, v := range r.RepoURLs {
			c.aggregate.RepoURLs[k] = v
		}
		for k, v := range r.LineFragments {
			c.aggregate.LineFragments[k] = v
		}
	}

	// The priority of our aggregate is the largest priority we collect.
	if c.aggregate.Priority < r.Priority {
		c.aggregate.Priority = r.Priority
	}

	// We receive monotonically decreasing values, so we update on every call.
	c.aggregate.MaxPendingPriority = r.MaxPendingPriority
}

// Done returns the aggregated result. Before returning them the files are
// ranked and truncated according to the input SearchOptions.
//
// If no results are aggregated, ok is false and the result is nil.
func (c *collectSender) Done() (_ *zoekt.SearchResult, ok bool) {
	if c.aggregate == nil {
		return nil, false
	}

	agg := c.aggregate
	c.aggregate = nil

	zoekt.SortFilesByScore(agg.Files)
	if max := c.maxDocDisplayCount; max > 0 && len(agg.Files) > max {
		agg.Files = agg.Files[:max]
	}

	return agg, true
}
