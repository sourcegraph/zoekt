package main

import (
	"fmt"
	"sync"
	"time"
)

type indexMutex struct {
	indexMu sync.RWMutex

	runningMu sync.Mutex
	running   map[string]struct{}
}

func (m *indexMutex) With(repoName string, f func()) bool {
	m.indexMu.RLock()
	defer m.indexMu.RUnlock()

	m.runningMu.Lock()
	if m.running == nil {
		m.running = map[string]struct{}{}
	}

	_, alreadyRunning := m.running[repoName]
	m.running[repoName] = struct{}{}
	m.runningMu.Unlock()

	if alreadyRunning {
		return false
	}

	defer func() {
		m.runningMu.Lock()
		delete(m.running, repoName)
		m.runningMu.Unlock()
	}()

	f()
	return true
}

func (m *indexMutex) Global(f func()) {
	fmt.Printf("in Global\n")
	m.indexMu.Lock()
	defer m.indexMu.Unlock()
	f()
}

// Aquires a global lock, then waits until running is empty
func (m *indexMutex) GlobalWaitForPending(f func()) {
	fmt.Printf("in globalWaitForPending, waiting for lock\n")
	m.indexMu.Lock()
	defer m.indexMu.Unlock()

	fmt.Printf("waiting for m.running to be empty...\n")
	start := time.Now()
	for {
		t := time.NewTicker(time.Second * 1)
		m.runningMu.Lock()
		defer m.runningMu.Unlock()
		if len(m.running) == 0 {
			break
		}
		<-t.C
	}
	fmt.Printf("m.running empty (waited %s), proceeding with Global f()\n", time.Since(start))

	f()
}
