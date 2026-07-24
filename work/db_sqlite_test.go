package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
