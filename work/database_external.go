package work

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	hostdatabase "github.com/kitwork/engine/database"
	"github.com/kitwork/engine/database/statement"
	httputil "github.com/kitwork/engine/utilities/http"
	"github.com/kitwork/engine/value"
	"github.com/lib/pq"
)

// connect({ postgresql: url }) of kitwork/database (KITWORK_DATABASE.md mục 1, mục 2 "SQL thô"): a
// database outside the app, which the app connects to itself, with its own credentials, no alias. It is
// someone else's, so it has no schema, policy, before or after here: it answers query(sql, [giá trị]) and
// nothing else.
//
//	export const crm = connect({ postgresql: env.CRM_URL });                    // .shared/schema/crm.work
//	crm.query("SELECT id, email FROM customers WHERE id = $1", [customerId]);  // anywhere in the app
//
// Declaring opens nothing: the pool opens when the first query runs, one per configuration in the app's
// pool, and every value is bound by the driver, never written into the SQL. Its dial goes through the
// SSRF guard of kitwork/http, so an app reaches no address in private or local space (the host's own
// databases among them). The connection carries what the app's URL says and nothing of the host's: lib/pq
// fills what a connection string leaves out from the process's environment (PGHOST, PGUSER, PGPASSWORD,
// PGSSLCERT, …), the host user's ~/.pgpass and ~/.postgresql/, so the URL must name host, user and
// database, and every setting lib/pq would take from the host is written out (postgresURL). A host turns
// connect() off before it opens to other people's apps with app.policy({ tenantConnections: false }) in
// application.work.

// tenantConnections is app.policy({ tenantConnections }) of the host: whether app code may connect({ … }).
var tenantConnections atomic.Bool

func init() { tenantConnections.Store(true) }

// SetTenantConnections is app.policy({ tenantConnections }) of the host's manifest, set at boot.
func SetTenantConnections(allowed bool) { tenantConnections.Store(allowed) }

// TenantConnections reports whether app code may connect({ … }) on this host.
func TenantConnections() bool { return tenantConnections.Load() }

const (
	// externalQueryTimeout bounds one query(): the handle is declared at load, so no request's context
	// reaches it.
	externalQueryTimeout = 30 * time.Second
	// externalRowCeiling is the most rows one query() gives, as list() gives at most statementRowCeiling.
	externalRowCeiling = statementRowCeiling
	// externalByteCeiling is the most text and bytes one query() gives, as kitwork/http reads at most
	// 10 MiB of a body by default. The driver reads a whole row before it is counted, so it bounds a
	// result, not one row (Postgres caps a field at 1 GB).
	externalByteCeiling = 10 << 20
	// appPoolCeiling is how many pools one app keeps open at once: every database of the app, its own
	// files and its connect() configurations together (mục 6, "số pool có trần").
	appPoolCeiling = 16
	// externalMaxOpen and externalMaxIdle bound the connections of one connect() pool.
	externalMaxOpen = 8
	externalMaxIdle = 2
)

const disabledConnect = "connect(…) bị host tắt: application.work khai app.policy({ tenantConnections: false }); database có tên là connection(\"tên\") với app.databases({ … }) của host"

// connectExternal is connect(source): a handle on the database outside the app, checked but not opened.
func connectExternal(tenant *Tenant, args []value.Value) value.Value {
	if !tenantConnections.Load() {
		return value.InvalidFailure(value.DeniedCode, disabledConnect)
	}
	shape := "connect nhận một object có một bộ nối: connect({ postgresql: environment.CRM_URL }), bộ nối là postgresql hay mysql"
	if len(args) != 1 || args[0].K != value.Map || len(args[0].Map()) != 1 {
		return value.InvalidFailure(value.InvalidArgumentCode, shape)
	}
	for connector, source := range args[0].Map() {
		switch {
		case connector == "sqlite":
			return value.InvalidFailure(value.InvalidArgumentCode, `connect({ sqlite: … }) không có: connect nối ra database ngoài (postgresql, mysql); SQLite là connection("tên") với { sqlite: đường_dẫn } trong app.databases({ … }) của application.work`)
		case !statement.Connectors[connector]:
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("connect({ %s: … }): %s", connector, shape))
		}
		// the value is never written in a message: it holds the credentials
		if source.K != value.String || strings.TrimSpace(source.String()) == "" {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("connect({ %s: … }) cần URL của database là một chuỗi, như environment.CRM_URL; giá trị ở đây là %s", connector, describeKind(source)))
		}
		url := strings.TrimSpace(source.String())
		kind := "postgres"
		if connector == "mysql" {
			kind = "mysql"
		}
		if separator := strings.Index(url, "://"); separator > 0 {
			if scheme := strings.ToLower(url[:separator]); scheme != connector && !(connector == "postgresql" && scheme == "postgres") {
				return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("connect({ %s: … }): URL có scheme %s, không phải của %s", connector, scheme, connector))
			}
		}
		if !hostdatabase.DriverLinked(kind) {
			return value.InvalidFailure(value.UnsupportedCode, fmt.Sprintf("connect({ %s: … }): bản Kitwork này không có bộ nối %s", connector, connector))
		}
		external := &ExternalDatabase{tenant: tenant, connector: connector, config: &hostdatabase.Config{Type: kind, URL: url}}
		if kind == "postgres" {
			settings, problem := readPostgresURL(url)
			if problem != "" {
				return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("connect({ %s: … }): %s", connector, problem))
			}
			external.postgres = settings
		}
		return value.Value{K: value.Proxy, V: external}
	}
	return value.InvalidFailure(value.InvalidArgumentCode, shape)
}

// postgresURLForm is how a connect() URL is written, for a message that must not quote the URL itself.
const postgresURLForm = "postgresql://user:password@host:5432/database"

// postgresURL is the connection a connect({ postgresql: url }) names, read from its URL alone: where (host,
// port, database), who (user, password) and how (sslmode, connect_timeout, application_name).
type postgresURL struct {
	host, port, database, user, password     string
	sslmode, connectTimeout, applicationName string
}

// readPostgresURL reads an app's connect() URL, or says what is wrong with it without quoting it (it holds
// the password). host, user and database must be written: left out, lib/pq would take them from the host's
// environment. A setting that names a file or a service of the host (sslcert, sslkey, sslrootcert,
// sslinline, passfile, service, …) is refused with every other setting the list does not have.
func readPostgresURL(source string) (postgresURL, string) {
	if strings.Contains(source, "#") {
		return postgresURL{}, "URL có dấu #: mật khẩu có ký tự đặc biệt thì mã hoá (%23 cho #)"
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Opaque != "" || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return postgresURL{}, "URL không đọc được: viết " + postgresURLForm + ", mật khẩu có ký tự đặc biệt thì mã hoá (%40 cho @)"
	}
	host := parsed.Hostname()
	if host == "" || strings.ContainsAny(host, ",/") {
		return postgresURL{}, "URL cần đúng một host: " + postgresURLForm + "; host không lấy từ môi trường của máy chạy app"
	}
	port := parsed.Port()
	if port == "" {
		port = "5432"
	} else if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 {
		return postgresURL{}, "cổng trong URL là một số từ 1 tới 65535"
	}
	if parsed.User == nil || parsed.User.Username() == "" {
		return postgresURL{}, "URL cần user: " + postgresURLForm + "; user không lấy từ môi trường của máy chạy app"
	}
	database := strings.TrimPrefix(parsed.Path, "/")
	if database == "" {
		return postgresURL{}, "URL cần tên database: " + postgresURLForm
	}
	password, _ := parsed.User.Password()
	settings := postgresURL{
		host: host, port: port, database: database, user: parsed.User.Username(), password: password,
		sslmode: "require", connectTimeout: "10",
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return postgresURL{}, "tham số của URL không đọc được"
	}
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		written := query[key]
		if len(written) != 1 {
			return postgresURL{}, fmt.Sprintf("URL có tham số %s nhiều lần", settingName(key))
		}
		switch setting := written[0]; key {
		case "sslmode":
			switch setting {
			case "disable", "require", "verify-ca", "verify-full":
				settings.sslmode = setting
			default:
				return postgresURL{}, "sslmode là disable, require, verify-ca hay verify-full"
			}
		case "connect_timeout":
			if seconds, err := strconv.Atoi(setting); err != nil || seconds < 1 || seconds > 300 {
				return postgresURL{}, "connect_timeout là số giây, từ 1 tới 300"
			}
			settings.connectTimeout = setting
		case "application_name":
			settings.applicationName = setting
		default:
			return postgresURL{}, fmt.Sprintf("URL có tham số %s, không nhận: chỉ sslmode, connect_timeout, application_name; chứng chỉ, passfile và service là của máy chạy app, app không trỏ tới", settingName(key))
		}
	}
	return settings, ""
}

// settingName is a URL setting's name as a message repeats it: a name like libpq's, or "lạ".
func settingName(key string) string {
	if key == "" || len(key) > 40 {
		return "lạ"
	}
	for _, character := range key {
		if (character < 'a' || character > 'z') && character != '_' {
			return "lạ"
		}
	}
	return key
}

// noClientCertificate is a path no file is at, written as sslcert and sslkey: with sslcert empty, lib/pq
// loads the host user's ~/.postgresql/postgresql.crt and .key, and it skips a certificate that does not
// exist. The folder is never made, and its name is drawn once per process.
var noClientCertificate = func() string {
	random := make([]byte, 8)
	_, _ = rand.Read(random)
	return filepath.Join(os.TempDir(), "kitwork-connect-"+hex.EncodeToString(random), "no-client-certificate.crt")
}()

// postgresEnvironmentSettings are the server's run-time settings lib/pq takes from the environment, with
// the value written in their place: only when the host's environment has one, so a pooler that refuses
// settings it does not know (PgBouncer) sees nothing new.
var postgresEnvironmentSettings = []struct{ variable, setting, written string }{
	{"PGOPTIONS", "options", ""},
	{"PGAPPNAME", "application_name", ""},
	{"PGTZ", "timezone", "UTC"},
	{"PGGEQO", "geqo", "on"},
}

// options are the settings lib/pq connects with: the URL's, and in place of everything lib/pq would fill
// from the host (its environment, ~/.pgpass, ~/.postgresql/) a value written out.
func (s postgresURL) options() map[string]string {
	options := map[string]string{
		"host": s.host, "port": s.port, "dbname": s.database, "user": s.user,
		"password":        s.password, // written even when empty: lib/pq then reads no ~/.pgpass
		"sslmode":         s.sslmode,
		"connect_timeout": s.connectTimeout,
		"sslcert":         noClientCertificate,
		"sslkey":          noClientCertificate,
		"sslrootcert":     "", // verify-ca and verify-full check against the system's roots
		"sslsni":          "1",
		"client_encoding": "UTF8",
		"datestyle":       "ISO, MDY",
	}
	if s.applicationName != "" {
		options["application_name"] = s.applicationName
	}
	for _, environment := range postgresEnvironmentSettings {
		if _, set := os.LookupEnv(environment.variable); set {
			if _, written := options[environment.setting]; !written {
				options[environment.setting] = environment.written
			}
		}
	}
	return options
}

// connectionString writes options as lib/pq reads a key=value string: each value quoted, ' and \ escaped.
func connectionString(options map[string]string) string {
	keys := make([]string, 0, len(options))
	for key := range options {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		quoted := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(options[key])
		parts = append(parts, key+"='"+quoted+"'")
	}
	return strings.Join(parts, " ")
}

// postgresRefusedEnvironment are the variables lib/pq panics on when the process has one at all, even
// empty (parseEnviron): with one set, no connector can be made.
var postgresRefusedEnvironment = []string{
	"PGHOSTADDR", "PGSERVICE", "PGSERVICEFILE", "PGREALM", "PGREQUIRESSL", "PGSSLCRL",
	"PGREQUIREPEER", "PGKRBSRVNAME", "PGGSSLIB", "PGSYSCONFDIR", "PGLOCALEDIR",
}

// refusedPostgresEnvironment is the first of postgresRefusedEnvironment the host's environment has.
func refusedPostgresEnvironment() string {
	for _, variable := range postgresRefusedEnvironment {
		if _, set := os.LookupEnv(variable); set {
			return variable
		}
	}
	return ""
}

// newPostgresConnector is pq.NewConnector, its panic on the environment an error.
func newPostgresConnector(options map[string]string) (connector *pq.Connector, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			connector, err = nil, fmt.Errorf("lib/pq: %v", recovered)
		}
	}()
	return pq.NewConnector(connectionString(options))
}

// describeKind names a value's kind for a message that must not hold the value.
func describeKind(item value.Value) string {
	if item.K == value.Nil {
		return "null"
	}
	if item.K == value.String {
		return "chuỗi rỗng"
	}
	return item.K.String()
}

// ExternalDatabase is the handle connect({ … }) gives.
type ExternalDatabase struct {
	tenant    *Tenant
	connector string
	config    *hostdatabase.Config
	postgres  postgresURL // the URL read, for postgresql
}

// poolKey is the handle's pool in the app's pool: one per configuration.
func (e *ExternalDatabase) poolKey() string {
	s := e.postgres
	return "connect:" + e.connector + ":" + strings.Join([]string{
		s.host, s.port, s.database, s.user, s.password, s.sslmode, s.connectTimeout, s.applicationName,
	}, "\x00")
}

func (e *ExternalDatabase) OnGet(key string) value.Value {
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s không có: database ngoài khai bằng connect(…) chỉ có query(sql, [giá trị])", key))
}

func (e *ExternalDatabase) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (e *ExternalDatabase) OnInvoke(method string, args ...value.Value) value.Value {
	if method != "query" {
		return e.OnGet(method)
	}
	return e.query(args)
}

// query runs one SQL statement with its values bound by the driver ($1, $2 for postgresql) and gives the
// rows it returns, each an object by column name; a statement that returns no rows gives [].
func (e *ExternalDatabase) query(args []value.Value) value.Value {
	if !tenantConnections.Load() {
		return value.InvalidFailure(value.DeniedCode, disabledConnect)
	}
	if len(args) < 1 || len(args) > 2 || args[0].K != value.String || strings.TrimSpace(args[0].String()) == "" {
		return value.InvalidFailure(value.InvalidArgumentCode, `query nhận câu SQL và mảng giá trị: crm.query("SELECT … WHERE id = $1", [id])`)
	}
	var bound []any
	if len(args) == 2 && !args[1].IsNil() {
		if args[1].K != value.Array {
			return value.InvalidFailure(value.InvalidArgumentCode, "giá trị của query là một mảng: crm.query(sql, [id]), mỗi chỗ $1, $2 một giá trị")
		}
		for index, item := range args[1].Array() {
			converted, ok := externalArgument(item)
			switch {
			case !ok && item.K == value.Number:
				return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("giá trị thứ %d của query là NaN hay Infinity: database không giữ được", index+1))
			case !ok:
				return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("giá trị thứ %d của query là %s: chỉ chuỗi, số, true/false hay null", index+1, item.K.String()))
			}
			bound = append(bound, converted)
		}
	}
	pool, failure := e.open()
	if failure.IsError {
		return failure
	}
	ctx, cancel := context.WithTimeout(context.Background(), externalQueryTimeout)
	defer cancel()
	// failed is a query's error as the app reads it; past the deadline it is the deadline, whatever the
	// driver made of the cancel
	failed := func(err error) value.Value {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = context.DeadlineExceeded
		}
		return e.failure(err)
	}
	rows, err := pool.QueryContext(ctx, args[0].String(), bound...)
	if err != nil {
		return failed(err)
	}
	defer rows.Close()
	// stop is how a query leaves before its last row: the context is cancelled before the rows close, so
	// the driver asks the server to stop instead of reading the rest of the result to close them
	stop := func(failure value.Value) value.Value {
		cancel()
		_ = rows.Close()
		return failure
	}
	names, err := rows.Columns()
	if err != nil {
		return stop(failed(err))
	}
	result := []value.Value{}
	size := 0
	for rows.Next() {
		if len(result) == externalRowCeiling {
			return stop(value.InvalidFailure(value.TooLargeCode, fmt.Sprintf("query trả quá %d dòng: thêm LIMIT vào câu, đọc theo trang", externalRowCeiling)))
		}
		cells := make([]any, len(names))
		pointers := make([]any, len(names))
		for index := range cells {
			pointers[index] = &cells[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return stop(failed(err))
		}
		row := make(map[string]value.Value, len(names))
		for index, name := range names {
			size += externalCellSize(cells[index])
			row[name] = externalCell(cells[index])
		}
		if size > externalByteCeiling {
			return stop(value.InvalidFailure(value.TooLargeCode, fmt.Sprintf("query trả quá %d MiB: chọn ít cột hơn, thêm LIMIT vào câu, đọc theo trang", externalByteCeiling>>20)))
		}
		result = append(result, value.New(row))
	}
	if err := rows.Err(); err != nil {
		return failed(err)
	}
	return value.New(result)
}

// externalCellSize is what a cell counts toward externalByteCeiling: its text or bytes, 8 for any other.
func externalCellSize(cell any) int {
	switch typed := cell.(type) {
	case []byte:
		return len(typed)
	case string:
		return len(typed)
	}
	return 8
}

// open is the app's pool of this configuration, made at the first query, its dial guarded.
func (e *ExternalDatabase) open() (*sql.DB, value.Value) {
	manager := e.tenant.databaseManager()
	if manager == nil {
		return nil, value.InvalidFailure(value.UnavailableCode, "connect: app chưa có nơi giữ kết nối")
	}
	if e.config.Type != "postgres" {
		return nil, value.InvalidFailure(value.UnsupportedCode, fmt.Sprintf("connect({ %s: … }).query chưa có trong engine hôm nay: chỉ postgresql", e.connector))
	}
	key := e.poolKey()
	if pool := manager.Lookup(key); pool != nil {
		return pool, value.Value{}
	}
	// a soft bound: two first queries at once may both pass it, never by more than the app's connect() lines
	if manager.Count() >= appPoolCeiling {
		return nil, value.InvalidFailure(value.UnavailableCode, fmt.Sprintf("connect: app đã mở %d pool database, trần của một app", appPoolCeiling))
	}
	// lib/pq panics on some variables of the environment: the host's, so the app hears only that its
	// connection cannot open, and the host's log names the variable
	if variable := refusedPostgresEnvironment(); variable != "" {
		fmt.Printf("[connect] the host's environment sets %s, which lib/pq refuses: connect({ postgresql }) stays closed until it is unset\n", variable)
		return nil, value.InvalidFailure(value.UnavailableCode, fmt.Sprintf("connect({ %s: … }): môi trường của máy chạy app có biến mà bộ nối %s không nhận, nên không mở kết nối", e.connector, e.connector))
	}
	connector, err := newPostgresConnector(e.postgres.options())
	if err != nil {
		// its error may quote a setting, so it is not repeated
		return nil, value.InvalidFailure(value.UnavailableCode, fmt.Sprintf("connect({ %s: … }): bộ nối không nhận cấu hình này", e.connector))
	}
	connector.Dialer(guardedDialer{})
	pool, err := manager.Open(key, func() (*sql.DB, error) {
		pool := sql.OpenDB(connector)
		pool.SetMaxOpenConns(externalMaxOpen)
		pool.SetMaxIdleConns(externalMaxIdle)
		pool.SetConnMaxLifetime(30 * time.Minute)
		return pool, nil
	})
	if err != nil {
		// the app's pool is closed: the app is shutting down
		return nil, value.InvalidFailure(value.UnavailableCode, fmt.Sprintf("connect({ %s: … }): app đang đóng, không mở thêm kết nối", e.connector))
	}
	return pool, value.Value{}
}

// failure is a query's error as the app reads it, never holding where the database is or who logs in
// (KITWORK_DATABASE.md mục 5: "Câu báo không bao giờ chứa thông tin kết nối hay bí mật").
func (e *ExternalDatabase) failure(err error) value.Value {
	var denied *deniedDatabaseAddress
	var server *pq.Error
	switch {
	case errors.As(err, &denied):
		return value.InvalidFailure(value.DeniedCode, fmt.Sprintf("connect({ %s: … }): địa chỉ của database nằm trong mạng riêng hay trên chính máy này, bị chặn", e.connector))
	case errors.Is(err, context.DeadlineExceeded):
		return value.InvalidFailure(value.TimeoutCode, fmt.Sprintf("query chạy quá %s", externalQueryTimeout))
	case errors.As(err, &server):
		switch server.Code.Class() {
		case "08":
			return value.InvalidFailure(value.UnavailableCode, fmt.Sprintf("connect({ %s: … }): mất kết nối tới database", e.connector))
		case "28", "3D":
			return value.InvalidFailure(value.DeniedCode, fmt.Sprintf("connect({ %s: … }): database từ chối đăng nhập hay không có database đó", e.connector))
		case "53":
			return value.InvalidFailure(value.UnavailableCode, fmt.Sprintf("connect({ %s: … }): database hết tài nguyên (kết nối, bộ nhớ hay đĩa), thử lại sau", e.connector))
		case "57":
			if server.Code == "57014" {
				return value.InvalidFailure(value.TimeoutCode, "query bị database dừng giữa chừng (statement_timeout hay bị huỷ)")
			}
			return value.InvalidFailure(value.UnavailableCode, fmt.Sprintf("connect({ %s: … }): database đang dừng hay chưa nhận kết nối", e.connector))
		}
		// any other message of the server: what it quotes of the connection (a role, a user, a database, a
		// client host, as 42501 "permission denied for database" and 55000 "database … is not currently
		// accepting connections" do) is left out
		return value.InvalidFailure(value.DatabaseCode, "query: "+connectionNames.ReplaceAllString(server.Message, `$1 "…"`))
	}
	// a dial that failed (refused, no route, a name that does not resolve) or a broken connection: its
	// text names the host, so only what happened is said
	return value.InvalidFailure(value.UnavailableCode, fmt.Sprintf("connect({ %s: … }): không nối được database", e.connector))
}

// connectionNames are the words a server's message quotes a role, a user, a database or a client host by.
var connectionNames = regexp.MustCompile(`\b(role|user|database|host) "[^"]*"`)

// externalArgument is a value of query bound by the driver: a string, a number (a whole one as an
// integer), true/false, null.
func externalArgument(item value.Value) (any, bool) {
	switch item.K {
	case value.Nil:
		return nil, true
	case value.String:
		return item.String(), true
	case value.Bool:
		return item.Truthy(), true
	case value.Number:
		if item.N == math.Trunc(item.N) && math.Abs(item.N) <= 1<<53-1 {
			return int64(item.N), true
		}
		if math.IsNaN(item.N) || math.IsInf(item.N, 0) {
			return nil, false
		}
		return item.N, true
	case value.Time:
		if moment, ok := item.V.(time.Time); ok {
			return moment, true
		}
	}
	return nil, false
}

// externalCell is a cell a query gives, as the app reads it: text as a string, a whole number past what
// JavaScript holds exactly as its digits, a moment in RFC 3339.
func externalCell(cell any) value.Value {
	switch typed := cell.(type) {
	case nil:
		return value.NewNil()
	case []byte:
		if utf8.Valid(typed) {
			return value.New(string(typed))
		}
		return value.New(fmt.Sprintf("\\x%x", typed))
	case int64:
		if typed > 1<<53-1 || typed < -(1<<53-1) {
			return value.New(fmt.Sprint(typed))
		}
		return value.New(float64(typed))
	case time.Time:
		return value.New(typed.UTC().Format(time.RFC3339Nano))
	}
	return value.New(cell)
}

// deniedDatabaseAddress is the SSRF guard refusing the database's address.
type deniedDatabaseAddress struct{}

func (*deniedDatabaseAddress) Error() string {
	return "connect: database address in private or local space is blocked"
}

// guardedDialer dials a connect() database through the SSRF guard of kitwork/http, after DNS: the address
// checked is the one dialled. A unix socket has no address to check and is refused with it.
type guardedDialer struct{}

func (guardedDialer) dialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return &deniedDatabaseAddress{}
		}
		if ip := net.ParseIP(host); ip == nil || httputil.DeniedIP(ip) {
			return &deniedDatabaseAddress{}
		}
		return nil
	}}
}

func (d guardedDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}

func (d guardedDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return d.DialContext(ctx, network, address)
}

func (d guardedDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, &deniedDatabaseAddress{}
	}
	return d.dialer(10*time.Second).DialContext(ctx, network, address)
}
