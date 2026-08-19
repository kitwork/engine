//go:build turso

package work

import (
	"path/filepath"

	"github.com/kitwork/engine/database"
	requestscope "github.com/kitwork/engine/request"
)

// TursoDB is what `import { turso } from "kitwork"` resolves to — but ONLY in a `-tags turso` build.
//
// It is the SAME per-tenant embedded-database capability as `sqlite` (see db.sqlite.go): same .data/
// blueprint, same path-safety (sqliteRel), same query builder, same HTTP-undownloadable guarantee.
// The one difference is the engine: Type "turso" routes Database.Connect → Config.Connect →
// sql.Open("turso", …) onto the Turso Database engine (tursogo) instead of modernc.
//
//	turso.exec("CREATE TABLE …")                       // DDL
//	turso.table("users").create({ name: "An" })        // INSERT … RETURNING (tursogo speaks it)
//	turso.open("analytics.db").table("events").find()  // a named file, STILL on turso
//
// Why a wrapper and not a bare *SQLite: exec/table/where/… promote from the embedded *SQLite so they
// keep working, but open()/memory() are OVERRIDDEN here. The embedded SQLite.Open/Memory hardcode
// Type "sqlite" — inheriting them would make turso.open("x.db") silently fall back to modernc. These
// overrides keep every entry point on the turso engine.
//
// Opt-in DEMO surface, not the default: a tag-free build has no `turso` import and no tursogo linked.
type TursoDB struct {
	*SQLite
}

// Turso resolves `import { turso }` to the tenant's default database at .data/app.db, on tursogo.
func (w *KitWork) Turso() *TursoDB {
	return &TursoDB{tursoForRequest(w.tenant, "app.db", w.requestScope)}
}

// Open names another database file inside the tenant's .data/ folder — a blueprint, zero I/O —
// STAYING on the turso engine (unlike the inherited SQLite.Open, which would revert to modernc).
func (d *TursoDB) Open(path string) *TursoDB {
	return &TursoDB{tursoForRequest(d.tenant, path, d.requestScope)}
}

// Memory returns the tenant's in-memory turso database (:memory:, one shared connection) — for tests
// and scratch work. It overrides SQLite.Memory so the engine stays turso, not sqlite.
func (d *TursoDB) Memory() *TursoDB {
	preset := &database.Config{Alias: "turso::memory:", Type: "turso", Name: ":memory:"}
	return &TursoDB{&SQLite{Database: &Database{
		tenant:       d.tenant,
		requestScope: d.requestScope,
		config:       &database.Config{},
		preset:       preset,
	}}}
}

// tursoForRequest mirrors sqliteForRequest exactly, except the preset Type is "turso". The file still
// lands under the tenant's .data/ folder, so it inherits the same anti-traversal and no-HTTP-download
// guarantees; the SQLite file format is shared, so the on-disk .db is the same shape modernc writes.
func tursoForRequest(t *Tenant, rel string, requestScope *requestscope.Scope) *SQLite {
	rel = sqliteRel(rel)
	preset := &database.Config{
		Alias: "turso:" + rel,
		Type:  "turso",
		Name:  t.resolve(".data", filepath.FromSlash(rel)),
	}
	return &SQLite{Database: &Database{
		tenant:       t,
		requestScope: requestScope,
		config:       &database.Config{},
		preset:       preset,
	}}
}
