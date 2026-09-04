package relational

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
)

func functionTestExecute(t *testing.T, engine *Engine, query string, arguments ...any) Result {
	t.Helper()
	result, err := engine.Execute(context.Background(), query, arguments...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return result
}

func TestSQLFunctionLifecycleAndSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "functions.kitdb")
	engine, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engine.Close() }()
	functionTestExecute(t, engine, `CREATE FUNCTION normalize_sku(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN upper(trim(s))`)
	result := functionTestExecute(t, engine, `SELECT normalize_sku(' ab-42 ') AS sku, normalize_sku(NULL) AS missing`)
	if result.Rows[0][0] != "AB-42" || result.Rows[0][1] != nil || result.Columns[0].Kind != "text" {
		t.Fatalf("result = %#v", result)
	}
	functionTestExecute(t, engine, `CREATE TABLE products (id INTEGER PRIMARY KEY, sku TEXT UNIQUE)`)
	functionTestExecute(t, engine, `INSERT INTO products (id, sku) VALUES (1, ' z '), (2, ' a ')`)
	result = functionTestExecute(t, engine, `SELECT normalize_sku(sku) AS normalized FROM products WHERE normalize_sku(sku) <> 'Z' ORDER BY normalize_sku(sku)`)
	if len(result.Rows) != 1 || result.Rows[0][0] != "A" {
		t.Fatalf("projection = %#v", result)
	}
	functionTestExecute(t, engine, `UPDATE products SET sku = normalize_sku(sku) WHERE id = 1`)
	if got := functionTestExecute(t, engine, `SELECT sku FROM products WHERE id = 1`).Rows[0][0]; got != "Z" {
		t.Fatal(got)
	}
	tx, err := engine.BeginTransaction(context.Background(), TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	functionTestExecute(t, engine, `CREATE OR REPLACE FUNCTION normalize_sku(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN lower(trim(s))`)
	old, err := tx.Execute(context.Background(), `SELECT normalize_sku(' Ab ')`)
	if err != nil || old.Rows[0][0] != "AB" {
		t.Fatalf("snapshot = %#v, %v", old, err)
	}
	columns, err := tx.Describe(context.Background(), `SELECT normalize_sku(sku) AS normalized FROM products`)
	if err != nil || len(columns) != 1 || columns[0].Kind != "text" {
		t.Fatalf("describe = %#v, %v", columns, err)
	}
	functionTestExecute(t, engine, `DROP FUNCTION normalize_sku(TEXT)`)
	if _, err := engine.Execute(context.Background(), `SELECT normalize_sku('a')`); err == nil {
		t.Fatal("dropped function survived")
	}
	if _, err := tx.Execute(context.Background(), `SELECT normalize_sku('a')`); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	functionTestExecute(t, engine, `CREATE FUNCTION normalize_sku(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN upper(trim(s))`)
	for _, checkpoint := range []bool{false, true} {
		if checkpoint {
			if _, err := engine.Checkpoint(); err != nil {
				t.Fatal(err)
			}
		}
		if err := engine.Close(); err != nil {
			t.Fatal(err)
		}
		engine, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := functionTestExecute(t, engine, `SELECT normalize_sku($1)`, " xx ").Rows[0][0]; got != "XX" {
			t.Fatal(got)
		}
		tables, err := engine.Tables()
		if err != nil || len(tables) != 1 || tables[0].Name != "products" {
			t.Fatalf("functions leaked into tables: %#v, %v", tables, err)
		}
	}
}

func TestSQLFunctionValidationAndStatementRollback(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "invalid.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	for _, query := range []string{
		`CREATE FUNCTION lower(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN s`,
		`CREATE FUNCTION version() RETURNS TEXT LANGUAGE SQL RETURN 'fake'`,
		`CREATE FUNCTION bad(s TEXT) RETURNS INTEGER LANGUAGE SQL RETURN upper(s)`,
		`CREATE FUNCTION bad(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN upper(4)`,
		`CREATE FUNCTION bad(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN upper(s, s)`,
		`CREATE FUNCTION bad(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN bad(s)`,
		`CREATE FUNCTION bad(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN missing`,
		`CREATE FUNCTION bad(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN $1`,
		`CREATE FUNCTION bad() RETURNS TEXT LANGUAGE SQL RETURN current_timestamp`,
		`CREATE FUNCTION bad() RETURNS TEXT LANGUAGE SQL RETURN pg_read_file('/secret')`,
		`CREATE FUNCTION bad(s TEXT) RETURNS TEXT LANGUAGE plpgsql RETURN s`,
		`CREATE FUNCTION bad(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN s; DELETE FROM products`,
	} {
		if _, err := engine.Execute(context.Background(), query); err == nil {
			t.Fatalf("accepted %s", query)
		}
	}
	catalog, err := engine.database.Catalog()
	if err != nil || len(catalog.Functions) != 0 {
		t.Fatalf("invalid DDL changed catalog: %#v %v", catalog, err)
	}
	functionTestExecute(t, engine, `CREATE FUNCTION clean(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN upper(s)`)
	functionTestExecute(t, engine, `CREATE FUNCTION identity_number(n INTEGER) RETURNS INTEGER LANGUAGE SQL RETURN n`)
	functionTestExecute(t, engine, `CREATE FUNCTION fallback(n INTEGER) RETURNS INTEGER LANGUAGE SQL RETURN coalesce(n, 1 % 0)`)
	if got := functionTestExecute(t, engine, `SELECT fallback(7)`).Rows[0][0]; got != int64(7) {
		t.Fatal(got)
	}
	for _, query := range []string{
		`CREATE FUNCTION clean(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN s`,
		`CREATE OR REPLACE FUNCTION clean(s INTEGER) RETURNS INTEGER LANGUAGE SQL RETURN s`,
		`CREATE FUNCTION wrapper(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN clean(s)`,
		`SELECT clean()`, `SELECT clean(1)`, `SELECT clean('a', 'b')`,
		`DROP FUNCTION clean(INTEGER)`,
	} {
		if _, err := engine.Execute(context.Background(), query); err == nil {
			t.Fatalf("accepted %s", query)
		}
	}
	functionTestExecute(t, engine, `CREATE TABLE products (id INTEGER PRIMARY KEY, sku TEXT UNIQUE)`)
	functionTestExecute(t, engine, `INSERT INTO products (id, sku) VALUES (1, 'a'), (2, 'A')`)
	if _, err := engine.Execute(context.Background(), `UPDATE products SET sku = clean(sku) WHERE id > 0`); err == nil {
		t.Fatal("unique violation accepted")
	}
	if got := functionTestExecute(t, engine, `SELECT sku FROM products WHERE id = 1`).Rows[0][0]; got != "a" {
		t.Fatal("partial update", got)
	}
	if _, err := engine.Execute(context.Background(), `SELECT unknown_function(sku) FROM products LIMIT 0`); err == nil {
		t.Fatal("unknown function accepted on empty result")
	}
	functionTestExecute(t, engine, `DELETE FROM products WHERE clean(sku) = 'A'`)
	functionTestExecute(t, engine, `DROP FUNCTION IF EXISTS missing(TEXT)`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.Execute(ctx, `DROP FUNCTION clean`); err == nil {
		t.Fatal("canceled DDL accepted")
	}
	if got := functionTestExecute(t, engine, `SELECT clean('a')`).Rows[0][0]; got != "A" {
		t.Fatal(got)
	}
}

func TestSQLFunctionBackupAndIndependentDatabases(t *testing.T) {
	root := t.TempDir()
	engine, err := OpenWithOptions(filepath.Join(root, "source.kitdb"), Options{Kernel: kitdbengine.OpenOptions{RetainHistory: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	functionTestExecute(t, engine, `CREATE FUNCTION greet(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN 'hello ' || s`)
	anchor := filepath.Join(root, "backup.kitdb")
	if _, err := engine.database.CreateBackupAnchor(context.Background(), anchor); err != nil {
		t.Fatal(err)
	}
	copy, err := Open(anchor)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	if got := functionTestExecute(t, copy, `SELECT greet('KitDB')`).Rows[0][0]; got != "hello KitDB" {
		t.Fatal(got)
	}
	other, err := Open(filepath.Join(root, "other.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Execute(context.Background(), `SELECT greet('KitDB')`); err == nil {
		t.Fatal("function crossed database boundary")
	}
}

func TestSQLFunctionPostgresWire(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "wire.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- engine.ServePostgres(ctx, listener, PostgresServerOptions{PostgresOptions: PostgresOptions{Database: "wire", User: "kitdb", Password: "test-only"}})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
		_ = engine.Close()
	})
	db, err := sql.Open("postgres", fmt.Sprintf("postgres://kitdb:test-only@%s/wire?sslmode=disable", listener.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE FUNCTION clean(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN upper(trim(s))`); err != nil {
		t.Fatal(err)
	}
	var name, kind string
	if err := db.QueryRow(`SELECT routine_name, data_type FROM information_schema.routines WHERE routine_schema = 'public' AND routine_name = 'clean'`).Scan(&name, &kind); err != nil || name != "clean" || kind != "text" {
		t.Fatalf("routine metadata: %q %q %v", name, kind, err)
	}
	var count int64
	if err := db.QueryRow(`SELECT pronargs FROM pg_catalog.pg_proc WHERE proname = 'clean'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("pg_proc: %d %v", count, err)
	}
	var value string
	if err := db.QueryRow(`SELECT clean($1) AS sku`, " ab ").Scan(&value); err != nil || value != "AB" {
		t.Fatalf("prepared call: %q %v", value, err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(`SELECT clean($1)`, " cd ").Scan(&value); err != nil || value != "CD" {
		t.Fatalf("transaction call: %q %v", value, err)
	}
	if _, err := tx.Exec(`DROP FUNCTION clean`); err == nil {
		t.Fatal("DDL accepted inside data transaction")
	}
	_ = tx.Rollback()
	if err := db.QueryRow(`SELECT clean('ef')`).Scan(&value); err != nil || value != "EF" {
		t.Fatalf("rollback lost function: %q %v", value, err)
	}
	if _, err := db.Exec(`DROP FUNCTION clean`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT clean('ef')`).Scan(&value); err == nil || !strings.Contains(err.Error(), "unknown function") {
		t.Fatalf("drop = %v", err)
	}
}

func TestSQLFunctionBoundsAndConcurrentReplace(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "bounded.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	functionTestExecute(t, engine, `CREATE FUNCTION double_text(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN s || s`)
	for _, input := range []string{strings.Repeat("x", maximumFunctionTextBytes+1), strings.Repeat("x", maximumFunctionTextBytes/2+1)} {
		if _, err := engine.Execute(context.Background(), `SELECT double_text($1)`, input); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("unbounded input/intermediate: %v", err)
		}
	}
	query := "SELECT " + strings.TrimSuffix(strings.Repeat("double_text('x'),", 65), ",")
	if _, err := engine.Execute(context.Background(), query); err == nil || !strings.Contains(err.Error(), "64 user function calls") {
		t.Fatalf("call bound: %v", err)
	}
	functionTestExecute(t, engine, `CREATE FUNCTION number_value(n INTEGER) RETURNS INTEGER LANGUAGE SQL RETURN n + 1`)
	var workers sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < 20; i++ {
				result, err := engine.Execute(context.Background(), `SELECT number_value(1)`)
				if err != nil {
					t.Error(err)
					return
				}
				if result.Rows[0][0] != int64(2) && result.Rows[0][0] != int64(3) {
					t.Errorf("mixed definition: %#v", result)
				}
			}
		}()
	}
	for i := 0; i < 10; i++ {
		functionTestExecute(t, engine, fmt.Sprintf(`CREATE OR REPLACE FUNCTION number_value(n INTEGER) RETURNS INTEGER LANGUAGE SQL RETURN n + %d`, 1+i%2))
	}
	workers.Wait()
}
