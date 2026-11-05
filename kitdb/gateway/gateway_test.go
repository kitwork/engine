package gateway

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/pgwire"
	"github.com/lib/pq"
)

// fixture is one database made of two SQLite files in WAL mode, each kept open by a writer the way the engine keeps
// them: kitwork.db (schema public) and billing.db (schema billing), plus other.db, a file of no target.
type fixture struct {
	directory string
	public    string
	billing   string
	other     string
	writer    *sql.DB
	billingDB *sql.DB
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	directory := t.TempDir()
	fixture := &fixture{
		directory: directory,
		public:    filepath.Join(directory, "application", "kitwork.db"),
		billing:   filepath.Join(directory, "application", "billing.db"),
		other:     filepath.Join(directory, "other.db"),
	}
	if err := os.MkdirAll(filepath.Dir(fixture.public), 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.writer = createFile(t, fixture.public,
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, active INTEGER NOT NULL, picture BLOB, score REAL)",
		"INSERT INTO users VALUES (1, 'An', 1, x'0102', 1.5), (2, 'Binh', 0, NULL, 2), (3, 'Chi', 1, NULL, NULL)",
		"CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER, amount INTEGER)",
		"INSERT INTO orders VALUES (1, 1, 450), (2, 1, -5), (3, 2, 123456)",
		"CREATE TABLE secret (id INTEGER PRIMARY KEY, code TEXT, note TEXT)",
		"CREATE INDEX secret_code ON secret(code)",
		"INSERT INTO secret VALUES (1, 's1', 'hidden'), (2, 's2', 'hidden')",
		"CREATE VIEW secret_view AS SELECT id, code FROM secret",
		"CREATE VIEW active_users AS SELECT id, name FROM users WHERE active = 1",
	)
	fixture.billingDB = createFile(t, fixture.billing,
		"CREATE TABLE invoices (id INTEGER PRIMARY KEY, user_id INTEGER, total INTEGER)",
		"INSERT INTO invoices VALUES (10, 1, 1999), (11, 2, 500)",
		"CREATE TABLE ledger (id INTEGER PRIMARY KEY, entry TEXT)",
		"INSERT INTO ledger VALUES (1, 'private')",
		"CREATE VIEW recent AS SELECT id, user_id FROM invoices",
	)
	otherDB := createFile(t, fixture.other, "CREATE TABLE leaked (value TEXT)", "INSERT INTO leaked VALUES ('outside the target')")
	_ = otherDB.Close()
	return fixture
}

func createFile(t *testing.T, path string, statements ...string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	for _, statement := range append([]string{"PRAGMA journal_mode=WAL"}, statements...) {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	return database
}

func (fixture *fixture) target() Target {
	return Target{Name: "demo.kitwork.io/application", Schemas: map[string]string{"public": fixture.public, "billing": fixture.billing}}
}

// fakeAccess allows a set of tables and records what the gateway asked it.
type fakeAccess struct {
	mutex    sync.Mutex
	allowed  map[string]bool
	asked    []string
	commands []string
	current  bool
	checks   int
	admin    bool
}

func newAccess(tables ...string) *fakeAccess {
	access := &fakeAccess{allowed: map[string]bool{}, current: true}
	for _, table := range tables {
		access.allowed[table] = true
	}
	return access
}

func (access *fakeAccess) CanSelect(_ context.Context, schema, table string) (bool, error) {
	access.mutex.Lock()
	defer access.mutex.Unlock()
	access.asked = append(access.asked, schema+"."+table)
	return access.allowed[schema+"."+table], nil
}

// CanModify allows what allow named ("insert public.orders").
func (access *fakeAccess) CanModify(_ context.Context, schema, table, privilege string) (bool, error) {
	access.mutex.Lock()
	defer access.mutex.Unlock()
	key := privilege + " " + schema + "." + table
	access.asked = append(access.asked, key)
	return access.allowed[key], nil
}

func (access *fakeAccess) Admin(context.Context) (bool, error) {
	access.mutex.Lock()
	defer access.mutex.Unlock()
	return access.admin, nil
}

func (access *fakeAccess) Command(_ context.Context, statement string) (string, bool, error) {
	access.mutex.Lock()
	defer access.mutex.Unlock()
	access.commands = append(access.commands, statement)
	if strings.HasPrefix(strings.ToUpper(statement), "CREATE USER") {
		return "CREATE ROLE", true, nil
	}
	return "", false, nil
}

func (access *fakeAccess) Current(context.Context) (bool, error) {
	access.mutex.Lock()
	defer access.mutex.Unlock()
	access.checks++
	return access.current, nil
}

func (access *fakeAccess) setCurrent(current bool) {
	access.mutex.Lock()
	defer access.mutex.Unlock()
	access.current = current
}

func (access *fakeAccess) currentChecks() int {
	access.mutex.Lock()
	defer access.mutex.Unlock()
	return access.checks
}

func (access *fakeAccess) askedTables() []string {
	access.mutex.Lock()
	defer access.mutex.Unlock()
	return append([]string(nil), access.asked...)
}

// fakeColumns are the kinds kitwork_columns would hold: orders.amount decimal(12,2), users.active bool,
// invoices.total decimal of unknown precision and scale 2.
type fakeColumns struct{}

func (fakeColumns) Column(schema, table, column string) (string, int, int, bool) {
	switch schema + "." + table + "." + column {
	case "public.orders.amount":
		return "decimal", 12, 2, true
	case "public.users.active":
		return "bool", 0, 0, true
	case "billing.invoices.total":
		return "decimal", 0, 2, true
	}
	return "", 0, 0, false
}

func defaultAccess() *fakeAccess {
	return newAccess("public.users", "public.orders", "billing.invoices")
}

func openSession(t *testing.T, fixture *fixture, access Access, options Options) *Session {
	t.Helper()
	session, err := Open(context.Background(), fixture.target(), access, fakeColumns{}, options)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func textParameters(values ...string) []pgwire.Parameter {
	parameters := make([]pgwire.Parameter, len(values))
	for index, value := range values {
		parameters[index] = pgwire.Parameter{Data: []byte(value)}
	}
	return parameters
}

func codeOf(err error) string {
	var protocol *pgwire.Error
	if errors.As(err, &protocol) {
		return protocol.Code
	}
	return ""
}

// cells is a result as text, rows joined by "|" and cells by ",", NULL written as NULL.
func cells(result pgwire.Result) string {
	rows := make([]string, len(result.Rows))
	for index, row := range result.Rows {
		values := make([]string, len(row))
		for column, field := range row {
			if field.Null {
				values[column] = "NULL"
			} else {
				values[column] = string(field.Data)
			}
		}
		rows[index] = strings.Join(values, ",")
	}
	return strings.Join(rows, "|")
}

func mustRun(t *testing.T, session *Session, query string, parameters ...pgwire.Parameter) pgwire.Result {
	t.Helper()
	result, err := session.Execute(context.Background(), query, parameters)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return result
}

func expectCode(t *testing.T, session *Session, query, code, message string) {
	t.Helper()
	_, err := session.Execute(context.Background(), query, nil)
	if err == nil {
		t.Fatalf("%s: no error, want %s", query, code)
	}
	if codeOf(err) != code || !strings.Contains(err.Error(), message) {
		t.Fatalf("%s: %s %v, want %s containing %q", query, codeOf(err), err, code, message)
	}
}

// The public schema answers to users, public.users and "public"."users"; another schema is written billing.x and
// joins across the files work as written; $1 used twice is one parameter.
//
// CONTROL: with bindParameters returning no arguments, "SELECT name FROM users WHERE id = $1 OR id = $1 + 1: a
// parameter of the statement has no value: missing named argument \"1\"".
func TestSchemasJoinsAndParameters(t *testing.T) {
	fixture := newFixture(t)
	session := openSession(t, fixture, defaultAccess(), Options{})
	for _, query := range []string{
		"SELECT name FROM users ORDER BY id",
		"SELECT name FROM public.users ORDER BY id",
		`SELECT name FROM "public"."users" ORDER BY id`,
	} {
		if got := cells(mustRun(t, session, query)); got != "An|Binh|Chi" {
			t.Fatalf("%s = %q, want An|Binh|Chi", query, got)
		}
	}
	joined := mustRun(t, session, "SELECT u.name, i.total FROM users u JOIN billing.invoices i ON i.user_id = u.id ORDER BY i.id")
	if got := cells(joined); got != "An,19.99|Binh,5.00" {
		t.Fatalf("join = %q, want An,19.99|Binh,5.00", got)
	}
	twice, err := session.Execute(context.Background(), "SELECT name FROM users WHERE id = $1 OR id = $1 + 1 ORDER BY id", textParameters("1"))
	if err != nil {
		t.Fatalf("SELECT name FROM users WHERE id = $1 OR id = $1 + 1: %v", err)
	}
	if got := cells(twice); got != "An|Binh" {
		t.Fatalf("$1 twice = %q, want An|Binh", got)
	}
	typed, err := session.Execute(context.Background(), "SELECT count(*) FROM orders WHERE amount > $1",
		[]pgwire.Parameter{{OID: pgwire.OIDInt8, Data: []byte("4")}})
	if err != nil || cells(typed) != "2" {
		t.Fatalf("an int8 parameter = %q (%v), want 2", cells(typed), err)
	}
}

func fileState(t *testing.T, directory string) string {
	t.Helper()
	var lines []string
	err := filepath.Walk(directory, func(path string, information os.FileInfo, err error) error {
		if err != nil || information.IsDir() {
			return err
		}
		line := filepath.Base(path)
		if !strings.HasSuffix(path, "-shm") { // readers keep their read marks in the shared-memory file
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			line += fmt.Sprintf(" %x", sha256.Sum256(data))
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// Every write, schema change, ATTACH, PRAGMA, VACUUM and second statement is refused with 25006 before SQLite runs
// it, and the files keep every byte; no file appears.
//
// CONTROL: with parseStatement not refusing what follows the first terminator, "SELECT 1; ATTACH 'file:…/other.db?
// mode=ro' AS other:  <nil>, want 25006" (the second statement was dropped without a word). With SQLite also given
// the whole query, "… AS other: XX000 too many attached databases - max 2, want 25006": the EXPLAIN pass ran the
// ATTACH, and only the attach limit of Open stopped it (without that limit too: "XX000 database other is already in
// use", the file of no target attached). Without the attach limit, "ATTACH on the session's own connection: <nil>,
// want too many attached databases".
func TestWritesAreRefused(t *testing.T) {
	fixture := newFixture(t)
	session := openSession(t, fixture, defaultAccess(), Options{})
	before := fileState(t, fixture.directory)
	other := "file:" + filepath.ToSlash(fixture.other) + "?mode=ro"
	created := filepath.Join(fixture.directory, "created.db")
	for _, query := range []string{
		"SELECT 1; ATTACH '" + other + "' AS other",
		"INSERT INTO users (name, active) VALUES ('x', 1)",
		"UPDATE users SET name = 'x'",
		"DELETE FROM users",
		"REPLACE INTO users (id, name, active) VALUES (1, 'x', 1)",
		"INSERT INTO users (name, active) VALUES ('x', 1) RETURNING id",
		"WITH gone AS (SELECT 1) DELETE FROM users",
		"WITH gone AS (SELECT 1) INSERT INTO users (name, active) SELECT 'x', 1",
		"CREATE TABLE extra (a)",
		"CREATE TEMP TABLE extra (a)",
		"CREATE INDEX users_name ON users(name)",
		"CREATE VIEW extra AS SELECT 1",
		"ALTER TABLE users ADD COLUMN extra TEXT",
		"DROP TABLE users",
		"DROP VIEW active_users",
		"ATTACH '" + other + "' AS other",
		"ATTACH 'file:" + filepath.ToSlash(created) + "' AS created",
		"DETACH billing",
		"PRAGMA query_only=0",
		"PRAGMA table_info(users)",
		"PRAGMA billing.journal_mode=DELETE",
		"VACUUM",
		"VACUUM INTO '" + filepath.ToSlash(created) + "'",
		"REINDEX",
		"ANALYZE",
		"BEGIN IMMEDIATE",
		"SET default_transaction_read_only = off",
		"SET SESSION CHARACTERISTICS AS TRANSACTION READ WRITE",
		"SELECT 1; DELETE FROM users",
		"SELECT 1;\n-- comment\n; DROP TABLE users",
		"SELECT ';' AS a; DROP TABLE users",
		"SELECT 1 AS [it's]; ATTACH '" + other + "' AS other",
		"SELECT $a(') ; ATTACH '" + other + "' AS other; --')",
		"EXPLAIN DELETE FROM users",
		"COPY users FROM STDIN",
	} {
		_, err := session.Execute(context.Background(), query, nil)
		if codeOf(err) != "25006" {
			t.Fatalf("%s: %s %v, want 25006", query, codeOf(err), err)
		}
	}
	if after := fileState(t, fixture.directory); after != before {
		t.Fatalf("the files changed:\n%s\nwant\n%s", after, before)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("created.db exists (%v)", err)
	}
	// Beneath the statement checks, SQLite itself attaches nothing more to the session's connection.
	if _, err := session.connection.ExecContext(context.Background(), "ATTACH ? AS other", other); err == nil ||
		!strings.Contains(err.Error(), "too many attached databases") {
		t.Fatalf("ATTACH on the session's own connection: %v, want too many attached databases", err)
	}
	// The connection still reads only what it was given.
	expectCode(t, session, "SELECT * FROM other.leaked", "42P01", "no such table")
	if got := cells(mustRun(t, session, "SELECT count(*) FROM users")); got != "3" {
		t.Fatalf("users after the refusals = %q, want 3", got)
	}
	// Functions that reach outside are not there.
	for _, query := range []string{"SELECT load_extension('x')", "SELECT sqlite_attach('a', 'b', NULL)"} {
		if _, err := session.Execute(context.Background(), query, nil); err == nil {
			t.Fatalf("%s: no error", query)
		}
	}
}

// A table the user may not select is refused with 42501 wherever the statement reads it: a subquery in WHERE, a
// CTE, a view, an index-only scan, EXISTS, a scalar subquery, json_each over it; virtual tables other than
// json_each and json_tree are refused; sqlite_schema, an allowed view and json_each work.
//
// CONTROL: with authorize returning before reading the program, "subquery in WHERE: SELECT name FROM users WHERE id
// IN (SELECT id FROM secret): no error, want 42501".
func TestForbiddenTablesAreRefused(t *testing.T) {
	fixture := newFixture(t)
	access := defaultAccess()
	session := openSession(t, fixture, access, Options{})
	for _, refused := range []struct{ name, query, message string }{
		{"subquery in WHERE", "SELECT name FROM users WHERE id IN (SELECT id FROM secret)", "permission denied for table public.secret"},
		{"common table expression", "WITH hidden AS (SELECT note FROM secret) SELECT * FROM hidden", "permission denied for table public.secret"},
		{"view", "SELECT * FROM secret_view", "permission denied for table public.secret"},
		{"index-only scan", "SELECT code FROM secret WHERE code > 'a'", "permission denied for table public.secret"},
		{"exists", "SELECT name FROM users WHERE EXISTS (SELECT 1 FROM secret WHERE secret.id = users.id)", "permission denied for table public.secret"},
		{"scalar subquery", "SELECT name, (SELECT count(*) FROM secret) FROM users", "permission denied for table public.secret"},
		{"union", "SELECT name FROM users UNION ALL SELECT note FROM secret", "permission denied for table public.secret"},
		{"count", "SELECT count(*) FROM secret", "permission denied for table public.secret"},
		{"other schema", "SELECT * FROM billing.ledger", "permission denied for table billing.ledger"},
		{"json_each over it", "SELECT value FROM json_each((SELECT json_group_array(note) FROM secret))", "permission denied for table public.secret"},
		{"explain of it", "EXPLAIN QUERY PLAN SELECT * FROM secret", "permission denied for table public.secret"},
		{"raw pages", "SELECT * FROM sqlite_dbpage('public')", "virtual tables"},
		{"page statistics", "SELECT * FROM dbstat", "virtual tables"},
		{"pragma function", "SELECT * FROM pragma_table_info('users')", "virtual tables"},
	} {
		_, err := session.Execute(context.Background(), refused.query, nil)
		if err == nil {
			t.Fatalf("%s: %s: no error, want 42501", refused.name, refused.query)
		}
		if codeOf(err) != "42501" || !strings.Contains(err.Error(), refused.message) {
			t.Fatalf("%s: %s: %s %v, want 42501 %q", refused.name, refused.query, codeOf(err), err, refused.message)
		}
	}
	for _, allowed := range []struct{ query, want string }{
		{"SELECT name FROM active_users ORDER BY id", "An|Chi"},
		{"SELECT name FROM public.sqlite_schema WHERE type = 'table' ORDER BY name", "orders|secret|users"},
		{"SELECT count(*) FROM billing.sqlite_schema", "3"},
		{"SELECT sum(value) FROM json_each('[1, 2, 3]')", "6"},
		{"SELECT count(*) FROM json_tree('{\"a\": [1, 2]}')", "4"},
		{"SELECT u.name FROM users u, json_each('[1, 3]') j WHERE u.id = j.value ORDER BY u.id", "An|Chi"},
	} {
		if got := cells(mustRun(t, session, allowed.query)); got != allowed.want {
			t.Fatalf("%s = %q, want %q", allowed.query, got, allowed.want)
		}
	}
	// A view is checked by the tables it reads: active_users asked for users, never for itself.
	for _, asked := range access.askedTables() {
		if asked == "public.active_users" || asked == "public.secret_view" {
			t.Fatalf("CanSelect was asked for the view %s", asked)
		}
	}
}

// Another connection redefines a view between two statements (the engine migrating a schema): the next statement
// is checked against the new definition, not the one this connection had cached. Inside a transaction block the
// snapshot keeps the definition the block began with.
//
// CONTROL: with refreshSchemas keeping root pages once read (no reload when the version moved), "after the view
// changed: SELECT * FROM billing.recent: no error, want 42501 permission denied for table billing.ledger" (EXPLAIN
// listed SQLite's cached program over invoices; the statement then ran a program prepared again over ledger).
func TestViewChangedByAnotherConnection(t *testing.T) {
	fixture := newFixture(t)
	session := openSession(t, fixture, defaultAccess(), Options{})
	if got := cells(mustRun(t, session, "SELECT id FROM billing.recent ORDER BY id")); got != "10|11" {
		t.Fatalf("recent = %q, want 10|11", got)
	}
	redefine := func(source string) {
		for _, statement := range []string{"DROP VIEW recent", "CREATE VIEW recent AS SELECT id, " + source} {
			if _, err := fixture.billingDB.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
	}
	redefine("entry AS user_id FROM ledger")
	_, err := session.Execute(context.Background(), "SELECT * FROM billing.recent", nil)
	if err == nil {
		t.Fatalf("after the view changed: SELECT * FROM billing.recent: no error, want 42501 permission denied for table billing.ledger")
	}
	if codeOf(err) != "42501" || !strings.Contains(err.Error(), "billing.ledger") {
		t.Fatalf("after the view changed: %s %v, want 42501 for billing.ledger", codeOf(err), err)
	}
	redefine("user_id FROM invoices")
	mustRun(t, session, "BEGIN READ ONLY")
	if got := cells(mustRun(t, session, "SELECT id FROM billing.recent ORDER BY id")); got != "10|11" {
		t.Fatalf("recent in the block = %q, want 10|11", got)
	}
	redefine("entry AS user_id FROM ledger")
	if got := cells(mustRun(t, session, "SELECT id FROM billing.recent ORDER BY id")); got != "10|11" {
		t.Fatalf("recent in the block after the change = %q, want the block's snapshot 10|11", got)
	}
	mustRun(t, session, "COMMIT")
	expectCode(t, session, "SELECT * FROM billing.recent", "42501", "billing.ledger")
}

// Values come back typed: INTEGER int8, REAL float8, TEXT text, BLOB bytea in \x hex, NULL; a decimal of
// kitwork_columns is numeric with exactly its scale (450 at scale 2 is 4.50, -5 is -0.05) and numeric(12,2)'s type
// modifier; a bool is t/f; expressions are typed by their values.
//
// CONTROL: with newResultColumn ignoring Columns, "amount = 450 (OID 20), want 4.50 (OID 1700)".
func TestValueTypes(t *testing.T) {
	fixture := newFixture(t)
	session := openSession(t, fixture, defaultAccess(), Options{})
	orders := mustRun(t, session, "SELECT amount FROM orders ORDER BY id")
	if column := orders.Columns[0]; cells(orders) != "4.50|-0.05|1234.56" || column.DataTypeOID != pgwire.OIDNumeric {
		t.Fatalf("amount = %s (OID %d), want 4.50 (OID 1700)", strings.Split(cells(orders), "|")[0], column.DataTypeOID)
	} else if column.TypeModifier != (12<<16|2)+4 {
		t.Fatalf("amount's type modifier = %d, want %d", column.TypeModifier, (12<<16|2)+4)
	}
	totals := mustRun(t, session, "SELECT total FROM billing.invoices ORDER BY id")
	if cells(totals) != "19.99|5.00" || totals.Columns[0].TypeModifier != (18<<16|2)+4 {
		t.Fatalf("total = %q modifier %d, want 19.99|5.00 at numeric(18,2)", cells(totals), totals.Columns[0].TypeModifier)
	}
	users := mustRun(t, session, "SELECT id, name, active, picture, score, NULL AS empty, id * 2 AS doubled, score + 1 AS raised FROM users ORDER BY id")
	if got, want := cells(users), `1,An,t,\x0102,1.5,NULL,2,2.5|2,Binh,f,NULL,2,NULL,4,3|3,Chi,t,NULL,NULL,NULL,6,NULL`; got != want {
		t.Fatalf("users = %q, want %q", got, want)
	}
	wantTypes := []uint32{pgwire.OIDInt8, pgwire.OIDText, pgwire.OIDBool, pgwire.OIDBytea, pgwire.OIDFloat8, pgwire.OIDText, pgwire.OIDInt8, pgwire.OIDFloat8}
	for index, column := range users.Columns {
		if column.DataTypeOID != wantTypes[index] {
			t.Fatalf("column %s has OID %d, want %d", column.Name, column.DataTypeOID, wantTypes[index])
		}
	}
	if users.CommandTag != "SELECT 3" {
		t.Fatalf("tag = %q, want SELECT 3", users.CommandTag)
	}
	// Values of different classes in one column come back as text rather than a broken int8.
	mixed := mustRun(t, session, "SELECT 1 AS a UNION ALL SELECT 'two'")
	if mixed.Columns[0].DataTypeOID != pgwire.OIDText || cells(mixed) != "1|two" {
		t.Fatalf("mixed = %q OID %d, want 1|two as text", cells(mixed), mixed.Columns[0].DataTypeOID)
	}
	for _, check := range []struct {
		value any
		scale int
		want  string
	}{{int64(450), 2, "4.50"}, {int64(-5), 2, "-0.05"}, {int64(0), 2, "0.00"}, {int64(7), 0, "7"},
		{int64(-9223372036854775808), 2, "-92233720368547758.08"}, {"4.5", 2, "4.50"}, {"-.5", 3, "-0.500"}} {
		if got, _ := decimalText(check.value, check.scale); got != check.want {
			t.Fatalf("decimalText(%v, %d) = %q, want %q", check.value, check.scale, got, check.want)
		}
	}
}

// More than MaxRows rows or MaxBytes bytes is 54000, and the statement stops; a single value larger than MaxBytes
// is refused by SQLite itself before it is copied out.
//
// CONTROL: with the MaxRows check removed from run, "more rows than allowed: no error, want 54000"; with the byte
// count removed, "SELECT 'abcdefgh' FROM eighty rows: no error, want 54000"; without the length limit of attach,
// "SELECT zeroblob(1000): 54000 result exceeds 64 bytes, want 54000 containing \"a value exceeds\"".
func TestLimits(t *testing.T) {
	fixture := newFixture(t)
	rows := openSession(t, fixture, defaultAccess(), Options{MaxRows: 2})
	if got := cells(mustRun(t, rows, "SELECT id FROM users WHERE id < 3 ORDER BY id")); got != "1|2" {
		t.Fatalf("two rows = %q, want 1|2", got)
	}
	_, err := rows.Execute(context.Background(), "SELECT id FROM users", nil)
	if err == nil {
		t.Fatalf("more rows than allowed: no error, want 54000")
	}
	if codeOf(err) != "54000" || !strings.Contains(err.Error(), "result exceeds 2 rows") || !strings.Contains(err.Error(), "LIMIT 2 OFFSET 2") {
		t.Fatalf("more rows than allowed: %s %v, want 54000 result exceeds 2 rows, saying LIMIT 2 OFFSET 2", codeOf(err), err)
	}
	if got := cells(mustRun(t, rows, "SELECT count(*) FROM users")); got != "3" {
		t.Fatalf("the session after a refusal = %q, want 3", got)
	}
	bytes := openSession(t, fixture, defaultAccess(), Options{MaxBytes: 64})
	eighty := "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c WHERE x < 80) SELECT 'abcdefgh' FROM c"
	if _, err := bytes.Execute(context.Background(), eighty, nil); err == nil {
		t.Fatalf("SELECT 'abcdefgh' FROM eighty rows: no error, want 54000")
	} else if codeOf(err) != "54000" || !strings.Contains(err.Error(), "result exceeds 64 bytes (64 bytes)") || !strings.Contains(err.Error(), "add LIMIT") {
		t.Fatalf("SELECT 'abcdefgh' FROM eighty rows: %s %v, want 54000 result exceeds 64 bytes", codeOf(err), err)
	}
	expectCode(t, bytes, "SELECT zeroblob(1000)", "54000", "a value exceeds")
	expectCode(t, bytes, "SELECT length(zeroblob(1000))", "54000", "a value exceeds")
	if got := cells(mustRun(t, bytes, "SELECT name FROM users WHERE id = 1")); got != "An" {
		t.Fatalf("the session after a refusal = %q, want An", got)
	}
}

// BEGIN READ ONLY … COMMIT reads one snapshot: a row another connection commits between two reads of the block is
// not seen until the block ends. ReadyForQuery reports the block.
//
// CONTROL: with begin not running BEGIN on SQLite, "second count in the block = 4, want 3 (one snapshot)".
func TestTransactionReadsOneSnapshot(t *testing.T) {
	fixture := newFixture(t)
	session := openSession(t, fixture, defaultAccess(), Options{})
	if result := mustRun(t, session, "BEGIN READ ONLY"); result.CommandTag != "BEGIN" || session.TransactionStatus() != pgwire.TransactionActive {
		t.Fatalf("BEGIN READ ONLY: tag %q status %q", result.CommandTag, session.TransactionStatus())
	}
	if got := cells(mustRun(t, session, "SELECT count(*) FROM users")); got != "3" {
		t.Fatalf("first count = %q, want 3", got)
	}
	if _, err := fixture.writer.Exec("INSERT INTO users (name, active) VALUES ('Dung', 1)"); err != nil {
		t.Fatal(err)
	}
	if got := cells(mustRun(t, session, "SELECT count(*) FROM users")); got != "3" {
		t.Fatalf("second count in the block = %s, want 3 (one snapshot)", got)
	}
	if result := mustRun(t, session, "COMMIT"); result.CommandTag != "COMMIT" || session.TransactionStatus() != pgwire.TransactionIdle {
		t.Fatalf("COMMIT: tag %q status %q", result.CommandTag, session.TransactionStatus())
	}
	if got := cells(mustRun(t, session, "SELECT count(*) FROM users")); got != "4" {
		t.Fatalf("count after the block = %q, want 4", got)
	}
	// An error fails the block, as in PostgreSQL: statements are refused until it ends, and COMMIT rolls back.
	mustRun(t, session, "START TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY")
	expectCode(t, session, "SELECT * FROM secret", "42501", "public.secret")
	if session.TransactionStatus() != pgwire.TransactionFailed {
		t.Fatalf("status after an error in the block = %q, want E", session.TransactionStatus())
	}
	expectCode(t, session, "SELECT 1", "25P02", "current transaction is aborted")
	if result := mustRun(t, session, "COMMIT"); result.CommandTag != "ROLLBACK" || session.TransactionStatus() != pgwire.TransactionIdle {
		t.Fatalf("COMMIT of a failed block: tag %q status %q, want ROLLBACK and idle", result.CommandTag, session.TransactionStatus())
	}
	expectCode(t, session, "SAVEPOINT here", "25006", "SAVEPOINT")
	mustRun(t, session, "BEGIN")
	expectCode(t, session, "ROLLBACK TO SAVEPOINT here", "0A000", "savepoints")
	mustRun(t, session, "ROLLBACK")
}

// Access.Current is asked before every statement; once it says no, the session ends: 57P01 now and for every later
// statement, and nothing more reaches SQLite or Access.CanSelect.
//
// CONTROL: with enter not asking Access.Current, "after the account changed: no error, want 57P01".
func TestAccountChangeEndsTheSession(t *testing.T) {
	fixture := newFixture(t)
	access := defaultAccess()
	session := openSession(t, fixture, access, Options{})
	mustRun(t, session, "SELECT count(*) FROM users")
	if checks := access.currentChecks(); checks != 1 {
		t.Fatalf("Current was asked %d times for one statement, want 1", checks)
	}
	access.setCurrent(false)
	asked := len(access.askedTables())
	_, err := session.Execute(context.Background(), "SELECT count(*) FROM users", nil)
	if err == nil {
		t.Fatalf("after the account changed: no error, want 57P01")
	}
	if codeOf(err) != "57P01" || !strings.Contains(err.Error(), "sign in again") {
		t.Fatalf("after the account changed: %s %v, want 57P01", codeOf(err), err)
	}
	access.setCurrent(true)
	if _, err := session.Execute(context.Background(), "SELECT 1", nil); codeOf(err) != "57P01" {
		t.Fatalf("a later statement: %s %v, want 57P01", codeOf(err), err)
	}
	if _, err := session.Describe(context.Background(), "SELECT 1", nil); codeOf(err) != "57P01" {
		t.Fatalf("a later describe: %s %v, want 57P01", codeOf(err), err)
	}
	if len(access.askedTables()) != asked {
		t.Fatalf("CanSelect was asked after the session ended")
	}
	if session.connection != nil {
		t.Fatalf("the SQLite connection is still open after the session ended")
	}
}

// A database whose file is missing is an error naming the database, and no file is created; more schemas than
// SQLite attaches, and schema names that are not plain identifiers, are refused.
//
// CONTROL: with Open not checking the files before attaching, "missing file: 3D000 database
// \"demo.kitwork.io/application\": schema \"billing\" cannot be opened read-only: unable to open database file:
// unable to open database: its file, want 3D000 … does not exist".
func TestOpenErrors(t *testing.T) {
	fixture := newFixture(t)
	missing := filepath.Join(fixture.directory, "application", "missing.db")
	target := Target{Name: "demo.kitwork.io/application", Schemas: map[string]string{"public": fixture.public, "billing": missing}}
	_, err := Open(context.Background(), target, defaultAccess(), nil, Options{})
	if codeOf(err) != "3D000" || !strings.Contains(err.Error(), `database "demo.kitwork.io/application" does not exist`) {
		t.Fatalf("missing file: %s %v, want 3D000 … does not exist", codeOf(err), err)
	}
	if strings.Contains(err.Error(), fixture.directory) {
		t.Fatalf("the error shows the path: %v", err)
	}
	if _, statErr := os.Stat(missing); !os.IsNotExist(statErr) {
		t.Fatalf("missing.db was created (%v)", statErr)
	}
	many := Target{Name: "many", Schemas: map[string]string{}}
	for index := range MaxSchemas + 1 {
		many.Schemas[fmt.Sprintf("schema%d", index)] = fixture.public
	}
	if _, err := Open(context.Background(), many, defaultAccess(), nil, Options{}); codeOf(err) != "54000" || !strings.Contains(err.Error(), "at most 10") {
		t.Fatalf("eleven schemas: %s %v, want 54000 … at most 10", codeOf(err), err)
	}
	for _, name := range []string{"bad name", `x"; DETACH public; --`, "main", "TEMP", ""} {
		bad := Target{Name: "bad", Schemas: map[string]string{"public": fixture.public, name: fixture.billing}}
		if _, err := Open(context.Background(), bad, defaultAccess(), nil, Options{}); codeOf(err) != "3D000" {
			t.Fatalf("schema %q: %s %v, want 3D000", name, codeOf(err), err)
		}
	}
	if _, err := Open(context.Background(), fixture.target(), nil, nil, Options{}); err == nil {
		t.Fatalf("Open without Access: no error")
	}
}

// SET is accepted and ignored, SHOW answers the usual names, SELECT version() answers, an empty statement is empty.
//
// CONTROL: with isVersionCall always false, "SELECT version(): 42883 no such function: version".
func TestSessionStatements(t *testing.T) {
	fixture := newFixture(t)
	session := openSession(t, fixture, defaultAccess(), Options{})
	for _, query := range []string{
		"SET application_name = 'psql'", "SET search_path TO public", "SET client_encoding TO 'UTF8'",
		"SET extra_float_digits = 3", "SET DateStyle = ISO", "SET TIME ZONE 'UTC'", "SET SESSION statement_timeout = 0",
		"SET TRANSACTION ISOLATION LEVEL READ COMMITTED", "RESET ALL",
	} {
		if result := mustRun(t, session, query); result.CommandTag != strings.Fields(query)[0] {
			t.Fatalf("%s: tag %q", query, result.CommandTag)
		}
	}
	for query, want := range map[string]string{
		"SHOW server_version":              ServerVersion,
		"SHOW server_encoding":             "UTF8",
		"show client_encoding;":            "UTF8",
		"SHOW standard_conforming_strings": "on",
		"SHOW TimeZone":                    "UTC",
		"SHOW TIME ZONE":                   "UTC",
		"SHOW DateStyle":                   "ISO, MDY",
		"SHOW transaction_isolation":       "repeatable read",
		"SHOW TRANSACTION ISOLATION LEVEL": "repeatable read",
		"SHOW transaction_read_only":       "on",
	} {
		if got := cells(mustRun(t, session, query)); got != want {
			t.Fatalf("%s = %q, want %q", query, got, want)
		}
	}
	expectCode(t, session, "SHOW shared_buffers", "42704", "unrecognized configuration parameter")
	expectCode(t, session, "SET ROLE admin", "0A000", "SET ROLE")
	result, err := session.Execute(context.Background(), "SELECT version()", nil)
	if err != nil {
		t.Fatalf("SELECT version(): %s %v", codeOf(err), err)
	}
	if got := cells(result); !strings.HasPrefix(got, "PostgreSQL 16.0 (Kitwork, SQLite 3.") {
		t.Fatalf("SELECT version() = %q", got)
	}
	if got := cells(mustRun(t, session, "select pg_catalog.version();")); !strings.HasPrefix(got, "PostgreSQL 16.0") {
		t.Fatalf("select pg_catalog.version() = %q", got)
	}
	if result := mustRun(t, session, " ; -- nothing\n"); len(result.Columns) != 0 || len(result.Rows) != 0 {
		t.Fatalf("an empty statement returned %+v", result)
	}
	// PostgreSQL's casts read as SQLite's CAST (translate.go): on a parameter, a column, in a chain.
	if got := cells(mustRun(t, session, "SELECT $1::int + 1", textParameters("41")...)); got != "42" {
		t.Fatalf("SELECT $1::int + 1 = %q", got)
	}
	if got := cells(mustRun(t, session, "SELECT name::text, id::text::int FROM users WHERE id = 1")); got != "An,1" {
		t.Fatalf("casts of columns = %q", got)
	}
	expectCode(t, session, "SELECT 1 FROM nowhere", "42P01", "no such table: nowhere")
	expectCode(t, session, "SELECT nowhere FROM users", "42703", "no such column: nowhere")
	expectCode(t, session, "SELECT $1", "42P02", "has no value")
	expectCode(t, session, "SELEC 1", "25006", "SELEC")
}

// An account statement goes to Access.Command first; one it does not handle is a write and refused.
//
// CONTROL: with execute not calling Access.Command, "CREATE USER bob WITH PASSWORD 'x': 25006 cannot execute CREATE
// in a read-only transaction, want tag CREATE ROLE".
func TestAccountStatementsGoToAccess(t *testing.T) {
	fixture := newFixture(t)
	access := defaultAccess()
	session := openSession(t, fixture, access, Options{})
	result, err := session.Execute(context.Background(), "CREATE USER bob WITH PASSWORD 'x';", nil)
	if err != nil || result.CommandTag != "CREATE ROLE" {
		t.Fatalf("CREATE USER bob WITH PASSWORD 'x': %s %v, want tag CREATE ROLE", codeOf(err), err)
	}
	expectCode(t, session, "CREATE TABLE extra (a)", "25006", "cannot execute CREATE in a read-only transaction")
	expectCode(t, session, "GRANT SELECT ON TABLE secret TO bob; DROP TABLE users", "25006", "more than one statement")
	want := []string{"CREATE USER bob WITH PASSWORD 'x'", "CREATE TABLE extra (a)"}
	if strings.Join(access.commands, "|") != strings.Join(want, "|") {
		t.Fatalf("Command was given %q, want %q", access.commands, want)
	}
}

// The scanner ends a statement where SQLite does: a semicolon inside a string, an identifier ("…", `…`, […]), a
// comment, a blob or a $name(…) parameter does not end it; one outside does, and what follows is refused.
//
// CONTROL: with '[' scanned as punctuation, "SELECT 1 AS [;]: cannot execute more than one statement at a time in a
// read-only transaction, want one statement".
func TestStatementBoundaries(t *testing.T) {
	for _, single := range []string{
		"SELECT ';'", `SELECT 1 AS ";"`, "SELECT 1 AS `;`", "SELECT 1 AS [;]", "SELECT 1 -- ;\n", "SELECT 1 /* ; */",
		"SELECT 'it''s; fine'", "SELECT x'3b'", "SELECT 1;", "SELECT 1 ; ;\n-- end", "SELECT 1 /* unterminated ;",
		"SELECT 1 AS [it's]", `SELECT 1 AS "a""; b"`,
	} {
		parsed, err := parseStatement(single)
		if err != nil {
			t.Fatalf("%s: %v, want one statement", single, err)
		}
		if parsed.kind != statementRead {
			t.Fatalf("%s: kind %d, want a read", single, parsed.kind)
		}
	}
	for _, double := range []string{
		"SELECT 1 AS [it's]; ATTACH 'x' AS y", "SELECT 1 AS [it's]; ATTACH 'x' AS y; SELECT 1 AS [']",
		"SELECT 1 AS `a'b`; DELETE FROM users", `SELECT 1 AS "a'b"; DELETE FROM users`,
		"SELECT 1 -- c\n; DELETE FROM users", "SELECT 1 /* ' */; DELETE FROM users", "SELECT $a(') ; DELETE FROM users; --')",
		"SELECT x'00'; DELETE FROM users",
	} {
		if parsed, err := parseStatement(double); codeOf(err) != "25006" {
			t.Fatalf("%s: one statement (kind %d, %v), want a refusal", double, parsed.kind, err)
		}
	}
	if parsed, err := parseStatement("SELECT 1 ; -- trailing"); err != nil || parsed.text != "SELECT 1 " {
		t.Fatalf("the text SQLite runs = %q (%v), want the statement before its terminator", parsed.text, err)
	}
	if _, err := parseStatement("SELECT 'a\x00'"); codeOf(err) != "42601" {
		t.Fatalf("a zero byte: %v, want 42601", err)
	}
}

// Describe gives the columns of a statement without running it, typed as Execute types them, and refuses a
// forbidden table as Execute does.
//
// CONTROL: with Describe running the statement, "Describe: XX000 integer overflow".
func TestDescribe(t *testing.T) {
	fixture := newFixture(t)
	session := openSession(t, fixture, defaultAccess(), Options{})
	// The constant term is evaluated before any row is read, so running the statement fails whatever $1 is.
	query := "SELECT 'boom' AS boom, amount, users.id, name FROM orders JOIN users ON users.id = orders.user_id " +
		"WHERE abs(-9223372036854775807 - 1) > 0 AND orders.id = $1"
	nulls := []pgwire.Parameter{{Null: true}}
	columns, err := session.Describe(context.Background(), query, nulls)
	if err != nil {
		t.Fatalf("Describe: %s %v", codeOf(err), err)
	}
	want := []pgwire.Column{
		{Name: "boom", DataTypeOID: pgwire.OIDText, DataTypeSize: -1},
		{Name: "amount", DataTypeOID: pgwire.OIDNumeric, DataTypeSize: -1, TypeModifier: (12<<16 | 2) + 4},
		{Name: "id", DataTypeOID: pgwire.OIDInt8, DataTypeSize: 8},
		{Name: "name", DataTypeOID: pgwire.OIDText, DataTypeSize: -1},
	}
	if fmt.Sprint(columns) != fmt.Sprint(want) {
		t.Fatalf("Describe = %v, want %v", columns, want)
	}
	if _, err := session.Execute(context.Background(), query, textParameters("1")); err == nil || !strings.Contains(err.Error(), "integer overflow") {
		t.Fatalf("Execute: %v, want integer overflow (the statement does fail when it runs)", err)
	}
	if _, err := session.Describe(context.Background(), "SELECT note FROM secret WHERE id = $1", nulls); codeOf(err) != "42501" {
		t.Fatalf("Describe of a forbidden table: %s %v, want 42501", codeOf(err), err)
	}
	if _, err := session.Describe(context.Background(), "PRAGMA table_info(users)", nil); codeOf(err) != "25006" {
		t.Fatalf("Describe of a PRAGMA: %s %v, want 25006", codeOf(err), err)
	}
	columns, err = session.Describe(context.Background(), "SHOW server_version", nil)
	if err != nil || len(columns) != 1 || columns[0].Name != "server_version" {
		t.Fatalf("Describe of SHOW = %v (%v)", columns, err)
	}
	columns, err = session.Describe(context.Background(), "EXPLAIN QUERY PLAN SELECT name FROM users", nil)
	if err != nil || len(columns) != 4 || columns[3].Name != "detail" {
		t.Fatalf("Describe of EXPLAIN QUERY PLAN = %v (%v), want its four columns", columns, err)
	}
}

// ctx cancels a running statement: 57014, and the session goes on.
//
// CONTROL: with run querying under context.Background(), "the statement still runs 10s after its context ended".
func TestCancel(t *testing.T) {
	fixture := newFixture(t)
	// Closed by hand: a statement that ignored its context would hold the session, and a Close in Cleanup would wait.
	session, err := Open(context.Background(), fixture.target(), defaultAccess(), fakeColumns{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := session.Execute(ctx, "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c) SELECT count(*) FROM c", nil)
		done <- err
	}()
	select {
	case err := <-done:
		if codeOf(err) != "57014" {
			t.Fatalf("canceled statement: %s %v, want 57014", codeOf(err), err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the statement still runs 10s after its context ended")
	}
	if got := cells(mustRun(t, session, "SELECT count(*) FROM users")); got != "3" {
		t.Fatalf("after the cancel = %q, want 3", got)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

// testAuthenticator opens a gateway Session for the cleartext password "secret".
type testAuthenticator struct {
	fixture *fixture
	access  *fakeAccess
}

func (authenticator testAuthenticator) Authenticate(ctx context.Context, startup pgwire.Startup, password string) (pgwire.Session, error) {
	if password != "secret" {
		return nil, pgwire.NewError("28P01", "password authentication failed for user "+startup.User())
	}
	target := authenticator.fixture.target()
	if startup.Database() != target.Name {
		return nil, pgwire.NewError("3D000", fmt.Sprintf("database %q does not exist", startup.Database()))
	}
	return Open(ctx, target, authenticator.access, fakeColumns{}, Options{})
}

// Through pgwire.Server and lib/pq: parameters by the extended protocol, decimals, a forbidden table, a write and a
// read-only sql.Tx.
//
// CONTROL: with authorize returning before reading the program, "a forbidden table over the wire: no error, want
// 42501".
func TestEndToEndWithLibPQ(t *testing.T) {
	fixture := newFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	server := pgwire.Server{Authenticator: testAuthenticator{fixture: fixture, access: defaultAccess()}}
	go func() { served <- server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	port := listener.Addr().(*net.TCPAddr).Port
	client, err := sql.Open("postgres", fmt.Sprintf(
		"host=127.0.0.1 port=%d user=reader password=secret dbname=demo.kitwork.io/application sslmode=disable", port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	client.SetMaxOpenConns(1)

	var name, total string
	joined := "SELECT u.name, i.total FROM users u JOIN billing.invoices i ON i.user_id = u.id WHERE u.id = $1 AND i.id > $2"
	if err := client.QueryRow(joined, 1, 5).Scan(&name, &total); err != nil {
		t.Fatalf("join with parameters: %v", err)
	}
	if name != "An" || total != "19.99" {
		t.Fatalf("join = %s %s, want An 19.99", name, total)
	}
	var amount string
	var active bool
	var id int64
	typed := "SELECT o.amount, u.active, u.id FROM orders o JOIN users u ON u.id = o.user_id WHERE o.id = $1"
	if err := client.QueryRow(typed, 2).Scan(&amount, &active, &id); err != nil {
		t.Fatalf("typed columns: %v", err)
	}
	if amount != "-0.05" || !active || id != 1 {
		t.Fatalf("typed columns = %s %v %d, want -0.05 true 1", amount, active, id)
	}
	var version string
	if err := client.QueryRow("SELECT version()").Scan(&version); err != nil || !strings.HasPrefix(version, "PostgreSQL 16.0") {
		t.Fatalf("version() = %q (%v)", version, err)
	}

	_, err = client.Exec("SELECT note FROM secret WHERE id = $1", 1)
	var wire *pq.Error
	if err == nil {
		t.Fatalf("a forbidden table over the wire: no error, want 42501")
	}
	if !errors.As(err, &wire) || wire.Code != "42501" {
		t.Fatalf("a forbidden table over the wire: %v, want 42501", err)
	}
	if _, err := client.Exec("DELETE FROM users"); !errors.As(err, &wire) || wire.Code != "25006" {
		t.Fatalf("a write over the wire: %v, want 25006", err)
	}

	transaction, err := client.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx read-only: %v", err)
	}
	var first, second int
	if err := transaction.QueryRow("SELECT count(*) FROM users").Scan(&first); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.writer.Exec("INSERT INTO users (name, active) VALUES ('Dung', 1)"); err != nil {
		t.Fatal(err)
	}
	if err := transaction.QueryRow("SELECT count(*) FROM users").Scan(&second); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if first != 3 || second != 3 {
		t.Fatalf("counts in the read-only transaction = %d %d, want 3 3", first, second)
	}
	// lib/pq writes BEGIN READ WRITE for a transaction that is not read-only (database tools do the same): it begins a
	// read transaction, and a write inside it is refused.
	plain, err := client.BeginTx(context.Background(), &sql.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx read-write: %v, want a read transaction", err)
	}
	if _, err := plain.Exec("INSERT INTO users (name, active) VALUES ('Em', 1)"); !errors.As(err, &wire) || wire.Code != "25006" {
		t.Fatalf("INSERT inside BEGIN READ WRITE: %v, want 25006", err)
	}
	_ = plain.Rollback()
}
