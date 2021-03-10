package shards

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// Note: This is a Sourcegraph specific addition to allow long running queries
// along normal interactive queries.

// scheduler is for managing concurrent searches. Its goals are:
//
//   1. Limit the number of concurrent searches.
//   2. Allow exclusive access.
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
type scheduler struct {
	throttle *semaphore.Weighted
	capacity int64
}

func newScheduler(capacity int64) *scheduler {
	return &scheduler{
		throttle: semaphore.NewWeighted(capacity),
		capacity: capacity,
	}
}

// Acquire blocks until a normal process is created (ie for a search
// request). See process documentation. It will only return an error if the
// context expires.
func (s *scheduler) Acquire(ctx context.Context) (*process, error) {
	return s.acquire(ctx, 1)
}

// Exclusive blocks until an exclusive process is created. An exclusive
// process is the only running process. See process documentation.
func (s *scheduler) Exclusive() *process {
	// won't error since context.Background won't expire
	proc, _ := s.acquire(context.Background(), s.capacity)
	return proc
}

func (s *scheduler) acquire(ctx context.Context, weight int64) (*process, error) {
	if err := s.throttle.Acquire(ctx, weight); err != nil {
		return nil, err
	}
	return &process{
		sched:  s,
		weight: weight,
	}, nil
}

// process represents a running search query or an exclusive process. When the
// process is done a call to Release is required.
type process struct {
	sched  *scheduler
	weight int64
}

// Release the resources/locks/semaphores associated with this process. Can
// only be called once.
func (p *process) Release() {
	p.sched.throttle.Release(p.weight)
}
