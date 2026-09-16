package relational

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSQLTriggerPostgresWire(t *testing.T) {
	e := triggerTestOpen(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
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
	for _, query := range []string{
		`CREATE TABLE products (id INTEGER PRIMARY KEY, price INTEGER)`,
		`CREATE TABLE audit (id SERIAL PRIMARY KEY, source_id INTEGER, label TEXT, price INTEGER CHECK (price > 0))`,
		`CREATE TRIGGER z_last AFTER INSERT ON products FOR EACH ROW INSERT INTO audit (source_id,label,price) VALUES (NEW.id,'last',NEW.price)`,
		`CREATE TRIGGER a_first AFTER INSERT ON products FOR EACH ROW WHEN (NEW.price IS NOT NULL) INSERT INTO audit (source_id,label,price) VALUES (NEW.id,'first',NEW.price)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(query, err)
		}
	}
	result, err := db.Exec(`INSERT INTO products (id,price) VALUES ($1,$2)`, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("affected: %d %v", affected, err)
	}
	rows, err := db.Query(`SELECT label FROM audit ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			t.Fatal(err)
		}
		labels = append(labels, label)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	if fmt.Sprint(labels) != "[first last]" {
		t.Fatal(labels)
	}
	var name, timing, condition, action string
	var order int64
	if err := db.QueryRow(`SELECT trigger_name, action_timing, action_condition, action_statement, action_order FROM information_schema.triggers WHERE trigger_name = 'a_first'`).Scan(&name, &timing, &condition, &action, &order); err != nil || name != "a_first" || timing != "AFTER" || order != 1 || !strings.Contains(condition, `NEW."price"`) || !strings.Contains(action, `INSERT INTO "audit"`) {
		t.Fatalf("metadata: %s %s %s %s %d %v", name, timing, condition, action, order, err)
	}
	var kind, oid, tableOID int64
	if err := db.QueryRow(`SELECT tgtype, tgrelid FROM pg_catalog.pg_trigger WHERE tgname = 'a_first'`).Scan(&kind, &tableOID); err != nil || kind != 5 {
		t.Fatalf("pg_trigger: %d %d %v", kind, tableOID, err)
	}
	if err := db.QueryRow(`SELECT oid FROM pg_catalog.pg_class WHERE relname = 'products'`).Scan(&oid); err != nil || oid != tableOID {
		t.Fatalf("table identity: %d %d %v", oid, tableOID, err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO products (id,price) VALUES (2,20)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO products (id,price) VALUES (3,30),(4,-1)`); err == nil {
		t.Fatal("trigger target check bypassed")
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM audit`).Scan(&count); err == nil {
		t.Fatal("failed transaction not aborted")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("rollback: %d %v", count, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM products`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("source rollback: %d %v", count, err)
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DROP TRIGGER a_first ON products`); err == nil {
		t.Fatal("trigger DDL accepted inside data transaction")
	}
	_ = tx.Rollback()
	if _, err := db.Exec(`ALTER TABLE audit RENAME COLUMN source_id TO product_id`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT action_statement FROM information_schema.triggers WHERE trigger_name = 'a_first'`).Scan(&action); err != nil || !strings.Contains(action, `"product_id"`) {
		t.Fatalf("renamed metadata: %s %v", action, err)
	}
	if _, err := db.Exec(`DROP TRIGGER a_first ON products`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE products`); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query(`SELECT trigger_name FROM information_schema.triggers`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("deleted triggers still visible")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
