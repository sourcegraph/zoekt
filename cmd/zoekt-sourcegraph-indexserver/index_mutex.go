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

	// runningMu protects running and maintenance state. Acquire indexMu first.
	runningMu sync.Mutex

	// Match both names (legacy shard paths) and IDs (renames and ID-based paths).
	running map[indexRepository]struct{}

	maintenanceMu sync.Mutex
	maintenance   *indexMaintenance // protected by runningMu
}

// With runs f if repoName is neither running nor reserved by maintenance.
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
	if alreadyRunning || (m.maintenance != nil && m.maintenance.blocks != nil && m.maintenance.blocks(repo)) {
		m.runningMu.Unlock()
		metricIndexMutexAlreadyRunning.Inc()
		return false
	}
	m.running[repo] = struct{}{}
	if m.maintenance != nil {
		m.maintenance.changed[repo] = struct{}{}
	}
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

// Global runs f exclusively, excluding repository jobs and maintenance passes.
func (m *indexMutex) Global(f func()) {
	metricIndexMutexGlobal.Inc()
	defer metricIndexMutexGlobal.Dec()

	m.indexMu.Lock()
	defer m.indexMu.Unlock()

	f()
}

// TryGlobal runs f only if the directory is idle. Unlike Global it never queues
// a writer behind a long index job, which would block new readers too.
func (m *indexMutex) TryGlobal(f func()) {
	if m.indexMu.TryLock() {
		defer m.indexMu.Unlock()
		metricIndexMutexGlobal.Inc()
		defer metricIndexMutexGlobal.Dec()
		f()
	}
}

// Maintain serializes maintenance passes, but lets unrelated index jobs run.
// The pass must reserve affected repositories with indexMaintenance.with before
// mutating files. Global operations (tenant deletion) still exclude both.
func (m *indexMutex) Maintain(f func(*indexMaintenance)) {
	m.maintenanceMu.Lock()
	defer m.maintenanceMu.Unlock()
	m.indexMu.RLock()
	defer m.indexMu.RUnlock()

	m.runningMu.Lock()
	p := &indexMaintenance{mu: m, changed: make(map[indexRepository]struct{})}
	for repo := range m.running {
		p.changed[repo] = struct{}{}
	}
	m.maintenance = p
	m.runningMu.Unlock()
	defer func() {
		m.runningMu.Lock()
		m.maintenance = nil
		m.runningMu.Unlock()
	}()
	f(p)
}

type indexRepository struct {
	name string
	id   uint32
}

type indexMaintenance struct {
	mu *indexMutex
	// Include jobs that finished during the scan: their writes invalidate its
	// snapshot even though they no longer hold a repository lock.
	changed map[indexRepository]struct{}
	blocks  func(indexRepository) bool
}

// with skips repositories touched since the pass began, and prevents matching
// jobs from starting until f returns. Calls within a pass must be sequential.
func (p *indexMaintenance) with(matches func(indexRepository) bool, f func()) bool {
	p.mu.runningMu.Lock()
	for repo := range p.changed {
		if matches(repo) {
			p.mu.runningMu.Unlock()
			return false
		}
	}
	p.blocks = matches
	p.mu.runningMu.Unlock()
	defer func() {
		p.mu.runningMu.Lock()
		p.blocks = nil
		p.mu.runningMu.Unlock()
	}()
	f()
	return true
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
