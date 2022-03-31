package main

import (
	"container/heap"
	"reflect"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type queueItem struct {
	// repoID is the ID of the repo
	repoID uint32
	// opts are the options to use when indexing repoID.
	opts IndexOptions
	// indexed is true if opts has been indexed.
	indexed bool
	// indexState is the indexState of the last attempt at indexing repoID.
	indexState indexState
	// heapIdx is the index of the item in the heap. If < 0 then the item is
	// not on the heap.
	heapIdx int
	// seq is a sequence number used as a tie breaker. This is to ensure we
	// act like a FIFO queue.
	seq int64
}

// Queue is a priority queue which returns the next repo to index. It is safe
// to use concurrently. It is a min queue on:
//
//    (!indexed, time added to the queue)
//
// We use the above since:
//
// * We rather index a repo sooner if we know the commit is stale.
// * The order of repos returned by Sourcegraph API are ordered by importance.
type Queue struct {
	mu    sync.Mutex
	items map[uint32]*queueItem
	pq    pqueue
	seq   int64
}

// Pop returns the opts of the next repo to index. If the queue is empty ok is
// false.
func (q *Queue) Pop() (opts IndexOptions, ok bool) {
	q.mu.Lock()
	if len(q.pq) == 0 {
		q.mu.Unlock()
		return IndexOptions{}, false
	}
	item := heap.Pop(&q.pq).(*queueItem)
	opts = item.opts

	metricQueueLen.Set(float64(len(q.pq)))
	metricQueueCap.Set(float64(len(q.items)))

	q.mu.Unlock()
	return opts, true
}

// Len returns the number of items in the queue.
func (q *Queue) Len() int {
	q.mu.Lock()
	l := len(q.pq)
	q.mu.Unlock()
	return l
}

// AddOrUpdate sets which opts to index next. If opts.RepoID is already in the
// queue, it is updated.
func (q *Queue) AddOrUpdate(opts IndexOptions) {
	q.mu.Lock()
	item := q.get(opts.RepoID)
	if !reflect.DeepEqual(item.opts, opts) {
		item.indexed = false
		item.opts = opts
	}
	if item.heapIdx < 0 {
		q.seq++
		item.seq = q.seq
		heap.Push(&q.pq, item)
		metricQueueLen.Set(float64(len(q.pq)))
		metricQueueCap.Set(float64(len(q.items)))
	} else {
		heap.Fix(&q.pq, item.heapIdx)
	}
	q.mu.Unlock()
}

// Bump will take any repository in ids which is not on the queue and
// re-insert it with the last known IndexOptions. Bump returns ids that are
// unknown to the queue.
func (q *Queue) Bump(ids []uint32) []uint32 {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.items == nil {
		q.init()
	}

	var missing []uint32
	for _, id := range ids {
		item, ok := q.items[id]
		if !ok {
			missing = append(missing, id)
		} else if item.heapIdx < 0 {
			q.seq++
			item.seq = q.seq
			heap.Push(&q.pq, item)
			metricQueueLen.Set(float64(len(q.pq)))
			metricQueueCap.Set(float64(len(q.items)))
		}
	}

	return missing
}

// Iterate will call f on each item known to the queue, including items that
// have been popped from the queue. Note: this is done in a random order and
// the queue mutex is held during all calls to f. Do not mutate the data.
func (q *Queue) Iterate(f func(*IndexOptions)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, item := range q.items {
		f(&item.opts)
	}
}

// SetIndexed sets what the currently indexed options are for opts.RepoID.
func (q *Queue) SetIndexed(opts IndexOptions, state indexState) {
	q.mu.Lock()
	item := q.get(opts.RepoID)

	item.indexState = state
	if state != indexStateFail {
		item.indexed = reflect.DeepEqual(opts, item.opts)
	}

	if item.heapIdx >= 0 {
		// We only update the position in the queue, never add it.
		heap.Fix(&q.pq, item.heapIdx)
	}

	q.mu.Unlock()
}

// MaybeRemoveMissing will remove all queue items not in ids and return the
// number of names removed from the queue. It will heuristically not run to
// conserve resources.
//
// In the server's steady state we expect that the list of names is equal to
// the items in queue. As such in the steady state this function should do no
// removals. Removal requires memory allocation and coarse locking. To avoid
// that we use a heuristic which can falsely decide it doesn't need to
// remove. However, we will converge onto removing items.
func (q *Queue) MaybeRemoveMissing(ids []uint32) uint {
	q.mu.Lock()
	sameSize := len(q.items) == len(ids)
	q.mu.Unlock()

	// heuristically skip expensive work
	if sameSize {
		debug.Printf("skipping MaybeRemoveMissing due to same size: %d", len(ids))
		return 0
	}

	set := make(map[uint32]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	var count uint
	for _, item := range q.items {
		if _, ok := set[item.opts.RepoID]; ok {
			continue
		}

		if item.heapIdx >= 0 {
			heap.Remove(&q.pq, item.heapIdx)
		}

		item.indexState = ""

		delete(q.items, item.opts.RepoID)
		count++
	}

	metricQueueLen.Set(float64(len(q.pq)))
	metricQueueCap.Set(float64(len(q.items)))

	return count
}

// get returns the item for repoID. If the repoID hasn't been seen before, it
// is added to q.items.
//
// Note: get requires that q.mu is held.
func (q *Queue) get(repoID uint32) *queueItem {
	if q.items == nil {
		q.init()
	}

	item, ok := q.items[repoID]
	if !ok {
		item = &queueItem{
			repoID:  repoID,
			heapIdx: -1,
		}
		q.items[repoID] = item
	}

	return item
}

func (q *Queue) init() {
	q.items = map[uint32]*queueItem{}
	q.pq = make(pqueue, 0)
}

// pqueue implements a priority queue via the interface for container/heap
type pqueue []*queueItem

func (pq pqueue) Len() int { return len(pq) }

func (pq pqueue) Less(i, j int) bool {
	x := pq[i]
	y := pq[j]
	return lessQueueItem(x, y)
}

func (pq pqueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].heapIdx = i
	pq[j].heapIdx = j
}

func (pq *pqueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*queueItem)
	item.heapIdx = n
	*pq = append(*pq, item)
}

func (pq *pqueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	item.heapIdx = -1
	*pq = old[0 : n-1]
	return item
}

//
func lessQueueItem(x, y *queueItem) bool {
	// If we know x needs an update and y doesn't, then return true. Otherwise
	// they are either equal priority or y is more urgent.
	if x.indexed != y.indexed {
		return !x.indexed
	}

	if xFail, yFail := x.indexState == indexStateFail, y.indexState == indexStateFail; xFail != yFail {
		// if you failed to index, you are likely to fail again. So prefer
		// non-failed.
		return !xFail
	}

	// tie breaker is to prefer the item added to the queue first
	return x.seq < y.seq
}

var (
	metricQueueLen = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "index_queue_len",
		Help: "The number of repositories in the index queue.",
	})
	metricQueueCap = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "index_queue_cap",
		Help: "The number of repositories tracked by the index queue, including popped items. Should be the same as index_num_assigned.",
	})
)
