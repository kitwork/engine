package relational

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/managed"
)

func waitSessionGone(t *testing.T, node *PostgresNode, id int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		node.mu.Lock()
		_, present := node.sessionRegistry[id]
		node.mu.Unlock()
		if !present {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("termination did not drain")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestKillSessionNativePermissionsAndRollback(t *testing.T) {
	node := sessionTestNode(t, 16)
	ctx := context.Background()
	open := func(user string) *sql.DB {
		db, err := node.OpenAuthenticatedDatabase(ctx, user, "session-test-password", "shop")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	admin, victim, reader := open("admin"), open("admin"), open("reader")
	if _, err := admin.Exec("CREATE TABLE killed(id BIGINT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	conn, err := victim.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO killed(id) VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := tx.QueryRow("SELECT session_id FROM kitdb_catalog.sessions WHERE transaction_state='transaction'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	command := fmt.Sprintf("KILL SESSION %d", id)
	if _, err := reader.Exec(command); postgresNodeWireSQLState(err) != "42501" {
		t.Fatalf("cross-user: %v", err)
	}
	if _, err := tx.Exec(command); postgresNodeWireSQLState(err) != "25001" {
		t.Fatalf("transaction: %v", err)
	}
	if _, err := admin.Exec(command); err != nil {
		t.Fatal(err)
	}
	waitSessionGone(t, node, id)
	if err := tx.Commit(); err == nil {
		t.Fatal("killed transaction committed")
	}
	var count int
	if err := admin.QueryRow("SELECT COUNT(*) FROM killed").Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback %d: %v", count, err)
	}
	var self int64
	if err := reader.QueryRow("SELECT session_id FROM kitdb_catalog.sessions").Scan(&self); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Exec(fmt.Sprintf("KILL SESSION %d", self)); postgresNodeWireSQLState(err) != "55000" {
		t.Fatalf("self: %v", err)
	}
	other := open("reader")
	if err := other.Ping(); err != nil {
		t.Fatal(err)
	}
	var otherID int64
	if err := reader.QueryRow("SELECT session_id FROM kitdb_catalog.sessions WHERE session_id<>$1", self).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Exec(fmt.Sprintf("KILL SESSION %d", otherID)); err != nil {
		t.Fatal(err)
	}
	waitSessionGone(t, node, otherID)
	if _, err := admin.Exec(fmt.Sprintf("ALTER USER reader NOLOGIN")); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Exec(command); postgresNodeWireSQLState(err) != "42501" {
		t.Fatalf("disabled authority: %v", err)
	}
	if len(node.sessionRows(managed.UserInfo{Name: "reader"})) != 1 {
		t.Fatal("NOLOGIN silently killed session")
	}
}

func TestKillSessionPgwireTransportAndDescribe(t *testing.T) {
	node := sessionTestNode(t, 16)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	address, stop := startPostgresNodeTestServer(t, node)
	defer stop()
	u := url.URL{Scheme: "postgres", Host: address, Path: "/shop", User: url.UserPassword("admin", "session-test-password"), RawQuery: "sslmode=disable"}
	victim, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer victim.Close()
	controller, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	if _, err := controller.ExecContext(ctx, "CREATE TABLE killed_pg(id BIGINT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	conn, err := victim.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO killed_pg(id) VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := controller.QueryRowContext(ctx, "SELECT session_id FROM kitdb_catalog.sessions WHERE transaction_state='transaction'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	command := fmt.Sprintf("KILL SESSION %d", id)
	stmt, err := controller.PrepareContext(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	node.mu.Lock()
	exists := node.sessionRegistry[id] != nil
	node.mu.Unlock()
	if !exists {
		t.Fatal("Describe terminated session")
	}
	if _, err := stmt.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	waitSessionGone(t, node, id)
	if err := tx.Commit(); err == nil {
		t.Fatal("pg killed transaction committed")
	}
	var count int
	if err := controller.QueryRowContext(ctx, "SELECT COUNT(*) FROM killed_pg").Scan(&count); err != nil || count != 0 {
		t.Fatalf("pg rollback %d: %v", count, err)
	}
}

func TestKillSessionClosesNativeRowStream(t *testing.T) {
	node := sessionTestNode(t, 8)
	ctx := context.Background()
	controller, err := node.OpenAuthenticatedDatabase(ctx, "admin", "session-test-password", "shop")
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	if _, err := controller.Exec("CREATE TABLE streaming(id BIGINT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Exec("INSERT INTO streaming(id) VALUES(1),(2)"); err != nil {
		t.Fatal(err)
	}
	pool, err := node.OpenAuthenticatedDatabase(ctx, "reader", "session-test-password", "shop")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var id int64
	if err := conn.Raw(func(raw any) error { id = raw.(*nativeSQLConnection).record.id; return nil }); err != nil {
		t.Fatal(err)
	}
	rows, err := conn.QueryContext(ctx, "SELECT id FROM streaming")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if _, err := controller.Exec(fmt.Sprintf("KILL SESSION %d", id)); err != nil {
		t.Fatal(err)
	}
	waitSessionGone(t, node, id)
	if rows.Next() {
		t.Fatal("terminated row stream returned data")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := node.Close(); err != nil {
		t.Fatal(err)
	}
	if got := node.manager.Stats(); got.ActiveLeases != 0 {
		t.Fatalf("stream lease leaked: %+v", got)
	}
}
