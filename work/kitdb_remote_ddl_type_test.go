package work

import "testing"

func TestKitSQLCreateTableAcceptsArrayType(t *testing.T) {
	statement, err := parseKitSQL(
		`CREATE TABLE products (id TEXT PRIMARY KEY, images ARRAY NOT NULL)`,
		kitSQLBindings{},
	)
	if err != nil {
		t.Fatalf("parse CREATE TABLE: %v", err)
	}
	if statement.createTable == nil || len(statement.createTable.columns) != 2 {
		t.Fatalf("unexpected CREATE TABLE plan: %#v", statement.createTable)
	}
	if got := statement.createTable.columns[1].spec.kind; got != "array" {
		t.Fatalf("ARRAY kind = %q, want array", got)
	}
}
