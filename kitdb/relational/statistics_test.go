package relational

import (
	"context"
	"path/filepath"
	"testing"
)

func TestStandaloneDurableRowCountAndAnalyze(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "statistics.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE events (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	assertTableCount(t, engine, 0, true)
	if _, err := engine.Execute(ctx, `
		INSERT INTO events (id, name) VALUES (1, 'one'), (2, 'two'), (3, 'three')
	`); err != nil {
		t.Fatal(err)
	}
	assertTableCount(t, engine, 3, true)
	if _, err := engine.Execute(ctx, `DELETE FROM events WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	assertTableCount(t, engine, 2, true)

	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Execute(ctx, `INSERT INTO events (id, name) VALUES (4, 'four')`); err != nil {
		t.Fatal(err)
	}
	tables, err := transaction.tables()
	if err != nil || len(tables) != 1 || tables[0].Rows != 3 || !tables[0].Analyzed {
		t.Fatalf("transaction table statistics = %#v, %v", tables, err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertTableCount(t, engine, 2, true)

	engine.mu.Lock()
	schema, err := engine.schemaLocked("events")
	if err != nil {
		engine.mu.Unlock()
		t.Fatal(err)
	}
	key, err := tableCountKey(schema)
	if err != nil {
		engine.mu.Unlock()
		t.Fatal(err)
	}
	kernel, err := engine.database.Begin()
	if err == nil {
		err = kernel.Delete(key)
	}
	if err == nil {
		_, err = kernel.Commit()
	}
	engine.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	assertTableCount(t, engine, 0, false)

	counted, err := engine.Execute(ctx, `SELECT COUNT(*) FROM events`)
	if err != nil || len(counted.Rows) != 1 || counted.Rows[0][0] != int64(2) {
		t.Fatalf("legacy fallback count = %#v, %v", counted.Rows, err)
	}
	if _, err := engine.Execute(ctx, `ANALYZE events`); err != nil {
		t.Fatal(err)
	}
	assertTableCount(t, engine, 2, true)
}

func assertTableCount(t *testing.T, engine *Engine, count uint64, analyzed bool) {
	t.Helper()
	tables, err := engine.Tables()
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || tables[0].Rows != count || tables[0].Analyzed != analyzed {
		t.Fatalf("table statistics = %#v, want rows=%d analyzed=%v", tables, count, analyzed)
	}
}
