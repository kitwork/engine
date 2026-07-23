package work

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kitwork/engine/database"
)

// Scheduler Phase 3 — the shared-Postgres cluster path, exercised against the real system DB. Gated
// behind KITWORK_PG_DEMO so it never runs in the ordinary suite (it touches a network Postgres):
//
//	KITWORK_PG_DEMO=1 go test ./work/ -run TestCronClusterPostgres -v
//
// Two Tenant instances (node-A + node-B) — same app, same DB — coordinate through cron_runs.
// It proves the two things that make multi-node work: (1) AT-MOST-ONCE — the UNIQUE(identity, name,
// scheduled_for) arbiter + FOR UPDATE SKIP LOCKED claim mean a slot runs on exactly one node; (2) CRASH
// RECOVERY — a slot left 'running' by a dead node whose lease expired is reclaimed and finished by a
// live node.
func TestCronClusterPostgres(t *testing.T) {
	if os.Getenv("KITWORK_PG_DEMO") == "" {
		t.Skip("set KITWORK_PG_DEMO=1 to run the shared-Postgres multi-node demo")
	}

	cfg := &database.Config{
		Alias: "system", Type: "postgres", Host: "103.166.184.138", Port: 5432,
		User: "postgres", Password: "!kitwork@1612", Name: "kitwork", SSLMode: "disable",
	}
	db, err := cfg.Connect()
	if err != nil {
		t.Fatalf("connect system Postgres: %v", err)
	}
	defer db.Close()

	savedSystem, savedLease := database.System, cronLeaseTTL
	database.System = db           // presence of a system DB routes cron to the shared PG store (no flag)
	cronLeaseTTL = 2 * time.Second // short lease → fast crash recovery for the demo
	defer func() { database.System, cronLeaseTTL = savedSystem, savedLease }()

	tmp, _ := os.MkdirTemp("", "kwcluster-*")
	defer os.RemoveAll(tmp)
	dir := filepath.Join(tmp, "kwtest", "clusterdemo.local")
	cronDir := filepath.Join(tmp, "kwtest", "_cron") // _cron is IDENTITY-level
	if err := os.MkdirAll(cronDir, 0755); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "router.kitwork.js"),
		[]byte(`import { router } from "kitwork"; router.get((ctx)=>ctx.text("ok"));`), 0644)
	os.WriteFile(filepath.Join(cronDir, "beat.kitwork.js"),
		[]byte(`import { cron } from "kitwork"; cron.every("1s").handle((ctx)=>{ ctx.log("beat "+ctx.scheduledFor); });`), 0644)

	const appID = "kwtest" // partition key is the IDENTITY, not identity/domain
	clean := func() {
		db.Exec(`DELETE FROM cron_runs WHERE identity=$1`, appID)
		db.Exec(`DELETE FROM crons WHERE identity=$1`, appID)
	}
	clean()       // fresh start
	defer clean() // leave the shared tables empty of this demo's rows

	// ── two nodes, same app, same DB ──────────────────────────────────────────────────────────────
	nodeA := NewAppTenant(tmp, "kwtest")
	nodeA.cronNode = "node-A"
	nodeB := NewAppTenant(tmp, "kwtest")
	nodeB.cronNode = "node-B"
	if err := nodeA.Run(); err != nil {
		t.Fatal(err)
	}
	if err := nodeB.Run(); err != nil {
		t.Fatal(err)
	}

	// let them coordinate for a while
	var total, distinct int
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT scheduled_for) FROM cron_runs
			WHERE identity=$1 AND status='completed'`, appID).Scan(&total, &distinct)
		if total >= 5 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	nodeA.StopCronJobs()
	nodeB.StopCronJobs()
	time.Sleep(500 * time.Millisecond) // let in-flight runs settle

	// PART 1 — at-most-once across nodes
	db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT scheduled_for) FROM cron_runs WHERE identity=$1`, appID).
		Scan(&total, &distinct)
	t.Logf("PART 1: %d executions across %d distinct slots", total, distinct)
	if total < 5 {
		t.Fatalf("too few executions (%d) — the shared dispatcher never ran", total)
	}
	if total != distinct {
		t.Fatalf("AT-MOST-ONCE BROKEN: %d executions for %d slots — a slot ran on more than one node", total, distinct)
	}
	rows, _ := db.Query(`SELECT node, COUNT(*) FROM cron_runs
		WHERE identity=$1 AND status='completed' GROUP BY node ORDER BY node`, appID)
	dist := map[string]int{}
	for rows.Next() {
		var owner sql.NullString
		var c int
		rows.Scan(&owner, &c)
		dist[owner.String] = c
		t.Logf("   %-8s ran %d slots", owner.String, c)
	}
	rows.Close()
	if dist["node-A"] > 0 && dist["node-B"] > 0 {
		t.Logf("   → work SPREAD across both nodes ✓")
	} else {
		t.Logf("   → one node won every race this run (still correct: at-most-once holds). dist=%v", dist)
	}

	// PART 2 — crash recovery. Fabricate a slot left 'running' by a crashed node whose lease has expired.
	_, err = db.Exec(`INSERT INTO cron_runs
		(identity, name, scheduled_for, status, attempt, max_attempts, available_at, node, lease_until, started_at, created_at)
		VALUES ($1, 'beat', TIMESTAMPTZ '2020-01-01 00:00:00+00', 'running', 0, 1,
		        NOW()-interval '30 second', 'node-DEAD', NOW()-interval '10 second', NOW()-interval '30 second', NOW())`,
		appID)
	if err != nil {
		t.Fatalf("seed dead-node slot: %v", err)
	}

	// A fresh live node boots: its dispatcher must reclaim the expired lease and finish the orphaned slot.
	nodeC := NewAppTenant(tmp, "kwtest")
	nodeC.cronNode = "node-C"
	if err := nodeC.Run(); err != nil {
		t.Fatal(err)
	}
	var status, owner string
	rec := time.Now().Add(8 * time.Second)
	for time.Now().Before(rec) {
		db.QueryRow(`SELECT status, COALESCE(node,'') FROM cron_runs
			WHERE identity=$1 AND scheduled_for = TIMESTAMPTZ '2020-01-01 00:00:00+00'`, appID).Scan(&status, &owner)
		if status == "completed" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	nodeC.StopCronJobs()
	nodeA.Close()
	nodeB.Close()
	nodeC.Close()

	t.Logf("PART 2: orphaned slot → status=%q owner=%q", status, owner)
	if status != "completed" {
		t.Fatalf("CRASH RECOVERY FAILED: dead node's slot ended %q, not completed", status)
	}
	if owner == "node-DEAD" || owner == "" {
		t.Fatalf("CRASH RECOVERY FAILED: slot not taken over by a live node (owner=%q)", owner)
	}
	t.Logf("   → node-DEAD's orphaned work was reclaimed + finished by %s ✓", owner)
}
