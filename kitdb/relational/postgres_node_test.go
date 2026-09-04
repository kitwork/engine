package relational

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
	"github.com/kitwork/engine/kitdb/pgwire"
)

func TestPostgresNodeDiscoversAndRoutesIndependentDatabases(t *testing.T) {
	root := t.TempDir()
	alphaPath := filepath.Join(root, "alpha.kitdb")
	betaPath := filepath.Join(root, "beta.kitdb")
	createPostgresNodeFixture(t, alphaPath, "alpha_rows", "from-alpha")
	createPostgresNodeFixture(t, betaPath, "beta_rows", "from-beta")

	node, err := OpenPostgresNode(PostgresNodeOptions{
		Root: root, User: "kitdb", Password: "node-secret",
		DatabaseAcquireTimeout: 50 * time.Millisecond,
		ManagerLimits: kitdbnode.Limits{
			MaxOpenDatabases: 4, MaxPageCacheBytes: 8 << 20,
			DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 2,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := node.Close(); err != nil {
			t.Errorf("close PostgreSQL node: %v", err)
		}
	})
	if stats := node.manager.Stats(); stats.OpenDatabases != 0 || stats.ActiveLeases != 0 {
		t.Fatalf("metadata-only node opened a database: %+v", stats)
	}

	address, stop := startPostgresNodeTestServer(t, node)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	maintenance := openPostgresNodeTestClient(t, address, "kitdb", "node-secret")
	defer maintenance.Close()
	if names := queryPostgresNodeStrings(t, ctx, maintenance,
		`SELECT datname FROM pg_catalog.pg_database ORDER BY datname`); !slices.Equal(names, []string{"alpha", "beta", "kitdb"}) {
		t.Fatalf("maintenance pg_database = %v", names)
	}
	transaction, err := maintenance.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if names := queryPostgresNodeStrings(t, ctx, transaction,
		`SELECT datname FROM pg_catalog.pg_database ORDER BY datname`); !slices.Equal(names, []string{"alpha", "beta", "kitdb"}) {
		t.Fatalf("transactional maintenance pg_database = %v", names)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	maintenanceSession := &postgresSession{
		authenticator: &postgresAuthenticator{database: "kitdb", user: "kitdb", readOnly: true},
		node:          node,
		maintenance:   true,
	}
	if _, err := maintenanceSession.Describe(ctx, `BEGIN DEFERRABLE`, nil); postgresNodeWireSQLState(err) != "0A000" {
		t.Fatalf("unsupported maintenance BEGIN describe error = %v", err)
	}
	if _, err := maintenance.QueryContext(ctx, `SELECT * FROM alpha_rows`); postgresNodeSQLState(err) != "3D000" {
		t.Fatalf("maintenance user query error = %v", err)
	}
	if stats := node.manager.Stats(); stats.OpenDatabases != 0 {
		t.Fatalf("maintenance catalog opened user database: %+v", stats)
	}

	alpha := openPostgresNodeTestClient(t, address, "alpha", "node-secret")
	defer alpha.Close()
	if got := queryPostgresNodeValue(t, ctx, alpha, `SELECT value FROM alpha_rows WHERE id = 1`); got != "from-alpha" {
		t.Fatalf("alpha value = %q", got)
	}
	if got := queryPostgresNodeValue(t, ctx, alpha, `SELECT current_database()`); got != "alpha" {
		t.Fatalf("alpha current_database = %q", got)
	}
	if names := queryPostgresNodeStrings(t, ctx, alpha,
		`SELECT datname FROM pg_catalog.pg_database ORDER BY datname`); !slices.Equal(names, []string{"alpha", "beta", "kitdb"}) {
		t.Fatalf("alpha pg_database = %v", names)
	}
	originalRoot := node.root
	node.root = filepath.Join(root, "missing")
	if _, err := alpha.QueryContext(ctx,
		`SELECT datname FROM pg_catalog.pg_database ORDER BY datname`); postgresNodeSQLState(err) != "58030" {
		t.Fatalf("unreadable node catalog error = %v", err)
	}
	node.root = originalRoot

	alphaSuffix := openPostgresNodeTestClient(t, address, "alpha.kitdb", "node-secret")
	defer alphaSuffix.Close()
	if got := queryPostgresNodeValue(t, ctx, alphaSuffix, `SELECT value FROM alpha_rows WHERE id = 1`); got != "from-alpha" {
		t.Fatalf("alpha suffix value = %q", got)
	}
	if stats := node.manager.Stats(); stats.OpenDatabases != 1 || stats.ActiveLeases != 1 {
		t.Fatalf("two alpha sessions did not share one managed engine: %+v", stats)
	}

	beta := openPostgresNodeTestClient(t, address, "beta", "node-secret")
	defer beta.Close()
	if got := queryPostgresNodeValue(t, ctx, beta, `SELECT value FROM beta_rows WHERE id = 1`); got != "from-beta" {
		t.Fatalf("beta value = %q", got)
	}
	if stats := node.manager.Stats(); stats.OpenDatabases != 2 || stats.ActiveLeases != 2 {
		t.Fatalf("independent databases did not own independent handles: %+v", stats)
	}

	missing := openPostgresNodeRawClient(address, "missing", "node-secret")
	if err := missing.PingContext(ctx); postgresNodeSQLState(err) != "3D000" {
		t.Fatalf("missing database error = %v", err)
	}
	_ = missing.Close()
	unauthorized := openPostgresNodeRawClient(address, "alpha", "wrong-secret")
	if err := unauthorized.PingContext(ctx); postgresNodeSQLState(err) != "28P01" {
		t.Fatalf("wrong password error = %v", err)
	}
	_ = unauthorized.Close()

	gammaPath := filepath.Join(root, "gamma.kitdb")
	createPostgresNodeFixture(t, gammaPath, "gamma_rows", "from-gamma")
	if names := queryPostgresNodeStrings(t, ctx, maintenance,
		`SELECT datname FROM pg_catalog.pg_database ORDER BY datname`); !slices.Equal(names, []string{"alpha", "beta", "gamma", "kitdb"}) {
		t.Fatalf("dynamic pg_database = %v", names)
	}
	gamma := openPostgresNodeTestClient(t, address, "gamma", "node-secret")
	defer gamma.Close()
	if got := queryPostgresNodeValue(t, ctx, gamma, `SELECT value FROM gamma_rows WHERE id = 1`); got != "from-gamma" {
		t.Fatalf("gamma value = %q", got)
	}
}

func TestPostgresNodeEvictsIdleEngineAndReleasesFileLocks(t *testing.T) {
	root := t.TempDir()
	alphaPath := filepath.Join(root, "alpha.kitdb")
	betaPath := filepath.Join(root, "beta.kitdb")
	createPostgresNodeFixture(t, alphaPath, "rows", "alpha")
	createPostgresNodeFixture(t, betaPath, "rows", "beta")

	node, err := OpenPostgresNode(PostgresNodeOptions{
		Root: root, User: "kitdb", Password: "node-secret",
		DatabaseAcquireTimeout: 50 * time.Millisecond,
		ManagerLimits: kitdbnode.Limits{
			MaxOpenDatabases: 1, MaxPageCacheBytes: 1 << 20,
			DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	address, stopServer := startPostgresNodeTestServer(t, node)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	alpha := openPostgresNodeTestClient(t, address, "alpha", "node-secret")
	if got := queryPostgresNodeValue(t, ctx, alpha, `SELECT value FROM rows WHERE id = 1`); got != "alpha" {
		t.Fatalf("alpha value = %q", got)
	}
	busy := openPostgresNodeRawClient(address, "beta", "node-secret")
	if err := busy.PingContext(ctx); postgresNodeSQLState(err) != "53300" {
		t.Fatalf("busy database capacity error = %v", err)
	}
	_ = busy.Close()
	if err := alpha.Close(); err != nil {
		t.Fatal(err)
	}
	waitForPostgresNode(t, func() bool {
		node.mu.Lock()
		defer node.mu.Unlock()
		entry := node.engines["alpha"]
		return entry != nil && entry.sessions == 0
	})

	beta := openPostgresNodeTestClient(t, address, "beta", "node-secret")
	if got := queryPostgresNodeValue(t, ctx, beta, `SELECT value FROM rows WHERE id = 1`); got != "beta" {
		t.Fatalf("beta value = %q", got)
	}
	if err := beta.Close(); err != nil {
		t.Fatal(err)
	}
	waitForPostgresNode(t, func() bool {
		stats := node.manager.Stats()
		return stats.OpenDatabases == 1 && stats.Evictions == 1
	})

	stopServer()
	if err := node.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{alphaPath, betaPath} {
		database, err := kitdbengine.Open(path)
		if err != nil {
			t.Fatalf("reopen %s after node close: %v", filepath.Base(path), err)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func createPostgresNodeFixture(t *testing.T, path, table, value string) {
	t.Helper()
	engine, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if _, err := engine.Execute(context.Background(), fmt.Sprintf(`
CREATE TABLE %s (
    id INTEGER PRIMARY KEY,
    value TEXT NOT NULL
)`, table)); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(
		context.Background(), fmt.Sprintf(`INSERT INTO %s (id, value) VALUES ($1, $2)`, table),
		int64(1), value,
	); err != nil {
		t.Fatal(err)
	}
}

func startPostgresNodeTestServer(t *testing.T, node *PostgresNode) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- node.ServePostgres(ctx, listener, PostgresServerOptions{
			MaxConnections: 16, IdleTimeout: time.Minute, QueryTimeout: 5 * time.Second,
		})
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("ServePostgres node: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("ServePostgres node did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return listener.Addr().String(), stop
}

func openPostgresNodeTestClient(t *testing.T, address, database, password string) *sql.DB {
	t.Helper()
	client := openPostgresNodeRawClient(address, database, password)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.PingContext(ctx); err != nil {
		_ = client.Close()
		t.Fatalf("connect database %s: %v", database, err)
	}
	return client
}

func openPostgresNodeRawClient(address, database, password string) *sql.DB {
	dsn := &url.URL{
		Scheme: "postgres", Host: address, Path: "/" + database,
		User: url.UserPassword("kitdb", password),
	}
	query := dsn.Query()
	query.Set("sslmode", "disable")
	dsn.RawQuery = query.Encode()
	client, _ := sql.Open("postgres", dsn.String())
	client.SetMaxOpenConns(1)
	return client
}

type postgresNodeQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func queryPostgresNodeStrings(
	t *testing.T,
	ctx context.Context,
	database postgresNodeQueryer,
	query string,
) []string {
	t.Helper()
	rows, err := database.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func queryPostgresNodeValue(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	query string,
) string {
	t.Helper()
	var value string
	if err := database.QueryRowContext(ctx, query).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func postgresNodeSQLState(err error) string {
	var protocol *pq.Error
	if errors.As(err, &protocol) {
		return string(protocol.Code)
	}
	return ""
}

func postgresNodeWireSQLState(err error) string {
	var protocol *pgwire.Error
	if errors.As(err, &protocol) {
		return protocol.Code
	}
	return ""
}

func waitForPostgresNode(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for PostgreSQL node state")
}
