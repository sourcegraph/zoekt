package index

import (
	"strconv"
	"testing"
)

func TestDocMatchTreeCache_Basic(t *testing.T) {
	cache := newDocMatchTreeCache(2)

	mt1 := &docMatchTree{}
	mt2 := &docMatchTree{}
	mt3 := &docMatchTree{}

	// Add and Get
	cache.Add("f1", "v1", mt1)
	cache.Add("f2", "v2", mt2)
	if v, ok := cache.Get("f1", "v1"); !ok || v != mt1 {
		t.Errorf("expected mt1, got %v", v)
	}
	if v, ok := cache.Get("f2", "v2"); !ok || v != mt2 {
		t.Errorf("expected mt2, got %v", v)
	}

	// Add triggers eviction (random, so one of the two should be evicted)
	cache.Add("f3", "v3", mt3)
	v1, ok1 := cache.Get("f1", "v1")
	v2, ok2 := cache.Get("f2", "v2")
	v3, ok3 := cache.Get("f3", "v3")

	// Should have exactly 2 items
	present := 0
	if ok1 {
		present++
		if v1 != mt1 {
			t.Errorf("expected mt1, got %v", v1)
		}
	}
	if ok2 {
		present++
		if v2 != mt2 {
			t.Errorf("expected mt2, got %v", v2)
		}
	}
	if ok3 {
		present++
		if v3 != mt3 {
			t.Errorf("expected mt3, got %v", v3)
		}
	}
	if present != 2 {
		t.Errorf("expected exactly 2 items in cache, got %d", present)
	}
}

func TestDocMatchTreeCache_Concurrent(t *testing.T) {
	cache := newDocMatchTreeCache(100)

	// Create some test data
	trees := make([]*docMatchTree, 50)
	for i := range trees {
		trees[i] = &docMatchTree{}
	}

	// Start multiple goroutines doing concurrent reads and writes
	const numGoroutines = 10
	const numOperations = 1000

	done := make(chan bool, numGoroutines)

	// Reader goroutines (should be majority of operations)
	for i := 0; i < numGoroutines-1; i++ {
		go func(id int) {
			for j := 0; j < numOperations; j++ {
				field := "field" + strconv.Itoa(j%10)
				value := "value" + strconv.Itoa(j%20)
				cache.Get(field, value)
			}
			done <- true
		}(i)
	}

	// Writer goroutine (fewer write operations)
	go func() {
		for j := 0; j < numOperations/10; j++ {
			field := "field" + strconv.Itoa(j%10)
			value := "value" + strconv.Itoa(j%20)
			tree := trees[j%len(trees)]
			cache.Add(field, value, tree)
		}
		done <- true
	}()

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}
}
