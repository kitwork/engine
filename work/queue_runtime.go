package work

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kitwork/engine/compiler"
	queuestore "github.com/kitwork/engine/utilities/queue"
	"github.com/kitwork/engine/value"
)

const queueResourceName = "queue"

// QueueHandler is a job the worker runs: what a file of .shared/queue/ exported, with the bytecode its
// handler belongs to (each file compiles to its own; the runner resets a pooled VM onto it). On the worker of
// kind schedule it is a schedule, what a file of .schedule/ exported.
type QueueHandler struct {
	Name string
	// Source is the file from the app's folder (.shared/queue/camOn.work); Place where queue.job is written.
	Source   string
	Place    string
	Callback *value.Lambda
	Bytecode *compiler.Bytecode
	// Retry is the attempts after the first (.retry(n)); Timeout bounds each attempt, 0 for none.
	Retry       int
	Timeout     time.Duration
	TimeoutText string
	ContentHash string
}

// queueDefaultParallel and queueDefaultKeep are a queue's when its config.work does not say: how many runs
// one machine runs at once, and how long an ended run is kept.
const (
	queueDefaultParallel = 4
	queueDefaultKeep     = 30 * 24 * time.Hour
)

// errQueueCancelled is the cause an attempt is stopped with when its run is cancelled.
var errQueueCancelled = errors.New("lần chạy bị huỷ")

// queueNever is a time the poller never reaches: nothing to look for.
var queueNever = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// queueRuntime is the worker resource owned by app.Runtime — one per app and kind (the queue's, and the
// schedules' with their clock), shared by every site of it. It lives in work rather than utilities because
// the handlers it runs are Kitwork bytecode, and only a Tenant can execute that.
type queueRuntime struct {
	owner *Tenant
	// kind is the runs of kitwork_runs the worker runs: queueKind, or scheduleKind for the worker of the
	// app's schedules, whose book is their rules and their due times (nil for the queue's).
	kind string
	book *scheduleBook

	lifecycle sync.Mutex
	mu        sync.Mutex
	handlers  []*QueueHandler
	byName    map[string]*QueueHandler
	parallel  int
	keep      time.Duration
	// runs is the database of the host's catalogue the runs of the kind live in, as the config.work of its
	// folder chose with queue.database(db) or schedule.database(db) (runs_database.go); nil is the app's
	// database. A send, a read and the worker started next all read it.
	runs atomic.Pointer[runsPlace]
	// unloaded is what the last load could not read (a file that did not compile or run, a job whose
	// declaration has a problem); withheld is the jobs of it, by name: the jobs with a problem, and those
	// kitwork_definitions holds from a file that did not load. Their runs stay pending, attempts untouched,
	// until the file loads again: a mistake in a deploy is not the removal of the job, whose runs would
	// otherwise end failed as runs of no job.
	unloaded queuestore.Withheld
	withheld map[string]bool
	// next is when the poller looks for due runs again: when the first pending run it knows of is due
	// (queueNever when none). A send, a replay, a retry and a reclaim bring it forward (noteDue), so an
	// idle app's poll reads nothing, and never takes the database's writer gate.
	next time.Time
	// reclaimAt is when the poller gives back lost leases again: when the first lease no attempt here
	// holds runs out (one a stopped process left), or a lease of a settle that could not be written.
	reclaimAt time.Time
	// polls counts the poller's turns.
	polls atomic.Int64
	// attempts are the attempts this process runs now, each until its goroutine ends. Two can be of one
	// run: an attempt past its .timeout() is settled at once but runs on until its VM stops (a host call it
	// is blocked in returns first), while the retry of the same run already runs. Each is its own entry, so
	// the one that ends never takes the other's out (its lease, its heartbeat, its room in parallel).
	attempts map[*queueAttempt]struct{}
	wake     chan struct{}
	cancels  []chan struct{}
	wg       sync.WaitGroup
	running  bool
	closed   bool
}

// queueAttempt is one attempt running here: its run, the lease it holds and how to stop it.
type queueAttempt struct {
	id    string
	token string
	stop  context.CancelCauseFunc
}

// The kinds of kitwork_runs: a job's runs, a schedule's.
const (
	queueKind    = "queue"
	scheduleKind = "schedule"
)

// label is how the worker's lines on the server's output begin.
func (q *queueRuntime) label() string {
	if q.kind == scheduleKind {
		return "[Schedule]"
	}
	return "[Queue]"
}

// noun is what the worker runs, as a line names one: job, lịch.
func (q *queueRuntime) noun() string {
	if q.kind == scheduleKind {
		return "lịch"
	}
	return "job"
}

// handlerFor resolves a claimed run's job to the code that runs it.
func (q *queueRuntime) handlerFor(name string) *QueueHandler {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.byName[name]
}

// declares reports a job the app's .shared/queue/ declares now: loaded, or withheld until its file loads.
func (q *queueRuntime) declares(name string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.byName[name] != nil || q.withheld[name]
}

// withheldJobs is the set of withheld jobs; the map is replaced, never changed, so it is read unlocked.
func (q *queueRuntime) withheldJobs() map[string]bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.withheld
}

// noteDue brings the poller's next look forward to at.
func (q *queueRuntime) noteDue(at time.Time) {
	q.mu.Lock()
	if at.Before(q.next) {
		q.next = at
	}
	q.mu.Unlock()
}

// takeDue reports whether a run may be due at now, and if so takes the look: until the claim says when
// the next one is due, only a noteDue brings the look forward.
func (q *queueRuntime) takeDue(now time.Time) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if now.Before(q.next) {
		return false
	}
	q.next = queueNever
	return true
}

// noteReclaim brings the poller's next reclaim forward to at.
func (q *queueRuntime) noteReclaim(at time.Time) {
	q.mu.Lock()
	if at.Before(q.reclaimAt) {
		q.reclaimAt = at
	}
	q.mu.Unlock()
}

// takeReclaim is takeDue for the reclaim of lost leases.
func (q *queueRuntime) takeReclaim(now time.Time) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if now.Before(q.reclaimAt) {
		return false
	}
	q.reclaimAt = queueNever
	return true
}

// capacity is how many runs this machine could start now: parallel less the attempts running, those past
// their time that have not stopped yet included (each is a VM). Claiming more would hold runs behind a
// lease this worker cannot act on yet.
func (q *queueRuntime) capacity() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || !q.running {
		return 0
	}
	return q.parallel - len(q.attempts)
}

// startAttempt registers an attempt with the wait group, refusing once the runtime is closing: an attempt
// accepted after Close began would run while the app tears its resources down.
func (q *queueRuntime) startAttempt(attempt *queueAttempt) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || !q.running {
		return false
	}
	if q.attempts == nil {
		q.attempts = map[*queueAttempt]struct{}{}
	}
	q.attempts[attempt] = struct{}{}
	q.wg.Add(1)
	return true
}

// endAttempt takes the attempt out, and only it: another attempt of the same run stays.
func (q *queueRuntime) endAttempt(attempt *queueAttempt) {
	q.mu.Lock()
	delete(q.attempts, attempt)
	q.mu.Unlock()
	q.wg.Done()
}

// held is a copy of the attempts running here.
func (q *queueRuntime) held() []*queueAttempt {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]*queueAttempt, 0, len(q.attempts))
	for attempt := range q.attempts {
		out = append(out, attempt)
	}
	return out
}

// stopAttempt stops the attempts of a cancelled run, if this process runs one.
func (q *queueRuntime) stopAttempt(id string) {
	for _, attempt := range q.held() {
		if attempt.id == id {
			attempt.stop(errQueueCancelled)
		}
	}
}

// Drain stops claiming and waits for the attempts running here. app.Runtime.Close drains a resource
// before it closes any: an attempt that ends during the wait still writes its outcome, through the app's
// database and the kitwork:runs resource, both still open then.
func (q *queueRuntime) Drain() { q.Close() }

func (q *queueRuntime) Close() {
	if q == nil {
		return
	}
	q.lifecycle.Lock()
	defer q.lifecycle.Unlock()

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		q.wg.Wait()
		return
	}
	q.closed = true
	stopQueueWorkerNoLock(q)
	q.mu.Unlock()
	q.wg.Wait()
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
	candidate := &queueRuntime{owner: t, kind: queueKind, wake: make(chan struct{}, 1), parallel: queueDefaultParallel,
		keep: queueDefaultKeep, next: queueNever, reclaimAt: queueNever}
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

// wakeQueueWorker tells the app's worker a run is due now, so it claims before its next tick.
func (t *Tenant) wakeQueueWorker() { t.queueRunDue(time.Time{}) }

// queueRunDue tells the app's worker a pending run is due at at (stored by a send, a replay): it looks for
// it then, at once when it is due now.
func (t *Tenant) queueRunDue(at time.Time) {
	worker := t.queueWorker()
	if worker == nil {
		return
	}
	worker.noteDue(at)
	if !time.Now().Before(at) {
		select {
		case worker.wake <- struct{}{}:
		default:
		}
	}
}

// stopQueueAttempt stops the attempt of a run just marked cancelling, when this process runs it.
func (t *Tenant) stopQueueAttempt(id string) {
	if worker := t.queueWorker(); worker != nil {
		worker.stopAttempt(id)
	}
}
