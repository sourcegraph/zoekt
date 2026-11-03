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
	once sync.Once
	val  weak.Pointer[T]
	err  error
}

func (c *weakCache[T]) get(key string, compile func(string) (*T, error)) (*T, error) {
	for {
		v, ok := c.m.Load(key)
		if !ok {
			v, _ = c.m.LoadOrStore(key, &weakEntry[T]{})
		}
		e := v.(*weakEntry[T])

		var val *T
		e.once.Do(func() {
			val, e.err = compile(key)
			if e.err != nil {
				return
			}
			e.val = weak.Make(val)
			// Only remove e: a newer entry for key may already exist.
			runtime.AddCleanup(val, func(e *weakEntry[T]) { c.m.CompareAndDelete(key, e) }, e)
		})

		if e.err != nil {
			c.m.CompareAndDelete(key, e)
			return nil, e.err
		}
		if val == nil {
			val = e.val.Value()
		}
		if val != nil {
			return val, nil
		}
		// The value was collected before its cleanup ran, or the compile
		// panicked. Replace the entry.
		c.m.CompareAndDelete(key, e)
	}
}
