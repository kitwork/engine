package relational

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func openJoinAggregateEngine(t testing.TB, layout string) *Engine {
	t.Helper()
	engine, err := Open(filepath.Join(t.TempDir(), "joins.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })
	constraint := `PRIMARY KEY (merchant, id)`
	if layout != "primary" {
		constraint = `row_id INTEGER PRIMARY KEY`
		if layout == "unique" {
			constraint += `, UNIQUE (merchant, id)`
		}
	}
	statements := []string{
		`CREATE TABLE products (merchant TEXT NOT NULL, id INTEGER NOT NULL, name TEXT, price DECIMAL, units BIGINT, ` + constraint + `)`,
		`CREATE TABLE events (id INTEGER PRIMARY KEY, merchant TEXT, product_id INTEGER)`,
		`CREATE TABLE merchants (name TEXT PRIMARY KEY, label TEXT)`,
		`INSERT INTO merchants (name, label) VALUES ('shopee', 'Shopee'), ('lazada', 'Lazada')`,
		`INSERT INTO events (id, merchant, product_id) VALUES
		 (1, 'shopee', 1), (2, 'shopee', 1), (3, 'lazada', 1), (4, 'shopee', 2),
		 (5, 'shopee', 99), (6, NULL, 1), (7, 'shopee', NULL), (8, 'shopee', 3)`,
	}
	if layout == "index" {
		statements = append(statements, `CREATE INDEX products_merchant_id ON products (merchant, id)`)
	}
	for _, statement := range statements {
		joinTestExecute(t, engine, statement)
	}
	for i, values := range []string{
		`'shopee', 1, 'keyboard', '10.25', 9007199254740993`,
		`'lazada', 1, 'keyboard', '20.75', 5`,
		`'shopee', 2, 'mouse', NULL, NULL`,
		`'shopee', 3, 'tea', '3.5', 7`,
	} {
		columns := "merchant, id, name, price, units"
		if layout != "primary" {
			columns += ", row_id"
			values += fmt.Sprintf(", %d", i+1)
		}
		joinTestExecute(t, engine, "INSERT INTO products ("+columns+") VALUES ("+values+")")
	}
	return engine
}

func joinTestExecute(t testing.TB, engine *Engine, query string, args ...any) Result {
	t.Helper()
	result, err := engine.Execute(context.Background(), query, args...)
	if err != nil {
		t.Fatalf("%.500s: %v", query, err)
	}
	return result
}

const joinAggregateFrom = ` FROM events e LEFT JOIN products p ON (p.id = e.product_id AND e.merchant = p.merchant) `

func TestJoinAggregateCompositeAccessAndExactResults(t *testing.T) {
	for _, layout := range []string{"primary", "unique", "index"} {
		t.Run(layout, func(t *testing.T) {
			engine := openJoinAggregateEngine(t, layout)
			query := `SELECT COUNT(*) AS total, COUNT(p.id) AS found, COUNT(p.price) AS priced,
			 SUM(p.price) AS total_price, AVG(p.price) AS mean_price, MIN(p.price) AS low, MAX(p.price) AS high,
			 SUM(p.units) AS units` + joinAggregateFrom
			want := [][]any{{int64(8), int64(5), int64(4), "44.75", "11.1875", "3.5", "20.75", "18014398509481998"}}
			for _, suffix := range []string{"", " LIMIT 1", " HAVING total > $1 LIMIT 1"} {
				args := []any(nil)
				if strings.Contains(suffix, "$1") {
					args = []any{int64(7)}
				}
				result := joinTestExecute(t, engine, query+suffix, args...)
				if !reflect.DeepEqual(result.Rows, want) {
					t.Fatalf("aggregate = %#v, want %#v", result.Rows, want)
				}
			}
			inner := joinTestExecute(t, engine, strings.Replace(query, "LEFT JOIN", "JOIN", 1))
			if inner.Rows[0][0] != int64(5) || inner.Rows[0][3] != "44.75" {
				t.Fatalf("inner aggregate = %#v", inner.Rows)
			}
			rows := joinTestExecute(t, engine, `SELECT e.id, p.merchant, p.price`+joinAggregateFrom+`ORDER BY e.id`)
			if len(rows.Rows) != 8 || rows.Rows[2][1] != "lazada" || rows.Rows[4][1] != nil || rows.Rows[6][1] != nil {
				t.Fatalf("composite row JOIN = %#v", rows.Rows)
			}
			// Explain must not interpret COUNT(*) as a catalog count or bind target
			// predicates against the base schema. Actual traversal reads all matches.
			explained := joinTestExecute(t, engine, `EXPLAIN ANALYZE `+query+` WHERE p.price >= $1 LIMIT 1`, "10")
			stats := explained.Execution
			if stats == nil || stats.Path != "nested-loop-join-aggregate" || stats.RowsMatched != 3 || stats.Groups != 1 || stats.MaterializationPeakBytes == 0 {
				t.Fatalf("execution = %+v", stats)
			}
			if layout == "primary" && stats.IndexEntriesScanned != 0 {
				t.Fatalf("composite point lookup fell back to index scan: %+v", stats)
			}
			if layout == "unique" && (stats.IndexEntriesScanned != 5 || stats.PointLookups != 11) {
				t.Fatalf("composite unique lookup work = %+v", stats)
			}
		})
	}
}

func TestJoinAggregateGroupingNullsAndComposition(t *testing.T) {
	engine := openJoinAggregateEngine(t, "primary")
	tests := []struct {
		query string
		want  [][]any
	}{
		{`SELECT p.merchant AS merchant, COUNT(*) AS total, SUM(p.price) AS amount` + joinAggregateFrom +
			`GROUP BY p.merchant HAVING total >= 3 ORDER BY total DESC LIMIT 1 OFFSET 1`, [][]any{{nil, int64(3), nil}}},
		{`SELECT p.merchant, p.name, COUNT(*) AS total` + joinAggregateFrom +
			`WHERE e.id <= 4 GROUP BY p.merchant, p.name ORDER BY p.merchant, p.name`,
			[][]any{{"lazada", "keyboard", int64(1)}, {"shopee", "keyboard", int64(2)}, {"shopee", "mouse", int64(1)}}},
		{`SELECT p.merchant` + joinAggregateFrom + `WHERE p.id IS NOT NULL GROUP BY p.merchant ORDER BY p.merchant`,
			[][]any{{"lazada"}, {"shopee"}}},
		{`SELECT e.id AS event_id, p.id AS product_id, COUNT(*) AS total` + joinAggregateFrom +
			`WHERE e.id <= 4 GROUP BY e.id, p.id ORDER BY p.id DESC, e.id`,
			[][]any{{int64(4), int64(2), int64(1)}, {int64(1), int64(1), int64(1)}, {int64(2), int64(1), int64(1)}, {int64(3), int64(1), int64(1)}}},
		{`SELECT COUNT(*), COUNT(p.price), SUM(p.price), AVG(p.price), MIN(p.price), MAX(p.price)` + joinAggregateFrom + `WHERE e.id < 0`,
			[][]any{{int64(0), int64(0), nil, nil, nil, nil}}},
		{`SELECT COUNT(*)` + joinAggregateFrom + `WHERE p.id IS NULL`, [][]any{{int64(3)}}},
		{`SELECT m.label, COUNT(*) AS total` + joinAggregateFrom + `LEFT JOIN merchants m ON p.merchant = m.name
		 GROUP BY m.label ORDER BY total DESC`, [][]any{{"Shopee", int64(4)}, {nil, int64(3)}, {"Lazada", int64(1)}}},
		{`WITH totals AS (SELECT p.merchant AS merchant, COUNT(*) AS total` + joinAggregateFrom +
			`GROUP BY p.merchant) SELECT merchant, total FROM totals WHERE total > 3`, [][]any{{"shopee", int64(4)}}},
		{`SELECT COUNT(*) AS total` + joinAggregateFrom + `UNION ALL SELECT COUNT(*) FROM merchants ORDER BY total`, [][]any{{int64(2)}, {int64(8)}}},
	}
	for _, test := range tests {
		result := joinTestExecute(t, engine, test.query)
		if !reflect.DeepEqual(result.Rows, test.want) {
			t.Errorf("%s: got %#v want %#v", test.query, result.Rows, test.want)
		}
	}
	for _, query := range []string{
		`SELECT p.name, COUNT(*)` + joinAggregateFrom + `WHERE e.id < 0 GROUP BY p.name`,
		`SELECT COUNT(*) AS total` + joinAggregateFrom + `HAVING total > 20`,
		`SELECT COUNT(*)` + joinAggregateFrom + `LIMIT 1 OFFSET 1`,
		`SELECT COUNT(*)` + joinAggregateFrom + `LIMIT 0`,
	} {
		if result := joinTestExecute(t, engine, query); len(result.Rows) != 0 {
			t.Fatalf("expected empty: %#v", result.Rows)
		}
	}
}

func TestJoinAggregateSnapshotReadYourWritesAndRollback(t *testing.T) {
	engine := openJoinAggregateEngine(t, "primary")
	ctx := context.Background()
	tx, err := engine.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	joinTestExecute(t, engine, `UPDATE products SET price = 100 WHERE merchant = 'shopee' AND id = 1`)
	query := `SELECT SUM(p.price)` + joinAggregateFrom
	result, err := tx.Execute(ctx, query)
	if err != nil || result.Rows[0][0] != "44.75" {
		t.Fatalf("snapshot = %#v, %v", result.Rows, err)
	}
	if _, err := tx.Execute(ctx, `DELETE FROM events WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	result, err = tx.Execute(ctx, query)
	if err != nil || result.Rows[0][0] != "34.5" {
		t.Fatalf("read own deletion = %#v, %v", result.Rows, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if result := joinTestExecute(t, engine, query); result.Rows[0][0] != "224.25" {
		t.Fatalf("rollback = %#v", result.Rows)
	}
}

func TestJoinAggregateRejectsUnsupportedShapesAndBudgets(t *testing.T) {
	engine := openJoinAggregateEngine(t, "primary")
	for _, query := range []string{
		`SELECT SUM(price * 2)` + joinAggregateFrom,
		`SELECT DISTINCT COUNT(*)` + joinAggregateFrom,
		`SELECT p.name, COUNT(*)` + joinAggregateFrom,
		`SELECT COUNT(id)` + joinAggregateFrom,
		`SELECT SUM(p.name)` + joinAggregateFrom,
		`SELECT SUM(wrong.price)` + joinAggregateFrom,
		`SELECT COUNT(*)` + joinAggregateFrom + `ORDER BY p.price`,
		`SELECT COUNT(*)` + joinAggregateFrom + `GROUP BY wrong.id`,
		`SELECT e.id, p.id, COUNT(*)` + joinAggregateFrom + `GROUP BY e.id, p.id ORDER BY p.id`,
	} {
		if result, err := engine.Execute(context.Background(), query); err == nil || len(result.Rows) != 0 {
			t.Fatalf("accepted or partial result %s: %#v %v", query, result.Rows, err)
		}
	}
	engine.maximumResultRows = 2
	if _, err := engine.Execute(context.Background(), `SELECT p.merchant, COUNT(*)`+joinAggregateFrom+`GROUP BY p.merchant LIMIT 1`); err == nil || !strings.Contains(err.Error(), "group limit") {
		t.Fatalf("group budget = %v", err)
	}
	engine.maximumResultRows = 100
	tx, err := engine.BeginTransaction(context.Background(), TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	parsed, err := kitdbsql.ParseStatement(`SELECT p.name, COUNT(*)` + joinAggregateFrom + `GROUP BY p.name`)
	if err != nil {
		t.Fatal(err)
	}
	budget := newMaterializationBudget(100)
	budget.maximumBytes = 128
	working := newMaterializationWorkingSet(budget)
	result, err := tx.executeJoinSelect(context.Background(), parsed.Select, nil, true, working)
	working.close()
	if err == nil || !strings.Contains(err.Error(), "query memory budget") || len(result.Rows) != 0 || budget.workingBytes != 0 {
		t.Fatalf("memory admission = %#v %v budget=%+v", result, err, budget)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = tx.executeJoinSelect(canceled, parsed.Select, nil, true, nil)
	if !errors.Is(err, context.Canceled) || len(result.Rows) != 0 {
		t.Fatalf("cancellation = %#v, %v", result.Rows, err)
	}
}

func TestJoinEqualitiesKeepResidualsAndNullExtension(t *testing.T) {
	engine := openJoinAggregateEngine(t, "primary")
	query := `SELECT COUNT(*), COUNT(p.id) FROM events e LEFT JOIN products p
	 ON p.merchant = e.merchant AND p.id = e.product_id AND p.id = e.id`
	result := joinTestExecute(t, engine, query)
	if !reflect.DeepEqual(result.Rows, [][]any{{int64(8), int64(1)}}) {
		t.Fatalf("contradictory equalities or outer null extension = %#v", result.Rows)
	}
	// A leading-prefix index can narrow candidates, but must not replace ON.
	joinTestExecute(t, engine, `CREATE TABLE copies (row_id INTEGER PRIMARY KEY, merchant TEXT, id INTEGER, name TEXT)`)
	joinTestExecute(t, engine, `CREATE INDEX copies_merchant ON copies (merchant)`)
	joinTestExecute(t, engine, `INSERT INTO copies (row_id, merchant, id, name) VALUES
	 (1, 'shopee', 1, 'first'), (2, 'shopee', 2, 'second'), (3, 'shopee', NULL, 'null')`)
	result = joinTestExecute(t, engine, `SELECT COUNT(*), COUNT(p.id) FROM events e
	 LEFT JOIN copies p ON e.merchant = p.merchant AND e.product_id = p.id`)
	if !reflect.DeepEqual(result.Rows, [][]any{{int64(8), int64(3)}}) {
		t.Fatalf("prefix residual = %#v", result.Rows)
	}
	for _, on := range []string{
		`p.merchant = e.merchant AND p.id = e.merchant`,
		`p.merchant = e.merchant AND p.id = p.row_id`,
		`p.merchant = e.merchant AND e.id = e.product_id`,
	} {
		if _, err := engine.Execute(context.Background(), `SELECT COUNT(*) FROM events e JOIN copies p ON `+on); err == nil {
			t.Fatalf("accepted invalid ON %s", on)
		}
	}
}

func TestJoinAggregateTraversalBoundsNeverReturnPartialTotals(t *testing.T) {
	engine := openJoinAggregateEngine(t, "primary")
	joinTestExecute(t, engine, `CREATE TABLE many (id INTEGER PRIMARY KEY, bucket INTEGER)`)
	joinTestExecute(t, engine, `CREATE TABLE targets (id INTEGER PRIMARY KEY, bucket INTEGER)`)
	joinTestExecute(t, engine, `CREATE INDEX targets_bucket ON targets (bucket)`)
	var rows []string
	for i := 1; i <= maximumJoinInputRows; i++ {
		rows = append(rows, fmt.Sprintf("(%d, 1)", i))
		if len(rows) == 256 || i == maximumJoinInputRows {
			joinTestExecute(t, engine, `INSERT INTO many (id, bucket) VALUES `+strings.Join(rows, ","))
			rows = rows[:0]
		}
	}
	joinTestExecute(t, engine, `INSERT INTO targets (id, bucket) VALUES (1, 1), (2, 1)`)
	if _, err := engine.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	// Exact input and candidate boundaries are both allowed, despite LIMIT 1.
	query := `SELECT COUNT(*) FROM many m JOIN targets t ON m.bucket = t.bucket LIMIT 1`
	result := joinTestExecute(t, engine, query)
	if !reflect.DeepEqual(result.Rows, [][]any{{int64(maximumJoinCandidatePairs)}}) {
		t.Fatalf("boundary count = %#v", result.Rows)
	}
	joinTestExecute(t, engine, `INSERT INTO targets (id, bucket) VALUES (3, 1)`)
	result, err := engine.Execute(context.Background(), query)
	if err == nil || !strings.Contains(err.Error(), "candidate limit") || len(result.Rows) != 0 {
		t.Fatalf("partial candidate count = %#v, %v", result.Rows, err)
	}
	joinTestExecute(t, engine, `INSERT INTO many (id, bucket) VALUES (10001, 1)`)
	result, err = engine.Execute(context.Background(), `SELECT COUNT(*) FROM many m JOIN targets t ON m.bucket = t.id LIMIT 1`)
	if err == nil || !strings.Contains(err.Error(), "10000-row input") || len(result.Rows) != 0 {
		t.Fatalf("partial input count = %#v, %v", result.Rows, err)
	}
}

func TestJoinEqualityDoesNotRoundLookupValuesIntoMatches(t *testing.T) {
	for _, test := range []struct{ name, sourceType, targetType, exact, rounded string }{
		{"decimal", "NUMERIC(6,2)", "NUMERIC(6,1)", "1.2", "1.24"},
		{"time", "TIME(3)", "TIME(0)", "12:30:00", "12:30:00.123"},
		{"varchar", "VARCHAR(3)", "VARCHAR(1)", "a", "a "},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine := openJoinAggregateEngine(t, "primary")
			joinTestExecute(t, engine, `CREATE TABLE precise (id INTEGER PRIMARY KEY, code `+test.sourceType+`)`)
			joinTestExecute(t, engine, `CREATE TABLE coarse (id INTEGER PRIMARY KEY, code `+test.targetType+` UNIQUE)`)
			joinTestExecute(t, engine, `INSERT INTO precise (id,code) VALUES (1,$1),(2,$2)`, test.exact, test.rounded)
			joinTestExecute(t, engine, `INSERT INTO coarse (id,code) VALUES (1,$1)`, test.exact)
			result := joinTestExecute(t, engine, `SELECT COUNT(*), COUNT(c.id) FROM precise p LEFT JOIN coarse c ON p.code = c.code`)
			if !reflect.DeepEqual(result.Rows, [][]any{{int64(2), int64(1)}}) {
				t.Fatalf("lossy coercion matched a different key: %#v", result.Rows)
			}
		})
	}
}
