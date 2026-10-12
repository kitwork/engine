package relational

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb"
)

type concurrencyObservation struct {
	query string
	rows  [][]any
}

type concurrencyCase struct {
	name        string
	setup       []string
	reads       []concurrencyObservation
	writes      [2]string
	query       string
	want        [2][][]any
	independent bool
}

// Both commit orders must preserve invariants while admitting independent writes.
func TestTransactionConcurrencyConflictMatrix(t *testing.T) {
	cases := []concurrencyCase{
		{
			name:   "same-row-lost-update",
			setup:  []string{"CREATE TABLE items(id INTEGER PRIMARY KEY, value INTEGER)", "INSERT INTO items VALUES(1, 0)"},
			reads:  []concurrencyObservation{{"SELECT value FROM items WHERE id=1", [][]any{{int64(0)}}}},
			writes: [2]string{"UPDATE items SET value=1 WHERE id=1", "UPDATE items SET value=2 WHERE id=1"},
			query:  "SELECT id, value FROM items ORDER BY id",
			want:   [2][][]any{{{int64(1), int64(1)}}, {{int64(1), int64(2)}}},
		},
		{
			name:        "disjoint-row-updates",
			independent: true,
			setup:       []string{"CREATE TABLE items(id INTEGER PRIMARY KEY, value INTEGER)", "INSERT INTO items VALUES(1, 0), (2, 0)"},
			writes:      [2]string{"UPDATE items SET value=1 WHERE id=1", "UPDATE items SET value=2 WHERE id=2"},
			query:       "SELECT id, value FROM items ORDER BY id",
			want:        [2][][]any{{{int64(1), int64(1)}, {int64(2), int64(2)}}, {{int64(1), int64(1)}, {int64(2), int64(2)}}},
		},
		{
			name:   "write-skew",
			setup:  []string{"CREATE TABLE doctors(id INTEGER PRIMARY KEY, active BOOLEAN)", "INSERT INTO doctors VALUES(1, true), (2, true)"},
			reads:  []concurrencyObservation{{"SELECT count(*) FROM doctors WHERE active=true", [][]any{{int64(2)}}}},
			writes: [2]string{"UPDATE doctors SET active=false WHERE id=1", "UPDATE doctors SET active=false WHERE id=2"},
			query:  "SELECT id, active FROM doctors ORDER BY id",
			want:   [2][][]any{{{int64(1), false}, {int64(2), true}}, {{int64(1), true}, {int64(2), false}}},
		},
		{
			name:  "point-read-dependency-cycle",
			setup: []string{"CREATE TABLE doctors(id INTEGER PRIMARY KEY, active BOOLEAN)", "INSERT INTO doctors VALUES(1, true), (2, true)"},
			reads: []concurrencyObservation{
				{"SELECT active FROM doctors WHERE id=1", [][]any{{true}}},
				{"SELECT active FROM doctors WHERE id=2", [][]any{{true}}},
			},
			writes: [2]string{"UPDATE doctors SET active=false WHERE id=1", "UPDATE doctors SET active=false WHERE id=2"},
			query:  "SELECT id, active FROM doctors ORDER BY id",
			want:   [2][][]any{{{int64(1), false}, {int64(2), true}}, {{int64(1), true}, {int64(2), false}}},
		},
		{
			name:   "empty-index-range-phantom",
			setup:  []string{"CREATE TABLE reservations(id INTEGER PRIMARY KEY, slot INTEGER)", "CREATE INDEX slot_idx ON reservations(slot)"},
			reads:  []concurrencyObservation{{"SELECT count(*) FROM reservations WHERE slot >= 10 AND slot < 20", [][]any{{int64(0)}}}},
			writes: [2]string{"INSERT INTO reservations VALUES(1, 11)", "INSERT INTO reservations VALUES(2, 12)"},
			query:  "SELECT id, slot FROM reservations ORDER BY id",
			want:   [2][][]any{{{int64(1), int64(11)}}, {{int64(2), int64(12)}}},
		},
		{
			name:   "index-range-entry-update",
			setup:  []string{"CREATE TABLE reservations(id INTEGER PRIMARY KEY, slot INTEGER)", "CREATE INDEX slot_idx ON reservations(slot)", "INSERT INTO reservations VALUES(1, 5), (2, 25)"},
			reads:  []concurrencyObservation{{"SELECT count(*) FROM reservations WHERE slot >= 10 AND slot < 20", [][]any{{int64(0)}}}},
			writes: [2]string{"UPDATE reservations SET slot=11 WHERE id=1", "UPDATE reservations SET slot=12 WHERE id=2"},
			query:  "SELECT id, slot FROM reservations ORDER BY id",
			want:   [2][][]any{{{int64(1), int64(11)}, {int64(2), int64(25)}}, {{int64(1), int64(5)}, {int64(2), int64(12)}}},
		},
		{
			name:   "absent-key-claim",
			setup:  []string{"CREATE TABLE items(id INTEGER PRIMARY KEY, value INTEGER)"},
			reads:  []concurrencyObservation{{"SELECT id FROM items WHERE id=7", nil}},
			writes: [2]string{"INSERT INTO items VALUES(7, 1)", "INSERT INTO items VALUES(7, 2)"},
			query:  "SELECT id, value FROM items ORDER BY id",
			want:   [2][][]any{{{int64(7), int64(1)}}, {{int64(7), int64(2)}}},
		},
		{
			name:   "unique-claim-different-primary-keys",
			setup:  []string{"CREATE TABLE users(id INTEGER PRIMARY KEY, email TEXT UNIQUE)"},
			writes: [2]string{"INSERT INTO users VALUES(1, 'same@example.test')", "INSERT INTO users VALUES(2, 'same@example.test')"},
			query:  "SELECT id, email FROM users ORDER BY id",
			want:   [2][][]any{{{int64(1), "same@example.test"}}, {{int64(2), "same@example.test"}}},
		},
		{
			name:   "parent-delete-versus-child-insert",
			setup:  []string{"CREATE TABLE parents(id INTEGER PRIMARY KEY)", "CREATE TABLE children(id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parents(id))", "INSERT INTO parents VALUES(1)"},
			writes: [2]string{"DELETE FROM parents WHERE id=1", "INSERT INTO children VALUES(1, 1)"},
			query:  "SELECT count(*) FROM parents UNION ALL SELECT count(*) FROM children",
			want:   [2][][]any{{{int64(0)}, {int64(0)}}, {{int64(1)}, {int64(1)}}},
		},
	}
	for _, scenario := range cases {
		for first := 0; first < 2; first++ {
			order := "left-first"
			if first == 1 {
				order = "right-first"
			}
			t.Run(scenario.name+"/"+order, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "concurrency.kitdb")
				engine := concurrencyOpen(t, path, scenario.setup...)
				ctx := context.Background()
				transactions := [2]*Transaction{concurrencyBegin(t, engine), concurrencyBegin(t, engine)}
				if transactions[0].BaseTransaction() != transactions[1].BaseTransaction() {
					t.Fatal("transactions did not capture the same boundary")
				}
				for i, transaction := range transactions {
					for _, read := range scenario.reads {
						result, err := transaction.Execute(ctx, read.query)
						if err != nil {
							t.Fatal(err)
						}
						concurrencyRowsEqual(t, result.Rows, read.rows)
					}
					if _, err := transaction.Execute(ctx, scenario.writes[i]); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := transactions[first].Commit(ctx); err != nil {
					t.Fatal(err)
				}
				_, err := transactions[1-first].Commit(ctx)
				if scenario.independent {
					if err != nil {
						t.Fatalf("independent writer failed: %v", err)
					}
				} else if !errors.Is(err, ErrTransactionConflict) {
					t.Fatalf("expected conflict, got %v", err)
				}
				concurrencyCheckRows(t, engine, scenario.query, scenario.want[first])
				concurrencyNoSnapshots(t, engine)
				if err := engine.Close(); err != nil {
					t.Fatal(err)
				}
				reopened := concurrencyOpen(t, path)
				concurrencyCheckRows(t, reopened, scenario.query, scenario.want[first])
			})
		}
	}
}

func TestTransactionConcurrencySnapshotAndRollback(t *testing.T) {
	engine := concurrencyOpen(t, filepath.Join(t.TempDir(), "snapshot.kitdb"),
		"CREATE TABLE items(id INTEGER PRIMARY KEY, value INTEGER)", "INSERT INTO items VALUES(1, 10)")
	ctx := context.Background()
	reader := concurrencyBegin(t, engine)
	writer := concurrencyBegin(t, engine)
	if _, err := writer.Execute(ctx, "UPDATE items SET value=20 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	result, err := writer.Execute(ctx, "SELECT value FROM items WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	concurrencyRowsEqual(t, result.Rows, [][]any{{int64(20)}})
	// Neither a concurrent transaction nor an ordinary read sees uncommitted data.
	concurrencyCheckRows(t, engine, "SELECT value FROM items WHERE id=1", [][]any{{int64(10)}})
	for phase := 0; phase < 2; phase++ {
		result, err := reader.Execute(ctx, "SELECT value FROM items WHERE id=1")
		if err != nil {
			t.Fatal(err)
		}
		concurrencyRowsEqual(t, result.Rows, [][]any{{int64(10)}})
		if phase == 0 {
			if _, err := writer.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A transaction with no mutations can finish its older snapshot successfully.
	if _, err := reader.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	rollback := concurrencyBegin(t, engine)
	if _, err := rollback.Execute(ctx, "DELETE FROM items WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err := rollback.Rollback(); err != nil {
		t.Fatal(err)
	}
	concurrencyCheckRows(t, engine, "SELECT value FROM items WHERE id=1", [][]any{{int64(20)}})
	concurrencyNoSnapshots(t, engine)
}

func TestTransactionConcurrencyCatalogFence(t *testing.T) {
	engine := concurrencyOpen(t, filepath.Join(t.TempDir(), "catalog.kitdb"),
		"CREATE TABLE items(id INTEGER PRIMARY KEY, value INTEGER)", "INSERT INTO items VALUES(1, 10)")
	transaction := concurrencyBegin(t, engine)
	ctx := context.Background()
	if _, err := transaction.Execute(ctx, "UPDATE items SET value=20 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	// DDL on the same facade waits for its active transaction read leases.
	// A second facade represents another frontend over the same owned kernel.
	other, err := Attach(engine.database, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	if _, err := other.Execute(ctx, "CREATE INDEX value_idx ON items(value)"); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Commit(ctx); !errors.Is(err, ErrTransactionConflict) || !errors.Is(err, kitdb.ErrTransactionConflict) {
		t.Fatalf("catalog conflict = %v", err)
	}
	concurrencyCheckRows(t, engine, "SELECT value FROM items WHERE id=1", [][]any{{int64(10)}})
	concurrencyNoSnapshots(t, engine)
}

func TestTransactionConcurrencySimultaneousCommitIsAtomic(t *testing.T) {
	engine := concurrencyOpen(t, filepath.Join(t.TempDir(), "atomic.kitdb"),
		"CREATE TABLE items(id INTEGER PRIMARY KEY, value INTEGER)", "INSERT INTO items VALUES(1, 0), (2, 0)",
		"CREATE TABLE audit(id INTEGER PRIMARY KEY, writer INTEGER)")
	ctx := context.Background()
	transactions := [2]*Transaction{concurrencyBegin(t, engine), concurrencyBegin(t, engine)}
	for i, transaction := range transactions {
		if _, err := transaction.Execute(ctx, "UPDATE items SET value=$1", int64(i+1)); err != nil {
			t.Fatal(err)
		}
		if _, err := transaction.Execute(ctx, "INSERT INTO audit VALUES($1, $1)", int64(i+1)); err != nil {
			t.Fatal(err)
		}
	}
	type outcome struct {
		writer int
		err    error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	for i, transaction := range transactions {
		go func() { <-start; _, err := transaction.Commit(ctx); results <- outcome{i + 1, err} }()
	}
	close(start)
	winner, conflicts := 0, 0
	for range 2 {
		select {
		case result := <-results:
			if result.err == nil {
				winner = result.writer
			} else if errors.Is(result.err, ErrTransactionConflict) {
				conflicts++
			} else {
				t.Fatal(result.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent commit did not finish")
		}
	}
	if winner == 0 || conflicts != 1 {
		t.Fatalf("winner=%d conflicts=%d", winner, conflicts)
	}
	concurrencyCheckRows(t, engine, "SELECT value FROM items ORDER BY id", [][]any{{int64(winner)}, {int64(winner)}})
	concurrencyCheckRows(t, engine, "SELECT id, writer FROM audit", [][]any{{int64(winner), int64(winner)}})
	concurrencyNoSnapshots(t, engine)
}

func concurrencyOpen(t *testing.T, path string, setup ...string) *Engine {
	t.Helper()
	engine, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	for _, statement := range setup {
		if _, err := engine.Execute(context.Background(), statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	return engine
}

func concurrencyBegin(t *testing.T, engine *Engine) *Transaction {
	t.Helper()
	transaction, err := engine.BeginTransaction(context.Background(), TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transaction.Rollback() })
	return transaction
}

func concurrencyCheckRows(t *testing.T, engine *Engine, query string, want [][]any) {
	t.Helper()
	result, err := engine.Execute(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	concurrencyRowsEqual(t, result.Rows, want)
}

func concurrencyRowsEqual(t *testing.T, got, want [][]any) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows=%#v want=%#v", got, want)
	}
}

func concurrencyNoSnapshots(t *testing.T, engine *Engine) {
	t.Helper()
	stats, err := engine.database.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.ActiveSnapshots != 0 {
		t.Fatalf("retained snapshots=%d", stats.ActiveSnapshots)
	}
}
