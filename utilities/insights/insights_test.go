package insights_test

import (
	"database/sql"
	"testing"

	"github.com/kitwork/engine/utilities/insights"
	_ "modernc.org/sqlite"
)

func TestInsightsStore(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Open sqlite failed: %v", err)
	}
	defer db.Close()

	store := insights.NewStore(db)
	if store == nil {
		t.Fatal("NewStore returned nil")
	}

	// Record searches
	if err := store.RecordSearch("golang vm", 5); err != nil {
		t.Fatalf("RecordSearch failed: %v", err)
	}
	if err := store.RecordSearch("missing feature", 0); err != nil {
		t.Fatalf("RecordSearch failed: %v", err)
	}
	if err := store.RecordSearch("missing feature", 0); err != nil {
		t.Fatalf("RecordSearch failed: %v", err)
	}

	// Test Searches
	searches, err := store.Searches(10)
	if err != nil {
		t.Fatalf("Searches failed: %v", err)
	}
	if len(searches) != 2 {
		t.Errorf("Expected 2 search records, got %d", len(searches))
	}

	// Test Gaps
	gaps, err := store.Gaps(10)
	if err != nil {
		t.Fatalf("Gaps failed: %v", err)
	}
	if len(gaps) != 1 {
		t.Fatalf("Expected 1 content gap, got %d", len(gaps))
	}
	if gaps[0].Query != "missing feature" || gaps[0].Total != 2 {
		t.Errorf("Unexpected gap record: %+v", gaps[0])
	}
}
