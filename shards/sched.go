package shards

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
)

// Note: This is a Sourcegraph specific addition to allow long running queries
// along normal interactive queries.

// scheduler is for managing concurrent searches. Its goals are:
//
//   1. Limit the number of concurrent searches.
//   2. Allow exclusive access.
//   3. Co-operatively limit long running searches.
//   4. No tuneables.
//
// ### Limit the number of concurrent searches
//
// Searching is CPU bound, so we can't do better than #CPU queries
// concurrently. If we do so, we just create more memory pressure.
//
// ### Allow exclusive access
//
// During the time the shard list is accessed and a search is actually done on
// a shard it can't be closed. As such while a search is running we do not
// allow any closing of shards. However, we do need to close and add shards as
// the indexer proceeds. To do this we have an exclusive process which will be
// the only one running. This is like a Lock on a RWMutex, while a normal
// search is a RLock.
//
// ### Co-operatively limit long running searches
//
// Some searches are slow. Either due to a hard to execute search query (can't
// use trigram index) or a large number of results. We want to support this
// use case while still allowing interactive queries to be fast.
//
// ### No tuneables
//
// We want to avoid the need to tune the scheduler depending on the workload /
// instance. As such we use a simple design whose inputs are time and number
// of CPUs.
//
// ## Design
//
// We use semaphores to limit the number of running processes. An exclusive
// process acquires the full semaphore. A process represents something which
// has acquired on the semaphore. Every process is either fast or slow. A
// process starts as fast, but is downgraded to slow after a period of
// time. Downgrading relies on a process co-operatively deciding to downgrade.
//
// We intentionally keep the algorithm simple, but have a general interface to
// allow improvements as we learn more.
type scheduler struct {
	// Exclusive process holds a write lock, search processes hold read locks.
	mu             sync.RWMutex
	semInteractive *semaphore.Weighted
	semBatch       *semaphore.Weighted

	// interactiveDuration is how long we run a search query at interactive
	// priority before downgrading it to a batch/slow query.
	interactiveDuration time.Duration
}

func newScheduler(capacity int64) *scheduler {
	// Burst upto 1/4 of CPUs for batch. This means we now can use upto 1.25 the
	// amount of CPU.
	batchCap := capacity / 4
	if batchCap == 0 {
		batchCap = 1
	}

	return &scheduler{
		semInteractive: semaphore.NewWeighted(capacity),
		semBatch:       semaphore.NewWeighted(batchCap),

		interactiveDuration: 5 * time.Second,
	}
}

// Acquire blocks until a normal process is created (ie for a search
// request). See process documentation. It will only return an error if the
// context expires.
func (s *scheduler) Acquire(ctx context.Context) (*process, error) {
	s.mu.RLock()

	// Start in interactive. yieldFunc will switch us to batch. sem can be nil
	// if we fail while switching to batch. nil value prevents us releasing
	// twice.
	sem := s.semInteractive

	if err := sem.Acquire(ctx, 1); err != nil {
		s.mu.RUnlock()
		return nil, err
	}

	return &process{
		releaseFunc: func() {
			if sem != nil {
				sem.Release(1)
				sem = nil
			}
			s.mu.RUnlock()
		},
		yieldTimer: newDeadlineTimer(time.Now().Add(s.interactiveDuration)),
		yieldFunc: func(ctx context.Context) error {
			if sem != nil {
				sem.Release(1)
				sem = nil
			}

			// Try to acquire batch. Only set sem if we succeed so we know we can
			// clean it up. If this fails we assume the process will stop running
			// (ctx has expired).
			semNext := s.semBatch
			if err := semNext.Acquire(ctx, 1); err != nil {
				return err
			}

			sem = semNext
			return nil
		},
	}, nil
}

// Exclusive blocks until an exclusive process is created. An exclusive
// process is the only running process. See process documentation.
func (s *scheduler) Exclusive() *process {
	// Exclusive process holds a write lock on mu, which ensures we have no
	// processes running (search semaphores are empty).
	//
	// exclusive processes will never yield, so we leave yieldTimer and
	// yieldFunc nil.
	s.mu.Lock()
	return &process{
		releaseFunc: func() {
			s.mu.Unlock()
		},
	}
}

// process represents a running search query or an exclusive process. When the
// process is done a call to Release is required.
type process struct {
	// yieldTimer ensures we only call yieldFunc once after a deadline.
	yieldTimer *deadlineTimer
	// yieldFunc is called once by Yield.
	yieldFunc func(context.Context) error

	// releaseFunc is called once by Release
	releaseFunc func()
}

// Release the resources/locks/semaphores associated with this process. Can
// only be called once.
func (p *process) Release() {
	if p.yieldTimer != nil {
		p.yieldTimer.Stop()
	}

	p.releaseFunc()
}

// Yield may block to allow another process to run. This should be called
// relatively often by a search to allow other processes to run. This can not
// be called concurrently.
//
// The only error it will return is a context error if ctx expires. In that
// case the process should stop running and call Release.
func (p *process) Yield(ctx context.Context) error {
	if p.yieldTimer == nil || !p.yieldTimer.Exceeded() {
		return nil
	}

	// Try to yield. This can return an error if our context expired.
	err := p.yieldFunc(ctx)
	if err != nil {
		return err
	}

	// Successfully yielded. Stop our timer and mark it nil so we don't call
	// yieldFunc again.
	p.yieldTimer.Stop()
	p.yieldTimer = nil

	return nil
}

// newDeadlineTimer returns a timer which fires after deadline. Once it fires
// Exceeded will always return true. Callers must call Stop when done to
// release resources.
func newDeadlineTimer(deadline time.Time) *deadlineTimer {
	return &deadlineTimer{
		t: time.NewTimer(deadline.Sub(time.Now())),
	}
}

type deadlineTimer struct {
	// t.C fires after deadline. Once it fires we set to nil to indicate it has
	// fired.
	t *time.Timer
}

// Exceeded returns true if time is after the deadline.
func (t *deadlineTimer) Exceeded() bool {
	if t.t == nil {
		return true
	}
	select {
	case <-t.t.C:
	default:
		return false
	}

	t.Stop()

	return true
}

// Stop stops the underlying timer. Can be called multiple times.
func (t *deadlineTimer) Stop() {
	if t.t == nil {
		return
	}
	t.t.Stop()
	t.t = nil
}
