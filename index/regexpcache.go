package index

import (
	"container/list"
	"log"
	"os"
	"strconv"
	"sync"

	"github.com/grafana/regexp"

	"github.com/sourcegraph/zoekt/internal/hybridre2"
)

const (
	envRegexpCacheSize        = "ZOEKT_REGEXP_CACHE_SIZE"
	envRegexpCachePatternSize = "ZOEKT_REGEXP_CACHE_MAX_PATTERN_BYTES"

	// defaultRegexpCacheSize bounds each cache by entry count. A search
	// compiles the same pattern once per shard, so this only has to cover
	// the searches running at once. With RE2 on, a cached regexp's lazy DFA
	// can grow to go-re2's 128 MiB limit, so the worst case is this times
	// that. Zero disables the cache.
	defaultRegexpCacheSize = 256

	// defaultMaxCachedPatternLen is the longest pattern (in bytes) that is
	// cached. The compiled program grows with pattern length, so longer
	// patterns are compiled per shard as before rather than pinned by the
	// cache. This does not bound RE2's lazy DFA. Typical queries are well
	// below this.
	defaultMaxCachedPatternLen = 4 << 10
)

type regexpCacheConfig struct {
	size       int
	maxPattern int
}

// regexpCache returns the process-wide caches, configured from the
// environment on first use.
var regexpCache = sync.OnceValue(func() *compiledRegexps {
	return newCompiledRegexps(regexpCacheConfigFromEnv())
})

func regexpCacheConfigFromEnv() regexpCacheConfig {
	return regexpCacheConfig{
		size:       envInt(envRegexpCacheSize, defaultRegexpCacheSize),
		maxPattern: envInt(envRegexpCachePatternSize, defaultMaxCachedPatternLen),
	}
}

// envInt reads a non-negative integer from the environment. An unset
// variable gives def; an invalid or negative one logs a warning and gives def.
func envInt(name string, def int) int {
	val, ok := os.LookupEnv(name)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(val)
	if err != nil || n < 0 {
		log.Printf("index: ignoring invalid %s=%q, using %d", name, val, def)
		return def
	}
	return n
}

func newCompiledRegexps(cfg regexpCacheConfig) *compiledRegexps {
	return &compiledRegexps{
		plain:  newLRU[*regexp.Regexp](cfg.size, cfg.maxPattern),
		hybrid: newLRU[*hybridre2.Regexp](cfg.size, cfg.maxPattern),
	}
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
	// maxKeyLen is the longest key that is cached.
	maxKeyLen int
	items     map[string]*lruEntry[T]
	order     *list.List // keys, most recent first
}

func newLRU[T any](limit, maxKeyLen int) *lru[T] {
	return &lru[T]{limit: limit, maxKeyLen: maxKeyLen, items: map[string]*lruEntry[T]{}, order: list.New()}
}

func (c *lru[T]) get(key string, compile func(string) (T, error)) (T, error) {
	if c.limit == 0 || len(key) > c.maxKeyLen {
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
