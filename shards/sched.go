package shards

import (
	"context"
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
	throttle *semaphore.Weighted

	// capacity is the max concurrent searches we allow.
	capacity int64

	// interactiveDuration is how long we run a search query at interactive
	// priority before downgrading it to a batch/slow query.
	interactiveDuration time.Duration
}

func newScheduler(capacity int64) *scheduler {
	return &scheduler{
		throttle: semaphore.NewWeighted(capacity),
		capacity: capacity,

		interactiveDuration: 5 * time.Second,
	}
}

// Acquire blocks until a normal process is created (ie for a search
// request). See process documentation. It will only return an error if the
// context expires.
func (s *scheduler) Acquire(ctx context.Context) (*process, error) {
	proc, err := s.acquire(ctx, 1)
	if err != nil {
		return nil, err
	}

	proc.yieldTimer = newDeadlineTimer(time.Now().Add(s.interactiveDuration))
	proc.yieldFunc = func(ctx context.Context) error {
		// will be implemented next commit.
		return nil
	}
	return proc, nil
}

// Exclusive blocks until an exclusive process is created. An exclusive
// process is the only running process. See process documentation.
func (s *scheduler) Exclusive() *process {
	// won't error since context.Background won't expire
	proc, _ := s.acquire(context.Background(), s.capacity)
	// exclusive processes will never yield, so we leave yieldTimer and
	// yieldFunc nil.
	return proc
}

func (s *scheduler) acquire(ctx context.Context, weight int64) (*process, error) {
	if err := s.throttle.Acquire(ctx, weight); err != nil {
		return nil, err
	}
	return &process{
		releaseFunc: func() {
			s.throttle.Release(weight)
		},
	}, nil
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
// The only error it will return is a context error if ctx expires.
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
