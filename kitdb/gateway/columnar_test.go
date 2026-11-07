package gateway

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/kitdb/analytics"
	kitdb "github.com/kitwork/engine/kitdb/sqlite"
)

// TestColumnarIsDeclared: a table's columns copy is the database's to declare (KITWORK_KITDB.md câu 13.11 (c),
// 07/11/2026). Without CREATE INDEX … USING columnar no copy is built and SQLite answers every statement; with one,
// the copy answers a statement that reads only its columns (count(*) too), and SQLite one that reads another column;
// a column renamed stays in the copy under its new name; DROP INDEX takes the copy out; the structure file says the
// copy, and a file made from it has it. Rows written while no copy is declared change with no trace in the history; a
// copy declared again is built again rather than answering from the copy of before.
//
// CONTROL (07/11/2026): with the planner reading every table as before (no declaration asked), "with no columns copy
// declared the copy answered 1 statements"; with a rename not written in kitwork_columnar, "after RENAME COLUMN price
// TO amount: the copy answered 0 statements, want 1"; with the structure not writing the copy, "the structure file …
// lacks CREATE INDEX sales_columnar ON sales USING columnar (merchant, amount)"; with the copy taking no notice of a
// Tracked change, "declared again after writes with no trace: SELECT merchant, count(*), sum(amount) … differing".
func TestColumnarIsDeclared(t *testing.T) {
	path, database := salesFile(t, 3000, false)
	registry := analytics.NewRegistry()
	registry.MinimumRows = 1
	t.Cleanup(func() { _ = registry.Close() })
	access := newAccess("public.sales")
	access.admin = true
	session, err := Open(context.Background(), Target{Name: "shop.example/application", Schemas: map[string]string{"public": path}},
		access, fakeColumns{}, Options{Analytics: registry, Writers: writers{path: database}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	without := salesSession(t, path, nil)
	// answered runs query twice, around the registry's builds, and gives how many times the copy answered the second,
	// after comparing both with SQLite's answer
	answered := func(label, query string) int64 {
		t.Helper()
		want := mustRun(t, without, query)
		if got := mustRun(t, session, query); !sameResult(got, want) {
			t.Fatalf("%s: %s: %q, SQLite %q", label, query, cells(got), cells(want))
		}
		registry.Wait()
		before := analyticAnswers.Load()
		if got := mustRun(t, session, query); !sameResult(got, want) {
			t.Fatalf("%s: %s after the build: %q, SQLite %q", label, query, cells(got), cells(want))
		}
		return analyticAnswers.Load() - before
	}
	grouped := `SELECT merchant, count(*), sum(price) FROM sales GROUP BY merchant`

	if tag := run(t, session, `DROP INDEX sales_columnar`); tag != "DROP INDEX" {
		t.Fatalf("tag %q", tag)
	}
	if n := answered("with no columns copy declared", grouped); n != 0 {
		t.Fatalf("with no columns copy declared the copy answered %d statements", n)
	}

	if tag := run(t, session, `CREATE INDEX sales_columnar ON sales USING columnar (merchant, price)`); tag != "CREATE INDEX" {
		t.Fatalf("tag %q", tag)
	}
	if n := answered("declared", grouped); n != 1 {
		t.Fatalf("declared: the copy answered %d statements, want 1", n)
	}
	if n := answered("count(*) of a declared table", `SELECT count(*) FROM sales`); n != 1 {
		t.Fatalf("count(*) of a declared table: the copy answered %d statements, want 1", n)
	}
	if n := answered("a column the copy does not hold", `SELECT merchant, count(note) FROM sales GROUP BY merchant`); n != 0 {
		t.Fatalf("a column the copy does not hold: the copy answered %d statements", n)
	}

	run(t, session, `ALTER TABLE sales RENAME COLUMN price TO amount`)
	renamed := `SELECT merchant, count(*), sum(amount) FROM sales GROUP BY merchant`
	if n := answered("after RENAME COLUMN price TO amount", renamed); n != 1 {
		t.Fatalf("after RENAME COLUMN price TO amount: the copy answered %d statements, want 1", n)
	}

	// the declaration taken out and written again in kitwork_columnar, with no schema statement: in between the history
	// does not follow sales and its rows change with no trace; the copy of before, still in the copies' file and of the
	// same columns, must not answer once the copy is declared again (the writer's Tracked change makes it stale)
	setColumnar := func(columns ...string) {
		t.Helper()
		if _, err := database.Write(context.Background(), "t", func(tx *kitdb.Transaction) error {
			if err := writeColumnar(tx, kindChange{table: "sales", uncolumnar: true}); err != nil {
				return err
			}
			for _, column := range columns {
				if err := writeColumnar(tx, kindChange{table: "sales", column: column, columnar: true}); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	setColumnar()
	before := database.Revision()
	if _, err := database.Write(context.Background(), "t", func(tx *kitdb.Transaction) error {
		if _, err := tx.Exec(`UPDATE sales SET amount = amount + 1000 WHERE id % 3 = 0`); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM sales WHERE id % 11 = 0`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if after := database.Revision(); after != before {
		t.Fatalf("writes to sales with no copy declared committed revision %d", after)
	}
	setColumnar("merchant", "amount")
	if n := answered("declared again after writes with no trace", renamed); n != 1 {
		t.Fatalf("declared again after writes with no trace: the copy answered %d statements, want 1", n)
	}

	reader, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	structure, err := Structure(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}
	want := "CREATE INDEX sales_columnar ON sales USING columnar (merchant, amount);"
	if !strings.Contains(structure, want) {
		t.Fatalf("the structure file:\n%s\nlacks %s", structure, want)
	}
	copyPath := filepath.Join(t.TempDir(), "kitwork.db")
	copied, err := kitdb.Open(context.Background(), copyPath, kitdb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = copied.Close() })
	if err := LoadStructure(context.Background(), copied, "boot", structure); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, copied, `SELECT group_concat(column_name, ',') FROM (SELECT column_name FROM kitwork_columnar WHERE table_name = 'sales' ORDER BY column_name)`); got != "amount,merchant" {
		t.Fatalf("the file made from the structure has the copy of %v, want amount,merchant", got)
	}

	refused(t, session, `CREATE INDEX sales_columnar ON sales USING columnar (nope)`, "42703", `column "nope"`)
	refused(t, session, `CREATE INDEX sales_columnar ON sales (merchant)`, "0A000", "columns copy")
	refused(t, session, `CREATE INDEX other ON sales USING columnar (merchant)`, "0A000", "named sales_columnar")
	refused(t, session, `CREATE UNIQUE INDEX sales_columnar ON sales USING columnar (merchant)`, "0A000", "not UNIQUE")

	if tag := run(t, session, `DROP INDEX sales_columnar`); tag != "DROP INDEX" {
		t.Fatalf("tag %q", tag)
	}
	if n := answered("after DROP INDEX", renamed); n != 0 {
		t.Fatalf("after DROP INDEX the copy answered %d statements", n)
	}

}
