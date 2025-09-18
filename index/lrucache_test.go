package index

import (
	"testing"
)

func TestLRUCache_Basic(t *testing.T) {
	cache := NewLRUCache[string, int](2)

	// Add and Get
	cache.Add("a", 1)
	cache.Add("b", 2)
	if v, ok := cache.Get("a"); !ok || v != 1 {
		t.Errorf("expected 1, got %v", v)
	}
	if v, ok := cache.Get("b"); !ok || v != 2 {
		t.Errorf("expected 2, got %v", v)
	}

	// Add triggers eviction
	cache.Add("c", 3)
	if _, ok := cache.Get("a"); ok {
		t.Errorf("expected 'a' to be evicted")
	}
	if v, ok := cache.Get("b"); !ok || v != 2 {
		t.Errorf("expected 2, got %v", v)
	}
	if v, ok := cache.Get("c"); !ok || v != 3 {
		t.Errorf("expected 3, got %v", v)
	}

	// Access order updates
	cache.Get("b")
	cache.Add("d", 4)
	if _, ok := cache.Get("c"); ok {
		t.Errorf("expected 'c' to be evicted after 'b' was used")
	}
	if v, ok := cache.Get("b"); !ok || v != 2 {
		t.Errorf("expected 2, got %v", v)
	}
	if v, ok := cache.Get("d"); !ok || v != 4 {
		t.Errorf("expected 4, got %v", v)
	}
}

func TestLRUCache_Remove(t *testing.T) {
	cache := NewLRUCache[string, int](2)
	cache.Add("a", 1)
	cache.Add("b", 2)
	cache.Remove("a")
	if _, ok := cache.Get("a"); ok {
		t.Errorf("expected 'a' to be removed")
	}
	if v, ok := cache.Get("b"); !ok || v != 2 {
		t.Errorf("expected 2, got %v", v)
	}
}

func TestLRUCache_Len(t *testing.T) {
	cache := NewLRUCache[string, int](2)
	if cache.Len() != 0 {
		t.Errorf("expected len 0, got %d", cache.Len())
	}
	cache.Add("a", 1)
	if cache.Len() != 1 {
		t.Errorf("expected len 1, got %d", cache.Len())
	}
	cache.Add("b", 2)
	if cache.Len() != 2 {
		t.Errorf("expected len 2, got %d", cache.Len())
	}
	cache.Add("c", 3)
	if cache.Len() != 2 {
		t.Errorf("expected len 2 after eviction, got %d", cache.Len())
	}
}
