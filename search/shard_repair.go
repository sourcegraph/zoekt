package search

import (
	"errors"
	"log"
	"runtime/debug"
	"sync"

	"github.com/sourcegraph/zoekt"
)

const shardRepairConcurrency = 4

var errShardRepairSuperseded = errors.New("shard repair superseded")

type shardRepairRequest struct {
	key     string
	faulted zoekt.Searcher
}

// shardRepairQueue deduplicates repairs by loaded shard instance and bounds
// concurrent open/mmap work when a storage fault affects many shards at once.
type shardRepairQueue struct {
	mu sync.Mutex

	entries  map[zoekt.Searcher]string
	inFlight map[zoekt.Searcher]struct{}
	pending  []shardRepairRequest
	running  int

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
	delete(q.entries, searcher)
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
		q.inFlight = make(map[zoekt.Searcher]struct{})
	}
	if _, ok := q.inFlight[searcher]; ok {
		return false
	}

	q.inFlight[searcher] = struct{}{}
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

	switch {
	case recovered != nil:
		log.Printf("[ERROR] fault while re-opening shard %s: %v\n%s", request.key, recovered, stack)
	case errors.Is(reloadErr, errShardRepairSuperseded):
		log.Printf("[INFO] shard repair superseded by a concurrent update: %s", request.key)
	case reloadErr != nil:
		log.Printf("[ERROR] failed to re-open shard %s: %v", request.key, reloadErr)
	default:
		log.Printf("[INFO] re-opened shard after recovered fault: %s", request.key)
	}

	q.mu.Lock()
	delete(q.inFlight, request.faulted)
	q.running--
	q.startPendingLocked()
	q.mu.Unlock()
}

func (q *shardRepairQueue) idle() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.inFlight) == 0 && len(q.pending) == 0 && q.running == 0
}
