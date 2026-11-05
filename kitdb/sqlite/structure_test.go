package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// TestStructureIsGivenWhenTheSchemaChanges: Options.Structure is called once the database opens, after a schema
// statement, after a Maintain that changes the schema or kitwork_columns, and after a write of kitwork_columns; never
// after a write of rows only, which leaves the structure as it is.
//
// CONTROL (05/11/2026): with a batch's commit not checking kitwork_columns, "after a write of kitwork_columns: 2
// calls, want 3"; with every commit giving it, "after a write of rows: 4 calls, want 3".
func TestStructureIsGivenWhenTheSchemaChanges(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int64
	database, err := Open(ctx, filepath.Join(t.TempDir(), "kitwork.db"), Options{Structure: func(context.Context, *sql.Conn) { calls.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	want := func(step string, count int64) {
		t.Helper()
		if got := calls.Load(); got != count {
			t.Fatalf("%s: %d calls, want %d", step, got, count)
		}
	}
	want("open", 1)
	if _, err := database.Migrate(ctx, "t", `CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT) STRICT`,
		`CREATE TABLE kitwork_columns (table_name TEXT NOT NULL, column_name TEXT NOT NULL, kind TEXT NOT NULL, precision INTEGER NOT NULL, scale INTEGER NOT NULL, PRIMARY KEY (table_name, column_name))`); err != nil {
		t.Fatal(err)
	}
	want("after a schema statement", 2)
	if _, err := database.Write(ctx, "t", func(transaction *Transaction) error {
		_, err := transaction.Exec(`INSERT INTO kitwork_columns VALUES ('notes', 'body', 'text', 0, 0)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	want("after a write of kitwork_columns", 3)
	if _, err := database.Write(ctx, "t", func(transaction *Transaction) error {
		_, err := transaction.Exec(`INSERT INTO notes (body) VALUES ('a')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	want("after a write of rows", 3)
	if _, err := database.Maintain(ctx, "t", func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `ALTER TABLE notes ADD COLUMN title TEXT`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	want("after a Maintain that moved the schema", 4)
	if _, err := database.Maintain(ctx, "t", func(*sql.Conn) error { return nil }); err != nil {
		t.Fatal(err)
	}
	want("after a Maintain that changed nothing", 4)
}
