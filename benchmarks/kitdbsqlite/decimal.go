package main

import (
	"database/sql"
	"fmt"
)

// cmdDecimal shows why a decimal cannot stay TEXT in SQLite: a TEXT column compares and orders as text, and
// SUM over it returns a REAL. The same values as INTEGER scaled by 10^2 compare, order and sum exactly.
func cmdDecimal() {
	db, err := sql.Open("sqlite", ":memory:")
	must(err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE t (text_value TEXT NOT NULL, scaled INTEGER NOT NULL) STRICT`)
	must(err)
	for _, v := range []struct {
		text   string
		scaled int64
	}{{"4.5", 450}, {"10", 1000}, {"9.9", 990}, {"0.1", 10}, {"0.2", 20}} {
		_, err = db.Exec(`INSERT INTO t VALUES (?, ?)`, v.text, v.scaled)
		must(err)
	}
	show := func(label, query string) {
		fmt.Printf("%-52s %v\n", label, querySQLite(db, query))
	}
	show("TEXT: > 4.5 (expected 10, 9.9)", `SELECT text_value FROM t WHERE text_value > 4.5 ORDER BY rowid`)
	show("TEXT: ORDER BY (expected 0.1 0.2 4.5 9.9 10)", `SELECT text_value FROM t ORDER BY text_value`)
	show("TEXT: SUM and its type (expected 24.7, exact)", `SELECT SUM(text_value), typeof(SUM(text_value)) FROM t`)
	show("INTEGER x100: > 450 (expected 1000, 990)", `SELECT scaled FROM t WHERE scaled > 450 ORDER BY rowid`)
	show("INTEGER x100: ORDER BY (expected 10 20 450 990 1000)", `SELECT scaled FROM t ORDER BY scaled`)
	show("INTEGER x100: SUM and its type (2470 = 24.70)", `SELECT SUM(scaled), typeof(SUM(scaled)) FROM t`)
}
