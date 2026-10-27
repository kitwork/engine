package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/kitdb/relational"
	_ "modernc.org/sqlite"
)

func toSlash(path string) string { return strings.ReplaceAll(path, `\`, "/") }

func pageSize() string {
	if v := os.Getenv("KITDBSQLITE_PAGE"); v != "" {
		return v
	}
	return "16384" // bytes per row on this table: 4 KiB 1,695 · 8 KiB 1,593 · 16 KiB 1,469 · 32 KiB 1,414
}

func openKitDB(path string, options relational.Options) *relational.Engine {
	start := time.Now()
	options.MaximumResultRows = relational.MaximumResultRows
	engine, err := relational.OpenWithOptions(path, options)
	must(err)
	fmt.Printf("open KitDB: %v\n", time.Since(start).Round(time.Millisecond))
	return engine
}

// cmdCopy streams every row out of KitDB in (merchant, id) order into a new SQLite file.
func cmdCopy(source, target string) {
	if _, err := os.Stat(target); err == nil {
		panic("target exists, not overwriting: " + target)
	}
	ctx := context.Background()
	engine := openKitDB(source, relational.Options{})
	defer engine.Close()
	kit, err := relational.OpenNativeSQL(engine)
	must(err)
	defer kit.Close()

	out, err := sql.Open("sqlite", "file:"+toSlash(target)+"?_pragma=page_size("+pageSize()+
		")&_pragma=journal_mode(OFF)&_pragma=synchronous(OFF)&_pragma=cache_size(-262144)")
	must(err)
	out.SetMaxOpenConns(1)
	defer out.Close()
	_, err = out.Exec(createTable())
	must(err)

	marks := strings.TrimSuffix(strings.Repeat("?, ", len(shoppingColumns)), ", ")
	insertSQL := "INSERT INTO shopping (" + columnList() + ") VALUES (" + marks + ")"
	tx, err := out.Begin()
	must(err)
	stmt, err := tx.Prepare(insertSQL)
	must(err)

	sum := newChain()
	values := make([]any, len(shoppingColumns))
	pointers := make([]any, len(shoppingColumns))
	for i := range values {
		pointers[i] = &values[i]
	}
	arguments := make([]any, len(shoppingColumns))
	fields := make([][]byte, len(shoppingColumns))
	var copied int64
	var lastMerchant string
	var lastID int64
	outOfOrder := 0
	perMerchant := map[string]int64{}
	start := time.Now()
	limit, _ := strconv.ParseInt(os.Getenv("KITDBSQLITE_LIMIT"), 10, 64)

	// A KitDB query returns at most 100,000 rows, has no row-value comparison (merchant, id) > (…), and scans
	// the whole table for an OR: read one merchant at a time, one page of ids at a time, and find the next
	// merchant through the primary-key prefix.
	const page = 50000
	nextMerchant := func(after string) (string, bool) {
		r, err := engine.Execute(ctx, "SELECT merchant FROM shopping WHERE merchant > $1 ORDER BY merchant LIMIT 1", after)
		must(err)
		if len(r.Rows) == 0 {
			return "", false
		}
		return r.Rows[0][0].(string), true
	}
	pageSQL := "SELECT " + columnList() + " FROM shopping WHERE merchant = $1 AND id > $2 ORDER BY id LIMIT " + strconv.Itoa(page)
	merchant, more := nextMerchant("")
read:
	for more {
		after := int64(-(1<<53 - 1)) // a KitDB integer lies within ±(2^53−1)
		firstPage := true
		for {
			query := pageSQL
			if firstPage {
				query = strings.Replace(query, "id > $2", "id >= $2", 1)
			}
			rows, err := kit.QueryContext(ctx, query, merchant, after)
			must(err)
			count := 0
			for rows.Next() {
				if limit > 0 && copied >= limit {
					rows.Close()
					break read
				}
				must(rows.Scan(pointers...))
				for i, c := range shoppingColumns {
					a, f, err := convert(c, values[i])
					if err != nil {
						panic(fmt.Sprintf("row %d: %v", copied+1, err))
					}
					arguments[i], fields[i] = a, f
				}
				m, id := arguments[0].(string), arguments[1].(int64)
				if m != merchant {
					panic(fmt.Sprintf("the page of %q returned merchant %q", merchant, m))
				}
				if copied > 0 && (m < lastMerchant || (m == lastMerchant && id <= lastID)) {
					outOfOrder++
				}
				lastMerchant, lastID, after = m, id, id
				sum.advance(fields)
				_, err := stmt.Exec(arguments...)
				must(err)
				copied++
				count++
				perMerchant[m]++
				if copied%20000 == 0 {
					must(stmt.Close())
					must(tx.Commit())
					tx, err = out.Begin()
					must(err)
					stmt, err = tx.Prepare(insertSQL)
					must(err)
				}
				if copied%1000000 == 0 {
					elapsed := time.Since(start)
					fmt.Printf("  %d rows, %v, %.0f rows/s\n", copied, elapsed.Round(time.Second), float64(copied)/elapsed.Seconds())
				}
			}
			must(rows.Err())
			rows.Close()
			firstPage = false
			if count < page {
				break
			}
		}
		merchant, more = nextMerchant(merchant)
	}
	must(stmt.Close())
	must(tx.Commit())
	fmt.Println("rows per merchant:", perMerchant)
	fmt.Printf("copied %d rows in %v; rows out of (merchant, id) order: %d\n", copied, time.Since(start).Round(time.Second), outOfOrder)
	fmt.Printf("checksum of the KitDB stream: %s\n", sum.hex())

	start = time.Now()
	_, err = out.Exec("PRAGMA analysis_limit = 10000")
	must(err)
	_, err = out.Exec("ANALYZE")
	must(err)
	_, err = out.Exec("PRAGMA journal_mode = WAL")
	must(err)
	fmt.Printf("ANALYZE: %v\n", time.Since(start).Round(time.Millisecond))
}

// cmdVerify checks the file, then reads every row back in (merchant, id) order through the same checksum.
func cmdVerify(target string) {
	db, err := sql.Open("sqlite", "file:"+toSlash(target)+"?mode=ro&_pragma=cache_size(-262144)")
	must(err)
	db.SetMaxOpenConns(1)
	defer db.Close()
	start := time.Now()
	var check string
	must(db.QueryRow("PRAGMA quick_check").Scan(&check))
	fmt.Printf("PRAGMA quick_check: %s (%v)\n", check, time.Since(start).Round(time.Second))

	start = time.Now()
	rows, err := db.Query("SELECT " + columnList() + " FROM shopping ORDER BY merchant, id")
	must(err)
	defer rows.Close()
	sum := newChain()
	values := make([]any, len(shoppingColumns))
	pointers := make([]any, len(shoppingColumns))
	for i := range values {
		pointers[i] = &values[i]
	}
	fields := make([][]byte, len(shoppingColumns))
	var read int64
	for rows.Next() {
		must(rows.Scan(pointers...))
		for i, c := range shoppingColumns {
			f, err := canonicalFromSQLite(c, values[i])
			must(err)
			if f == nil && values[i] != nil {
				f = []byte{}
			}
			fields[i] = f
		}
		sum.advance(fields)
		read++
	}
	must(rows.Err())
	fmt.Printf("read back %d rows (expected %d) in %v\n", read, importRows, time.Since(start).Round(time.Second))
	fmt.Printf("checksum read back from SQLite: %s\n", sum.hex())
	fmt.Printf("checksum of the PostgreSQL import: %s (not expected to match, see importChecksum)\n", importChecksum)
}
