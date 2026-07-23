package work

import (
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/database"
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

// Sqlite is what `import { sqlite } from "kitwork"` resolves to (0-arg getter, auto-called): the
// tenant's default database at .data/app.db.
func (w *KitWork) Sqlite() *SQLite {
	return sqliteFor(w.tenant, "app.db")
}

// Open names another database file inside the tenant's .data/ folder — a blueprint, zero I/O.
// Subfolders are fine ("archive/2026.db"); traversal is not (see sqliteRel).
func (s *SQLite) Open(path string) *SQLite {
	return sqliteFor(s.tenant, path)
}

// Memory returns the tenant's in-memory database (:memory:, one shared connection) — for tests and
// scratch work. It vanishes with the process.
func (s *SQLite) Memory() *SQLite {
	preset := &database.Config{Alias: "sqlite::memory:", Type: "sqlite", Name: ":memory:"}
	return &SQLite{Database: &Database{tenant: s.tenant, config: &database.Config{}, preset: preset}}
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

// sqliteFor builds the blueprint for one tenant database file: resolve the path under .data/,
// pin the connection config (alias "sqlite:<rel>" keys the cache in tenant.databases).
func sqliteFor(t *Tenant, rel string) *SQLite {
	rel = sqliteRel(rel)
	preset := &database.Config{
		Alias: "sqlite:" + rel,
		Type:  "sqlite",
		Name:  t.resolve(".data", filepath.FromSlash(rel)),
	}
	return &SQLite{Database: &Database{tenant: t, config: &database.Config{}, preset: preset}}
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
