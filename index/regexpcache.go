package index

import (
	"container/list"
	"sync"

	"github.com/grafana/regexp"

	"github.com/sourcegraph/zoekt/internal/hybridre2"
)

const (
	// regexpCacheSize bounds each cache by entry count. A search compiles the
	// same pattern once per shard, so this only has to cover the searches
	// running at once.
	regexpCacheSize = 256

	// maxCachedPatternLen is the longest pattern (in bytes) that is cached.
	// The compiled program grows with pattern length, so longer patterns are
	// compiled per shard as before rather than pinned by the cache. This does
	// not bound RE2's lazy DFA, which can additionally grow per cached regexp
	// up to go-re2's memory limit. Typical queries are well below this.
	maxCachedPatternLen = 4 << 10
)

var regexpCache = &compiledRegexps{
	plain:  newLRU[*regexp.Regexp](regexpCacheSize),
	hybrid: newLRU[*hybridre2.Regexp](regexpCacheSize),
}

type compiledRegexps struct {
	plain  *lru[*regexp.Regexp]
	hybrid *lru[*hybridre2.Regexp]
}

func (c *compiledRegexps) compile(pattern string) (*regexp.Regexp, error) {
	return c.plain.get(pattern, regexp.Compile)
}

func (c *compiledRegexps) compileHybrid(pattern string) (*hybridre2.Regexp, error) {
	return c.hybrid.get(pattern, hybridre2.Compile)
}

type lruEntry[T any] struct {
	val  T
	err  error
	elem *list.Element
}

// lru is a bounded, concurrency-safe memo. Callers missing on the same key
// concurrently each compile, and the first to finish publishes its result;
// the others return it and drop their own. Compile errors are cached like
// values, since they are deterministic for a given pattern. Evicted values
// stay valid for searches still holding them.
type lru[T any] struct {
	mu    sync.Mutex
	limit int
	items map[string]*lruEntry[T]
	order *list.List // keys, most recent first
}

func newLRU[T any](limit int) *lru[T] {
	return &lru[T]{limit: limit, items: map[string]*lruEntry[T]{}, order: list.New()}
}

func (c *lru[T]) get(key string, compile func(string) (T, error)) (T, error) {
	if len(key) > maxCachedPatternLen {
		return compile(key)
	}

	c.mu.Lock()
	if e, ok := c.items[key]; ok {
		c.order.MoveToFront(e.elem)
		c.mu.Unlock()
		return e.val, e.err
	}
	c.mu.Unlock()

	// Compile without the lock so a miss never blocks other shards. A panic
	// propagates before anything is inserted.
	val, err := compile(key)

	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		return e.val, e.err
	}
	c.items[key] = &lruEntry[T]{val: val, err: err, elem: c.order.PushFront(key)}
	if c.order.Len() > c.limit {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(string))
	}
	return val, err
}
