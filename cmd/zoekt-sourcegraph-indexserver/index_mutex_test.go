package main

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIndexMutexRepositoryIdentity(t *testing.T) {
	var mu indexMutex
	require.True(t, mu.With("repo", 1, func() {
		require.False(t, mu.With("renamed", 1, func() {}))
		require.False(t, mu.With("repo", 2, func() {}))
		require.True(t, mu.With("unrelated", 3, func() {}))
	}))
	require.True(t, mu.With("renamed", 1, func() {}))
}

// holdIndex models a long-running index job and releases it even on failure.
func holdIndex(t *testing.T, mu *indexMutex, name string, id uint32) func() {
	t.Helper()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		mu.With(name, id, func() {
			close(started)
			<-release
		})
	}()
	var once sync.Once
	stop := func() { once.Do(func() { close(release); <-done }) }
	t.Cleanup(stop)
	awaitMaintenance(t, started)
	return stop
}

func awaitMaintenance(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("operation blocked behind an unrelated index job")
	}
}

func TestIndexMutexMaintenance(t *testing.T) {
	var mu indexMutex
	stop := holdIndex(t, &mu, "long", 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		mu.Maintain(func(p *indexMaintenance) {
			if p.with(func(repo indexRepository) bool { return repo.name == "long" }, func() {}) {
				t.Error("maintenance touched an active index")
			}
			mu.With("changed-during-scan", 2, func() {})
			if p.with(func(repo indexRepository) bool { return repo.name == "changed-during-scan" }, func() {}) {
				t.Error("maintenance used a snapshot invalidated by a completed index")
			}
			p.with(func(repo indexRepository) bool { return repo.name == "reserved" }, func() {
				if mu.With("reserved", 3, func() {}) {
					t.Error("index started on a maintenance input")
				}
				if !mu.With("unrelated", 4, func() {}) {
					t.Error("unrelated index did not run during maintenance")
				}
			})
			if !mu.With("reserved", 3, func() {}) {
				t.Error("maintenance did not release its input")
			}
		})
	}()
	awaitMaintenance(t, done)
	stop()
	// A fresh pass can maintain repositories skipped by the previous pass.
	mu.Maintain(func(p *indexMaintenance) {
		require.True(t, p.with(func(indexRepository) bool { return true }, func() {}))
	})
}
