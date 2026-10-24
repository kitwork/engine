package work

import (
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/database"
	requestscope "github.com/kitwork/engine/request"
)

// SQLite is one SQLite file the engine opens for its own tables: a site's file under .data/ (the SQLite
// backend of a schema'd database, SchemaTable), the app's scheduler.db (appSqliteFor), or the host's file
// of connection("tên") (hostSQLite). It is a BLUEPRINT, not a connection: nothing touches the disk until
// the first query (Database's preset path opens lazily and caches the handle in the app's pool), and the
// ENGINE owns the connection lifecycle.
//
// App code no longer reaches it: import { sqlite } from "kitwork" (sqlite.table/open/memory/exec, and the
// inspector's readQuery/readCatalog/readTablePage) was removed on 22/10/2026. SQLite is connection("tên")
// with { sqlite: path } in the host's app.databases (KITWORK_DATABASE.md mục 1).
//
// A site's file lives under its .data/ folder BY CONSTRUCTION: the static server refuses any dot
// segment (tree_serve), so a site's database can never be downloaded over HTTP.
type SQLite struct {
	*Database
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
		preset:       preset,
	}}
}

// hostSQLite is the host's SQLite file of connection("tên"): app.databases({ tên: { sqlite: path } }) in
// application.work, the path read from the manifest's folder. The host chose it; it may lie outside every
// app, and every app that names it shares it.
func hostSQLite(t *Tenant, name, path string, requestScope *requestscope.Scope) *SQLite {
	return &SQLite{Database: &Database{
		tenant:       t,
		requestScope: requestScope,
		preset:       hostSQLiteConfig(name, path),
	}}
}

// hostSQLiteMaxIdle is how many connections the pool of a SQLite file of the host's catalogue keeps between
// statements: none kept (the config's zero) would open the file, read its schema and checkpoint its WAL
// again for every statement, a write of the runs on it (queue.database, 24/10/2026) included.
const hostSQLiteMaxIdle = 2

// hostSQLiteConfig is the pool config of the host's SQLite file of connection("tên"), the same whichever of
// the app's statements and the runs (catalogRuns) opens the pool first.
func hostSQLiteConfig(name, path string) *database.Config {
	return &database.Config{
		Alias:   "sqlite:" + name,
		Type:    "sqlite",
		Name:    path,
		MaxIdle: hostSQLiteMaxIdle,
	}
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
	return &SQLite{Database: &Database{tenant: t, preset: preset}}
}

// sqliteRel normalises a database name to a safe path RELATIVE to .data/. Anything that tries to escape
// (.. segments, absolute paths, drive letters) is flattened to its base name — the file always lands
// inside the tenant's .data/, no exceptions.
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
