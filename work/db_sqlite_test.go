package work

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// The sqlite entry end to end through a real tenant VM: import { sqlite } resolves to the tenant's
// .data/app.db; DDL goes through exec(); data goes through the ORDINARY query builder (create/where/
// find/count — proving the $N placeholders and RETURNING the builder emits run unchanged on modernc
// sqlite); open() names a second file; and the database file must NOT be downloadable over HTTP
// (dot-segment refusal).
func TestTreeSqliteEntry(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-sqlite-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	router := `import { router, sqlite } from "kitwork";` + "\n" +
		`router.get((ctx) => {` + "\n" +
		`  sqlite.exec("CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY, name TEXT, age INTEGER)");` + "\n" +
		`  sqlite.table("users").create({ name: "An", age: 20 });` + "\n" +
		`  sqlite.table("users").create({ name: "Binh", age: 30 });` + "\n" +
		`  sqlite.table("users").create({ name: "Cu", age: 10 });` + "\n" +
		`  const adults = sqlite.table("users").where("age", ">", 18).sort("age", "asc").list();` + "\n" +
		`  const logs = sqlite.open("logs.db");` + "\n" +
		`  logs.exec("CREATE TABLE IF NOT EXISTS events (id INTEGER PRIMARY KEY, kind TEXT)");` + "\n" +
		`  logs.table("events").create({ kind: "boot" });` + "\n" +
		`  return ctx.json({ adults: adults.length, first: adults[0].name, events: logs.table("events").count() });` + "\n" +
		`});`
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	get := func(path string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec.Code, rec.Body.String()
	}

	code, body := get("/")
	if code != 200 {
		t.Fatalf("route status %d, body: %s", code, body)
	}
	if !strings.Contains(body, `"adults":2`) {
		t.Errorf("builder where/find on sqlite failed — body: %s", body)
	}
	if !strings.Contains(body, `"first":"An"`) {
		t.Errorf("sort/scan on sqlite failed — body: %s", body)
	}
	if !strings.Contains(body, `"events":1`) {
		t.Errorf("second file via open() failed — body: %s", body)
	}

	// The files must exist exactly where the blueprint promised: .data/ inside the tenant.
	if _, err := os.Stat(filepath.Join(dir, ".data", "app.db")); err != nil {
		t.Errorf("default database not at .data/app.db: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".data", "logs.db")); err != nil {
		t.Errorf("open(\"logs.db\") not at .data/logs.db: %v", err)
	}

	// And the database must never be downloadable — dot segments are refused by the static server.
	if code, _ := get("/.data/app.db"); code == 200 {
		t.Fatalf("SECURITY: tenant database is downloadable over HTTP (status %d)", code)
	}
}

// sqliteRel must flatten every escape attempt into .data/ — traversal, absolute paths, drive letters.
func TestSqliteRelSafety(t *testing.T) {
	cases := map[string]string{
		"app.db":             "app.db",
		"analytics.db":       "analytics.db",
		"archive/2026.db":    "archive/2026.db",
		"../secrets.db":      "secrets.db",
		"../../etc/passwd":   "passwd",
		"/absolute.db":       "absolute.db",
		"C:/windows/evil.db": "evil.db",
		"a/../../escape.db":  "escape.db",
		"..":                 "app.db", // pure traversal has no name — falls back to the default
		"../..":              "app.db",
		"":                   "app.db",
	}
	for in, want := range cases {
		if got := sqliteRel(in); got != want {
			t.Errorf("sqliteRel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSQLiteReadQueryExecutesOneBoundedSelect(t *testing.T) {
	root := t.TempDir()
	site := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(site, 0o755); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	defer tenant.Close()
	db := sqliteFor(tenant, "read-query.db")
	if result := db.Exec("CREATE TABLE records (id INTEGER PRIMARY KEY, label TEXT, payload BLOB)"); result.K == value.Invalid {
		t.Fatal(result.String())
	}
	for index := 1; index <= sqliteReadQueryMaxRows+5; index++ {
		if result := db.Exec(
			"INSERT INTO records (id, label, payload) VALUES (?, ?, ?)",
			value.New(index), value.New(fmt.Sprintf("row-%03d", index)), value.New([]byte{byte(index)}),
		); result.K == value.Invalid {
			t.Fatalf("seed row %d: %s", index, result.String())
		}
	}

	result := db.ReadQuery(value.New("/* inspector */ SELECT id AS duplicate, label AS duplicate, payload, ';' AS marker FROM records ORDER BY id;"))
	payload, ok := result.Interface().(map[string]any)
	if !ok || payload["ok"] != true {
		t.Fatalf("read query failed: %#v", result.Interface())
	}
	columns, ok := payload["columns"].([]any)
	if !ok || len(columns) != 4 {
		t.Fatalf("columns = %#v", payload["columns"])
	}
	firstColumn := columns[0].(map[string]any)
	secondColumn := columns[1].(map[string]any)
	if firstColumn["name"] != "duplicate" || secondColumn["name"] != "duplicate" {
		t.Fatalf("duplicate aliases/order were not preserved: %#v", columns)
	}
	rows, ok := payload["rows"].([]any)
	if !ok || len(rows) != sqliteReadQueryMaxRows || payload["truncated"] != true {
		t.Fatalf("rows/truncated = %T %d %#v", payload["rows"], len(rows), payload["truncated"])
	}
	firstRow := rows[0].([]any)
	if firstRow[0] != float64(1) || firstRow[1] != "row-001" || firstRow[3] != ";" {
		t.Fatalf("first row = %#v", firstRow)
	}
	blob, ok := firstRow[2].(map[string]any)
	if !ok || blob["type"] != "blob" || blob["base64"] != "AQ==" || blob["bytes"] != float64(1) {
		t.Fatalf("blob cell = %#v", firstRow[2])
	}

	// query_only is connection-local and must be reset before the pooled connection is reused.
	if inserted := db.Exec("INSERT INTO records (id, label) VALUES (?, ?)", value.New(1000), value.New("after-read")); inserted.K == value.Invalid {
		t.Fatalf("query_only was not reset: %s", inserted.String())
	}
}

func TestSQLiteReadQueryRejectsWritesStacksAndOtherSQLiteSurfaces(t *testing.T) {
	root := t.TempDir()
	site := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(site, 0o755); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	defer tenant.Close()
	db := sqliteFor(tenant, "read-query-policy.db")
	db.Exec("CREATE TABLE records (id INTEGER PRIMARY KEY, label TEXT)")
	db.Exec("INSERT INTO records (id, label) VALUES (?, ?)", value.New(1), value.New("kept"))

	for _, statement := range []string{
		"INSERT INTO records (id, label) VALUES (2, 'blocked')",
		"UPDATE records SET label = 'blocked' WHERE id = 1",
		"DELETE FROM records",
		"DROP TABLE records",
		"PRAGMA table_info(records)",
		"ATTACH DATABASE 'other.db' AS other",
		"WITH changed AS (SELECT 1) SELECT * FROM changed",
		"EXPLAIN SELECT * FROM records",
		"SELECT * FROM records; DELETE FROM records",
		"SELECT * FROM records; -- hidden tail\nUPDATE records SET label = 'blocked'",
		"SELECTED FROM records",
		"SELECT 'unterminated",
		"SELECT load_extension('outside')",
		"SELECT readfile('outside')",
		"SELECT `writefile`('outside', 'blocked')",
	} {
		result := db.ReadQuery(value.New(statement)).Interface().(map[string]any)
		if result["ok"] != false || result["code"] != "READ_ONLY_REQUIRED" {
			t.Errorf("statement %q returned %#v", statement, result)
		}
	}

	count := db.ReadQuery(value.New("SELECT count(*) AS count, max(label) AS label FROM records")).Interface().(map[string]any)
	if count["ok"] != true {
		t.Fatalf("verification SELECT failed: %#v", count)
	}
	row := count["rows"].([]any)[0].([]any)
	if row[0] != float64(1) || row[1] != "kept" {
		t.Fatalf("read-only policy allowed mutation: %#v", row)
	}
	if result := db.ReadQuery(value.New(map[string]any{"type": "string"})).Interface().(map[string]any); result["code"] != "INVALID_QUERY" {
		t.Fatalf("non-string SQL payload = %#v", result)
	}
}

func TestSQLiteReadQueryReturnsStableBoundErrors(t *testing.T) {
	root := t.TempDir()
	site := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(site, 0o755); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	defer tenant.Close()
	db := sqliteFor(tenant, "read-query-errors.db")

	tooLong := "SELECT '" + strings.Repeat("x", sqliteReadQueryMaxSQLBytes) + "'"
	if result := db.ReadQuery(value.New(tooLong)).Interface().(map[string]any); result["code"] != "QUERY_TOO_LARGE" {
		t.Fatalf("oversized SQL = %#v", result)
	}
	if result := db.ReadQuery(value.New("SELECT missing FROM nowhere")).Interface().(map[string]any); result["code"] != "SQL_ERROR" {
		t.Fatalf("SQL error = %#v", result)
	}
	columns := make([]string, sqliteReadQueryMaxColumns+1)
	for index := range columns {
		columns[index] = fmt.Sprintf("%d AS c%d", index, index)
	}
	if result := db.ReadQuery(value.New("SELECT " + strings.Join(columns, ", "))).Interface().(map[string]any); result["code"] != "RESULT_TOO_LARGE" {
		t.Fatalf("column bound = %#v", result)
	}
}
