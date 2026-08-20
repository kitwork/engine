package work

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	requestscope "github.com/kitwork/engine/request"
)

// ---- interactive streams (Hrana baton) ----
//
// A db manager edits a row by opening a transaction that SPANS several HTTP requests: BEGIN, then the
// UPDATE/DELETE, then COMMIT — each its own pipeline. Hrana ties them together with a `baton`: the
// server pins one connection to a stream and hands back a baton the client sends on the next request.
// Without this, each request lands on a different pooled connection, the write autocommits, and the
// later COMMIT fails with "no transaction" — the change applies yet the manager reports a commit error.

type hranaStream struct {
	conn      *sql.Conn
	store     map[int32]string // prepared statements persist for the stream's life
	tenantKey string
	lastUsed  time.Time
}

var (
	streamsMu  sync.Mutex
	streams    = map[string]*hranaStream{}
	reaperOnce sync.Once
	streamIdle = 60 * time.Second
)

func newBaton() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// startStreamReaper closes streams left idle (a client that opened a transaction and went away), so a
// pinned connection is never leaked indefinitely.
func startStreamReaper() {
	reaperOnce.Do(func() {
		go func() {
			for range time.Tick(15 * time.Second) {
				cutoff := time.Now().Add(-streamIdle)
				streamsMu.Lock()
				for baton, s := range streams {
					if s.lastUsed.Before(cutoff) {
						_ = s.conn.Close()
						delete(streams, baton)
					}
				}
				streamsMu.Unlock()
			}
		}()
	})
}

// libSQL / Hrana-over-HTTP server: Kitwork speaks turso's wire protocol, so any libSQL client — the
// turso CLI, @libsql/client, Drizzle/Prisma libSQL adapters, a turso-compatible db manager — connects
// to a tenant's database by URL + auth token, exactly like turso.io:
//
//	client = createClient({ url: "http://localhost:8080", authToken: "<token>" })   // Host selects the tenant
//	client = createClient({ url: "http://localhost:8080/kiturl.db", authToken: "..." }) // path selects the db file
//
// The db is exposed by DECLARING it in JS — turso("kiturl.db", {schema}, { token: env.require("DB_TOKEN"),
// access: "readwrite" }) — so the decision to open it is visible in code, not a side effect of an env var
// existing. access:"readonly" (the default) refuses writes. The client POSTs Hrana pipelines to
// /v2/pipeline or /v3/pipeline; we run them on the connection the tenant already holds and answer in
// Hrana's JSON envelope. A pipeline runs on ONE pinned connection, and interactive transactions that
// span requests are held together with a baton (see the stream machinery above) — so a db manager's
// BEGIN … edit … COMMIT commits atomically instead of half-applying.

// ---- Hrana wire types (per HRANA_3_SPEC; v2 shares this JSON shape) ----

type hranaPipelineReq struct {
	Baton    *string              `json:"baton"`
	Requests []hranaStreamRequest `json:"requests"`
}

type hranaStreamRequest struct {
	Type  string      `json:"type"`
	Stmt  *hranaStmt  `json:"stmt"`
	Batch *hranaBatch `json:"batch"`
	SQLID *int32      `json:"sql_id"` // store_sql / close_sql
	SQL   *string     `json:"sql"`    // store_sql
}

type hranaStmt struct {
	SQL       *string         `json:"sql"`
	SQLID     *int32          `json:"sql_id"`
	Args      []hranaValue    `json:"args"`
	NamedArgs []hranaNamedArg `json:"named_args"`
	WantRows  bool            `json:"want_rows"`
}

type hranaNamedArg struct {
	Name  string     `json:"name"`
	Value hranaValue `json:"value"`
}

type hranaBatch struct {
	Steps []hranaBatchStep `json:"steps"`
}

type hranaBatchStep struct {
	Stmt hranaStmt `json:"stmt"`
	// condition is intentionally ignored in v1: steps run in order, stopping on the first error.
}

// hranaValue is the typed value encoding. Integer is a STRING (64-bit precision); blob is base64.
type hranaValue struct {
	Type   string          `json:"type"`
	Value  json.RawMessage `json:"value,omitempty"`
	Base64 string          `json:"base64,omitempty"`
}

func (t *Tenant) serveLibSQLIf(w http.ResponseWriter, r *http.Request, scope *requestscope.Scope) bool {
	pathDB, kind, matched := hranaTarget(r.URL.Path)
	if !matched {
		return false
	}
	// The db must be DECLARED served — turso("db", {schema}, { token, access }). Not served → fall
	// through to normal routing (the /v2,/v3 paths are only reserved for an exposed db).
	dbName, cfg, served := resolveServe(t, pathDB)
	if !served {
		return false
	}

	// GET /v2 or /v3 — version probe. A 200 means "this version is supported".
	if kind == "version" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return true
		}
		w.WriteHeader(http.StatusOK)
		return true
	}

	// POST /v2/pipeline or /v3/pipeline.
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	if !bearerMatches(r.Header.Get("Authorization"), cfg.token) {
		writeDataJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return true
	}

	var req hranaPipelineReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&req); err != nil {
		writeDataJSON(w, http.StatusBadRequest, map[string]any{"error": "bad pipeline json: " + err.Error()})
		return true
	}

	pool := tursoForRequest(t, dbName, scope).db()
	if pool == nil {
		writeDataJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "database unavailable"})
		return true
	}
	ctx := r.Context()
	readonly := cfg.access != "readwrite" // access:"readonly" (default) → writes are refused
	startStreamReaper()

	// Resolve the stream: a baton reuses a PINNED connection (so an interactive BEGIN … COMMIT spanning
	// several requests — a manager's row edit — is one real transaction); no baton opens a fresh stream.
	scopeKey := tenantScopeKey(t)
	var stream *hranaStream
	baton := ""
	if req.Baton != nil && *req.Baton != "" {
		streamsMu.Lock()
		if s := streams[*req.Baton]; s != nil && s.tenantKey == scopeKey {
			stream, baton = s, *req.Baton
			s.lastUsed = time.Now()
		}
		streamsMu.Unlock()
		if stream == nil {
			writeDataJSON(w, http.StatusOK, map[string]any{
				"baton": nil, "base_url": nil,
				"results": []any{hranaErr("stream expired — open a new one")},
			})
			return true
		}
	} else {
		conn, err := pool.Conn(ctx)
		if err != nil {
			writeDataJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
			return true
		}
		stream = &hranaStream{conn: conn, store: map[int32]string{}, tenantKey: scopeKey, lastUsed: time.Now()}
		baton = newBaton()
	}

	closed := false
	results := make([]map[string]any, 0, len(req.Requests))
	for _, sr := range req.Requests {
		if sr.Type == "close" {
			closed = true
		}
		results = append(results, runHranaRequest(ctx, stream.conn, sr, stream.store, readonly))
	}

	// A `close` request ends the stream (release the connection, return baton null); otherwise keep it
	// alive and hand the baton back so the client can continue the transaction on the next request.
	var batonOut any
	streamsMu.Lock()
	if closed {
		delete(streams, baton)
		streamsMu.Unlock()
		_ = stream.conn.Close()
		batonOut = nil
	} else {
		streams[baton] = stream
		stream.lastUsed = time.Now()
		streamsMu.Unlock()
		batonOut = baton
	}
	writeDataJSON(w, http.StatusOK, map[string]any{
		"baton":    batonOut,
		"base_url": nil,
		"results":  results,
	})
	return true
}

// hranaTarget matches the reserved libSQL paths, returning the optional db-name prefix and whether it
// is a "pipeline" POST or a "version" probe. "/v2/pipeline" and "/kiturl.db/v3/pipeline" both match.
func hranaTarget(path string) (db string, kind string, ok bool) {
	p := strings.Trim(path, "/")
	for _, v := range []string{"v2", "v3"} {
		if p == v {
			return "", "version", true
		}
		if p == v+"/pipeline" {
			return "", "pipeline", true
		}
		if strings.HasSuffix(p, "/"+v) {
			return strings.TrimSuffix(p, "/"+v), "version", true
		}
		if strings.HasSuffix(p, "/"+v+"/pipeline") {
			return strings.TrimSuffix(p, "/"+v+"/pipeline"), "pipeline", true
		}
	}
	return "", "", false
}

func runHranaRequest(ctx context.Context, conn *sql.Conn, sr hranaStreamRequest, store map[int32]string, readonly bool) map[string]any {
	switch sr.Type {
	case "close":
		return hranaOK(map[string]any{"type": "close"})
	case "get_autocommit":
		return hranaOK(map[string]any{"type": "get_autocommit", "is_autocommit": true})
	case "store_sql":
		if sr.SQLID == nil || sr.SQL == nil {
			return hranaErr("store_sql: missing sql_id or sql")
		}
		store[*sr.SQLID] = *sr.SQL
		return hranaOK(map[string]any{"type": "store_sql"})
	case "close_sql":
		if sr.SQLID != nil {
			delete(store, *sr.SQLID)
		}
		return hranaOK(map[string]any{"type": "close_sql"})
	case "execute":
		if sr.Stmt == nil {
			return hranaErr("execute: missing stmt")
		}
		res, err := runHranaStmt(ctx, conn, *sr.Stmt, store, readonly)
		if err != nil {
			return hranaErr(err.Error())
		}
		return hranaOK(map[string]any{"type": "execute", "result": res})
	case "batch":
		if sr.Batch == nil {
			return hranaErr("batch: missing batch")
		}
		n := len(sr.Batch.Steps)
		stepResults := make([]any, n)
		stepErrors := make([]any, n)
		// The client manages the transaction itself: a "write" batch already sends BEGIN … COMMIT as
		// steps. So run the steps as-is; do NOT impose our own transaction (that would nest BEGINs). On a
		// failure, stop and ROLLBACK so a half-open client transaction is not leaked on the pooled conn.
		failed := false
		for i, step := range sr.Batch.Steps {
			res, err := runHranaStmt(ctx, conn, step.Stmt, store, readonly)
			if err != nil {
				stepErrors[i] = map[string]any{"message": err.Error()}
				failed = true
				break
			}
			stepResults[i] = res
		}
		if failed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK") // no-op if there was no open transaction
		}
		return hranaOK(map[string]any{
			"type":   "batch",
			"result": map[string]any{"step_results": stepResults, "step_errors": stepErrors},
		})
	default:
		return hranaErr("unsupported request type: " + sr.Type)
	}
}

// stmtSQL resolves a statement's SQL from inline `sql` or a stored `sql_id`.
func stmtSQL(stmt hranaStmt, store map[int32]string) (string, error) {
	if stmt.SQL != nil {
		return *stmt.SQL, nil
	}
	if stmt.SQLID != nil {
		if s, ok := store[*stmt.SQLID]; ok {
			return s, nil
		}
		return "", errString("unknown sql_id (statement was not stored in this pipeline)")
	}
	return "", errString("statement has neither sql nor sql_id")
}

func runHranaStmt(ctx context.Context, conn *sql.Conn, stmt hranaStmt, store map[int32]string, readonly bool) (map[string]any, error) {
	sqlText, err := stmtSQL(stmt, store)
	if err != nil {
		return nil, err
	}
	if readonly && isWriteSQL(sqlText) {
		return nil, errString("database is read-only (serve access: readonly) — writes are refused")
	}
	args, err := hranaArgs(stmt)
	if err != nil {
		return nil, err
	}
	start := time.Now()

	// Decide Query vs Exec by whether the statement actually YIELDS ROWS, not by want_rows alone. A plain
	// write (INSERT/UPDATE/DELETE without RETURNING) returns no rows but DOES have an affected count — and
	// QueryContext cannot report RowsAffected, so a client that sends a DELETE with want_rows:true would
	// see affected_row_count:0 and conclude "nothing changed" even though the row was deleted. Route such
	// writes through Exec so the affected count is real. (A SELECT, or a write with RETURNING, still uses
	// Query to return its rows.)
	yieldsRows := stmt.WantRows
	if isWriteSQL(sqlText) && !strings.Contains(strings.ToLower(sqlText), "returning") {
		yieldsRows = false
	}

	if yieldsRows {
		rows, err := conn.QueryContext(ctx, sqlText, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return hranaRowsResult(rows, time.Since(start))
	}

	result, err := conn.ExecContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	affected, _ := result.RowsAffected()
	res := map[string]any{
		"cols":               []any{},
		"rows":               []any{},
		"affected_row_count": affected,
		"last_insert_rowid":  nil,
		"rows_read":          0,
		"rows_written":       affected,
		"query_duration_ms":  float64(time.Since(start).Microseconds()) / 1000,
	}
	if id, err := result.LastInsertId(); err == nil && id != 0 {
		res["last_insert_rowid"] = strconv.FormatInt(id, 10)
	}
	return res, nil
}

func hranaRowsResult(rows *sql.Rows, dur time.Duration) (map[string]any, error) {
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	decltypes := make([]string, len(names))
	if cts, err := rows.ColumnTypes(); err == nil {
		for i, ct := range cts {
			decltypes[i] = ct.DatabaseTypeName()
		}
	}
	cols := make([]any, len(names))
	for i, n := range names {
		name := n
		var dt any
		if decltypes[i] != "" {
			dt = decltypes[i]
		}
		cols[i] = map[string]any{"name": name, "decltype": dt}
	}

	outRows := []any{}
	var read int64
	for rows.Next() {
		cells := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		encoded := make([]any, len(names))
		for i, c := range cells {
			encoded[i] = hranaEncodeValue(c)
		}
		outRows = append(outRows, encoded)
		read++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{
		"cols":               cols,
		"rows":               outRows,
		"affected_row_count": 0,
		"last_insert_rowid":  nil,
		"rows_read":          read,
		"rows_written":       0,
		"query_duration_ms":  float64(dur.Microseconds()) / 1000,
	}, nil
}

// hranaEncodeValue maps a scanned SQLite value to the Hrana typed encoding. INTEGER is a string.
func hranaEncodeValue(v any) map[string]any {
	switch x := v.(type) {
	case nil:
		return map[string]any{"type": "null"}
	case int64:
		return map[string]any{"type": "integer", "value": strconv.FormatInt(x, 10)}
	case int:
		return map[string]any{"type": "integer", "value": strconv.Itoa(x)}
	case float64:
		return map[string]any{"type": "float", "value": x}
	case bool:
		if x {
			return map[string]any{"type": "integer", "value": "1"}
		}
		return map[string]any{"type": "integer", "value": "0"}
	case string:
		return map[string]any{"type": "text", "value": x}
	case []byte:
		return map[string]any{"type": "blob", "base64": base64.StdEncoding.EncodeToString(x)}
	case time.Time:
		return map[string]any{"type": "text", "value": x.Format(time.RFC3339)}
	default:
		b, _ := json.Marshal(x)
		return map[string]any{"type": "text", "value": string(b)}
	}
}

// hranaArgs converts positional + named args to driver args. Named args are passed as sql.NamedArg.
func hranaArgs(stmt hranaStmt) ([]any, error) {
	out := make([]any, 0, len(stmt.Args)+len(stmt.NamedArgs))
	for _, a := range stmt.Args {
		v, err := hranaDecodeValue(a)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	for _, na := range stmt.NamedArgs {
		v, err := hranaDecodeValue(na.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, sql.Named(na.Name, v))
	}
	return out, nil
}

func hranaDecodeValue(v hranaValue) (any, error) {
	switch v.Type {
	case "null", "":
		return nil, nil
	case "integer":
		var s string
		if err := json.Unmarshal(v.Value, &s); err != nil {
			return nil, errString("integer value must be a string")
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, err
		}
		return n, nil
	case "float":
		var f float64
		if err := json.Unmarshal(v.Value, &f); err != nil {
			return nil, errString("float value must be a number")
		}
		return f, nil
	case "text":
		var s string
		if err := json.Unmarshal(v.Value, &s); err != nil {
			return nil, errString("text value must be a string")
		}
		return s, nil
	case "blob":
		b, err := base64.StdEncoding.DecodeString(v.Base64)
		if err != nil {
			return nil, err
		}
		return b, nil
	default:
		return nil, errString("unknown value type: " + v.Type)
	}
}

func hranaOK(response map[string]any) map[string]any {
	return map[string]any{"type": "ok", "response": response}
}

func hranaErr(message string) map[string]any {
	return map[string]any{"type": "error", "error": map[string]any{"message": message}}
}

type errString string

func (e errString) Error() string { return string(e) }
