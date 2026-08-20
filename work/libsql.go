package work

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	requestscope "github.com/kitwork/engine/request"
)

// libSQL / Hrana-over-HTTP server: Kitwork speaks turso's wire protocol, so any libSQL client — the
// turso CLI, @libsql/client, Drizzle/Prisma libSQL adapters, a turso-compatible db manager — connects
// to a tenant's database by URL + auth token, exactly like turso.io:
//
//	client = createClient({ url: "http://localhost:8080", authToken: "<DB_TOKEN>" })   // Host selects the tenant
//	client = createClient({ url: "http://localhost:8080/kiturl.db", authToken: "..." }) // path selects the db file
//
// The client POSTs Hrana pipelines to /v2/pipeline or /v3/pipeline; we run them on the connection the
// tenant already holds and answer in Hrana's JSON envelope. Enabled only when the tenant sets DB_TOKEN.
// A pipeline runs on ONE pinned connection (so BEGIN…COMMIT inside a single pipeline is a real
// transaction); we do not persist interactive streams across HTTP requests (baton is always returned
// null), which is what a db manager's autocommit queries need.
//
// Full read+write, like a turso auth token — the token is the whole gate. (The narrower, read-only
// /_db/query endpoint remains for simple curl use.)

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
	dbName, kind, ok := hranaTarget(r.URL.Path)
	if !ok {
		return false
	}
	// Gate the whole libSQL surface on DB_TOKEN. Off → let the path fall through to normal routing.
	tokenVal := t.envValue().Get("DB_TOKEN")
	if tokenVal.IsNil() || tokenVal.String() == "" {
		return false
	}
	token := tokenVal.String()

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
	if !bearerMatches(r.Header.Get("Authorization"), token) {
		writeDataJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return true
	}

	var req hranaPipelineReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&req); err != nil {
		writeDataJSON(w, http.StatusBadRequest, map[string]any{"error": "bad pipeline json: " + err.Error()})
		return true
	}

	if dbName == "" {
		dbName = t.envValue().Get("DB_DEFAULT").String()
		if strings.TrimSpace(dbName) == "" || t.envValue().Get("DB_DEFAULT").IsNil() {
			dbName = "app.db"
		}
	}
	pool := tursoForRequest(t, dbName, scope).db()
	if pool == nil {
		writeDataJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "database unavailable"})
		return true
	}

	// One pinned connection for the whole pipeline, so a BEGIN…COMMIT within it is a real transaction.
	ctx := r.Context()
	conn, err := pool.Conn(ctx)
	if err != nil {
		writeDataJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return true
	}
	defer conn.Close()

	// Prepared statements (store_sql/sql_id) that @libsql/client uses for batch() are resolved within
	// the pipeline: the client stores the SQL and references it in the same pipeline (baton is null, so
	// nothing needs to persist across HTTP requests).
	store := map[int32]string{}
	results := make([]map[string]any, 0, len(req.Requests))
	for _, sr := range req.Requests {
		results = append(results, runHranaRequest(ctx, conn, sr, store))
	}
	writeDataJSON(w, http.StatusOK, map[string]any{
		"baton":    nil,
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

func runHranaRequest(ctx context.Context, conn *sql.Conn, sr hranaStreamRequest, store map[int32]string) map[string]any {
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
		res, err := runHranaStmt(ctx, conn, *sr.Stmt, store)
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
			res, err := runHranaStmt(ctx, conn, step.Stmt, store)
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

func runHranaStmt(ctx context.Context, conn *sql.Conn, stmt hranaStmt, store map[int32]string) (map[string]any, error) {
	sqlText, err := stmtSQL(stmt, store)
	if err != nil {
		return nil, err
	}
	args, err := hranaArgs(stmt)
	if err != nil {
		return nil, err
	}
	start := time.Now()

	if stmt.WantRows {
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
