package work

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/id"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

// Schema-aware `db.<table>` surface under the `database` capability: a schema maps table names to
// column specs; the engine auto-migrates the table to match and hands back a validated query builder.
//
//	import { database } from "kitwork";
//	const { turso, sqlite, kitid, text, int, datetime } = database;
//	const vouchers = {
//	  id: kitid().primaryKey(),
//	  code: text().notNull().unique(),
//	  discount: int().default(0),
//	  status: text().default("active"),
//	  created_at: datetime().defaultNow(),
//	};
//	export const db = turso("app.db", { vouchers });          // or sqlite("app.db", { vouchers })
//
//	// handler
//	db.vouchers.where("status", "=", "active").orderBy("created_at", "desc").limit(10).list();
//	db.vouchers.find("voucher_123");
//	db.vouchers.create({ code: "HD169K40", discount: 40000 });  // id/status/created_at auto-filled
//
// How it works:
//   - db.<table> resolves via the VM's Proxy.OnGet — no VM changes.
//   - create() generates the kitid PK + applies defaults; where()/orderBy() validate columns.
//   - the table is migrated on first use, cached by schema hash — not re-checked per request.
//   - database.turso → tursogo, database.sqlite → modernc; database.define(alias) → entity-scoped
//     shared tables (rows partitioned by identity).
//
// Migration (see migrate() below): additive ADD COLUMN with default backfill runs automatically; type
// changes and column drops are DESTRUCTIVE (rebuild + CAST / drop) and run ONLY with { drop: true }.
// Known limits: the rebuild does not restore indexes/triggers/FKs (the DSL declares none), and the
// shared (entity) migration is not yet coordinated across nodes.

// ---- column DSL: kitid()/text()/integer()/datetime() + chainable modifiers ----

type ColumnSpec struct {
	kind       string // see colStorage below for the full set
	primary    bool
	notNull    bool
	unique     bool
	hasDefault bool
	def        value.Value
	defaultNow bool
	touch      bool     // touchOnUpdate: re-stamp the current time on every update() (updated_at)
	enumVals   []string // for kind == "enum": the allowed values
}

// The modifiers are variadic so the VM does NOT auto-call them as getters (a 0-in/1-out method is a
// getter here; a variadic method stays a callable). They ignore any args and return the spec to chain.
func (c *ColumnSpec) PrimaryKey(_ ...value.Value) *ColumnSpec { c.primary = true; return c }
func (c *ColumnSpec) NotNull(_ ...value.Value) *ColumnSpec    { c.notNull = true; return c }
func (c *ColumnSpec) Unique(_ ...value.Value) *ColumnSpec     { c.unique = true; return c }
func (c *ColumnSpec) DefaultNow(_ ...value.Value) *ColumnSpec { c.defaultNow = true; return c }

// OnUpdate marks an "updated_at" column: it is re-stamped with the current time on EVERY update()
// (unless the caller passes the column explicitly). Pair it with now() — now().onUpdate() stamps on
// both insert (defaultNow) and update (touch); datetime().onUpdate() stamps on update only.
func (c *ColumnSpec) OnUpdate(_ ...value.Value) *ColumnSpec { c.touch = true; return c }
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
// Identifiers
func (d *Database) Kitid() value.Value  { return columnFunc("kitid") }
func (d *Database) Uuid() value.Value   { return columnFunc("uuid") }
func (d *Database) Serial() value.Value { return columnFunc("serial") } // INTEGER; auto-increment not wired yet

// Text
func (d *Database) Text() value.Value    { return columnFunc("text") }
func (d *Database) Varchar() value.Value { return paramFunc("varchar") } // length is metadata (no-op on sqlite)
func (d *Database) Char() value.Value    { return paramFunc("char") }

// Numbers
func (d *Database) Integer() value.Value { return columnFunc("integer") }
func (d *Database) Int() value.Value     { return columnFunc("integer") } // alias
func (d *Database) Float() value.Value   { return columnFunc("float") }
func (d *Database) Real() value.Value    { return columnFunc("float") }  // alias
func (d *Database) Double() value.Value  { return columnFunc("float") }  // doublePrecision alias
func (d *Database) Decimal() value.Value { return paramFunc("decimal") } // stored as TEXT (exact), read as number

// Boolean
func (d *Database) Bool() value.Value    { return columnFunc("bool") }
func (d *Database) Boolean() value.Value { return columnFunc("bool") } // alias

// Enum — variadic, values passed straight: enum("draft", "published")
func (d *Database) Enum() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		c := &ColumnSpec{kind: "enum"}
		for _, a := range args {
			if a.K == value.String {
				c.enumVals = append(c.enumVals, a.String())
			}
		}
		return value.New(c)
	})
}

// Time — datetime/date/time are MANUAL; now() is the auto-timestamp preset.
func (d *Database) Datetime() value.Value { return columnFunc("datetime") }
func (d *Database) Date() value.Value     { return columnFunc("date") }
func (d *Database) Time() value.Value     { return columnFunc("time") }
func (d *Database) Year() value.Value     { return columnFunc("year") }  // INTEGER, auto current year on insert
func (d *Database) Month() value.Value    { return columnFunc("month") } // INTEGER, auto current month
func (d *Database) Day() value.Value      { return columnFunc("day") }   // INTEGER, auto current day

// Structured
func (d *Database) Json() value.Value  { return columnFunc("json") }
func (d *Database) Jsonb() value.Value { return columnFunc("jsonb") } // ≡ json on sqlite (both TEXT)
func (d *Database) Array() value.Value { return columnFunc("array") } // JSON array in TEXT
func (d *Database) Blob() value.Value  { return columnFunc("blob") }
func (d *Database) Bytes() value.Value { return columnFunc("blob") } // alias

// Networking & AI
func (d *Database) Ip() value.Value     { return columnFunc("ip") }  // TEXT (no native type)
func (d *Database) Mac() value.Value    { return columnFunc("mac") } // TEXT
func (d *Database) Vector() value.Value { return paramFunc("vector") }

// Presets — the two universal patterns get a one-word name; compose the rest with modifiers.
func (d *Database) Id() value.Value { // = kitid().primaryKey()
	return value.NewFunc(func(_ ...value.Value) value.Value {
		return value.New(&ColumnSpec{kind: "kitid", primary: true})
	})
}
func (d *Database) Now() value.Value { // = datetime().defaultNow()
	return value.NewFunc(func(_ ...value.Value) value.Value {
		return value.New(&ColumnSpec{kind: "datetime", defaultNow: true})
	})
}

// paramFunc builds a column whose first arg is a size/precision/dimension hint (varchar(50),
// decimal(10,2), vector(1536)). The hint is metadata for v1 — sqlite ignores length/precision, and the
// vector is stored as a JSON array — but it is accepted so schemas read the same across engines.
func paramFunc(kind string) value.Value {
	return value.NewFunc(func(_ ...value.Value) value.Value {
		return value.New(&ColumnSpec{kind: kind})
	})
}

// ---- storage classes + coercion: the type registry ----

// colStorage maps each column kind to one of SQLite's 5 storage classes. Everything a schema declares
// resolves to TEXT / INTEGER / REAL / BLOB; the semantic types (bool, json, enum, uuid, …) ride on top
// via coercion (coerceWrite / coerceRead). Unknown kinds default to TEXT.
var colStorage = map[string]string{
	"text": "TEXT", "varchar": "TEXT", "char": "TEXT", "kitid": "TEXT", "uuid": "TEXT",
	"datetime": "TEXT", "date": "TEXT", "time": "TEXT", "decimal": "TEXT", "enum": "TEXT",
	"json": "TEXT", "jsonb": "TEXT", "array": "TEXT", "vector": "TEXT", "ip": "TEXT", "mac": "TEXT",
	"integer": "INTEGER", "bool": "INTEGER", "serial": "INTEGER", "year": "INTEGER", "month": "INTEGER", "day": "INTEGER",
	"float": "REAL",
	"blob":  "BLOB",
}

func storageClass(kind string) string {
	if s, ok := colStorage[kind]; ok {
		return s
	}
	return "TEXT"
}

// coerceWrite converts a JS value to its stored form: bool → 0/1, json/array/vector → a JSON string,
// decimal → an exact TEXT string. Everything else passes through.
func coerceWrite(kind string, v value.Value) value.Value {
	switch kind {
	case "bool":
		if v.K == value.Bool {
			if v.N != 0 {
				return value.New(1)
			}
			return value.New(0)
		}
	case "json", "jsonb", "array", "vector":
		if v.K == value.Map || v.K == value.Array {
			if b, err := json.Marshal(v); err == nil {
				return value.New(string(b))
			}
		}
	case "decimal":
		if v.K == value.Number {
			return value.New(numText(v.N)) // exact-as-authored text; pass a string for full precision
		}
	}
	return v
}

// coerceRead converts a stored value back to its JS form (the inverse of coerceWrite), so callers get
// booleans, objects and numbers — not 0/1 and JSON strings.
func coerceRead(kind string, v value.Value) value.Value {
	switch kind {
	case "bool":
		if v.K == value.Number {
			return value.New(v.N != 0)
		}
	case "json", "jsonb", "array", "vector":
		if v.K == value.String {
			var out value.Value
			if err := json.Unmarshal([]byte(v.String()), &out); err == nil {
				return out
			}
		}
	case "decimal":
		if v.K == value.String {
			if f, err := strconv.ParseFloat(v.String(), 64); err == nil {
				return value.New(f)
			}
		}
	}
	return v
}

func numText(n float64) string {
	if n == float64(int64(n)) {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'g', -1, 64)
}

func inEnum(spec *ColumnSpec, s string) bool {
	for _, e := range spec.enumVals {
		if e == s {
			return true
		}
	}
	return false
}

func uuidV4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// autoValue returns the value a column generates on insert when the caller omits it: a kitid/uuid, the
// current timestamp (now/defaultNow), the current year/month/day, or a static default.
func autoValue(spec *ColumnSpec) (value.Value, bool) {
	nowT := time.Now().UTC()
	switch {
	case spec.kind == "kitid":
		return value.New(id.Entity()), true
	case spec.kind == "uuid":
		return value.New(uuidV4()), true
	case spec.defaultNow:
		return value.New(nowT.Format(time.RFC3339)), true
	case spec.kind == "year":
		return value.New(nowT.Year()), true
	case spec.kind == "month":
		return value.New(int(nowT.Month())), true
	case spec.kind == "day":
		return value.New(nowT.Day()), true
	case spec.hasDefault:
		return spec.def, true
	}
	return value.Value{}, false
}

// coerceResult maps a query result (an array of row maps, or one row map) back to JS types per the
// schema, so reads return booleans/objects/numbers rather than 0/1 and JSON strings.
func coerceResult(columns map[string]*ColumnSpec, result value.Value) value.Value {
	switch result.K {
	case value.Array:
		if arr, ok := result.V.(*[]value.Value); ok {
			for i := range *arr {
				coerceRowInPlace(columns, (*arr)[i])
			}
		}
	case value.Map:
		coerceRowInPlace(columns, result)
	}
	return result
}

func coerceRowInPlace(columns map[string]*ColumnSpec, row value.Value) {
	if row.K != value.Map {
		return
	}
	m := row.Map()
	for name, spec := range columns {
		if v, ok := m[name]; ok {
			m[name] = coerceRead(spec.kind, v)
		}
	}
}

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
// args[2]=options. `drop` is the EXPLICIT opt-in for DESTRUCTIVE migration — dropping columns the
// schema removed AND rebuilding for type changes (CAST may lose data). Without it, both are refused
// and the data is left untouched.
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
		opts := args[2].Map()
		if d, ok := opts["drop"]; ok && d.K == value.Bool && d.N != 0 {
			p.allowDrop = true
		}
		// serve: a `token` in the options declares THIS db reachable over libSQL/HTTP (the value stays
		// in env — token: env.require("DB_TOKEN") — but the DECISION to expose is visible here in JS, not
		// hidden in .env). `access` defaults to "readonly"; only "readwrite" grants writes.
		if tok, ok := opts["token"]; ok && !tok.IsNil() && tok.String() != "" {
			access := ""
			if a, ok := opts["access"]; ok && a.K == value.String {
				access = a.String()
			}
			registerServe(tenant, p.dbName, tok.String(), access)
		}
	}
	registerSchema(p)
	return value.Value{K: value.Proxy, V: p}
}

// registerSchema records a declared schema so a preflight (kitwork check) can preview its migration
// plan without a live request. Keyed by tenant + engine + file, so redeclaring the same db dedupes.
var (
	schemaRegMu sync.Mutex
	schemaReg   = map[string]*dbProxy{}
)

func registerSchema(p *dbProxy) {
	if p == nil || p.tenant == nil {
		return
	}
	key := p.tenant.appID() + "|" + p.tenant.Domain() + "|" + p.engine + "|" + p.dbName
	schemaRegMu.Lock()
	schemaReg[key] = p
	schemaRegMu.Unlock()
}

// ---- serve registry: which tenant dbs are exposed over libSQL/HTTP, and how ----
//
// Populated by turso("db", {schema}, { token, access }) at declare time (which runs during Tenant.Run,
// so the endpoint is live before the first request). The libSQL and /_db handlers consult THIS instead
// of reading env.DB_TOKEN — exposure is a declared fact, not the side effect of an env var existing.

type serveConfig struct {
	token  string
	access string // "readonly" | "readwrite"
}

var (
	serveRegMu sync.Mutex
	serveReg   = map[string]serveConfig{} // "appID|domain|dbName" -> config (engine-agnostic)
)

// tenantScopeKey identifies a tenant uniquely. config.base is its resolved directory (root/identity/
// domain), so two tenants never collide — including many test tenants that all use domain "localhost".
func tenantScopeKey(t *Tenant) string {
	if t.config != nil && t.config.base != "" {
		return t.config.base
	}
	return t.appID() + "|" + t.Domain()
}

func serveDBKey(t *Tenant, dbName string) string {
	return tenantScopeKey(t) + "|" + dbName
}

// registerServe records an exposed db. Access defaults to the SAFE option: only an explicit
// "readwrite" grants writes; anything else (including empty) is read-only.
func registerServe(t *Tenant, dbName, token, access string) {
	if t == nil || token == "" {
		return
	}
	if strings.ToLower(strings.TrimSpace(access)) != "readwrite" {
		access = "readonly"
	} else {
		access = "readwrite"
	}
	serveRegMu.Lock()
	serveReg[serveDBKey(t, dbName)] = serveConfig{token: token, access: access}
	serveRegMu.Unlock()
}

// resolveServe finds the exposure for (tenant, dbName). An empty dbName resolves to the tenant's single
// exposed db when there is exactly one (so a bare URL works); ambiguous → not served.
func resolveServe(t *Tenant, dbName string) (string, serveConfig, bool) {
	if t == nil {
		return "", serveConfig{}, false
	}
	serveRegMu.Lock()
	defer serveRegMu.Unlock()
	if dbName != "" {
		c, ok := serveReg[serveDBKey(t, dbName)]
		return dbName, c, ok
	}
	prefix := tenantScopeKey(t) + "|"
	var outName string
	var outCfg serveConfig
	found := 0
	for key, cfg := range serveReg {
		if strings.HasPrefix(key, prefix) {
			outName = strings.TrimPrefix(key, prefix)
			outCfg = cfg
			found++
		}
	}
	if found != 1 {
		return "", serveConfig{}, false
	}
	return outName, outCfg, true
}

// isWriteSQL reports whether a statement mutates data/schema — used to enforce access:"readonly".
// SELECT/WITH/BEGIN/COMMIT/ROLLBACK/PRAGMA/EXPLAIN are allowed through; the rest are writes.
func isWriteSQL(text string) bool {
	s := strings.TrimSpace(text)
	end := strings.IndexFunc(s, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '(' })
	first := s
	if end > 0 {
		first = s[:end]
	}
	switch strings.ToLower(first) {
	case "insert", "update", "delete", "replace", "create", "drop", "alter", "truncate", "reindex", "vacuum", "attach", "detach":
		return true
	}
	return false
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
		t.q = t.source().Table(t.table)
	}
	return t.q
}

// ready runs the lazy migration and reports a failure as an in-band Invalid, so every terminal opens
// with `if v, ok := t.ready(); !ok { return v }`. A failed migration then surfaces as a clear
// "migration failed: …" instead of the cryptic "no such column" the raw SQL would throw against a
// table the ALTER never actually reached.
func (t *SchemaTable) ready() (value.Value, bool) {
	if t.failed {
		return t.failVal(), false
	}
	if err := t.ensureTable(); err != nil {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db: table %q migration failed: %v", t.table, err)}, false
	}
	return value.Value{}, true
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
	if v, ok := t.ready(); !ok {
		return v
	}
	return coerceResult(t.columns, t.builder().List(args...))
}

func (t *SchemaTable) First(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	return coerceResult(t.columns, t.builder().First(args...))
}

// Find looks a row up by primary key — the schema knows which column that is.
func (t *SchemaTable) Find(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	return coerceResult(t.columns, t.builder().Find(args...))
}

// Count is COUNT(*) through the builder — not list().length, which would load every row to count it.
func (t *SchemaTable) Count(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	return t.builder().Count(args...)
}

// Exists is a cheap SELECT 1 … LIMIT 1 existence check (returns a bool), not list().length > 0.
func (t *SchemaTable) Exists(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	return t.builder().Exists(args...)
}

// Create fills what the schema promises: a generated kitid primary key, defaultNow timestamps, and
// column defaults for anything the caller omitted — then rejects any field that is not a column.
func (t *SchemaTable) Create(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	if len(args) == 0 || args[0].K != value.Map {
		return value.Value{K: value.Invalid, V: "db.create: expects an object"}
	}
	row, errMsg := fillRow(t.columns, args[0].Map())
	if errMsg != "" {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db.create: table %q %s", t.table, errMsg)}
	}
	return coerceResult(t.columns, t.source().Table(t.table).Create(value.New(row)))
}

// fillRow is the shared create logic for BOTH worlds (local turso and shared entity): validate every
// provided field against the schema, then fill what was omitted — a generated kitid PK, defaultNow
// timestamps, and column defaults. Returns the offending column name if a field is not in the schema.
// (The entity world stamps `identity` separately, in Entities.Create.)
// coerceWriteRow validates + coerces the provided fields for a WRITE (create or update): it rejects an
// unknown column or a bad enum value, and coerces bool→0/1, json/array→JSON, decimal→text.
func coerceWriteRow(columns map[string]*ColumnSpec, provided map[string]value.Value) (map[string]value.Value, string) {
	out := map[string]value.Value{}
	for k, v := range provided {
		spec, ok := columns[k]
		if !ok {
			return nil, fmt.Sprintf("has no column %q", k)
		}
		if spec.kind == "enum" && v.K == value.String && !inEnum(spec, v.String()) {
			return nil, fmt.Sprintf("column %q must be one of %v (got %q)", k, spec.enumVals, v.String())
		}
		out[k] = coerceWrite(spec.kind, v)
	}
	return out, ""
}

func fillRow(columns map[string]*ColumnSpec, provided map[string]value.Value) (map[string]value.Value, string) {
	row, errMsg := coerceWriteRow(columns, provided)
	if errMsg != "" {
		return nil, errMsg
	}
	for name, spec := range columns {
		if _, given := row[name]; given {
			continue
		}
		if v, ok := autoValue(spec); ok {
			row[name] = coerceWrite(spec.kind, v)
		}
	}
	return row, ""
}

// applyTouch re-stamps every touchOnUpdate column (an "updated_at" declared with now().onUpdate()) to
// the current time on an update — unless the caller set it explicitly, which wins. This is the ONE
// write where a defaultNow column is regenerated: create() stamps defaultNow via fillRow; update()
// leaves created_at frozen and only refreshes touch columns here.
func applyTouch(columns map[string]*ColumnSpec, row map[string]value.Value) {
	now := value.New(time.Now().UTC().Format(time.RFC3339))
	for name, spec := range columns {
		if !spec.touch {
			continue
		}
		if _, given := row[name]; given {
			continue
		}
		row[name] = coerceWrite(spec.kind, now)
	}
}

// ---- CREATE TABLE IF NOT EXISTS, once per (db file, table) ----

var ensuredTables sync.Map // key: "<abs db path>::<table>"

// Update sets the given fields (coerced per the schema) on rows matching the current where().
func (t *SchemaTable) Update(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	if len(args) > 0 && args[0].K == value.Map {
		row, errMsg := coerceWriteRow(t.columns, args[0].Map())
		if errMsg != "" {
			return value.Value{K: value.Invalid, V: fmt.Sprintf("db.update: table %q %s", t.table, errMsg)}
		}
		applyTouch(t.columns, row) // now().onUpdate() columns refresh on every update
		args[0] = value.New(row)
	}
	return t.builder().Update(args...)
}

// Delete removes the matching rows for real (DELETE FROM … WHERE …). The schema DSL declares exactly
// the columns it has, so there is no hidden `deleted_at` soft-delete convention here — the base query
// builder's Delete() is a soft delete that assumes such a column, which a schema table need not have.
// For soft delete, declare a `deleted_at` column and update({ deleted_at: now() }) explicitly.
func (t *SchemaTable) Delete(_ ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	return t.builder().Remove()
}

func (t *SchemaTable) Remove(args ...value.Value) value.Value { return t.Delete(args...) }

// Plan is a DRY RUN: it reports what migrate() would do to bring the live table up to the schema,
// WITHOUT changing anything. db.vouchers.plan() returns an array of
// { action, column, from, to, destructive, willApply } so a handler (or a check command) can preview
// exactly what a deploy will migrate — including destructive steps that will be refused without
// { drop: true }.
func (t *SchemaTable) Plan(_ ...value.Value) value.Value {
	if t.failed {
		return t.failVal()
	}
	db := t.source().db()
	if db == nil {
		return value.Value{K: value.Invalid, V: "db.plan: connection unavailable"}
	}
	current, err := tableColumns(db, t.table)
	if err != nil {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db.plan: %v", err)}
	}
	steps := planMigration(current, t.columns, false, t.allowDrop)
	out := make([]value.Value, 0, len(steps))
	for _, s := range steps {
		out = append(out, value.New(map[string]value.Value{
			"action":      value.New(s.Action),
			"column":      value.New(s.Column),
			"from":        value.New(s.From),
			"to":          value.New(s.To),
			"destructive": value.New(s.Destructive),
			"willApply":   value.New(s.WillApply),
		}))
	}
	return value.New(out)
}

// ensureTable runs the migration once per (engine, file, table, SCHEMA HASH). The hash in the key is
// what makes it react to schema changes: edit the schema (add a column) and the key changes, so a
// hot-reload re-runs migrate() and ALTERs the live table — not just first boot.
func (t *SchemaTable) ensureTable() error {
	key := t.engine + ":" + t.tenant.resolve(".data", t.dbName) + "::" + t.table + "::" + schemaHash(t.columns)
	if _, done := ensuredTables.Load(key); done {
		return nil
	}
	// Do NOT cache on failure: a migration that could not apply (e.g. the ALTER did not take because
	// another process held the database) must be RETRIED on the next request, not silently marked done.
	if err := migrate(t.source().db(), t.table, t.columns, false, t.allowDrop); err != nil {
		fmt.Printf("[db.migrate] ERROR table %q: %v — not applied; will retry next request\n", t.table, err)
		return err
	}
	ensuredTables.Store(key, true)
	return nil
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
	parts := []string{fmt.Sprintf("%q %s", name, storageClass(c.kind))}
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
		t.e = (&Database{tenant: t.tenant, requestScope: t.scope}).Entity().Table(t.table)
	}
	return t.e
}

// ready runs the shared-world migration lazily and surfaces a failure as an in-band Invalid — the
// EntityTable mirror of SchemaTable.ready (see there).
func (t *EntityTable) ready() (value.Value, bool) {
	if t.failed {
		return t.failVal(), false
	}
	if err := t.ensureTable(); err != nil {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db: table %q migration failed: %v", t.table, err)}, false
	}
	return value.Value{}, true
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
	if v, ok := t.ready(); !ok {
		return v
	}
	return coerceResult(t.columns, t.builder().List(args...))
}

func (t *EntityTable) First(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	return coerceResult(t.columns, t.builder().First(args...))
}

func (t *EntityTable) Find(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	return coerceResult(t.columns, t.builder().Find(args...))
}

func (t *EntityTable) Count(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	return t.builder().Count(args...)
}

func (t *EntityTable) Exists(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	return t.builder().Exists(args...)
}

func (t *EntityTable) Create(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	if len(args) == 0 || args[0].K != value.Map {
		return value.Value{K: value.Invalid, V: "db.create: expects an object"}
	}
	row, errMsg := fillRow(t.columns, args[0].Map())
	if errMsg != "" {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db.create: table %q %s", t.table, errMsg)}
	}
	// Entities.Create stamps the `identity` column itself — we never set it here.
	return coerceResult(t.columns, t.builder().Create(value.New(row)))
}

func (t *EntityTable) Update(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	if len(args) > 0 && args[0].K == value.Map {
		row, errMsg := coerceWriteRow(t.columns, args[0].Map())
		if errMsg != "" {
			return value.Value{K: value.Invalid, V: fmt.Sprintf("db.update: table %q %s", t.table, errMsg)}
		}
		applyTouch(t.columns, row) // now().onUpdate() columns refresh on every update
		args[0] = value.New(row)
	}
	return t.builder().Update(args...)
}

// Delete is a hard delete (see SchemaTable.Delete) — but still bounded by the identity predicate the
// EntityTable's builder carries, so one app can never delete another app's rows in the shared table.
func (t *EntityTable) Delete(_ ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	return t.builder().Remove()
}

func (t *EntityTable) Remove(args ...value.Value) value.Value { return t.Delete(args...) }

// ensureTable migrates the shared table on database.System, with the extra `identity` column.
func (t *EntityTable) ensureTable() error {
	if database.System == nil {
		return nil
	}
	key := fmt.Sprintf("system:%p:%s:%s", database.System, t.table, schemaHash(t.columns))
	if _, done := ensuredTables.Load(key); done {
		return nil
	}
	if err := migrate(database.System, t.table, t.columns, true, false); err != nil {
		fmt.Printf("[db.migrate] ERROR shared table %q: %v — not applied; will retry next request\n", t.table, err)
		return err
	}
	ensuredTables.Store(key, true)
	return nil
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

// planStep is one item of a migration plan — the delta between the schema and the live table. WillApply
// carries the { drop: true } decision: a destructive step (retype/drop) applies only when it is set.
type planStep struct {
	Action      string // "create" | "add" | "retype" | "drop"
	Column      string
	From        string // current type (retype/drop)
	To          string // desired type (add/retype)
	Destructive bool
	WillApply   bool
}

// planMigration computes what migrate() would do WITHOUT touching the database — the single classifier
// shared by the dry-run (db.<table>.plan()) and the applier (migrate). Pure: (current, schema) in, plan
// out. Destructive steps (retype/drop) are marked WillApply only when allowDrop is set.
func planMigration(current map[string]string, columns map[string]*ColumnSpec, withIdentity, allowDrop bool) []planStep {
	if len(current) == 0 {
		return []planStep{{Action: "create", WillApply: true}}
	}
	var steps []planStep
	for _, name := range sortedColumns(columns) {
		cur, ok := current[name]
		want := desiredType(columns[name])
		switch {
		case !ok:
			steps = append(steps, planStep{Action: "add", Column: name, To: want, WillApply: true})
		case !sameType(cur, want):
			steps = append(steps, planStep{Action: "retype", Column: name, From: cur, To: want, Destructive: true, WillApply: allowDrop})
		}
	}
	var drops []string
	for col := range current {
		if _, want := columns[col]; want {
			continue
		}
		if withIdentity && col == identityColumn {
			continue
		}
		drops = append(drops, col)
	}
	sort.Strings(drops)
	for _, col := range drops {
		steps = append(steps, planStep{Action: "drop", Column: col, From: current[col], Destructive: true, WillApply: allowDrop})
	}
	return steps
}

// describe renders one plan step as a human-readable line — shared by the migrate log and the CLI.
func (s planStep) describe() string {
	switch s.Action {
	case "create":
		return "create table"
	case "add":
		return fmt.Sprintf("add column %s %s", s.Column, s.To)
	case "retype":
		if s.WillApply {
			return fmt.Sprintf("retype %s %s→%s (rebuild + CAST)", s.Column, s.From, s.To)
		}
		return fmt.Sprintf("retype %s %s→%s REFUSED (pass { drop: true }; may lose data)", s.Column, s.From, s.To)
	case "drop":
		if s.WillApply {
			return fmt.Sprintf("drop column %s", s.Column)
		}
		return fmt.Sprintf("extra column %s kept (pass { drop: true } to drop)", s.Column)
	}
	return s.Action
}

// logPlan announces the plan — so an auto-migration is never silent about what it changes or refuses.
// It is the single place that reports the delta; the apply path below only logs failures.
func logPlan(table string, steps []planStep) {
	for _, s := range steps {
		fmt.Printf("[db.migrate] %q — %s\n", table, s.describe())
	}
}

// ---- kitwork check integration: preview migrations without a live request ----

// TablePlan is one table's migration plan for a preflight (kitwork check): what migrate() would do to
// the live table to match the schema, computed WITHOUT applying anything.
type TablePlan struct {
	Engine string
	DB     string
	Table  string
	Steps  []planStep
}

// Lines renders the plan as CLI-ready strings ("<engine>/<db>.<table>: <step>"), so callers outside
// this package need not touch the internal step type.
func (tp TablePlan) Lines() []string {
	prefix := fmt.Sprintf("%s/%s.%s", tp.Engine, tp.DB, tp.Table)
	if len(tp.Steps) == 0 {
		return []string{prefix + ": up to date"}
	}
	out := make([]string, 0, len(tp.Steps))
	for _, s := range tp.Steps {
		out = append(out, prefix+": "+s.describe())
	}
	return out
}

// MigrationPlansFor returns the migration plan for every schema tenant t declared during its Run. It
// introspects each table's live state — or, when the tenant DB does not exist yet, treats it as empty
// so the plan is a clean "create". NOTHING is applied. Used by kitwork check to preview a deploy.
func MigrationPlansFor(t *Tenant) []TablePlan {
	if t == nil {
		return nil
	}
	id, dom := t.appID(), t.Domain()

	schemaRegMu.Lock()
	var proxies []*dbProxy
	for _, p := range schemaReg {
		if p.tenant != nil && p.tenant.appID() == id && p.tenant.Domain() == dom {
			proxies = append(proxies, p)
		}
	}
	schemaRegMu.Unlock()
	sort.Slice(proxies, func(i, j int) bool {
		return proxies[i].engine+proxies[i].dbName < proxies[j].engine+proxies[j].dbName
	})

	var plans []TablePlan
	for _, p := range proxies {
		// Introspect the live DB only if it already exists — never create a file during a preflight.
		live := map[string]map[string]string{}
		if _, err := os.Stat(p.tenant.resolve(".data", p.dbName)); err == nil {
			if db := (&SchemaTable{tenant: p.tenant, engine: p.engine, dbName: p.dbName}).source().db(); db != nil {
				for name := range p.tables {
					if cols, err := tableColumns(db, name); err == nil {
						live[name] = cols
					}
				}
			}
		}
		for _, name := range sortedTableNames(p.tables) {
			steps := planMigration(live[name], p.tables[name], false, p.allowDrop)
			plans = append(plans, TablePlan{Engine: p.engine, DB: p.dbName, Table: name, Steps: steps})
		}
	}
	return plans
}

func sortedTableNames(tables map[string]map[string]*ColumnSpec) []string {
	names := make([]string, 0, len(tables))
	for n := range tables {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// checkpointWAL flushes the write-ahead log into the main database file. A schema change (DDL) that
// lives only in the WAL is NOT reliably visible to a separate turso instance/process opening the same
// file — the ALTER runs and records, the WAL grows, but the main file (and thus another process's view)
// still shows the old columns, which surfaces as "table X has no column Y" on the next insert. Forcing
// the DDL down into the main file makes it durable and visible to every reader. Best-effort: on a WAL-
// less db (":memory:") the PRAGMA is a harmless no-op.
func checkpointWAL(db *sql.DB) {
	if db == nil {
		return
	}
	_, _ = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
}

func migrate(db *sql.DB, table string, columns map[string]*ColumnSpec, withIdentity, allowDrop bool) error {
	if db == nil {
		return fmt.Errorf("connection unavailable")
	}
	ensureHistory(db)

	current, err := tableColumns(db, table)
	if err != nil {
		return fmt.Errorf("introspect %q: %w", table, err)
	}

	steps := planMigration(current, columns, withIdentity, allowDrop)
	logPlan(table, steps) // announce the plan (including any refused destructive step) before applying

	// New table → straight CREATE.
	if len(current) == 0 {
		var extra []string
		if withIdentity {
			extra = append(extra, fmt.Sprintf("%q TEXT NOT NULL", identityColumn))
		}
		ddl := schemaDDL(table, columns, extra...)
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("create %q: %w", table, err)
		}
		recordMigration(db, table, "create-table", ddl)
		checkpointWAL(db)
		return nil
	}

	// Split the plan into what actually applies (destructive steps are here only when allowed).
	var newCols, typeChanged, extras []string
	for _, s := range steps {
		switch {
		case s.Action == "add":
			newCols = append(newCols, s.Column)
		case s.Action == "retype" && s.WillApply:
			typeChanged = append(typeChanged, s.Column)
		case s.Action == "drop" && s.WillApply:
			extras = append(extras, s.Column)
		}
	}

	// A type change or an allowed drop → full table rebuild.
	if len(typeChanged) > 0 || len(extras) > 0 {
		if err := rebuild(db, table, columns, current, withIdentity, allowDrop, extras, typeChanged); err != nil {
			return err
		}
		checkpointWAL(db)
		return nil
	}

	// Otherwise purely additive: ADD the new columns (static defaults backfill existing rows).
	for _, name := range newCols {
		stmt := fmt.Sprintf("ALTER TABLE %q ADD COLUMN %s", table, addColumnSQL(name, columns[name]))
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("add %q.%q: %w", table, name, err)
		}
		recordMigration(db, table, "add-column", stmt)
	}

	// Verify the ADDs actually landed. Turso, when another process/connection is mid-write on the file,
	// has been observed to return NO error from an ALTER that nonetheless does not take — which then
	// surfaces as a cryptic "table X has no column Y" on the very next insert. Re-introspect and fail
	// loudly HERE instead, so ensureTable does not cache success and the next request retries.
	if len(newCols) > 0 {
		after, err := tableColumns(db, table)
		if err != nil {
			return fmt.Errorf("verify %q: %w", table, err)
		}
		for _, name := range newCols {
			if _, ok := after[name]; !ok {
				return fmt.Errorf("column %q did not persist on %q — the ALTER returned no error but the column is absent (another process likely holds the database)", name, table)
			}
		}
		// Flush the ADDs to the main file so a separate process/instance sees them (see checkpointWAL).
		checkpointWAL(db)
	}
	return nil
}

// rebuild is the SQLite table-rebuild ("12-step"): CREATE a new table with the desired schema, COPY
// the data across (CASTing changed types), DROP the old, RENAME the new — all inside a transaction, so
// a failure rolls back to the untouched original. With allowDrop=false, extra columns (in the DB, not
// the schema) are PRESERVED in the new table; with allowDrop=true they are dropped.
func rebuild(db *sql.DB, table string, columns map[string]*ColumnSpec, current map[string]string,
	withIdentity, allowDrop bool, extras, typeChanged []string) error {

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
		return fmt.Errorf("rebuild %q begin: %w", table, err)
	}
	fail := func(stage string, e error) error {
		tx.Rollback()
		return fmt.Errorf("rebuild %q rolled back at %s: %w", table, stage, e)
	}
	createDDL := fmt.Sprintf("CREATE TABLE %q (%s)", tmp, strings.Join(defs, ", "))
	if _, err := tx.Exec(createDDL); err != nil {
		return fail("create", err)
	}
	if len(intoCols) > 0 {
		copyStmt := fmt.Sprintf("INSERT INTO %q (%s) SELECT %s FROM %q",
			tmp, strings.Join(intoCols, ", "), strings.Join(selectExprs, ", "), table)
		if _, err := tx.Exec(copyStmt); err != nil {
			return fail("copy", err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("DROP TABLE %q", table)); err != nil {
		return fail("drop-old", err)
	}
	if _, err := tx.Exec(fmt.Sprintf("ALTER TABLE %q RENAME TO %q", tmp, table)); err != nil {
		return fail("rename", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("rebuild %q commit: %w", table, err)
	}

	action := "rebuild-table"
	if len(typeChanged) > 0 {
		action += " retype[" + strings.Join(typeChanged, ",") + "]"
	}
	if allowDrop && len(extras) > 0 {
		action += " drop[" + strings.Join(extras, ",") + "]"
	}
	recordMigration(db, table, action, createDDL)
	return nil
}

// castExpr copies a column, wrapping it in CAST when the target type differs (a type change).
func castExpr(name, want, cur string) string {
	if sameType(cur, want) {
		return fmt.Sprintf("%q", name)
	}
	return fmt.Sprintf("CAST(%q AS %s)", name, want)
}

func desiredType(c *ColumnSpec) string { return storageClass(c.kind) }

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

// addColumnSQL is a column def for ALTER TABLE ADD COLUMN. SQLite's ADD COLUMN cannot carry PRIMARY
// KEY / UNIQUE (so the added column is nullable), but a DEFAULT is allowed AND is applied to EXISTING
// rows — so a static schema default BACKFILLS old rows here, not only new rows via fillRow. (defaultNow
// is left to fillRow: old rows keep NULL rather than a fabricated "created just now" timestamp.)
func addColumnSQL(name string, c *ColumnSpec) string {
	def := ""
	if c.hasDefault {
		def = " DEFAULT " + sqlLiteral(coerceWrite(c.kind, c.def)) // coerce bool/json defaults for the backfill
	}
	return fmt.Sprintf("%q %s%s", name, storageClass(c.kind), def)
}

// sqlLiteral renders a column default as a SQL literal for DDL (the ADD COLUMN backfill above).
func sqlLiteral(v value.Value) string {
	switch v.K {
	case value.Number:
		if v.N == float64(int64(v.N)) {
			return strconv.FormatInt(int64(v.N), 10)
		}
		return strconv.FormatFloat(v.N, 'g', -1, 64)
	case value.Bool:
		if v.N != 0 {
			return "1"
		}
		return "0"
	default:
		return "'" + strings.ReplaceAll(v.String(), "'", "''") + "'"
	}
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
