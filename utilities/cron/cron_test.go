package cron_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/kitwork/engine/utilities/cron"
	_ "modernc.org/sqlite"
)

func TestSqliteCronStore(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Open db failed: %v", err)
	}
	defer db.Close()

	store := cron.NewSqliteStore(db, "node_1")
	if err := store.InitSchema(); err != nil {
		t.Fatalf("InitSchema failed: %v", err)
	}

	jobs := []cron.JobRecord{
		{
			Name:          "daily-backup",
			Expression:    "0 0 * * *",
			Timezone:      "UTC",
			OverlapPolicy: "skip",
			MaxAttempts:   1,
			RetentionDays: 30,
			ContentHash:   "hash123",
		},
	}

	if err := store.Sync("app_1", jobs); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	slotTime := time.Now().Truncate(time.Minute)
	if err := store.InsertSlot("app_1", "daily-backup", slotTime, 1); err != nil {
		t.Fatalf("InsertSlot failed: %v", err)
	}

	if !store.HasActive("app_1", "daily-backup") {
		t.Error("Expected HasActive to return true")
	}

	claimed := store.Claim("app_1", "node_1", 30*time.Second, 10)
	if len(claimed) != 1 {
		t.Fatalf("Expected 1 claimed slot, got %d", len(claimed))
	}
	if claimed[0].Name != "daily-backup" {
		t.Errorf("Expected job name 'daily-backup', got '%s'", claimed[0].Name)
	}

	store.Complete(claimed[0].ID, "success output", 100)
	store.RecordSummary("app_1", "daily-backup", "completed")

	crons := store.ListCrons("app_1")
	if len(crons) != 1 {
		t.Fatalf("Expected 1 cron in list, got %d", len(crons))
	}
	if crons[0]["name"] != "daily-backup" {
		t.Errorf("Expected name 'daily-backup', got '%v'", crons[0]["name"])
	}
}
