package analytics

import (
	"context"
	"testing"
	"time"
)

func TestStoreScanAndAggregate(t *testing.T) {
	schema, err := NewSchema(
		Column{Name: "tenant", Kind: KindText},
		Column{Name: "status", Kind: KindText},
		Column{Name: "active", Kind: KindBool},
		Column{Name: "price", Kind: KindFloat64},
		Column{Name: "quantity", Kind: KindInt64},
		Column{Name: "created_at", Kind: KindTime},
	)
	if err != nil {
		t.Fatalf("new schema: %v", err)
	}
	store := NewStore(schema)
	first := []Row{
		{
			"tenant":     "acme",
			"status":     "active",
			"active":     true,
			"price":      12.5,
			"quantity":   int64(4),
			"created_at": time.Date(2026, 8, 1, 8, 30, 0, 0, time.UTC),
		},
		{
			"tenant":     "acme",
			"status":     "active",
			"active":     true,
			"price":      18.25,
			"quantity":   int64(2),
			"created_at": time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC),
		},
	}
	second := []Row{
		{
			"tenant":     "kit",
			"status":     "disabled",
			"active":     false,
			"price":      99.0,
			"quantity":   int64(1),
			"created_at": time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
		},
	}
	if err := store.Append(first); err != nil {
		t.Fatalf("append first: %v", err)
	}
	if err := store.Append(second); err != nil {
		t.Fatalf("append second: %v", err)
	}

	filter, err := Eq("tenant", "acme")
	if err != nil {
		t.Fatalf("eq: %v", err)
	}
	active, err := Eq("active", true)
	if err != nil {
		t.Fatalf("eq bool: %v", err)
	}

	rows, err := store.Scan(context.Background(), filter, active)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("scan rows = %d, want 2", len(rows))
	}
	if rows[0]["tenant"].(string) != "acme" {
		t.Fatalf("row tenant = %#v", rows[0]["tenant"])
	}

	count, err := store.Count(context.Background(), filter, active)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}

	summary, err := store.GroupBy(
		context.Background(),
		[]string{"status"},
		nil,
		Count("rows"),
		SumInt64("quantity", ""),
		AvgFloat64("price", ""),
	)
	if err != nil {
		t.Fatalf("group by: %v", err)
	}
	if len(summary) != 2 {
		t.Fatalf("group by rows = %d, want 2", len(summary))
	}
	if summary[0].Keys["status"] == nil {
		t.Fatalf("group key missing: %#v", summary[0])
	}
}

func TestZoneMapPruning(t *testing.T) {
	schema, err := NewSchema(
		Column{Name: "price", Kind: KindFloat64},
	)
	if err != nil {
		t.Fatalf("new schema: %v", err)
	}
	store := NewStore(schema)
	if err := store.Append([]Row{
		{"price": 12.0},
		{"price": 15.0},
	}); err != nil {
		t.Fatalf("append first: %v", err)
	}
	if err := store.Append([]Row{
		{"price": 90.0},
		{"price": 99.0},
	}); err != nil {
		t.Fatalf("append second: %v", err)
	}
	filter := BetweenFloat64("price", 80, 100)
	rows, err := store.Scan(context.Background(), filter)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
}

func TestSchemaValidation(t *testing.T) {
	if _, err := NewSchema(
		Column{Name: "id", Kind: KindInt64},
		Column{Name: "id", Kind: KindText},
	); err == nil {
		t.Fatal("expected duplicate column error")
	}
}
