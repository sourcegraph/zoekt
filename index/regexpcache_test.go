package index

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestLRUCachesValuesAndErrors(t *testing.T) {
	c := newLRU[string](4)
	var calls int
	compile := func(k string) (string, error) {
		calls++
		if k == "bad" {
			return "", errors.New("bad pattern")
		}
		return "v:" + k, nil
	}

	for range 3 {
		if v, err := c.get("a", compile); v != "v:a" || err != nil {
			t.Fatalf("get(a) = %q, %v", v, err)
		}
		if _, err := c.get("bad", compile); err == nil {
			t.Fatal("get(bad): want error")
		}
	}
	if calls != 2 {
		t.Fatalf("compile called %d times, want 2", calls)
	}
}

func TestLRUEvictsLeastRecentlyUsed(t *testing.T) {
	c := newLRU[string](2)
	var calls int
	compile := func(k string) (string, error) { calls++; return k, nil }

	c.get("a", compile)
	c.get("b", compile)
	c.get("a", compile) // b is now the oldest
	c.get("c", compile) // evicts b
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
	c.get("a", compile)
	if calls != 3 {
		t.Fatalf("a should still be cached, calls = %d", calls)
	}
	c.get("b", compile)
	if calls != 4 {
		t.Fatalf("b should have been evicted, calls = %d", calls)
	}
	if len(c.items) != 2 || c.order.Len() != 2 {
		t.Fatalf("size = %d/%d, want 2", len(c.items), c.order.Len())
	}
}

func TestLRUSkipsLongPatterns(t *testing.T) {
	c := newLRU[string](4)
	var calls int
	compile := func(k string) (string, error) { calls++; return k, nil }

	long := strings.Repeat("a", maxCachedPatternLen+1)
	c.get(long, compile)
	c.get(long, compile)
	if calls != 2 || len(c.items) != 0 {
		t.Fatalf("calls = %d, cached = %d; want 2, 0", calls, len(c.items))
	}

	atCap := strings.Repeat("a", maxCachedPatternLen)
	c.get(atCap, compile)
	c.get(atCap, compile)
	if calls != 3 {
		t.Fatalf("pattern at the cap should be cached, calls = %d", calls)
	}
}

func TestLRUPanickingCompileLeavesNoEntry(t *testing.T) {
	c := newLRU[string](4)
	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Fatalf("recovered %v, want the original panic", r)
			}
		}()
		c.get("k", func(string) (string, error) { panic("boom") })
	}()
	if len(c.items) != 0 || c.order.Len() != 0 {
		t.Fatalf("size = %d/%d after the panic, want 0", len(c.items), c.order.Len())
	}

	var calls int
	compile := func(string) (string, error) { calls++; return "v", nil }
	for range 2 {
		if v, err := c.get("k", compile); v != "v" || err != nil {
			t.Fatalf("retry = %q, %v", v, err)
		}
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 recompile then a hit", calls)
	}
}

func TestLRUFirstWriterWins(t *testing.T) {
	c := newLRU[*int](4)
	first, second := 1, 2
	inCompile := make(chan struct{})
	release := make(chan struct{})

	got := make(chan *int, 1)
	go func() {
		v, _ := c.get("k", func(string) (*int, error) {
			close(inCompile)
			<-release
			return &first, nil
		})
		got <- v
	}()
	<-inCompile

	// A second caller finishes its compile while the first is still in flight.
	if v, _ := c.get("k", func(string) (*int, error) { return &second, nil }); v != &second {
		t.Fatalf("second caller got %v, want its own compile", v)
	}
	close(release)
	if v := <-got; v != &second {
		t.Fatalf("late caller got %v, want the published value", v)
	}
	if v, _ := c.get("k", nil); v != &second {
		t.Fatalf("cached = %v, want the published value", v)
	}
}

func TestLRUConcurrent(t *testing.T) {
	const limit = 16
	c := newLRU[string](limit)
	var compiles atomic.Int64
	compile := func(k string) (string, error) { compiles.Add(1); return k, nil }

	var wg sync.WaitGroup
	for g := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 2000 {
				k := fmt.Sprint((g + i) % 40)
				if v, err := c.get(k, compile); v != k || err != nil {
					t.Errorf("get(%s) = %q, %v", k, v, err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if len(c.items) != c.order.Len() || len(c.items) > limit {
		t.Fatalf("size = %d/%d, limit %d", len(c.items), c.order.Len(), limit)
	}
}
