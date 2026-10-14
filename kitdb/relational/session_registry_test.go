package relational

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/managed"
	"github.com/kitwork/engine/kitdb/pgwire"
)

func sessionTestNode(t *testing.T, limit int) *PostgresNode {
	t.Helper()
	path := createManagedNodeRoot(t, "shop")
	root, err := managed.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = root.EnableAuth("admin", "session-test-password"); err != nil {
		t.Fatal(err)
	}
	if err = root.SetUser(managed.UserInfo{Name: "reader", Grants: []managed.DatabaseGrant{{Database: "shop", Role: "reader"}}}, "session-test-password"); err != nil {
		t.Fatal(err)
	}
	if err = root.Close(); err != nil {
		t.Fatal(err)
	}
	node, err := OpenPostgresNode(PostgresNodeOptions{Root: path, MaximumSessions: limit})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := node.Close(); err != nil {
			t.Error(err)
		}
	})
	return node
}

func TestSessionRegistryScopeTransactionAndDrain(t *testing.T) {
	node := sessionTestNode(t, 3)
	ctx := context.Background()
	admin, err := node.OpenAuthenticatedDatabase(ctx, "admin", "session-test-password", "shop")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if err := admin.Ping(); err != nil {
		t.Fatal(err)
	}
	reader, err := node.OpenKitSQLDatabase(ctx, "reader", "session-test-password", "shop")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var count int
	if err := reader.QueryRow("SELECT COUNT(*) FROM kitdb_catalog.sessions WHERE username='admin' OR username='reader'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("scope %d: %v", count, err)
	}
	var adapter string
	if err := reader.QueryRow("SELECT adapter FROM kitdb_catalog.sessions").Scan(&adapter); err != nil || adapter != "kitsql" {
		t.Fatalf("adapter %q: %v", adapter, err)
	}
	for _, source := range []string{"SELECT password FROM kitdb_catalog.sessions", "DELETE FROM kitdb_catalog.sessions", "SELECT * FROM kitdb_catalog.sessions UNION SELECT * FROM kitdb_catalog.users"} {
		if _, err := admin.Query(source); err == nil {
			t.Fatalf("unsafe query: %s", source)
		}
	}
	if _, err := admin.Exec("CREATE TABLE pending(id BIGINT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	tx, err := admin.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO pending(id) VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := tx.QueryRow("SELECT transaction_state FROM kitdb_catalog.sessions WHERE username='admin'").Scan(&state); err != nil || state != "transaction" {
		t.Fatalf("state %q: %v", state, err)
	}
	pg, err := node.Authenticate(ctx, pgwire.Startup{Parameters: map[string]string{"user": "admin", "database": "kitdb"}}, "session-test-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Execute(ctx, "BEGIN", nil); err != nil {
		t.Fatal(err)
	}
	if got := node.sessionRows(managed.UserInfo{Name: "admin", Admin: true}); len(got) != 3 {
		t.Fatalf("sessions: %v", got)
	}
	if _, err := node.Authenticate(ctx, pgwire.Startup{Parameters: map[string]string{"user": "admin", "database": "shop"}}, "session-test-password"); postgresNodeWireSQLState(err) != "53300" {
		t.Fatalf("limit: %v", err)
	}
	if err := node.Close(); err != nil {
		t.Fatal(err)
	}
	if len(node.sessionRows(managed.UserInfo{Name: "admin", Admin: true})) != 0 {
		t.Fatal("registry not drained")
	}
	if stats := node.manager.Stats(); stats.ActiveLeases != 0 || !stats.Closed {
		t.Fatalf("manager: %+v", stats)
	}
	if _, err := pg.Execute(ctx, "SELECT 1", nil); err == nil {
		t.Fatal("closed pg session accepted query")
	}
	_ = tx.Rollback()
	_ = admin.Close()
	_ = reader.Close()
	reopened, err := OpenPostgresNode(PostgresNodeOptions{Root: node.root})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	db, err := reopened.OpenAuthenticatedDatabase(ctx, "admin", "session-test-password", "shop")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow("SELECT COUNT(*) FROM pending").Scan(&count); err != nil || count != 0 {
		t.Fatalf("drain rollback %d: %v", count, err)
	}
}

func TestSessionRegistryNativeCancellationAndCapacity(t *testing.T) {
	node := sessionTestNode(t, 1)
	ctx := context.Background()
	account := managed.UserInfo{Name: "admin", Admin: true}
	connector := nativeNodeSQLConnector{node: node, database: "shop", account: &account}
	conn, err := connector.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connector.Connect(ctx); postgresNodeWireSQLState(err) != "53300" {
		t.Fatalf("native limit: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if len(node.sessionRows(account)) != 0 {
		t.Fatal("close leaked record")
	}
	db, err := node.OpenAuthenticatedDatabase(ctx, "admin", "session-test-password", "shop")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxIdleConns(0)
	if _, err := db.Exec("CREATE TABLE cancelled(id BIGINT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	txctx, cancel := context.WithCancel(ctx)
	tx, err := db.BeginTx(txctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO cancelled(id) VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.Now().Add(3 * time.Second)
	for len(node.sessionRows(account)) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(node.sessionRows(account)) != 0 {
		t.Fatal("cancelled transaction retained connection")
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM cancelled").Scan(&count); err != nil || count != 0 {
		t.Fatalf("cancellation rollback %d: %v", count, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				c, err := connector.Connect(ctx)
				if err == nil {
					_ = c.(driver.Conn).Close()
				}
				_ = node.sessionRows(account)
			}
		}()
	}
	wg.Wait()
	if len(node.sessionRows(account)) != 0 {
		t.Fatal("concurrent churn leaked record")
	}
}

func TestSessionRegistryPgwireDisconnectRollback(t *testing.T) {
	node := sessionTestNode(t, 8)
	address, stop := startPostgresNodeTestServer(t, node)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	observer, err := node.OpenAuthenticatedDatabase(ctx, "admin", "session-test-password", "shop")
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	if _, err := observer.ExecContext(ctx, "CREATE TABLE disconnected(id BIGINT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	u := url.URL{Scheme: "postgres", Host: address, Path: "/shop", User: url.UserPassword("admin", "session-test-password"), RawQuery: "sslmode=disable"}
	db, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO disconnected(id) VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := observer.QueryRowContext(ctx, "SELECT transaction_state FROM kitdb_catalog.sessions WHERE adapter='pgwire'").Scan(&state); err != nil || state != "transaction" {
		t.Fatalf("pg transaction %q: %v", state, err)
	}
	if _, err := tx.ExecContext(ctx, "SELECT * FROM missing_table"); err == nil {
		t.Fatal("expected failed transaction")
	}
	if err := observer.QueryRowContext(ctx, "SELECT transaction_state FROM kitdb_catalog.sessions WHERE adapter='pgwire'").Scan(&state); err != nil || state != "failed" {
		t.Fatalf("pg failed %q: %v", state, err)
	}
	if err := conn.Raw(func(raw any) error { return raw.(driver.Conn).Close() }); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		var count int
		if err := observer.QueryRowContext(ctx, "SELECT COUNT(*) FROM kitdb_catalog.sessions WHERE adapter='pgwire'").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnected pgwire session retained")
		}
		time.Sleep(time.Millisecond)
	}
	_ = tx.Rollback()
	var count int
	if err := observer.QueryRowContext(ctx, "SELECT COUNT(*) FROM disconnected").Scan(&count); err != nil || count != 0 {
		t.Fatalf("disconnect rollback %d: %v", count, err)
	}
	stop()
}

func TestSessionRegistryConcurrentNodeDrain(t *testing.T) {
	node := sessionTestNode(t, 16)
	connector := nativeNodeSQLConnector{node: node, database: "shop"}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			conn, err := connector.Connect(context.Background())
			if err == nil {
				_ = conn.Close()
			}
		}()
	}
	close(start)
	if err := node.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if len(node.sessionRows(managed.UserInfo{Admin: true})) != 0 {
		t.Fatal("registration raced drain")
	}
	if got := node.manager.Stats(); got.ActiveLeases != 0 || !got.Closed {
		t.Fatalf("drain: %+v", got)
	}
}

func TestSessionRegistryConcurrentCloseWaitsForRelease(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	conn := &nativeSQLConnection{release: func() error { close(entered); <-release; return nil }}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- conn.Close() }()
	<-entered
	go func() { second <- conn.Close() }()
	select {
	case <-second:
		t.Fatal("second Close returned before lease release")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}
