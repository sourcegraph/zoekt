package index

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestLRUCachesValuesAndErrors(t *testing.T) {
	c := newLRU[string](4, defaultMaxCachedPatternLen)
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
	c := newLRU[string](2, defaultMaxCachedPatternLen)
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
	c := newLRU[string](4, defaultMaxCachedPatternLen)
	var calls int
	compile := func(k string) (string, error) { calls++; return k, nil }

	long := strings.Repeat("a", defaultMaxCachedPatternLen+1)
	c.get(long, compile)
	c.get(long, compile)
	if calls != 2 || len(c.items) != 0 {
		t.Fatalf("calls = %d, cached = %d; want 2, 0", calls, len(c.items))
	}

	atCap := strings.Repeat("a", defaultMaxCachedPatternLen)
	c.get(atCap, compile)
	c.get(atCap, compile)
	if calls != 3 {
		t.Fatalf("pattern at the cap should be cached, calls = %d", calls)
	}
}

func TestLRUPanickingCompileLeavesNoEntry(t *testing.T) {
	c := newLRU[string](4, defaultMaxCachedPatternLen)
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
	c := newLRU[*int](4, defaultMaxCachedPatternLen)
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
	c := newLRU[string](limit, defaultMaxCachedPatternLen)
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

func TestRegexpCacheConfigFromEnv(t *testing.T) {
	defaults := regexpCacheConfig{defaultRegexpCacheSize, defaultMaxCachedPatternLen}
	for _, tc := range []struct {
		name      string
		size, pat string // empty means unset
		want      regexpCacheConfig
	}{
		{"defaults", "", "", defaults},
		{"valid", "8", "100", regexpCacheConfig{8, 100}},
		{"zero disables", "0", "0", regexpCacheConfig{0, 0}},
		{"invalid", "many", "1.5", defaults},
		{"negative", "-1", "-1", defaults},
		{"only size", "8", "", regexpCacheConfig{8, defaultMaxCachedPatternLen}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for name, val := range map[string]string{envRegexpCacheSize: tc.size, envRegexpCachePatternSize: tc.pat} {
				// t.Setenv restores the original value afterwards; unset
				// the variable for this case unless the case sets it.
				t.Setenv(name, "")
				os.Unsetenv(name)
				if val != "" {
					t.Setenv(name, val)
				}
			}
			if got := regexpCacheConfigFromEnv(); got != tc.want {
				t.Errorf("config = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRegexpCacheHonoursConfig(t *testing.T) {
	c := newCompiledRegexps(regexpCacheConfig{size: 2, maxPattern: 4})
	for _, p := range []string{"a", "b", "c", "abcde"} {
		if _, err := c.compile(p); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(c.plain.items); n != 2 {
		t.Errorf("cached %d patterns, want the size limit 2", n)
	}
	if _, ok := c.plain.items["abcde"]; ok {
		t.Error("pattern over the length cap was cached")
	}

	off := newCompiledRegexps(regexpCacheConfig{size: 0, maxPattern: 4})
	re1, _ := off.compile("a")
	re2, _ := off.compile("a")
	if re1 == re2 || len(off.plain.items) != 0 {
		t.Error("size 0 should compile every call and cache nothing")
	}
}
