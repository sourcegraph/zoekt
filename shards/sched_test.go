package shards

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func BenchmarkYield(b *testing.B) {
	// Use quantum longer than the benchmark runs
	quantum := time.Minute

	// Benchmark of the raw primitive we are using to tell if we should yield.
	b.Run("timer", func(b *testing.B) {
		t := time.NewTimer(quantum)
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
		deadline := time.Now().Add(quantum)

		for n := 0; n < b.N; n++ {
			if time.Now().After(deadline) {
				b.Fatal("done")
			}
		}
	})

	// Benchmark of our wrapper around time.Timer
	b.Run("deadlineTimer", func(b *testing.B) {
		t := newDeadlineTimer(time.Now().Add(quantum))
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
		sched := newMultiScheduler(1)
		sched.interactiveDuration = quantum
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
	quantum := 10 * time.Millisecond
	deadline := time.Now().Add(quantum)

	sched := newMultiScheduler(1)
	sched.interactiveDuration = quantum
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

func TestMultiScheduler(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	capacity := 8
	batchCap := capacity / 4
	sched := newMultiScheduler(int64(capacity))
	sched.interactiveDuration = 0 // instantly downgrade to batch on call to yield.

	var procs []*process
	addProc := func() {
		t.Helper()
		proc, err := sched.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		procs = append(procs, proc)
	}
	defer func() {
		for _, p := range procs {
			p.Release()
		}
	}()

	// Fill up interactive queue
	for i := 0; i < capacity; i++ {
		addProc()
	}

	// We expect this to fail since the queue is at capacity
	if _, err := sched.Acquire(quickCtx(t)); err == nil {
		t.Fatal("expected first acquire after cap to fail")
	}

	// move procs[0] to batch queue freeing up interactive
	if err := procs[0].Yield(ctx); err != nil {
		t.Fatal(err)
	}
	addProc()

	// We expect this to fail since the queue is at capacity again.
	if _, err := sched.Acquire(quickCtx(t)); err == nil {
		t.Fatal("expected second acquire after cap to fail")
	}

	// Fill up batch queue. Already has one item
	for i := 1; i < batchCap; i++ {
		if err := procs[i].Yield(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// We expect this to fail since the batch queue is at capacity.
	if err := procs[batchCap].Yield(quickCtx(t)); err == nil {
		t.Fatal("expected second acquire after cap to fail")
	}

	for _, p := range procs {
		p.Release()
	}
	procs = nil
}

func quickCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

func TestParseTuneables(t *testing.T) {
	cases := map[string]map[string]int{
		"":                   {},
		"disable":            {"disable": 1},
		"disable,batchdiv=2": {"disable": 1, "batchdiv": 2},
	}

	for v, want := range cases {
		got := parseTuneables(v)
		if d := cmp.Diff(want, got); d != "" {
			t.Errorf("parseTuneables(%q) mismatch (-want, +got):\n%s", v, d)
		}
	}
}
