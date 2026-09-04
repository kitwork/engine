package work

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	requestscope "github.com/kitwork/engine/request"
)

// Data API — the tenant's database exposed over an authenticated URL, so app.db can be reached "as a
// database" from another Kitwork app, a script, or a laptop, instead of only by the process that owns
// the file. This is the practical, single-node form of "Kitwork as a database server": the process
// already holds the file (single writer + the single-instance lock), and this just lets others send it
// SQL over HTTP.
//
//	POST /_db/query
//	Authorization: Bearer <DB_TOKEN>
//	{ "db": "app.db", "sql": "select code, clicks from links where clicks > ?", "args": [0] }
//	→ { "rows": [ { "code": "kitwork", "clicks": 3 }, ... ] }
//
// Safety: OFF unless the db is declared served — sqlite("db", {schema}, { token }) (no serve → 404,
// nothing exposed). Read-only — only SELECT/WITH is accepted, and stacked statements are rejected — so
// a leaked token cannot mutate or drop data here (writes go through the libSQL endpoint under an
// access:"readwrite" serve).
const dataAPIQueryPath = "/_db/query"

// serveDataAPIIf handles the reserved /_db/query path. Returns true when it owned the response.
func (t *Tenant) serveDataAPIIf(w http.ResponseWriter, r *http.Request, scope *requestscope.Scope) bool {
	if r.URL.Path != dataAPIQueryPath {
		return false
	}

	if r.Method != http.MethodPost {
		writeDataJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
		return true
	}

	var req struct {
		DB   string `json:"db"`
		SQL  string `json:"sql"`
		Args []any  `json:"args"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeDataJSON(w, http.StatusBadRequest, map[string]any{"error": "bad json: " + err.Error()})
		return true
	}

	// The db must be explicitly declared served. Not served means reveal nothing.
	dbName, cfg, served := resolveServe(t, req.DB)
	if !served {
		writeDataJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return true
	}
	if !bearerMatches(r.Header.Get("Authorization"), cfg.token) {
		writeDataJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return true
	}
	// /_db/query is the simple, always-read-only surface (writes go through the libSQL endpoint).
	if !isReadOnlySQL(req.SQL) {
		writeDataJSON(w, http.StatusForbidden, map[string]any{"error": "read-only endpoint: only a single SELECT/WITH statement is allowed"})
		return true
	}
	if cfg.engine == "kitdb" {
		result, err := executeKitDBRemoteSQL(
			r.Context(), scope, cfg.database, req.SQL, kitSQLBindingsFromAny(req.Args), true,
		)
		if err != nil {
			writeDataJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return true
		}
		writeDataJSON(w, http.StatusOK, map[string]any{"rows": kitDBRemoteResultMaps(result)})
		return true
	}
	if cfg.engine != "sqlite" {
		writeDataJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "database engine unavailable"})
		return true
	}

	conn := sqliteForRequest(t, dbName, scope).db()
	if conn == nil {
		writeDataJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "database unavailable"})
		return true
	}

	rows, err := conn.QueryContext(r.Context(), req.SQL, req.Args...)
	if err != nil {
		writeDataJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return true
	}
	defer rows.Close()
	out, err := rowsToMaps(rows)
	if err != nil {
		writeDataJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return true
	}
	writeDataJSON(w, http.StatusOK, map[string]any{"rows": out})
	return true
}

func bearerMatches(header, token string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	got := strings.TrimSpace(header[len(prefix):])
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// isReadOnlySQL accepts a single SELECT or WITH…SELECT and nothing else. Stacked statements (a second
// statement after ';') are rejected so a SELECT cannot smuggle a write.
func isReadOnlySQL(text string) bool {
	s := strings.TrimSpace(text)
	s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
	if s == "" || strings.Contains(s, ";") {
		return false
	}
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "select") || strings.HasPrefix(lower, "with")
}

// rowsToMaps materializes a result set into JSON-friendly maps (raw []byte → string).
func rowsToMaps(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, name := range cols {
			v := values[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			row[name] = v
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func writeDataJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
