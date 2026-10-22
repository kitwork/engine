package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// A site's SQLite file is the engine's (the SQL backend of SchemaTable): the ORDINARY query builder runs
// on it unchanged (create/where/sort/count — the $N placeholders and RETURNING it emits run on modernc
// sqlite), each name is its own file under the site's .data/, and the file is never downloadable over HTTP
// (dot-segment refusal). App code reached the same files through import { sqlite } until 22/10/2026
// (TestImportSqliteIsAnError).
func TestSiteSQLiteFileUnderData(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(`import { router } from "kitwork";
router.get((ctx) => ctx.text("ok"));`), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(tmp, "localhost")
	t.Cleanup(tenant.Close)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}

	users := sqliteFor(tenant, "app.db")
	if _, err := users.db().Exec("CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY, name TEXT, age INTEGER)"); err != nil {
		t.Fatal(err)
	}
	for _, row := range []map[string]value.Value{
		{"name": value.New("An"), "age": value.New(20)},
		{"name": value.New("Binh"), "age": value.New(30)},
		{"name": value.New("Cu"), "age": value.New(10)},
	} {
		if created := users.Table("users").Create(value.New(row)); created.IsError {
			t.Fatalf("create %v: %v", row, created)
		}
	}
	adults := users.Table("users").Where(value.New("age"), value.New(">"), value.New(18)).Sort("age", "asc").List().Array()
	if len(adults) != 2 || adults[0].Get("name").String() != "An" {
		t.Fatalf("where/sort on sqlite: %v, want An then Binh", adults)
	}
	logs := sqliteFor(tenant, "logs.db")
	if _, err := logs.db().Exec("CREATE TABLE IF NOT EXISTS events (id INTEGER PRIMARY KEY, kind TEXT)"); err != nil {
		t.Fatal(err)
	}
	logs.Table("events").Create(value.New(map[string]value.Value{"kind": value.New("boot")}))
	if count := logs.Table("events").Count(); count.N != 1 {
		t.Fatalf("a second file: count %v, want 1", count)
	}

	// The files exist exactly where the blueprint promised: .data/ inside the site.
	for _, name := range []string{"app.db", "logs.db"} {
		if _, err := os.Stat(filepath.Join(dir, ".data", name)); err != nil {
			t.Errorf("%s not at .data/%s: %v", name, name, err)
		}
	}
	// And a database is never downloadable: dot segments are refused by the static server.
	request := httptest.NewRequest(http.MethodGet, "http://localhost/.data/app.db", nil)
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, request)
	if recorder.Code == http.StatusOK {
		t.Fatalf("SECURITY: a site's database is downloadable over HTTP (status %d)", recorder.Code)
	}
}

// import { sqlite } from "kitwork" was removed on 22/10/2026: the site does not boot, and the error says
// what the spec writes (connection("tên") with { sqlite: path } in the host's app.databases). Read by a way
// the compiler does not see (the module kitwork by a namespace), sqlite is the same error where it is read.
func TestImportSqliteIsAnError(t *testing.T) {
	_, _, err := connectionTenant(t, map[string]string{
		"router.kitwork.js": "import { router, sqlite } from \"kitwork\";\nrouter.get((ctx) => ctx.json(sqlite.table(\"users\").list()));",
	})
	if err == nil || !strings.Contains(err.Error(), `import { sqlite } from "kitwork" đã bỏ: SQLite là connection("tên") trong app/.shared/schema/, với tên đó khai { sqlite: đường_dẫn } trong app.databases({ … }) của application.work`) {
		t.Fatalf("import { sqlite } must stop the boot naming connection(\"tên\"): %v", err)
	}

	tenant, _ := mustBoot(t, map[string]string{
		"router.kitwork.js": `import * as kit from "kitwork";
import { router, safe } from "kitwork";
router.get((ctx) => {
  const { error } = safe(() => kit.sqlite.table("users"));
  return ctx.json({ code: error.code, message: error.message });
});`,
	})
	code, body, raw := hitJSON(t, tenant, "/")
	if message, _ := body["message"].(string); code != 200 || body["code"] != value.InvalidArgumentCode ||
		!strings.HasPrefix(message, `sqlite của "kitwork" đã bỏ: SQLite là connection("tên")`) {
		t.Fatalf("kitwork().sqlite read at run time: code=%d body=%s", code, raw)
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
