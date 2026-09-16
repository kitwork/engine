package relational

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/searchprojection"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	"github.com/kitwork/engine/search"
)

func TestStandaloneSearchAggregatesMatchSQL(t *testing.T) {
	for _, packed := range []bool{false, true} {
		t.Run(fmt.Sprintf("packed=%t", packed), func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "aggregate.kitdb")
			options := Options{Kernel: kitdbengine.OpenOptions{RetainHistory: true}, ExperimentalProjections: packed,
				SearchForegroundWait: 10 * time.Second, MaximumSearchResults: 2, MaximumSearchCandidates: 4, MaximumResultRows: 50}
			engine, err := OpenWithOptions(path, options)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if engine != nil {
					engine.Close()
				}
			}()
			projectionExecute(t, engine, `CREATE TABLE products (
				id INTEGER PRIMARY KEY, name TEXT SEARCHABLE, description TEXT SEARCHABLE,
				merchant TEXT, category TEXT, price INTEGER, amount NUMERIC(30,2), sold BIGINT, eligible BOOLEAN)`)
			var values []string
			for i := 0; i < 120; i++ {
				name, description, eligible := "highlands", "coffee highlands", "true"
				if i%4 == 0 {
					name, description, eligible = "tea", "coffee", "false"
				}
				merchant := fmt.Sprintf("'shop%d'", i%3)
				if i%7 == 0 {
					merchant = "NULL"
				}
				price, amount, sold := fmt.Sprint(i), "9007199254740993.25", "9007199254740993"
				if i%5 == 0 {
					price, amount, sold = "NULL", "NULL", "NULL"
				}
				values = append(values, fmt.Sprintf("(%d,'%s','%s',%s,'category%d',%s,%s,%s,%s)", i, name, description, merchant, i%2, price, amount, sold, eligible))
			}
			projectionExecute(t, engine, `INSERT INTO products VALUES `+strings.Join(values, ","))
			refresh := func() {
				if packed {
					if _, err := engine.RefreshProjections(ctx); err != nil {
						t.Fatal(err)
					}
				}
			}
			refresh()
			compare := func(projection, suffix string) {
				t.Helper()
				query := `SELECT ` + projection + ` FROM products WHERE * SEARCH 'highlands coffee'` + suffix
				got := projectionExecute(t, engine, query)
				want := projectionExecute(t, engine, `SELECT `+projection+` FROM products WHERE eligible = true`+suffix)
				if !reflect.DeepEqual(got.Columns, want.Columns) || len(got.Rows) != len(want.Rows) || len(got.Rows) != 0 && !reflect.DeepEqual(got.Rows, want.Rows) {
					t.Fatalf("%s\ngot=%+v\nwant=%+v", query, got, want)
				}
			}
			all := `COUNT(*) AS total, COUNT(price) AS priced, SUM(price) AS total_price, AVG(price) AS average_price, MIN(price) AS low, MAX(price) AS high, SUM(amount) AS total_amount, AVG(amount) AS average_amount, SUM(sold) AS total_sold, AVG(sold) AS average_sold`
			compare(all, "")
			exact := projectionExecute(t, engine, `SELECT `+all+` FROM products WHERE * SEARCH 'highlands coffee'`)
			if !reflect.DeepEqual(exact.Rows, [][]any{{int64(90), int64(72), int64(4320), "60", int64(1), int64(119), "648518346341351514", "9007199254740993.25", "648518346341351496", "9007199254740993"}}) {
				t.Fatalf("exact aggregate values=%#v", exact.Rows)
			}
			compare(all, ` LIMIT 1`)
			compare(all, ` LIMIT 0`)
			compare(all, ` LIMIT 1 OFFSET 1`)
			compare(all, ` AND price IS NULL`)
			compare(all, ` AND price > 10000`)
			compare(all, ` AND (price < 10 OR price >= 100)`)
			compare(`merchant, `+all, ` GROUP BY merchant ORDER BY total DESC, merchant`)
			compare(`merchant, category, `+all, ` GROUP BY merchant, category ORDER BY merchant, category LIMIT 3 OFFSET 1`)
			compare(`merchant, COUNT(*) AS total, MIN(name) AS first_name, MAX(name) AS last_name`, ` GROUP BY merchant HAVING total > 10 ORDER BY total DESC, merchant`)
			compare(`merchant`, ` GROUP BY merchant ORDER BY merchant`)
			compare(`COUNT(*) AS total`, ` HAVING total > 10000`)
			compare(`COUNT(*) AS total`, ` HAVING total > 0`)
			compare(`merchant, COUNT(*) AS total`, ` AND price > 10000 GROUP BY merchant ORDER BY total`)
			compare(`COUNT(*), COUNT(*)`, ``)
			for _, text := range []any{"missing", "", "   ", "!!!", nil} {
				got, err := engine.Execute(ctx, `SELECT COUNT(*) AS total, SUM(price), AVG(amount), MIN(sold), MAX(price) FROM products WHERE * SEARCH $1`, text)
				if err != nil || len(got.Rows) != 1 || !reflect.DeepEqual(got.Rows[0], []any{int64(0), nil, nil, nil, nil}) {
					t.Fatalf("empty search %v=%+v, %v", text, got, err)
				}
			}
			result, err := engine.Execute(ctx, `SELECT merchant, COUNT(*) AS total FROM products WHERE * SEARCH $1 AND price >= $2 GROUP BY merchant HAVING total > $3 ORDER BY merchant`, "highlands coffee", 100, 1)
			if err != nil || len(result.Rows) == 0 {
				t.Fatalf("parameterized aggregates=%+v, %v", result, err)
			}
			alias := projectionExecute(t, engine, `SELECT p.merchant AS shop, COUNT(*) AS total FROM products AS p WHERE (p.name,p.description) SEARCH 'highlands coffee' GROUP BY p.merchant ORDER BY shop`)
			if len(alias.Rows) != 4 {
				t.Fatalf("qualified groups=%+v", alias)
			}
			if packed {
				got := projectionExecute(t, engine, `SELECT merchant, SUM(price) AS total FROM products WHERE * SEARCH 'highlands coffee' GROUP BY merchant ORDER BY merchant`)
				if got.Execution == nil || got.Execution.Path != "search-aggregate-snapshot" || got.Execution.RowsMatched != 90 || got.Execution.RowsScanned != 90 || got.Execution.MaterializationPeakBytes == 0 || got.Execution.ColumnarPayloadBytesRead != 0 {
					t.Fatalf("aggregate execution=%+v", got.Execution)
				}
				explain := projectionExecute(t, engine, `EXPLAIN ANALYZE SELECT merchant, SUM(price) AS total FROM products WHERE * SEARCH 'highlands coffee' GROUP BY merchant ORDER BY merchant LIMIT 1`)
				if !strings.Contains(fmt.Sprint(explain.Rows), "hydrated=90 matched=90 groups=4 point_lookups=90") {
					t.Fatalf("search aggregate EXPLAIN lost input counts: %+v", explain.Rows)
				}
			}
			for _, query := range []string{
				`SELECT SUM(name) FROM products WHERE * SEARCH 'coffee'`,
				`SELECT SUM(_score) FROM products WHERE * SEARCH 'coffee'`,
				`SELECT merchant, SUM(price) FROM products WHERE * SEARCH 'coffee'`,
				`SELECT COUNT(*) FROM products WHERE * SEARCH 'coffee' ORDER BY _score DESC`,
				`SELECT COUNT(*) FROM products WHERE * SEARCH 'coffee' AFTER 'cursor'`,
				`SELECT SUM(other.price) FROM products WHERE * SEARCH 'coffee'`,
				`SELECT merchant FROM products WHERE * SEARCH 'coffee' GROUP BY other.merchant`,
			} {
				if _, err := engine.Execute(ctx, query); err == nil {
					t.Fatalf("invalid aggregate accepted: %s", query)
				}
			}
			if _, err := engine.Execute(ctx, `SELECT id, COUNT(*) FROM products WHERE * SEARCH 'coffee' GROUP BY id LIMIT 1`); err == nil || !strings.Contains(err.Error(), "group limit") {
				t.Fatalf("group limit silently truncated: %v", err)
			}
			projectionExecute(t, engine, `UPDATE products SET name = 'tea', description = 'tea', eligible = false WHERE id = 1`)
			projectionExecute(t, engine, `DELETE FROM products WHERE id = 3`)
			if packed {
				if _, err := engine.Execute(ctx, `SELECT SUM(price) FROM products WHERE * SEARCH 'coffee'`); err == nil {
					t.Fatal("stale aggregate accepted")
				}
			}
			refresh()
			compare(`merchant, `+all, ` GROUP BY merchant ORDER BY merchant`)
			if err := engine.Close(); err != nil {
				t.Fatal(err)
			}
			engine, err = OpenWithOptions(path, options)
			if err != nil {
				t.Fatal(err)
			}
			compare(all, "")
		})
	}
}

func TestAggregateStreamMemoryAndCancellation(t *testing.T) {
	field := kitdbsql.Field{Name: "merchant", Kind: "text"}
	budget := newMaterializationBudget(100)
	budget.maximumBytes = 1024
	working := newMaterializationWorkingSet(budget)
	stream, err := newAggregateStream([]kitdbsql.Field{field}, []aggregateBinding{{function: "count"}}, 100, working)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.add(map[string]any{"merchant": strings.Repeat("x", 2048)}); err == nil || !strings.Contains(err.Error(), "memory budget") {
		t.Fatalf("group byte guard=%v", err)
	}
	working.close()
	if budget.workingBytes != 0 {
		t.Fatal("working budget leaked")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := stream.finish(ctx, nil, &kitdbsql.SelectStatement{}, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("aggregate cancellation=%v", err)
	}
}

func TestSearchAggregateSnapshotAndFailure(t *testing.T) {
	ctx := context.Background()
	engine, err := Open(filepath.Join(t.TempDir(), "snapshot.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	projectionExecute(t, engine, `CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT SEARCHABLE, price INTEGER)`)
	projectionExecute(t, engine, `INSERT INTO products VALUES (1,'coffee',10),(2,'coffee',20)`)
	statement, err := kitdbsql.ParseStatement(`SELECT SUM(price) AS total FROM products WHERE * SEARCH 'coffee'`)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := engine.schemaLocked("products")
	if err != nil {
		t.Fatal(err)
	}
	bound, err := engine.bindRelationalSearchPlan(schema, statement.Select, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := engine.database.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	generation, _, err := activeRowLayout(snapshot, schema)
	if err != nil {
		t.Fatal(err)
	}
	watermark := searchprojection.Watermark{RowGeneration: generation}
	ids := make([]string, 2)
	for i := range ids {
		// Use the same identity encoder as projection construction.
		key, err := rowKey(schema, map[string]any{"id": int64(i + 1)}, 0)
		if err != nil {
			t.Fatal(err)
		}
		ids[i], err = relationalSearchDocumentID(schema, key)
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := engine.collectRelationalSearchAggregate(ctx, func(ctx context.Context, _ search.MatchQuery, accept func(string) (bool, error)) (uint64, error) {
		for i, id := range ids {
			if i == 1 {
				projectionExecute(t, engine, `UPDATE products SET price = 1000 WHERE id = 2`)
			}
			if _, err := accept(id); err != nil {
				return 0, err
			}
		}
		return 2, nil
	}, snapshot, bound, watermark, nil)
	if err != nil || fmt.Sprint(rows) != "[[30]]" {
		t.Fatalf("mixed source snapshots: %v, %v", rows, err)
	}
	sentinel := errors.New("posting read failed")
	rows, err = engine.collectRelationalSearchAggregate(ctx, func(ctx context.Context, _ search.MatchQuery, accept func(string) (bool, error)) (uint64, error) {
		if _, err := accept(ids[0]); err != nil {
			return 0, err
		}
		return 1, sentinel
	}, snapshot, bound, watermark, nil)
	if len(rows) != 0 || !errors.Is(err, sentinel) {
		t.Fatalf("partial aggregate leaked: %v, %v", rows, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	rows, err = engine.collectRelationalSearchAggregate(canceled, func(ctx context.Context, _ search.MatchQuery, accept func(string) (bool, error)) (uint64, error) {
		if _, err := accept(ids[0]); err != nil {
			return 0, err
		}
		cancel()
		return 1, nil
	}, snapshot, bound, watermark, nil)
	if len(rows) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled aggregate leaked: %v, %v", rows, err)
	}
}
