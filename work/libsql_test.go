package work

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// servedTenant boots a tenant that DECLARES a served db in JS — turso(db, {schema}, { token, access }).
// That declaration (run during tenant.Run) is what enables the libSQL and /_db endpoints; there is no
// .env magic. The declared `seed` table just turns exposure on — a client can create other tables.
func servedTenant(t *testing.T, dbName, token, access string) *Tenant {
	t.Helper()
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	router := fmt.Sprintf(`import { router, database } from "kitwork";
const { turso, kitid } = database;
export const db = turso(%q, { seed: { id: kitid().primaryKey() } }, { token: %q, access: %q });
router.get((ctx) => ctx.json({ ok: true }));`, dbName, token, access)
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	return tenant
}

// Drive the endpoint with the exact Hrana pipeline JSON a libSQL client sends, and verify the response
// envelope + typed value encoding (integer as a STRING, text as text). This is what turso's own clients
// and db managers speak.
func TestLibSQLHranaPipeline(t *testing.T) {
	tenant := servedTenant(t, "app.db", "tok-abc", "readwrite")

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
	tenant := servedTenant(t, "app.db", "tok", "readwrite")
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

// Prepared statements + batch, the shape @libsql/client sends for db.batch(): store_sql once, then a
// batch whose steps reference it by sql_id. This was the case that broke the first real-client run.
func TestLibSQLStoreSQLAndBatch(t *testing.T) {
	tenant := servedTenant(t, "app.db", "tok", "readwrite")
	body := `{"baton":null,"requests":[
	  {"type":"execute","stmt":{"sql":"CREATE TABLE t (n integer)","want_rows":false}},
	  {"type":"store_sql","sql_id":1,"sql":"INSERT INTO t (n) VALUES (?)"},
	  {"type":"batch","batch":{"steps":[
	     {"stmt":{"sql_id":1,"args":[{"type":"integer","value":"10"}]}},
	     {"stmt":{"sql_id":1,"args":[{"type":"integer","value":"20"}]}}
	  ]}},
	  {"type":"close_sql","sql_id":1},
	  {"type":"execute","stmt":{"sql":"SELECT sum(n) s FROM t","want_rows":true}},
	  {"type":"close"}
	]}`
	req := httptest.NewRequest(http.MethodPost, "http://localhost/v3/pipeline", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(`"type":"error"`)) {
		t.Fatalf("no result should be an error; body: %s", rec.Body.String())
	}
	// sum(10,20) = 30 → integer encoded as the string "30".
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"value":"30"`)) {
		t.Errorf("store_sql + batch should have inserted 10 and 20 (sum 30); body: %s", rec.Body.String())
	}
}

// An interactive transaction that SPANS requests — BEGIN, then the write, then COMMIT, each its own
// pipeline tied by a baton — must commit atomically. This is what a db manager does for a grid edit;
// without baton streams the write autocommits and the COMMIT fails ("all changes failed to commit").
func TestLibSQLInteractiveTransaction(t *testing.T) {
	tenant := servedTenant(t, "app.db", "tok", "readwrite")
	pipe := func(body string) map[string]any {
		req := httptest.NewRequest(http.MethodPost, "http://localhost/v2/pipeline", bytes.NewReader([]byte(body)))
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		var m map[string]any
		json.Unmarshal(rec.Body.Bytes(), &m)
		return m
	}
	pipe(`{"baton":null,"requests":[
	  {"type":"execute","stmt":{"sql":"CREATE TABLE links (id integer primary key, code text)","want_rows":false}},
	  {"type":"execute","stmt":{"sql":"INSERT INTO links(code) VALUES('keep')","want_rows":false}},
	  {"type":"execute","stmt":{"sql":"INSERT INTO links(code) VALUES('gone')","want_rows":false}},
	  {"type":"close"}]}`)

	// BEGIN in its own request → a baton comes back (the stream is kept open).
	r1 := pipe(`{"baton":null,"requests":[{"type":"execute","stmt":{"sql":"BEGIN","want_rows":false}}]}`)
	b, _ := r1["baton"].(string)
	if b == "" {
		t.Fatal("BEGIN should return a non-null baton keeping the stream open")
	}
	// DELETE on the same stream. Hrana rotates the single-use baton after each
	// request while retaining the pinned connection behind it.
	r2 := pipe(`{"baton":"` + b + `","requests":[{"type":"execute","stmt":{"sql":"DELETE FROM links WHERE code='gone'","want_rows":false}}]}`)
	b2, _ := r2["baton"].(string)
	if b2 == "" || b2 == b {
		t.Fatalf("baton must rotate while retaining the stream, old=%q new=%q", b, b2)
	}
	if replay := pipe(`{"baton":"` + b + `","requests":[{"type":"get_autocommit"}]}`); !bytes.Contains(mustJSON(replay), []byte("stream expired")) {
		t.Fatalf("consumed baton must not be replayable: %s", mustJSON(replay))
	}
	r3 := pipe(`{"baton":"` + b2 + `","requests":[{"type":"execute","stmt":{"sql":"COMMIT","want_rows":false}},{"type":"close"}]}`)
	if r3["baton"] != nil {
		t.Errorf("after close the baton should be null, got %v", r3["baton"])
	}
	body, _ := json.Marshal(pipe(`{"baton":null,"requests":[{"type":"execute","stmt":{"sql":"SELECT code FROM links","want_rows":true}},{"type":"close"}]}`))
	if !bytes.Contains(body, []byte(`"keep"`)) || bytes.Contains(body, []byte(`"gone"`)) {
		t.Errorf("interactive DELETE+COMMIT did not commit correctly; final: %s", body)
	}
	// A made-up baton is rejected as expired, not silently accepted.
	if r := pipe(`{"baton":"deadbeef","requests":[{"type":"execute","stmt":{"sql":"select 1","want_rows":true}}]}`); !bytes.Contains(mustJSON(r), []byte("stream expired")) {
		t.Errorf("unknown baton should report an expired stream; got %s", mustJSON(r))
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// A write sent with want_rows:true (what a db manager does — it asks for a result to confirm the edit)
// must still report the real affected_row_count. The bug: want_rows routed the DELETE through Query,
// which cannot report RowsAffected, so it came back 0 and the manager concluded "nothing changed" even
// though the row was gone — "all changes failed to commit" while the delete actually happened.
func TestLibSQLWriteWithWantRowsReportsAffected(t *testing.T) {
	tenant := servedTenant(t, "app.db", "tok", "readwrite")
	pipe := func(body string) []byte {
		req := httptest.NewRequest(http.MethodPost, "http://localhost/v2/pipeline", bytes.NewReader([]byte(body)))
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec.Body.Bytes()
	}
	pipe(`{"baton":null,"requests":[
	  {"type":"execute","stmt":{"sql":"CREATE TABLE t(id integer primary key, c text)","want_rows":false}},
	  {"type":"execute","stmt":{"sql":"INSERT INTO t(c) VALUES('a'),('b'),('c')","want_rows":false}},
	  {"type":"close"}]}`)
	del := pipe(`{"baton":null,"requests":[{"type":"execute","stmt":{"sql":"DELETE FROM t WHERE c='b'","want_rows":true}},{"type":"close"}]}`)
	if !bytes.Contains(del, []byte(`"affected_row_count":1`)) {
		t.Errorf("DELETE with want_rows:true must report affected_row_count:1, got: %s", del)
	}
	upd := pipe(`{"baton":null,"requests":[{"type":"execute","stmt":{"sql":"UPDATE t SET c='A' WHERE c='a'","want_rows":true}},{"type":"close"}]}`)
	if !bytes.Contains(upd, []byte(`"affected_row_count":1`)) {
		t.Errorf("UPDATE with want_rows:true must report affected_row_count:1, got: %s", upd)
	}
	// An INSERT … RETURNING with want_rows still comes back as rows.
	ins := pipe(`{"baton":null,"requests":[{"type":"execute","stmt":{"sql":"INSERT INTO t(c) VALUES('z') RETURNING c","want_rows":true}},{"type":"close"}]}`)
	if !bytes.Contains(ins, []byte(`"type":"text","value":"z"`)) {
		t.Errorf("INSERT … RETURNING should still yield its row, got: %s", ins)
	}
}

// access:"readonly" (also the default when access is omitted) must refuse writes while allowing reads.
func TestLibSQLReadonlyRefusesWrites(t *testing.T) {
	tenant := servedTenant(t, "app.db", "ro", "readonly")
	pipe := func(sql string, wantRows bool) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"baton":null,"requests":[{"type":"execute","stmt":{"sql":%q,"want_rows":%v}}]}`, sql, wantRows)
		req := httptest.NewRequest(http.MethodPost, "http://localhost/v2/pipeline", bytes.NewReader([]byte(body)))
		req.Header.Set("Authorization", "Bearer ro")
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec
	}
	// A write is refused with the read-only error, never reaching the db.
	if w := pipe("CREATE TABLE x (n integer)", false); !bytes.Contains(w.Body.Bytes(), []byte("read-only")) {
		t.Errorf("readonly serve must refuse a write; body: %s", w.Body.String())
	}
	// A read passes.
	if r := pipe("select 1 as n", true); bytes.Contains(r.Body.Bytes(), []byte(`"type":"error"`)) {
		t.Errorf("readonly serve must allow SELECT; body: %s", r.Body.String())
	}
}
