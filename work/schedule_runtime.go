package work

import (
	"fmt"
	"sort"
	"sync/atomic"
	"time"

	queuestore "github.com/kitwork/engine/utilities/queue"
	schedulerule "github.com/kitwork/engine/utilities/schedule"
	"github.com/kitwork/engine/value"
)

// ── The app's clock: the due times of its schedules (KITWORK_SCHEDULE.md mục 5, 9) ─────────────────
//
// The schedules of an app run on a worker of kind schedule (queue_persist.go), the one the queue uses: the
// same kitwork_runs, lease, fencing, retry, timeout and backoff. What the clock adds is the step before the
// claim: at each due time it stores one run of the schedule (queuestore.Dispatch), keyed by the due time, so
// a due time is stored once whatever restarts, and moves the schedule's last due time handled
// (kitwork_schedules). It reads the time only when a due time may have come (dispatchAt): an idle app's tick
// reads its clock and nothing else.
//
// The rules of a due time the spec leaves open (mục 12), as the engine runs them today:
//   - every() counts from midnight of the schedule's zone and starts again at each midnight, as hourly(n):
//     every("30s") is due at :00 and :30, every("2h") at 00:00, 02:00… of the zone, every("7m") at 00:00,
//     00:07… 23:55; whole days fall on midnight (12.1, utilities/schedule rule.go). The day summer time
//     changes, every() counts the time that passes from midnight (12.2).
//   - due times missed while the app was down run once, the last of them, as soon as the app is back, its
//     context.job.at the time it was due (12.3). A schedule declared for the first time, or with another
//     rule, counts from when it was synced: nothing before it is missed.
//   - a due time that comes while a run of the schedule has not ended (pending, running) is passed over:
//     one run of a schedule at a time (12.4). A once() and now() are never passed over.
//   - a schedule without .timezone(…) counts in UTC, and boot says so for each: the app's zone,
//     router.timezone(…) of the app's config.work (KITWORK_ROUTER.md), is not read by the engine yet.

const scheduleResourceName = "schedule"

// scheduleDefaultLocation is the zone of a schedule that does not write .timezone(…).
var scheduleDefaultLocation = time.UTC

// scheduleClock is the clock the schedules read the time from (nil: the system's), set by SetScheduleClock.
var scheduleClock atomic.Pointer[func() time.Time]

// SetScheduleClock makes the app's schedules read the time from clock, nil for the system's, and returns
// what puts the clock it replaced back. Which due time has come is the schedules' clock's to say; a run's
// lease, its retry and its timeout stay on the system's. A test's way to reach a due time without waiting
// for it.
func SetScheduleClock(clock func() time.Time) (restore func()) {
	var next *func() time.Time
	if clock != nil {
		next = &clock
	}
	previous := scheduleClock.Swap(next)
	return func() { scheduleClock.Store(previous) }
}

// scheduleNow is the time the schedules read.
func scheduleNow() time.Time {
	if clock := scheduleClock.Load(); clock != nil {
		return (*clock)()
	}
	return time.Now()
}

// scheduleBook is the schedules of an app, as its .schedule/ declares them, with their due times: the
// worker of kind schedule holds it, guarded by the worker's mu.
type scheduleBook struct {
	entries []*scheduleEntry
	byName  map[string]*scheduleEntry
	// dispatchAt is when, on the schedules' clock, the next due time may have come: the clock is read
	// against the rules only then (the zero time: at once).
	dispatchAt time.Time
}

// scheduleEntry is one schedule the app's clock runs.
type scheduleEntry struct {
	name   string
	source string
	place  string
	rule   *schedulerule.Rule
	// location is the zone the rule counts in; state is the rule as kitwork_schedules keeps it (the rule as
	// written, with its zone).
	location *time.Location
	state    string
	// declaredAt is when the rule was first synced; onceAt the due time a once() chose; lastAt the last due
	// time handled. Read from kitwork_schedules at start, moved by the dispatch.
	declaredAt time.Time
	onceAt     time.Time
	lastAt     time.Time
}

// due is the due time to store at now: the last the rule had at or before now, when it is after the last
// handled (an earlier one missed is not stored: the last stands for them).
func (e *scheduleEntry) due(now time.Time) (time.Time, bool) {
	if e.rule.Once {
		if e.onceAt.IsZero() || e.onceAt.After(now) || !e.lastAt.Before(e.onceAt) {
			return time.Time{}, false
		}
		return e.onceAt, true
	}
	latest, ok := e.rule.Latest(now)
	if !ok || !latest.After(e.lastAt) {
		return time.Time{}, false
	}
	return latest, true
}

// next is the next due time after now, false when there is none (a once() that ran).
func (e *scheduleEntry) next(now time.Time) (time.Time, bool) {
	if e.rule.Once {
		if e.onceAt.IsZero() || !e.lastAt.Before(e.onceAt) {
			return time.Time{}, false
		}
		return e.onceAt, true
	}
	after := now
	if e.lastAt.After(after) {
		after = e.lastAt
	}
	return e.rule.Next(after)
}

func (t *Tenant) scheduleWorker() *queueRuntime {
	if t == nil || t.appRuntime == nil {
		return nil
	}
	current, _ := t.appRuntime.Resource(scheduleResourceName).(*queueRuntime)
	return current
}

func (t *Tenant) ensureScheduleWorker() (*queueRuntime, error) {
	if t == nil || t.appRuntime == nil {
		return nil, fmt.Errorf("app runtime is unavailable")
	}
	if current := t.scheduleWorker(); current != nil {
		return current, nil
	}
	candidate := &queueRuntime{owner: t, kind: scheduleKind, book: &scheduleBook{byName: map[string]*scheduleEntry{}},
		wake: make(chan struct{}, 1), parallel: queueDefaultParallel, keep: queueDefaultKeep, next: queueNever,
		reclaimAt: queueNever}
	resource, installed, err := t.appRuntime.InstallResource(scheduleResourceName, candidate)
	if err != nil {
		return nil, err
	}
	if !installed {
		current, ok := resource.(*queueRuntime)
		if !ok {
			return nil, fmt.Errorf("app schedule resource has unexpected type %T", resource)
		}
		return current, nil
	}
	return candidate, nil
}

// StartScheduleWorker writes the app's schedules into kitwork_definitions and kitwork_schedules and starts
// its clock. Called again (hot reload), it stops the running clock first.
func (t *Tenant) StartScheduleWorker() { t.startWorker(t.scheduleWorker()) }

// StopScheduleWorker halts the clock and waits for the runs running here to finish.
func (t *Tenant) StopScheduleWorker() { t.stopWorker(t.scheduleWorker()) }

// scheduleEntry is the schedule name the app's clock runs, nil when it runs none of that name.
func (q *queueRuntime) scheduleEntry(name string) *scheduleEntry {
	if q == nil || q.book == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.book.byName[name]
}

// listSchedules is what list() reads of each schedule: name, rule, mode, retry, timeout, zone, the next due
// time (null for a once() that ran) and its file.
func (q *queueRuntime) listSchedules(now time.Time) []map[string]value.Value {
	if q == nil || q.book == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	listed := make([]map[string]value.Value, 0, len(q.book.entries))
	for _, entry := range q.book.entries {
		timeout := value.Value{K: value.Nil}
		if entry.rule.TimeoutText != "" {
			timeout = value.New(entry.rule.TimeoutText)
		}
		next := value.Value{K: value.Nil}
		if due, ok := entry.next(now); ok {
			next = value.New(schedulerule.Stamp(due, entry.location))
		}
		listed = append(listed, map[string]value.Value{
			"name":     value.New(entry.name),
			"rule":     value.New(entry.rule.Text()),
			"mode":     value.New(entry.rule.Mode()),
			"retry":    value.New(entry.rule.Retry),
			"timeout":  timeout,
			"timezone": value.New(entry.location.String()),
			"next":     next,
			"source":   value.New(entry.source),
		})
	}
	return listed
}

// noteDispatch brings the clock's next look at the rules forward to at.
func (q *queueRuntime) noteDispatch(at time.Time) {
	q.mu.Lock()
	if q.book != nil && at.Before(q.book.dispatchAt) {
		q.book.dispatchAt = at
	}
	q.mu.Unlock()
}

// wakeWorker tells worker a run is due at at: it claims then, at once when it is due now.
func wakeWorker(worker *queueRuntime, at time.Time) {
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

// syncSchedules makes kitwork_schedules what the book declares, and the book what kitwork_schedules kept:
// a schedule with the rule it was synced with keeps its last due time handled and the due time its once()
// chose; one declared for the first time, or with another rule, counts from now. The state of a schedule no
// file declares any more goes, unless its file did not load (withheld). Runs under worker.mu, before the
// poller starts.
func (t *Tenant) syncSchedules(worker *queueRuntime, store *queuestore.Store) error {
	states, err := store.ScheduleStates()
	if err != nil {
		return err
	}
	now := scheduleNow()
	var put []queuestore.ScheduleState
	declared := map[string]bool{}
	for _, entry := range worker.book.entries {
		declared[entry.name] = true
		state, kept := states[entry.name]
		if !kept || state.Rule != entry.state {
			state = queuestore.ScheduleState{Name: entry.name, Rule: entry.state, DeclaredAt: now, LastAt: now}
			if entry.rule.Once {
				state.OnceAt, _ = entry.rule.Next(now)
			}
			put = append(put, state)
		}
		entry.declaredAt, entry.onceAt, entry.lastAt = state.DeclaredAt, state.OnceAt, state.LastAt
	}
	var remove []string
	for name := range states {
		if !declared[name] && !worker.withheld[name] && !worker.unloaded.Names[name] {
			remove = append(remove, name)
		}
	}
	sort.Strings(remove)
	if err := store.SyncSchedules(put, remove, now); err != nil {
		return err
	}
	// the first look is now: a due time missed while the app was down is stored at once
	worker.book.dispatchAt = time.Time{}
	return nil
}

// dispatchSchedules stores the run of every due time that has come on the schedules' clock, and notes when
// the next one comes. It runs on the poller's goroutine, before the claim.
func (t *Tenant) dispatchSchedules(worker *queueRuntime, store *queuestore.Store) {
	now := scheduleNow()
	worker.mu.Lock()
	book := worker.book
	if now.Before(book.dispatchAt) {
		worker.mu.Unlock()
		return
	}
	book.dispatchAt = queueNever
	entries := append([]*scheduleEntry(nil), book.entries...)
	worker.mu.Unlock()

	next := queueNever
	stored := false
	for _, entry := range entries {
		worker.mu.Lock()
		due, isDue := entry.due(now)
		worker.mu.Unlock()
		if isDue {
			at := schedulerule.Stamp(due, entry.location)
			_, outcome, err := store.Dispatch(due, queuestore.NewRun{
				Name: entry.name, Data: scheduleRunData(at, triggerSchedule), Key: queuestore.Stamp(due),
				MaxAttempts: entry.rule.Retry + 1, Timeout: entry.rule.Timeout, Available: time.Now(),
			}, !entry.rule.Once, time.Now())
			if err != nil {
				// the due time stays due: looked at again after a poll
				fmt.Printf("[Schedule] %s, tới giờ %s: không lưu được lần chạy: %v\n", entry.name, at, err)
				if retry := now.Add(queuePollInterval); retry.Before(next) {
					next = retry
				}
				continue
			}
			worker.mu.Lock()
			entry.lastAt = due
			worker.mu.Unlock()
			stored = stored || outcome == queuestore.Dispatched
		}
		worker.mu.Lock()
		upcoming, ok := entry.next(now)
		worker.mu.Unlock()
		if ok && upcoming.Before(next) {
			next = upcoming
		}
	}
	worker.noteDispatch(next)
	if stored {
		worker.noteDue(time.Now())
	}
}
