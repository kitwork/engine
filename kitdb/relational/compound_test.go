package relational

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnionAllExecutesOnOneSnapshotWithGlobalOrderAndLimit(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "union.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	for _, statement := range []string{
		`CREATE TABLE active_items (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE archived_items (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`INSERT INTO active_items (id, name) VALUES (1, 'one'), (3, 'three')`,
		`INSERT INTO archived_items (id, name) VALUES (2, 'two'), (4, 'four')`,
	} {
		if _, err := engine.Execute(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	query := `
		SELECT id AS item_id, name FROM active_items WHERE id >= $1
		UNION ALL
		SELECT id, name FROM archived_items WHERE id >= $2
		ORDER BY item_id DESC, 2 ASC
		LIMIT 3 OFFSET 1
	`
	columns, err := engine.Describe(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 2 || columns[0].Name != "item_id" || columns[1].Name != "name" {
		t.Fatalf("described columns = %#v", columns)
	}
	result, err := engine.Execute(ctx, query, int64(1), int64(2))
	if err != nil {
		t.Fatal(err)
	}
	if result.CommandTag != "SELECT 3" || len(result.Rows) != 3 ||
		result.Rows[0][0] != int64(3) || result.Rows[1][0] != int64(2) || result.Rows[2][0] != int64(1) {
		t.Fatalf("UNION ALL result = %#v", result)
	}

	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	if _, err := transaction.Execute(ctx, `INSERT INTO active_items (id, name) VALUES (5, 'five')`); err != nil {
		t.Fatal(err)
	}
	visible, err := transaction.Execute(ctx, `
		SELECT id, name FROM active_items
		UNION ALL SELECT id, name FROM archived_items
		ORDER BY id DESC LIMIT 1
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible.Rows) != 1 || visible.Rows[0][0] != int64(5) {
		t.Fatalf("transaction-local UNION ALL = %#v", visible.Rows)
	}
}

func TestUnionAllConstantsValidationAndExplain(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "union-validation.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()

	result, err := engine.Execute(ctx, `SELECT 1 AS value UNION ALL SELECT 3 UNION ALL SELECT 2 ORDER BY 1 DESC`)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 3 || result.Rows[0][0] != int64(3) || result.Rows[2][0] != int64(1) ||
		result.Columns[0].Name != "value" {
		t.Fatalf("constant UNION ALL = %#v", result)
	}

	if _, err := engine.Execute(ctx, `SELECT 1 UNION ALL SELECT 'one'`); err == nil || !strings.Contains(err.Error(), "column 1 has type") {
		t.Fatalf("type mismatch error = %v", err)
	}
	if _, err := engine.Execute(ctx, `SELECT 1 UNION ALL SELECT 1, 2`); err == nil || !strings.Contains(err.Error(), "returns 2 columns") {
		t.Fatalf("column count error = %v", err)
	}
	if _, err := engine.Execute(ctx, `SELECT 1 AS value UNION ALL SELECT 2 ORDER BY missing`); err == nil || !strings.Contains(err.Error(), "no output field") {
		t.Fatalf("ORDER BY binding error = %v", err)
	}

	explained, err := engine.Execute(ctx, `EXPLAIN ANALYZE SELECT 1 AS value UNION ALL SELECT 2 ORDER BY value`)
	if err != nil {
		t.Fatal(err)
	}
	if len(explained.Rows) < 4 || explained.Rows[0][1] != "union all" || explained.Rows[len(explained.Rows)-1][1] != "actual" {
		t.Fatalf("compound EXPLAIN = %#v", explained.Rows)
	}
}

func TestUnionAllEnforcesOneBoundedWorkingSet(t *testing.T) {
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "union-bounded.kitdb"), Options{MaximumResultRows: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE items (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO items (id) VALUES (1), (2), (3)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `SELECT id FROM items UNION ALL SELECT 4`); err == nil || !strings.Contains(err.Error(), "working set exceeds 3 rows") {
		t.Fatalf("working-set error = %v", err)
	}
	limited, err := engine.Execute(ctx, `SELECT id FROM items UNION ALL SELECT 4 LIMIT 2`)
	if err != nil || len(limited.Rows) != 2 {
		t.Fatalf("bounded UNION ALL = %#v, %v", limited.Rows, err)
	}
	if _, err := engine.Execute(ctx, `SELECT id FROM items UNION ALL SELECT 4 LIMIT 2 OFFSET 2`); err == nil ||
		!strings.Contains(err.Error(), "OFFSET plus LIMIT") {
		t.Fatalf("offset working-set error = %v", err)
	}
}
