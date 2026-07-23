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
)

// Scheduler Phase 1 end to end for `import { cron }`. Two _cron/*.kitwork.js files register interval
// jobs and must be eagerly loaded at Run() and FIRE on their own — no request needed:
//   - tick.kitwork.js  → cron.every(...)   (name "tick" from the filename)
//   - beat.kitwork.js  → cron.every(...)   (name "beat" from the filename)
//
// Each handler runs `http.get(url)` (a bare statement → POPFIN fires it) so a real httptest server
// counts the ticks per path, which also proves kitwork()/http resolve inside a cron callback (own
// bytecode FastReset + tenant Builtins).
func TestCronLoadAndFire(t *testing.T) {
	savedLocal := AllowLocal
	AllowLocal = true // handlers hit loopback; SSRF guard would otherwise block it
	defer func() { AllowLocal = savedLocal }()

	var tickHits, beatHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/tick"):
			atomic.AddInt32(&tickHits, 1)
		case strings.HasPrefix(r.URL.Path, "/beat"):
			atomic.AddInt32(&beatHits, 1)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	tmp, err := os.MkdirTemp("", "kitwork-cron-*")
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
	// tenant marker (root router) — not hit in this test, but keeps the tenant well-formed
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"),
		[]byte(`import { router } from "kitwork"; router.get((ctx) => ctx.text("ok"));`), 0644); err != nil {
		t.Fatal(err)
	}

	// name comes from the filename "tick" (one file = one cron)
	tick := `import { cron, http } from "kitwork";` + "\n" +
		`cron.every("1s").handle(() => {` + "\n" +
		`  http.get("` + srv.URL + `/tick");` + "\n" +
		`});`
	if err := os.WriteFile(filepath.Join(cronDir, "tick.kitwork.js"), []byte(tick), 0644); err != nil {
		t.Fatal(err)
	}

	// UNNAMED — the filename "beat" is the identity
	beat := `import { cron, http } from "kitwork";` + "\n" +
		`cron.every("1s").handle(() => {` + "\n" +
		`  http.get("` + srv.URL + `/beat");` + "\n" +
		`});`
	if err := os.WriteFile(filepath.Join(cronDir, "beat.kitwork.js"), []byte(beat), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewAppTenant(tmp, "acme") // app runtime for identity "acme" — owns apps/acme/_cron
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close() // StopCronJobs — halt the ticker goroutines

	// Jobs registered at Run() (before any request). Give the 1s dispatcher time for a few fires.
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&tickHits) >= 2 && atomic.LoadInt32(&beatHits) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if n := atomic.LoadInt32(&tickHits); n < 2 {
		t.Fatalf("named cron job did not fire enough: got %d tick hits, want >= 2", n)
	}
	if n := atomic.LoadInt32(&beatHits); n < 2 {
		t.Fatalf("filename-default cron job did not fire enough: got %d beat hits, want >= 2", n)
	}

	// Registry must hold both jobs, each with its own bytecode + resolved identity.
	tenant.cronMu.Lock()
	defer tenant.cronMu.Unlock()
	if len(tenant.crons) != 2 {
		t.Fatalf("want 2 registered crons, got %d", len(tenant.crons))
	}
	names := map[string]*CronJob{}
	for _, j := range tenant.crons {
		names[j.Name] = j
		if j.Bytecode == nil {
			t.Errorf("cron %q has no bytecode attached — executeCronCallback would FastReset the wrong code", j.Name)
		}
		if j.Expression != "@every 1s" {
			t.Errorf("cron %q expression = %q, want \"@every 1s\"", j.Name, j.Expression)
		}
	}
	if _, ok := names["tick"]; !ok {
		t.Errorf("explicit-name job \"tick\" missing; got names %v", keysOf(names))
	}
	if _, ok := names["beat"]; !ok {
		t.Errorf("filename-default job \"beat\" missing (filename identity not applied); got names %v", keysOf(names))
	}
}

func keysOf(m map[string]*CronJob) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
