package relational

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"reflect"
	"testing"
	"time"
)

func TestMutationExpressionPostgresWire(t *testing.T) {
	e := triggerTestOpen(t)
	for _, query := range []string{
		`CREATE FUNCTION clean(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN upper(trim(s))`,
		`CREATE FUNCTION taxed(n NUMERIC) RETURNS NUMERIC LANGUAGE SQL RETURN n + 0.25`,
		`CREATE TABLE items (id SERIAL PRIMARY KEY, label TEXT, amount NUMERIC(20,2))`,
		`CREATE TABLE audit (id SERIAL PRIMARY KEY, source_id INTEGER, amount NUMERIC(20,2))`,
		`CREATE TRIGGER inserted AFTER INSERT ON items FOR EACH ROW INSERT INTO audit (source_id,amount) VALUES (NEW.id,NEW.amount)`,
		`CREATE TRIGGER updated AFTER UPDATE ON items FOR EACH ROW INSERT INTO audit (source_id,amount) VALUES (NEW.id,NEW.amount)`,
	} {
		functionTestExecute(t, e, query)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	done := make(chan error, 1)
	go func() {
		done <- e.ServePostgres(ctx, listener, PostgresServerOptions{PostgresOptions: PostgresOptions{Database: "wire", User: "kitdb", Password: "test-only"}})
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
	})
	db, err := sql.Open("postgres", fmt.Sprintf("postgres://kitdb:test-only@%s/wire?sslmode=disable", listener.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	statement, err := db.PrepareContext(ctx, `INSERT INTO items (label,amount) VALUES (clean($1),taxed(CAST($2 AS NUMERIC))) RETURNING id,clean(label) AS normalized,taxed(amount) AS taxed`)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	var id int64
	var label, amount string
	if err := statement.QueryRowContext(ctx, " ab ", "9007199254740993.25").Scan(&id, &label, &amount); err != nil || id != 1 || label != "AB" || amount != "9007199254740993.75" {
		t.Fatalf("prepared INSERT: %d %s %s %v", id, label, amount, err)
	}
	if err := statement.QueryRowContext(ctx, " cd ", "1").Scan(&id, &label, &amount); err != nil || id != 2 || label != "CD" || amount != "1.5" {
		t.Fatalf("reuse: %d %s %s %v", id, label, amount, err)
	}
	if err := db.QueryRowContext(ctx, `UPDATE items SET amount = taxed(amount) WHERE id = $1 RETURNING clean(label),taxed(amount)`, 2).Scan(&label, &amount); err != nil || label != "CD" || amount != "1.75" {
		t.Fatalf("UPDATE: %s %s %v", label, amount, err)
	}
	rows, err := db.QueryContext(ctx, `DELETE FROM items WHERE id = $1 RETURNING clean(label) AS normalized, taxed(amount) AS total`, 999)
	if err != nil {
		t.Fatal(err)
	}
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"normalized", "total"}) {
		t.Fatalf("empty columns: %v %v", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "TEXT" || types[1].DatabaseTypeName() != "NUMERIC" {
		t.Fatalf("column types: %v %v", types, err)
	}
	if rows.Next() {
		t.Fatal("unexpected empty result row")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("audit: %d %v", count, err)
	}
	// The server must not send a partial RETURNING result or publish trigger
	// writes when evaluation fails after the source mutation was staged.
	if err := db.QueryRowContext(ctx, `UPDATE items SET amount = amount + 1 RETURNING 10 / (id - 2)`).Scan(&amount); err == nil {
		t.Fatal("late RETURNING failure accepted")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("audit rollback: %d %v", count, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRowContext(ctx, `INSERT INTO items (label,amount) VALUES (clean('tx'),1 + 2) RETURNING clean(label)`).Scan(&label); err != nil || label != "TX" {
		t.Fatalf("transaction INSERT: %s %v", label, err)
	}
	if err := tx.QueryRowContext(ctx, `DELETE FROM items RETURNING 10 / (id - 2)`).Scan(&amount); err == nil {
		t.Fatal("transaction failure accepted")
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM items`).Scan(&count); err == nil {
		t.Fatal("failed wire transaction did not abort")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("source rollback: %d %v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("audit rollback: %d %v", count, err)
	}
	if err := db.QueryRowContext(ctx, `DELETE FROM items WHERE id = 2 RETURNING id, clean(label), taxed(amount)`).Scan(&id, &label, &amount); err != nil || id != 2 || label != "CD" || amount != "1.75" {
		t.Fatalf("DELETE: %d %s %s %v", id, label, amount, err)
	}
	readOnly, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := readOnly.QueryRowContext(ctx, `INSERT INTO items (label,amount) VALUES (clean('blocked'),1) RETURNING id`).Scan(&id); err == nil {
		t.Fatal("read-only write accepted")
	}
	_ = readOnly.Rollback()
}
