package index

import (
	"runtime"
	"sync"
	"weak"

	"github.com/grafana/regexp"

	"github.com/sourcegraph/zoekt/internal/hybridre2"
)

// regexpCache holds the process-wide caches of compiled regexps, one for
// grafana/regexp and one for hybridre2.
var regexpCache = &compiledRegexps{}

type compiledRegexps struct {
	plain  weakCache[regexp.Regexp]
	hybrid weakCache[hybridre2.Regexp]
}

func (c *compiledRegexps) compile(pattern string) (*regexp.Regexp, error) {
	return c.plain.get(pattern, regexp.Compile)
}

func (c *compiledRegexps) compileHybrid(pattern string) (*hybridre2.Regexp, error) {
	return c.hybrid.get(pattern, hybridre2.Compile)
}

// weakCache shares a compiled value between its users for as long as any
// of them holds it. A search compiles the same pattern once per shard, and
// the shards of a search (and concurrent searches with the same pattern)
// overlap, so they share one compile. The cache only holds values weakly:
// once no search references a value the garbage collector frees it and a
// cleanup removes its entry, so the cache needs no size limit and never
// pins a regexp (or RE2's lazy DFA) that nothing uses.
//
// Concurrent callers missing on the same key wait for a single compile.
// Errors are returned to the callers sharing that compile but not cached,
// since there is no value whose collection would remove the entry.
type weakCache[T any] struct {
	m sync.Map // string -> *weakEntry[T]
}

type weakEntry[T any] struct {
	// done is closed when the compile finishes. Waiters block on it rather
	// than a mutex, so they all wake at once instead of one at a time.
	done chan struct{}
	val  weak.Pointer[T]
	err  error
}

func (c *weakCache[T]) get(key string, compile func(string) (*T, error)) (*T, error) {
	for {
		v, ok := c.m.Load(key)
		if !ok {
			e := &weakEntry[T]{done: make(chan struct{})}
			if v, ok = c.m.LoadOrStore(key, e); !ok {
				return c.fill(key, e, compile)
			}
		}
		e := v.(*weakEntry[T])

		// A non-blocking receive on a closed channel doesn't take its lock,
		// so check that first: most calls hit a finished entry.
		select {
		case <-e.done:
		default:
			<-e.done
		}
		if e.err != nil {
			return nil, e.err
		}
		if val := e.val.Value(); val != nil {
			return val, nil
		}
		// The value was collected before its cleanup ran, or the compile
		// panicked. Replace the entry.
		c.m.CompareAndDelete(key, e)
	}
}

// fill compiles key into e, which the caller just stored, and wakes the
// callers waiting on it. If compile panics, done is still closed and the
// waiters replace e.
func (c *weakCache[T]) fill(key string, e *weakEntry[T], compile func(string) (*T, error)) (*T, error) {
	defer close(e.done)

	val, err := compile(key)
	if err != nil {
		e.err = err
		c.m.CompareAndDelete(key, e)
		return nil, err
	}
	e.val = weak.Make(val)
	// Only remove e: a newer entry for key may already exist.
	runtime.AddCleanup(val, func(e *weakEntry[T]) { c.m.CompareAndDelete(key, e) }, e)
	return val, nil
}
