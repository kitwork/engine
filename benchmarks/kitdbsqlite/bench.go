package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/relational"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type benchQuery struct {
	name, sql string
}

// The analytic queries are those of BenchmarkShoppingProductionAnalytics (kitdb/relational).
var analyticQueries = []benchQuery{
	{"COUNT(*) whole table", `SELECT COUNT(*) FROM shopping`},
	{"common category + SUM/AVG", `SELECT COUNT(*), SUM(price), AVG(price) FROM shopping WHERE category = 100636`},
	{"rare category + SUM/AVG/MAX", `SELECT SUM(price), AVG(stock), MAX(sold) FROM shopping WHERE category = 4459`},
	{"high id range + SUM/AVG", `SELECT COUNT(*), SUM(price), AVG(price) FROM shopping WHERE id >= 20000000000`},
	{"GROUP BY category (5,699 groups)", `SELECT category, COUNT(*) AS products, SUM(price) AS value FROM shopping GROUP BY category ORDER BY category`},
	{"GROUP BY merchant, count only", `SELECT merchant, COUNT(*) AS products FROM shopping GROUP BY merchant ORDER BY merchant`},
	{"GROUP BY merchant + SUM(price)", `SELECT merchant, COUNT(*) AS products, SUM(price) AS value FROM shopping GROUP BY merchant ORDER BY merchant`},
	{"count in one merchant by key", `SELECT COUNT(*) AS products, MIN(id) AS first_id, MAX(id) AS last_id FROM shopping WHERE merchant = 'lazada' AND id >= 0`},
}

func openBenchKitDB(path string) *relational.Engine {
	return openKitDB(path, relational.Options{ExperimentalProjections: true, BatchAggregates: true})
}

func openBenchSQLite(path string) *sql.DB {
	db, err := sql.Open("sqlite", "file:"+toSlash(path)+"?mode=ro&_pragma=cache_size(-262144)")
	must(err)
	db.SetMaxOpenConns(1)
	return db
}

// rowsText shortens a result so both engines compare; numbers print as %.6g so a decimal AVG and a float AVG
// compare equal.
func rowsText(rows [][]any) string {
	var out []string
	for i, r := range rows {
		if i == 3 {
			out = append(out, fmt.Sprintf("… (%d rows)", len(rows)))
			break
		}
		cells := make([]string, len(r))
		for j, v := range r {
			cells[j] = cellText(v)
		}
		out = append(out, strings.Join(cells, ","))
	}
	return strings.Join(out, " | ")
}

func cellText(v any) string {
	switch x := v.(type) {
	case int64:
		return fmt.Sprintf("%.6g", float64(x))
	case float64:
		return fmt.Sprintf("%.6g", x)
	case string:
		var f float64
		if _, err := fmt.Sscan(x, &f); err == nil && strings.ContainsAny(x, "0123456789") &&
			!strings.ContainsAny(x, "abcdefghijklmnopqrstuvwxyz") {
			return fmt.Sprintf("%.6g", f)
		}
		return x
	case []byte:
		return string(x)
	case nil:
		return "NULL"
	}
	return fmt.Sprint(v)
}

func querySQLite(db *sql.DB, query string) [][]any {
	rows, err := db.Query(query)
	must(err)
	defer rows.Close()
	columns, _ := rows.Columns()
	var out [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		must(rows.Scan(pointers...))
		out = append(out, values)
	}
	must(rows.Err())
	return out
}

// timeRuns runs three times, or twice when the first run is slower than slow.
func timeRuns(run func(), slow time.Duration) (first, best time.Duration) {
	for i := 0; i < 3; i++ {
		start := time.Now()
		run()
		d := time.Since(start)
		if i == 0 {
			first, best = d, d
		} else if d < best {
			best = d
		}
		if i == 1 && first > slow {
			break
		}
	}
	return
}

func duration(d time.Duration) string {
	if d >= time.Second {
		return fmt.Sprintf("%.2f s", d.Seconds())
	}
	return fmt.Sprintf("%.2f ms", float64(d.Nanoseconds())/1e6)
}

func executionPath(ctx context.Context, engine *relational.Engine, query string) string {
	observed, err := engine.Execute(ctx, "EXPLAIN ANALYZE "+query)
	must(err)
	if observed.Execution == nil {
		return "?"
	}
	return observed.Execution.Path
}

func cmdBench(kitPath, sqlitePath, label string) {
	ctx := context.Background()
	engine := openBenchKitDB(kitPath)
	defer engine.Close()
	db := openBenchSQLite(sqlitePath)
	defer db.Close()
	fmt.Printf("== %s\n", label)

	// Random keys through SQLite's rowid: 1..N are contiguous because the table was loaded once, never deleted.
	random := rand.New(rand.NewPCG(2026, 10))
	type key struct {
		merchant string
		id       int64
	}
	var keys []key
	byRowID, err := db.Prepare("SELECT merchant, id FROM shopping WHERE rowid = ?")
	must(err)
	for len(keys) < 2000 {
		var k key
		must(byRowID.QueryRow(1+random.Int64N(importRows)).Scan(&k.merchant, &k.id))
		keys = append(keys, k)
	}
	byRowID.Close()

	// 1. one full row by primary key
	pointPlan, err := kitdbsql.ParseStatement("SELECT * FROM shopping WHERE merchant = $1 AND id = $2")
	must(err)
	sqlitePoint, err := db.Prepare("SELECT * FROM shopping WHERE merchant = ? AND id = ?")
	must(err)
	values := make([]any, len(shoppingColumns))
	pointers := make([]any, len(shoppingColumns))
	for i := range values {
		pointers[i] = &values[i]
	}
	for _, name := range []string{"first read", "same keys again"} {
		start := time.Now()
		for _, k := range keys {
			r, err := engine.ExecutePlan(ctx, &pointPlan, k.merchant, k.id)
			must(err)
			if len(r.Rows) != 1 {
				panic(fmt.Sprintf("KitDB did not find %v", k))
			}
		}
		kit := time.Since(start) / time.Duration(len(keys))
		start = time.Now()
		for _, k := range keys {
			must(sqlitePoint.QueryRow(k.merchant, k.id).Scan(pointers...))
		}
		lite := time.Since(start) / time.Duration(len(keys))
		fmt.Printf("one full row by key (2,000 keys, %s): KitDB %s | SQLite %s\n", name, duration(kit), duration(lite))
	}
	sqlitePoint.Close()

	// 2. one page of 20 rows within a merchant, after an id
	pagePlan, err := kitdbsql.ParseStatement("SELECT merchant, id, name, price FROM shopping WHERE merchant = $1 AND id > $2 ORDER BY id LIMIT 20")
	must(err)
	sqlitePage, err := db.Prepare("SELECT merchant, id, name, price FROM shopping WHERE merchant = ? AND id > ? ORDER BY id LIMIT 20")
	must(err)
	pageKeys := keys[:500]
	start := time.Now()
	for _, k := range pageKeys {
		_, err := engine.ExecutePlan(ctx, &pagePlan, k.merchant, k.id)
		must(err)
	}
	kitPage := time.Since(start) / time.Duration(len(pageKeys))
	start = time.Now()
	for _, k := range pageKeys {
		rows, err := sqlitePage.Query(k.merchant, k.id)
		must(err)
		for rows.Next() {
		}
		rows.Close()
	}
	litePage := time.Since(start) / time.Duration(len(pageKeys))
	sqlitePage.Close()
	fmt.Printf("page of 20 rows in one merchant (500 times): KitDB %s | SQLite %s\n\n", duration(kitPage), duration(litePage))

	// 3. analytics
	fmt.Printf("%-34s | %-30s | %-22s | result\n", "query", "KitDB first / best (path)", "SQLite first / best")
	for _, q := range analyticQueries {
		path := executionPath(ctx, engine, q.sql)
		var kitRows, liteRows [][]any
		kitFirst, kitBest := timeRuns(func() {
			r, err := engine.Execute(ctx, q.sql)
			must(err)
			kitRows = r.Rows
		}, 20*time.Second)
		liteFirst, liteBest := timeRuns(func() { liteRows = querySQLite(db, q.sql) }, 20*time.Second)
		same := "same"
		if rowsText(kitRows) != rowsText(liteRows) {
			same = "DIFFERENT: KitDB " + rowsText(kitRows) + " / SQLite " + rowsText(liteRows)
		}
		fmt.Printf("%-34s | %-9s / %-9s (%s) | %-9s / %-9s | %s  [%s]\n", q.name, duration(kitFirst), duration(kitBest), path,
			duration(liteFirst), duration(liteBest), same, rowsText(liteRows))
	}
}

func cmdRefresh(kitPath string) {
	engine := openBenchKitDB(kitPath)
	defer engine.Close()
	start := time.Now()
	report, err := engine.RefreshAnalytics(context.Background())
	must(err)
	fmt.Printf("KCOL (.analytics) built in %v\n%+v\n", time.Since(start).Round(time.Second), report)
}

func cmdExecute(kitPath, statement string) {
	engine := openBenchKitDB(kitPath)
	defer engine.Close()
	r, err := engine.Execute(context.Background(), statement)
	must(err)
	fmt.Printf("%s -> %d rows\n", statement, len(r.Rows))
}

func cmdMeasure(kitPath, query string) {
	ctx := context.Background()
	engine := openBenchKitDB(kitPath)
	defer engine.Close()
	path := executionPath(ctx, engine, query)
	var rows [][]any
	first, best := timeRuns(func() {
		r, err := engine.Execute(ctx, query)
		must(err)
		rows = r.Rows
	}, 20*time.Second)
	fmt.Printf("KitDB %s / %s (%s) [%s]\n", duration(first), duration(best), path, rowsText(rows))
}

func cmdQuery(sqlitePath, query string) {
	db := openBenchSQLite(sqlitePath)
	defer db.Close()
	start := time.Now()
	rows := querySQLite(db, query)
	fmt.Printf("%v (%s)\n", rows, duration(time.Since(start)))
}

// cmdDefinition prints the Schema IR of every table, straight from the kernel catalog: information_schema and
// pg_catalog exist only on pgwire, not through Execute.
func cmdDefinition(kitPath string) {
	db, err := kitdbengine.Open(kitPath)
	must(err)
	defer db.Close()
	snapshot, err := db.Catalog()
	must(err)
	for _, s := range snapshot.Structs {
		var pretty bytes.Buffer
		if json.Indent(&pretty, s.Definition, "", "  ") != nil {
			pretty.Write(s.Definition)
		}
		fmt.Printf("== %s\n%s\n", s.Name, pretty.String())
	}
}
