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
	// DB_TOKEN turns the endpoint on.
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("DB_TOKEN=secret-token-123\n"), 0644); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { turso, kitid, text, int } = database;
const links = { id: kitid().primaryKey(), code: text().notNull().unique(), clicks: int().default(0) };
const db = turso("kiturl.db", { links: links });
router.get((ctx) => {
  db.links.create({ code: "kitwork", clicks: 3 });
  return ctx.json({ ok: true });
});`
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
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

// With no DB_TOKEN configured the endpoint stays invisible (404), exposing nothing.
func TestDataAPIDisabledWithoutToken(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	router := `import { router } from "kitwork";
router.get((ctx) => ctx.json({ ok: true }));`
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"sql": "select 1"})
	req := httptest.NewRequest(http.MethodPost, "http://localhost/_db/query", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer anything")
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("endpoint should be 404 when DB_TOKEN unset, got %d", rec.Code)
	}
}
