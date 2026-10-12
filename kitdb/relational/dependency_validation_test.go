package relational

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func dependencyValidationEngine(t *testing.T) *Engine {
	t.Helper()
	return concurrencyOpen(t, filepath.Join(t.TempDir(), "dependencies.kitdb"),
		"CREATE TABLE source(id INTEGER PRIMARY KEY, value INTEGER UNIQUE)",
		"CREATE INDEX source_value ON source(value)",
		"CREATE TABLE sink(id INTEGER PRIMARY KEY, value INTEGER)",
		"INSERT INTO source VALUES(1, 1), (2, 2)",
		"INSERT INTO sink VALUES(1, 0)")
}

func TestSQLDependencyValidationReads(t *testing.T) {
	for _, test := range []struct {
		name, read, change string
		coarse, conflict   bool
	}{
		{"point", "SELECT value FROM source WHERE id=1", "UPDATE source SET value=3 WHERE id=1", false, true},
		{"point-miss", "SELECT value FROM source WHERE id=3", "INSERT INTO source VALUES(3, 3)", false, true},
		{"unique-miss", "SELECT id FROM source WHERE value=3", "INSERT INTO source VALUES(3, 3)", false, true},
		{"empty-range", "SELECT id FROM source WHERE value>=10 AND value<20", "INSERT INTO source VALUES(3, 12)", false, true},
		{"range-lower-inclusive", "SELECT id FROM source WHERE value>=10 AND value<20", "INSERT INTO source VALUES(3, 10)", false, true},
		{"range-upper-exclusive", "SELECT id FROM source WHERE value>=10 AND value<20", "INSERT INTO source VALUES(3, 20)", false, false},
		{"range-entry-update", "SELECT id FROM source WHERE value>=10 AND value<20", "UPDATE source SET value=12 WHERE id=2", false, true},
		{"limit-range", "SELECT id FROM source WHERE value>=1 ORDER BY value LIMIT 1", "INSERT INTO source VALUES(3, 3)", false, true},
		{"point-independent", "SELECT value FROM source WHERE id=1", "UPDATE source SET value=3 WHERE id=2", false, false},
		{"aggregate-fallback", "SELECT sum(value) FROM source", "INSERT INTO sink VALUES(2, 0)", true, true},
		{"join-fallback", "SELECT source.id FROM source JOIN sink ON source.id=sink.id", "INSERT INTO sink VALUES(2, 0)", true, true},
		{"pragma-fallback", "PRAGMA cache_status", "INSERT INTO sink VALUES(2, 0)", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine := dependencyValidationEngine(t)
			tx := concurrencyBegin(t, engine)
			ctx := context.Background()
			if _, err := tx.Execute(ctx, test.read); err != nil {
				t.Fatal(err)
			}
			if tx.dependencies.coarse != test.coarse {
				t.Fatalf("coarse=%v want=%v", tx.dependencies.coarse, test.coarse)
			}
			if _, err := tx.Execute(ctx, "UPDATE sink SET value=1 WHERE id=1"); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.Execute(ctx, test.change); err != nil {
				t.Fatal(err)
			}
			_, err := tx.Commit(ctx)
			if test.conflict {
				if !errors.Is(err, ErrTransactionConflict) {
					t.Fatalf("expected conflict: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			want := int64(1)
			if test.conflict {
				want = 0
			}
			concurrencyCheckRows(t, engine, "SELECT value FROM sink WHERE id=1", [][]any{{want}})
			concurrencyNoSnapshots(t, engine)
			assertDependenciesReleased(t, tx)
		})
	}
}

func TestSQLDependencyValidationRollbackEvidence(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint("failed-statement=", failed), func(t *testing.T) {
			engine := dependencyValidationEngine(t)
			tx := concurrencyBegin(t, engine)
			ctx := context.Background()
			point, err := tx.Savepoint()
			if err != nil {
				t.Fatal(err)
			}
			if failed {
				if _, err := tx.Execute(ctx, "INSERT INTO source VALUES(3, 1)"); err == nil {
					t.Fatal("duplicate unique accepted")
				}
			} else {
				if _, err := tx.Execute(ctx, "SELECT value FROM source WHERE id=1"); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.RollbackTo(point); err != nil {
				t.Fatal(err)
			}
			if len(tx.operations) != 0 {
				t.Fatal("rollback retained mutations")
			}
			if _, err := tx.Execute(ctx, "UPDATE sink SET value=1 WHERE id=1"); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.Execute(ctx, "DELETE FROM source WHERE id=1"); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Commit(ctx); !errors.Is(err, ErrTransactionConflict) {
				t.Fatalf("lost rollback evidence: %v", err)
			}
			concurrencyCheckRows(t, engine, "SELECT value FROM sink WHERE id=1", [][]any{{int64(0)}})
			concurrencyNoSnapshots(t, engine)
		})
	}
}

func TestSQLDependencyValidationKernelWriters(t *testing.T) {
	for _, mode := range []string{"independent", "overlap", "aba", "retention"} {
		t.Run(mode, func(t *testing.T) {
			engine := dependencyValidationEngine(t)
			tx := concurrencyBegin(t, engine)
			ctx := context.Background()
			if _, err := tx.Execute(ctx, "SELECT value FROM source WHERE id=1"); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Execute(ctx, "UPDATE sink SET value=1 WHERE id=1"); err != nil {
				t.Fatal(err)
			}
			schema, err := tx.schema("source")
			if err != nil {
				t.Fatal(err)
			}
			key, err := rowKey(schema, map[string]any{"id": int64(1)}, 0)
			if err != nil {
				t.Fatal(err)
			}
			original, found, err := tx.Get(key)
			if err != nil || !found {
				t.Fatalf("row missing: %v", err)
			}
			count := 1
			if mode == "aba" {
				count = 2
			}
			if mode == "retention" {
				count = 513
			}
			for i := 0; i < count; i++ {
				writer, err := engine.database.Begin()
				if err != nil {
					t.Fatal(err)
				}
				if mode == "aba" && i == 0 {
					err = writer.Delete(key)
				} else if mode == "overlap" || mode == "aba" {
					err = writer.Put(key, original)
				} else {
					err = writer.Put([]byte("unrelated/kernel"), []byte(fmt.Sprint(i)))
				}
				if err != nil {
					_ = writer.Rollback()
					t.Fatal(err)
				}
				if _, err := writer.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			_, err = tx.Commit(ctx)
			if mode == "independent" {
				if err != nil {
					t.Fatalf("unrelated kernel writer: %v", err)
				}
			} else if !errors.Is(err, ErrTransactionConflict) {
				t.Fatalf("expected conflict: %v", err)
			}
			concurrencyNoSnapshots(t, engine)
		})
	}
}

func TestSQLDependencyValidationIndependentFacades(t *testing.T) {
	engine := dependencyValidationEngine(t)
	other, err := Attach(engine.database, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	ctx := context.Background()
	transactions := []*Transaction{concurrencyBegin(t, engine), concurrencyBegin(t, other)}
	for i, tx := range transactions {
		if _, err := tx.Execute(ctx, "UPDATE source SET value=$1 WHERE id=$2", int64(10+i), int64(1+i)); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, tx := range transactions {
		go func(tx *Transaction) { <-start; _, err := tx.Commit(ctx); results <- err }(tx)
	}
	close(start)
	for range transactions {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	concurrencyCheckRows(t, engine, "SELECT id, value FROM source ORDER BY id", [][]any{{int64(1), int64(10)}, {int64(2), int64(11)}})
	concurrencyNoSnapshots(t, engine)
}

func TestSQLDependencyValidationTriggerFallback(t *testing.T) {
	engine := dependencyValidationEngine(t)
	ctx := context.Background()
	if _, err := engine.Execute(ctx, "CREATE TABLE audit(id INTEGER PRIMARY KEY, value INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, "CREATE TRIGGER sink_audit AFTER UPDATE ON sink FOR EACH ROW INSERT INTO audit (id, value) VALUES(NEW.id, NEW.value)"); err != nil {
		t.Fatal(err)
	}
	tx := concurrencyBegin(t, engine)
	if _, err := tx.Execute(ctx, "UPDATE sink SET value=1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if !tx.dependencies.coarse {
		t.Fatal("trigger did not select conservative validation")
	}
	if _, err := engine.Execute(ctx, "UPDATE source SET value=8 WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(ctx); !errors.Is(err, ErrTransactionConflict) {
		t.Fatalf("trigger fallback: %v", err)
	}
	concurrencyCheckRows(t, engine, "SELECT value FROM sink WHERE id=1", [][]any{{int64(0)}})
	concurrencyCheckRows(t, engine, "SELECT value FROM source WHERE id=1", [][]any{{int64(1)}})
	concurrencyCheckRows(t, engine, "SELECT id FROM audit", nil)
	concurrencyNoSnapshots(t, engine)
}

func TestSQLDependencyValidationNativeAdapter(t *testing.T) {
	for _, dependent := range []bool{false, true} {
		t.Run(fmt.Sprint("dependent=", dependent), func(t *testing.T) {
			engine := dependencyValidationEngine(t)
			database, err := OpenNativeSQL(engine)
			if err != nil {
				t.Fatal(err)
			}
			database.SetMaxOpenConns(2)
			t.Cleanup(func() { _ = database.Close() })
			ctx := context.Background()
			left, err := database.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer left.Rollback()
			right, err := database.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer right.Rollback()
			if dependent {
				var value int64
				if err := right.QueryRowContext(ctx, "SELECT value FROM source WHERE id=1").Scan(&value); err != nil {
					t.Fatal(err)
				}
				if value != 1 {
					t.Fatal(value)
				}
			}
			if _, err := left.ExecContext(ctx, "UPDATE source SET value=10 WHERE id=1"); err != nil {
				t.Fatal(err)
			}
			if _, err := right.ExecContext(ctx, "UPDATE source SET value=11 WHERE id=2"); err != nil {
				t.Fatal(err)
			}
			if err := left.Commit(); err != nil {
				t.Fatal(err)
			}
			err = right.Commit()
			if dependent {
				if !errors.Is(err, ErrTransactionConflict) {
					t.Fatalf("native lost dependency: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			want := int64(11)
			if dependent {
				want = 2
			}
			concurrencyCheckRows(t, engine, "SELECT id, value FROM source ORDER BY id", [][]any{{int64(1), int64(10)}, {int64(2), want}})
			concurrencyNoSnapshots(t, engine)
		})
	}
}

func BenchmarkSQLDependencyValidation(b *testing.B) {
	for _, coarse := range []bool{false, true} {
		b.Run(fmt.Sprint("coarse=", coarse), func(b *testing.B) {
			engine, err := Open(filepath.Join(b.TempDir(), "bench.kitdb"))
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = engine.Close() })
			ctx := context.Background()
			for _, query := range []string{"CREATE TABLE items(id INTEGER PRIMARY KEY, value INTEGER)", "INSERT INTO items VALUES(1, 0)"} {
				if _, err := engine.Execute(ctx, query); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tx, err := engine.BeginTransaction(ctx, TransactionOptions{})
				if err != nil {
					b.Fatal(err)
				}
				if coarse {
					tx.requireCoarseValidation()
				}
				if _, err := tx.Execute(ctx, "UPDATE items SET value=$1 WHERE id=1", int64(i)); err != nil {
					_ = tx.Rollback()
					b.Fatal(err)
				}
				if _, err := tx.Commit(ctx); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
