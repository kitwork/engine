package relational

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/pgwire"
)

func TestReverseForeignKeyPostgresWire(t *testing.T) {
	e := triggerTestOpen(t)
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
	exec := func(q string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, q := range []string{`CREATE TABLE parents(id INTEGER PRIMARY KEY, code TEXT UNIQUE)`,
		`CREATE TABLE children(id INTEGER PRIMARY KEY, code TEXT REFERENCES parents(code) ON UPDATE RESTRICT ON DELETE RESTRICT)`,
		`CREATE INDEX child_code ON children(code)`, `INSERT INTO parents VALUES(1,'A'),(2,'B')`, `INSERT INTO children VALUES(1,'A')`,
		`CREATE TABLE audit(id SERIAL PRIMARY KEY, parent INTEGER)`, `CREATE TRIGGER changed AFTER UPDATE ON parents FOR EACH ROW INSERT INTO audit(parent) VALUES(NEW.id)`} {
		exec(q)
	}
	state := func(err error, want string) {
		t.Helper()
		var sqlErr interface{ SQLState() string }
		if !errors.As(err, &sqlErr) || sqlErr.SQLState() != want {
			t.Fatalf("expected SQLSTATE %s: %v", want, err)
		}
	}
	// The same FK error class covers missing parents and reverse constraints.
	_, err = db.ExecContext(ctx, `INSERT INTO children VALUES(9,'missing')`)
	state(err, "23503")
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SAVEPOINT retry_point`); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`UPDATE parents SET code=$1 WHERE id=$2 RETURNING code`, `DELETE FROM parents WHERE code=$1 AND id=$2 RETURNING code`, `INSERT INTO parents VALUES($2,$1) ON CONFLICT(id) DO UPDATE SET code=excluded.code RETURNING code`} {
		statement, err := tx.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		code := "X"
		if q[0] == 'D' {
			code = "A"
		}
		var value string
		err = statement.QueryRowContext(ctx, code, 1).Scan(&value)
		state(err, "23503")
		statement.Close()
		_, err = tx.ExecContext(ctx, `SELECT * FROM parents`)
		state(err, "25P02")
		if _, err := tx.ExecContext(ctx, `ROLLBACK TO retry_point`); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE parents SET code='BB' WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM children`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM parents`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM parents`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	// An AFTER action cannot re-create an orphan after the reverse check.
	exec(`INSERT INTO parents VALUES(1,'A')`)
	exec(`CREATE TRIGGER blocked AFTER DELETE ON parents FOR EACH ROW INSERT INTO children(id,code) VALUES(OLD.id,OLD.code)`)
	_, err = db.ExecContext(ctx, `DELETE FROM parents`)
	state(err, "23503")
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM parents`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	exec(`DROP TRIGGER blocked ON parents`)
	// Late RETURNING failure still restores reverse-checked mutations/audit.
	err = db.QueryRowContext(ctx, `UPDATE parents SET code='Z' RETURNING 1/(id-1)`).Scan(&count)
	if err == nil {
		t.Fatal("RETURNING failure accepted")
	}
	var code string
	if err := db.QueryRowContext(ctx, `SELECT code FROM parents WHERE id=1`).Scan(&code); err != nil || code != "A" {
		t.Fatal(code, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	// Mapping a bounded check failure must never look like an FK violation.
	var protocolError *pgwire.Error
	if err := postgresError(ErrForeignKeyCheckLimit); !errors.As(err, &protocolError) || protocolError.Code != "54000" {
		t.Fatal(err)
	}
}
