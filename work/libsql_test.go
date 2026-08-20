package work

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Drive the endpoint with the exact Hrana pipeline JSON a libSQL client sends, and verify the response
// envelope + typed value encoding (integer as a STRING, text as text). This is what turso's own clients
// and db managers speak.
func TestLibSQLHranaPipeline(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("DB_TOKEN=tok-abc\nDB_DEFAULT=app.db\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"),
		[]byte("import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.json({ ok: true }));"), 0644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}

	pipeline := func(token string, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://localhost/v2/pipeline", bytes.NewReader([]byte(body)))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec
	}

	// version probe
	{
		req := httptest.NewRequest(http.MethodGet, "http://localhost/v2", nil)
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		if rec.Code != 200 {
			t.Fatalf("GET /v2 version probe = %d, want 200", rec.Code)
		}
	}

	// unauthorized
	if rec := pipeline("", `{"baton":null,"requests":[{"type":"execute","stmt":{"sql":"select 1","want_rows":true}}]}`); rec.Code != 401 {
		t.Fatalf("no token should be 401, got %d", rec.Code)
	}

	// CREATE + INSERT (with typed args) + SELECT, all in one authorized pipeline.
	body := `{"baton":null,"requests":[
	  {"type":"execute","stmt":{"sql":"CREATE TABLE links (id integer primary key, code text, clicks integer)","want_rows":false}},
	  {"type":"execute","stmt":{"sql":"INSERT INTO links (code, clicks) VALUES (?, ?)","args":[{"type":"text","value":"kitwork"},{"type":"integer","value":"3"}],"want_rows":false}},
	  {"type":"execute","stmt":{"sql":"SELECT code, clicks FROM links","want_rows":true}},
	  {"type":"close"}
	]}`
	rec := pipeline("tok-abc", body)
	if rec.Code != 200 {
		t.Fatalf("pipeline status %d: %s", rec.Code, rec.Body.String())
	}
	t.Logf("hrana response: %s", rec.Body.String())

	var resp struct {
		Baton   *string `json:"baton"`
		Results []struct {
			Type     string `json:"type"`
			Response struct {
				Type   string `json:"type"`
				Result struct {
					Cols             []map[string]any   `json:"cols"`
					Rows             [][]map[string]any `json:"rows"`
					AffectedRowCount int64              `json:"affected_row_count"`
					LastInsertRowid  *string            `json:"last_insert_rowid"`
				} `json:"result"`
			} `json:"response"`
			Error map[string]any `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad hrana json: %v", err)
	}
	if len(resp.Results) != 4 {
		t.Fatalf("want 4 results, got %d", len(resp.Results))
	}
	for i, r := range resp.Results {
		if r.Type != "ok" {
			t.Fatalf("result[%d] not ok: %+v", i, r.Error)
		}
	}
	// INSERT gave a last_insert_rowid.
	if resp.Results[1].Response.Result.LastInsertRowid == nil {
		t.Errorf("insert should report last_insert_rowid")
	}
	// SELECT rows with correct typed encoding.
	sel := resp.Results[2].Response.Result
	if len(sel.Rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(sel.Rows))
	}
	code := sel.Rows[0][0]
	clicks := sel.Rows[0][1]
	if code["type"] != "text" || code["value"] != "kitwork" {
		t.Errorf("code cell wrong: %+v (TEXT must encode as text, not blob)", code)
	}
	if clicks["type"] != "integer" || clicks["value"] != "3" {
		t.Errorf("clicks cell wrong: %+v (INTEGER must be a STRING value)", clicks)
	}
}

// A transaction inside a single pipeline is real (same pinned connection): BEGIN/INSERT/COMMIT then
// COUNT sees the row.
func TestLibSQLTransactionInPipeline(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "localhost")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("DB_TOKEN=tok\n"), 0644)
	os.WriteFile(filepath.Join(dir, "router.kitwork.js"),
		[]byte("import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.json({}));"), 0644)
	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	body := `{"baton":null,"requests":[
	  {"type":"execute","stmt":{"sql":"CREATE TABLE t (n integer)","want_rows":false}},
	  {"type":"execute","stmt":{"sql":"BEGIN","want_rows":false}},
	  {"type":"execute","stmt":{"sql":"INSERT INTO t (n) VALUES (1)","want_rows":false}},
	  {"type":"execute","stmt":{"sql":"INSERT INTO t (n) VALUES (2)","want_rows":false}},
	  {"type":"execute","stmt":{"sql":"COMMIT","want_rows":false}},
	  {"type":"execute","stmt":{"sql":"SELECT count(*) c FROM t","want_rows":true}},
	  {"type":"close"}
	]}`
	req := httptest.NewRequest(http.MethodPost, "http://localhost/v2/pipeline", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"value":"2"`)) {
		t.Errorf("committed transaction should count 2 rows; body: %s", rec.Body.String())
	}
}
