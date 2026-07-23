package work

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The compact per-cron summary on `crons` (run_count/last_status/last_run) must accumulate as runs
// complete, and successful cron_runs rows must be PRUNABLE while that summary survives — so the run log
// stays small (transient) but "how is this cron doing" is always one row.
func TestCronSummaryAndRetentionSplit(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-summary-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	cronDir := filepath.Join(tmp, "acme", "_cron")
	if err := os.MkdirAll(cronDir, 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(cronDir, "beat.kitwork.js"),
		[]byte(`import { cron } from "kitwork"; cron.every("1s").handle((ctx)=>{ ctx.log("ok"); });`), 0644)

	tenant := NewAppTenant(tmp, "acme")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()

	// wait for a few completed runs
	var completed int
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		tenant.cronDB.QueryRow(`SELECT COUNT(*) FROM cron_runs WHERE status='completed'`).Scan(&completed)
		if completed >= 3 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	tenant.StopCronJobs()
	time.Sleep(300 * time.Millisecond)

	// summary on crons accumulated
	var runCount, failCount int
	var lastStatus, lastRun string
	err = tenant.cronDB.QueryRow(`SELECT run_count, fail_count, last_status, last_run FROM crons WHERE name='beat'`).
		Scan(&runCount, &failCount, &lastStatus, &lastRun)
	if err != nil {
		t.Fatalf("crons summary query: %v", err)
	}
	if runCount < 3 {
		t.Errorf("run_count=%d, want >= 3", runCount)
	}
	if failCount != 0 {
		t.Errorf("fail_count=%d, want 0 (no failures)", failCount)
	}
	if lastStatus != "completed" {
		t.Errorf("last_status=%q, want completed", lastStatus)
	}
	if lastRun == "" {
		t.Errorf("last_run empty, want a timestamp")
	}

	var runsBefore int
	tenant.cronDB.QueryRow(`SELECT COUNT(*) FROM cron_runs WHERE status='completed'`).Scan(&runsBefore)

	// Prune successes (completedBefore in the future → delete them all); the summary must NOT change.
	tenant.cronStore.retention("acme", "beat", time.Now().Add(time.Hour), time.Now().Add(-24*time.Hour))

	var runsAfter, runCountAfter int
	tenant.cronDB.QueryRow(`SELECT COUNT(*) FROM cron_runs WHERE status='completed'`).Scan(&runsAfter)
	tenant.cronDB.QueryRow(`SELECT run_count FROM crons WHERE name='beat'`).Scan(&runCountAfter)

	if runsBefore == 0 {
		t.Fatal("no completed cron_runs to prune")
	}
	if runsAfter != 0 {
		t.Errorf("success prune left %d completed rows, want 0 (successes are transient)", runsAfter)
	}
	if runCountAfter != runCount {
		t.Errorf("summary run_count changed after prune: %d → %d (must survive)", runCount, runCountAfter)
	}
	t.Logf("pruned %d completed rows; crons summary intact (run_count=%d, last_status=%s)",
		runsBefore, runCountAfter, lastStatus)
}
