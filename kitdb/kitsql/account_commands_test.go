package kitsql_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/kitsql"
	"github.com/kitwork/engine/kitdb/managed"
	"github.com/kitwork/engine/kitdb/relational"
)

func TestSQLAccountAdministrationAdapters(t *testing.T) {
	path := t.TempDir()
	if _, err := managed.Init(path); err != nil {
		t.Fatal(err)
	}
	root, err := managed.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	e, err := relational.Open(filepath.Join(path, "shop", "data.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(context.Background(), "CREATE TABLE items(id BIGINT PRIMARY KEY, value TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(context.Background(), "INSERT INTO items VALUES(1,'original')"); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Attach("shop"); err != nil {
		t.Fatal(err)
	}
	const password = "account-command-secret"
	if err := root.EnableAuth("admin", password); err != nil {
		t.Fatal(err)
	}
	if err := root.SetUser(managed.UserInfo{Name: "reader", Grants: []managed.DatabaseGrant{{Database: "shop", Role: "reader"}}}, password); err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var traces []string
	httpEntered, httpRelease := make(chan struct{}), make(chan struct{})
	node, err := relational.OpenPostgresNode(relational.PostgresNodeOptions{Root: path, Trace: func(_ string, source string, _ int) {
		mu.Lock()
		traces = append(traces, source)
		mu.Unlock()
		if source == "SELECT value FROM items WHERE id=99" {
			close(httpEntered)
			<-httpRelease
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	pgCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- node.ServePostgres(pgCtx, listener, relational.PostgresServerOptions{MaxConnections: 32})
	}()
	defer func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not drain")
		}
	}()
	handler, err := kitsql.NewHandler(kitsql.HandlerOptions{Open: kitsql.OpenNodeDatabase(node), AllowInsecureLocal: true})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	rawOpen := func(adapter, user, database, credential string) (*sql.DB, error) {
		var db *sql.DB
		var err error
		switch adapter {
		case "native":
			db, err = node.OpenAuthenticatedDatabase(ctx, user, credential, database)
		case "pg":
			u := url.URL{Scheme: "postgres", Host: listener.Addr().String(), Path: "/" + database, User: url.UserPassword(user, credential), RawQuery: "sslmode=disable"}
			db, err = sql.Open("postgres", u.String())
		case "http":
			u := url.URL{Scheme: "kitsql", Host: strings.TrimPrefix(httpServer.URL, "http://"), Path: "/" + database, User: url.UserPassword(user, credential), RawQuery: "sslmode=disable"}
			db, err = kitsql.OpenWithClient(u.String(), httpServer.Client())
		}
		if err == nil {
			db.SetMaxOpenConns(1)
		}
		return db, err
	}
	open := func(adapter, user, database string) *sql.DB {
		t.Helper()
		db, err := rawOpen(adapter, user, database, password)
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	assertLoginDenied := func(adapter, user, credential string) {
		t.Helper()
		db, err := rawOpen(adapter, user, "shop", credential)
		if err == nil {
			err = db.PingContext(ctx)
			db.Close()
		}
		if err == nil {
			t.Fatalf("%s unexpectedly authenticated %s", adapter, user)
		}
	}
	const rotated = "rotated-account-secret"
	for _, adapter := range []string{"native", "pg", "http"} {
		admin := open(adapter, "admin", "shop")
		reader := open(adapter, "reader", "shop")
		label := map[string]string{"native": "native", "pg": "pgwire", "http": "kitsql"}[adapter]
		var sessionCount int
		if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM kitdb_catalog.sessions WHERE adapter=$1 AND (username='reader' OR username='admin')", label).Scan(&sessionCount); err != nil || sessionCount != 1 {
			t.Fatalf("%s session scope/label: %d %v", adapter, sessionCount, err)
		}
		user := "created_" + adapter
		if _, err := reader.ExecContext(ctx, "CREATE USER blocked_"+adapter+" WITH PASSWORD $1", password); err == nil {
			t.Fatalf("%s non-admin created account", adapter)
		}
		if _, err := reader.ExecContext(ctx, "GRANT SCHEMA ON DATABASE shop TO reader"); err == nil {
			t.Fatalf("%s non-admin granted privileges", adapter)
		}
		for _, source := range []string{"ALTER USER reader NOLOGIN", "DROP USER reader"} {
			if _, err := reader.ExecContext(ctx, source); err == nil {
				t.Fatalf("%s non-admin managed account: %s", adapter, source)
			}
		}
		reader.Close()
		if _, err := admin.ExecContext(ctx, "CREATE USER "+user+" WITH PASSWORD $1", password); err != nil {
			t.Fatalf("%s create: %v", adapter, err)
		}
		if _, err := admin.ExecContext(ctx, "CREATE USER "+user+" WITH PASSWORD 'different-account-password'"); err == nil {
			t.Fatal("duplicate overwrote account")
		}
		if _, err := node.OpenAuthenticatedDatabase(ctx, user, password, "shop"); err == nil {
			t.Fatal("new account implicitly allowed to connect")
		}
		if _, err := admin.ExecContext(ctx, "GRANT CONNECT, READ ON DATABASE shop TO "+user); err != nil {
			t.Fatalf("%s grant: %v", adapter, err)
		}
		client := open(adapter, user, "shop")
		var value string
		if err := client.QueryRowContext(ctx, "SELECT value FROM items WHERE id=1").Scan(&value); err != nil || value != "original" {
			t.Fatalf("%s new-user read: %s/%v", adapter, value, err)
		}
		if _, err := admin.ExecContext(ctx, "REVOKE READ ON DATABASE shop FROM "+user); err != nil {
			t.Fatal(err)
		}
		err = client.QueryRowContext(ctx, "SELECT value FROM items WHERE id=1").Scan(&value)
		if adapter == "http" && err == nil {
			t.Fatal("HTTP request reused revoked permission")
		}
		if adapter != "http" && err != nil {
			t.Fatalf("existing session snapshot unexpectedly revoked: %v", err)
		}
		client.Close()
		client = open(adapter, user, "shop")
		if err := client.QueryRowContext(ctx, "SELECT value FROM items WHERE id=1").Scan(&value); err == nil {
			t.Fatalf("%s fresh connection retained READ", adapter)
		}
		client.Close()
		if adapter != "http" {
			tx, err := admin.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = tx.ExecContext(ctx, "CREATE USER tx_"+adapter+" WITH PASSWORD $1", password)
			tx.Rollback()
			if err == nil {
				t.Fatalf("%s committed root auth inside data transaction", adapter)
			}
			var count int
			if err := admin.QueryRowContext(ctx, "SELECT COUNT(*) FROM kitdb_catalog.users WHERE username=$1", "tx_"+adapter).Scan(&count); err != nil || count != 0 {
				t.Fatalf("transaction account created: %d/%v", count, err)
			}
		}
		if _, err := admin.ExecContext(ctx, "REVOKE CONNECT ON DATABASE shop FROM "+user); err != nil {
			t.Fatal(err)
		}
		if _, err := node.OpenAuthenticatedDatabase(ctx, user, password, "shop"); err == nil {
			t.Fatal("revoked connect remained available")
		}
		if _, err := admin.ExecContext(ctx, "CREATE USER malformed WITH PASSWORD "+password); err == nil || strings.Contains(err.Error(), password) {
			t.Fatalf("malformed credential disclosure: %v", err)
		}
		for _, source := range []string{"ALTER USER admin NOLOGIN", "DROP USER admin"} {
			if _, err := admin.ExecContext(ctx, source); err == nil {
				t.Fatalf("%s removed last enabled admin", adapter)
			}
		}
		if _, err := admin.ExecContext(ctx, "GRANT CONNECT, READ ON DATABASE shop TO "+user); err != nil {
			t.Fatal(err)
		}
		if _, err := admin.ExecContext(ctx, "ALTER USER "+user+" WITH PASSWORD $1", rotated); err != nil {
			t.Fatalf("%s rotate: %v", adapter, err)
		}
		assertLoginDenied(adapter, user, password)
		client, err = rawOpen(adapter, user, "shop", rotated)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.QueryRowContext(ctx, "SELECT value FROM items WHERE id=1").Scan(&value); err != nil {
			t.Fatal("rotation lost grants", err)
		}
		if _, err := admin.ExecContext(ctx, "ALTER USER "+user+" NOLOGIN"); err != nil {
			t.Fatal(err)
		}
		assertLoginDenied(adapter, user, rotated)
		err = client.QueryRowContext(ctx, "SELECT value FROM items WHERE id=1").Scan(&value)
		if (adapter == "http") != (err != nil) {
			t.Fatalf("%s disable session policy: %v", adapter, err)
		}
		client.Close()
		if _, err := admin.ExecContext(ctx, "ALTER USER "+user+" LOGIN"); err != nil {
			t.Fatal(err)
		}
		client, err = rawOpen(adapter, user, "shop", rotated)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.QueryRowContext(ctx, "SELECT value FROM items WHERE id=1").Scan(&value); err != nil {
			t.Fatal("LOGIN lost password/grants", err)
		}
		if adapter != "http" {
			for _, source := range []string{"ALTER USER " + user + " NOLOGIN", "DROP USER " + user} {
				tx, err := admin.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				_, err = tx.ExecContext(ctx, source)
				tx.Rollback()
				if err == nil {
					t.Fatal("managed accounts inside data transaction")
				}
			}
		}
		if _, err := admin.ExecContext(ctx, "DROP USER "+user); err != nil {
			t.Fatal(err)
		}
		assertLoginDenied(adapter, user, rotated)
		err = client.QueryRowContext(ctx, "SELECT value FROM items WHERE id=1").Scan(&value)
		if (adapter == "http") != (err != nil) {
			t.Fatalf("%s drop session policy: %v", adapter, err)
		}
		client.Close()
		if _, err := admin.ExecContext(ctx, "DROP USER IF EXISTS "+user); err != nil {
			t.Fatal(err)
		}
		var accountCount, grantCount int
		if err := admin.QueryRowContext(ctx, "SELECT COUNT(*) FROM kitdb_catalog.users WHERE username=$1", user).Scan(&accountCount); err != nil || accountCount != 0 {
			t.Fatalf("drop user row: %d/%v", accountCount, err)
		}
		if err := admin.QueryRowContext(ctx, "SELECT COUNT(*) FROM kitdb_catalog.grants WHERE username=$1", user).Scan(&grantCount); err != nil || grantCount != 0 {
			t.Fatalf("drop grants: %d/%v", grantCount, err)
		}
		target := open("native", "reader", "shop")
		if err := target.PingContext(ctx); err != nil {
			t.Fatal(err)
		}
		var targetID int64
		if err := admin.QueryRowContext(ctx, "SELECT session_id FROM kitdb_catalog.sessions WHERE username='reader' AND adapter='native'").Scan(&targetID); err != nil {
			t.Fatal(err)
		}
		if _, err := admin.ExecContext(ctx, fmt.Sprintf("KILL SESSION %d", targetID)); err != nil {
			t.Fatalf("%s terminate: %v", adapter, err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			var n int
			if err := admin.QueryRowContext(ctx, "SELECT COUNT(*) FROM kitdb_catalog.sessions WHERE session_id=$1", targetID).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("target did not drain")
			}
			time.Sleep(time.Millisecond)
		}
		target.Close()
		admin.Close()
	}
	httpVictim := open("http", "reader", "shop")
	defer httpVictim.Close()
	httpResult := make(chan error, 1)
	go func() {
		rows, err := httpVictim.QueryContext(ctx, "SELECT value FROM items WHERE id=99")
		if rows != nil {
			for rows.Next() {
			}
			if rowErr := rows.Err(); rowErr != nil {
				err = rowErr
			}
			rows.Close()
		}
		httpResult <- err
	}()
	select {
	case <-httpEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP request did not enter")
	}
	control := open("native", "admin", "shop")
	defer control.Close()
	var httpID int64
	if err := control.QueryRowContext(ctx, "SELECT session_id FROM kitdb_catalog.sessions WHERE adapter='kitsql'").Scan(&httpID); err != nil {
		close(httpRelease)
		t.Fatal(err)
	}
	if _, err := control.ExecContext(ctx, fmt.Sprintf("KILL SESSION %d", httpID)); err != nil {
		close(httpRelease)
		t.Fatal(err)
	}
	close(httpRelease)
	select {
	case err := <-httpResult:
		if err == nil {
			t.Fatal("terminated HTTP request succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("terminated HTTP request stuck")
	}
	// Stateless KitSQL can authenticate a new request after this one is killed.
	var value string
	if err := httpVictim.QueryRowContext(ctx, "SELECT value FROM items WHERE id=1").Scan(&value); err != nil || value != "original" {
		t.Fatalf("new HTTP request: %q %v", value, err)
	}
	maintenance := open("pg", "admin", "kitdb")
	if _, err := maintenance.ExecContext(ctx, "CREATE USER maintenance_created WITH PASSWORD $1", password); err != nil {
		t.Fatal(err)
	}
	maintenance.Close()
	mu.Lock()
	defer mu.Unlock()
	for _, trace := range traces {
		if strings.Contains(trace, password) || strings.Contains(trace, "different-account-password") || strings.Contains(trace, rotated) {
			t.Fatalf("credential in trace: %s", trace)
		}
	}
}
