package relational

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

func TestPostgresNodeWarmDatabaseRetainsProjectionWhileIdleBudgetTrimsOthers(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		createPostgresNodeProjectionFixture(t, filepath.Join(root, name+".kitdb"), name)
	}

	node, err := OpenPostgresNode(PostgresNodeOptions{
		Root: root, User: "kitdb", Password: "node-secret",
		WarmDatabases:                       []string{"alpha.kitdb"},
		MaximumIdleProjectionDatabases:      1,
		MaximumIdleProjectionDirectoryBytes: 8 << 20,
		DatabaseAcquireTimeout:              50 * time.Millisecond,
		ManagerLimits: kitdbnode.Limits{
			MaxOpenDatabases: 2, MaxPageCacheBytes: 2 << 20,
			DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 2,
		},
		Relational: Options{
			ExperimentalProjections: true,
			Kernel:                  kitdbengine.OpenOptions{RetainHistory: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	address, stopServer := startPostgresNodeTestServer(t, node)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	queryAndClose := func(database string) {
		t.Helper()
		client := openPostgresNodeTestClient(t, address, database, "node-secret")
		var total int64
		if err := client.QueryRowContext(ctx,
			`SELECT SUM(price) FROM products WHERE bucket = 1`).Scan(&total); err != nil {
			t.Fatalf("query %s: %v", database, err)
		}
		if total != 40 {
			t.Fatalf("query %s total = %d, want 40", database, total)
		}
		if err := client.Close(); err != nil {
			t.Fatalf("close %s: %v", database, err)
		}
		waitForPostgresNode(t, func() bool {
			node.mu.Lock()
			defer node.mu.Unlock()
			entry := node.engines[database]
			return entry != nil && entry.sessions == 0
		})
	}

	queryAndClose("alpha")
	warm := node.Stats()
	if warm.WarmIdleEngines != 1 || warm.ProjectionCachedEngines != 1 ||
		warm.ProjectionCacheEntries != 1 || warm.ProjectionDirectoryBytes <= 0 {
		t.Fatalf("warm alpha stats = %+v", warm)
	}

	queryAndClose("beta")
	bounded := node.Stats()
	if bounded.ManagedEngines != 2 || bounded.IdleEngines != 2 ||
		bounded.ProjectionCachedEngines != 1 || bounded.ProjectionCacheEntries != 1 ||
		bounded.ProjectionCacheTrims != 1 || bounded.ProjectionDirectoryBytesTrimmed <= 0 {
		t.Fatalf("bounded idle projection stats = %+v", bounded)
	}
	node.mu.Lock()
	alpha := node.engines["alpha"]
	beta := node.engines["beta"]
	node.mu.Unlock()
	if alpha == nil || !alpha.warm || alpha.engine.ProjectionCacheStats().Entries != 1 {
		t.Fatalf("warm alpha entry = %+v", alpha)
	}
	if beta == nil || beta.warm || beta.engine.ProjectionCacheStats().Entries != 0 {
		t.Fatalf("cooled beta entry = %+v", beta)
	}

	queryAndClose("gamma")
	afterEviction := node.Stats()
	if afterEviction.ManagedEngines != 2 || afterEviction.WarmIdleEngines != 1 ||
		afterEviction.ProjectionCachedEngines != 1 || afterEviction.ProjectionCacheTrims != 2 {
		t.Fatalf("warm eviction stats = %+v", afterEviction)
	}
	node.mu.Lock()
	_, alphaPresent := node.engines["alpha"]
	_, betaPresent := node.engines["beta"]
	_, gammaPresent := node.engines["gamma"]
	node.mu.Unlock()
	if !alphaPresent || betaPresent || !gammaPresent {
		t.Fatalf("engine residency alpha=%t beta=%t gamma=%t", alphaPresent, betaPresent, gammaPresent)
	}

	stopServer()
	if err := node.Close(); err != nil {
		t.Fatal(err)
	}
	closed := node.Stats()
	if !closed.Closed || closed.ManagedEngines != 0 || closed.ProjectionCacheEntries != 0 ||
		closed.Manager.ActiveLeases != 0 {
		t.Fatalf("closed node stats = %+v", closed)
	}
}

func TestPostgresNodeBoundsIdleSearchReaderResidency(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		createPostgresNodeSearchProjectionFixture(t, filepath.Join(root, name+".kitdb"), name)
	}
	node, err := OpenPostgresNode(PostgresNodeOptions{
		Root: root, User: "kitdb", Password: "node-secret",
		MaximumIdleProjectionDatabases:      2,
		MaximumIdleProjectionDirectoryBytes: 8 << 20,
		MaximumIdleProjectionReaderBytes:    4 << 20,
		ManagerLimits: kitdbnode.Limits{
			MaxOpenDatabases: 2, MaxPageCacheBytes: 2 << 20,
			DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 2,
		},
		Relational: Options{
			ExperimentalProjections: true,
			SearchReaderCacheBytes:  4 << 20,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	databases, err := node.discoverDatabases()
	if err != nil {
		t.Fatal(err)
	}
	queryAndRelease := func(name string) {
		t.Helper()
		database, found := findPostgresNodeDatabase(databases, name)
		if !found {
			t.Fatalf("database %q was not discovered", name)
		}
		entry, err := node.acquireEngine(context.Background(), database)
		if err != nil {
			t.Fatalf("acquire %s: %v", name, err)
		}
		result, queryErr := entry.engine.Execute(
			context.Background(),
			`SELECT id, name, _score FROM products WHERE * SEARCH 'blue widget' LIMIT 5`,
		)
		releaseErr := node.releaseEngine(entry)
		if err := errors.Join(queryErr, releaseErr); err != nil {
			t.Fatalf("query/release %s: %v", name, err)
		}
		if result.Execution == nil || result.Execution.SearchReaderCacheMisses != 1 {
			t.Fatalf("search execution %s = %+v", name, result.Execution)
		}
	}

	queryAndRelease("alpha")
	first := node.Stats()
	if first.ProjectionSearchReaders != 1 ||
		first.ProjectionSearchFileHandles != expectedProjectionSearchReadHandles() ||
		first.ProjectionReaderResidentBytes <= 0 ||
		first.ProjectionReaderCapacityBytes < first.ProjectionReaderResidentBytes {
		t.Fatalf("first search residency = %+v", first)
	}
	queryAndRelease("beta")
	bounded := node.Stats()
	if bounded.ProjectionSearchReaders != 1 ||
		bounded.ProjectionSearchFileHandles != expectedProjectionSearchReadHandles() ||
		bounded.ProjectionCacheTrims != 1 ||
		bounded.ProjectionReaderBytesTrimmed <= 0 ||
		bounded.ProjectionReaderCapacityBytes > bounded.MaximumIdleProjectionReaderBytes {
		t.Fatalf("bounded search residency = %+v", bounded)
	}
	node.mu.Lock()
	alpha := node.engines["alpha"]
	beta := node.engines["beta"]
	node.mu.Unlock()
	if alpha == nil || alpha.engine.ProjectionCacheStats().SearchReaders != 0 {
		t.Fatalf("oldest idle search reader was retained: %+v", alpha)
	}
	if beta == nil || beta.engine.ProjectionCacheStats().SearchReaders != 1 {
		t.Fatalf("newest idle search reader was trimmed: %+v", beta)
	}
}

func TestPostgresNodeRejectsInvalidWarmProjectionPolicy(t *testing.T) {
	root := t.TempDir()
	createPostgresNodeProjectionFixture(t, filepath.Join(root, "alpha.kitdb"), "alpha")
	base := PostgresNodeOptions{
		Root: root, User: "kitdb", Password: "node-secret",
		ManagerLimits: kitdbnode.Limits{
			MaxOpenDatabases: 1, MaxPageCacheBytes: 1 << 20,
			DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 1,
		},
	}
	tests := []PostgresNodeOptions{
		func() PostgresNodeOptions {
			options := base
			options.WarmDatabases = []string{"missing"}
			return options
		}(),
		func() PostgresNodeOptions {
			options := base
			options.WarmDatabases = []string{"alpha", "alpha.kitdb"}
			return options
		}(),
		func() PostgresNodeOptions {
			options := base
			options.WarmDatabases = []string{"alpha"}
			options.MaximumIdleProjectionDirectoryBytes = 1
			return options
		}(),
		func() PostgresNodeOptions {
			options := base
			options.WarmDatabases = []string{"alpha"}
			options.MaximumIdleProjectionReaderBytes = 1
			return options
		}(),
	}
	for index, options := range tests {
		if node, err := OpenPostgresNode(options); err == nil {
			_ = node.Close()
			t.Fatalf("invalid warm policy %d succeeded", index)
		}
	}
}

func TestPostgresNodeWarmCapacityFailsWithoutWaiting(t *testing.T) {
	root := t.TempDir()
	createPostgresNodeFixture(t, filepath.Join(root, "alpha.kitdb"), "rows", "alpha")
	createPostgresNodeFixture(t, filepath.Join(root, "beta.kitdb"), "rows", "beta")
	node, err := OpenPostgresNode(PostgresNodeOptions{
		Root: root, User: "kitdb", Password: "node-secret",
		WarmDatabases:          []string{"alpha"},
		DatabaseAcquireTimeout: 5 * time.Second,
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
	if err := alpha.Close(); err != nil {
		t.Fatal(err)
	}
	waitForPostgresNode(t, func() bool { return node.Stats().WarmIdleEngines == 1 })

	started := time.Now()
	beta := openPostgresNodeRawClient(address, "beta", "node-secret")
	err = beta.PingContext(ctx)
	_ = beta.Close()
	if postgresNodeSQLState(err) != "53300" {
		t.Fatalf("warm capacity error = %v", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("warm capacity waited %v despite having no evictable engine", elapsed)
	}

	stopServer()
	if err := node.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresNodeMixedWorkloadIsBounded(t *testing.T) {
	const (
		databaseCount         = 8
		projectedCount        = 4
		rowsPerDatabase       = 512
		operationsPerDatabase = 24
	)
	root := t.TempDir()
	for database := 0; database < databaseCount; database++ {
		createPostgresNodeMixedFixture(
			t,
			filepath.Join(root, fmt.Sprintf("tenant%d.kitdb", database)),
			database < projectedCount,
			rowsPerDatabase,
		)
	}

	queryMetrics := &pgwire.QueryMetrics{}
	node, err := OpenPostgresNode(PostgresNodeOptions{
		Root: root, User: "kitdb", Password: "node-secret",
		WarmDatabases:                  []string{"tenant0", "tenant1"},
		MaximumIdleProjectionDatabases: 2,
		DatabaseAcquireTimeout:         time.Second,
		ManagerLimits: kitdbnode.Limits{
			MaxOpenDatabases: databaseCount, MaxPageCacheBytes: databaseCount << 20,
			DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 2,
		},
		Relational: Options{ExperimentalProjections: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	address, stopServer := startPostgresNodeTestServerWithOptions(t, node, PostgresServerOptions{
		MaxConnections:             databaseCount,
		MaxConcurrentQueries:       3,
		MaxConcurrentQueriesPerKey: 1,
		MaxQueuedQueries:           databaseCount,
		MaxQueuedQueriesPerKey:     2,
		IdleTimeout:                time.Minute,
		QueryTimeout:               10 * time.Second,
		QueryMetrics:               queryMetrics,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	clients := make([]*sql.DB, databaseCount)
	for database := range clients {
		clients[database] = openPostgresNodeTestClient(
			t, address, fmt.Sprintf("tenant%d", database), "node-secret",
		)
	}
	baseline := queryMetrics.Snapshot()

	start := make(chan struct{})
	errorsFound := make(chan error, databaseCount)
	var workers sync.WaitGroup
	for database, client := range clients {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for operation := 0; operation < operationsPerDatabase; operation++ {
				if err := runPostgresNodeMixedOperation(
					ctx, client, database < projectedCount, database, operation,
					rowsPerDatabase, mixedFixtureActiveTotal(rowsPerDatabase),
				); err != nil {
					errorsFound <- err
					return
				}
			}
		}()
	}
	close(start)
	workers.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	snapshot := queryMetrics.Snapshot()
	expectedQueries := uint64(databaseCount * operationsPerDatabase)
	if snapshot.Active != 0 || snapshot.Queued != 0 || snapshot.Peak > 3 ||
		snapshot.Acquired-baseline.Acquired != expectedQueries ||
		snapshot.Completed-baseline.Completed != expectedQueries ||
		snapshot.Failed != baseline.Failed || snapshot.Rejected != baseline.Rejected ||
		snapshot.WaitTimeouts != baseline.WaitTimeouts {
		t.Fatalf("mixed query metrics baseline=%+v final=%+v", baseline, snapshot)
	}

	for _, client := range clients {
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
	}
	waitForPostgresNode(t, func() bool { return node.Stats().Sessions == 0 })
	stats := node.Stats()
	if stats.ProjectionCachedEngines != 2 || stats.ProjectionCacheEntries != 4 ||
		stats.WarmIdleEngines != 2 || stats.ProjectionCacheTrims < 2 ||
		stats.Manager.ActiveLeases != databaseCount {
		t.Fatalf("mixed node residency = %+v", stats)
	}

	stopServer()
	if err := node.Close(); err != nil {
		t.Fatal(err)
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

func createPostgresNodeProjectionFixture(t *testing.T, path, label string) {
	t.Helper()
	engine, err := OpenWithOptions(path, Options{
		ExperimentalProjections: true,
		Kernel:                  kitdbengine.OpenOptions{RetainHistory: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if _, err := engine.Execute(context.Background(), `
CREATE TABLE products (
    id INTEGER PRIMARY KEY,
    bucket INTEGER NOT NULL,
    price INTEGER NOT NULL,
    label TEXT NOT NULL
)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(context.Background(),
		`INSERT INTO products (id,bucket,price,label) VALUES (1,1,10,$1),(2,2,20,$1),(3,1,30,$1)`,
		label,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RefreshAnalytics(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func createPostgresNodeSearchProjectionFixture(t *testing.T, path, label string) {
	t.Helper()
	engine, err := OpenWithOptions(path, Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if _, err := engine.Execute(context.Background(), `
CREATE TABLE products (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL SEARCHABLE,
    label TEXT NOT NULL
)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(context.Background(),
		`INSERT INTO products (id,name,label) VALUES (1,'blue widget',$1),(2,'green widget',$1)`,
		label,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RefreshProjections(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresNodeWarmDatabaseValidatesProjectionBeforeResidency(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "alpha.kitdb")
	createPostgresNodeProjectionFixture(t, path, "alpha")
	if err := os.WriteFile(path+".analytics", []byte("not a snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	node, err := OpenPostgresNode(PostgresNodeOptions{
		Root: root, User: "kitdb", Password: "secret",
		WarmDatabases: []string{"alpha"},
		Relational:    Options{ExperimentalProjections: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	databases, err := node.discoverDatabases()
	if err != nil {
		t.Fatal(err)
	}
	database, found := findPostgresNodeDatabase(databases, "alpha")
	if !found {
		t.Fatal("alpha database was not discovered")
	}
	entry, err := node.acquireEngine(context.Background(), database)
	if entry != nil {
		_ = node.releaseEngine(entry)
		t.Fatal("warm database retained an invalid projection")
	}
	var problem *ProjectionOpenError
	if !errors.As(err, &problem) || problem.Policy != ProjectionOpenValidate ||
		problem.Kind != "analytics" || problem.Status != "invalid" {
		t.Fatalf("warm projection admission = %#v, %v", problem, err)
	}
	if stats := node.Stats(); stats.ManagedEngines != 0 || stats.Manager.ActiveLeases != 0 {
		t.Fatalf("failed warm admission retained resources: %+v", stats)
	}
}

func createPostgresNodeMixedFixture(t testing.TB, path string, projected bool, rows int) {
	t.Helper()
	engine, err := OpenWithOptions(path, Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if _, err := engine.Execute(context.Background(), `
CREATE TABLE events (
    tenant_id INTEGER NOT NULL,
    id INTEGER NOT NULL,
    label TEXT NOT NULL SEARCHABLE WEIGHT 3,
    amount INTEGER NOT NULL,
    active BOOLEAN NOT NULL,
    PRIMARY KEY (tenant_id, id)
)`); err != nil {
		t.Fatal(err)
	}
	for start := 0; start < rows; start += 128 {
		var source strings.Builder
		source.WriteString("INSERT INTO events (tenant_id,id,label,amount,active) VALUES ")
		for row := start; row < min(start+128, rows); row++ {
			if row != start {
				source.WriteByte(',')
			}
			fmt.Fprintf(
				&source, "(1,%d,'blue widget %d',%d,%t)",
				row, row%17, row%100, row%2 == 0,
			)
		}
		if _, err := engine.Execute(context.Background(), source.String()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := engine.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if projected {
		if _, err := engine.RefreshProjections(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func mixedFixtureActiveTotal(rows int) int64 {
	var total int64
	for row := 0; row < rows; row += 2 {
		total += int64(row % 100)
	}
	return total
}

func runPostgresNodeMixedOperation(
	ctx context.Context,
	client *sql.DB,
	projected bool,
	database int,
	operation int,
	rowsPerDatabase int,
	expectedActiveTotal int64,
) error {
	id := operation % rowsPerDatabase
	if projected {
		switch operation % 3 {
		case 0:
			var label string
			if err := client.QueryRowContext(ctx,
				`SELECT label FROM events WHERE tenant_id = 1 AND id = $1`, id,
			).Scan(&label); err != nil {
				return fmt.Errorf("tenant%d point read: %w", database, err)
			}
			if label == "" {
				return fmt.Errorf("tenant%d point read returned an empty label", database)
			}
		case 1:
			var total int64
			if err := client.QueryRowContext(ctx,
				`SELECT SUM(amount) FROM events WHERE active = true`,
			).Scan(&total); err != nil {
				return fmt.Errorf("tenant%d aggregate: %w", database, err)
			}
			if total != expectedActiveTotal {
				return fmt.Errorf("tenant%d aggregate total=%d, want %d", database, total, expectedActiveTotal)
			}
		case 2:
			rows, err := client.QueryContext(ctx,
				`SELECT id, _score FROM events WHERE * SEARCH 'blue widget' ORDER BY _score DESC LIMIT 5`,
			)
			if err != nil {
				return fmt.Errorf("tenant%d search: %w", database, err)
			}
			count := 0
			for rows.Next() {
				var resultID int64
				var score float64
				if err := rows.Scan(&resultID, &score); err != nil {
					_ = rows.Close()
					return fmt.Errorf("tenant%d search row: %w", database, err)
				}
				count++
			}
			iterationErr := rows.Err()
			closeErr := rows.Close()
			if iterationErr != nil {
				return fmt.Errorf("tenant%d search iteration: %w", database, iterationErr)
			}
			if closeErr != nil {
				return fmt.Errorf("tenant%d search close: %w", database, closeErr)
			}
			if count != 5 {
				return fmt.Errorf("tenant%d search count=%d, want 5", database, count)
			}
		}
		return nil
	}

	if operation%2 == 0 {
		var amount int64
		if err := client.QueryRowContext(ctx,
			`SELECT amount FROM events WHERE tenant_id = 1 AND id = $1`, id,
		).Scan(&amount); err != nil {
			return fmt.Errorf("tenant%d OLTP read: %w", database, err)
		}
		return nil
	}
	result, err := client.ExecContext(ctx,
		`UPDATE events SET amount = $1 WHERE tenant_id = 1 AND id = $2`,
		operation+database, id,
	)
	if err != nil {
		return fmt.Errorf("tenant%d OLTP update: %w", database, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("tenant%d OLTP affected rows: %w", database, err)
	}
	if affected != 1 {
		return fmt.Errorf("tenant%d OLTP affected=%d, want 1", database, affected)
	}
	return nil
}

func startPostgresNodeTestServer(t *testing.T, node *PostgresNode) (string, func()) {
	return startPostgresNodeTestServerWithOptions(t, node, PostgresServerOptions{
		MaxConnections: 16, IdleTimeout: time.Minute, QueryTimeout: 5 * time.Second,
	})
}

func startPostgresNodeTestServerWithOptions(
	t testing.TB,
	node *PostgresNode,
	options PostgresServerOptions,
) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- node.ServePostgres(ctx, listener, options)
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

func openPostgresNodeTestClient(t testing.TB, address, database, password string) *sql.DB {
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
