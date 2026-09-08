package work

import (
	"context"
	"database/sql/driver"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kitwork/engine/database"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/value"
)

// SQLite is the per-tenant embedded database entry — import { sqlite } from "kitwork".
//
// It is a BLUEPRINT, not a connection: sqlite / sqlite.open("x.db") only NAME a file; nothing touches
// the disk until the first query (Database.Connect's preset path opens lazily and caches the handle in
// tenant.databases). There is deliberately no close() — the ENGINE owns the connection lifecycle, the
// same way http owns its transport. The query surface (table/where/find/create/…) is the ordinary
// query builder, promoted from the embedded *Database — ONE builder, one security audit surface
// (parameterized queries, mandatory WHERE), two backends.
//
//	sqlite.table("users").where("age", ">", 18).find()   // default file: .data/app.db
//	sqlite.open("analytics.db").table("events").find()   // named file:  .data/analytics.db
//	sqlite.memory().exec("CREATE TABLE t (x)")           // :memory: — tests, scratch
//
// Every file lives under the tenant's .data/ folder BY CONSTRUCTION: the static server refuses any
// dot segment (tree_serve), so a tenant database can never be downloaded over HTTP — unlike a
// database file at the tenant root, which would be served like any other static file.
type SQLite struct {
	*Database
}

const (
	sqliteReadQueryMaxSQLBytes    = 32 << 10
	sqliteReadQueryMaxRows        = 120
	sqliteReadQueryMaxColumns     = 128
	sqliteReadQueryMaxCellBytes   = 256 << 10
	sqliteReadQueryMaxResultBytes = 2 << 20
	sqliteReadQueryTimeout        = 2 * time.Second
	sqliteReadQueryResetTimeout   = 250 * time.Millisecond
	sqliteJavaScriptMaxSafeInt    = int64(1<<53 - 1)
)

// Sqlite is what `import { sqlite } from "kitwork"` resolves to (0-arg getter, auto-called): the
// tenant's default database at .data/app.db.
func (w *KitWork) Sqlite() *SQLite {
	return sqliteForRequest(w.tenant, "app.db", w.requestScope)
}

// Open names another database file inside the tenant's .data/ folder — a blueprint, zero I/O.
// Subfolders are fine ("archive/2026.db"); traversal is not (see sqliteRel).
func (s *SQLite) Open(path string) *SQLite {
	return sqliteForRequest(s.tenant, path, s.requestScope)
}

// Memory returns the tenant's in-memory database (:memory:, one shared connection) — for tests and
// scratch work. It vanishes with the process.
func (s *SQLite) Memory() *SQLite {
	preset := &database.Config{Alias: "sqlite::memory:", Type: "sqlite", Name: ":memory:"}
	return &SQLite{Database: &Database{
		tenant:       s.tenant,
		requestScope: s.requestScope,
		config:       &database.Config{},
		preset:       preset,
	}}
}

// Exec runs raw SQL — the escape hatch for DDL (CREATE TABLE / CREATE INDEX / migrations), which a
// query builder cannot express. Data access should stay on the builder (parameterized, mandatory
// WHERE); args here are still bound as parameters, never interpolated. Errors return in-band
// (K=Invalid → .isError/.message), the same shape as a failed query.
func (s *SQLite) Exec(sqlText string, args ...value.Value) value.Value {
	conn := s.db()
	if conn == nil {
		return value.Value{K: value.Invalid, V: "sqlite: connection unavailable"}
	}
	goArgs := make([]any, len(args))
	for i, a := range args {
		goArgs[i] = a.Interface()
	}
	res, err := conn.Exec(sqlText, goArgs...)
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	affected, _ := res.RowsAffected()
	return value.New(map[string]value.Value{"rowsAffected": value.New(int(affected))})
}

// ReadQuery executes one bounded SELECT against this already-confined SQLite blueprint. It is the
// deliberately narrow escape hatch used by database inspectors: the caller may choose SQL, but it
// can never choose a path/DSN, mutate the database, attach another file, or retain an engine-owned
// connection. The lexical single-SELECT gate and SQLite's per-connection query_only flag are both
// required; neither is treated as a substitute for the other.
func (s *SQLite) ReadQuery(input value.Value) (result value.Value) {
	if input.K != value.String {
		return sqliteReadQueryFailure("INVALID_QUERY", "Enter one SELECT statement")
	}
	sqlText := input.String()
	if len(sqlText) > sqliteReadQueryMaxSQLBytes {
		return sqliteReadQueryFailure("QUERY_TOO_LARGE", "SELECT statement exceeds the 32 KiB limit")
	}
	if strings.TrimSpace(sqlText) == "" || strings.ContainsRune(sqlText, '\x00') {
		return sqliteReadQueryFailure("INVALID_QUERY", "Enter one SELECT statement")
	}
	if !sqliteSingleReadStatement(sqlText) {
		return sqliteReadQueryFailure("READ_ONLY_REQUIRED", "Only one SELECT statement is allowed")
	}

	parent := context.Background()
	if s.requestScope != nil {
		parent = s.requestScope.Context()
	}
	ctx, cancel := context.WithTimeout(parent, sqliteReadQueryTimeout)
	defer cancel()

	db := s.db()
	if db == nil {
		return sqliteReadQueryFailure("DATABASE_UNAVAILABLE", "SQLite database is unavailable")
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}
	defer conn.Close()

	queryOnly := false
	defer func() {
		if !queryOnly {
			return
		}
		resetCtx, resetCancel := context.WithTimeout(context.Background(), sqliteReadQueryResetTimeout)
		_, resetErr := conn.ExecContext(resetCtx, "PRAGMA query_only=OFF")
		resetCancel()
		if resetErr == nil {
			return
		}
		// Never return a connection whose write policy is unknown to the shared pool.
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		result = sqliteReadQueryFailure("DATABASE_UNAVAILABLE", "SQLite connection could not be reset safely")
	}()

	if _, err = conn.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}
	queryOnly = true
	started := time.Now()
	rows, err := conn.QueryContext(ctx, sqlText)
	if err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}
	defer rows.Close()

	names, err := rows.Columns()
	if err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}
	if len(names) == 0 || len(names) > sqliteReadQueryMaxColumns {
		return sqliteReadQueryFailure("RESULT_TOO_LARGE", "Query result exceeds the 128 column limit")
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}
	columns := make([]map[string]any, len(names))
	resultBytes := 0
	for index, name := range names {
		if len(name) > 1024 {
			return sqliteReadQueryFailure("RESULT_TOO_LARGE", "Query result contains an oversized column name")
		}
		typeName := "UNKNOWN"
		if index < len(types) && strings.TrimSpace(types[index].DatabaseTypeName()) != "" {
			typeName = strings.ToUpper(strings.TrimSpace(types[index].DatabaseTypeName()))
		}
		columns[index] = map[string]any{"name": name, "type": typeName}
		resultBytes += len(name) + len(typeName)
	}

	resultRows := make([][]any, 0, sqliteReadQueryMaxRows)
	truncated := false
	for rows.Next() {
		if len(resultRows) == sqliteReadQueryMaxRows {
			truncated = true
			break
		}
		values := make([]any, len(names))
		pointers := make([]any, len(names))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return s.sqliteReadQueryContextFailure(err)
		}
		for index, raw := range values {
			cell, size, ok := sqliteReadQueryCell(raw)
			if !ok || resultBytes+size > sqliteReadQueryMaxResultBytes {
				return sqliteReadQueryFailure("RESULT_TOO_LARGE", "Query result exceeds the 2 MiB limit")
			}
			values[index] = cell
			resultBytes += size
		}
		resultRows = append(resultRows, values)
	}
	if err := rows.Err(); err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}
	duration := time.Since(started).Milliseconds()
	return value.New(map[string]any{
		"ok":           true,
		"columns":      columns,
		"rows":         resultRows,
		"rowCount":     len(resultRows),
		"returnedRows": len(resultRows),
		"truncated":    truncated,
		"durationMs":   duration,
	})
}

func sqliteReadQueryFailure(code, message string) value.Value {
	return value.New(map[string]any{"ok": false, "code": code, "message": message})
}

func (s *SQLite) sqliteReadQueryContextFailure(err error) value.Value {
	if errors.Is(err, context.DeadlineExceeded) {
		return sqliteReadQueryFailure("TIMEOUT", "Query exceeded the 2 second time limit")
	}
	if errors.Is(err, context.Canceled) {
		return sqliteReadQueryFailure("CANCELLED", "Query was cancelled")
	}
	message := strings.Join(strings.Fields(err.Error()), " ")
	if s != nil && s.preset != nil && s.preset.Name != "" {
		message = strings.ReplaceAll(message, s.preset.Name, "[database]")
		message = strings.ReplaceAll(message, filepath.ToSlash(s.preset.Name), "[database]")
	}
	if len(message) > 320 {
		message = message[:320]
	}
	if message == "" {
		message = "SQLite could not execute this SELECT statement"
	}
	return sqliteReadQueryFailure("SQL_ERROR", message)
}

func sqliteReadQueryCell(raw any) (any, int, bool) {
	switch cell := raw.(type) {
	case nil:
		return nil, 4, true
	case []byte:
		if len(cell) > sqliteReadQueryMaxCellBytes {
			return nil, 0, false
		}
		encoded := base64.StdEncoding.EncodeToString(cell)
		return map[string]any{"type": "blob", "base64": encoded, "bytes": len(cell)}, len(encoded) + 32, true
	case string:
		if len(cell) > sqliteReadQueryMaxCellBytes || !utf8.ValidString(cell) {
			return nil, 0, false
		}
		return cell, len(cell), true
	case int64:
		if cell > sqliteJavaScriptMaxSafeInt || cell < -sqliteJavaScriptMaxSafeInt {
			text := strconv.FormatInt(cell, 10)
			return text, len(text), true
		}
		return cell, 8, true
	case float64:
		if math.IsNaN(cell) || math.IsInf(cell, 0) {
			return nil, 0, false
		}
		return cell, 8, true
	case bool:
		return cell, 1, true
	case time.Time:
		text := cell.UTC().Format(time.RFC3339Nano)
		return text, len(text), true
	default:
		text := fmt.Sprint(cell)
		if len(text) > sqliteReadQueryMaxCellBytes || !utf8.ValidString(text) {
			return nil, 0, false
		}
		return text, len(text), true
	}
}

// sqliteSingleReadStatement accepts one SELECT (with optional leading/trailing comments and one
// trailing semicolon). It understands SQLite's string/comment/quoted-identifier delimiters so a
// semicolon inside data is not mistaken for a stacked statement. WITH/EXPLAIN/PRAGMA/ATTACH are
// intentionally outside this first read-only profile.
func sqliteSingleReadStatement(text string) bool {
	start, ok := sqliteSkipSpaceAndComments(text, 0)
	if !ok || start+6 > len(text) || !strings.EqualFold(text[start:start+6], "select") {
		return false
	}
	if start+6 < len(text) && sqliteIdentifierByte(text[start+6]) {
		return false
	}
	for index := start + 6; index < len(text); {
		switch text[index] {
		case '\'', '"', '`':
			quote := text[index]
			index++
			contentStart := index
			closed := false
			for index < len(text) {
				if text[index] != quote {
					index++
					continue
				}
				if index+1 < len(text) && text[index+1] == quote {
					index += 2
					continue
				}
				content := text[contentStart:index]
				if quote != '\'' {
					content = strings.ReplaceAll(content, string([]byte{quote, quote}), string(quote))
					if sqliteForbiddenReadIdentifier(content) {
						return false
					}
				}
				index++
				closed = true
				break
			}
			if !closed {
				return false
			}
		case '[':
			index++
			contentStart := index
			for index < len(text) && text[index] != ']' {
				index++
			}
			if index == len(text) {
				return false
			}
			if sqliteForbiddenReadIdentifier(text[contentStart:index]) {
				return false
			}
			index++
		case '-':
			if index+1 < len(text) && text[index+1] == '-' {
				for index < len(text) && text[index] != '\n' {
					index++
				}
				continue
			}
			index++
		case '/':
			if index+1 < len(text) && text[index+1] == '*' {
				var commentOK bool
				index, commentOK = sqliteSkipBlockComment(text, index)
				if !commentOK {
					return false
				}
				continue
			}
			index++
		case ';':
			end, tailOK := sqliteSkipSpaceAndComments(text, index+1)
			return tailOK && end == len(text)
		default:
			if sqliteIdentifierByte(text[index]) {
				start := index
				for index < len(text) && sqliteIdentifierByte(text[index]) {
					index++
				}
				if sqliteForbiddenReadIdentifier(text[start:index]) {
					return false
				}
				continue
			}
			index++
		}
	}
	return true
}

func sqliteSkipSpaceAndComments(text string, index int) (int, bool) {
	for index < len(text) {
		if strings.ContainsRune(" \t\r\n\f\v", rune(text[index])) {
			index++
			continue
		}
		if index+1 < len(text) && text[index] == '-' && text[index+1] == '-' {
			for index < len(text) && text[index] != '\n' {
				index++
			}
			continue
		}
		if index+1 < len(text) && text[index] == '/' && text[index+1] == '*' {
			var ok bool
			index, ok = sqliteSkipBlockComment(text, index)
			if !ok {
				return index, false
			}
			continue
		}
		break
	}
	return index, true
}

func sqliteSkipBlockComment(text string, index int) (int, bool) {
	index += 2
	for index+1 < len(text) {
		if text[index] == '*' && text[index+1] == '/' {
			return index + 2, true
		}
		index++
	}
	return len(text), false
}

func sqliteIdentifierByte(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '$'
}

func sqliteForbiddenReadIdentifier(identifier string) bool {
	switch strings.ToLower(identifier) {
	case "load_extension", "readfile", "writefile":
		return true
	default:
		return false
	}
}

// sqliteFor builds the blueprint for one tenant database file: resolve the path under .data/,
// pin the connection config (alias "sqlite:<rel>" keys the cache in tenant.databases).
func sqliteFor(t *Tenant, rel string) *SQLite {
	return sqliteForRequest(t, rel, nil)
}

func sqliteForRequest(t *Tenant, rel string, requestScope *requestscope.Scope) *SQLite {
	rel = sqliteRel(rel)
	preset := &database.Config{
		Alias: "sqlite:" + rel,
		Type:  "sqlite",
		Name:  t.resolve(".data", filepath.FromSlash(rel)),
	}
	return &SQLite{Database: &Database{
		tenant:       t,
		requestScope: requestScope,
		config:       &database.Config{},
		preset:       preset,
	}}
}

// appSqliteFor is like sqliteFor but resolves under the IDENTITY-level .data/ (apps/<identity>/.data),
// shared by every domain of the app — the scheduler uses it so one app has ONE scheduler.db and its
// domains coordinate through it (SQLite's UNIQUE constraint dedups slots across their connections).
func appSqliteFor(t *Tenant, rel string) *SQLite {
	rel = sqliteRel(rel)
	preset := &database.Config{
		Alias: "sqlite:app:" + rel,
		Type:  "sqlite",
		Name:  t.resolveApp(".data", filepath.FromSlash(rel)),
	}
	return &SQLite{Database: &Database{tenant: t, config: &database.Config{}, preset: preset}}
}

// sqliteRel normalises a user-supplied database name to a safe path RELATIVE to .data/. Anything that
// tries to escape (.. segments, absolute paths, drive letters) is flattened to its base name — the
// file always lands inside the tenant's .data/, no exceptions.
func sqliteRel(rel string) string {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return "app.db"
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, ":") {
		base := filepath.Base(clean)
		if base == "." || base == ".." || base == "/" || base == "" {
			return "app.db" // pure traversal ("..", "../..") has no usable name — fall back
		}
		return base
	}
	return clean
}
