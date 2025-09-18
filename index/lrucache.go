package index

import (
	"container/list"
)

type LRUCache[K comparable, V any] struct {
	maxEntries int
	ll         *list.List
	cache      map[K]*list.Element
}

type entry[K comparable, V any] struct {
	key   K
	value V
}

// NewLRUCache creates a new LRUCache with the given max size.
func NewLRUCache[K comparable, V any](maxEntries int) *LRUCache[K, V] {
	return &LRUCache[K, V]{
		maxEntries: maxEntries,
		ll:         list.New(),
		cache:      make(map[K]*list.Element),
	}
}

func (c *LRUCache[K, V]) Get(key K) (V, bool) {
	if ele, ok := c.cache[key]; ok {
		c.ll.MoveToFront(ele)
		return ele.Value.(*entry[K, V]).value, true
	}
	var zero V
	return zero, false
}

func (c *LRUCache[K, V]) Add(key K, value V) {
	if ele, ok := c.cache[key]; ok {
		c.ll.MoveToFront(ele)
		ele.Value.(*entry[K, V]).value = value
		return
	}
	ele := c.ll.PushFront(&entry[K, V]{key, value})
	c.cache[key] = ele
	if c.maxEntries != 0 && c.ll.Len() > c.maxEntries {
		c.removeOldest()
	}
}

func (c *LRUCache[K, V]) Remove(key K) {
	if ele, ok := c.cache[key]; ok {
		c.removeElement(ele)
	}
}

func (c *LRUCache[K, V]) removeOldest() {
	if ele := c.ll.Back(); ele != nil {
		c.removeElement(ele)
	}
}

func (c *LRUCache[K, V]) removeElement(e *list.Element) {
	c.ll.Remove(e)
	kv := e.Value.(*entry[K, V])
	delete(c.cache, kv.key)
}

func (c *LRUCache[K, V]) Len() int {
	return c.ll.Len()
}
