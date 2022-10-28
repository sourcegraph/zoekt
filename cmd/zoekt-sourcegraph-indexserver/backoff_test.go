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
	EmptyQueue(queue)

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
	EmptyQueue(queue)

	queue.SetIndexed(opts, indexStateFail)

	if _, ok := queue.Pop(); ok {
		t.Fatal("queue should be empty after SetIndexed")
	}

	time.Sleep(backoffDuration * 20)

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
	EmptyQueue(queue)

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
	EmptyQueue(queue)

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

func TestQueue_MaxBackoffDuration(t *testing.T) {
	backoffDuration := 1 * time.Hour
	maxBackoffDuration := 1 * time.Millisecond

	queue := NewQueue(backoffDuration, maxBackoffDuration, logtest.Scoped(t))
	opts := IndexOptions{RepoID: 1, Name: "foo"}

	queue.AddOrUpdate(opts)
	EmptyQueue(queue)

	// consecutive failures increase duration up to a maximum
	for i := 0; i < 100; i++ {
		queue.SetIndexed(opts, indexStateFail)
	}

	if _, ok := queue.Pop(); ok {
		t.Fatal("queue should be empty after SetIndexed")
	}

	// sleep past maxBackoffDuration but long before backoffDuration would pass
	time.Sleep(maxBackoffDuration * 200)

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
			EmptyQueue(queue)
			queue.SetIndexed(opts, indexStateFail)

			queue.Bump([]uint32{opts.RepoID})

			if _, ok := queue.Pop(); !ok {
				t.Fatal("queue should not be empty after bump when backoff is disabled")
			}
		})
	}
}

func TestBackoff_AllowByDefault(t *testing.T) {
	backoffDuration := 1 * time.Minute
	maxBackoffDuration := 2 * backoffDuration

	backoff := backoff{
		backoffDuration: backoffDuration,
		maxBackoff:      maxBackoffDuration,
	}

	now := time.Now()
	AssertAllow(t, now, backoff)
}

func TestBackoff_Disallow(t *testing.T) {
	backoffDuration := 10 * time.Minute
	maxBackoffDuration := 2 * backoffDuration
	opts := IndexOptions{RepoID: 1, Name: "foo"}

	backoff := backoff{
		backoffDuration: backoffDuration,
		maxBackoff:      maxBackoffDuration,
	}

	now := time.Now()
	backoff.Fail(now, logtest.Scoped(t), opts)
	AssertDisallow(t, now, backoff)
}

func TestBackoff_BackoffExpiration(t *testing.T) {
	backoffDuration := 10 * time.Minute
	maxBackoffDuration := 2 * backoffDuration
	opts := IndexOptions{RepoID: 1, Name: "foo"}

	backoff := backoff{
		backoffDuration: backoffDuration,
		maxBackoff:      maxBackoffDuration,
	}

	now := time.Now()
	backoff.Fail(now, logtest.Scoped(t), opts)
	AssertDisallow(t, now, backoff)

	backoffUntil := now.Add(backoffDuration)
	AssertDisallow(t, backoffUntil, backoff)

	// backoff not applied for any timestamp after backoff until
	expiredBackoff := now.Add(backoffDuration + (1 * time.Nanosecond))
	AssertAllow(t, expiredBackoff, backoff)
}

func TestBackoff_ResetBackoffUntil(t *testing.T) {
	backoffDuration := 10 * time.Minute
	maxBackoffDuration := 2 * backoffDuration
	opts := IndexOptions{RepoID: 1, Name: "foo"}

	backoff := backoff{
		backoffDuration: backoffDuration,
		maxBackoff:      maxBackoffDuration,
	}

	now := time.Now()
	backoff.Fail(now, logtest.Scoped(t), opts)
	AssertDisallow(t, now, backoff)

	backoff.Reset()
	AssertAllow(t, now, backoff)
}

func TestBackoff_MaximumBackoffUntil(t *testing.T) {
	backoffDuration := 10 * time.Minute
	maxBackoffDuration := 25 * time.Minute
	opts := IndexOptions{RepoID: 1, Name: "foo"}

	backoff := backoff{
		backoffDuration: backoffDuration,
		maxBackoff:      maxBackoffDuration,
	}

	firstIndex := time.Now()
	backoff.Fail(firstIndex, logtest.Scoped(t), opts)
	currentBackoffUntil := backoffDuration

	// disallowed before we pass backoff until timestamp
	AssertDisallow(t, firstIndex.Add(currentBackoffUntil-1*time.Minute), backoff)

	secondIndex := firstIndex.Add(currentBackoffUntil + 1*time.Minute)
	backoff.Fail(secondIndex, logtest.Scoped(t), opts)

	// failures applies increased backoff duration due to consecutive failures
	currentBackoffUntil += backoffDuration

	// disallowed before we pass backoff until timestamp
	AssertDisallow(t, secondIndex.Add(currentBackoffUntil-1*time.Minute), backoff)

	thirdIndex := secondIndex.Add(currentBackoffUntil + 1*time.Minute)
	backoff.Fail(thirdIndex, logtest.Scoped(t), opts)

	// This would be the new backoff until timestamp if we were not bounded by maxBackoffDuration
	currentBackoffUntil += backoffDuration
	// currentBackoffUntil is not applied since it exceeds maximum
	AssertAllow(t, thirdIndex.Add(currentBackoffUntil-1*time.Minute), backoff)

	// Maximum backoff duration was applied
	AssertDisallow(t, thirdIndex.Add(maxBackoffDuration-1*time.Minute), backoff)
}

func TestBackoff_IncrementConsecutiveFailures(t *testing.T) {
	failedCount := 5
	backoffDuration := 1 * time.Minute
	maxBackoffDuration := time.Duration(failedCount) * backoffDuration
	opts := IndexOptions{RepoID: 1, Name: "foo"}

	backoff := backoff{
		backoffDuration: backoffDuration,
		maxBackoff:      maxBackoffDuration,
	}

	now := time.Now()
	expectedFailuresCount := 0

	for i := 0; i < failedCount; i++ {
		backoff.Fail(now.Add(time.Duration(i)*backoffDuration), logtest.Scoped(t), opts)
		expectedFailuresCount++
		AssertFailuresCount(t, expectedFailuresCount, backoff)
	}
}

func TestBackoff_MaximumConsecutiveFailures(t *testing.T) {
	maximumCount := 3
	failedCount := 2 * maximumCount
	backoffDuration := 1 * time.Minute
	maxBackoffDuration := time.Duration(maximumCount) * backoffDuration
	opts := IndexOptions{RepoID: 1, Name: "foo"}

	backoff := backoff{
		backoffDuration: backoffDuration,
		maxBackoff:      maxBackoffDuration,
	}

	now := time.Now()
	expectedFailuresCount := 0

	// consecutive failures count increments per failure
	for i := 0; i < maximumCount; i++ {
		backoff.Fail(now.Add(time.Duration(i)*backoffDuration), logtest.Scoped(t), opts)
		expectedFailuresCount++
		AssertFailuresCount(t, expectedFailuresCount, backoff)
	}

	// consecutive failures count does not change
	for i := maximumCount - 1; i < failedCount; i++ {
		backoff.Fail(now.Add(time.Duration(i)*backoffDuration), logtest.Scoped(t), opts)
		AssertFailuresCount(t, expectedFailuresCount, backoff)
	}
}

func TestBackoff_ResetConsecutiveFailures(t *testing.T) {
	failedCount := 3
	backoffDuration := 10 * time.Minute
	maxBackoffDuration := time.Duration(failedCount) * backoffDuration
	opts := IndexOptions{RepoID: 1, Name: "foo"}

	backoff := backoff{
		backoffDuration: backoffDuration,
		maxBackoff:      maxBackoffDuration,
	}

	for i := 0; i < failedCount; i++ {
		now := time.Now()

		// fail j consecutive times
		for j := i; j <= i; j++ {
			backoff.Fail(now.Add(time.Duration(j)*backoffDuration), logtest.Scoped(t), opts)
		}

		// reset behavior is independent of current consecutiveFailures count
		backoff.Reset()
		AssertFailuresCount(t, 0, backoff)
	}
}

func AssertAllow(t *testing.T, now time.Time, b backoff) {
	if indexingAllowed := b.Allow(now); !indexingAllowed {
		t.Errorf("Indexing is not allowed to proceed by default at %s due to backing off until %s",
			now, b.backoffUntil)
	}
}

func AssertDisallow(t *testing.T, now time.Time, b backoff) {
	if indexingAllowed := b.Allow(now); indexingAllowed {
		t.Errorf("Indexing is allowed to proceed at %s after failure despite being set to backoff until %s",
			now, b.backoffUntil)
	}
}

func AssertFailuresCount(t *testing.T, expected int, b backoff) {
	if failuresCount := b.consecutiveFailures; failuresCount != expected {
		t.Errorf("Item currently tracks %d consecutive failures when expected consecutive failures count is %d",
			failuresCount, expected)
	}
}

func EmptyQueue(q *Queue) {
	for ok := true; ok; _, ok = q.Pop() {
	}
}
