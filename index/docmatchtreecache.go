package index

import (
	"container/list"
	"os"
	"strconv"
)

// docMatchTreeCache is an LRU cache for docMatchTrees.
type docMatchTreeCache struct {
	maxEntries int
	ll         *list.List
	cache      map[docMatchTreeCacheKey]*list.Element
}

type docMatchTreeCacheKey struct {
	field string
	value string
}

type docMatchTreeCacheEntry struct {
	key   docMatchTreeCacheKey
	value *docMatchTree
}

// newDocMatchTreeCache creates a new docMatchTreeCache. The cache size can be set
// via the ZOEKT_DOCMATCHTREE_CACHE environment variable. If unset or invalid, defaults to 0 (disabled).
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
		ll:         list.New(),
		cache:      make(map[docMatchTreeCacheKey]*list.Element),
	}
}

func (c *docMatchTreeCache) Get(field, value string) (*docMatchTree, bool) {
	k := docMatchTreeCacheKey{field, value}
	if ele, ok := c.cache[k]; ok {
		c.ll.MoveToFront(ele)
		return ele.Value.(*docMatchTreeCacheEntry).value, true
	}
	return nil, false
}

func (c *docMatchTreeCache) Add(field, value string, mt *docMatchTree) {
	if c.maxEntries == 0 {
		return
	}
	k := docMatchTreeCacheKey{field, value}
	if ele, ok := c.cache[k]; ok {
		c.ll.MoveToFront(ele)
		ele.Value.(*docMatchTreeCacheEntry).value = mt
		return
	}
	ele := c.ll.PushFront(&docMatchTreeCacheEntry{k, mt})
	c.cache[k] = ele
	if c.maxEntries != 0 && c.ll.Len() > c.maxEntries {
		c.removeOldest()
	}
}

func (c *docMatchTreeCache) removeOldest() {
	if ele := c.ll.Back(); ele != nil {
		c.removeElement(ele)
	}
}

func (c *docMatchTreeCache) removeElement(e *list.Element) {
	c.ll.Remove(e)
	kv := e.Value.(*docMatchTreeCacheEntry)
	delete(c.cache, kv.key)
}
