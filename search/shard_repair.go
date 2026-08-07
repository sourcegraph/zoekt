package search

import (
	"log"
	"runtime/debug"
	"sync"

	"github.com/sourcegraph/zoekt"
)

const shardRepairConcurrency = 4

type shardRepairRequest struct {
	key     string
	faulted zoekt.Searcher
}

// shardRepairQueue deduplicates repairs by shard key and bounds concurrent
// open/mmap work when a storage fault affects many shards at once.
type shardRepairQueue struct {
	mu sync.Mutex

	entries    map[zoekt.Searcher]string
	inFlight   map[string]struct{}
	pending    []shardRepairRequest
	running    int
	unresolved map[string]struct{}

	reload func(string, zoekt.Searcher) error
}

func newShardRepairQueue(reload func(string, zoekt.Searcher) error) *shardRepairQueue {
	return &shardRepairQueue{reload: reload}
}

func (q *shardRepairQueue) register(searcher zoekt.Searcher, key string) {
	if searcher == nil || key == "" {
		return
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if q.entries == nil {
		q.entries = make(map[zoekt.Searcher]string)
	}
	q.entries[searcher] = key
}

func (q *shardRepairQueue) unregister(searcher zoekt.Searcher) {
	if searcher == nil {
		return
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if key, ok := q.entries[searcher]; ok {
		delete(q.entries, searcher)
		delete(q.unresolved, key)
	}
}

func (q *shardRepairQueue) schedule(searcher zoekt.Searcher) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	key, ok := q.entries[searcher]
	if !ok {
		// A concurrent watcher replacement or removal already handled this
		// shard.
		return false
	}
	if q.inFlight == nil {
		q.inFlight = make(map[string]struct{})
	}
	if _, ok := q.inFlight[key]; ok {
		return false
	}

	q.inFlight[key] = struct{}{}
	q.pending = append(q.pending, shardRepairRequest{key: key, faulted: searcher})
	q.startPendingLocked()
	return true
}

func (q *shardRepairQueue) startPendingLocked() {
	for q.running < shardRepairConcurrency && len(q.pending) > 0 {
		request := q.pending[0]
		q.pending[0] = shardRepairRequest{}
		q.pending = q.pending[1:]
		q.running++
		go q.run(request)
	}
}

func (q *shardRepairQueue) run(request shardRepairRequest) {
	log.Printf("[WARN] re-opening shard after recovered fault: %s", request.key)

	var (
		reloadErr error
		recovered any
		stack     []byte
	)
	func() {
		restorePanicOnFault := debug.SetPanicOnFault(true)
		defer func() {
			debug.SetPanicOnFault(restorePanicOnFault)
			if recovered = recover(); recovered != nil {
				stack = debug.Stack()
			}
		}()
		reloadErr = q.reload(request.key, request.faulted)
	}()

	resolved := recovered == nil && reloadErr == nil
	switch {
	case recovered != nil:
		log.Printf("[ERROR] fault while re-opening shard %s: %v\n%s", request.key, recovered, stack)
	case reloadErr != nil:
		log.Printf("[ERROR] failed to re-open shard %s: %v", request.key, reloadErr)
	default:
		log.Printf("[INFO] re-opened shard after recovered fault: %s", request.key)
	}

	q.mu.Lock()
	currentKey, stillRegistered := q.entries[request.faulted]
	if resolved || !stillRegistered || currentKey != request.key {
		delete(q.unresolved, request.key)
	} else {
		if q.unresolved == nil {
			q.unresolved = make(map[string]struct{})
		}
		q.unresolved[request.key] = struct{}{}
	}
	delete(q.inFlight, request.key)
	q.running--
	q.startPendingLocked()
	q.mu.Unlock()
}

func (q *shardRepairQueue) ready() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.inFlight) == 0 && len(q.unresolved) == 0
}
