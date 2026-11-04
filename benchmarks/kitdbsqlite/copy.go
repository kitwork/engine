package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func toSlash(path string) string { return strings.ReplaceAll(path, `\`, "/") }

func pageSize() string {
	if v := os.Getenv("KITDBSQLITE_PAGE"); v != "" {
		return v
	}
	return "16384" // bytes per row on this table: 4 KiB 1,695 · 8 KiB 1,593 · 16 KiB 1,469 · 32 KiB 1,414
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
