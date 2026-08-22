package analytics

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kitwork/engine/kitdb"
)

func TestKitDBProjectionSeedingAndCommitFeed(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpenKitDB(t, dbPath)
	defer db.Close()

	commitKitDBValue(t, db, "product/1", "red shirt|12.50|1")
	commitKitDBValue(t, db, "product/2", "blue hat|9.25|0")

	schema, err := NewSchema(
		Column{Name: "sku", Kind: KindText},
		Column{Name: "title", Kind: KindText},
		Column{Name: "price", Kind: KindFloat64},
		Column{Name: "active", Kind: KindBool},
	)
	if err != nil {
		t.Fatalf("new schema: %v", err)
	}

	projectionDir := t.TempDir()
	projection, err := OpenKitDBProjection(projectionDir, schema, db, kitdbRowDecoder)
	if err != nil {
		t.Fatalf("open projection: %v", err)
	}
	defer projection.Close()

	if projection.Err() != nil {
		t.Fatalf("projection error after seed: %v", projection.Err())
	}
	count, err := projection.Count(context.Background())
	if err != nil {
		t.Fatalf("projection count: %v", err)
	}
	if count != 2 {
		t.Fatalf("projection count = %d, want 2", count)
	}

	commitKitDBValue(t, db, "product/1", "red shirt premium|14.00|1")
	commitKitDBDelete(t, db, "product/2")

	filter, err := Eq("sku", "product/1")
	if err != nil {
		t.Fatalf("eq: %v", err)
	}
	rows, err := projection.Scan(context.Background(), filter)
	if err != nil {
		t.Fatalf("projection scan: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("projection scan rows = %d, want 1", len(rows))
	}
	if rows[0]["title"].(string) != "red shirt premium" {
		t.Fatalf("updated projection title = %q", rows[0]["title"])
	}
	if rows[0]["price"].(float64) != 14.0 {
		t.Fatalf("updated projection price = %v", rows[0]["price"])
	}

	count, err = projection.Count(context.Background())
	if err != nil {
		t.Fatalf("projection count after feed: %v", err)
	}
	if count != 1 {
		t.Fatalf("projection count after feed = %d, want 1", count)
	}
}

func kitdbRowDecoder(key, value []byte) (string, Row, bool, error) {
	sku := string(key)
	if len(value) == 0 {
		return sku, nil, true, nil
	}
	parts := strings.SplitN(string(value), "|", 3)
	if len(parts) != 3 {
		return "", nil, false, fmt.Errorf("invalid encoded value: %q", value)
	}
	price, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return "", nil, false, err
	}
	row := Row{
		"sku":    sku,
		"title":  parts[0],
		"price":  price,
		"active": parts[2] == "1",
	}
	return sku, row, true, nil
}

func mustOpenKitDB(t *testing.T, path string) *kitdb.DB {
	t.Helper()
	db, err := kitdb.Open(path)
	if err != nil {
		t.Fatalf("kitdb.Open(%q): %v", path, err)
	}
	return db
}

func commitKitDBValue(t *testing.T, db *kitdb.DB, key, value string) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := tx.Put([]byte(key), []byte(value)); err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatalf("Commit(%q): %v", key, err)
	}
}

func commitKitDBDelete(t *testing.T, db *kitdb.DB, key string) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := tx.Delete([]byte(key)); err != nil {
		t.Fatalf("Delete(%q): %v", key, err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatalf("Commit delete(%q): %v", key, err)
	}
}
