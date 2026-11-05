package gateway

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/kitdb/analytics"
	"github.com/kitwork/engine/kitdb/pgwire"
	kitdb "github.com/kitwork/engine/kitdb/sqlite"
)

// salesFile is a KitDB file of sales written through the writer (so it has a history), and a writer to add rows; an
// index on category when indexed.
func salesFile(t *testing.T, rows int, indexed bool) (string, *kitdb.Database) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "application", "kitwork.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	database, err := kitdb.Open(ctx, path, kitdb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	schema := []string{`CREATE TABLE sales (id INTEGER PRIMARY KEY, merchant TEXT NOT NULL, category INTEGER, price INTEGER, rating REAL, note TEXT)`}
	if indexed {
		schema = append(schema, `CREATE INDEX sales_category ON sales (category)`)
	}
	if _, err := database.Migrate(ctx, "boot", schema...); err != nil {
		t.Fatal(err)
	}
	random := rand.New(rand.NewPCG(1, 2))
	if _, err := database.Write(ctx, "t", func(tx *kitdb.Transaction) error {
		for id := 1; id <= rows; id++ {
			var category, rating, note any
			if random.IntN(10) > 0 {
				category = int64(random.IntN(40))
			}
			if random.IntN(5) > 0 {
				rating = float64(random.IntN(50)) / 10
			}
			if random.IntN(3) == 0 {
				note = fmt.Sprintf("n%d", random.IntN(7))
			}
			if _, err := tx.Exec(`INSERT INTO sales VALUES (?, ?, ?, ?, ?, ?)`, id, []string{"shopee", "lazada", "tiki"}[random.IntN(3)],
				category, random.Int64N(1_000_000), rating, note); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return path, database
}

func salesSession(t *testing.T, path string, registry *analytics.Registry) *Session {
	t.Helper()
	session, err := Open(context.Background(), Target{Name: "shop.example/application", Schemas: map[string]string{"public": path}},
		newAccess("public.sales"), fakeColumns{}, Options{Analytics: registry})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// sameResult compares two results: their columns' names and types, then every field.
func sameResult(a, b pgwire.Result) bool {
	if len(a.Columns) != len(b.Columns) || len(a.Rows) != len(b.Rows) || a.CommandTag != b.CommandTag {
		return false
	}
	for index := range a.Columns {
		if a.Columns[index].Name != b.Columns[index].Name || a.Columns[index].DataTypeOID != b.Columns[index].DataTypeOID {
			return false
		}
	}
	return cells(a) == cells(b)
}

var salesQueries = []struct {
	text       string
	parameters []pgwire.Parameter
}{
	{`SELECT merchant, count(*), sum(price), avg(rating), min(note), max(price) FROM sales GROUP BY merchant`, nil},
	{`SELECT merchant AS m, count(*) AS n FROM public.sales s GROUP BY s.merchant ORDER BY n DESC, m LIMIT 2`, nil},
	{`SELECT note, sum(price) total FROM sales WHERE price BETWEEN 1000 AND 600000 AND (rating >= 2.5 OR rating IS NULL) GROUP BY note ORDER BY 2 DESC`, nil},
	{`SELECT count(*), sum(price), min(rating), max(rating) FROM sales WHERE merchant IN ('shopee', 'tiki') AND NOT category < 10`, nil},
	{`SELECT "merchant", COUNT("price") FROM "sales" WHERE note IS NOT NULL AND merchant <> 'lazada' GROUP BY "merchant" ORDER BY COUNT("price")`, nil},
	{`SELECT count(*) FROM sales WHERE price > ?1 AND merchant = ?2`, []pgwire.Parameter{{Format: 1, OID: pgwire.OIDInt8, Data: []byte{0, 0, 0, 0, 0, 7, 161, 32}}, {Data: []byte("tiki")}}},
	{`SELECT category, avg(price) FROM sales WHERE category NOT IN (1, 2, 3) GROUP BY category ORDER BY category DESC LIMIT 5 OFFSET 3`, nil},
}

// TestAnalyticAnswersAsSQLite: a statement SQLite would scan its table for is answered by the column copy once the
// registry built it in the background (every column asked for so far), with the same columns, types and values a
// session without the copy gives, and still after rows were written; until it is built SQLite answers, the same.
//
// CONTROL (05/11/2026): with ORDER BY not applied, "from the copy: SELECT note, sum(price) total … ORDER BY 2 DESC: …"
// differing; with OFFSET not applied, and with NOT IN read as IN, "from the copy: SELECT category, avg(price) … NOT IN
// (1, 2, 3) … OFFSET 3: …" differing; with no build after one that ended while more columns were asked for, "from
// the copy: the copy answered 5 statements, want 7".
func TestAnalyticAnswersAsSQLite(t *testing.T) {
	path, database := salesFile(t, 5000, false)
	registry := analytics.NewRegistry()
	registry.MinimumRows = 1
	t.Cleanup(func() { _ = registry.Close() })
	with, without := salesSession(t, path, registry), salesSession(t, path, nil)
	check := func(label string, wantAnswers int64) {
		t.Helper()
		before := analyticAnswers.Load()
		for _, query := range salesQueries {
			got, err := with.Execute(context.Background(), query.text, query.parameters)
			if err != nil {
				t.Fatalf("%s: %s: %v", label, query.text, err)
			}
			want, err := without.Execute(context.Background(), query.text, query.parameters)
			if err != nil {
				t.Fatalf("%s: %s without the copy: %v", label, query.text, err)
			}
			if !sameResult(got, want) {
				t.Fatalf("%s: %s:\n  the copy %v %q\n  SQLite   %v %q", label, query.text, got.Columns, cells(got), want.Columns, cells(want))
			}
		}
		if answered := analyticAnswers.Load() - before; wantAnswers >= 0 && answered != wantAnswers {
			t.Fatalf("%s: the copy answered %d statements, want %d", label, answered, wantAnswers)
		}
	}
	check("while the copy is built", -1) // the build may end between two statements
	registry.Wait()
	check("from the copy", int64(len(salesQueries)))
	if _, err := database.Write(context.Background(), "t", func(tx *kitdb.Transaction) error {
		if _, err := tx.Exec(`UPDATE sales SET price = price + 7, note = 'n9' WHERE id % 13 = 0`); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM sales WHERE id % 17 = 0`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	check("after writes", int64(len(salesQueries)))
}

// TestAnalyticLeavesTheRestToSQLite: a plan through an index, a statement of another shape (HAVING, LIKE, a bare
// column beside an aggregate, a join), a text compared with a number column, a table too small: SQLite answers, as a
// session without the copy does.
//
// CONTROL (05/11/2026): with an index plan taken by the copy too, "SELECT count(*), sum(price) FROM sales WHERE
// category = 7: the copy answered it"; with a bare column beside aggregates taken as the group's, "SELECT merchant,
// price, count(*) FROM sales GROUP BY merchant: \"lazada,lazada,998|…\", SQLite \"lazada,449383,998|…\""; with a
// table of any size copied, "a table of 50 rows got a copy".
func TestAnalyticLeavesTheRestToSQLite(t *testing.T) {
	path, _ := salesFile(t, 3000, true)
	registry := analytics.NewRegistry()
	registry.MinimumRows = 1
	t.Cleanup(func() { _ = registry.Close() })
	with, without := salesSession(t, path, registry), salesSession(t, path, nil)
	// a copy of every column the statements below read, so only the path keeps them from it
	mustRun(t, with, `SELECT merchant, count(*), sum(price), min(note), max(category) FROM sales GROUP BY merchant`)
	registry.Wait()
	before := analyticAnswers.Load()
	mustRun(t, with, `SELECT merchant, count(*), sum(price), min(note), max(category) FROM sales GROUP BY merchant`)
	if analyticAnswers.Load() != before+1 {
		t.Fatal("the copy did not answer the statement it was built for")
	}
	for _, query := range []string{
		`SELECT count(*), sum(price) FROM sales WHERE category = 7`,
		`SELECT merchant, count(*) FROM sales GROUP BY merchant HAVING count(*) > 10`,
		`SELECT count(*) FROM sales WHERE note LIKE 'n%'`,
		`SELECT merchant, price, count(*) FROM sales GROUP BY merchant`,
		`SELECT count(*) FROM sales a JOIN sales b ON a.id = b.id`,
		`SELECT count(*) FROM sales WHERE merchant = 5`,
		`SELECT id, price FROM sales WHERE price > 10 LIMIT 3`,
	} {
		before := analyticAnswers.Load()
		got := mustRun(t, with, query)
		want := mustRun(t, without, query)
		if !sameResult(got, want) {
			t.Fatalf("%s: %q, SQLite %q", query, cells(got), cells(want))
		}
		if analyticAnswers.Load() != before {
			t.Fatalf("%s: the copy answered it", query)
		}
	}
	small, _ := salesFile(t, 50, false)
	tiny := analytics.NewRegistry() // the least is 100,000 rows
	t.Cleanup(func() { _ = tiny.Close() })
	session := salesSession(t, small, tiny)
	mustRun(t, session, `SELECT count(*), sum(price) FROM sales WHERE merchant = 'tiki'`)
	tiny.Wait()
	before = analyticAnswers.Load()
	mustRun(t, session, `SELECT count(*), sum(price) FROM sales WHERE merchant = 'tiki'`)
	if analyticAnswers.Load() != before {
		t.Fatal("a table of 50 rows got a copy")
	}
}

// TestAnalyticTakesASearchSQLiteIsSlowAt: a statement whose plan scans the table through an index (for its order) is
// the copy's; one whose plan searches an index is SQLite's while it answers within the budget; past it, the copy
// answers, the same.
//
// CONTROL (05/11/2026): with a scan in an index's order left to SQLite (as first written: shopping's GROUP BY merchant
// through its key took 35 s on SQLite), "a scan through the index: … (the copy answered 0)"; with a search never
// given to the copy, "past the budget: \"62,30018355,tiki\" (the copy answered 0)".
func TestAnalyticTakesASearchSQLiteIsSlowAt(t *testing.T) {
	path, _ := salesFile(t, 3000, true)
	registry := analytics.NewRegistry()
	registry.MinimumRows = 1
	t.Cleanup(func() { _ = registry.Close() })
	with, without := salesSession(t, path, registry), salesSession(t, path, nil)
	query := `SELECT count(*), sum(price), max(merchant) FROM sales WHERE category = 7`
	mustRun(t, with, `SELECT merchant, count(*), sum(price), min(note), max(category) FROM sales GROUP BY merchant`)
	registry.Wait()
	// a scan in the order of an index is a scan: the copy's
	grouped := `SELECT category, count(*), sum(price) FROM sales GROUP BY category`
	before := analyticAnswers.Load()
	if got, want := mustRun(t, with, grouped), mustRun(t, without, grouped); !sameResult(got, want) || analyticAnswers.Load() != before+1 {
		t.Fatalf("a scan through the index: %q (the copy answered %d), SQLite %q", cells(got), analyticAnswers.Load()-before, cells(want))
	}
	before = analyticAnswers.Load()
	if got, want := mustRun(t, with, query), mustRun(t, without, query); !sameResult(got, want) || analyticAnswers.Load() != before {
		t.Fatalf("within the budget: %q (the copy answered %d), SQLite %q", cells(got), analyticAnswers.Load()-before, cells(want))
	}
	previous := analyticBudget
	analyticBudget = 0 // as when SQLite runs past it
	t.Cleanup(func() { analyticBudget = previous })
	if got, want := mustRun(t, with, query), mustRun(t, without, query); !sameResult(got, want) || analyticAnswers.Load() != before+1 {
		t.Fatalf("past the budget: %q (the copy answered %d), SQLite %q", cells(got), analyticAnswers.Load()-before, cells(want))
	}
}
