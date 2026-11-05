package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kitwork/engine/kitdb/analytics"
	"github.com/kitwork/engine/kitdb/columnar"
)

// analyticsColumns are the columns of shopping the analytic queries of RESULTS_2026-10-27.md read: the integers
// they filter, group and sum, the merchant they group by.
var analyticsColumns = []string{"merchant", "id", "shop", "category", "price", "pricing", "discount", "sold", "stock",
	"review", "official"}

// cmdAnalytics builds KCOL on SQLite (kitdb/analytics, docs/KITWORK_KITDB.md mục 6 of the Kitwork repository) for
// shopping's analytic columns, from one read transaction of the file opened read-only, into directory, and reads
// every block of it back. The file is never written. Evidence for mục 6 and 11, bước 2 (05/11/2026).
func cmdAnalytics(path, directory string) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+toSlash(path)+"?mode=ro&_pragma=busy_timeout(10000)&_pragma=cache_size(-262144)")
	must(err)
	defer db.Close()
	db.SetMaxOpenConns(2) // the build's read transaction, then the queries' own
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	must(err)
	defer tx.Rollback()

	start := time.Now()
	if _, err := os.Stat(filepath.Join(directory, "shopping.json")); err == nil {
		fmt.Println("the copy built before in " + directory + " is read") // its view says whether it is behind
	} else {
		manifest, err := analytics.Build(ctx, tx, directory, "shopping", analyticsColumns)
		must(err)
		fmt.Printf("built %d rows of %d columns at revision %d: %s, %s\n", manifest.Rows, len(manifest.Columns),
			manifest.Revision, duration(time.Since(start)), gigabytes(manifest.Bytes))
	}

	answerAnalytics(ctx, db, directory)
	copy, err := analytics.Open(directory, "shopping")
	must(err)
	defer copy.Close()
	for _, columns := range [][]string{{"price"}, {"merchant", "price"}, analyticsColumns} {
		start = time.Now()
		var rows, sum int64
		report, err := copy.Scan(ctx, columns, nil, func(batch *columnar.Batch) error {
			rows += int64(batch.Rows)
			last := batch.Columns[len(batch.Columns)-1]
			if last.Field.Kind == columnar.Integer {
				for row := range batch.Rows {
					sum += last.Integers[row]
				}
			}
			return nil
		})
		must(err)
		fmt.Printf("scanned %v: %d rows, %s read, %s\n", columns, rows,
			gigabytes(int64(report.ColumnPayloadBytesRead+report.BlockHeaderBytesRead)), duration(time.Since(start)))
		_ = sum
	}
}

// analyticsQueries are the analytic queries of RESULTS_2026-10-27.md, each as SQL and as the copy's query.
var analyticsQueries = []struct {
	name, sql string
	query     analytics.Query
}{
	{"common category + SUM/AVG", `SELECT COUNT(*), SUM(price), AVG(price) FROM shopping WHERE category = 100636`,
		analytics.Query{Filter: &analytics.Condition{Op: analytics.OpEqual, Column: "category", Value: int64(100636)},
			Aggregates: []analytics.Aggregate{{Function: "count"}, {Function: "sum", Column: "price"}, {Function: "avg", Column: "price"}}}},
	{"rare category + SUM/AVG/MAX", `SELECT SUM(price), AVG(stock), MAX(sold) FROM shopping WHERE category = 4459`,
		analytics.Query{Filter: &analytics.Condition{Op: analytics.OpEqual, Column: "category", Value: int64(4459)},
			Aggregates: []analytics.Aggregate{{Function: "sum", Column: "price"}, {Function: "avg", Column: "stock"}, {Function: "max", Column: "sold"}}}},
	{"high id range + SUM/AVG", `SELECT COUNT(*), SUM(price), AVG(price) FROM shopping WHERE id >= 20000000000`,
		analytics.Query{Filter: &analytics.Condition{Op: analytics.OpAtLeast, Column: "id", Value: int64(20000000000)},
			Aggregates: []analytics.Aggregate{{Function: "count"}, {Function: "sum", Column: "price"}, {Function: "avg", Column: "price"}}}},
	{"GROUP BY category (5,699 groups)", `SELECT category, COUNT(*), SUM(price) FROM shopping GROUP BY category ORDER BY category`,
		analytics.Query{Group: []string{"category"}, Aggregates: []analytics.Aggregate{{Function: "count"}, {Function: "sum", Column: "price"}}}},
	{"GROUP BY merchant, count only", `SELECT merchant, COUNT(*) FROM shopping GROUP BY merchant ORDER BY merchant`,
		analytics.Query{Group: []string{"merchant"}, Aggregates: []analytics.Aggregate{{Function: "count"}}}},
	{"GROUP BY merchant + SUM(price)", `SELECT merchant, COUNT(*), SUM(price) FROM shopping GROUP BY merchant ORDER BY merchant`,
		analytics.Query{Group: []string{"merchant"}, Aggregates: []analytics.Aggregate{{Function: "count"}, {Function: "sum", Column: "price"}}}},
	{"whole table: COUNT, SUM, MIN, MAX", `SELECT COUNT(*), SUM(price), MIN(price), MAX(sold) FROM shopping`,
		analytics.Query{Aggregates: []analytics.Aggregate{{Function: "count"}, {Function: "sum", Column: "price"}, {Function: "min", Column: "price"}, {Function: "max", Column: "sold"}}}},
}

// answerAnalytics answers each query from the copy, through a view of a read transaction of its own, and from
// SQLite in that same transaction, and prints both times and whether the rows are the same. The copy answers every
// query twice first, the second timed, then SQLite answers each: a SQLite scan of the whole file pushes the copy out of
// the system's file cache, so alternating would time the copy read from disk.
func answerAnalytics(ctx context.Context, db *sql.DB, directory string) {
	table, err := analytics.OpenTable(directory, "shopping")
	must(err)
	defer table.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	must(err)
	defer tx.Rollback()
	start := time.Now()
	view, err := table.View(ctx, tx)
	must(err)
	fmt.Println()
	fmt.Println(fmt.Sprintf("the view at revision %d (%d changed rows): %s", view.Revision, view.Changed(), duration(time.Since(start))))
	results := make([]analytics.Result, len(analyticsQueries))
	copyTimes := make([]time.Duration, len(analyticsQueries))
	for pass := range 2 {
		for position, item := range analyticsQueries {
			start := time.Now()
			result, err := view.Execute(ctx, item.query)
			must(err)
			if pass == 1 {
				results[position], copyTimes[position] = result, time.Since(start)
			}
		}
	}
	fmt.Println(fmt.Sprintf("%-36s %10s %10s  %s", "query", "KCOL", "SQLite", "rows"))
	for position, item := range analyticsQueries {
		start := time.Now()
		rows, err := tx.QueryContext(ctx, item.sql)
		must(err)
		var want [][]any
		names, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(names))
			pointers := make([]any, len(names))
			for index := range values {
				pointers[index] = &values[index]
			}
			must(rows.Scan(pointers...))
			want = append(want, values)
		}
		must(rows.Err())
		rows.Close()
		sqliteTime := time.Since(start)
		verdict := "the same"
		if fmt.Sprint(results[position].Rows) != fmt.Sprint(want) {
			verdict = fmt.Sprintf("DIFFERENT: %.200v against %.200v", results[position].Rows, want)
		}
		fmt.Println(fmt.Sprintf("%-36s %10s %10s  %d, %s", item.name, duration(copyTimes[position]), duration(sqliteTime), len(want), verdict))
	}
}
