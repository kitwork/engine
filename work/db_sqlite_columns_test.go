package work

import (
	"database/sql"
	"strings"
	"testing"
)

// TestColumnKindsKeepTheDatabasesOwnColumns: writing a table's declared kinds leaves the row of a column the code does
// not declare (one added through the port, KITWORK_DATABASE.md "Database là gốc"), and drops the row of a column the
// table no longer has.
//
// CONTROL (05/11/2026): with every row of the table deleted first (until 05/11/2026), "kinds a:text b:integer, want
// a:text b:integer price:decimal(10,2)".
func TestColumnKindsKeepTheDatabasesOwnColumns(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	for _, statement := range []string{
		`CREATE TABLE t (a TEXT, b INTEGER, price INTEGER)`,
		sqliteColumnsSchema,
		`INSERT INTO kitwork_columns (table_name, column_name, kind, precision, scale) VALUES
			('t', 'a', 'json', 0, 0), ('t', 'price', 'decimal', 10, 2), ('t', 'gone', 'bool', 0, 0), ('other', 'x', 'bool', 0, 0)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := writeColumnKinds(database, "t", map[string]*ColumnSpec{"a": {kind: "text", seq: 1}, "b": {kind: "integer", seq: 2}}); err != nil {
		t.Fatal(err)
	}
	rows, err := database.Query(`SELECT column_name || ':' || kind || CASE WHEN kind = 'decimal' THEN '(' || precision || ',' || scale || ')' ELSE '' END
		FROM kitwork_columns WHERE table_name = 't' ORDER BY column_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var kinds []string
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, kind)
	}
	if got := strings.Join(kinds, " "); got != "a:text b:integer price:decimal(10,2)" {
		t.Fatalf("kinds %s, want a:text b:integer price:decimal(10,2)", got)
	}
	var other int
	if err := database.QueryRow(`SELECT count(*) FROM kitwork_columns WHERE table_name = 'other'`).Scan(&other); err != nil || other != 1 {
		t.Fatalf("another table's rows: %d, %v", other, err)
	}
}
