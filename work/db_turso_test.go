//go:build turso

package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The turso entry end to end through a REAL tenant VM — the twin of TestTreeSqliteEntry, but the
// data lives on the Turso Database engine (tursogo) instead of modernc. It proves the whole stack
// runs unchanged on Turso: `import { turso }` resolves to the tenant's .data/app.db; DDL goes through
// exec(); data goes through the ORDINARY query builder (create/where/sort/list — so the $N
// placeholders and RETURNING the builder emits run on tursogo); and the database file must NOT be
// downloadable over HTTP.
//
// Built ONLY with `-tags turso` (the tag also compiles database/turso_driver.go, which registers the
// "turso" driver). A default build never sees this file.
func TestTursoTenantEntry(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-turso-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	router := `import { router, turso } from "kitwork";` + "\n" +
		`router.get((ctx) => {` + "\n" +
		`  turso.exec("CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY, name TEXT, age INTEGER)");` + "\n" +
		`  turso.table("users").create({ name: "An", age: 20 });` + "\n" +
		`  turso.table("users").create({ name: "Binh", age: 30 });` + "\n" +
		`  turso.table("users").create({ name: "Cu", age: 10 });` + "\n" +
		`  const adults = turso.table("users").where("age", ">", 18).sort("age", "asc").list();` + "\n" +
		`  return ctx.json({ adults: adults.length, first: adults[0].name });` + "\n" +
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
		t.Errorf("builder where/list on TURSO failed — body: %s", body)
	}
	if !strings.Contains(body, `"first":"An"`) {
		t.Errorf("sort/scan on TURSO failed — body: %s", body)
	}

	// tursogo must have written the SQLite-format file exactly where the blueprint promised.
	if _, err := os.Stat(filepath.Join(dir, ".data", "app.db")); err != nil {
		t.Errorf("turso database not at .data/app.db: %v", err)
	}

	// And the database must never be downloadable — dot segments are refused by the static server.
	if code, _ := get("/.data/app.db"); code == 200 {
		t.Fatalf("SECURITY: tenant turso database is downloadable over HTTP (status %d)", code)
	}
}

// Regression: turso.open()/turso.memory() must STAY on the turso engine. The trap is that TursoDB
// embeds *SQLite, whose Open/Memory hardcode Type "sqlite"; without the overrides in db.turso.go,
// turso.open("x.db") silently reverts to modernc. This pins each entry point's engine, with the
// plain sqlite capability as the control (its open must stay "sqlite").
func TestTursoOpenAndMemoryStayOnTurso(t *testing.T) {
	tn := NewTenant(t.TempDir(), "localhost")

	base := &TursoDB{tursoForRequest(tn, "app.db", nil)}
	if got := base.preset.Type; got != "turso" {
		t.Errorf("turso default: preset.Type = %q, want turso", got)
	}
	if got := base.Open("analytics.db").preset.Type; got != "turso" {
		t.Errorf("turso.open() dropped the engine: preset.Type = %q, want turso", got)
	}
	if got := base.Memory().preset.Type; got != "turso" {
		t.Errorf("turso.memory() dropped the engine: preset.Type = %q, want turso", got)
	}

	// CONTROL — the plain sqlite capability's open must remain sqlite, so the assertions above are
	// about turso preserving its engine, not about every Open returning "turso".
	if got := sqliteForRequest(tn, "app.db", nil).Open("analytics.db").preset.Type; got != "sqlite" {
		t.Errorf("CONTROL: sqlite.open() changed engine: preset.Type = %q, want sqlite", got)
	}
}
