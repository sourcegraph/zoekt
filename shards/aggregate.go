package shards

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/stream"
)

var (
	metricFinalAggregateSize = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "zoekt_final_aggregate_size",
		Help:    "The number of file matches we aggregated before flushing",
		Buckets: prometheus.ExponentialBuckets(1, 2, 20),
	}, []string{"reason"})
)

// collectSender is a sender that will aggregate results. Once sending is
// done, you call Done to return the aggregated result which are ranked.
//
// Note: It aggregates Progress as well, and expects that the
// MaxPendingPriority it receives are monotonically decreasing.
type collectSender struct {
	aggregate          *zoekt.SearchResult
	maxDocDisplayCount int
	sizeBytes          uint64
	useDocumentRanks   bool
}

func newCollectSender(opts *zoekt.SearchOptions) *collectSender {
	return &collectSender{
		maxDocDisplayCount: opts.MaxDocDisplayCount,
		useDocumentRanks:   opts.UseDocumentRanks,
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

	c.sizeBytes += r.SizeBytes()
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
	c.sizeBytes = 0

	zoekt.SortFiles(agg.Files, c.useDocumentRanks)

	if max := c.maxDocDisplayCount; max > 0 && len(agg.Files) > max {
		agg.Files = agg.Files[:max]
	}

	return agg, true
}

// newFlushCollectSender creates a sender which will collect and rank results
// until opts.FlushWallTime. After that it will stream each result as it is
// sent.
func newFlushCollectSender(opts *zoekt.SearchOptions, sender zoekt.Sender) (zoekt.Sender, func()) {
	// We don't need to do any collecting, so just pass back the sender to use
	// directly.
	if opts.FlushWallTime == 0 {
		return sender, func() {}
	}

	// We transition through 3 states
	// 1. collectSender != nil: collect results via collectSender
	// 2. timerFired: send collected results and mark collectSender nil
	// 3. collectSender == nil: directly use sender

	var (
		mu            sync.Mutex
		collectSender = newCollectSender(opts)
		timerCancel   = make(chan struct{})
	)

	// stopCollectingAndFlush will send what we have collected and all future
	// sends will go via sender directly.
	stopCollectingAndFlush := func(reason string) {
		if collectSender == nil {
			return
		}

		if agg, ok := collectSender.Done(); ok {
			metricFinalAggregateSize.WithLabelValues(reason).Observe(float64(len(agg.Files)))
			sender.Send(agg)
		}

		// From now on use sender directly
		collectSender = nil

		// Stop timer goroutine if it is still running.
		close(timerCancel)
	}

	// Wait FlushWallTime to call stopCollecting.
	go func() {
		timer := time.NewTimer(opts.FlushWallTime)
		select {
		case <-timerCancel:
			timer.Stop()
		case <-timer.C:
			mu.Lock()
			stopCollectingAndFlush("timer_expired")
			mu.Unlock()
		}
	}()

	finalFlush := func() {
		mu.Lock()
		stopCollectingAndFlush("final_flush")
		mu.Unlock()
	}

	return stream.SenderFunc(func(event *zoekt.SearchResult) {
		mu.Lock()
		if collectSender != nil {
			collectSender.Send(event)

			// Protect against too large aggregates. This should be the exception and only
			// happen for queries yielding an extreme number of results.
			if opts.MaxSizeBytes > 0 && collectSender.sizeBytes > uint64(opts.MaxSizeBytes) {
				stopCollectingAndFlush("max_size_reached")

			}
		} else {
			sender.Send(event)
		}
		mu.Unlock()
	}), finalFlush
}
