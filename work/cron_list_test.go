package work

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// cron.list() is the READ side of the scheduler: it returns the durable per-cron summary from the
// `crons` table (name, schedule, source, status, last run + counts) so a dashboard can show the
// otherwise-invisible background jobs. It reads the same partition (appID) the dispatcher writes.
func TestCronListReadAPI(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-cronlist-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	cronDir := filepath.Join(tmp, "acme", "_cron")
	if err := os.MkdirAll(cronDir, 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(cronDir, "pulse.kitwork.js"),
		[]byte(`import { cron } from "kitwork"; cron.every("1s").handle((ctx)=>{ ctx.log("tick"); });`), 0644)

	tenant := NewAppTenant(tmp, "acme")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()

	// Wait for at least one completed run so the summary columns are populated.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		tenant.cronDB.QueryRow(`SELECT run_count FROM crons WHERE name='pulse'`).Scan(&n)
		if n >= 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	tenant.StopCronJobs()

	list := tenant.Kitwork().Cron().List().Interface()
	rows, ok := list.([]any)
	if !ok {
		t.Fatalf("cron.list() not a list: %T %v", list, list)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 cron (pulse), got %d: %v", len(rows), rows)
	}
	job := rows[0].(map[string]any)
	if job["name"] != "pulse" {
		t.Errorf("name = %v, want pulse", job["name"])
	}
	if job["schedule"] != "@every 1s" {
		t.Errorf("schedule = %v, want '@every 1s'", job["schedule"])
	}
	if job["source"] != "_cron/pulse.kitwork.js" {
		t.Errorf("source = %v, want '_cron/pulse.kitwork.js'", job["source"])
	}
	// collectionValue JSON round-trips the row, so integers arrive as float64.
	if job["runCount"].(float64) < 1 {
		t.Errorf("runCount = %v, want >= 1", job["runCount"])
	}
	if job["lastStatus"] != "completed" {
		t.Errorf("lastStatus = %v, want completed", job["lastStatus"])
	}
}
