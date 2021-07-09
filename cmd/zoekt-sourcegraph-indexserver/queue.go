package main

import (
	"container/heap"
	"reflect"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type queueItem struct {
	// repoName is the name of the repo
	repoName string
	// opts are the options to use when indexing repoName.
	opts IndexOptions
	// indexed is true if opts has been indexed.
	indexed bool
	// indexState is the indexState of the last attempt at indexing repoName.
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
	items map[string]*queueItem
	pq    pqueue
	seq   int64
}

// Pop returns the repoName and opts of the next repo to index. If the queue
// is empty ok is false.
func (q *Queue) Pop() (repoName string, opts IndexOptions, ok bool) {
	q.mu.Lock()
	if len(q.pq) == 0 {
		q.mu.Unlock()
		return "", IndexOptions{}, false
	}
	item := heap.Pop(&q.pq).(*queueItem)
	repoName = item.repoName
	opts = item.opts

	metricQueueLen.Set(float64(len(q.pq)))
	metricQueueCap.Set(float64(len(q.items)))

	q.mu.Unlock()
	return repoName, opts, true
}

// Len returns the number of items in the queue.
func (q *Queue) Len() int {
	q.mu.Lock()
	l := len(q.pq)
	q.mu.Unlock()
	return l
}

// AddOrUpdate sets which opts to index next for repoName. If repoName is
// already in the queue, it is updated.
func (q *Queue) AddOrUpdate(repoName string, opts IndexOptions) {
	q.mu.Lock()
	item := q.get(repoName)
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

// SetIndexed sets what the currently indexed options are for repoName.
func (q *Queue) SetIndexed(repoName string, opts IndexOptions, state indexState) {
	q.mu.Lock()
	item := q.get(repoName)
	item.setIndexState(state)
	if state != indexStateFail {
		item.indexed = reflect.DeepEqual(opts, item.opts)
	}
	if item.heapIdx >= 0 {
		// We only update the position in the queue, never add it.
		heap.Fix(&q.pq, item.heapIdx)
	}
	q.mu.Unlock()
}

// MaybeRemoveMissing will remove all queue items not in names. It will
// heuristically not run to conserve resources and return -1. Otherwise it
// will return the number of names removed from the queue.
//
// In the server's steady state we expect that the list of names is equal to
// the items in queue. As such in the steady state this function should do no
// removals. Removal requires memory allocation and coarse locking. To avoid
// that we use a heuristic which can falsely decide it doesn't need to
// remove. However, we will converge onto removing items.
func (q *Queue) MaybeRemoveMissing(names []string) int {
	q.mu.Lock()
	sameSize := len(q.items) == len(names)
	q.mu.Unlock()

	// heuristically skip expensive work
	if sameSize {
		return -1
	}

	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	count := 0
	for name, item := range q.items {
		if _, ok := set[name]; ok {
			continue
		}

		if item.heapIdx >= 0 {
			heap.Remove(&q.pq, item.heapIdx)
		}
		item.setIndexState("")
		delete(q.items, name)
		count++
	}

	metricQueueLen.Set(float64(len(q.pq)))
	metricQueueCap.Set(float64(len(q.items)))

	return count
}

// get returns the item for repoName. If the repoName hasn't been seen before,
// it is added to q.items.
//
// Note: get requires that q.mu is held.
func (q *Queue) get(repoName string) *queueItem {
	if q.items == nil {
		q.items = map[string]*queueItem{}
		q.pq = make(pqueue, 0)
	}

	item, ok := q.items[repoName]
	if !ok {
		item = &queueItem{
			repoName: repoName,
			heapIdx:  -1,
		}
		q.items[repoName] = item
	}

	return item
}

// setIndexedState will set indexedState and update the corresponding metrics
// if the state is changing.
func (item *queueItem) setIndexState(state indexState) {
	if state == item.indexState {
		return
	}
	if item.indexState != "" {
		metricIndexState.WithLabelValues(string(item.indexState)).Dec()
	}
	item.indexState = state
	if item.indexState != "" {
		metricIndexState.WithLabelValues(string(item.indexState)).Inc()
	}
}

// pqueue implements a priority queue via the interface for container/heap
type pqueue []*queueItem

func (pq pqueue) Len() int { return len(pq) }

func (pq pqueue) Less(i, j int) bool {
	// If we know x needs an update and y doesn't, then return true. Otherwise
	// they are either equal priority or y is more urgent.
	x := pq[i]
	y := pq[j]
	if x.indexed == y.indexed {
		// tie breaker is to prefer the item added to the queue first
		return x.seq < y.seq
	}
	return !x.indexed
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

var (
	metricQueueLen = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "index_queue_len",
		Help: "The number of repositories in the index queue.",
	})
	metricQueueCap = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "index_queue_cap",
		Help: "The number of repositories tracked by the index queue, including popped items. Should be the same as index_num_assigned.",
	})
	metricIndexState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "index_state_count",
		Help: "The count of repositories per the state of the last index.",
	}, []string{"state"})
)
