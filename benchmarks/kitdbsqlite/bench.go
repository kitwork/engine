package main

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type benchQuery struct {
	name, sql string
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

func cmdQuery(sqlitePath, query string) {
	db := openBenchSQLite(sqlitePath)
	defer db.Close()
	start := time.Now()
	rows := querySQLite(db, query)
	fmt.Printf("%v (%s)\n", rows, duration(time.Since(start)))
}
