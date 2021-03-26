package shards

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"golang.org/x/sync/semaphore"
)

// Note: This is a Sourcegraph specific addition to allow long running queries
// along normal interactive queries.

// scheduler is for managing concurrent searches.
type scheduler interface {
	// Acquire blocks until a normal process is created (ie for a search
	// request). See process documentation. It will only return an error if the
	// context expires.
	Acquire(ctx context.Context) (*process, error)

	// Exclusive blocks until an exclusive process is created. An exclusive
	// process is the only running process. See process documentation.
	Exclusive() *process
}

// The ZOEKTSCHED environment variable controls variables within the
// scheduler. It is a comma-separated list of name=val pairs setting these
// named variables:
//
//   disable: setting disable=1 will use the old zoekt scheduler.
//
//   batchdiv: settings batchDiv=X will make the batch queue size 1/X of the
//   interactive queue size. By default it is 4.
//
//   interactiveseconds: settings interactiveseconds=X will allow search
//   queries to run in the larger interactive queue for Xs before moving them
//   to the batch queue.
//
// Note: these tuneables should be regarded as temporary while we experiment
// with our scheduler in production. They should not be relied upon in
// customers/sourcegraph.com in a permanent manor (only temporary).
var zoektSched = parseTuneables(os.Getenv("ZOEKTSCHED"))

// newScheduler returns a scheduler for use in searches. It will return a
// multiScheduler unless that has been disabled with the environment variable
// SCHED_DISABLE. If so it will an equivalent scheduler as upstream zoekt.
func newScheduler(capacity int64) scheduler {
	if zoektSched["disable"] == 1 {
		log.Println("ZOEKTSCHED=disable=1 specified. Using old zoekt scheduler.")
		return &semaphoreScheduler{
			throttle: semaphore.NewWeighted(capacity),
			capacity: capacity,
		}
	}
	return newMultiScheduler(capacity)
}

// multiScheduler is for managing concurrent searches. Its goals are:
//
//   1. Limit the number of concurrent searches.
//   2. Allow exclusive access.
//   3. Co-operatively limit long running searches.
//   4. No tuneables.
//
// ### Limit the number of concurrent searches
//
// Searching is CPU bound, so we can't do better than #CPU queries
// concurrently. If we do so, we just create more memory pressure.
//
// ### Allow exclusive access
//
// During the time the shard list is accessed and a search is actually done on
// a shard it can't be closed. As such while a search is running we do not
// allow any closing of shards. However, we do need to close and add shards as
// the indexer proceeds. To do this we have an exclusive process which will be
// the only one running. This is like a Lock on a RWMutex, while a normal
// search is a RLock.
//
// ### Co-operatively limit long running searches
//
// Some searches are slow. Either due to a hard to execute search query (can't
// use trigram index) or a large number of results. We want to support this
// use case while still allowing interactive queries to be fast.
//
// ### No tuneables
//
// We want to avoid the need to tune the scheduler depending on the workload /
// instance. As such we use a simple design whose inputs are time and number
// of CPUs.
//
// ## Design
//
// We use semaphores to limit the number of running processes. An exclusive
// process acquires the full semaphore. A process represents something which
// has acquired on the semaphore. Every process is either fast or slow. A
// process starts as fast, but is downgraded to slow after a period of
// time. Downgrading relies on a process co-operatively deciding to downgrade.
//
// We intentionally keep the algorithm simple, but have a general interface to
// allow improvements as we learn more.
type multiScheduler struct {
	// Exclusive process holds a write lock, search processes hold read locks.
	mu             *rwmutex
	semInteractive *sema
	semBatch       *sema

	// interactiveDuration is how long we run a search query at interactive
	// priority before downgrading it to a batch/slow query.
	interactiveDuration time.Duration
}

func newMultiScheduler(capacity int64) *multiScheduler {
	batchdiv := zoektSched["batchdiv"]
	if batchdiv == 0 {
		// Burst upto 1/4 of interactive capacity for batch.
		batchdiv = 4
	} else {
		log.Printf("ZOEKTSCHED=batchdiv=%d specified. Batch queue size 1/%d of %d.", batchdiv, batchdiv, capacity)
	}

	batchCap := capacity / int64(batchdiv)
	if batchCap == 0 {
		batchCap = 1
	}

	interactiveseconds := zoektSched["interactiveseconds"]
	if interactiveseconds == 0 {
		interactiveseconds = 5
	} else {
		log.Printf("ZOEKTSCHED=interactiveseconds=%d specified. Search requests will move to batch queue after %d seconds.", interactiveseconds, interactiveseconds)
	}

	return &multiScheduler{
		mu:             newRWMutex(),
		semInteractive: newSema(capacity, "interactive"),
		semBatch:       newSema(batchCap, "batch"),

		interactiveDuration: time.Duration(interactiveseconds) * time.Second,
	}
}

// Acquire implements scheduler.Acquire.
func (s *multiScheduler) Acquire(ctx context.Context) (*process, error) {
	if err := s.mu.RLock(ctx); err != nil {
		return nil, err
	}

	// Start in interactive. yieldFunc will switch us to batch. sem can be nil
	// if we fail while switching to batch. nil value prevents us releasing
	// twice.
	sem := s.semInteractive

	if err := sem.Acquire(ctx); err != nil {
		s.mu.RUnlock()
		return nil, err
	}

	return &process{
		releaseFunc: func() {
			if sem != nil {
				sem.Release()
				sem = nil
			}
			s.mu.RUnlock()
		},
		yieldTimer: newDeadlineTimer(time.Now().Add(s.interactiveDuration)),
		yieldFunc: func(ctx context.Context) error {
			if sem != nil {
				sem.Release()
				sem = nil
			}

			// Try to acquire batch. Only set sem if we succeed so we know we can
			// clean it up. If this fails we assume the process will stop running
			// (ctx has expired).
			semNext := s.semBatch
			if err := semNext.Acquire(ctx); err != nil {
				return err
			}

			sem = semNext
			return nil
		},
	}, nil
}

// Exclusive implements scheduler.Exclusive.
func (s *multiScheduler) Exclusive() *process {
	// Exclusive process holds a write lock on mu, which ensures we have no
	// processes running (search semaphores are empty).
	//
	// exclusive processes will never yield, so we leave yieldTimer and
	// yieldFunc nil.
	s.mu.Lock()
	return &process{
		releaseFunc: func() {
			s.mu.Unlock()
		},
	}
}

// semaphoreScheduler shares a single semaphore for all searches. An exclusive
// process acquires the full semaphore. This is equivalent to how concurrency
// is managed in upstream. It exists as a fallback while we test
// multiScheduler.
type semaphoreScheduler struct {
	throttle *semaphore.Weighted
	capacity int64
}

// Acquire implements scheduler.Acquire.
func (s *semaphoreScheduler) Acquire(ctx context.Context) (*process, error) {
	return s.acquire(ctx, 1)
}

// Exclusive implements scheduler.Exclusive.
func (s *semaphoreScheduler) Exclusive() *process {
	// Won't error since context.Background won't expire.
	proc, _ := s.acquire(context.Background(), s.capacity)
	return proc
}

func (s *semaphoreScheduler) acquire(ctx context.Context, weight int64) (*process, error) {
	if err := s.throttle.Acquire(ctx, weight); err != nil {
		return nil, err
	}
	return &process{
		releaseFunc: func() {
			s.throttle.Release(weight)
		},
	}, nil
}

// process represents a running search query or an exclusive process. When the
// process is done a call to Release is required.
type process struct {
	// yieldTimer ensures we only call yieldFunc once after a deadline.
	yieldTimer *deadlineTimer
	// yieldFunc is called once by Yield.
	yieldFunc func(context.Context) error

	// releaseFunc is called once by Release
	releaseFunc func()
}

// Release the resources/locks/semaphores associated with this process. Can
// only be called once.
func (p *process) Release() {
	if p.yieldTimer != nil {
		p.yieldTimer.Stop()
	}

	p.releaseFunc()
}

// Yield may block to allow another process to run. This should be called
// relatively often by a search to allow other processes to run. This can not
// be called concurrently.
//
// The only error it will return is a context error if ctx expires. In that
// case the process should stop running and call Release.
func (p *process) Yield(ctx context.Context) error {
	if p.yieldTimer == nil || !p.yieldTimer.Exceeded() {
		return nil
	}

	// Try to yield. This can return an error if our context expired.
	err := p.yieldFunc(ctx)
	if err != nil {
		return err
	}

	// Successfully yielded. Stop our timer and mark it nil so we don't call
	// yieldFunc again.
	p.yieldTimer.Stop()
	p.yieldTimer = nil

	return nil
}

// newDeadlineTimer returns a timer which fires after deadline. Once it fires
// Exceeded will always return true. Callers must call Stop when done to
// release resources.
func newDeadlineTimer(deadline time.Time) *deadlineTimer {
	return &deadlineTimer{
		t: time.NewTimer(deadline.Sub(time.Now())),
	}
}

type deadlineTimer struct {
	// t.C fires after deadline. Once it fires we set to nil to indicate it has
	// fired.
	t *time.Timer
}

// Exceeded returns true if time is after the deadline.
func (t *deadlineTimer) Exceeded() bool {
	if t.t == nil {
		return true
	}
	select {
	case <-t.t.C:
	default:
		return false
	}

	t.Stop()

	return true
}

// Stop stops the underlying timer. Can be called multiple times.
func (t *deadlineTimer) Stop() {
	if t.t == nil {
		return
	}
	t.t.Stop()
	t.t = nil
}

// parseTuneables parses a comma separated string of key=value pairs. "=value"
// is optional, defaults to 1. value is expected to be an int. Errors are
// ignored (value will be 0).
func parseTuneables(v string) map[string]int {
	m := map[string]int{}

	for _, kv := range strings.Split(v, ",") {
		if kv == "" {
			continue
		}

		p := strings.SplitN(kv, "=", 2)
		if len(p) == 1 {
			m[p[0]] = 1
		} else {
			m[p[0]], _ = strconv.Atoi(p[1])
		}
	}

	return m
}

// We use a gauge and counter to track the number of processes in each
// state. They can be one of the following states:
//
//   1. global queued
//   2. interactive queued
//   3. interactive running
//   4. batch queued
//   5. batch running
//
// From each state you either transition to the next state or the process
// ends.
//
// Additionally once a process transitions from "global queued" it will be
// "global running" until termination. This is an additional state on top of
// the ones listed above.
//
// Global refers to the global scheduler lock. A process can only be blocked
// in global queued if an exclusive lock has been acquired.
//
// We have counters for each possible reason a process finished:
//
//   - interactive timedout
//   - batch timedout
//   - released
//
// We have separate gauges and counters for exclusive processes which match
// what we track for normal processes:
//
//   - exclusive queued
//   - exclusive running
var (
	metricSched = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "zoekt_shards_sched",
		Help: "The current number of zoekt scheduler processes in a state.",
	}, []string{"type", "state"})
	metricSchedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "zoekt_shards_sched_total",
		Help: "The total number of zoekt scheduler processes in a state.",
	}, []string{"type", "state"})
)

// sema is a semaphore which tracks its state in prometheus.
type sema struct {
	sem *semaphore.Weighted

	metricQueued        *gaugeCounter
	metricRunning       *gaugeCounter
	metricTimedoutTotal prometheus.Counter
}

func newSema(capacity int64, typ string) *sema {
	return &sema{
		sem: semaphore.NewWeighted(capacity),

		metricQueued: &gaugeCounter{
			gauge:   metricSched.WithLabelValues(typ, "queued"),
			counter: metricSchedTotal.WithLabelValues(typ, "queued"),
		},
		metricRunning: &gaugeCounter{
			gauge:   metricSched.WithLabelValues(typ, "running"),
			counter: metricSchedTotal.WithLabelValues(typ, "running"),
		},
		metricTimedoutTotal: metricSchedTotal.WithLabelValues(typ, "timedout"),
	}
}

func (s *sema) Acquire(ctx context.Context) error {
	s.metricQueued.Inc()
	defer s.metricQueued.Dec()

	err := s.sem.Acquire(ctx, 1)
	if err != nil {
		s.metricTimedoutTotal.Inc()
		return err
	}

	s.metricRunning.Inc()

	return nil
}

func (s *sema) Release() {
	s.sem.Release(1)
	s.metricRunning.Dec()
}

// rwmutex is a wrapper around sync.RWMutex. It additionally respects context
// cancellation and will track the state of the mutex in prometheus.
type rwmutex struct {
	mu sync.RWMutex

	metricQueued        *gaugeCounter
	metricRunning       *gaugeCounter
	metricTimedoutTotal prometheus.Counter

	metricExclusiveQueued  *gaugeCounter
	metricExclusiveRunning *gaugeCounter
}

func newRWMutex() *rwmutex {
	return &rwmutex{
		metricQueued: &gaugeCounter{
			gauge:   metricSched.WithLabelValues("global", "queued"),
			counter: metricSchedTotal.WithLabelValues("global", "queued"),
		},
		metricRunning: &gaugeCounter{
			gauge:   metricSched.WithLabelValues("global", "running"),
			counter: metricSchedTotal.WithLabelValues("global", "running"),
		},
		metricTimedoutTotal: metricSchedTotal.WithLabelValues("global", "timedout"),

		metricExclusiveQueued: &gaugeCounter{
			gauge:   metricSched.WithLabelValues("exclusive", "queued"),
			counter: metricSchedTotal.WithLabelValues("exclusive", "queued"),
		},
		metricExclusiveRunning: &gaugeCounter{
			gauge:   metricSched.WithLabelValues("exclusive", "running"),
			counter: metricSchedTotal.WithLabelValues("exclusive", "running"),
		},
	}
}

func (s *rwmutex) RLock(ctx context.Context) error {
	s.metricQueued.Inc()
	defer s.metricQueued.Dec()

	err := rlockAcquire(ctx, &s.mu)
	if err != nil {
		s.metricTimedoutTotal.Inc()
		return err
	}

	s.metricRunning.Inc()

	return nil
}

func (s *rwmutex) RUnlock() {
	s.mu.RUnlock()
	s.metricRunning.Dec()
}

func (s *rwmutex) Lock() {
	s.metricExclusiveQueued.Inc()
	defer s.metricExclusiveQueued.Dec()

	s.mu.Lock()
	s.metricExclusiveRunning.Inc()
}

func (s *rwmutex) Unlock() {
	s.mu.Unlock()
	s.metricExclusiveRunning.Dec()
}

func rlockAcquire(ctx context.Context, mu *sync.RWMutex) error {
	// Lock in goroutine to respect ctx
	done := make(chan struct{})
	go func() {
		mu.RLock()
		close(done)
	}()

	select {
	case <-done:
		return nil

	case <-ctx.Done():
		// We can't cancel RLock. So we wait for it to lock in the background and
		// immediately unlock.
		go func() {
			<-done
			mu.RUnlock()
		}()

		return ctx.Err()
	}
}

// gaugeCounter is a wrapper around a gauge and a counter. Whenever the gauge
// is incremented so is the counter. Decrement only affects the gauge.
type gaugeCounter struct {
	gauge   prometheus.Gauge
	counter prometheus.Counter
}

func (m *gaugeCounter) Inc() {
	m.gauge.Inc()
	m.counter.Inc()
}

func (m *gaugeCounter) Dec() {
	m.gauge.Dec()
}
