package work

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/internal/loopback"
	"github.com/kitwork/engine/value"
	"github.com/lib/pq"
)

// connect({ postgresql: url }) carries what the app's URL says and nothing of the host's
// (KITWORK_DATABASE.md mục 1: "Quyền và ranh giới app do runtime cấp"; CLAUDE.md: an app never sees the
// host's or the OS's environment). lib/pq fills a setting a connection string leaves out from the
// process's environment, the host user's ~/.pgpass and ~/.postgresql/, so the URL must name host, user and
// database, may say only sslmode, connect_timeout and application_name, and every other setting is written.

// externalHandle is connect({ postgresql: source }) of an app booted for the test, or the test fails.
func externalHandle(t *testing.T, tenant *Tenant, source string) *ExternalDatabase {
	t.Helper()
	handle := connectExternal(tenant, []value.Value{value.New(map[string]value.Value{"postgresql": value.New(source)})})
	external, ok := handle.V.(*ExternalDatabase)
	if !ok {
		t.Fatalf("connect({ postgresql }): %v", handle)
	}
	return external
}

// bootQuietApp boots an app with one site and no route of its own.
func bootQuietApp(t *testing.T) *Tenant {
	t.Helper()
	tenant, _, err := bootApp(t, map[string]string{"shop.example/router.work": `import * as router from "kitwork/router";`}, "shop.example")
	if err != nil {
		t.Fatal(err)
	}
	return tenant
}

// A URL that leaves host, user or database to the environment, or names a setting of the host's (a file,
// a service), is refused when connect() is declared, before anything opens; the message never quotes the
// URL or its password.
func TestConnectURLIsTheAppsAlone(t *testing.T) {
	for _, test := range []struct{ source, want string }{
		{"postgres://crm:secret@/crm", "URL cần đúng một host: postgresql://user:password@host:5432/database; host không lấy từ môi trường"},
		{"postgres://crm:secret@a.example,b.example/crm", "URL cần đúng một host"},
		{"postgres://db.example/crm", "URL cần user: postgresql://user:password@host:5432/database; user không lấy từ môi trường"},
		{"postgres://:secret@db.example/crm", "URL cần user"},
		{"postgres://crm:secret@db.example", "URL cần tên database: postgresql://user:password@host:5432/database"},
		{"postgres://crm:secret@db.example:0/crm", "cổng trong URL là một số từ 1 tới 65535"},
		{"postgres://crm:secret@db.example/crm?sslcert=/etc/kitwork/client.crt", "URL có tham số sslcert, không nhận: chỉ sslmode, connect_timeout, application_name; chứng chỉ, passfile và service là của máy chạy app"},
		{"postgres://crm:secret@db.example/crm?sslkey=/etc/kitwork/client.key", "URL có tham số sslkey, không nhận"},
		{"postgres://crm:secret@db.example/crm?sslrootcert=/etc/ssl/private/ca.pem", "URL có tham số sslrootcert, không nhận"},
		{"postgres://crm:secret@db.example/crm?sslinline=true", "URL có tham số sslinline, không nhận"},
		{"postgres://crm:secret@db.example/crm?passfile=/root/.pgpass", "URL có tham số passfile, không nhận"},
		{"postgres://crm:secret@db.example/crm?service=prod", "URL có tham số service, không nhận"},
		{"postgres://crm:secret@db.example/crm?host=internal.db", "URL có tham số host, không nhận"},
		{"postgres://crm:secret@db.example/crm?password=other", "URL có tham số password, không nhận"},
		{"postgres://crm:secret@db.example/crm?X-Secret=1", "URL có tham số lạ, không nhận"},
		{"postgres://crm:secret@db.example/crm?sslmode=prefer", "sslmode là disable, require, verify-ca hay verify-full"},
		{"postgres://crm:secret@db.example/crm?connect_timeout=0", "connect_timeout là số giây, từ 1 tới 300"},
		{"postgres://crm:secret@db.example/crm?sslmode=require&sslmode=disable", "URL có tham số sslmode nhiều lần"},
		{"postgres://crm:se#cret@db.example/crm", "URL có dấu #: mật khẩu có ký tự đặc biệt thì mã hoá (%23 cho #)"},
		{"host=db.example user=crm password=secret dbname=crm", "URL không đọc được: viết postgresql://user:password@host:5432/database"},
	} {
		code, message := failureOf(connectExternal(nil, []value.Value{value.New(map[string]value.Value{"postgresql": value.New(test.source)})}))
		if code != value.InvalidArgumentCode || !strings.HasPrefix(message, "connect({ postgresql: … }): "+test.want) {
			t.Errorf("connect({ postgresql: %q }): %s %q, want INVALID_ARGUMENT …%s…", test.source, code, message, test.want)
		}
		for _, secret := range []string{"secret", "db.example", "/etc/", "/root", "other"} {
			if strings.Contains(message, secret) {
				t.Errorf("connect({ postgresql: %q }): the message quotes %q: %q", test.source, secret, message)
			}
		}
	}
	// what the URL may say
	settings, problem := readPostgresURL("postgresql://crm:p%40ss@db.example:6432/crm?sslmode=verify-full&connect_timeout=5&application_name=shop")
	if problem != "" || settings != (postgresURL{
		host: "db.example", port: "6432", database: "crm", user: "crm", password: "p@ss",
		sslmode: "verify-full", connectTimeout: "5", applicationName: "shop",
	}) {
		t.Fatalf("a full URL: %+v %q", settings, problem)
	}
	if settings, problem := readPostgresURL("postgres://crm@db.example/crm"); problem != "" || settings.port != "5432" || settings.sslmode != "require" || settings.connectTimeout != "10" || settings.password != "" {
		t.Fatalf("the defaults: %+v %q", settings, problem)
	}
	// a password with a quote, a backslash and a space reaches lib/pq as it was written
	settings, problem = readPostgresURL("postgres://crm:it%27s%20a%5Cpass%3D1@db.example/crm")
	if problem != "" {
		t.Fatal(problem)
	}
	connector, err := newPostgresConnector(settings.options())
	if err != nil {
		t.Fatal(err)
	}
	if password := connectorOptions(t, connector)["password"]; password != `it's a\pass=1` {
		t.Fatalf("the password lib/pq reads = %q", password)
	}
}

// connectorOptions is what a lib/pq connector will connect with: its opts, read by reflection (lib/pq
// keeps them unexported), as they stand after NewConnector has laid the process's environment under the
// connection string.
func connectorOptions(t *testing.T, connector *pq.Connector) map[string]string {
	t.Helper()
	field := reflect.ValueOf(connector).Elem().FieldByName("opts")
	if !field.IsValid() || field.Kind() != reflect.Map {
		t.Fatalf("lib/pq's Connector has no opts map: the test must follow the driver")
	}
	options := map[string]string{}
	for iterator := field.MapRange(); iterator.Next(); {
		options[iterator.Key().String()] = iterator.Value().String()
	}
	return options
}

// Every setting lib/pq takes from the process's environment is written by the URL or by the engine, so
// none of the host's reaches the connection: not its PGHOST/PGUSER/PGPASSWORD (the host's own database
// and credentials, sent to a server the app chooses), not its client certificate (PGSSLCERT, or
// ~/.postgresql/ when sslcert is empty), not its ~/.pgpass (read when no password is written), not its
// server settings. Run against the connector lib/pq builds, after its own environment pass.
func TestConnectCarriesNothingOfTheHost(t *testing.T) {
	host := map[string]string{
		"PGHOST": "host-db.internal", "PGPORT": "6543", "PGDATABASE": "host_system", "PGUSER": "host_user",
		"PGPASSWORD": "host-password", "PGOPTIONS": "-c search_path=host", "PGAPPNAME": "host-app",
		"PGSSLMODE": "disable", "PGSSLCERT": "/etc/kitwork/client.crt", "PGSSLKEY": "/etc/kitwork/client.key",
		"PGSSLROOTCERT": "/etc/kitwork/ca.pem", "PGSSLSNI": "0", "PGCONNECT_TIMEOUT": "99",
		"PGCLIENTENCODING": "LATIN1", "PGDATESTYLE": "German", "PGTZ": "Asia/Ho_Chi_Minh", "PGGEQO": "off",
	}
	for variable, setting := range host {
		t.Setenv(variable, setting)
	}
	settings, problem := readPostgresURL("postgres://crm@crm.example/crm")
	if problem != "" {
		t.Fatal(problem)
	}
	connector, err := newPostgresConnector(settings.options())
	if err != nil {
		t.Fatalf("connector: %v", err)
	}
	options := connectorOptions(t, connector)
	for key, setting := range options {
		for variable, hosts := range host {
			if setting == hosts {
				t.Errorf("%s = %q, the host's %s", key, setting, variable)
			}
		}
	}
	for key, want := range map[string]string{
		"host": "crm.example", "port": "5432", "dbname": "crm", "user": "crm",
		"password": "", "sslmode": "require", "connect_timeout": "10",
		"sslcert": noClientCertificate, "sslkey": noClientCertificate, "sslrootcert": "", "sslsni": "1",
		"options": "", "application_name": "", "timezone": "UTC", "geqo": "on",
		"client_encoding": "UTF8", "datestyle": "ISO, MDY",
	} {
		if got, written := options[key]; !written || got != want {
			t.Errorf("%s = %q (written %v), want %q", key, got, written, want)
		}
	}
	if _, err := os.Stat(noClientCertificate); !os.IsNotExist(err) {
		t.Fatalf("the path written as sslcert must not exist: %v", err)
	}

	// without the host's server settings in the environment, none is written: a pooler that refuses
	// settings it does not know sees what it saw before
	for _, variable := range []string{"PGOPTIONS", "PGAPPNAME", "PGTZ", "PGGEQO"} {
		if err := os.Unsetenv(variable); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"options", "application_name", "timezone", "geqo"} {
		if setting, written := settings.options()[key]; written {
			t.Errorf("%s = %q written with no such variable on the host", key, setting)
		}
	}
}

// End to end: the host's environment holds the database's password, the app's URL none. The server never
// receives it, so the login is refused (DENIED); written by the URL, the same login is let in.
func TestConnectSendsNoPasswordOfTheHost(t *testing.T) {
	_, address, _ := startKitDBPostgresTransactionTest(t, 0)
	seed := openKitDBPostgresDatabaseTestClient(t, address, "transactions", "transaction-secret")
	defer seed.Close()
	if _, err := seed.Exec(`INSERT INTO products (id, sku, title, price) VALUES ('a', 'CAF', 'Cà phê', 35000)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	restore := loopback.Open()
	defer restore()
	t.Setenv("PGPASSWORD", "transaction-secret")
	t.Setenv("PGPASSFILE", filepath.Join(t.TempDir(), "pgpass"))
	if err := os.WriteFile(os.Getenv("PGPASSFILE"), []byte("*:*:*:*:transaction-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	without := &url.URL{Scheme: "postgres", User: url.User("kitdb"), Host: address, Path: "/transactions", RawQuery: "sslmode=disable&connect_timeout=3"}
	tenant, _, err := bootApp(t, crmAppAt(without.String()), "shop.example")
	if err != nil {
		t.Fatal(err)
	}
	if refused := answer(t, tenant, "?sku=CAF"); refused["code"] != value.DeniedCode {
		t.Fatalf("a URL with no password, the host's PGPASSWORD set: %v, want DENIED (the host's password must not be sent)", refused)
	}

	with := *without
	with.User = url.UserPassword("kitdb", "transaction-secret")
	tenant, _, err = bootApp(t, crmAppAt(with.String()), "shop.example")
	if err != nil {
		t.Fatal(err)
	}
	if found := answer(t, tenant, "?sku=CAF"); count(t, found, "rows") != 1 {
		t.Fatalf("CONTROL: the password written in the URL: %v, want the row", found)
	}
}

// lib/pq panics on some variables of the environment (PGSERVICEFILE, PGHOSTADDR, …), even empty: the
// query says the connection cannot open, the host's log names the variable, and nothing panics.
func TestConnectRefusedEnvironment(t *testing.T) {
	tenant := bootQuietApp(t)
	t.Setenv("PGSERVICEFILE", "")
	external := externalHandle(t, tenant, "postgres://crm:secret@crm.example/crm")
	code, message := failureOf(external.OnInvoke("query", value.New("SELECT 1")))
	if code != value.UnavailableCode || message != "connect({ postgresql: … }): môi trường của máy chạy app có biến mà bộ nối postgresql không nhận, nên không mở kết nối" {
		t.Fatalf("query with PGSERVICEFILE set on the host: %s %q", code, message)
	}
	if variable := refusedPostgresEnvironment(); variable != "PGSERVICEFILE" {
		t.Fatalf("refusedPostgresEnvironment() = %q", variable)
	}
}

// What a failed query says: what happened, by its code, and never what the connection holds (an address,
// a port, a role, a user, a database name), though a server's message quotes them.
func TestConnectFailuresSayWhatHappened(t *testing.T) {
	external := &ExternalDatabase{connector: "postgresql"}
	for _, test := range []struct {
		err           error
		code, message string
	}{
		{&deniedDatabaseAddress{}, value.DeniedCode, "connect({ postgresql: … }): địa chỉ của database nằm trong mạng riêng hay trên chính máy này, bị chặn"},
		{context.DeadlineExceeded, value.TimeoutCode, "query chạy quá 30s"},
		{&net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("203.0.113.9"), Port: 5432}, Err: errors.New("connection refused")},
			value.UnavailableCode, "connect({ postgresql: … }): không nối được database"},
		{io.ErrUnexpectedEOF, value.UnavailableCode, "connect({ postgresql: … }): không nối được database"},
		{&pq.Error{Code: "08006", Message: "connection failure"}, value.UnavailableCode, "connect({ postgresql: … }): mất kết nối tới database"},
		{&pq.Error{Code: "28P01", Message: `password authentication failed for user "crm_admin"`}, value.DeniedCode, "connect({ postgresql: … }): database từ chối đăng nhập hay không có database đó"},
		{&pq.Error{Code: "28000", Message: `no pg_hba.conf entry for host "198.51.100.7", user "crm_admin", database "crm_prod"`}, value.DeniedCode, "connect({ postgresql: … }): database từ chối đăng nhập hay không có database đó"},
		{&pq.Error{Code: "3D000", Message: `database "crm_prod" does not exist`}, value.DeniedCode, "connect({ postgresql: … }): database từ chối đăng nhập hay không có database đó"},
		{&pq.Error{Code: "53300", Message: `too many connections for role "crm_admin"`}, value.UnavailableCode, "connect({ postgresql: … }): database hết tài nguyên (kết nối, bộ nhớ hay đĩa), thử lại sau"},
		{&pq.Error{Code: "57P03", Message: "the database system is starting up"}, value.UnavailableCode, "connect({ postgresql: … }): database đang dừng hay chưa nhận kết nối"},
		{&pq.Error{Code: "57014", Message: "canceling statement due to statement timeout"}, value.TimeoutCode, "query bị database dừng giữa chừng (statement_timeout hay bị huỷ)"},
		{&pq.Error{Code: "42501", Message: `permission denied for database "crm_prod"`}, value.DatabaseCode, `query: permission denied for database "…"`},
		{&pq.Error{Code: "55000", Message: `database "crm_prod" is not currently accepting connections`}, value.DatabaseCode, `query: database "…" is not currently accepting connections`},
		{&pq.Error{Code: "42501", Message: `permission denied to set role "crm_owner"`}, value.DatabaseCode, `query: permission denied to set role "…"`},
		// the app's own names stay: they are what it wrote
		{&pq.Error{Code: "42P01", Message: `relation "orders" does not exist`}, value.DatabaseCode, `query: relation "orders" does not exist`},
	} {
		code, message := failureOf(external.failure(test.err))
		if code != test.code || message != test.message {
			t.Errorf("%v: %s %q, want %s %q", test.err, code, message, test.code, test.message)
		}
		for _, held := range []string{"crm_admin", "crm_prod", "crm_owner", "203.0.113.9", "198.51.100.7", "5432"} {
			if strings.Contains(message, held) {
				t.Errorf("%v: the message holds %q: %q", test.err, held, message)
			}
		}
	}
}

// injectRows puts pool in the app's pool under the handle's configuration, so the handle's queries run on
// it without a server.
func injectRows(t *testing.T, tenant *Tenant, external *ExternalDatabase, pool *sql.DB) {
	t.Helper()
	if _, err := tenant.databaseManager().Open(external.poolKey(), func() (*sql.DB, error) { return pool, nil }); err != nil {
		t.Fatal(err)
	}
}

// sqliteRows is an empty SQLite file for injectRows.
func sqliteRows(t *testing.T) *sql.DB {
	t.Helper()
	rows, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "rows.db"))
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// The cells a query gives as the app reads them: a whole number past what JavaScript holds exactly as its
// digits, bytes that are not UTF-8 as \x hex, a moment in RFC 3339 (UTC), null as null; a value bound that
// a database cannot hold (NaN, Infinity) is refused naming its place, before anything opens.
func TestConnectQueryCells(t *testing.T) {
	tenant := bootQuietApp(t)
	external := externalHandle(t, tenant, "postgres://u:p@db.example/cells")
	injectRows(t, tenant, external, sqliteRows(t))
	result := external.OnInvoke("query", value.New(`SELECT 9007199254740993 AS big, -9007199254740993 AS low, 9007199254740991 AS exact, x'ff00' AS raw, 'Cà phê' AS title, NULL AS empty`))
	if result.K != value.Array || len(result.Array()) != 1 {
		t.Fatalf("query: %v", result)
	}
	row := result.Array()[0].Map()
	for column, want := range map[string]value.Value{
		"big": value.New("9007199254740993"), "low": value.New("-9007199254740993"), "exact": value.New(float64(9007199254740991)),
		"raw": value.New(`\xff00`), "title": value.New("Cà phê"),
	} {
		if got := row[column]; got.K != want.K || got.String() != want.String() {
			t.Errorf("%s = %v (%s), want %v (%s)", column, got, got.K, want, want.K)
		}
	}
	if !row["empty"].IsNil() {
		t.Errorf("empty = %v, want null", row["empty"])
	}
	moment := time.Date(2026, 10, 22, 9, 30, 0, 5, time.FixedZone("ICT", 7*3600))
	if got := externalCell(moment); got.String() != "2026-10-22T02:30:00.000000005Z" {
		t.Errorf("a moment = %v, want RFC 3339 in UTC", got)
	}
	for index, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		code, message := failureOf(external.OnInvoke("query", value.New("SELECT $1, $2"), value.New([]value.Value{value.New(1), value.New(bad)})))
		if code != value.InvalidArgumentCode || message != "giá trị thứ 2 của query là NaN hay Infinity: database không giữ được" {
			t.Errorf("value %d (%v): %s %q", index, bad, code, message)
		}
	}
}

// An app keeps at most appPoolCeiling pools open: a configuration past it is UNAVAILABLE, while one already
// open keeps answering.
func TestConnectPoolCeiling(t *testing.T) {
	tenant := bootQuietApp(t)
	open := externalHandle(t, tenant, "postgres://u:p@db.example/open")
	injectRows(t, tenant, open, sqliteRows(t))
	manager := tenant.databaseManager()
	for filler := 0; manager.Count() < appPoolCeiling; filler++ {
		pool, err := sql.Open("sqlite", filepath.Join(t.TempDir(), fmt.Sprintf("filler-%d.db", filler)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Open(fmt.Sprintf("test:filler:%d", filler), func() (*sql.DB, error) { return pool, nil }); err != nil {
			t.Fatal(err)
		}
	}
	another := externalHandle(t, tenant, "postgres://u:p@db.example/another")
	code, message := failureOf(another.OnInvoke("query", value.New("SELECT 1")))
	if code != value.UnavailableCode || message != fmt.Sprintf("connect: app đã mở %d pool database, trần của một app", appPoolCeiling) {
		t.Fatalf("a configuration past the ceiling: %s %q", code, message)
	}
	if answered := open.OnInvoke("query", value.New("SELECT 1 AS one")); answered.K != value.Array || len(answered.Array()) != 1 {
		t.Fatalf("an open configuration at the ceiling: %v", answered)
	}
}

// A query bounds the text and bytes it gives, not only its rows: past externalByteCeiling it fails
// TOO_LARGE, so a server the app chose cannot make the host hold gigabytes.
func TestConnectQueryByteBound(t *testing.T) {
	tenant := bootQuietApp(t)
	external := externalHandle(t, tenant, "postgres://u:p@db.example/bytes")
	injectRows(t, tenant, external, sqliteRows(t))
	const megabytes = `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 12) SELECT zeroblob(1048576) AS chunk FROM n`
	code, message := failureOf(external.OnInvoke("query", value.New(megabytes)))
	if code != value.TooLargeCode || message != "query trả quá 10 MiB: chọn ít cột hơn, thêm LIMIT vào câu, đọc theo trang" {
		t.Fatalf("12 MiB: %s %q", code, message)
	}
	if under := external.OnInvoke("query", value.New(megabytes+" LIMIT 9")); under.K != value.Array || len(under.Array()) != 9 {
		t.Fatalf("9 MiB: %v", under)
	}
}

// endlessConnector is a driver whose query never ends: each row is one more. It records whether the
// query's context was cancelled when its rows closed: closing them first would make lib/pq read the rest
// of the result (rows.Close reads to the end) before the cancel asks the server to stop.
type endlessConnector struct{ cancelledAtClose, liveAtClose *atomic.Int32 }

func (c endlessConnector) Connect(context.Context) (driver.Conn, error) { return endlessConn{c}, nil }
func (c endlessConnector) Driver() driver.Driver                        { return endlessDriver{} }

type endlessDriver struct{}

func (endlessDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("endless: use the connector")
}

type endlessConn struct{ connector endlessConnector }

func (endlessConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("endless: no prepare")
}
func (endlessConn) Close() error              { return nil }
func (endlessConn) Begin() (driver.Tx, error) { return nil, errors.New("endless: no transaction") }
func (c endlessConn) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	return &endlessRows{ctx: ctx, connector: c.connector}, nil
}

type endlessRows struct {
	ctx       context.Context
	connector endlessConnector
	closed    bool
}

func (r *endlessRows) Columns() []string { return []string{"sku"} }
func (r *endlessRows) Next(destination []driver.Value) error {
	destination[0] = "S"
	return nil
}
func (r *endlessRows) Close() error {
	if !r.closed {
		r.closed = true
		if r.ctx.Err() != nil {
			r.connector.cancelledAtClose.Add(1)
		} else {
			r.connector.liveAtClose.Add(1)
		}
	}
	return nil
}

// A query that leaves before its last row (past the bound of rows) cancels its context before its rows
// close, so the driver stops the server's query instead of draining it.
func TestConnectQueryStopsBeforeClosing(t *testing.T) {
	tenant := bootQuietApp(t)
	external := externalHandle(t, tenant, "postgres://u:p@db.example/endless")
	connector := endlessConnector{cancelledAtClose: &atomic.Int32{}, liveAtClose: &atomic.Int32{}}
	injectRows(t, tenant, external, sql.OpenDB(connector))
	code, _ := failureOf(external.OnInvoke("query", value.New("SELECT sku FROM endless")))
	if code != value.TooLargeCode {
		t.Fatalf("an endless query: %s, want TOO_LARGE", code)
	}
	if connector.cancelledAtClose.Load() != 1 || connector.liveAtClose.Load() != 0 {
		t.Fatalf("rows closed with the query's context live %d time(s), cancelled %d: the cancel must come first",
			connector.liveAtClose.Load(), connector.cancelledAtClose.Load())
	}
}
