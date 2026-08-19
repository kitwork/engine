//go:build turso

package work

import (
	"database/sql"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/id"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

// SPIKE (gated, -tags turso): a schema-aware `db.<table>` surface, sketched to feel out the API from
// the last few turns — NOT the finished feature. It deliberately stops before migrations.
//
//	// schema module (imported once, module-scope — not per request)
//	import { defineDb, kitid, text, integer, datetime } from "kitwork/db";
//	const vouchers = {
//	  id: kitid().primaryKey(),
//	  code: text().notNull().unique(),
//	  discount: integer().default(0),
//	  status: text().default("active"),
//	  created_at: datetime().defaultNow(),
//	};
//	export const db = defineDb("app.db", { vouchers });
//
//	// handler
//	db.vouchers.where("status", "=", "active").orderBy("created_at", "desc").limit(10).list();
//	db.vouchers.find("voucher_123");
//	db.vouchers.create({ code: "HD169K40", discount: 40000 });  // id/status/created_at auto-filled
//
// What the spike PROVES (the parts that were in question):
//   - db.<table> resolves via the VM's existing Proxy.OnGet — no VM changes.
//   - schema-awareness pays off: create() generates the kitid PK + applies defaults; where()/orderBy()
//     reject an unknown column instead of building an empty-identifier query that silently returns 0.
//   - the table is CREATEd once (first use, cached), not with exec("CREATE TABLE") on every request.
//
// What it deliberately does NOT do (the hard 80%, on purpose): migrations/ALTER, engine selection
// (it is turso-only here), and the string vs lambda where() only validates the string form.

// ---- column DSL: kitid()/text()/integer()/datetime() + chainable modifiers ----

type ColumnSpec struct {
	kind       string // kitid | text | integer | datetime
	primary    bool
	notNull    bool
	unique     bool
	hasDefault bool
	def        value.Value
	defaultNow bool
}

// The modifiers are variadic so the VM does NOT auto-call them as getters (a 0-in/1-out method is a
// getter here; a variadic method stays a callable). They ignore any args and return the spec to chain.
func (c *ColumnSpec) PrimaryKey(_ ...value.Value) *ColumnSpec { c.primary = true; return c }
func (c *ColumnSpec) NotNull(_ ...value.Value) *ColumnSpec    { c.notNull = true; return c }
func (c *ColumnSpec) Unique(_ ...value.Value) *ColumnSpec     { c.unique = true; return c }
func (c *ColumnSpec) DefaultNow(_ ...value.Value) *ColumnSpec { c.defaultNow = true; return c }
func (c *ColumnSpec) Default(args ...value.Value) *ColumnSpec {
	c.hasDefault = true
	if len(args) > 0 {
		c.def = args[0]
	}
	return c
}

func columnFunc(kind string) value.Value {
	return value.NewFunc(func(_ ...value.Value) value.Value {
		return value.New(&ColumnSpec{kind: kind})
	})
}

// The WHOLE DB surface lives under `database`: column builders (text/int/kitid/datetime), the local
// engine factories (turso/sqlite), and the shared world (define/entity). One import, destructure what
// you need:
//
//	import { database } from "kitwork";
//	const { turso, kitid, text, int, datetime } = database;
//	const db = turso("app.db", { vouchers });   // === database.turso("app.db", { vouchers })
//
// Putting the column DSL here (member access on `database`) also sidesteps the parser's named-import
// subpath limitation entirely — no `kitwork/db` namespace, no parser change needed.
//
// Each column builder is a 0-arg getter returning a callable Func, so `const { text } = database`
// binds `text` to a function and `text()` builds a column spec.
func (d *Database) Kitid() value.Value    { return columnFunc("kitid") }
func (d *Database) Text() value.Value     { return columnFunc("text") }
func (d *Database) Integer() value.Value  { return columnFunc("integer") }
func (d *Database) Int() value.Value      { return columnFunc("integer") } // alias of integer()
func (d *Database) Datetime() value.Value { return columnFunc("datetime") }

// Turso and Sqlite are the LOCAL per-tenant file factories. They are VARIADIC (not 0-arg getters) so
// that `const { turso } = database` yields a callable: database.turso("app.db", schema) and the
// destructured turso("app.db", schema) are the SAME call. The engine picks the backend — turso →
// tursogo, sqlite → modernc — and both reuse the .data/ + path-safety plumbing of the raw capability.
func (d *Database) Turso(args ...value.Value) value.Value {
	return newDbProxy(d.tenant, d.requestScope, "turso", args...)
}
func (d *Database) Sqlite(args ...value.Value) value.Value {
	return newDbProxy(d.tenant, d.requestScope, "sqlite", args...)
}

// ---- defineDb("app.db", { vouchers, categories }) → a Proxy whose db.<table> is schema-aware ----

type dbProxy struct {
	tenant    *Tenant
	scope     *requestscope.Scope
	engine    string // "turso" | "sqlite"
	dbName    string
	allowDrop bool // opt-in via turso("app.db", schema, { drop: true }) — enables destructive migration
	tables    map[string]map[string]*ColumnSpec
}

// newDbProxy parses turso("app.db", { …schema }, { drop: true }): args[0]=file, args[1]=schema,
// args[2]=options. `drop` is the EXPLICIT flag that lets migration rebuild the table to drop columns
// the schema removed; without it, extra columns are preserved.
func newDbProxy(tenant *Tenant, scope *requestscope.Scope, engine string, args ...value.Value) value.Value {
	p := &dbProxy{tenant: tenant, scope: scope, engine: engine, dbName: "app.db", tables: map[string]map[string]*ColumnSpec{}}
	if len(args) > 0 && args[0].K == value.String {
		p.dbName = args[0].String()
	}
	if len(args) > 1 && args[1].K == value.Map {
		for tableName, tableVal := range args[1].Map() {
			cols := map[string]*ColumnSpec{}
			if tableVal.K == value.Map {
				for colName, colVal := range tableVal.Map() {
					if spec, ok := colVal.V.(*ColumnSpec); ok {
						cols[colName] = spec
					}
				}
			}
			p.tables[tableName] = cols
		}
	}
	if len(args) > 2 && args[2].K == value.Map {
		if d, ok := args[2].Map()["drop"]; ok && d.K == value.Bool && d.N != 0 {
			p.allowDrop = true
		}
	}
	return value.Value{K: value.Proxy, V: p}
}

// OnGet is the whole point: db.vouchers routes here. A known table → a schema-aware handle; an unknown
// name → an in-band error (so a typo fails loudly, not as an empty result).
func (p *dbProxy) OnGet(key string) value.Value {
	cols, ok := p.tables[key]
	if !ok {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db: no table %q defined in this database", key)}
	}
	return value.New(&SchemaTable{tenant: p.tenant, scope: p.scope, engine: p.engine, dbName: p.dbName, allowDrop: p.allowDrop, table: key, columns: cols})
}

// OnCompare and OnInvoke complete the ProxyHandler interface (a dbProxy that only implemented OnGet
// would fail the v.V.(ProxyHandler) assertion, so nothing would resolve). db itself is not compared
// or called; a bare-property access that arrives as an invoke is routed to the table lookup.
func (p *dbProxy) OnCompare(_ string, _ value.Value) value.Value        { return value.Value{K: value.Nil} }
func (p *dbProxy) OnInvoke(method string, _ ...value.Value) value.Value { return p.OnGet(method) }

// ---- SchemaTable: the schema-aware wrapper around the ordinary query builder ----

type SchemaTable struct {
	tenant    *Tenant
	scope     *requestscope.Scope
	engine    string // "turso" | "sqlite"
	dbName    string
	allowDrop bool
	table     string
	columns   map[string]*ColumnSpec

	q       *query.Query
	failed  bool
	failMsg string
}

func (t *SchemaTable) fail(msg string) *SchemaTable { t.failed = true; t.failMsg = msg; return t }
func (t *SchemaTable) failVal() value.Value         { return value.Value{K: value.Invalid, V: t.failMsg} }

// source builds the local blueprint for this table's engine — sqlite → modernc, anything else → turso.
// Both resolve the file under .data/ with the same path-safety, so the schema layer is engine-agnostic.
func (t *SchemaTable) source() *SQLite {
	if t.engine == "sqlite" {
		return sqliteForRequest(t.tenant, t.dbName, t.scope)
	}
	return tursoForRequest(t.tenant, t.dbName, t.scope)
}

func (t *SchemaTable) builder() *query.Query {
	if t.q == nil {
		t.ensureTable()
		t.q = t.source().Table(t.table)
	}
	return t.q
}

func (t *SchemaTable) known(col string) bool { _, ok := t.columns[col]; return ok }

// ---- narrowing chain: each returns *SchemaTable ----

func (t *SchemaTable) Where(args ...value.Value) *SchemaTable {
	if t.failed {
		return t
	}
	// Validate the STRING form (where("col", ...)). The lambda form (where(u => u.col == x)) compiles
	// through the VM and is passed through unvalidated in this spike.
	if len(args) > 0 && args[0].K == value.String && !t.known(args[0].String()) {
		return t.fail(fmt.Sprintf("db: table %q has no column %q", t.table, args[0].String()))
	}
	t.builder().Where(args...)
	return t
}

func (t *SchemaTable) OrderBy(col string, dir ...string) *SchemaTable {
	if t.failed {
		return t
	}
	if !t.known(col) {
		return t.fail(fmt.Sprintf("db: table %q has no column %q", t.table, col))
	}
	t.builder().OrderBy(col, dir...)
	return t
}

func (t *SchemaTable) Sort(col string, dir ...string) *SchemaTable { return t.OrderBy(col, dir...) }

func (t *SchemaTable) Limit(n int) *SchemaTable {
	if t.failed {
		return t
	}
	t.builder().Limit(n)
	return t
}

// ---- terminals ----

func (t *SchemaTable) List(args ...value.Value) value.Value {
	if t.failed {
		return t.failVal()
	}
	return t.builder().List(args...)
}

func (t *SchemaTable) First(args ...value.Value) value.Value {
	if t.failed {
		return t.failVal()
	}
	return t.builder().First(args...)
}

// Find looks a row up by primary key — the schema knows which column that is.
func (t *SchemaTable) Find(args ...value.Value) value.Value {
	if t.failed {
		return t.failVal()
	}
	return t.builder().Find(args...)
}

// Count is COUNT(*) through the builder — not list().length, which would load every row to count it.
func (t *SchemaTable) Count(args ...value.Value) value.Value {
	if t.failed {
		return t.failVal()
	}
	return t.builder().Count(args...)
}

// Create fills what the schema promises: a generated kitid primary key, defaultNow timestamps, and
// column defaults for anything the caller omitted — then rejects any field that is not a column.
func (t *SchemaTable) Create(args ...value.Value) value.Value {
	if t.failed {
		return t.failVal()
	}
	if len(args) == 0 || args[0].K != value.Map {
		return value.Value{K: value.Invalid, V: "db.create: expects an object"}
	}
	row, badCol := fillRow(t.columns, args[0].Map())
	if badCol != "" {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db.create: table %q has no column %q", t.table, badCol)}
	}
	t.ensureTable()
	return t.source().Table(t.table).Create(value.New(row))
}

// fillRow is the shared create logic for BOTH worlds (local turso and shared entity): validate every
// provided field against the schema, then fill what was omitted — a generated kitid PK, defaultNow
// timestamps, and column defaults. Returns the offending column name if a field is not in the schema.
// (The entity world stamps `identity` separately, in Entities.Create.)
func fillRow(columns map[string]*ColumnSpec, provided map[string]value.Value) (map[string]value.Value, string) {
	row := map[string]value.Value{}
	for k, v := range provided {
		if _, ok := columns[k]; !ok {
			return nil, k
		}
		row[k] = v
	}
	for name, spec := range columns {
		if _, given := row[name]; given {
			continue
		}
		switch {
		case spec.kind == "kitid":
			row[name] = value.New(id.Entity())
		case spec.defaultNow:
			row[name] = value.New(time.Now().UTC().Format(time.RFC3339))
		case spec.hasDefault:
			row[name] = spec.def
		}
	}
	return row, ""
}

// ---- CREATE TABLE IF NOT EXISTS, once per (db file, table) ----

var ensuredTables sync.Map // key: "<abs db path>::<table>"

// ensureTable runs the migration once per (engine, file, table, SCHEMA HASH). The hash in the key is
// what makes it react to schema changes: edit the schema (add a column) and the key changes, so a
// hot-reload re-runs migrate() and ALTERs the live table — not just first boot.
func (t *SchemaTable) ensureTable() {
	key := t.engine + ":" + t.tenant.resolve(".data", t.dbName) + "::" + t.table + "::" + schemaHash(t.columns)
	if _, done := ensuredTables.Load(key); done {
		return
	}
	migrate(t.source().db(), t.table, t.columns, false, t.allowDrop)
	ensuredTables.Store(key, true)
}

// schemaDDL builds CREATE TABLE IF NOT EXISTS from the schema. `extra` prepends raw column definitions
// (the entity world passes `"identity" TEXT NOT NULL` so shared rows can be scoped).
func schemaDDL(table string, columns map[string]*ColumnSpec, extra ...string) string {
	names := make([]string, 0, len(columns))
	for n := range columns {
		names = append(names, n)
	}
	sort.Strings(names) // deterministic DDL

	defs := make([]string, 0, len(columns)+len(extra))
	defs = append(defs, extra...)
	for _, n := range names {
		defs = append(defs, columnSQL(n, columns[n]))
	}
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %q (%s)", table, strings.Join(defs, ", "))
}

func columnSQL(name string, c *ColumnSpec) string {
	sqlType := "TEXT"
	if c.kind == "integer" {
		sqlType = "INTEGER"
	}
	parts := []string{fmt.Sprintf("%q %s", name, sqlType)}
	if c.primary {
		parts = append(parts, "PRIMARY KEY")
	}
	if c.notNull {
		parts = append(parts, "NOT NULL")
	}
	if c.unique {
		parts = append(parts, "UNIQUE")
	}
	// Defaults are applied in Create() (kitid/defaultNow need engine involvement anyway), so the DDL
	// carries no DEFAULT clause in this spike.
	return strings.Join(parts, " ")
}

// ============================================================================
// database.define(alias, { … }) → the SHARED world: schema-aware tables that
// ride on database.entity(), so rows are identity-scoped across tenants.
// This is where schema meets the multi-tenant sharing story: one physical table,
// each app sees only its own rows, and the schema fills kitid/defaults on top.
// ============================================================================

// Define hangs off the `database` capability (import { database } from "kitwork"). The first arg is an
// ALIAS in spirit (the entity scope binds to database.System either way in this sketch); the second is
// the schema. Returns a Proxy whose db.<table> is an identity-scoped, schema-aware handle.
func (d *Database) Define(args ...value.Value) value.Value {
	return newEntityProxy(d.tenant, d.requestScope, args...)
}

type entityProxy struct {
	tenant *Tenant
	scope  *requestscope.Scope
	tables map[string]map[string]*ColumnSpec
}

func newEntityProxy(tenant *Tenant, scope *requestscope.Scope, args ...value.Value) value.Value {
	p := &entityProxy{tenant: tenant, scope: scope, tables: map[string]map[string]*ColumnSpec{}}
	// Accept both define("alias", { schema }) and define({ schema }).
	schemaArg := len(args) - 1
	if schemaArg >= 0 && args[schemaArg].K == value.Map {
		for tableName, tableVal := range args[schemaArg].Map() {
			cols := map[string]*ColumnSpec{}
			if tableVal.K == value.Map {
				for colName, colVal := range tableVal.Map() {
					if spec, ok := colVal.V.(*ColumnSpec); ok {
						cols[colName] = spec
					}
				}
			}
			p.tables[tableName] = cols
		}
	}
	return value.Value{K: value.Proxy, V: p}
}

func (p *entityProxy) OnGet(key string) value.Value {
	cols, ok := p.tables[key]
	if !ok {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db: no table %q defined in this database", key)}
	}
	return value.New(&EntityTable{tenant: p.tenant, scope: p.scope, table: key, columns: cols})
}

func (p *entityProxy) OnCompare(_ string, _ value.Value) value.Value {
	return value.Value{K: value.Nil}
}
func (p *entityProxy) OnInvoke(method string, _ ...value.Value) value.Value { return p.OnGet(method) }

// EntityTable is the shared-world twin of SchemaTable. It wraps *Entities (database.entity().table),
// so every read is auto-scoped `WHERE identity = <this app>` and every create stamps the identity —
// schema-awareness (kitid PK, defaults, column validation) rides on top of that, reusing fillRow.
type EntityTable struct {
	tenant  *Tenant
	scope   *requestscope.Scope
	table   string
	columns map[string]*ColumnSpec

	e       *Entities
	failed  bool
	failMsg string
}

func (t *EntityTable) fail(msg string) *EntityTable { t.failed = true; t.failMsg = msg; return t }
func (t *EntityTable) failVal() value.Value         { return value.Value{K: value.Invalid, V: t.failMsg} }
func (t *EntityTable) known(col string) bool        { _, ok := t.columns[col]; return ok }

func (t *EntityTable) builder() *Entities {
	if t.e == nil {
		t.ensureTable()
		t.e = (&Database{tenant: t.tenant, requestScope: t.scope}).Entity().Table(t.table)
	}
	return t.e
}

func (t *EntityTable) Where(args ...value.Value) *EntityTable {
	if t.failed {
		return t
	}
	if len(args) > 0 && args[0].K == value.String && !t.known(args[0].String()) {
		return t.fail(fmt.Sprintf("db: table %q has no column %q", t.table, args[0].String()))
	}
	t.e = t.builder().Where(args...)
	return t
}

func (t *EntityTable) OrderBy(col string, dir ...string) *EntityTable {
	if t.failed {
		return t
	}
	if !t.known(col) {
		return t.fail(fmt.Sprintf("db: table %q has no column %q", t.table, col))
	}
	t.e = t.builder().OrderBy(col, dir...)
	return t
}

func (t *EntityTable) Sort(col string, dir ...string) *EntityTable { return t.OrderBy(col, dir...) }

func (t *EntityTable) Limit(n int) *EntityTable {
	if t.failed {
		return t
	}
	t.e = t.builder().Limit(n)
	return t
}

func (t *EntityTable) List(args ...value.Value) value.Value {
	if t.failed {
		return t.failVal()
	}
	return t.builder().List(args...)
}

func (t *EntityTable) First(args ...value.Value) value.Value {
	if t.failed {
		return t.failVal()
	}
	return t.builder().First(args...)
}

func (t *EntityTable) Find(args ...value.Value) value.Value {
	if t.failed {
		return t.failVal()
	}
	return t.builder().Find(args...)
}

func (t *EntityTable) Count(args ...value.Value) value.Value {
	if t.failed {
		return t.failVal()
	}
	return t.builder().Count(args...)
}

func (t *EntityTable) Create(args ...value.Value) value.Value {
	if t.failed {
		return t.failVal()
	}
	if len(args) == 0 || args[0].K != value.Map {
		return value.Value{K: value.Invalid, V: "db.create: expects an object"}
	}
	row, badCol := fillRow(t.columns, args[0].Map())
	if badCol != "" {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db.create: table %q has no column %q", t.table, badCol)}
	}
	// Entities.Create stamps the `identity` column itself — we never set it here.
	return t.builder().Create(value.New(row))
}

// ensureTable migrates the shared table on database.System, with the extra `identity` column.
func (t *EntityTable) ensureTable() {
	if database.System == nil {
		return
	}
	key := fmt.Sprintf("system:%p:%s:%s", database.System, t.table, schemaHash(t.columns))
	if _, done := ensuredTables.Load(key); done {
		return
	}
	migrate(database.System, t.table, t.columns, true, false) // shared world: no destructive drop in the spike
	ensuredTables.Store(key, true)
}

// ============================================================================
// Migration engine: introspect → diff → apply-safe → record. Real migration,
// not blind CREATE IF NOT EXISTS.
//   - table absent                → CREATE (full DDL, constraints and all)
//   - column in schema, not in DB → ALTER TABLE ADD COLUMN (additive, nullable)
//   - column type changed         → REBUILD the table (SQLite can't ALTER a type), CASTing the data
//                                    across, inside a transaction
//   - column in DB, not in schema → LEFT INTACT + warning by default; DROPPED (via rebuild) only when
//                                    the caller opts in with turso(..., { drop: true })
// Every change is logged to _kitwork_migrations; re-runs when the schema hash changes.
// Both engines share the SQLite file format, so this works on turso and modernc alike. Indexes /
// triggers / foreign keys OUTSIDE the schema DSL are not recreated by the rebuild — the DSL declares
// none; a production version would scan sqlite_master and rebuild them too.
// ============================================================================

const migrationsTable = "_kitwork_migrations"

func migrate(db *sql.DB, table string, columns map[string]*ColumnSpec, withIdentity, allowDrop bool) {
	if db == nil {
		return
	}
	ensureHistory(db)

	current, err := tableColumns(db, table)
	if err != nil {
		fmt.Printf("[db.migrate] introspect %q failed: %v\n", table, err)
		return
	}

	// New table → straight CREATE.
	if len(current) == 0 {
		var extra []string
		if withIdentity {
			extra = append(extra, fmt.Sprintf("%q TEXT NOT NULL", identityColumn))
		}
		ddl := schemaDDL(table, columns, extra...)
		if _, err := db.Exec(ddl); err != nil {
			fmt.Printf("[db.migrate] create %q failed: %v\n", table, err)
			return
		}
		recordMigration(db, table, "create-table", ddl)
		return
	}

	// Classify the delta between the schema and the live table.
	var newCols, typeChanged, extras []string
	for _, name := range sortedColumns(columns) {
		curType, ok := current[name]
		switch {
		case !ok:
			newCols = append(newCols, name)
		case !sameType(curType, desiredType(columns[name])):
			typeChanged = append(typeChanged, name)
		}
	}
	for col := range current {
		if _, want := columns[col]; want {
			continue
		}
		if withIdentity && col == identityColumn {
			continue
		}
		extras = append(extras, col)
	}
	sort.Strings(extras)

	// A type change (SQLite cannot ALTER a type) OR an explicit drop of extras → full table rebuild.
	if len(typeChanged) > 0 || (allowDrop && len(extras) > 0) {
		rebuild(db, table, columns, current, withIdentity, allowDrop, extras, typeChanged)
		return
	}

	// Otherwise purely additive: ADD the new columns (nullable), leave extras intact.
	for _, name := range newCols {
		stmt := fmt.Sprintf("ALTER TABLE %q ADD COLUMN %s", table, addColumnSQL(name, columns[name]))
		if _, err := db.Exec(stmt); err != nil {
			fmt.Printf("[db.migrate] add %q.%q failed: %v\n", table, name, err)
			continue
		}
		recordMigration(db, table, "add-column", stmt)
	}
	for _, col := range extras {
		fmt.Printf("[db.migrate] %q.%q is in the database but not the schema — left intact (pass { drop: true } to remove)\n", table, col)
	}
}

// rebuild is the SQLite table-rebuild ("12-step"): CREATE a new table with the desired schema, COPY
// the data across (CASTing changed types), DROP the old, RENAME the new — all inside a transaction, so
// a failure rolls back to the untouched original. With allowDrop=false, extra columns (in the DB, not
// the schema) are PRESERVED in the new table; with allowDrop=true they are dropped.
func rebuild(db *sql.DB, table string, columns map[string]*ColumnSpec, current map[string]string,
	withIdentity, allowDrop bool, extras, typeChanged []string) {

	tmp := table + "__kwrebuild"
	var defs, intoCols, selectExprs []string

	if withIdentity {
		defs = append(defs, fmt.Sprintf("%q TEXT NOT NULL", identityColumn))
		if _, ok := current[identityColumn]; ok {
			intoCols = append(intoCols, fmt.Sprintf("%q", identityColumn))
			selectExprs = append(selectExprs, fmt.Sprintf("%q", identityColumn))
		}
	}
	for _, name := range sortedColumns(columns) {
		defs = append(defs, columnSQL(name, columns[name]))
		if curType, ok := current[name]; ok {
			intoCols = append(intoCols, fmt.Sprintf("%q", name))
			selectExprs = append(selectExprs, castExpr(name, desiredType(columns[name]), curType))
		}
		// A brand-new column (not in current) is created but not copied → NULL for existing rows.
	}
	if !allowDrop {
		for _, ex := range extras {
			defs = append(defs, fmt.Sprintf("%q %s", ex, current[ex]))
			intoCols = append(intoCols, fmt.Sprintf("%q", ex))
			selectExprs = append(selectExprs, fmt.Sprintf("%q", ex))
		}
	}

	tx, err := db.Begin()
	if err != nil {
		fmt.Printf("[db.migrate] rebuild %q: begin: %v\n", table, err)
		return
	}
	fail := func(stage string, e error) {
		tx.Rollback()
		fmt.Printf("[db.migrate] rebuild %q rolled back at %s: %v\n", table, stage, e)
	}
	createDDL := fmt.Sprintf("CREATE TABLE %q (%s)", tmp, strings.Join(defs, ", "))
	if _, err := tx.Exec(createDDL); err != nil {
		fail("create", err)
		return
	}
	if len(intoCols) > 0 {
		copyStmt := fmt.Sprintf("INSERT INTO %q (%s) SELECT %s FROM %q",
			tmp, strings.Join(intoCols, ", "), strings.Join(selectExprs, ", "), table)
		if _, err := tx.Exec(copyStmt); err != nil {
			fail("copy", err)
			return
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("DROP TABLE %q", table)); err != nil {
		fail("drop-old", err)
		return
	}
	if _, err := tx.Exec(fmt.Sprintf("ALTER TABLE %q RENAME TO %q", tmp, table)); err != nil {
		fail("rename", err)
		return
	}
	if err := tx.Commit(); err != nil {
		fmt.Printf("[db.migrate] rebuild %q: commit: %v\n", table, err)
		return
	}

	action := "rebuild-table"
	if len(typeChanged) > 0 {
		action += " retype[" + strings.Join(typeChanged, ",") + "]"
	}
	if allowDrop && len(extras) > 0 {
		action += " drop[" + strings.Join(extras, ",") + "]"
	}
	recordMigration(db, table, action, createDDL)
}

// castExpr copies a column, wrapping it in CAST when the target type differs (a type change).
func castExpr(name, want, cur string) string {
	if sameType(cur, want) {
		return fmt.Sprintf("%q", name)
	}
	return fmt.Sprintf("CAST(%q AS %s)", name, want)
}

func desiredType(c *ColumnSpec) string {
	if c.kind == "integer" {
		return "INTEGER"
	}
	return "TEXT"
}

func sameType(a, b string) bool { return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) }

// tableColumns introspects the live table → column name to declared type (uppercased). PRAGMA
// table_info works on both engines (SQLite file format).
func tableColumns(db *sql.DB, table string) (map[string]string, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := map[string]string{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[name] = strings.ToUpper(strings.TrimSpace(ctype))
	}
	return cols, rows.Err()
}

// addColumnSQL is a NAKED column def (name + type only): SQLite's ALTER TABLE ADD COLUMN cannot carry
// PRIMARY KEY / UNIQUE, and NOT NULL needs a default. The added column is nullable — a schema-level
// default still applies to new rows via fillRow.
func addColumnSQL(name string, c *ColumnSpec) string {
	sqlType := "TEXT"
	if c.kind == "integer" {
		sqlType = "INTEGER"
	}
	return fmt.Sprintf("%q %s", name, sqlType)
}

func ensureHistory(db *sql.DB) {
	db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q (
		id TEXT PRIMARY KEY, table_name TEXT, action TEXT, statement TEXT, applied_at TEXT)`, migrationsTable))
}

func recordMigration(db *sql.DB, table, action, statement string) {
	db.Exec(
		fmt.Sprintf("INSERT INTO %q (id, table_name, action, statement, applied_at) VALUES (?, ?, ?, ?, ?)", migrationsTable),
		id.Entity(), table, action, statement, time.Now().UTC().Format(time.RFC3339),
	)
}

func sortedColumns(columns map[string]*ColumnSpec) []string {
	names := make([]string, 0, len(columns))
	for n := range columns {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// schemaHash is a deterministic fingerprint of the schema shape — the migration cache key, so an
// edited schema re-runs migrate().
func schemaHash(columns map[string]*ColumnSpec) string {
	h := fnv.New64a()
	for _, name := range sortedColumns(columns) {
		c := columns[name]
		fmt.Fprintf(h, "%s|%s|%t|%t|%t|%t;", name, c.kind, c.primary, c.notNull, c.unique, c.defaultNow)
	}
	return fmt.Sprintf("%x", h.Sum64())
}
