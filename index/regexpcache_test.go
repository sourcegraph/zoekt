package index

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func cachedKeys[T any](c *weakCache[T]) int {
	n := 0
	c.m.Range(func(any, any) bool { n++; return true })
	return n
}

// waitForEmpty runs the GC until the cleanups have removed every entry.
func waitForEmpty[T any](t *testing.T, c *weakCache[T]) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for cachedKeys(c) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d entries left after GC, want 0", cachedKeys(c))
		}
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
}

func TestWeakCacheKeepsValueWhileReferenced(t *testing.T) {
	var c weakCache[string]
	var calls int
	compile := func(k string) (*string, error) {
		calls++
		v := "v:" + k
		return &v, nil
	}

	held, _ := c.get("a", compile)
	runtime.GC()
	runtime.GC()
	if v, _ := c.get("a", compile); v != held || calls != 1 {
		t.Fatalf("got %p after GC, want the held value %p; calls = %d", v, held, calls)
	}
	runtime.KeepAlive(held)

	held = nil
	waitForEmpty(t, &c)
	if v, _ := c.get("a", compile); *v != "v:a" || calls != 2 {
		t.Fatalf("got %q with %d calls, want a recompile after the value was collected", *v, calls)
	}
}

func TestWeakCacheSharesConcurrentCompile(t *testing.T) {
	var c weakCache[string]
	var calls atomic.Int64
	release := make(chan struct{})
	compile := func(k string) (*string, error) {
		calls.Add(1)
		<-release
		return &k, nil
	}

	const n = 16
	got := make([]*string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { got[i], _ = c.get("k", compile) })
	}
	time.Sleep(10 * time.Millisecond) // let the callers block on the compile
	close(release)
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("compiled %d times, want 1", calls.Load())
	}
	for _, v := range got {
		if v != got[0] {
			t.Fatal("callers got different values")
		}
	}
}

func TestWeakCacheDoesNotCacheErrors(t *testing.T) {
	var c weakCache[string]
	var calls int
	compile := func(string) (*string, error) { calls++; return nil, errors.New("bad pattern") }

	for range 2 {
		if _, err := c.get("bad", compile); err == nil {
			t.Fatal("want error")
		}
	}
	if calls != 2 || cachedKeys(&c) != 0 {
		t.Fatalf("calls = %d, entries = %d; want 2, 0", calls, cachedKeys(&c))
	}
}

func TestWeakCachePanickingCompileIsRetried(t *testing.T) {
	var c weakCache[string]
	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Fatalf("recovered %v, want the original panic", r)
			}
		}()
		c.get("k", func(string) (*string, error) { panic("boom") })
	}()

	v := "v"
	got, err := c.get("k", func(string) (*string, error) { return &v, nil })
	if got != &v || err != nil {
		t.Fatalf("retry = %v, %v; want a fresh compile", got, err)
	}
}
