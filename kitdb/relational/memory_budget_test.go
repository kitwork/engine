package relational

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/kitdb"
)

func TestRelationalMemoryBudgetSavepointAndFacades(t *testing.T) {
	kernel, err := kitdb.OpenWithOptions(filepath.Join(t.TempDir(), "memory.kitdb"), kitdb.OpenOptions{TransactionMemoryBytes: 8192})
	if err != nil {
		t.Fatal(err)
	}
	defer kernel.Close()
	first, err := Attach(kernel, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Attach(kernel, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	a, err := first.BeginTransaction(context.Background(), TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Rollback()
	b, err := second.BeginTransaction(context.Background(), TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Rollback()
	point, err := a.Savepoint()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Put([]byte("one"), make([]byte, 6000)); err != nil {
		t.Fatal(err)
	}
	if err := b.Put([]byte("two"), make([]byte, 3000)); !errors.Is(err, kitdb.ErrActiveTransactionMemory) {
		t.Fatalf("shared facade budget = %v", err)
	}
	if len(b.operations) != 0 {
		t.Fatal("rejected write allocated operations")
	}
	if err := a.RollbackTo(point); err != nil {
		t.Fatal(err)
	}
	if err := b.Put([]byte("two"), make([]byte, 3000)); err != nil {
		t.Fatal(err)
	}
	_ = a.Rollback()
	_ = b.Rollback()
	stats, _ := kernel.Stats()
	if stats.FrontendTransactionBytes != 0 || stats.SnapshotBytes != 0 {
		t.Fatalf("rollback leaked = %+v", stats)
	}
	ctx := context.Background()
	if _, err := first.Execute(ctx, `CREATE TABLE items(id BIGINT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Execute(ctx, `INSERT INTO items VALUES (1, 'old')`); err != nil {
		t.Fatal(err)
	}
	writer, err := first.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Execute(ctx, `UPDATE items SET value=$1 WHERE id=1`, strings.Repeat("x", 6000)); err != nil {
		t.Fatal(err)
	}
	// Preparing the kernel write also needs capacity while the frontend copy
	// remains alive. A failed commit must not publish the staged value.
	if _, err := writer.Commit(ctx); !errors.Is(err, kitdb.ErrActiveTransactionMemory) {
		t.Fatalf("commit memory rejection = %v", err)
	}
	rows, err := first.Execute(ctx, `SELECT value FROM items WHERE id=1`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 1 || rows.Rows[0][0] != "old" {
		t.Fatalf("failed commit published: %+v", rows)
	}
	if _, err := first.Execute(ctx, `UPDATE items SET value='new' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	stats, _ = kernel.Stats()
	if stats.FrontendTransactionBytes != 0 || stats.ActiveTransactionBytes != 0 || stats.SnapshotBytes != 0 {
		t.Fatalf("commit cleanup = %+v", stats)
	}
}
