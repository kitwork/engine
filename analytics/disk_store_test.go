package analytics

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDiskStoreReopen(t *testing.T) {
	schema, err := NewSchema(
		Column{Name: "tenant", Kind: KindText},
		Column{Name: "price", Kind: KindFloat64},
		Column{Name: "active", Kind: KindBool},
	)
	if err != nil {
		t.Fatalf("new schema: %v", err)
	}

	directory := t.TempDir()
	store, err := OpenDiskStore(directory, schema)
	if err != nil {
		t.Fatalf("open disk store: %v", err)
	}
	if err := store.Append([]Row{
		{"tenant": "acme", "price": 12.5, "active": true},
		{"tenant": "acme", "price": 19.0, "active": true},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := OpenDiskStore(directory, schema)
	if err != nil {
		t.Fatalf("reopen disk store: %v", err)
	}
	defer reopened.Close()

	filter, err := Eq("tenant", "acme")
	if err != nil {
		t.Fatalf("eq: %v", err)
	}
	count, err := reopened.Count(context.Background(), filter)
	if err != nil {
		t.Fatalf("count after reopen: %v", err)
	}
	if count != 2 {
		t.Fatalf("count after reopen = %d, want 2", count)
	}
	hits, err := reopened.Scan(context.Background(), filter)
	if err != nil {
		t.Fatalf("scan after reopen: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("scan after reopen = %d, want 2", len(hits))
	}
	if _, err := os.Stat(filepath.Join(directory, manifestFilename)); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
}

func TestDiskStoreSchemaMismatch(t *testing.T) {
	schema, err := NewSchema(Column{Name: "tenant", Kind: KindText})
	if err != nil {
		t.Fatalf("new schema: %v", err)
	}
	directory := t.TempDir()
	store, err := OpenDiskStore(directory, schema)
	if err != nil {
		t.Fatalf("open disk store: %v", err)
	}
	if err := store.Append([]Row{{"tenant": "acme"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	other, err := NewSchema(Column{Name: "tenant", Kind: KindText}, Column{Name: "price", Kind: KindFloat64})
	if err != nil {
		t.Fatalf("new other schema: %v", err)
	}
	if _, err := OpenDiskStore(directory, other); err == nil {
		t.Fatal("expected schema mismatch error")
	}
}
