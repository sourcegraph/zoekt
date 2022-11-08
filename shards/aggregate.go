package shards

import (
	"context"
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
	opts      *zoekt.SearchOptions
	aggregate *zoekt.SearchResult
}

func newCollectSender(opts *zoekt.SearchOptions) *collectSender {
	return &collectSender{opts: opts}
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

	zoekt.SortFiles(agg.Files, c.opts)

	if max := c.opts.MaxDocDisplayCount; max > 0 && len(agg.Files) > max {
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
		mu.Lock()
		defer mu.Unlock()

		if collectSender == nil {
			return
		}

		if agg, ok := collectSender.Done(); ok {
			metricFinalAggregateSize.WithLabelValues(reason).Observe(float64(len(agg.Files)))
			agg.FlushReason = reason
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
			stopCollectingAndFlush("timer_expired")
		}
	}()

	finalFlush := func() {
		stopCollectingAndFlush("final_flush")
	}

	return stream.SenderFunc(func(event *zoekt.SearchResult) {
		mu.Lock()
		if collectSender != nil {
			collectSender.Send(event)
		} else {
			sender.Send(event)
		}
		mu.Unlock()
	}), finalFlush
}

// limitSender wraps a sender and calls cancel once it has seen limit file
// matches.
func limitSender(cancel context.CancelFunc, sender zoekt.Sender, limit int) zoekt.Sender {
	return stream.SenderFunc(func(result *zoekt.SearchResult) {
		if len(result.Files) >= limit {
			result.Files = result.Files[:limit]
			cancel()
		}
		limit -= len(result.Files)
		sender.Send(result)
	})
}

func copyFileSender(sender zoekt.Sender) zoekt.Sender {
	return stream.SenderFunc(func(result *zoekt.SearchResult) {
		copyFiles(result)
		sender.Send(result)
	})
}
