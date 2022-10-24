package main

import (
	"github.com/sourcegraph/log/logtest"
	"testing"
	"time"
)

func TestQueue_BackoffOnFail(t *testing.T) {
	backoffDuration := 1 * time.Millisecond
	maxBackoffDuration := backoffDuration * 2

	queue := NewQueue(backoffDuration, maxBackoffDuration, logtest.Scoped(t))
	opts := IndexOptions{RepoID: 1, Name: "foo"}

	queue.AddOrUpdate(opts)

	// Empty queue
	for ok := true; ok; _, ok = queue.Pop() {
	}

	queue.SetIndexed(opts, indexStateFail)

	bumpTime := time.Now()
	queue.Bump([]uint32{opts.RepoID})

	// item is disallowed from being pushed to heap during backoff period
	if item, ok := queue.Pop(); ok {
		qi := queue.items[item.RepoID]
		if qi.backoff.backoffUntil.Before(bumpTime) {
			t.Errorf("backoffDuration already passed before first attempt to push item to heap in Bump(). Increase backoffDuration for the Queue. backoffDuration: %s. maxBackoffDuration: %s.",
				backoffDuration, maxBackoffDuration)
		} else {
			t.Fatal("queue should be empty")
		}
	}
}

func TestQueue_BackoffAllowAfterDuration(t *testing.T) {
	backoffDuration := 1 * time.Millisecond
	maxBackoffDuration := backoffDuration * 2

	queue := NewQueue(backoffDuration, maxBackoffDuration, logtest.Scoped(t))
	opts := IndexOptions{RepoID: 1, Name: "foo"}

	queue.AddOrUpdate(opts)

	// Empty queue
	for ok := true; ok; _, ok = queue.Pop() {
	}

	queue.SetIndexed(opts, indexStateFail)

	if _, ok := queue.Pop(); ok {
		t.Fatal("queue should be empty after SetIndexed")
	}

	time.Sleep(backoffDuration * 2)

	queue.Bump([]uint32{opts.RepoID})

	if _, ok := queue.Pop(); !ok {
		t.Fatal("queue should no longer be empty after waiting for longer than the backoff duration and then bumping index options")
	}
}

func TestQueue_ResetBackoffUntil(t *testing.T) {
	backoffDuration := 1 * time.Hour
	maxBackoffDuration := backoffDuration * 2

	queue := NewQueue(backoffDuration, maxBackoffDuration, logtest.Scoped(t))
	opts := IndexOptions{RepoID: 1, Name: "foo"}

	queue.AddOrUpdate(opts)

	// Empty queue
	for ok := true; ok; _, ok = queue.Pop() {
	}

	queue.SetIndexed(opts, indexStateFail)

	if _, ok := queue.Pop(); ok {
		t.Fatal("queue should be empty after SetIndexed")
	}

	queue.SetIndexed(opts, indexStateSuccess)

	queue.Bump([]uint32{opts.RepoID})

	if _, ok := queue.Pop(); !ok {
		t.Fatal("queue should no longer be empty after resetting backoff until time to zero value")
	}
}

func TestQueue_ResetFailuresCount(t *testing.T) {
	backoffDuration := 1 * time.Millisecond
	maxBackoffDuration := 1000 * time.Millisecond

	queue := NewQueue(backoffDuration, maxBackoffDuration, logtest.Scoped(t))
	opts := IndexOptions{RepoID: 1, Name: "foo"}

	queue.AddOrUpdate(opts)

	// Empty queue
	for ok := true; ok; _, ok = queue.Pop() {
	}

	// consecutive failures will push backoff until to a further out time
	for i := 0; i < 1000; i++ {
		queue.SetIndexed(opts, indexStateFail)
	}

	if _, ok := queue.Pop(); ok {
		t.Fatal("queue should be empty after SetIndexed")
	}

	queue.SetIndexed(opts, indexStateSuccess)

	queue.Bump([]uint32{opts.RepoID})

	// backoff until is only one duration in the future after resetting consecutive failures count
	queue.SetIndexed(opts, indexStateFail)

	if _, ok := queue.Pop(); ok {
		t.Fatal("queue should be empty after SetIndexed")
	}

	time.Sleep(backoffDuration)

	queue.Bump([]uint32{opts.RepoID})

	if _, ok := queue.Pop(); !ok {
		t.Fatal("queue should no longer be empty after waiting a backoff duration for the first failure")
	}
}

func TestQueue_IncreaseDurationWithFailuresCount(t *testing.T) {
	backoffDuration := 5 * time.Millisecond
	maxBackoffDuration := 1000 * time.Millisecond

	queue := NewQueue(backoffDuration, maxBackoffDuration, logtest.Scoped(t))
	opts := IndexOptions{RepoID: 1, Name: "foo"}

	queue.AddOrUpdate(opts)

	// Empty queue
	for ok := true; ok; _, ok = queue.Pop() {
	}

	sleep := 1 * time.Millisecond
	for i := 0; i < 10; i++ {
		queue.SetIndexed(opts, indexStateFail)

		if _, ok := queue.Pop(); ok {
			t.Fatal("queue should be empty after SetIndexed")
		}

		// bump should have no impact on queue during backoff duration which increases with consecutive failures count
		time.Sleep(sleep)
		queue.Bump([]uint32{opts.RepoID})
		if _, ok := queue.Pop(); ok {
			t.Fatalf("queue should be empty after %d consecutive failures and waiting %s since last failure. backoff duration for %d consecutive failures: %s maxBackoffDuration: %s",
				i+1, sleep, i+1, time.Duration(i+1)*backoffDuration, maxBackoffDuration)
		}

		time.Sleep(backoffDuration)
		queue.Bump([]uint32{opts.RepoID})
		if _, ok := queue.Pop(); !ok {
			t.Fatalf("queue should not be empty after %d consecutive failures and waiting %s since last failure. backoff duration for %d consecutive failures: %s maxBackoffDuration: %s",
				i+1, sleep+backoffDuration, i+1, time.Duration(i+1)*backoffDuration, maxBackoffDuration)
		}

		// the first bump in each iteration occurs after an increasing sleep
		// while still before we pass the backoff until time
		sleep += backoffDuration
	}
}

func TestQueue_MaxBackoffDuration(t *testing.T) {
	backoffDuration := 1 * time.Hour
	maxBackoffDuration := 1 * time.Millisecond

	queue := NewQueue(backoffDuration, maxBackoffDuration, logtest.Scoped(t))
	opts := IndexOptions{RepoID: 1, Name: "foo"}

	queue.AddOrUpdate(opts)

	// Empty queue
	for ok := true; ok; _, ok = queue.Pop() {
	}

	// consecutive failures increase duration up to a maximum
	for i := 0; i < 100; i++ {
		queue.SetIndexed(opts, indexStateFail)
	}

	if _, ok := queue.Pop(); ok {
		t.Fatal("queue should be empty after SetIndexed")
	}

	time.Sleep(maxBackoffDuration)

	queue.Bump([]uint32{opts.RepoID})

	if _, ok := queue.Pop(); !ok {
		t.Fatal("queue should no longer be empty after max backoff duration has passed")
	}
}

func TestQueue_BackoffDisabled(t *testing.T) {
	cases := []struct {
		name               string
		backoffDuration    time.Duration
		maxBackoffDuration time.Duration
	}{{
		name:               "negative backoff",
		backoffDuration:    -1 * time.Minute,
		maxBackoffDuration: 1 * time.Minute,
	}, {
		name:               "negative maximum backoff",
		backoffDuration:    1 * time.Minute,
		maxBackoffDuration: -1 * time.Minute,
	}, {
		name:               "negative backoff and negative maximum backoff",
		backoffDuration:    -1 * time.Minute,
		maxBackoffDuration: -1 * time.Minute,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			queue := NewQueue(tc.backoffDuration, tc.maxBackoffDuration, logtest.Scoped(t))
			opts := IndexOptions{RepoID: 1, Name: "foo"}

			queue.AddOrUpdate(opts)

			// Empty queue
			for ok := true; ok; _, ok = queue.Pop() {
			}

			queue.SetIndexed(opts, indexStateFail)

			queue.Bump([]uint32{opts.RepoID})

			if _, ok := queue.Pop(); !ok {
				t.Fatal("queue should not be empty after bump when backoff is disabled")
			}
		})
	}
}
