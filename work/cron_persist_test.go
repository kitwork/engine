package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Scheduler Phase 2 — durable path. A .persist() job must: be synced into scheduler.db (schedules row),
// fire through the DB dispatcher, record each run as a completed execution WITH history (ctx.log output
// + gas), and never double-fire a slot (UNIQUE(identity, name, scheduled_for) idempotency).
func TestCronPersistSuccessAndHistory(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-persist-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	dir := filepath.Join(tmp, "acme", "localhost")
	cronDir := filepath.Join(tmp, "acme", "_cron") // _cron is IDENTITY-level (apps/<identity>/_cron)
	if err := os.MkdirAll(cronDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"),
		[]byte(`import { router } from "kitwork"; router.get((ctx) => ctx.text("ok"));`), 0644); err != nil {
		t.Fatal(err)
	}
	// unnamed → filename "beat" is the identity; .persist() AFTER .handle() must still take effect
	beat := `import { cron } from "kitwork";` + "\n" +
		`cron.every("1s").handle((ctx) => { ctx.log("tick " + ctx.attempt); }).retention("7d");`
	if err := os.WriteFile(filepath.Join(cronDir, "beat.kitwork.js"), []byte(beat), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewAppTenant(tmp, "acme")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()

	if tenant.cronDB == nil {
		t.Fatal("persisted scheduler did not start (cronDB nil) — .persist() not detected?")
	}

	// Wait for at least 2 completed executions (dispatcher ticks every 1s).
	var completed int
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		_ = tenant.cronDB.QueryRow(`SELECT COUNT(*) FROM cron_runs WHERE status='completed'`).Scan(&completed)
		if completed >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	tenant.StopCronJobs()              // halt dispatcher for a stable snapshot
	time.Sleep(250 * time.Millisecond) // let in-flight runClaimed goroutines settle

	if completed < 2 {
		t.Fatalf("persisted job did not complete enough runs: got %d, want >= 2", completed)
	}

	// schedules row synced from the file
	var name, origin, expr, srcPath string
	var retention int
	err = tenant.cronDB.QueryRow(
		`SELECT name, origin, schedule, source, retention FROM crons`).
		Scan(&name, &origin, &expr, &srcPath, &retention)
	if err != nil {
		t.Fatalf("no schedules row synced: %v", err)
	}
	if name != "beat" || origin != "file" || expr != "@every 1s" {
		t.Errorf("schedules row wrong: name=%q origin=%q expr=%q (want beat/file/@every 1s)", name, origin, expr)
	}
	if retention != 7 {
		t.Errorf("retention_days=%d, want 7 (from .retention(\"7d\"))", retention)
	}

	// History captured: output has the logged line, gas recorded, all completed.
	var total, distinctSlots, withOutput, withGas int
	tenant.cronDB.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT scheduled_for) FROM cron_runs`).Scan(&total, &distinctSlots)
	tenant.cronDB.QueryRow(`SELECT COUNT(*) FROM cron_runs WHERE output LIKE 'tick %'`).Scan(&withOutput)
	tenant.cronDB.QueryRow(`SELECT COUNT(*) FROM cron_runs WHERE gas_used > 0`).Scan(&withGas)

	if total != distinctSlots {
		t.Errorf("IDEMPOTENCY broken: %d executions across %d distinct slots — a slot fired twice", total, distinctSlots)
	}
	if withOutput < 2 {
		t.Errorf("ctx.log output not captured to history: only %d rows have output", withOutput)
	}
	if withGas < 2 {
		t.Errorf("gas_used not recorded: only %d rows have gas > 0", withGas)
	}

	// A second Run() on the SAME dir must not error or duplicate the schedule (sync is idempotent).
	tenant2 := NewAppTenant(tmp, "acme")
	if err := tenant2.Run(); err != nil {
		t.Fatalf("restart Run() failed: %v", err)
	}
	defer tenant2.Close()
	var scheduleCount int
	tenant2.cronDB.QueryRow(`SELECT COUNT(*) FROM crons`).Scan(&scheduleCount)
	if scheduleCount != 1 {
		t.Errorf("restart duplicated schedules: got %d rows, want 1", scheduleCount)
	}
	tenant2.StopCronJobs()
}

// Scheduler-level retry (.retries(n)) + the .error() hook. A handler that always fails (energy limit)
// must cycle attempts up to max, land in 'failed', and fire .error().
func TestCronPersistRetryAndError(t *testing.T) {
	savedLocal := AllowLocal
	AllowLocal = true
	defer func() { AllowLocal = savedLocal }()

	savedBackoff := cronBackoff
	cronBackoff = func(int) time.Duration { return 10 * time.Millisecond } // don't wait real 20s
	defer func() { cronBackoff = savedBackoff }()

	var errHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&errHits, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	tmp, err := os.MkdirTemp("", "kitwork-retry-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	dir := filepath.Join(tmp, "acme", "localhost")
	cronDir := filepath.Join(tmp, "acme", "_cron") // _cron is IDENTITY-level (apps/<identity>/_cron)
	if err := os.MkdirAll(cronDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"),
		[]byte(`import { router } from "kitwork"; router.get((ctx) => ctx.text("ok"));`), 0644); err != nil {
		t.Fatal(err)
	}
	// The handler blows the energy budget → the VM returns an error → the job "throws".
	flap := `import { cron, http } from "kitwork";` + "\n" +
		`cron.every("1s")` + "\n" +
		`  .handle(() => { for (let i = 0; i < 1000000; i++) {} })` + "\n" +
		`  .retries(2)` + "\n" +
		`  .error((ctx, err) => { http.get("` + srv.URL + `/errhit"); });`
	if err := os.WriteFile(filepath.Join(cronDir, "flap.kitwork.js"), []byte(flap), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewAppTenant(tmp, "acme")
	tenant.MaxEnergy = 3000 // small budget so the loop halts fast with an energy-limit error
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	if tenant.cronDB == nil {
		t.Fatal("persisted scheduler did not start")
	}

	var failed int
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		_ = tenant.cronDB.QueryRow(
			`SELECT COUNT(*) FROM cron_runs WHERE status='failed' AND attempt=2`).Scan(&failed)
		if failed >= 1 && atomic.LoadInt32(&errHits) >= 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	tenant.StopCronJobs()
	time.Sleep(250 * time.Millisecond)

	if failed < 1 {
		t.Fatalf("no execution reached status='failed' with attempt=2 (max_attempts) — retry loop broken")
	}
	if n := atomic.LoadInt32(&errHits); n < 1 {
		t.Fatalf(".error() hook never fired (errHits=%d)", n)
	}

	// The error message from the energy-limit halt must be recorded.
	var msg string
	tenant.cronDB.QueryRow(
		`SELECT error_message FROM cron_runs WHERE status='failed' LIMIT 1`).Scan(&msg)
	if msg == "" {
		t.Errorf("failed execution has no error_message recorded")
	}
}
