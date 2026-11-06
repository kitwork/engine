package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
)

// structureOf is the structure file of database, read on a connection of its own.
func structureOf(t *testing.T, path string) string {
	t.Helper()
	file, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	connection, err := file.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	text, err := Structure(context.Background(), connection)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// shapeOf is what a file is made of, compared column by column: each table's columns (name, type, not null, default,
// key), its indexes, its foreign keys, and the kinds of kitwork_columns that make a value other than its storage.
func shapeOf(t *testing.T, path string) string {
	t.Helper()
	file, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	query := func(text string, args ...any) []string {
		rows, err := file.Query(text, args...)
		if err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		defer rows.Close()
		columns, _ := rows.Columns()
		var lines []string
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, fmt.Sprint(values...))
		}
		return lines
	}
	var shape []string
	for _, table := range query(`SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'kitwork_%' AND name NOT LIKE 'sqlite_%' ORDER BY name`) {
		shape = append(shape, "table "+table)
		shape = append(shape, query(`SELECT name, type, "notnull", dflt_value, pk FROM pragma_table_xinfo(?)`, table)...)
		shape = append(shape, query(`SELECT name, "unique", partial FROM pragma_index_list(?) ORDER BY name`, table)...)
		shape = append(shape, query(`SELECT "table", "from", "to", on_update, on_delete FROM pragma_foreign_key_list(?)`, table)...)
	}
	shape = append(shape, query(`SELECT type, name FROM sqlite_schema WHERE type IN ('index', 'view') AND name NOT LIKE 'sqlite_%' ORDER BY name`)...)
	// the kinds other than the one a column's storage gives, and the .searchable() weights
	shape = append(shape, query(`SELECT table_name, column_name, kind, precision, scale, search_weight, transform, readonly FROM kitwork_columns
		WHERE kind NOT IN ('integer', 'text', 'float', 'blob') OR search_weight != 0 OR transform != '' OR readonly != 0 ORDER BY table_name, column_name`)...)
	return strings.Join(shape, "\n")
}

// TestStructureFileMakesTheSameFile: a file's structure is written as PostgreSQL writes it (numeric(p, s), boolean,
// json, bigserial, defaults as values), what the port cannot say after "-- SQLite:" (a CHECK on a decimal, a table
// that is not STRICT, a partial index on a decimal, a view), and the kinds the PostgreSQL types do not say after
// "-- KitDB:" (a timestamp in milliseconds, a .searchable() weight); a new file made from it has the same tables,
// columns, keys, indexes and kinds, and its own structure is the same text, byte for byte.
//
// The engine's kinds a PostgreSQL type says (kitid, KitDB's domain, written once before the tables; date; timestamptz)
// are written as those types, with no row; a table's searchable columns are its search index, CREATE INDEX … USING
// search (…) WITH (…). CONTROL (05/11/2026): with kitid not written by its name, "the structure: …
// holds no "id kitid PRIMARY KEY NOT NULL""; with the loader not taking CREATE DOMAIN, "gateway: a structure file makes
// tables and indexes: "CREATE DOMAIN kitid" is neither".
//
// CONTROL (05/11/2026, slice 4): without the rows of a translated table's unsaid kinds, "the structure: … holds no
// "-- KitDB: the kinds its PostgreSQL types do not say""; with the translator's kinds written over those rows when the
// file is made, "the new file differs" (users created datetime and name's weight 2 lost).
//
// The first statement after the header, which names the marker "-- SQLite:" in a line of its own, is translated as
// any other (05/11/2026: it ran untranslated while a mention anywhere counted, the table then not STRICT).
//
// CONTROL (05/11/2026): with the marker found anywhere in the text before a statement, "the new file differs"
// (accounts made as written, "balance numeric(14, 4)", not STRICT).
//
// A column's rules (engine2's kitwork migrate, 06/11/2026), a transform's steps and a column set once, are what no
// PostgreSQL type says: their rows carry them, and a structure with none is written as before. CONTROL (06/11/2026):
// with kindRows leaving the rules out, the structure "holds no "('events', 'day', 'date', 0, 0, 'trim', 0)"".
func TestStructureFileMakesTheSameFile(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	source := filepath.Join(directory, "source.db")
	database, err := kitdbsqlite.Open(ctx, source, kitdbsqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Migrate(ctx, "test",
		`CREATE TABLE accounts (id INTEGER PRIMARY KEY, balance INTEGER NOT NULL DEFAULT 0) STRICT`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, "name" TEXT NOT NULL, "order" INTEGER, active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)), profile TEXT, created INTEGER) STRICT`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE, price INTEGER NOT NULL DEFAULT 150, note TEXT COLLATE NOCASE, picture BLOB, rate REAL, UNIQUE (user_id, note)) STRICT`,
		`CREATE INDEX orders_user ON orders (user_id, price DESC)`,
		`CREATE INDEX orders_cheap ON orders (id) WHERE price < 1000`,
		`CREATE TABLE ledger (id INTEGER PRIMARY KEY, amount INTEGER NOT NULL CHECK (amount > 0)) STRICT`,
		`CREATE TABLE loose (id INTEGER PRIMARY KEY, anything)`,
		`CREATE VIEW rich AS SELECT id FROM orders WHERE price > 100000`,
		`CREATE TABLE events (id TEXT PRIMARY KEY NOT NULL, day TEXT NOT NULL, at TEXT) STRICT`,
		columnsTable,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Write(ctx, "test", func(transaction *kitdbsqlite.Transaction) error {
		for _, row := range [][]any{{"accounts", "balance", "decimal", 14, 4, 0}, {"users", "active", "bool", 0, 0, 0}, {"users", "profile", "json", 0, 0, 0},
			{"orders", "price", "decimal", 10, 2, 0}, {"ledger", "amount", "decimal", 12, 2, 0}, {"users", "name", "text", 0, 0, 2},
			{"users", "created", "datetime", 0, 0, 0}, {"users", "order", "integer", 0, 0, 0},
			{"events", "id", "kitid", 0, 0, 0}, {"events", "day", "date", 0, 0, 0}, {"events", "at", "datetime", 0, 0, 0}} {
			if _, err := transaction.Exec(`INSERT INTO kitwork_columns (table_name, column_name, kind, precision, scale, search_weight) VALUES (?, ?, ?, ?, ?, ?)`, row...); err != nil {
				return err
			}
		}
		// the rules (engine2's kitwork migrate, 06/11/2026): a column a transform keeps, a column set once
		for _, row := range [][]any{{"events", "day", "date", "trim", 0}, {"orders", "user_id", "integer", "", 1}} {
			if _, err := transaction.Exec(`INSERT OR REPLACE INTO kitwork_columns (table_name, column_name, kind, precision, scale, transform, readonly) VALUES (?, ?, ?, 0, 0, ?, ?)`, row...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	text := structureOf(t, source)
	for _, want := range []string{StructureHeader, "id bigserial PRIMARY KEY", `"order" bigint`, "active boolean NOT NULL DEFAULT true CHECK",
		"profile json", "price numeric(10, 2) NOT NULL DEFAULT 1.50", "note text COLLATE NOCASE", "picture bytea", "rate double precision",
		"-- SQLite: a CHECK reads a decimal column", "-- SQLite: a table that is not STRICT", "-- SQLite: its WHERE reads a decimal column",
		"-- SQLite: a view", "CREATE INDEX orders_user ON orders (user_id, price DESC);",
		"-- KitDB: the kinds its PostgreSQL types do not say", "('users', 'created', 'datetime', 0, 0)",
		"CREATE INDEX users_search ON users USING search (name) WITH (name = 2);", "CREATE DOMAIN kitid AS text;",
		"id kitid PRIMARY KEY NOT NULL", "day date NOT NULL", "at timestamptz",
		"('events', 'day', 'date', 0, 0, 'trim', 0)", "('orders', 'user_id', 'integer', 0, 0, '', 1)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the structure:\n%s\nholds no %q", text, want)
		}
	}

	made := filepath.Join(directory, "made.db")
	copyOf, err := kitdbsqlite.Open(ctx, made, kitdbsqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer copyOf.Close()
	if err := LoadStructure(ctx, copyOf, "test", text); err != nil {
		t.Fatal(err)
	}
	if got, want := shapeOf(t, made), shapeOf(t, source); got != want {
		t.Fatalf("the new file differs:\n%s\nagainst\n%s", got, want)
	}
	if again := structureOf(t, made); again != text {
		t.Fatalf("the new file's structure differs:\n%s\nagainst\n%s", again, text)
	}
	if empty := filepath.Join(directory, "empty.db"); true {
		file, err := kitdbsqlite.Open(ctx, empty, kitdbsqlite.Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if got := structureOf(t, empty); got != "" {
			t.Fatalf("a file of no table has the structure %q", got)
		}
	}
	_ = os.Remove(filepath.Join(directory, "unused"))
}
