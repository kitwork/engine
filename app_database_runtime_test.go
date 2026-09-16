package engine

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	hostdatabase "github.com/kitwork/engine/database"
	"github.com/kitwork/engine/kitdb/kitsql"
	"github.com/kitwork/engine/kitdb/managed"
	"github.com/kitwork/engine/kitdb/relational"
	_ "github.com/lib/pq"
)

func TestAppDatabaseRuntimePublishesNativeFileWithoutListener(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "native-shop.kitdb")
	runtime, err := startAppDatabaseRuntime(context.Background(), AppDatabaseConfig{
		Alias: "native-shop", Path: databasePath, Concurrency: 2,
	}, make(chan error, 1))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.listener != nil {
		t.Fatal("native-only database unexpectedly opened a network listener")
	}
	connection := hostdatabase.LookupOwned("native-shop")
	if connection == nil || connection != runtime.nativeDatabase {
		t.Fatal("native database alias was not published")
	}
	for _, source := range []string{
		`CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`INSERT INTO products VALUES (1, 'keyboard')`,
	} {
		if _, err := connection.Exec(source); err != nil {
			_ = runtime.Close()
			t.Fatal(err)
		}
	}
	var name string
	if err := connection.QueryRow(`SELECT name FROM products WHERE id = 1`).Scan(&name); err != nil {
		_ = runtime.Close()
		t.Fatal(err)
	}
	if name != "keyboard" {
		t.Fatalf("name = %q", name)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if connection := hostdatabase.LookupOwned("native-shop"); connection != nil {
		t.Fatal("native database alias remained published after close")
	}
	reopened, err := relational.Open(databasePath)
	if err != nil {
		t.Fatalf("database lock remained after native close: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAppDatabaseKitSQLUsesOwnedNativeHandle(t *testing.T) {
	runtime, err := startAppDatabaseRuntime(context.Background(), AppDatabaseConfig{
		Alias: "shop", Path: filepath.Join(t.TempDir(), "shop.kitdb"),
		User: "kitdb", Password: "test-secret", KitSQL: true, Concurrency: 2,
	}, make(chan error, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	for _, source := range []string{
		`CREATE TABLE products (id BIGINT PRIMARY KEY, name TEXT NOT NULL)`,
		`INSERT INTO products VALUES (1, 'keyboard')`,
	} {
		if _, err := runtime.nativeDatabase.Exec(source); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := newAppDatabaseKitSQLHandler([]*appDatabaseRuntime{runtime}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	database, err := kitsql.Open("kitsql://kitdb:test-secret@" + parsed.Host + "/shop?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var name string
	if err := database.QueryRow(`SELECT name FROM products WHERE id = $1`, int64(1)).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "keyboard" {
		t.Fatalf("name = %q", name)
	}
}

func TestAppDatabaseRuntimeServesOneFileAndCachesSafeSelect(t *testing.T) {
	port := availableLoopbackPort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errorsChannel := make(chan error, 1)
	runtime, err := startAppDatabaseRuntime(ctx, AppDatabaseConfig{
		Path: filepath.Join(t.TempDir(), "shop.kitdb"), Host: "127.0.0.1", Port: port,
		User: "kitdb", Password: "test-secret", MemoryBytes: 4 << 20, Concurrency: 2,
		Cache: appDatabaseTestCache(),
	}, errorsChannel)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	database, err := sql.Open("postgres", fmt.Sprintf(
		"host=127.0.0.1 port=%d user=kitdb password=test-secret dbname=shop sslmode=disable connect_timeout=2",
		port,
	))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	queryContext, queryCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer queryCancel()
	if err := database.PingContext(queryContext); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`INSERT INTO products VALUES (1, 'keyboard')`,
	} {
		if _, err := database.ExecContext(queryContext, source); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 2; index++ {
		var name string
		if err := database.QueryRowContext(queryContext, `SELECT name FROM products WHERE id = 1`).Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name != "keyboard" {
			t.Fatalf("name = %q", name)
		}
	}
	stats := runtime.file.QueryCacheStats()
	if stats.Hits != 1 || stats.Misses != 1 || stats.Entries != 1 {
		t.Fatalf("cache stats = %+v", stats)
	}
	select {
	case err := <-errorsChannel:
		t.Fatalf("database listener stopped: %v", err)
	default:
	}
}

func TestAppDatabaseRuntimeInitializesManagedRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".kitdb")
	runtime, err := startAppDatabaseRuntime(context.Background(), AppDatabaseConfig{
		Path: root + string(filepath.Separator), Host: "127.0.0.1", User: "kitdb",
	}, make(chan error, 1))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.node == nil {
		t.Fatal("managed root did not open a standalone KitDB node")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if present, err := appDatabaseDirectoryTarget(root, root); err != nil || !present {
		t.Fatalf("managed root target = %t, %v", present, err)
	}
}

func TestAppDatabaseRuntimeResolvesManagedDatabaseNativelyAndReleasesLease(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), ".kitdb")
	if _, err := managed.Init(rootPath); err != nil {
		t.Fatal(err)
	}
	databaseDirectory := filepath.Join(rootPath, "shop")
	if err := os.MkdirAll(databaseDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(databaseDirectory, "data.kitdb")
	fixture, err := relational.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`CREATE TABLE rows (id INTEGER PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO rows VALUES (1, 'native-root')`,
	} {
		if _, err := fixture.Execute(context.Background(), source); err != nil {
			_ = fixture.Close()
			t.Fatal(err)
		}
	}
	if err := fixture.Close(); err != nil {
		t.Fatal(err)
	}
	registry, err := managed.Open(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Register("shop", "shop"); err != nil {
		_ = registry.Close()
		t.Fatal(err)
	}
	if err := registry.Close(); err != nil {
		t.Fatal(err)
	}

	runtime, err := startAppDatabaseRuntime(context.Background(), AppDatabaseConfig{
		Path: rootPath + string(filepath.Separator), Concurrency: 2,
	}, make(chan error, 1))
	if err != nil {
		t.Fatal(err)
	}
	connection, err := hostdatabase.ResolveOwned("shop")
	if err != nil || connection == nil {
		_ = runtime.Close()
		t.Fatalf("resolve managed shop = %p, %v", connection, err)
	}
	var value string
	if err := connection.QueryRow(`SELECT value FROM rows WHERE id = 1`).Scan(&value); err != nil {
		_ = runtime.Close()
		t.Fatal(err)
	}
	if value != "native-root" {
		t.Fatalf("value = %q", value)
	}
	if stats := runtime.node.Stats(); stats.ActiveEngines != 0 || stats.Sessions != 0 {
		_ = runtime.Close()
		t.Fatalf("native query retained an active node session: %+v", stats)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if connection, err := hostdatabase.ResolveOwned("shop"); err != nil || connection != nil {
		t.Fatalf("managed resolver remained after close: %p, %v", connection, err)
	}
	reopened, err := relational.Open(databasePath)
	if err != nil {
		t.Fatalf("managed database lock remained after close: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAppDatabaseRuntimeCloseDrainsOpenPostgresConnection(t *testing.T) {
	port := availableLoopbackPort(t)
	databasePath := filepath.Join(t.TempDir(), "shutdown.kitdb")
	runtime, err := startAppDatabaseRuntime(context.Background(), AppDatabaseConfig{
		Path: databasePath, Host: "127.0.0.1", Port: port,
		User: "kitdb", Password: "test-secret",
	}, make(chan error, 1))
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("postgres", fmt.Sprintf(
		"host=127.0.0.1 port=%d user=kitdb password=test-secret dbname=shutdown sslmode=disable connect_timeout=2",
		port,
	))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Ping(); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- runtime.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runtime close did not drain the PostgreSQL connection")
	}

	reopened, err := relational.Open(databasePath)
	if err != nil {
		t.Fatalf("database lock remained after runtime close: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAppDatabaseRuntimeCloseDrainsProjectionWorkers(t *testing.T) {
	port := availableLoopbackPort(t)
	runtime, err := startAppDatabaseRuntime(context.Background(), AppDatabaseConfig{
		Path: filepath.Join(t.TempDir(), "projection-shutdown.kitdb"), Host: "127.0.0.1", Port: port,
		User: "kitdb", Password: "test-secret",
	}, make(chan error, 1))
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("postgres", fmt.Sprintf(
		"host=127.0.0.1 port=%d user=kitdb password=test-secret dbname=projection-shutdown sslmode=disable connect_timeout=2",
		port,
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT SEARCHABLE, category TEXT ANALYTICS, price BIGINT ANALYTICS)`,
		`INSERT INTO products VALUES (1, 'keyboard', 'input', 100)`,
		`SELECT count(*), avg(price) FROM products WHERE category = 'input'`,
	} {
		if _, err := database.Exec(source); err != nil {
			database.Close()
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- runtime.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runtime close did not drain projection workers")
	}
}

func availableLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func appDatabaseTestCache() relational.QueryCacheOptions {
	return relational.QueryCacheOptions{
		Select: time.Minute, MaximumBytes: 1 << 20,
		MaximumEntries: 16, MaximumResult: 64 << 10,
	}
}
