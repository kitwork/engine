package work

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kitwork/engine/database"
	queuestore "github.com/kitwork/engine/utilities/queue"
	"github.com/kitwork/engine/value"
)

// ── Queue worker: claim, run, record ─────────────────────────────────────────────────────────────
//
// ONE poller goroutine per app drives every queue it owns:
//
//	sync     files → `queues` (a projection of _queue/*.kitwork.js; content_hash skips no-op writes)
//	poll     every 250ms: claim what this node has capacity for and run each in a pooled VM
//	settle   record completed / retry-with-backoff / dead-letter, plus output, gas and error
//
// The poller never runs JavaScript itself; it only coordinates. Handlers run in their own VMs, under
// the same energy ceiling as a request, so a runaway job stops at its budget like everything else.

// queueLeaseTTL bounds how long a claimed message stays leased to a node without a heartbeat before
// another node may reclaim it. A var so tests can shrink it. Energy is the real per-run fence; this
// only governs crash recovery.
var queueLeaseTTL = 30 * time.Second

// queuePollInterval is how often a node looks for work. Faster than cron's one second because a
// queue's latency is user-visible in a way a schedule's is not — a dispatch is something that just
// happened, not something booked for 08:00.
var queuePollInterval = 250 * time.Millisecond

// queueBackoff gates a retry: 2^attempt × 5s, capped at 30 minutes. A var so tests can shorten it.
var queueBackoff = func(attempt int) time.Duration {
	if attempt > 8 {
		attempt = 8
	}
	d := time.Duration(1<<uint(attempt)) * 5 * time.Second
	if d > 30*time.Minute {
		d = 30 * time.Minute
	}
	return d
}

// queueSuccessRetention is how long a COMPLETED message's row is kept. Successes are transient — the
// durable record is the counter on `queues`. Failures are kept for the handler's full .retention()
// window, because a dead letter you cannot read is not a dead letter.
var queueSuccessRetention = 1 * time.Hour

// StartQueueWorker launches this app's worker. Every registered handler is durable: its definition
// is synced into `queues` and its messages are dispatched through the database.
func (t *Tenant) StartQueueWorker() {
	worker := t.queueWorker()
	if worker == nil {
		return
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()

	stopQueueWorkerNoLock(worker)

	if len(worker.handlers) == 0 || worker.closed {
		return
	}
	if err := t.startQueueRuntime(worker); err != nil {
		fmt.Printf("[Queue] worker disabled: %v\n", err)
	}
}

// StopQueueWorker halts polling and waits for accepted messages to finish.
func (t *Tenant) StopQueueWorker() {
	worker := t.queueWorker()
	if worker == nil {
		return
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	stopQueueWorkerNoLock(worker)
}

// stopQueueWorkerNoLock cancels the poller and heartbeat. Caller holds worker.mu; the wait for
// in-flight runs happens OUTSIDE the lock, because a finishing run needs that same lock to
// decrement its slot and would otherwise wait for the waiter.
func stopQueueWorkerNoLock(worker *queueRuntime) {
	for _, cancel := range worker.cancels {
		close(cancel)
	}
	worker.cancels = nil
}

// startQueueRuntime picks the backend (shared Postgres when a system database is connected, else the
// app's own SQLite), migrates it, syncs definitions, reclaims orphans and starts the goroutines.
// Runs under worker.mu, held by the caller, so it must not re-lock.
func (t *Tenant) startQueueRuntime(worker *queueRuntime) error {
	appID := t.appID()

	// No flag: a connected system database is the switch, exactly as it is for the scheduler. It asks
	// which DIALECT rather than merely whether one is connected — a system database configured as
	// SQLite would otherwise be handed the Postgres store, whose `$1` placeholders and
	// `FOR UPDATE SKIP LOCKED` it cannot parse.
	var store queuestore.Store
	if database.SystemIsPostgres() {
		worker.db = database.System
		store = queuestore.NewPgStore(database.System, t.queueNodeID())
	} else if database.System != nil {
		worker.db = database.System
		store = queuestore.NewSqliteStore(database.System, t.queueNodeID())
	} else {
		db := appSqliteFor(t, "queue.db").db() // apps/<identity>/.data/queue.db — one per app
		if db == nil {
			return fmt.Errorf("queue.db connection unavailable")
		}
		worker.db = db
		store = queuestore.NewSqliteStore(db, t.queueNodeID())
	}
	worker.store = store

	if err := store.InitSchema(); err != nil {
		return err
	}

	records := make([]queuestore.QueueRecord, 0, len(worker.handlers))
	worker.byName = make(map[string]*QueueHandler, len(worker.handlers))
	for _, handler := range worker.handlers {
		normalizeQueueDefaults(handler)
		worker.byName[handler.Name] = handler
		records = append(records, queuestore.QueueRecord{
			Name:          handler.Name,
			MaxAttempts:   handler.MaxAttempts,
			RetentionDays: handler.RetentionDays,
			ContentHash:   handler.ContentHash,
		})
	}
	if len(records) == 0 {
		return nil
	}

	if err := store.Sync(appID, records); err != nil {
		fmt.Printf("[Queue] sync: %v\n", err)
	}
	store.Reclaim(appID, true) // boot: SQLite frees all of this app's 'running'; Postgres only expired leases

	worker.inflight = make(map[string]int, len(records))

	poll := make(chan struct{})
	worker.cancels = append(worker.cancels, poll)
	worker.wg.Add(1)
	go func() {
		defer worker.wg.Done()
		t.queuePoller(worker, poll, appID)
	}()

	beat := make(chan struct{})
	worker.cancels = append(worker.cancels, beat)
	worker.wg.Add(1)
	go func() {
		defer worker.wg.Done()
		t.queueHeartbeat(worker, beat)
	}()

	fmt.Printf("[Queue] worker up (%s, node=%s) — %d queue(s) for %q\n",
		store.Label(), t.queueNodeID(), len(records), appID)
	return nil
}

func normalizeQueueDefaults(handler *QueueHandler) {
	if handler.MaxAttempts < 1 {
		handler.MaxAttempts = 1
	}
	if handler.Parallel < 1 {
		handler.Parallel = 1
	}
	if handler.RetentionDays < 1 {
		handler.RetentionDays = 30
	}
}

func (t *Tenant) queuePoller(worker *queueRuntime, cancel chan struct{}, appID string) {
	ticker := time.NewTicker(queuePollInterval)
	defer ticker.Stop()
	tick := 0
	for {
		select {
		case <-cancel:
			return
		case <-ticker.C:
			t.claimAndRunQueue(worker, appID)
			worker.store.Reclaim(appID, false)
			tick++
			if tick%240 == 0 { // ~once a minute at the default interval
				t.queueRetentionSweep(worker)
			}
		}
	}
}

// queueHeartbeat pushes this node's leases forward while it lives, so the shared store can tell a
// live-but-busy node from a dead one. A host goroutine — the VM is occupied during a slow job.
func (t *Tenant) queueHeartbeat(worker *queueRuntime, cancel chan struct{}) {
	interval := queueLeaseTTL / 3
	if interval < 250*time.Millisecond {
		interval = 250 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-cancel:
			return
		case <-ticker.C:
			worker.store.Heartbeat(t.queueNodeID(), queueLeaseTTL)
		}
	}
}

// claimAndRunQueue takes only as much as this node has room for, then runs each claim.
//
// Capacity is computed BEFORE claiming so a node does not hold messages it cannot start — a claimed
// message is invisible to every other node until its lease expires, so over-claiming is how a queue
// appears stalled while another node sits idle.
func (t *Tenant) claimAndRunQueue(worker *queueRuntime, appID string) {
	capacity := worker.capacity()
	if capacity < 1 {
		return
	}
	if capacity > 20 {
		capacity = 20
	}

	for _, claimed := range worker.store.Claim(appID, t.queueNodeID(), queueLeaseTTL, capacity) {
		handler := worker.handlerFor(claimed.Name)
		if handler == nil {
			// This node has no code for that queue. Dead-letter it rather than loop: a message no
			// node can run would otherwise be claimed and released forever, and the failure would
			// look like silence instead of an error someone can read.
			worker.store.Fail(claimed.ID, claimed.Attempt, false, time.Time{},
				"no registered handler for queue "+claimed.Name, "", 0)
			worker.store.RecordSummary(appID, claimed.Name, "failed")
			continue
		}
		// Capacity was checked in aggregate; this reserves the specific queue's slot. Losing the race
		// here means putting the message straight back, WITHOUT consuming an attempt — the handler
		// never ran, so charging it a try would dead-letter work that was merely busy.
		if !worker.reserve(handler) {
			worker.store.Fail(claimed.ID, claimed.Attempt-1, true, time.Now(), "", "", 0)
			continue
		}
		if !worker.startRun() {
			worker.release(handler)
			worker.store.Fail(claimed.ID, claimed.Attempt-1, true, time.Now(), "", "", 0)
			continue
		}
		go func(job queuestore.ClaimedJob, h *QueueHandler) {
			defer worker.wg.Done()
			defer worker.release(h)
			t.runQueueJob(worker, job, h)
		}(claimed, handler)
	}
}

// runQueueJob executes one message and records the outcome. Attempt counts tries INCLUDING this one
// (the claim increments it), so this is the final try when attempt >= max_attempts.
func (t *Tenant) runQueueJob(worker *queueRuntime, claimed queuestore.ClaimedJob, handler *QueueHandler) {
	var out strings.Builder
	final := claimed.Attempt >= claimed.MaxAttempts
	job := t.queueContext(claimed, final, &out)

	gas, runErr := t.runInQueueVM(handler, handler.Callback, []value.Value{job})

	appID := t.appID()
	if runErr == nil {
		worker.store.Complete(claimed.ID, out.String(), int64(gas))
		worker.store.RecordSummary(appID, claimed.Name, "completed")
		if handler.OnSuccess != nil {
			_, _ = t.runInQueueVM(handler, handler.OnSuccess, []value.Value{job})
		}
		return
	}

	retry := claimed.Attempt < claimed.MaxAttempts
	worker.store.Fail(claimed.ID, claimed.Attempt, retry,
		time.Now().Add(queueBackoff(claimed.Attempt)), runErr.Error(), out.String(), int64(gas))
	if !retry {
		worker.store.RecordSummary(appID, claimed.Name, "failed")
	}
	if handler.OnError != nil {
		errObj := value.New(map[string]value.Value{"message": value.New(runErr.Error())})
		_, _ = t.runInQueueVM(handler, handler.OnError, []value.Value{job, errObj})
	}
}

// queueContext is the `job` a handler receives: the payload it was dispatched with, where this try
// sits in the budget, and a log sink whose output is stored as this message's history.
func (t *Tenant) queueContext(claimed queuestore.ClaimedJob, final bool, out *strings.Builder) value.Value {
	logFn := value.NewFunc(func(args ...value.Value) value.Value {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = a.Text()
		}
		out.WriteString(strings.Join(parts, " "))
		out.WriteByte('\n')
		return value.Value{K: value.Nil}
	})

	// The payload round-trips through JSON because that is how it was stored. A payload that cannot
	// be decoded becomes an empty object rather than a hard failure: the message still deserves to
	// reach its handler, which can then report a shape it does not recognise.
	var decoded any
	if claimed.Payload != "" {
		if err := json.Unmarshal([]byte(claimed.Payload), &decoded); err != nil {
			decoded = map[string]any{}
		}
	}

	return value.New(map[string]value.Value{
		"payload":        collectionValue(decoded),
		"log":            logFn,
		"name":           value.New(claimed.Name),
		"id":             value.New(int(claimed.ID)),
		"attempt":        value.New(claimed.Attempt),
		"maxAttempts":    value.New(claimed.MaxAttempts),
		"isFinalAttempt": value.New(final),
	})
}

// runInQueueVM runs a lambda belonging to a queue file's bytecode: a pooled VM is FastReset onto THAT
// bytecode (the lambda's Address offsets index into it) with the tenant's builtins and globals.
func (t *Tenant) runInQueueVM(handler *QueueHandler, lambda *value.Lambda, args []value.Value) (gas uint64, runErr error) {
	if handler.Bytecode == nil {
		return 0, fmt.Errorf("queue %q has no bytecode", handler.Name)
	}
	if lambda == nil {
		return 0, fmt.Errorf("queue %q has no handler", handler.Name)
	}
	return t.Execute(handler.Bytecode.Program, lambda, args)
}

// queueRetentionSweep prunes finished messages — successes fast, failures for the handler's window.
func (t *Tenant) queueRetentionSweep(worker *queueRuntime) {
	appID := t.appID()
	now := time.Now()
	completedBefore := now.Add(-queueSuccessRetention)

	worker.mu.Lock()
	handlers := make([]*QueueHandler, len(worker.handlers))
	copy(handlers, worker.handlers)
	worker.mu.Unlock()

	for _, handler := range handlers {
		days := handler.RetentionDays
		if days < 1 {
			days = 30
		}
		worker.store.Retention(appID, handler.Name, completedBefore, now.AddDate(0, 0, -days))
	}
}
