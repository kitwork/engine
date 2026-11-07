package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// ExecReturning runs a data statement ending with RETURNING in its own savepoint and hands each returned row to the
// check before letting the savepoint go: a check that fails rolls that statement back alone (the request's earlier
// statement stays, and the history records only what was kept), and the statement's error is the check's. It counts
// the rows the statement wrote. A statement that is no data statement is refused.
//
// CONTROL (07/11/2026): with the check's error ignored, the update to -2 was kept and recorded.
func TestExecReturningRollsBackARowItsCheckRefuses(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t, filepath.Join(t.TempDir(), "kitwork.db"))
	if _, err := database.Migrate(ctx, "test", `CREATE TABLE items (id INTEGER PRIMARY KEY, qty INTEGER NOT NULL) STRICT`); err != nil {
		t.Fatal(err)
	}
	refused := errors.New("qty below 0")
	nonNegative := func(rows *sql.Rows) error {
		var qty int64
		if err := rows.Scan(&qty); err != nil {
			return err
		}
		if qty < 0 {
			return refused
		}
		return nil
	}
	var written int64
	var failure error
	if _, err := database.Write(ctx, "test", func(transaction *Transaction) error {
		var err error
		if written, err = transaction.ExecReturning(`INSERT INTO items (qty) VALUES (3), (5) RETURNING qty`, nonNegative); err != nil {
			return err
		}
		_, failure = transaction.ExecReturning(`UPDATE items SET qty = qty - 5 RETURNING qty`, nonNegative)
		if _, err := transaction.ExecReturning(`SELECT 1`, nonNegative); err == nil || !errors.Is(err, ErrStatementRefused) {
			t.Errorf("a read through ExecReturning: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if written != 2 || !errors.Is(failure, refused) {
		t.Fatalf("written %d, the update's error %v", written, failure)
	}
	rows, err := database.readers.QueryContext(ctx, `SELECT group_concat(qty) FROM items`)
	if err != nil {
		t.Fatal(err)
	}
	var kept string
	if rows.Next() {
		_ = rows.Scan(&kept)
	}
	rows.Close()
	if kept != "3,5" {
		t.Errorf("the rows after the refused update: %q, want 3,5", kept)
	}
	changes := 0
	if err := database.Changes(ctx, 0, func(commit Commit) error {
		for _, change := range commit.Changes {
			if change.Table == "items" {
				changes++
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if changes != 2 {
		t.Errorf("the history records %d changes of items, want the two inserts", changes)
	}
}
