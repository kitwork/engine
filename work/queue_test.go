package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

// newQueueTenant lays out an app with one _queue handler and boots it. Returns the running tenant;
// the caller closes it.
func newQueueTenant(t *testing.T, handlerName, handlerSource string) *Tenant {
	t.Helper()

	tmp := t.TempDir()
	site := filepath.Join(tmp, "acme", "localhost")
	queueDir := filepath.Join(tmp, "acme", "_queue") // _queue is IDENTITY-level, beside _cron
	if err := os.MkdirAll(queueDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(site, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(site, "router.kitwork.js"),
		[]byte(`import { router } from "kitwork"; router.get((ctx) => ctx.text("ok"));`), 0644); err != nil {
		t.Fatal(err)
	}
	if handlerName != "" {
		if err := os.WriteFile(filepath.Join(queueDir, handlerName+".kitwork.js"),
			[]byte(handlerSource), 0644); err != nil {
			t.Fatal(err)
		}
	}

	tenant := NewAppTenant(tmp, "acme")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	return tenant
}

// dispatch calls queue.dispatch the way a route handler would, and returns the result object.
func dispatch(t *testing.T, tenant *Tenant, args ...value.Value) value.Value {
	t.Helper()
	return (&Queue{tenant: tenant}).Dispatch(args...)
}

func countQueueRows(t *testing.T, tenant *Tenant, query string, args ...any) int {
	t.Helper()
	worker := tenant.queueWorker()
	if worker == nil || worker.db == nil {
		t.Fatal("queue worker has no database — the worker did not start")
	}
	var n int
	if err := worker.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	return n
}

// waitFor polls until cond holds or the deadline passes. Returns whether it held.
func waitFor(deadline time.Duration, cond func() bool) bool {
	stop := time.Now().Add(deadline)
	for time.Now().Before(stop) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cond()
}

// A dispatched message must reach its handler, carry its PAYLOAD intact, and land in history with the
// handler's own output and the gas it burned.
//
// The assertion is deliberately on the logged text rather than on status='completed': a handler that
// received an empty payload would still complete, so "it completed" is not evidence the payload
// arrived. The log line is the only thing that can only be produced by the real value.
func TestQueueDispatchDeliversPayloadAndHistory(t *testing.T) {
	tenant := newQueueTenant(t, "greet", `import { queue } from "kitwork";
queue.handle((job) => { job.log("hello " + job.payload.name + " x" + job.attempt); });`)
	defer tenant.Close()

	if tenant.queueWorker() == nil || tenant.queueWorker().db == nil {
		t.Fatal("queue worker did not start")
	}

	for _, name := range []string{"quoc", "mai"} {
		result := dispatch(t, tenant, value.New("greet"), value.New(map[string]any{"name": name}))
		if !result.Get("queued").IsTrue() {
			t.Fatalf("dispatch(%q) was not queued: %v", name, result.Get("error").Text())
		}
	}

	ok := waitFor(8*time.Second, func() bool {
		return countQueueRows(t, tenant, `SELECT COUNT(*) FROM queue_jobs WHERE status='completed'`) >= 2
	})
	tenant.StopQueueWorker()
	time.Sleep(200 * time.Millisecond) // let in-flight runs settle before snapshotting

	if !ok {
		t.Fatalf("messages never completed: %d of 2",
			countQueueRows(t, tenant, `SELECT COUNT(*) FROM queue_jobs WHERE status='completed'`))
	}

	// The definition was synced from the file, named after it.
	var name, origin, source string
	err := tenant.queueWorker().db.QueryRow(`SELECT name, origin, source FROM queues`).
		Scan(&name, &origin, &source)
	if err != nil {
		t.Fatalf("no queues row synced: %v", err)
	}
	if name != "greet" || origin != "file" || source != "_queue/greet.kitwork.js" {
		t.Errorf("queues row wrong: name=%q origin=%q source=%q", name, origin, source)
	}

	// The payload reached the handler — each message logged its own name, on attempt 1.
	for _, want := range []string{"hello quoc x1", "hello mai x1"} {
		n := countQueueRows(t, tenant,
			`SELECT COUNT(*) FROM queue_jobs WHERE output LIKE ?`, "%"+want+"%")
		if n != 1 {
			t.Errorf("handler output %q appears %d times, want 1 — payload did not arrive intact", want, n)
		}
	}

	if n := countQueueRows(t, tenant, `SELECT COUNT(*) FROM queue_jobs WHERE gas_used > 0`); n < 2 {
		t.Errorf("gas_used not recorded: only %d rows have gas > 0", n)
	}

	// Summary counters on the definition, which is what survives retention.
	var processed, failed int
	tenant.queueWorker().db.QueryRow(`SELECT processed_count, failed_count FROM queues`).
		Scan(&processed, &failed)
	if processed < 2 || failed != 0 {
		t.Errorf("queues summary wrong: processed=%d failed=%d, want processed>=2 failed=0", processed, failed)
	}
}

// A dedupe key makes a dispatch idempotent. THE CONTROL IS THE POINT: the same three dispatches
// WITHOUT a key must produce three rows. Without that half, a test asserting "one row" would pass
// just as well if dispatch were broken and stored nothing beyond the first.
func TestQueueDedupeKeyDropsRepeats(t *testing.T) {
	tenant := newQueueTenant(t, "charge", `import { queue } from "kitwork";
queue.handle((job) => { job.log("charged"); });`)
	defer tenant.Close()

	// Stop the worker first: this test is about what ENQUEUE stores, and a worker draining rows
	// mid-count would measure the poller instead.
	tenant.StopQueueWorker()

	keyed := value.New(map[string]any{"key": "invoice:42"})
	queuedCount := 0
	for i := 0; i < 3; i++ {
		result := dispatch(t, tenant, value.New("charge"),
			value.New(map[string]any{"invoice": 42}), keyed)
		if result.Get("queued").IsTrue() {
			queuedCount++
		}
	}
	if queuedCount != 1 {
		t.Errorf("dispatch reported queued=true %d times for one key, want 1", queuedCount)
	}
	if n := countQueueRows(t, tenant,
		`SELECT COUNT(*) FROM queue_jobs WHERE dedupe_key='invoice:42'`); n != 1 {
		t.Errorf("keyed dispatch stored %d rows, want 1 — the UNIQUE arbiter did not drop repeats", n)
	}

	// CONTROL: no key means every dispatch is a new message.
	for i := 0; i < 3; i++ {
		if result := dispatch(t, tenant, value.New("charge"),
			value.New(map[string]any{"invoice": 7})); !result.Get("queued").IsTrue() {
			t.Fatalf("unkeyed dispatch %d was refused: %v", i, result.Get("error").Text())
		}
	}
	if n := countQueueRows(t, tenant,
		`SELECT COUNT(*) FROM queue_jobs WHERE dedupe_key IS NULL`); n != 3 {
		t.Errorf("unkeyed dispatches stored %d rows, want 3 — NULL keys are being deduplicated too", n)
	}
}

// A handler that keeps failing must exhaust its attempts, land in the dead letter with its error
// recorded, fire .error(), and then be replayable by queue.replay().
func TestQueueRetryDeadLetterAndReplay(t *testing.T) {
	savedLocal := AllowLocal
	AllowLocal = true
	defer func() { AllowLocal = savedLocal }()

	savedBackoff := queueBackoff
	queueBackoff = func(int) time.Duration { return 10 * time.Millisecond }
	defer func() { queueBackoff = savedBackoff }()

	var errHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&errHits, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// The loop blows the energy budget, so the VM halts the handler — a real failure, not a thrown
	// value the language does not have.
	tenant := newQueueTenant(t, "flaky", `import { queue, http } from "kitwork";
queue
  .retry(2)
  .handle(() => { for (let i = 0; i < 1000000; i++) {} })
  .error((job, err) => { http.get("`+srv.URL+`/errhit"); });`)
	tenant.MaxEnergy = 3000
	defer tenant.Close()

	if result := dispatch(t, tenant, value.New("flaky"), value.New(map[string]any{"n": 1})); !result.Get("queued").IsTrue() {
		t.Fatalf("dispatch refused: %v", result.Get("error").Text())
	}

	ok := waitFor(10*time.Second, func() bool {
		return countQueueRows(t, tenant,
			`SELECT COUNT(*) FROM queue_jobs WHERE status='failed' AND attempt=2`) >= 1 &&
			atomic.LoadInt32(&errHits) >= 1
	})
	tenant.StopQueueWorker()
	time.Sleep(200 * time.Millisecond)

	if !ok {
		t.Fatalf("message never dead-lettered after 2 attempts (errHits=%d)", atomic.LoadInt32(&errHits))
	}

	var errMsg string
	tenant.queueWorker().db.QueryRow(
		`SELECT error_message FROM queue_jobs WHERE status='failed' LIMIT 1`).Scan(&errMsg)
	if strings.TrimSpace(errMsg) == "" {
		t.Error("dead-lettered message has no error_message — the failure reason was lost")
	}

	// The dead letter is readable as data, with its payload still attached.
	failed := (&Queue{tenant: tenant}).Failed(value.New("flaky"))
	if failed.Length().Int() < 1 {
		t.Fatalf("queue.failed() returned nothing, want the dead-lettered message")
	}

	// And replayable: replay puts it back to pending with a fresh budget.
	revived := (&Queue{tenant: tenant}).Replay(value.New("flaky"))
	if revived.Int() < 1 {
		t.Fatalf("queue.replay() revived %d messages, want >= 1", revived.Int())
	}
	if n := countQueueRows(t, tenant,
		`SELECT COUNT(*) FROM queue_jobs WHERE status='pending' AND attempt=0`); n < 1 {
		t.Error("retried message is not pending with attempt reset to 0")
	}
}

// A message for a queue this node has no code for must dead-letter with a readable reason, not spin.
// Silently retrying forever is the failure mode that makes a queue look healthy while nothing moves.
func TestQueueUnknownHandlerDeadLetters(t *testing.T) {
	tenant := newQueueTenant(t, "known", `import { queue } from "kitwork";
queue.handle((job) => { job.log("ok"); });`)
	defer tenant.Close()

	if result := dispatch(t, tenant, value.New("ghost"), value.New(map[string]any{})); !result.Get("queued").IsTrue() {
		t.Fatalf("dispatch refused: %v", result.Get("error").Text())
	}

	ok := waitFor(6*time.Second, func() bool {
		return countQueueRows(t, tenant,
			`SELECT COUNT(*) FROM queue_jobs WHERE name='ghost' AND status='failed'`) == 1
	})
	tenant.StopQueueWorker()
	time.Sleep(200 * time.Millisecond)

	if !ok {
		t.Fatal("message for an unknown queue never failed — it is being claimed and released forever")
	}

	var errMsg string
	tenant.queueWorker().db.QueryRow(
		`SELECT error_message FROM queue_jobs WHERE name='ghost'`).Scan(&errMsg)
	if !strings.Contains(errMsg, "no registered handler") {
		t.Errorf("unknown-queue failure says %q, want it to name the missing handler", errMsg)
	}

	// CONTROL: the known queue in the same app still works, so the failure above is about the
	// missing handler and not about the worker being broken.
	if result := dispatch(t, tenant, value.New("known"), value.New(map[string]any{})); !result.Get("queued").IsTrue() {
		t.Fatalf("control dispatch refused: %v", result.Get("error").Text())
	}
	tenant.StartQueueWorker()
	completed := waitFor(6*time.Second, func() bool {
		return countQueueRows(t, tenant,
			`SELECT COUNT(*) FROM queue_jobs WHERE name='known' AND status='completed'`) == 1
	})
	tenant.StopQueueWorker()
	if !completed {
		t.Error("control: the known queue did not complete, so this test proves nothing about ghost")
	}
}

// A delayed dispatch must not be available before its delay elapses.
func TestQueueDelayHoldsMessageBack(t *testing.T) {
	tenant := newQueueTenant(t, "later", `import { queue } from "kitwork";
queue.handle((job) => { job.log("ran"); });`)
	defer tenant.Close()
	tenant.StopQueueWorker()

	result := dispatch(t, tenant, value.New("later"), value.New(map[string]any{}),
		value.New(map[string]any{"delay": "1h"}))
	if !result.Get("queued").IsTrue() {
		t.Fatalf("delayed dispatch refused: %v", result.Get("error").Text())
	}

	// available_at is an hour out, so a claim right now must find nothing.
	claimed := tenant.queueWorker().store.Claim("acme", "test-node", time.Minute, 10)
	if len(claimed) != 0 {
		t.Errorf("claimed %d delayed messages, want 0 — delay is not being honoured", len(claimed))
	}

	// CONTROL: the same dispatch with no delay is claimable immediately, proving the claim path
	// itself works and the zero above is the delay doing its job.
	if result := dispatch(t, tenant, value.New("later"), value.New(map[string]any{})); !result.Get("queued").IsTrue() {
		t.Fatalf("control dispatch refused: %v", result.Get("error").Text())
	}
	claimed = tenant.queueWorker().store.Claim("acme", "test-node", time.Minute, 10)
	if len(claimed) != 1 {
		t.Errorf("claimed %d undelayed messages, want 1", len(claimed))
	}
}
