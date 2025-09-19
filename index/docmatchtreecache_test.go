package index

import (
	"testing"
)

func TestDocMatchTreeCache_Basic(t *testing.T) {
	cache := newDocMatchTreeCache(2)

	mt1 := &docMatchTree{}
	mt2 := &docMatchTree{}
	mt3 := &docMatchTree{}
	mt4 := &docMatchTree{}

	// Add and Get
	cache.Add("f1", "v1", mt1)
	cache.Add("f2", "v2", mt2)
	if v, ok := cache.Get("f1", "v1"); !ok || v != mt1 {
		t.Errorf("expected mt1, got %v", v)
	}
	if v, ok := cache.Get("f2", "v2"); !ok || v != mt2 {
		t.Errorf("expected mt2, got %v", v)
	}

	// Add triggers eviction
	cache.Add("f3", "v3", mt3)
	if _, ok := cache.Get("f1", "v1"); ok {
		t.Errorf("expected 'f1','v1' to be evicted")
	}
	if v, ok := cache.Get("f2", "v2"); !ok || v != mt2 {
		t.Errorf("expected mt2, got %v", v)
	}
	if v, ok := cache.Get("f3", "v3"); !ok || v != mt3 {
		t.Errorf("expected mt3, got %v", v)
	}

	// Access order updates
	cache.Get("f2", "v2")
	cache.Add("f4", "v4", mt4)
	if _, ok := cache.Get("f3", "v3"); ok {
		t.Errorf("expected 'f3','v3' to be evicted after 'f2','v2' was used")
	}
	if v, ok := cache.Get("f2", "v2"); !ok || v != mt2 {
		t.Errorf("expected mt2, got %v", v)
	}
	if v, ok := cache.Get("f4", "v4"); !ok || v != mt4 {
		t.Errorf("expected mt4, got %v", v)
	}
}
