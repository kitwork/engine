package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/work"
	"github.com/lib/pq"
)

// The tests of the host's database port (app_database_runtime.go): app.database serving the SQLite files of the
// host's .database/ and of each app's, read-only, over the PostgreSQL wire, with SCRAM-SHA-256 and each cluster's
// accounts (KITWORK_KITDB.md câu 13.5).

const appDatabaseTestPassword = "port-admin-password"

// appDatabaseFixture is a host with a database of its own (shopping) and an app acme with the domain shop.example,
// whose database application has the schema public (kitwork.db) and the schema billing (billing.db).
type appDatabaseFixture struct {
	root    string
	host    string
	apps    string
	address string
}

func newAppDatabaseFixture(t *testing.T) *appDatabaseFixture {
	t.Helper()
	root := t.TempDir()
	fixture := &appDatabaseFixture{root: root, host: filepath.Join(root, ".database"), apps: filepath.Join(root, "apps")}
	writeAppDatabaseFile(t, filepath.Join(fixture.host, "shopping", "kitwork.db"),
		`CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT NOT NULL, price INTEGER NOT NULL)`,
		`INSERT INTO products (name, price) VALUES ('cà phê', 3500000), ('trà', 450)`,
		`CREATE TABLE kitwork_columns (table_name TEXT NOT NULL, column_name TEXT NOT NULL, kind TEXT NOT NULL, precision INTEGER NOT NULL, scale INTEGER NOT NULL, PRIMARY KEY (table_name, column_name))`,
		`INSERT INTO kitwork_columns VALUES ('products', 'price', 'decimal', 12, 2)`)
	site := filepath.Join(fixture.apps, "acme", "shop.example")
	if err := os.MkdirAll(site, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(site, "router.work"), []byte(`import * as router from "kitwork/router";`), 0o644); err != nil {
		t.Fatal(err)
	}
	application := filepath.Join(fixture.apps, "acme", ".database", "application")
	writeAppDatabaseFile(t, filepath.Join(application, "kitwork.db"),
		`CREATE TABLE orders (id TEXT PRIMARY KEY, email TEXT NOT NULL, amount INTEGER NOT NULL, paid INTEGER NOT NULL)`,
		`INSERT INTO orders VALUES ('o1', 'an@example.com', 3500000, 1), ('o2', 'binh@example.com', 9000000, 0)`,
		`CREATE TABLE kitwork_columns (table_name TEXT NOT NULL, column_name TEXT NOT NULL, kind TEXT NOT NULL, precision INTEGER NOT NULL, scale INTEGER NOT NULL, PRIMARY KEY (table_name, column_name))`,
		`INSERT INTO kitwork_columns VALUES ('orders', 'amount', 'decimal', 12, 2), ('orders', 'paid', 'bool', 0, 0)`)
	writeAppDatabaseFile(t, filepath.Join(application, "billing.db"),
		`CREATE TABLE invoices (id TEXT PRIMARY KEY, order_id TEXT NOT NULL, code TEXT NOT NULL)`,
		`INSERT INTO invoices VALUES ('i1', 'o1', 'HD-1')`)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	runtime, err := startAppDatabaseRuntime(ctx, AppDatabaseConfig{
		Path: fixture.host, Host: "127.0.0.1", Port: port, User: "kitdb", Password: appDatabaseTestPassword,
	}, appDatabaseApps{root: fixture.apps, layout: work.RootLayoutMultiTenant}, make(chan error, 1))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if err := runtime.Close(); err != nil {
			t.Errorf("close the port: %v", err)
		}
	})
	fixture.address = fmt.Sprintf("127.0.0.1:%d", port)
	return fixture
}

func writeAppDatabaseFile(t *testing.T, path string, statements ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, statement := range statements {
		if _, err := file.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

// connect opens a client of the port as user on database, through lib/pq (SCRAM-SHA-256).
func (fixture *appDatabaseFixture) connect(t *testing.T, user, password, database string) *sql.DB {
	t.Helper()
	host, port, _ := net.SplitHostPort(fixture.address)
	connector, err := pq.NewConnector(fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", host, port, user, password, database))
	if err != nil {
		t.Fatal(err)
	}
	client := sql.OpenDB(connector)
	client.SetMaxOpenConns(1)
	t.Cleanup(func() { client.Close() })
	return client
}

func appDatabaseRows(t *testing.T, client *sql.DB, query string, arguments ...any) string {
	t.Helper()
	rows, err := client.Query(query, arguments...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	columns, _ := rows.Columns()
	var lines []string
	for rows.Next() {
		values := make([]sql.NullString, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		fields := make([]string, len(values))
		for index, item := range values {
			fields[index] = item.String
			if !item.Valid {
				fields[index] = "NULL"
			}
		}
		lines = append(lines, strings.Join(fields, " "))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return strings.Join(lines, "; ")
}

// appDatabaseQuery runs query and reads all it gives, closing its rows: a query that should have been refused and
// was not leaves no connection held.
func appDatabaseQuery(client *sql.DB, query string) error {
	rows, err := client.Query(query)
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	return rows.Close()
}

func appDatabaseCode(err error) string {
	var wire *pq.Error
	if errors.As(err, &wire) {
		return string(wire.Code)
	}
	return ""
}

// The admin of app.database reads the host's database by its folder's name and an app's by <domain>/<database>, its
// schemas the files of the folder (users, public.users and billing.invoices, joined), decimals in their scale and
// booleans as booleans, with $n parameters; a database nobody has is 3D000; a write is 25006, and the files keep their
// bytes.
//
// CONTROL: with appDatabaseGateway.resolve taking the domain as a folder of the host's .database/, "SELECT … FROM
// orders: pq: database \"shop.example/application\" does not exist".
func TestAppDatabasePortReadsTheHostsAndAnAppsDatabases(t *testing.T) {
	fixture := newAppDatabaseFixture(t)
	shopping := fixture.connect(t, "kitdb", appDatabaseTestPassword, "shopping")
	if got := appDatabaseRows(t, shopping, `SELECT name, price FROM products ORDER BY id`); got != "cà phê 35000.00; trà 4.50" {
		t.Errorf("shopping.products = %q", got)
	}
	application := fixture.connect(t, "kitdb", appDatabaseTestPassword, "shop.example/application")
	got := appDatabaseRows(t, application, `SELECT o.id, o.amount, o.paid, i.code FROM public.orders AS o LEFT JOIN billing.invoices AS i ON i.order_id = o.id WHERE o.email <> $1 OR o.email = $1 ORDER BY o.id`, "nobody@example.com")
	if got != "o1 35000.00 true HD-1; o2 90000.00 false NULL" {
		t.Errorf("orders joined with billing.invoices = %q", got)
	}
	if got := appDatabaseRows(t, application, `SELECT count(*) FROM "public"."orders"`); got != "2" {
		t.Errorf(`"public"."orders" = %q`, got)
	}
	if _, err := application.Exec(`INSERT INTO orders VALUES ('o3', 'c@example.com', 1, 0)`); appDatabaseCode(err) != "25006" {
		t.Errorf("INSERT = %v, want 25006", err)
	}
	missing := fixture.connect(t, "kitdb", appDatabaseTestPassword, "nowhere.example/application")
	if err := missing.Ping(); appDatabaseCode(err) != "3D000" {
		t.Errorf("a database nobody has = %v, want 3D000", err)
	}
}

// Accounts are each cluster's, made by its admin over the port: a user given CONNECT on an app's database and SELECT on
// one table reads that table and is refused the other (42501), signs in with SCRAM (a wrong password is 28P01), is no
// user of another cluster, and loses the session when its grants change. A user that is no admin cannot make one.
//
// CONTROL: with appDatabaseAccess.CanSelect answering true for everyone, "billing.invoices as reader: <nil>, want 42501".
func TestAppDatabasePortAccountsAndGrants(t *testing.T) {
	fixture := newAppDatabaseFixture(t)
	admin := fixture.connect(t, "kitdb", appDatabaseTestPassword, "shop.example/application")
	for _, statement := range []string{
		`CREATE USER reader WITH PASSWORD 'reader-password-1'`,
		`GRANT CONNECT ON DATABASE "shop.example/application" TO reader`,
		`GRANT SELECT ON TABLE orders TO reader`,
	} {
		if _, err := admin.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	reader := fixture.connect(t, "reader", "reader-password-1", "shop.example/application")
	if got := appDatabaseRows(t, reader, `SELECT id FROM orders ORDER BY id`); got != "o1; o2" {
		t.Errorf("orders as reader = %q", got)
	}
	if err := appDatabaseQuery(reader, `SELECT code FROM billing.invoices`); appDatabaseCode(err) != "42501" {
		t.Errorf("billing.invoices as reader: %v, want 42501", err)
	}
	if _, err := reader.Exec(`CREATE USER sneaky WITH PASSWORD 'sneaky-password-1'`); appDatabaseCode(err) != "42501" {
		t.Errorf("CREATE USER as reader: %v, want 42501", err)
	}
	if err := fixture.connect(t, "reader", "wrong-password-1", "shop.example/application").Ping(); appDatabaseCode(err) != "28P01" {
		t.Errorf("a wrong password: %v, want 28P01", err)
	}
	if err := fixture.connect(t, "reader", "reader-password-1", "shopping").Ping(); appDatabaseCode(err) != "28P01" {
		t.Errorf("reader on the host's cluster: %v, want 28P01 (no such user there)", err)
	}
	if _, err := admin.Exec(`REVOKE SELECT ON TABLE orders FROM reader`); err != nil {
		t.Fatal(err)
	}
	if err := appDatabaseQuery(reader, `SELECT id FROM orders`); appDatabaseCode(err) != "57P01" {
		t.Errorf("the reader's session after its grants changed: %v, want 57P01", err)
	}
}

// app.database takes path, host, port, user, password, concurrency; the KitDB port's options are errors saying why
// they went, and the port needs a port and a password.
//
// CONTROL: with appDatabaseRemovedOptions empty, "memory: unknown option \"memory\" …, want the reason it went".
func TestAppDatabaseConfigOptions(t *testing.T) {
	for option, value := range map[string]interface{}{"alias": "shop", "kitsql": true, "memory": "64mb", "warm": true, "cache": "1m"} {
		_, err := parseAppDatabaseConfig(map[string]interface{}{"path": "./.database/", "port": float64(5455), "password": "x", option: value})
		if err == nil || !strings.Contains(err.Error(), "is gone (30/10/2026") {
			t.Errorf("%s: %v, want the reason it went", option, err)
		}
	}
	if _, err := parseAppDatabaseConfig(map[string]interface{}{"path": "./.database/", "password": "x"}); err == nil || !strings.Contains(err.Error(), "port is required") {
		t.Errorf("no port: %v", err)
	}
	if _, err := parseAppDatabaseConfig(map[string]interface{}{"path": "./.database/", "port": float64(5455)}); err == nil || !strings.Contains(err.Error(), "password is required") {
		t.Errorf("no password: %v", err)
	}
	config, err := parseAppDatabaseConfig(map[string]interface{}{"path": "./.database/", "port": float64(5455), "user": "admin", "password": "x", "concurrency": float64(2)})
	if err != nil || config.Port != 5455 || config.User != "admin" || config.Concurrency != 2 || config.Host != "127.0.0.1" {
		t.Errorf("config = %+v, %v", config, err)
	}
}
