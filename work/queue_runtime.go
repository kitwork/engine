package work

import (
	"database/sql"
	"fmt"
	"sync"

	queuestore "github.com/kitwork/engine/utilities/queue"
)

const queueResourceName = "queue"

// queueRuntime is the worker resource owned by app.Runtime — one per identity, shared by every
// domain of the app, exactly like the scheduler. It lives in work rather than utilities because the
// handlers it runs are Kitwork bytecode, and only a Tenant can execute that.
type queueRuntime struct {
	owner *Tenant

	mu       sync.Mutex
	handlers []*QueueHandler
	byName   map[string]*QueueHandler
	inflight map[string]int // queue name → messages this node is running right now
	cancels  []chan struct{}
	wg       sync.WaitGroup
	db       *sql.DB
	store    queuestore.Store
	node     string
	closed   bool
}

// handlerFor resolves a claimed message's queue to the code that runs it.
func (q *queueRuntime) handlerFor(name string) *QueueHandler {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.byName[name]
}

// capacity is how many messages this node could start right now: the sum of every queue's unused
// concurrency. Claiming more than this would hide work from idle nodes behind a lease this node
// cannot act on yet.
func (q *queueRuntime) capacity() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return 0
	}
	total := 0
	for _, handler := range q.handlers {
		free := handler.Parallel - q.inflight[handler.Name]
		if free > 0 {
			total += free
		}
	}
	return total
}

// reserve takes one of a queue's concurrency slots, reporting whether it got one. Aggregate capacity
// is checked before claiming, but the two are not the same check: a claim can return several
// messages of ONE queue, and only this per-queue reservation keeps that within its bound.
func (q *queueRuntime) reserve(handler *QueueHandler) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	if q.inflight == nil {
		q.inflight = make(map[string]int)
	}
	if q.inflight[handler.Name] >= handler.Parallel {
		return false
	}
	q.inflight[handler.Name]++
	return true
}

func (q *queueRuntime) release(handler *QueueHandler) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.inflight[handler.Name] > 0 {
		q.inflight[handler.Name]--
	}
}

func (q *queueRuntime) Close() {
	if q == nil {
		return
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		q.wg.Wait()
		return
	}
	q.closed = true
	for _, cancel := range q.cancels {
		close(cancel)
	}
	q.cancels = nil
	q.mu.Unlock()
	q.wg.Wait()
}

// startRun registers one in-flight job with the wait group, refusing once the runtime is closing.
// Checking and adding under the same lock is the point: a job accepted after Close() began would be
// running while the app tears its resources down.
func (q *queueRuntime) startRun() bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	q.wg.Add(1)
	return true
}

func (t *Tenant) queueWorker() *queueRuntime {
	if t == nil || t.appRuntime == nil {
		return nil
	}
	current, _ := t.appRuntime.Resource(queueResourceName).(*queueRuntime)
	return current
}

func (t *Tenant) ensureQueueWorker() (*queueRuntime, error) {
	if t == nil || t.appRuntime == nil {
		return nil, fmt.Errorf("app runtime is unavailable")
	}
	if current := t.queueWorker(); current != nil {
		return current, nil
	}
	candidate := &queueRuntime{owner: t}
	resource, installed, err := t.appRuntime.InstallResource(queueResourceName, candidate)
	if err != nil {
		return nil, err
	}
	if !installed {
		current, ok := resource.(*queueRuntime)
		if !ok {
			return nil, fmt.Errorf("app queue resource has unexpected type %T", resource)
		}
		return current, nil
	}
	return candidate, nil
}

// queueNodeID is this tenant instance's lease owner. It reuses the scheduler's per-process node id:
// a node is a node, and giving the queue a second identity would make one process look like two in
// the shared store's lease bookkeeping.
func (t *Tenant) queueNodeID() string {
	if worker := t.queueWorker(); worker != nil && worker.node != "" {
		return worker.node
	}
	return t.nodeID()
}
