package work

import (
	"fmt"
	"strings"
	"time"

	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/id"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// ── Scheduler: durable, database-backed cron ─────────────────────────────────────────────────────────
//
// EVERY file-cron is durable — its definition lives in the `crons` table and it is dispatched through
// the database, no opt-in. ONE dispatcher goroutine per tenant drives them all:
//
//   sync   files → `crons` (a projection of _cron/*.kitwork.js; content_hash skips no-op writes)
//   tick   every 1s: for each due job create a slot (row) in `cron_runs`
//          — the UNIQUE(schedule_id, scheduled_for) constraint is the idempotency arbiter: a restart
//            inside the same slot re-computes the same slot and the duplicate INSERT is dropped, so a
//            job fires AT MOST once per slot across crashes/restarts/nodes.
//   claim  mark a pending slot running (lease), run the handler in a pooled VM with a real ctx, then
//          record status/output/gas/error → history. Retry (max_attempts + backoff) is opt-in.
//
// Storage is behind cronStore (cron_store.go): per-tenant SQLite (.data/scheduler.db) by default, or a
// shared Postgres for multi-node clusters (KITWORK_SCHEDULER=shared). The authoring model is identical
// either way. On single-node SQLite the Go clock is the only clock; Postgres uses NOW().

// cronLeaseTTL bounds how long a claimed slot stays leased to a node without a heartbeat before another
// node may reclaim it. A var so the multi-node demo can shrink it. MaxEnergy (gas) is the real per-run
// fence; this only governs crash recovery.
var cronLeaseTTL = 30 * time.Second

// cronNodeID identifies THIS process as a lease_owner in the shared store — a per-process kitid
// (id.Entity(): time-ordered + random, globally unique). A tenant may override it via t.cronNode (the
// multi-node demo does this to run two "nodes" inside one process).
var cronNodeID = id.Entity()

// appID is the scheduler partition key: the tenant's IDENTITY (the `identity` column of entity) — the
// "app". Every domain of an app shares one _cron set and one partition, so crons are keyed by identity,
// not by domain. Single-tenant (flat/sites) layouts have no identity, so they fall back to the domain.
func (t *Tenant) appID() string {
	if t.entity.Identity != "" {
		return t.entity.Identity
	}
	return t.entity.Domain
}

// nodeID is this tenant instance's lease owner (defaults to the process node id).
func (t *Tenant) nodeID() string {
	if t.cronNode != "" {
		return t.cronNode
	}
	return cronNodeID
}

// startPersistedScheduler picks the store backend (shared Postgres if SchedulerShared + database.System,
// else per-tenant SQLite), migrates it, syncs the persisted jobs, reclaims orphaned slots, and launches
// the dispatcher + heartbeat goroutines. Called from StartCronJobs when the tenant has any cron. Runs
// under t.cronMu (held by the caller), so it must not re-lock it.
func (t *Tenant) startPersistedScheduler() error {
	appID := t.appID()

	// Default to the shared Postgres store whenever a system DB is connected — cron state belongs in one
	// central place. Fall back to a per-tenant SQLite file only when there is NO system DB (pure local
	// dev / single binary). No flag: the presence of database.System is the switch.
	var store cronStore
	if database.System != nil {
		t.cronDB = database.System
		store = &pgStore{db: database.System, node: t.nodeID()}
	} else {
		db := appSqliteFor(t, "scheduler.db").db() // apps/<identity>/.data/scheduler.db — one per app
		if db == nil {
			return fmt.Errorf("scheduler.db connection unavailable")
		}
		t.cronDB = db
		store = &sqliteStore{db: db, node: t.nodeID()}
	}
	t.cronStore = store

	if err := store.initSchema(); err != nil {
		return err
	}

	// Snapshot every cron + build the name→code map the worker uses to find a claimed slot's handler.
	// Within an app (identity) a cron's name is its file, unique — so name is the key.
	persisted := make([]*CronJob, 0, len(t.crons))
	t.cronByName = make(map[string]*CronJob)
	for _, job := range t.crons {
		normalizeCronDefaults(job)
		persisted = append(persisted, job)
		t.cronByName[job.Name] = job
	}
	if len(persisted) == 0 {
		return nil
	}

	if err := store.sync(appID, persisted); err != nil {
		fmt.Printf("[Cron] sync: %v\n", err)
	}
	store.reclaim(appID, true) // boot: SQLite wipes all this app's 'running'; PG frees only expired leases

	cancel := make(chan struct{})
	t.cronCancels = append(t.cronCancels, cancel)
	go t.persistDispatcher(cancel, appID, persisted)

	// Heartbeat in a host goroutine (never the VM): keep this node's leases alive while long jobs run.
	// No-op for SQLite (single node); the lifeline that stops premature reclaim on Postgres.
	hb := make(chan struct{})
	t.cronCancels = append(t.cronCancels, hb)
	go t.persistHeartbeat(hb)

	fmt.Printf("[Cron] persisted scheduler up (%s, node=%s) — %d job(s) for %q\n",
		store.label(), t.nodeID(), len(persisted), appID)
	return nil
}

// normalizeCronDefaults fills the durable defaults the spec specifies (max_attempts 1 = no auto-retry).
func normalizeCronDefaults(job *CronJob) {
	if job.MaxAttempts < 1 {
		job.MaxAttempts = 1
	}
	if job.OverlapPolicy == "" {
		job.OverlapPolicy = "skip"
	}
	if job.Timezone == "" {
		job.Timezone = "UTC"
	}
	if job.RetentionDays < 1 {
		job.RetentionDays = 30
	}
}

func (t *Tenant) persistDispatcher(cancel chan struct{}, appID string, persisted []*CronJob) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	tick := 0
	lastSlot := make(map[string]time.Time) // scheduleID → last slot dispatched, to skip redundant ticks
	for {
		select {
		case <-cancel:
			return
		case <-ticker.C:
			now := time.Now().UTC()
			t.dispatchDue(now, appID, persisted, lastSlot)
			t.claimAndRun(appID)
			t.cronStore.reclaim(appID, false) // PG: free expired leases (a crashed node's work). SQLite: no-op.
			tick++
			if tick%60 == 0 { // ~once a minute
				t.retentionSweep(persisted)
			}
		}
	}
}

// persistHeartbeat pushes this node's leases forward every ttl/3 while it lives, so the shared store can
// tell a live-but-busy node from a dead one. Host goroutine — the VM is blocked during a slow job.
func (t *Tenant) persistHeartbeat(cancel chan struct{}) {
	interval := cronLeaseTTL / 3
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
			t.cronStore.heartbeat(t.nodeID(), cronLeaseTTL)
		}
	}
}

// dispatchDue creates a pending slot for every job that is due this instant. `lastSlot` collapses the
// ~N ticks that land in one window down to a single INSERT (so rowids stay tidy and writes don't churn);
// the UNIQUE(identity, name, scheduled_for) constraint remains the real idempotency backstop, so a
// restart (which starts with an empty lastSlot) still cannot duplicate a slot already recorded.
func (t *Tenant) dispatchDue(now time.Time, appID string, persisted []*CronJob, lastSlot map[string]time.Time) {
	for _, job := range persisted {
		slot, due := jobSlot(job, now)
		if !due {
			continue
		}
		if prev, ok := lastSlot[job.Name]; ok && prev.Equal(slot) {
			continue // already handled this window on an earlier tick
		}
		// Overlap 'skip': don't open a new slot while a previous run for this job is still active.
		if job.OverlapPolicy == "skip" && t.cronStore.hasActive(appID, job.Name) {
			continue
		}
		if err := t.cronStore.insertSlot(appID, job.Name, slot, job.MaxAttempts); err != nil {
			fmt.Printf("[Cron] dispatch %s: %v\n", job.Name, err)
			continue // leave lastSlot unset so the next tick retries this window
		}
		lastSlot[job.Name] = slot
	}
}

// claimedRow is a pending slot materialized OUT of its result set before any UPDATE runs — SQLite locks
// the file while a query's rows are open, so an in-flight UPDATE would otherwise deadlock.
type claimedRow struct {
	id           int64  // cron_runs.id — the RUN's own id (execID)
	name         string // which cron (unique within the app/identity)
	scheduledFor string
	attempt      int
	maxAttempts  int
}

func (t *Tenant) claimAndRun(appID string) {
	for _, r := range t.cronStore.claim(appID, t.nodeID(), cronLeaseTTL, 20) {
		job := t.cronByName[r.name]
		if job == nil {
			// Code for this cron is not loaded on this node — close the slot so it does not loop.
			t.cronStore.fail(r.id, r.attempt+1, false, time.Time{}, "no registered handler for "+r.name, "", 0)
			continue
		}
		go t.runClaimed(r, job)
	}
}

// runClaimed executes one slot's handler and records the outcome. attempt counts PRIOR failures, so the
// current try is attempt+1 and this is the final try when attempt+1 >= max_attempts.
func (t *Tenant) runClaimed(r claimedRow, job *CronJob) {
	var out strings.Builder
	tryNum := r.attempt + 1
	final := tryNum >= r.maxAttempts
	ctx := t.cronContext(r.id, tryNum, r.maxAttempts, final, r.scheduledFor, &out)

	gas, runErr := t.runInJobVM(job, job.Callback, []value.Value{ctx})

	appID := t.appID()
	if runErr == nil {
		t.cronStore.complete(r.id, out.String(), int64(gas))
		t.cronStore.recordSummary(appID, r.name, "completed") // roll into the crons summary
		if job.OnSuccess != nil {
			_, _ = t.runInJobVM(job, job.OnSuccess, []value.Value{ctx})
		}
		return
	}

	// Failed. Retry (return to pending + backoff) only while more attempts remain; else mark failed.
	// available_at = now + 2^attempt × 10s. Reclaim is separate: it re-runs a slot that never finished.
	newAttempt := r.attempt + 1
	retry := newAttempt < r.maxAttempts
	t.cronStore.fail(r.id, newAttempt, retry, time.Now().Add(cronBackoff(newAttempt)),
		runErr.Error(), out.String(), int64(gas))
	if !retry {
		t.cronStore.recordSummary(appID, r.name, "failed") // terminal failure — summarise once
	}
	if job.OnError != nil {
		errObj := value.New(map[string]value.Value{"message": value.New(runErr.Error())})
		_, _ = t.runInJobVM(job, job.OnError, []value.Value{ctx, errObj})
	}
}

// cronContext is the `ctx` a persisted handler receives: what differs run-to-run, plus a log sink whose
// output is stored as this execution's history. Ordinary handlers ignore it (an unused arg is harmless).
func (t *Tenant) cronContext(execID int64, attempt, maxAttempts int, final bool, scheduledFor string, out *strings.Builder) value.Value {
	logFn := value.NewFunc(func(args ...value.Value) value.Value {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = a.Text()
		}
		out.WriteString(strings.Join(parts, " "))
		out.WriteByte('\n')
		return value.Value{K: value.Nil}
	})
	return value.New(map[string]value.Value{
		"log":            logFn,
		"attempt":        value.New(attempt),
		"maxAttempts":    value.New(maxAttempts),
		"isFinalAttempt": value.New(final),
		"scheduledFor":   value.New(scheduledFor),
		"executionId":    value.New(int(execID)),
	})
}

// runInJobVM runs a lambda that belongs to a cron file's bytecode: a pooled VM is FastReset onto THAT
// bytecode (the lambda's Address offsets index into it) and given the tenant's Builtins/Globals/Vars.
// Returns gas consumed and any run error (an Invalid result — thrown value or energy-limit halt).
func (t *Tenant) runInJobVM(job *CronJob, lambda *value.Lambda, args []value.Value) (gas uint64, runErr error) {
	bc := job.Bytecode
	if bc == nil {
		return 0, fmt.Errorf("cron %q has no bytecode", job.Name)
	}
	vmi := vmPool.Get()
	vm, ok := vmi.(*runtime.VM)
	if !ok {
		if vmi != nil {
			vmPool.Put(vmi)
		}
		return 0, fmt.Errorf("vm pool unavailable")
	}
	defer func() {
		if r := recover(); r != nil {
			runErr = fmt.Errorf("panic: %v", r)
		}
		vmPool.Put(vm)
	}()

	vm.Builtins = t.vm.Builtins
	vm.FastReset(bc.Instructions, bc.Constants, t.vm.Globals, bc.SourceMap)
	vm.MaxEnergy = t.MaxEnergy
	for k, v := range t.vm.Vars {
		vm.Vars[k] = v
	}

	res := vm.ExecuteLambda(lambda, args)
	gas = vm.Energy
	if res.K == value.Invalid {
		runErr = fmt.Errorf("%v", res.V)
	}
	return gas, runErr
}

// cronSuccessRetention is how long a SUCCESSFUL run's row is kept in cron_runs. Successes are transient
// — their coordination job ends once the slot's window passes; the durable record is the compact
// summary on `crons`. Failures are kept for the job's full .retention() window (for debugging). A var so
// tests can shorten it. Kept well above any minute-granularity slot window so idempotency holds.
var cronSuccessRetention = 1 * time.Hour

// retentionSweep prunes cron_runs — successes fast (cronSuccessRetention), failures for their .retention()
// window. The per-cron summary on `crons` is what persists; the run log stays small.
func (t *Tenant) retentionSweep(persisted []*CronJob) {
	appID := t.appID()
	now := time.Now()
	completedBefore := now.Add(-cronSuccessRetention)
	for _, job := range persisted {
		days := job.RetentionDays
		if days < 1 {
			days = 30
		}
		t.cronStore.retention(appID, job.Name, completedBefore, now.AddDate(0, 0, -days))
	}
}

// jobSlot returns the stable slot timestamp for `now` and whether the job is due. The slot is truncated
// so many dispatcher ticks inside one window collapse to one INSERT OR IGNORE. Cron expressions match at
// minute granularity (the spec's persisted granularity); intervals bucket by their period.
func jobSlot(job *CronJob, now time.Time) (time.Time, bool) {
	expr := job.Expression
	switch {
	case expr == "@hourly":
		return now.Truncate(time.Hour), true
	case expr == "@daily":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), true
	case expr == "@weekly":
		return now.Truncate(time.Hour), true // coarse fallback; .weekly("mon 09:00") uses a real cron expr
	case expr == "@monthly":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC), true
	case strings.HasPrefix(expr, "@every "):
		d, err := ParseDuration(strings.TrimPrefix(expr, "@every "))
		if err != nil || d <= 0 {
			return time.Time{}, false
		}
		if d < time.Second {
			d = time.Second // persisted granularity floor — sub-second intervals are an anti-pattern
		}
		return now.Truncate(d), true
	default:
		// Standard cron expression. Truncate to the minute so the "0" seconds field matches and the slot
		// is one-per-minute; matchCronExpression then checks minute/hour/dom/month/dow.
		minute := now.Truncate(time.Minute)
		if matchCronExpression(expr, minute) {
			return minute, true
		}
		return time.Time{}, false
	}
}

// cronBackoff is the retry gate: 2^attempt × 10s, capped at 1h. A package var so tests can shorten it.
var cronBackoff = func(attempt int) time.Duration {
	if attempt > 8 {
		attempt = 8
	}
	d := time.Duration(1<<uint(attempt)) * 10 * time.Second
	if d > time.Hour {
		d = time.Hour
	}
	return d
}
