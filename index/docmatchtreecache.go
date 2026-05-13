package index

import (
	"os"
	"strconv"
	"sync"
)

// docMatchTreeCache is a cache for docMatchTrees with random eviction.
type docMatchTreeCache struct {
	maxEntries int
	cache      map[docMatchTreeCacheKey]*docMatchTree
	mu         sync.RWMutex
}

type docMatchTreeCacheKey struct {
	field string
	value string
}

// newDocMatchTreeCache creates a new docMatchTreeCache.
// If cacheSize is 0, the value from the ZOEKT_DOCMATCHTREE_CACHE environment
// variable will be used if it is present.
func newDocMatchTreeCache(cacheSize int) *docMatchTreeCache {
	if v := os.Getenv("ZOEKT_DOCMATCHTREE_CACHE"); cacheSize == 0 && v != "" {
		var err error
		cacheSize, err = strconv.Atoi(v)
		if err != nil {
			cacheSize = 0
		}
	}
	return &docMatchTreeCache{
		maxEntries: cacheSize,
		cache:      make(map[docMatchTreeCacheKey]*docMatchTree),
	}
}

func (c *docMatchTreeCache) Get(field, value string) (*docMatchTree, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	k := docMatchTreeCacheKey{field, value}
	mt, ok := c.cache[k]
	return mt, ok
}

func (c *docMatchTreeCache) Add(field, value string, mt *docMatchTree) {
	if c.maxEntries == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	k := docMatchTreeCacheKey{field, value}
	c.cache[k] = mt
	if len(c.cache) > c.maxEntries {
		c.evictRandom()
	}
}

func (c *docMatchTreeCache) evictRandom() {
	for k := range c.cache {
		delete(c.cache, k)
		break
	}
}
