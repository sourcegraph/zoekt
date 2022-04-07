package main

import (
	"container/heap"
	"encoding/gob"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type queueItem struct {
	// RepoID is the ID of the repo
	RepoID uint32
	// Opts are the options to use when indexing repoID.
	Opts IndexOptions
	// indexed is true if opts has been indexed.
	Indexed bool
	// IndexState is the indexState of the last attempt at indexing repoID.
	IndexState indexState
	// HeapIdx is the index of the item in the heap. If < 0 then the item is
	// not on the heap.
	HeapIdx int
	// Seq is a sequence number used as a tiebreaker. This is to ensure we
	// act like a FIFO queue.
	Seq int64
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
	opts = item.Opts

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
	if !reflect.DeepEqual(item.Opts, opts) {
		item.Indexed = false
		item.Opts = opts
	}
	if item.HeapIdx < 0 {
		q.seq++
		item.Seq = q.seq
		heap.Push(&q.pq, item)
		metricQueueLen.Set(float64(len(q.pq)))
		metricQueueCap.Set(float64(len(q.items)))
	} else {
		heap.Fix(&q.pq, item.HeapIdx)
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
		} else if item.HeapIdx < 0 {
			q.seq++
			item.Seq = q.seq
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
		f(&item.Opts)
	}
}

// debugEncodeSortedGobStream gob-encodes all items contained in the queue
// (sorted by priority) to the provided writer.
func (q *Queue) debugEncodeSortedGobStream(w io.Writer) error {
	enc := gob.NewEncoder(w)

	var err error
	q.debugIteratedOrdered(func(item *queueItem) {
		if err != nil {
			return
		}

		err = enc.Encode(streamQueueReply{
			Type: streamReplyQueueItem,
			Item: item,
		})
	})

	if err != nil {
		reply := streamQueueReply{
			Type:        streamReplyError,
			ErrorString: fmt.Sprintf("encoding queueItem: %s", err),
		}

		replyErr := enc.Encode(reply)
		if replyErr != nil {
			return fmt.Errorf("encoding streamError reply: %w", replyErr)
		}

		return fmt.Errorf("encoding queueItem: %w", err)
	}

	err = enc.Encode(streamQueueReply{
		Type: streamReplyDone,
	})

	if err != nil {
		return fmt.Errorf("terminating stream: %w", err)
	}

	return nil
}

// debugIteratedOrdered will call f on each queueItem (sorted by indexing priority)
// known to the queue, including queueItems that have been popped from the queue).
//
// Note: The mutex is held during all calls to f. Callers must not modify the queueItems.
func (q *Queue) debugIteratedOrdered(f func(*queueItem)) {
	q.mu.Lock()
	defer q.mu.Unlock()

	queueItems := make([]*queueItem, len(q.items))

	i := 0
	for _, item := range q.items {
		queueItems[i] = item
		i++
	}

	sort.Slice(queueItems, func(i, j int) bool {
		x, y := queueItems[i], queueItems[j]
		return lessQueueItemPriority(x, y)
	})

	for _, item := range queueItems {
		f(item)
	}
}

type queueStreamReplyKind int

const (
	streamReplyQueueItem queueStreamReplyKind = iota
	streamReplyDone
	streamReplyError
)

type streamQueueReply struct {
	Type        queueStreamReplyKind
	Item        *queueItem
	ErrorString string
}

const gobStreamContentType = "application/x-gob-stream"

func (q *Queue) handleDebugQueue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method must be GET", http.StatusBadRequest)
		return
	}

	desiredContentType := r.Header.Get("Accept")
	if desiredContentType != gobStreamContentType {
		http.Error(w, fmt.Sprintf("unsupported content type: %q", desiredContentType), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", gobStreamContentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	err := q.debugEncodeSortedGobStream(w)
	if err != nil {
		http.Error(w, fmt.Sprintf("encoding gob stream: %s", err), http.StatusInternalServerError)
	}
}

// SetIndexed sets what the currently indexed options are for opts.RepoID.
func (q *Queue) SetIndexed(opts IndexOptions, state indexState) {
	q.mu.Lock()
	item := q.get(opts.RepoID)

	item.IndexState = state
	if state != indexStateFail {
		item.Indexed = reflect.DeepEqual(opts, item.Opts)
	}

	if item.HeapIdx >= 0 {
		// We only update the position in the queue, never add it.
		heap.Fix(&q.pq, item.HeapIdx)
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
		if _, ok := set[item.Opts.RepoID]; ok {
			continue
		}

		if item.HeapIdx >= 0 {
			heap.Remove(&q.pq, item.HeapIdx)
		}

		item.IndexState = ""

		delete(q.items, item.Opts.RepoID)
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
			RepoID:  repoID,
			HeapIdx: -1,
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
	return lessQueueItemPriority(x, y)
}

func (pq pqueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].HeapIdx = i
	pq[j].HeapIdx = j
}

func (pq *pqueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*queueItem)
	item.HeapIdx = n
	*pq = append(*pq, item)
}

func (pq *pqueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	item.HeapIdx = -1
	*pq = old[0 : n-1]
	return item
}

// lessQueueItemPriority returns true if the indexing priority x is less than that of y.
func lessQueueItemPriority(x, y *queueItem) bool {
	// If we know x needs an update and y doesn't, then return true. Otherwise
	// they are either equal priority or y is more urgent.
	if x.Indexed != y.Indexed {
		return !x.Indexed
	}

	if xFail, yFail := x.IndexState == indexStateFail, y.IndexState == indexStateFail; xFail != yFail {
		// if you failed to index, you are likely to fail again. So prefer
		// non-failed.
		return !xFail
	}

	// tiebreaker is to prefer the item added to the queue first
	return x.Seq < y.Seq
}

// queueItemStreamDecoder processes streams of gob-encoded queueItems.
type queueItemStreamDecoder struct {
	gobDecoder *gob.Decoder
	item       *queueItem

	err error
}

// newQueueItemStreamDecoder returns a decoder that will process the stream
// from the provided reader.
func newQueueItemStreamDecoder(r io.Reader) *queueItemStreamDecoder {
	d := gob.NewDecoder(r)

	return &queueItemStreamDecoder{
		gobDecoder: d,

		item: nil,
		err:  nil,
	}
}

// Next advances the decoder to the next queueItem in the stream, which can then
// be retrieved using the Item method. Next returns false when the
// decoder reaches the end of the input, or if the decoder encountered an error
// while decoding the stream.
//
// After Next returns false, the Err method can be used to retrieve the first error
// (besides io.EOF) that the decoder may have encountered while processing the stream.
func (d *queueItemStreamDecoder) Next() bool {
	if d.err != nil {
		return false
	}

	var reply streamQueueReply
	err := d.gobDecoder.Decode(&reply)
	if err != nil {
		if err == io.EOF {
			// the input terminated before it yielded all of its items
			d.err = io.ErrUnexpectedEOF
			return false
		}

		d.err = fmt.Errorf("decoding gob value: %w", err)
		return false
	}

	if reply.Type == streamReplyQueueItem {
		d.item = reply.Item
		return true
	}

	switch reply.Type {
	case streamReplyDone:
		d.err = io.EOF
	case streamReplyError:
		d.err = fmt.Errorf(reply.ErrorString)
	default:
		d.err = fmt.Errorf("unknown stream reply type: %d", reply.Type)
	}

	return false
}

// Err returns the first non io.EOF error that was encountered by the decoder while processing
// the stream.
func (d *queueItemStreamDecoder) Err() error {
	if d.err != nil && d.err != io.EOF {
		return d.err
	}

	return nil
}

// Item returns the most recent queueItem that was decoded by a call to
// Next.
func (d *queueItemStreamDecoder) Item() *queueItem {
	return d.item
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
