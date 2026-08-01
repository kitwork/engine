package work

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/runtime"
	queuestore "github.com/kitwork/engine/utilities/queue"
	"github.com/kitwork/engine/value"
)

// ── The durable queue ────────────────────────────────────────────────────────────────────────────
//
// A queue completes the pair the scheduler started. Cron answers "run this WHEN"; the queue answers
// "run this LATER, because something happened". Both are background work that must survive a
// restart, so both are stored and dispatched through a database — and neither needs Redis, a broker,
// or a second process to be true.
//
// The authoring model is the scheduler's, deliberately: ONE FILE = ONE HANDLER, named by its file.
//
//	apps/<identity>/_queue/send-welcome.kitwork.js   →  the queue "send-welcome"
//
//	import { queue } from "kitwork";
//	queue
//	  .retry(3)
//	  .handle((job) => { mail.send(job.payload.email); })
//	  .error((job, err) => job.log("giving up: " + err.message));
//
// and from anywhere in the app — a route, a cron, another job:
//
//	import { queue } from "kitwork";
//	queue.dispatch("send-welcome", { email: "a@b.com" });
//	queue.dispatch("send-welcome", { email: "a@b.com" }, { key: "welcome:42", delay: "5m" });
//
// `key` makes a dispatch idempotent: the UNIQUE(identity, name, dedupe_key) constraint drops the
// repeat, so a retried request cannot send two welcome mails. Without a key every dispatch is a new
// message, which is what an unkeyed dispatch means.

// QueueHandler is a registered worker. Like a cron, its Callback's Address offsets index into
// Bytecode — each _queue/*.kitwork.js compiles to its OWN bytecode, and the runner FastResets onto
// that bytecode before invoking the lambda.
type QueueHandler struct {
	Name        string
	Callback    *value.Lambda
	Bytecode    *compiler.Bytecode
	MaxAttempts int // total attempt budget, default 1 — retry only when its author asked for it
	// Parallel bounds how many messages of THIS queue one node runs at a time.
	Parallel      int
	RetentionDays int
	ContentHash   string
	OnSuccess     *value.Lambda
	OnError       *value.Lambda
}

// Queue is the tenant's queue namespace: `import { queue } from "kitwork"`. Same shape as router and
// cron — a noun you call methods on, reached via kitwork().queue.
type Queue struct {
	tenant *Tenant
}

func (w *KitWork) Queue() *Queue {
	return &Queue{tenant: w.tenant}
}

// ── authoring side (inside _queue/<name>.kitwork.js) ─────────────────────────────────────────────

func (q *Queue) Handle(args ...value.Value) *QueueBuilder {
	return newQueueBuilder(q.tenant).Handle(args...)
}
func (q *Queue) Retry(args ...value.Value) *QueueBuilder {
	return newQueueBuilder(q.tenant).Retry(args...)
}
func (q *Queue) Parallel(args ...value.Value) *QueueBuilder {
	return newQueueBuilder(q.tenant).Parallel(args...)
}
func (q *Queue) Keep(args ...value.Value) *QueueBuilder {
	return newQueueBuilder(q.tenant).Keep(args...)
}

// QueueBuilder holds ONE handler from the start; modifiers mutate it in place on either side of
// .handle(), which attaches the callback and registers it. Same construction as CronBuilder — the
// two read alike because they are the same idea pointed at a different trigger.
type QueueBuilder struct {
	tenant     *Tenant
	handler    *QueueHandler
	registered bool
}

func newQueueBuilder(t *Tenant) *QueueBuilder {
	return &QueueBuilder{tenant: t, handler: &QueueHandler{}}
}

func (qb *QueueBuilder) Handle(args ...value.Value) *QueueBuilder {
	if qb.registered {
		return qb
	}
	for _, a := range args {
		if !a.IsCallable() {
			continue
		}
		qb.handler.Callback = lambdaOf(a)
		qb.tenant.registerQueue(qb.handler)
		qb.registered = true
		break
	}
	return qb
}

// Retry sets the ATTEMPT BUDGET: how many times a message is tried in total before it is
// dead-lettered — `.retry(3)` means three runs, not one run plus three more. The number is the same
// one the runtime reports as `job.maxAttempts` and stores in `max_attempts`, so what an author
// writes and what they later read back are one number.
//
// Default 1: a handler that fails re-runs every side effect it already performed, so automatic retry
// is something the author opts into after making the handler idempotent, never a default kindness.
func (qb *QueueBuilder) Retry(args ...value.Value) *QueueBuilder {
	qb.handler.MaxAttempts = 1
	if len(args) > 0 && !args[0].IsCallable() {
		if n, err := strconv.Atoi(strings.TrimSpace(args[0].Text())); err == nil && n >= 1 {
			qb.handler.MaxAttempts = n
		}
	}
	return qb
}

// Parallel bounds how many messages of this queue one node runs at once (default 1). It is a
// per-node bound, not a global one — a global limit would need a lock every node agrees on, and the
// point of this design is that nodes coordinate through claims rather than through locks.
//
// Not `limit`: a route's `.limit({ rate, second })` already means rate limiting, and one word for
// two kinds of ceiling is how a reader ends up guessing.
func (qb *QueueBuilder) Parallel(args ...value.Value) *QueueBuilder {
	if len(args) > 0 && !args[0].IsCallable() {
		if n, err := strconv.Atoi(strings.TrimSpace(args[0].Text())); err == nil && n >= 1 {
			qb.handler.Parallel = n
		}
	}
	return qb
}

// Keep bounds how long finished messages are kept (default 30 days). Durability is NOT the thing
// being configured here — every dispatch is stored either way; this only prunes history.
//
// The store, the column and the sweep still say "retention", which is the policy this sets. `keep`
// is the word an author writes: it reads as an instruction next to a duration ("keep 30 days"),
// where `retention` reads as a section heading.
func (qb *QueueBuilder) Keep(args ...value.Value) *QueueBuilder {
	if len(args) > 0 && !args[0].IsCallable() {
		if d, err := ParseDuration(args[0].Text()); err == nil {
			qb.handler.RetentionDays = int(d.Hours() / 24)
		}
	}
	if qb.handler.RetentionDays < 1 {
		qb.handler.RetentionDays = 1
	}
	return qb
}

func (qb *QueueBuilder) Success(args ...value.Value) *QueueBuilder {
	if len(args) > 0 {
		qb.handler.OnSuccess = lambdaOf(args[0])
	}
	return qb
}

func (qb *QueueBuilder) Error(args ...value.Value) *QueueBuilder {
	if len(args) > 0 {
		qb.handler.OnError = lambdaOf(args[0])
	}
	return qb
}

func (t *Tenant) registerQueue(handler *QueueHandler) {
	worker, err := t.ensureQueueWorker()
	if err != nil {
		return
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if !worker.closed {
		worker.handlers = append(worker.handlers, handler)
	}
}

// ── dispatch side (from a route, a cron, or another job) ─────────────────────────────────────────

// Dispatch records one message and returns what happened, as an object rather than a bare id:
//
//	{ queued: true,  id: 12 }    — stored
//	{ queued: false, id: 0  }    — a message with this dedupe key is already waiting
//	{ queued: false, error: "…" } — it could not be stored
//
// It reports rather than throws because a dispatch usually sits inside a request that has its own
// answer to give; a failed enqueue should let the handler decide whether that answer changes.
//
// Dispatch works from ANY of the app's domains: the store is opened per call and partitioned by the
// same appID the worker uses, so a route on one domain can feed a handler the whole app shares.
func (q *Queue) Dispatch(args ...value.Value) value.Value {
	if len(args) == 0 {
		return dispatchResult(0, false, "queue.dispatch(name, payload) needs a queue name")
	}
	name := strings.TrimSpace(args[0].Text())
	if name == "" {
		return dispatchResult(0, false, "queue.dispatch: the queue name is empty")
	}

	payload := "{}"
	if len(args) > 1 {
		encoded, err := json.Marshal(args[1].Interface())
		if err != nil {
			return dispatchResult(0, false, "queue.dispatch: payload is not serialisable: "+err.Error())
		}
		payload = string(encoded)
	}

	dedupeKey, delay := dispatchOptions(args)

	store := q.tenant.openQueueStore()
	if store == nil {
		return dispatchResult(0, false, "queue.dispatch: no queue store is available")
	}

	maxAttempts := 1
	if worker := q.tenant.queueWorker(); worker != nil {
		worker.mu.Lock()
		if handler := worker.byName[name]; handler != nil && handler.MaxAttempts > 1 {
			maxAttempts = handler.MaxAttempts
		}
		worker.mu.Unlock()
	}

	id, queued, err := store.Enqueue(q.tenant.appID(), name, payload, dedupeKey,
		maxAttempts, time.Now().Add(delay))
	if err != nil {
		return dispatchResult(0, false, err.Error())
	}
	return dispatchResult(id, queued, "")
}

// dispatchOptions reads the third argument: { key, delay }. `key` makes the dispatch idempotent;
// `delay` holds the message back ("30s", "5m") for work that should not run immediately.
func dispatchOptions(args []value.Value) (string, time.Duration) {
	if len(args) < 3 || !args[2].IsMap() {
		return "", 0
	}
	options, ok := args[2].Interface().(map[string]any)
	if !ok {
		return "", 0
	}
	key := ""
	if raw, found := options["key"]; found {
		key = strings.TrimSpace(fmt.Sprint(raw))
	}
	var delay time.Duration
	if raw, found := options["delay"]; found {
		if d, err := ParseDuration(fmt.Sprint(raw)); err == nil && d > 0 {
			delay = d
		}
	}
	return key, delay
}

func dispatchResult(id int64, queued bool, errMsg string) value.Value {
	out := map[string]value.Value{
		"queued": value.New(queued),
		"id":     value.New(int(id)),
	}
	if errMsg != "" {
		out["error"] = value.New(errMsg)
	}
	return value.New(out)
}

// List is the read side: every queue of this app with its counts and last outcome. A dashboard uses
// it to make background work visible, which is the difference between a queue and a black hole.
func (q *Queue) List(args ...value.Value) value.Value {
	store := q.tenant.openQueueStore()
	if store == nil {
		return collectionValue([]map[string]any{})
	}
	return collectionValue(store.ListQueues(q.tenant.appID()))
}

// Jobs lists individual messages: queue.jobs() · queue.jobs("send-welcome") ·
// queue.jobs("send-welcome", "failed", 20).
func (q *Queue) Jobs(args ...value.Value) value.Value {
	store := q.tenant.openQueueStore()
	if store == nil {
		return collectionValue([]map[string]any{})
	}
	name, status, limit := "", "", 50
	if len(args) > 0 {
		name = strings.TrimSpace(args[0].Text())
	}
	if len(args) > 1 {
		status = strings.TrimSpace(args[1].Text())
	}
	if len(args) > 2 && args[2].Int() > 0 {
		limit = args[2].Int()
	}
	return collectionValue(store.ListJobs(q.tenant.appID(), name, status, limit))
}

// Failed is the dead-letter view. The dead-letter queue is not a separate table — it is these rows,
// the ones that ran out of attempts, still carrying their payload and their last error. A second
// table would hold the same rows under another name and would need its own path back.
func (q *Queue) Failed(args ...value.Value) value.Value {
	store := q.tenant.openQueueStore()
	if store == nil {
		return collectionValue([]map[string]any{})
	}
	name := ""
	if len(args) > 0 {
		name = strings.TrimSpace(args[0].Text())
	}
	limit := 50
	if len(args) > 1 && args[1].Int() > 0 {
		limit = args[1].Int()
	}
	return collectionValue(store.ListJobs(q.tenant.appID(), name, "failed", limit))
}

// Replay revives dead-lettered messages — one by id, or every failure of a queue:
//
//	queue.replay("send-welcome")      // all of them
//	queue.replay("send-welcome", 42)  // just this one
//
// Named `replay` rather than `retry` because `.retry(n)` on the builder is the attempt BUDGET, and
// this is the operator ACTION of running dead letters again. Two different things; two words.
//
// Returns how many rows were revived.
func (q *Queue) Replay(args ...value.Value) value.Value {
	store := q.tenant.openQueueStore()
	if store == nil {
		return value.New(0)
	}
	name := ""
	if len(args) > 0 {
		name = strings.TrimSpace(args[0].Text())
	}
	var jobID int64
	if len(args) > 1 {
		jobID = int64(args[1].Int())
	}
	return value.New(int(store.RetryFailed(q.tenant.appID(), name, jobID)))
}

// openQueueStore returns a store for a tenant that does not run the worker. The worker runs on the
// app-tenant (identity level); a DOMAIN-tenant serving a request has no worker of its own, so it
// opens its own view onto the same backend — shared Postgres when a system database is connected,
// else the app's identity-level SQLite. Same partition key either way, so it reads and writes
// exactly the rows the worker sees.
func (t *Tenant) openQueueStore() queuestore.Store {
	if worker := t.queueWorker(); worker != nil {
		worker.mu.Lock()
		store := worker.store
		worker.mu.Unlock()
		if store != nil {
			return store
		}
	}
	if database.SystemIsPostgres() {
		return queuestore.NewPgStore(database.System, t.queueNodeID())
	}
	if database.System != nil {
		return queuestore.NewSqliteStore(database.System, t.queueNodeID())
	}
	db := appSqliteFor(t, "queue.db").db()
	if db == nil {
		return nil
	}
	store := queuestore.NewSqliteStore(db, t.queueNodeID())
	// A dispatching tenant may be the first to touch the file — without the schema its INSERT would
	// fail for a reason that has nothing to do with the caller. InitSchema is CREATE IF NOT EXISTS,
	// so this is idempotent against the worker doing the same.
	if err := store.InitSchema(); err != nil {
		fmt.Printf("[Queue] schema: %v\n", err)
		return nil
	}
	return store
}

// ── loading ──────────────────────────────────────────────────────────────────────────────────────

// LoadQueueFiles evaluates every _queue/*.kitwork.js at boot so handlers are REGISTERED before any
// message arrives. Folder routers compile lazily on first request, which is too late for a worker
// that must claim work on its own clock.
//
// `_queue` sits at the IDENTITY (app) level beside `_cron` and `_core` — apps/<identity>/_queue —
// because a queue is app infrastructure every domain shares, keyed by identity.
func (t *Tenant) LoadQueueFiles() {
	worker, err := t.ensureQueueWorker()
	if err != nil {
		fmt.Printf("[Queue] worker unavailable: %v\n", err)
		return
	}
	dir := t.resolveApp("_queue")
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		return // no _queue/ folder — nothing to work
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".kitwork.js") {
			continue
		}
		file := filepath.Join(dir, e.Name())
		bc, compileErr := compiler.CompileFile(file)
		if compileErr != nil {
			fmt.Printf("[Queue] compile %s: %v\n", e.Name(), compileErr)
			continue
		}
		hash := ""
		if raw, rerr := os.ReadFile(file); rerr == nil {
			sum := sha256.Sum256(raw)
			hash = hex.EncodeToString(sum[:])
		}
		stem := strings.TrimSuffix(e.Name(), ".kitwork.js")
		t.runQueueFile(worker, bc, stem, hash)
	}

	t.StartQueueWorker()
}

// runQueueFile executes one compiled _queue file in an isolated VM, then finalizes what it
// registered: attaches the bytecode and NAMES the handler after the file. One file = one queue; a
// file registering more keeps only the first, because the rest would collide on that same filename
// identity. Globals are COPIED so a queue file's top-level declarations never leak into the shared
// tenant VM.
func (t *Tenant) runQueueFile(worker *queueRuntime, bc *compiler.Bytecode, stem, contentHash string) {
	worker.mu.Lock()
	before := len(worker.handlers)
	worker.mu.Unlock()

	globals := make(map[string]value.Value, len(t.vm.Globals))
	for k, v := range t.vm.Globals {
		globals[k] = v
	}

	vm := runtime.New(bc.Program)
	vm.Builtins = t.vm.Builtins
	vm.Globals = globals
	vm.MaxEnergy = t.MaxEnergy
	vm.Run()

	worker.mu.Lock()
	registered := worker.handlers[before:]
	if len(registered) > 1 {
		fmt.Printf("[Queue] %s.kitwork.js registered %d handlers; one file = one queue — keeping only %q\n",
			stem, len(registered), stem)
		worker.handlers = worker.handlers[:before+1]
		registered = worker.handlers[before:]
	}
	for i := range registered {
		registered[i].Bytecode = bc
		registered[i].ContentHash = contentHash
		registered[i].Name = stem // the file IS the queue's identity
	}
	worker.mu.Unlock()
}
