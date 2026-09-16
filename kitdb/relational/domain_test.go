package relational

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestSQLDomainLifecycleAndAtomicWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "domain.kitdb")
	engine, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engine.Close() }()
	exec := func(query string) Result { return functionTestExecute(t, engine, query) }
	fail := func(query string) {
		t.Helper()
		if _, err := engine.Execute(context.Background(), query); err == nil {
			t.Fatalf("accepted: %s", query)
		}
	}
	exec(`CREATE DOMAIN amount AS NUMERIC(12,2) DEFAULT 0 NOT NULL CHECK (VALUE >= 0)`)
	exec(`CREATE DOMAIN short_code AS VARCHAR(4) CHECK (VALUE <> '')`)
	exec(`CREATE TABLE products (id SERIAL PRIMARY KEY, price amount, code short_code)`)
	exec(`CREATE TABLE special (id INTEGER PRIMARY KEY, price amount DEFAULT 9)`)
	exec(`INSERT INTO products (code) VALUES ('AB')`)
	exec(`INSERT INTO products (price, code) VALUES (12.25, NULL)`)
	exec(`INSERT INTO special (id) VALUES (1)`)
	if got := fmt.Sprint(exec(`SELECT price FROM special`).Rows[0][0]); got != "9" {
		t.Fatalf("column default = %s", got)
	}
	fail(`INSERT INTO products (price) VALUES (NULL)`)
	fail(`INSERT INTO products (code) VALUES ('')`)
	fail(`INSERT INTO products (code) VALUES ('TOOLONG')`)
	fail(`INSERT INTO products (price) VALUES (4), (-1), (5)`)
	if got := exec(`SELECT COUNT(*) FROM products`).Rows[0][0]; got != int64(2) {
		t.Fatalf("partial failed insert: %v", got)
	}
	fail(`UPDATE products SET price = price - 1`)
	if got := fmt.Sprint(exec(`SELECT SUM(price) FROM products`).Rows[0][0]); got != "12.25" {
		t.Fatalf("partial failed update: %v", got)
	}
	tx, err := engine.BeginTransaction(context.Background(), TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Execute(context.Background(), `UPDATE products SET price = 5`); err != nil {
		t.Fatal(err)
	}
	savepoint, err := tx.Savepoint()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Execute(context.Background(), `UPDATE products SET price = -1`); err == nil {
		t.Fatal("domain check bypassed inside transaction")
	}
	if err := tx.RollbackTo(savepoint); err != nil {
		t.Fatal(err)
	}
	inside, err := tx.Execute(context.Background(), `SELECT SUM(price) FROM products`)
	if err != nil || fmt.Sprint(inside.Rows[0][0]) != "10" {
		t.Fatalf("native savepoint: %#v %v", inside, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(exec(`SELECT SUM(price) FROM products`).Rows[0][0]); got != "12.25" {
		t.Fatalf("rollback: %v", got)
	}
	fail(`DROP DOMAIN amount`)
	fail(`CREATE TABLE bad (id INTEGER PRIMARY KEY, x absent_domain)`)
	fail(`ALTER TABLE products ADD COLUMN extra amount`)
	exec(`ALTER TABLE products RENAME COLUMN price TO total`)
	fail(`UPDATE products SET total = -1`)
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
		fail(`UPDATE products SET total = -1`)
		fail(`DROP DOMAIN amount`)
		catalog, err := engine.database.Catalog()
		if err != nil || len(catalog.Domains) != 2 {
			t.Fatalf("recovery catalog: %#v, %v", catalog, err)
		}
	}
	// Typed Go plans pass through the same domain resolver, not a SQL-only shim.
	integer, _ := kitdbsql.LookupKind("int32")
	_, err = engine.ExecutePlan(context.Background(), &kitdbsql.ParsedStatement{Kind: kitdbsql.StatementCreate,
		CreateTable: &kitdbsql.CreateTableStatement{Name: "native", PrimaryKey: []string{"id"}, Columns: []kitdbsql.ColumnDefinition{
			{Name: "id", Type: integer}, {Name: "cost", DomainName: "amount"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fail(`INSERT INTO native (id, cost) VALUES (1, -4)`)
	exec(`DROP TABLE native`)
	exec(`DROP TABLE products`)
	fail(`DROP DOMAIN amount`)
	exec(`DROP TABLE special`)
	exec(`DROP DOMAIN amount`)
	exec(`DROP DOMAIN IF EXISTS amount`)
	exec(`DROP DOMAIN short_code`)
}

func TestSQLDomainValidationAndIsolation(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "validation.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	for _, query := range []string{
		`CREATE DOMAIN integer AS INTEGER`,
		`CREATE DOMAIN d AS INTEGER CHECK (other > 0)`,
		`CREATE DOMAIN d AS INTEGER CHECK (VALUE)`,
		`CREATE DOMAIN d AS INTEGER CHECK (VALUE AND TRUE)`,
		`CREATE DOMAIN d AS INTEGER CHECK (VALUE > 'oops')`,
		`CREATE DOMAIN d AS INTEGER DEFAULT 'bad'`,
		`CREATE DOMAIN d AS INTEGER CHECK (FALSE) CONSTRAINT c CHECK (VALUE = 1) CONSTRAINT c CHECK (VALUE = 2)`,
		`CREATE DOMAIN d AS JSON`,
		`CREATE DOMAIN d AS other_domain`,
	} {
		if _, err := engine.Execute(context.Background(), query); err == nil {
			t.Errorf("accepted: %s", query)
		}
	}
	functionTestExecute(t, engine, `CREATE DOMAIN nullable_positive AS INTEGER CHECK (VALUE > 0)`)
	functionTestExecute(t, engine, `CREATE TABLE partitioned (id INTEGER PRIMARY KEY, n nullable_positive ANALYTICS) PARTITION BY HASH (n)`)
	functionTestExecute(t, engine, `CREATE DOMAIN label AS TEXT CHECK (VALUE <> '')`)
	functionTestExecute(t, engine, `CREATE TABLE labels (id INTEGER PRIMARY KEY, name label SEARCHABLE)`)
	for _, query := range []string{
		`CREATE TABLE invalid_search (id INTEGER PRIMARY KEY, n nullable_positive SEARCHABLE)`,
		`CREATE TABLE invalid_partition (id INTEGER PRIMARY KEY, n label) PARTITION BY HASH (n)`,
	} {
		if _, err := engine.Execute(context.Background(), query); err == nil {
			t.Fatalf("unvalidated domain modifiers: %s", query)
		}
	}
	functionTestExecute(t, engine, `CREATE TABLE valid (id INTEGER PRIMARY KEY, n nullable_positive DEFAULT NULL)`)
	functionTestExecute(t, engine, `INSERT INTO valid (id) VALUES (1)`)
	if got := functionTestExecute(t, engine, `SELECT n FROM valid`).Rows[0][0]; got != nil {
		t.Fatal(got)
	}
	other, err := Open(filepath.Join(t.TempDir(), "other.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Execute(context.Background(), `CREATE TABLE nope (id INTEGER PRIMARY KEY, n nullable_positive)`); err == nil {
		t.Fatal("domain leaked across databases")
	}
	// The kernel protects dependencies even if another frontend bypasses SQL DDL.
	catalog, err := engine.database.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := engine.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := tx.DeleteDomain(kitdbsql.StableSchemaID("domain", "nullable_positive")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err == nil {
		t.Fatal("kernel deleted a referenced domain")
	}
	for _, entry := range catalog.Domains {
		mutated := entry
		mutated.Definition = append([]byte(nil), entry.Definition...)
		mutated.Hash = "wrong"
		if _, err := decodeStoredDomain(mutated); err == nil {
			t.Fatal("accepted mismatched domain hash")
		}
	}
}

func TestSQLDomainCrashBackupAndReadOnly(t *testing.T) {
	if path := os.Getenv("KITDB_DOMAIN_CRASH_CHILD"); path != "" {
		engine, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		functionTestExecute(t, engine, `CREATE DOMAIN amount AS NUMERIC(12,2) CHECK (VALUE >= 0)`)
		functionTestExecute(t, engine, `CREATE TABLE payments (id INTEGER PRIMARY KEY, value amount)`)
		functionTestExecute(t, engine, `INSERT INTO payments (id, value) VALUES (1, 12.25)`)
		os.Exit(32)
	}
	path := filepath.Join(t.TempDir(), "crash.kitdb")
	child := exec.Command(os.Args[0], "-test.run=^TestSQLDomainCrashBackupAndReadOnly$")
	child.Env = append(os.Environ(), "KITDB_DOMAIN_CRASH_CHILD="+path)
	output, err := child.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 32 {
		t.Fatalf("crash child: %v %s", err, output)
	}
	engine, err := OpenWithOptions(path, Options{Kernel: kitdbengine.OpenOptions{RetainHistory: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if _, err := engine.Execute(context.Background(), `UPDATE payments SET value = -1`); err == nil {
		t.Fatal("recovery lost CHECK")
	}
	anchor := filepath.Join(t.TempDir(), "backup.kitdb")
	if _, err := engine.database.CreateBackupAnchor(context.Background(), anchor); err != nil {
		t.Fatal(err)
	}
	if _, err := kitdbengine.VerifyBackupAnchor(context.Background(), anchor); err != nil {
		t.Fatal(err)
	}
	backup, err := Open(anchor)
	if err != nil {
		t.Fatal(err)
	}
	if got := functionTestExecute(t, backup, `SELECT value FROM payments`).Rows[0][0]; fmt.Sprint(got) != "12.25" {
		t.Fatal(got)
	}
	if _, err := backup.executeWithSequences(context.Background(), `CREATE DOMAIN forbidden AS INTEGER`, nil, nil, true); err == nil {
		t.Fatal("read-only execution accepted DDL")
	}
	defer backup.Close()
	if _, err := backup.Execute(context.Background(), `UPDATE payments SET value = -1`); err == nil {
		t.Fatal("backup lost CHECK")
	}
	functionTestExecute(t, backup, `CREATE TABLE more (id INTEGER PRIMARY KEY, value amount)`)
	if _, err := backup.Execute(context.Background(), `INSERT INTO more (id, value) VALUES (1, -1)`); err == nil {
		t.Fatal("backup lost reusable domain")
	}
}

func TestSQLDomainPostgresWire(t *testing.T) {
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
	db.SetMaxOpenConns(1)
	for _, query := range []string{
		`CREATE DOMAIN positive AS INTEGER DEFAULT 1 CHECK (VALUE > 0)`,
		`CREATE TABLE events (id SERIAL PRIMARY KEY, visits positive)`,
		`INSERT INTO events DEFAULT VALUES`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(query, err)
		}
	}
	var value int
	if err := db.QueryRow(`SELECT visits FROM events`).Scan(&value); err != nil || value != 1 {
		t.Fatalf("value: %v %v", value, err)
	}
	var name, kind string
	if err := db.QueryRow(`SELECT domain_name, data_type FROM information_schema.domains WHERE domain_name = 'positive'`).Scan(&name, &kind); err != nil || name != "positive" || kind != "integer" {
		t.Fatalf("domain metadata: %s %s %v", name, kind, err)
	}
	if err := db.QueryRow(`SELECT domain_name FROM information_schema.columns WHERE table_name = 'events' AND column_name = 'visits'`).Scan(&name); err != nil || name != "positive" {
		t.Fatalf("column metadata: %s %v", name, err)
	}
	var oid, base int64
	if err := db.QueryRow(`SELECT oid, typtype, typbasetype FROM pg_catalog.pg_type WHERE typname = 'positive'`).Scan(&oid, &kind, &base); err != nil || kind != "d" || base != 23 {
		t.Fatalf("pg_type: %d %s %d %v", oid, kind, base, err)
	}
	var attribute int64
	if err := db.QueryRow(`SELECT atttypid FROM pg_catalog.pg_attribute WHERE attname = 'visits'`).Scan(&attribute); err != nil || attribute != oid {
		t.Fatalf("attribute: %d %d %v", attribute, oid, err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO events (visits) VALUES ($1)`, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE events SET visits = -1`); err == nil {
		t.Fatal("domain check bypassed")
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&value); err == nil {
		t.Fatal("failed PostgreSQL transaction did not enter aborted state")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&value); err != nil || value != 1 {
		t.Fatalf("rollback: %d %v", value, err)
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DROP DOMAIN positive`); err == nil {
		t.Fatal("DOMAIN DDL accepted inside data transaction")
	}
	_ = tx.Rollback()
	if _, err := db.Exec(`DROP DOMAIN positive`); err == nil || !strings.Contains(err.Error(), "depends on it") {
		t.Fatalf("drop: %v", err)
	}
}
