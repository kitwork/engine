package work

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// End to end: the tenant db reachable "as a database over a URL". Seed a row via a normal request, then
// hit /_db/query with a bearer token and read it back through raw SQL — the same thing a remote client
// (kitdb driver, curl, a laptop) would do against http://localhost:8080/_db/query.
func TestDataAPIQueryOverHTTP(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// The db is DECLARED served in JS (token + access) — that is what turns the endpoint on, not a .env.
	router := `import * as router from "kitwork/router";
import { internals } from "kitwork";
const { sqlite, kitid, text, int } = internals;
const links = { id: kitid().primaryKey(), code: text().notNull().unique(), clicks: int().default(0) };
const db = sqlite("kiturl.db", { links: links }, { token: "secret-token-123", access: "readwrite" });
router.get((context) => {
  db.links.create({ code: "kitwork", clicks: 3 });
  return context.json({ ok: true });
});`
	if err := os.WriteFile(filepath.Join(dir, UIRouterFileName), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	defer tenant.Close()
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	serve := func(req *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec
	}

	// 1) seed a row through a normal request.
	if rec := serve(httptest.NewRequest(http.MethodGet, "http://localhost/", nil)); rec.Code != 200 {
		t.Fatalf("seed request failed: %d %s", rec.Code, rec.Body.String())
	}

	query := func(token, sql string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"db": "kiturl.db", "sql": sql})
		req := httptest.NewRequest(http.MethodPost, "http://localhost/_db/query", bytes.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return serve(req)
	}

	// 2) authorized SELECT returns the row.
	rec := query("secret-token-123", "select code, clicks from links")
	t.Logf("query -> %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	if rec.Code != 200 {
		t.Fatalf("authorized query status %d: %s", rec.Code, rec.Body.String())
	}
	var ok struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ok); err != nil {
		t.Fatalf("bad response json: %v", err)
	}
	if len(ok.Rows) != 1 || ok.Rows[0]["code"] != "kitwork" {
		t.Fatalf("expected one row code=kitwork, got %+v", ok.Rows)
	}

	// 3) no token → 401.
	if rec := query("", "select 1"); rec.Code != http.StatusUnauthorized {
		t.Errorf("missing token should be 401, got %d", rec.Code)
	}
	// 4) wrong token → 401.
	if rec := query("nope", "select 1"); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token should be 401, got %d", rec.Code)
	}
	// 5) non-SELECT is rejected even with a valid token (read-only).
	if rec := query("secret-token-123", "delete from links"); rec.Code != http.StatusForbidden {
		t.Errorf("write statement should be 403, got %d: %s", rec.Code, rec.Body.String())
	}
	// 6) stacked statement rejected.
	if rec := query("secret-token-123", "select 1; drop table links"); rec.Code != http.StatusForbidden {
		t.Errorf("stacked statement should be 403, got %d", rec.Code)
	}
}

// With no database declared served, the endpoint stays invisible (404).
func TestDataAPIDisabledWithoutToken(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	router := `import * as router from "kitwork/router";
router.get((context) => context.json({ ok: true }));`
	if err := os.WriteFile(filepath.Join(dir, UIRouterFileName), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(tmp, "localhost")
	defer tenant.Close()
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"sql": "select 1"})
	req := httptest.NewRequest(http.MethodPost, "http://localhost/_db/query", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer anything")
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("endpoint should be 404 when no db is declared served, got %d", rec.Code)
	}
}

// The libSQL server (Hrana over HTTP) went on 29/10/2026: a database's only port to the outside is the
// PostgreSQL wire (KITWORK_KITDB.md 13.5). Its paths are a site's paths again. With a database declared
// served, the way the Hrana tests declared one ({ token }), and its token sent, GET /v2 and
// POST /v2/pipeline reach the site's routes there, and POST /app.db/v3/pipeline, where the site has no
// route, is the site's 404; none of them is a Hrana answer.
// CONTROL: with t.serveLibSQLIf(w, r, requestScope) put back in Tenant.Serve (work.go),
// "GET /v2 = 200 \"\", want the route's answer".
func TestHranaPathsFallThrough(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "localhost")
	routes := map[string]string{
		"": `import * as router from "kitwork/router";
import { internals } from "kitwork";
const { sqlite, kitid, text } = internals;
const things = { id: kitid().primaryKey(), name: text() };
const db = sqlite("app.db", { things: things }, { token: "tok", access: "readwrite" });
router.get((context) => context.json({ route: "root", count: db.things.count() }));`,
		"v2": `import * as router from "kitwork/router";
router.get((context) => context.json({ route: "v2" }));`,
		filepath.Join("v2", "pipeline"): `import * as router from "kitwork/router";
router.post((context) => context.json({ route: "v2/pipeline" }));`,
	}
	for folder, source := range routes {
		if err := os.MkdirAll(filepath.Join(dir, folder), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, folder, UIRouterFileName), []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tenant := NewTenant(tmp, "localhost")
	defer tenant.Close()
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	serve := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec
	}

	// The database is declared served: the fall-through below is not for want of a served database.
	if rec := serve(http.MethodGet, "/", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"route":"root"`) {
		t.Fatalf("GET / = %d %q", rec.Code, rec.Body.String())
	}
	if name, config, served := resolveServe(tenant, ""); !served || name != "app.db" || config.token != "tok" {
		t.Fatalf("app.db is not declared served with its token: %q %+v %v", name, config, served)
	}

	pipeline := `{"baton":null,"requests":[{"type":"execute","stmt":{"sql":"SELECT * FROM things","want_rows":true}},{"type":"close"}]}`
	hranaAnswer := func(body string) bool {
		return strings.Contains(body, `"baton"`) || strings.Contains(body, `"results"`)
	}
	if rec := serve(http.MethodGet, "/v2", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"route":"v2"`) {
		t.Errorf("GET /v2 = %d %q, want the route's answer", rec.Code, rec.Body.String())
	}
	if rec := serve(http.MethodPost, "/v2/pipeline", pipeline); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"route":"v2/pipeline"`) || hranaAnswer(rec.Body.String()) {
		t.Errorf("POST /v2/pipeline = %d %q, want the route's answer", rec.Code, rec.Body.String())
	}
	if rec := serve(http.MethodPost, "/app.db/v3/pipeline", pipeline); rec.Code != http.StatusNotFound || hranaAnswer(rec.Body.String()) {
		t.Errorf("POST /app.db/v3/pipeline = %d %q, want the site's 404", rec.Code, rec.Body.String())
	}
}
