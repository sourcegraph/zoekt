package main

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// indexMutex is the concurrency control we have for operations that operate
// on the index directory. We have two broad operations: global and repository
// specific. A global operation is like a write lock on the whole directory. A
// repository operation ensure we don't have multiple operations happening for
// the same repository.
type indexMutex struct {
	// indexMu protects state in index directory. global takes write lock, repo
	// takes read lock.
	indexMu sync.RWMutex

	// runningMu protects running. You need to first be holding indexMu.
	runningMu sync.Mutex

	// Match both names (legacy shard paths) and IDs (renames and ID-based paths).
	running map[indexRepository]struct{}
}

// With runs f if no job with the same repository name or ID is running.
// If f runs true is returned, otherwise false is returned.
//
// With blocks if f runs or the Global lock is held.
func (m *indexMutex) With(repoName string, repoID uint32, f func()) bool {
	m.indexMu.RLock()
	defer m.indexMu.RUnlock()

	repo := indexRepository{name: repoName, id: repoID}
	m.runningMu.Lock()
	if m.running == nil {
		m.running = map[indexRepository]struct{}{}
	}
	alreadyRunning := false
	for running := range m.running {
		if running.name == repo.name || running.id == repo.id {
			alreadyRunning = true
			break
		}
	}
	if alreadyRunning {
		m.runningMu.Unlock()
		metricIndexMutexAlreadyRunning.Inc()
		return false
	}
	m.running[repo] = struct{}{}
	m.runningMu.Unlock()

	// release the repository
	defer func() {
		m.runningMu.Lock()
		delete(m.running, repo)
		m.runningMu.Unlock()
	}()

	metricIndexMutexRepo.Inc()
	defer metricIndexMutexRepo.Dec()

	f()

	return true
}

// Global runs f once the global lock is held. IE no other Global or With f's
// will be running.
func (m *indexMutex) Global(f func()) {
	metricIndexMutexGlobal.Inc()
	defer metricIndexMutexGlobal.Dec()

	m.indexMu.Lock()
	defer m.indexMu.Unlock()

	f()
}

type indexRepository struct {
	name string
	id   uint32
}

var (
	metricIndexMutexAlreadyRunning = promauto.NewCounter(prometheus.CounterOpts{
		Name: "index_mutex_already_running_total",
		Help: "Total number of times we skipped processing a repository since an index was already running.",
	})

	metricIndexMutexGlobal = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "index_mutex_global",
		Help: "The number of goroutines trying to or holding the global lock.",
	})

	metricIndexMutexRepo = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "index_mutex_repository",
		Help: "The number of goroutines successfully holding a repo lock.",
	})
)
