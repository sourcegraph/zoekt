package shards

import (
	"context"
	"testing"
	"time"
)

func BenchmarkYield(b *testing.B) {
	// Use quanta longer than the benchmark runs
	quanta := time.Minute

	// Benchmark of the raw primitive we are using to tell if we should yield.
	b.Run("timer", func(b *testing.B) {
		t := time.NewTimer(quanta)
		defer t.Stop()

		for n := 0; n < b.N; n++ {
			select {
			case <-t.C:
				b.Fatal("done")
			default:
			}
		}
	})

	// Benchmark of an alternative approach to timer. It is _much_ slower.
	b.Run("now", func(b *testing.B) {
		deadline := time.Now().Add(quanta)

		for n := 0; n < b.N; n++ {
			if time.Now().After(deadline) {
				b.Fatal("done")
			}
		}
	})

	// Benchmark of our wrapper around time.Timer
	b.Run("deadlineTimer", func(b *testing.B) {
		t := newDeadlineTimer(time.Now().Add(quanta))
		defer t.Stop()

		for n := 0; n < b.N; n++ {
			if t.Exceeded() {
				b.Fatal("done")
			}
		}
	})

	// Bencmark of actual yield function
	b.Run("yield", func(b *testing.B) {
		ctx := context.Background()
		sched := newScheduler(1)
		sched.interactiveDuration = quanta
		proc, err := sched.Acquire(ctx)
		if err != nil {
			b.Fatal(err)
		}
		defer proc.Release()

		for n := 0; n < b.N; n++ {
			proc.Yield(ctx)
		}
	})
}

func TestYield(t *testing.T) {
	ctx := context.Background()
	quanta := 10 * time.Millisecond
	deadline := time.Now().Add(quanta)

	sched := newScheduler(1)
	sched.interactiveDuration = quanta
	proc, err := sched.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Release()

	called := false
	oldYieldFunc := proc.yieldFunc
	proc.yieldFunc = func(ctx context.Context) error {
		if called {
			t.Fatal("yieldFunc called more than once")
		}
		called = true
		if time.Now().Before(deadline) {
			t.Fatal("yieldFunc called before deadline")
		}
		return oldYieldFunc(ctx)
	}

	var pre, post int
	for post < 10 {
		if err := proc.Yield(ctx); err != nil {
			t.Fatal(err)
		}

		if called {
			post++
		} else {
			pre++
		}
	}

	// We can't assert anything based on time since it will run into race
	// conditions with the runtime. So we just log the pre and post values so we
	// can eyeball them sometimes :)
	t.Logf("pre=%d post=%d", pre, post)
}
