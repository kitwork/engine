package work

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/id"
	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

// Schema-aware `db.<table>` surface under the `database` capability: a schema maps table names to
// column specs; the engine auto-migrates the table to match and hands back a validated query builder.
//
//	import { database } from "kitwork";
//	const { sqlite, kitid, text, int, datetime } = database;
//	const vouchers = {
//	  id: kitid().primaryKey(),
//	  code: text().notNull().unique(),
//	  discount: int().default(0),
//	  status: text().default("active"),
//	  created_at: datetime().defaultNow(),
//	};
//	export const db = sqlite("app.db", { vouchers });
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
//   - database.sqlite → modernc; database.define(alias) → entity-scoped
//     shared tables (rows partitioned by identity).
//
// Migration (see migrate() below): additive ADD COLUMN with default backfill runs automatically; type
// changes and column drops are DESTRUCTIVE (rebuild + CAST / drop) and run ONLY with { drop: true }.
// Known limits: the rebuild does not restore indexes/triggers/FKs (the DSL declares none), and the
// shared (entity) migration is not yet coordinated across nodes.

// ---- column DSL: kitid()/text()/integer()/datetime() + chainable modifiers ----

type ColumnSpec struct {
	kind         string // canonical KitDB kind from kitdb/sql
	from         string // migration identity hint: new_name: text().from("old_name")
	primary      bool
	primaryOrder int // positive order for an explicitly declared composite .key(n)
	notNull      bool
	unique       bool
	hasDefault   bool
	def          value.Value
	defaultNow   bool
	touch        bool           // touchOnUpdate: re-stamp the current time on every update() (updated_at)
	enumVals     []string       // allowed choice values; "enum" remains the catalog compatibility kind
	declareErr   string         // deferred builder error reported with the owning field name
	indexes      []colIndexRef  // .index() memberships — an index this column participates in
	uniques      []colUniqueRef // named .unique() memberships — shared names form tuple constraints
	checks       []colCheckRef  // .check(">=", 0) constraints, normalized into Struct Check IR
	fk           *fkRef         // set by ref() — a foreign key on this column
	searchable   bool           // set by .searchable() — this column feeds the full-text index
	searchWt     int            // .searchable({ weight }) — term-frequency multiplier at index time (default 1)
	analytics    bool           // explicit opt-in for derived analytics storage (required for text)
	partition    string         // explicit analytics routing policy: "hash" or "range"
	seq          uint64         // creation order — the VM evaluates the column builders in source order, so
	// sorting a schema's columns by seq reproduces the DECLARED order (the alternative, a plain Go map,
	// loses it, and DDL sorted by name would show columns alphabetically instead of as written).
}

// colIndexRef records that a column takes part in an index. name "" means an auto-named single-column
// index; a shared name across columns forms a composite. pos (>0) pins the column's position within a
// composite; pos 0 means "use declaration order". filter is the partial-index condition (from the
// .index() object arg) — for a composite it may be set on any one member.
type colIndexRef struct {
	name   string
	id     string
	pos    int
	filter []indexCond // partial-index predicate as structured equality/IS NULL conditions (AND-ed)
}

// colUniqueRef records one field's membership in a named tuple-unique constraint.
// A zero position uses declaration order; an explicit positive position pins order.
type colUniqueRef struct {
	name string
	pos  int
}

// indexCond is one "column = value" (or "column IS NULL" when val is nil) part of a partial index's
// filter. Structured, not raw SQL, so the public API never takes a WHERE string.
type indexCond struct {
	col string
	val value.Value
}

// fkRef is a foreign key declared with ref(target, { onDelete, onUpdate }). target is the referenced
// column's spec (resolved to table/column by pointer identity when the schema is registered). A ref
// inherits ONLY the target's storage type — never its primary-key/unique/default/auto-gen, so a FK to a
// kitid column does NOT auto-generate a random id when left blank.
type fkRef struct {
	target   *ColumnSpec
	onDelete string
	onUpdate string
	name     string
	pos      int
	// resolved from target by pointer identity at registration:
	table          string
	column         string
	targetStructID string
	targetFieldID  string
}

// colSeqCounter stamps each ColumnSpec with a monotonic creation number. kitid()/text()/… run in the
// order they appear in the schema object literal, so seq === declaration order.
var colSeqCounter atomic.Uint64

func nextColSeq() uint64 { return colSeqCounter.Add(1) }

// The modifiers are variadic so the VM does NOT auto-call them as getters (a 0-in/1-out method is a
// getter here; a variadic method stays a callable).
func (c *ColumnSpec) PrimaryKey(args ...value.Value) *ColumnSpec { return c.Key(args...) }

// Key is the preferred spelling of PrimaryKey: text().key() / uuid().key() make any column the primary
// key. A key is PRIMARY KEY + NOT NULL (columnSQL emits both) and unique by nature — no extra unique
// index needed. .primaryKey() stays as a compatibility alias.
func (c *ColumnSpec) Key(args ...value.Value) *ColumnSpec {
	c.primary = true
	if len(args) == 0 {
		return c
	}
	if len(args) != 1 || args[0].K != value.Number {
		c.declareErr = "key() accepts at most one positive integer position"
		return c
	}
	position := int(args[0].N)
	if args[0].N != float64(position) || position < 1 {
		c.declareErr = "key() position must be a positive integer"
		return c
	}
	c.primaryOrder = position
	return c
}
func (c *ColumnSpec) NotNull(_ ...value.Value) *ColumnSpec { c.notNull = true; return c }
func (c *ColumnSpec) Unique(args ...value.Value) *ColumnSpec {
	if len(args) == 0 {
		c.unique = true
		return c
	}
	ref := colUniqueRef{}
	for _, argument := range args {
		switch argument.K {
		case value.String:
			if ref.name != "" {
				c.declareErr = "unique() accepts at most one constraint name"
				return c
			}
			ref.name = strings.TrimSpace(argument.String())
			if ref.name == "" {
				c.declareErr = "unique() tuple constraint needs a non-empty name"
				return c
			}
		case value.Number:
			position := int(argument.N)
			if argument.N != float64(position) || position < 1 {
				c.declareErr = "unique() position must be a positive integer"
				return c
			}
			if ref.pos != 0 {
				c.declareErr = "unique() accepts at most one position"
				return c
			}
			ref.pos = position
		default:
			c.declareErr = "unique() accepts a constraint name and optional position"
			return c
		}
	}
	c.uniques = append(c.uniques, ref)
	return c
}
func (c *ColumnSpec) DefaultNow(_ ...value.Value) *ColumnSpec { c.defaultNow = true; return c }

// OnUpdate marks an "updated_at" column: it is re-stamped with the current time on EVERY update()
// (unless the caller passes the column explicitly). Pair it with now() — now().onUpdate() stamps on
// both insert (defaultNow) and update (touch); datetime().onUpdate() stamps on update only.
func (c *ColumnSpec) OnUpdate(_ ...value.Value) *ColumnSpec { c.touch = true; return c }

// Index adds this column to an index. Args are matched by type in any order: a string is the index
// name, a number is the column's position in a composite, an object is options { pos, unique, where }.
//
//	slug: text().index()                                   // single column, auto-named idx_<table>_slug
//	code:   text().index("links_status_code")              // composite: same name on several columns,
//	status: text().index("links_status_code")              //   ordered by DECLARATION order
//	a: text().index("ab", 2)                               // composite with an explicit position — pins
//	b: text().index("ab", 1)                               //   the order → index on (b, a)
//	created_at: now().index("active_recent", { status: "active", deleted_at: null })  // partial index
//	                                          // → WHERE status = 'active' AND deleted_at IS NULL
func (c *ColumnSpec) Index(args ...value.Value) *ColumnSpec {
	ref := colIndexRef{}
	for _, a := range args {
		switch a.K {
		case value.String:
			ref.name = a.String()
		case value.Number:
			ref.pos = int(a.N)
		case value.Map:
			// The object arg is a partial-index FILTER: each key is a column, the value the equality
			// target (null → IS NULL). Structured, so no raw SQL enters the schema.
			for col, v := range a.Map() {
				ref.filter = append(ref.filter, indexCond{col: col, val: v})
			}
		}
	}
	c.indexes = append(c.indexes, ref)
	return c
}

// Searchable marks a column for full-text search: db.posts.search("…") ranks rows by BM25 over every
// searchable column. The optional object sets a weight — text().searchable({ weight: 3 }) makes matches
// in this column count triple, so a title outranks a body — mirroring the .index({ … }) object shape.
// Bare .searchable() is weight 1. Search runs on the durable pure-Go segment engine owned by the
// process search manager; the relational database remains the source of truth and is never opened as
// a search-index file.
func (c *ColumnSpec) Searchable(args ...value.Value) *ColumnSpec {
	c.searchable = true
	c.searchWt = 1
	for _, a := range args {
		if a.K == value.Map {
			if w, ok := a.Map()["weight"]; ok && w.K == value.Number && w.N > 0 {
				c.searchWt = int(w.N)
			}
		}
	}
	return c
}

// Analytics opts this field into KitDB's derived analytics projection. Numeric
// and boolean fields remain projection-compatible by default; text-compatible
// fields require this explicit declaration before KitDB dictionary-encodes them.
func (c *ColumnSpec) Analytics(_ ...value.Value) *ColumnSpec {
	c.analytics = true
	return c
}

// Partition declares how immutable analytics chunks summarize this field.
// It does not split KROW/WAL ownership: the policy only lets the planner prune
// whole derived analytics extents before opening their KCOL block payloads.
func (c *ColumnSpec) Partition(args ...value.Value) *ColumnSpec {
	if len(args) != 1 || args[0].K != value.String {
		c.declareErr = `partition() requires one explicit strategy: "hash" or "range"`
		return c
	}
	strategy := strings.ToLower(strings.TrimSpace(args[0].String()))
	switch strategy {
	case "hash", "range":
		c.partition = strategy
	default:
		c.declareErr = `partition() supports "hash" or "range"; temporal partitions are not available yet`
	}
	return c
}

func (c *ColumnSpec) Default(args ...value.Value) *ColumnSpec {
	c.hasDefault = true
	if len(args) > 0 {
		c.def = args[0]
	}
	return c
}

// Check adds one structured column predicate without accepting raw SQL. The
// compact form is int().check(">=", 0); an optional leading name makes the
// constraint identity explicit: int().check("positive_price", ">=", 0).
func (c *ColumnSpec) Check(args ...value.Value) *ColumnSpec {
	check := colCheckRef{}
	switch len(args) {
	case 2:
		if args[0].K == value.String {
			check.operator = normalizeKitDBCheckOperator(args[0].String())
		}
		check.value = args[1]
	case 3:
		if args[0].K == value.String {
			check.name = strings.TrimSpace(args[0].String())
		}
		if args[1].K == value.String {
			check.operator = normalizeKitDBCheckOperator(args[1].String())
		}
		check.value = args[2]
	default:
		c.declareErr = "check() expects an operator and literal, with an optional constraint name"
		return c
	}
	if len(args) == 3 && check.name == "" {
		c.declareErr = "check() constraint name cannot be empty"
		return c
	}
	if !validKitDBCheckOperator(check.operator) {
		c.declareErr = "check() supports =, !=, <>, >, >=, <, and <="
		return c
	}
	if !validKitDBCheckLiteral(check.value) {
		c.declareErr = "check() literal must be null, boolean, number, string, bytes, time, or duration"
		return c
	}
	c.checks = append(c.checks, check)
	return c
}

// From preserves a field's persisted identity while changing its public name.
// It is only a migration hint; once the new catalog is committed, the hint can
// be removed because KitDB reconciles the stored identity by the new name.
func (c *ColumnSpec) From(args ...value.Value) *ColumnSpec {
	if len(args) > 0 && args[0].K == value.String {
		c.from = strings.TrimSpace(args[0].String())
	}
	return c
}

func columnFunc(kind string) value.Value {
	return value.NewFunc(func(_ ...value.Value) value.Value {
		return value.New(&ColumnSpec{kind: kind, seq: nextColSeq()})
	})
}

// The WHOLE DB surface lives under `database`: column builders (text/int/kitid/datetime), the local
// engine factories (sqlite/kitdb), and the shared world (define/entity). One import, destructure what
// you need:
//
//	import { database } from "kitwork";
//	const { sqlite, kitid, text, int, datetime } = database;
//	const db = sqlite("app.db", { vouchers });   // === database.sqlite("app.db", { vouchers })
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

// Choice declares one string selected from a finite set. The internal "enum"
// kind is retained so existing catalogs and database files need no migration.
func (d *Database) Choice() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		c := &ColumnSpec{kind: "enum", seq: nextColSeq()}
		if len(args) == 0 {
			c.declareErr = "choice() needs at least one string value"
			return value.New(c)
		}
		seen := make(map[string]struct{}, len(args))
		for index, a := range args {
			if a.K != value.String {
				if c.declareErr == "" {
					c.declareErr = fmt.Sprintf("choice() value %d must be a string", index+1)
				}
				continue
			}
			choice := a.String()
			if strings.TrimSpace(choice) == "" {
				if c.declareErr == "" {
					c.declareErr = fmt.Sprintf("choice() value %d cannot be empty", index+1)
				}
				continue
			}
			if _, duplicate := seen[choice]; duplicate {
				if c.declareErr == "" {
					c.declareErr = fmt.Sprintf("choice() value %q is declared more than once", choice)
				}
				continue
			}
			seen[choice] = struct{}{}
			c.enumVals = append(c.enumVals, choice)
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
		return value.New(&ColumnSpec{kind: "kitid", primary: true, seq: nextColSeq()})
	})
}
func (d *Database) Now() value.Value { // = datetime().defaultNow()
	return value.NewFunc(func(_ ...value.Value) value.Value {
		return value.New(&ColumnSpec{kind: "datetime", defaultNow: true, seq: nextColSeq()})
	})
}
func (d *Database) Updated() value.Value { // = now().onUpdate() — stamped on insert AND every update
	return value.NewFunc(func(_ ...value.Value) value.Value {
		return value.New(&ColumnSpec{kind: "datetime", defaultNow: true, touch: true, seq: nextColSeq()})
	})
}

// Ref declares a foreign key: ref(users.id, { onDelete: "cascade" }). Composite references share a
// constraint name and optionally pin their order: ref(accounts.tenant, "orders_account", 1). The
// column inherits ONLY the
// target's storage type — not its primary-key/unique/default/auto-gen — so a ref to a kitid never
// auto-generates a random id when left blank. Target/column names are resolved by pointer identity when
// the schema is registered (see newDbProxy).
func (d *Database) Ref() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		spec := &ColumnSpec{seq: nextColSeq()}
		fk := &fkRef{}
		if len(args) > 0 {
			if target, ok := args[0].V.(*ColumnSpec); ok {
				fk.target = target
				spec.kind = plainKindFor(target.kind) // same storage class, no auto-gen
			}
		}
		for _, argument := range args[1:] {
			switch argument.K {
			case value.String:
				if fk.name != "" {
					spec.declareErr = "ref() accepts at most one constraint name"
					break
				}
				fk.name = strings.TrimSpace(argument.String())
				if fk.name == "" {
					spec.declareErr = "ref() composite constraint needs a non-empty name"
				}
			case value.Number:
				position := int(argument.N)
				if argument.N != float64(position) || position < 1 {
					spec.declareErr = "ref() position must be a positive integer"
					break
				}
				if fk.pos != 0 {
					spec.declareErr = "ref() accepts at most one position"
					break
				}
				fk.pos = position
			case value.Map:
				opts := argument.Map()
				if v, ok := opts["onDelete"]; ok && v.K == value.String {
					fk.onDelete = v.String()
				}
				if v, ok := opts["onUpdate"]; ok && v.K == value.String {
					fk.onUpdate = v.String()
				}
			default:
				spec.declareErr = "ref() accepts a target, constraint name, position, and action options"
			}
		}
		if fk.pos != 0 && fk.name == "" && spec.declareErr == "" {
			spec.declareErr = "ref() position requires a composite constraint name"
		}
		spec.fk = fk
		return value.New(spec)
	})
}

// plainKindFor maps a target column's kind to a plain kind with the SAME storage class but no auto-gen,
// so a foreign key stores compatible values without inheriting the target's id generation.
func plainKindFor(targetKind string) string {
	// Exact integer widths select a different durable key codec from the legacy
	// integer kind. A foreign key must preserve that width even though both use
	// the same KROW storage class.
	switch targetKind {
	case "smallint", "int32", "bigint":
		return targetKind
	}
	switch storageClass(targetKind) {
	case "INTEGER":
		return "integer"
	case "REAL":
		return "float"
	case "BLOB":
		return "blob"
	default:
		return "text" // TEXT storage — kitid, uuid, text, …
	}
}

// fkAction maps a JS onDelete/onUpdate value to its SQL action.
func fkAction(a string) string {
	switch normalizeFKActionName(a) {
	case "cascade":
		return "CASCADE"
	case "restrict":
		return "RESTRICT"
	case "setnull":
		return "SET NULL"
	case "setdefault":
		return "SET DEFAULT"
	default:
		return "NO ACTION"
	}
}

func normalizeFKActionName(action string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(action), " ", ""))
}

func comparableFKActionName(action string) string {
	normalized := normalizeFKActionName(action)
	if normalized == "" {
		return "noaction"
	}
	return normalized
}

func validFKActionName(action string) bool {
	switch normalizeFKActionName(action) {
	case "", "noaction", "restrict", "cascade", "setnull", "setdefault":
		return true
	default:
		return false
	}
}

// paramFunc builds a column whose first arg is a size/precision/dimension hint (varchar(50),
// decimal(10,2), vector(1536)). The hint is metadata for v1 — sqlite ignores length/precision, and the
// vector is stored as a JSON array — but it is accepted so schemas read the same across engines.
func paramFunc(kind string) value.Value {
	return value.NewFunc(func(_ ...value.Value) value.Value {
		return value.New(&ColumnSpec{kind: kind, seq: nextColSeq()})
	})
}

// ---- storage classes + coercion ----

func storageClass(kind string) string {
	if typeInfo, found := kitdbsql.LookupKind(kind); found {
		return typeInfo.Storage.String()
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

// Sqlite and Kitdb are local per-tenant factories. They are variadic (not 0-arg getters), so a
// destructured factory and database.<factory>(...) are the same call. SQLite renders the shared
// ORM state to SQL; KitDB executes its storage-neutral plan directly. All files remain confined to
// the tenant's .data directory and are owned by the app runtime.
func (d *Database) Sqlite(args ...value.Value) value.Value {
	return newDbProxy(d.tenant, d.requestScope, "sqlite", args...)
}

// ---- defineDb("app.db", { vouchers, categories }) → a Proxy whose db.<table> is schema-aware ----

type dbProxy struct {
	tenant           *Tenant
	scope            *requestscope.Scope
	engine           string // "sqlite" | "kitdb"
	dbName           string
	databaseID       string                             // expected durable identity for node-catalog managed databases
	allowDrop        bool                               // opt-in via sqlite("app.db", schema, { drop: true }) — enables destructive migration
	migrate          bool                               // KitDB-only opt-in for planner-approved, non-destructive schema migration
	retainHistory    bool                               // KitDB-only opt-in for durable recovery/replication history
	recoverySet      bool                               // explicit recovery:false must not be overridden by searchable projections
	historyRetention kitdbengine.HistoryRetentionPolicy // process-local bounds; durable pins still win
	sourceDeclared   bool                               // created by tenant source rather than node-owned SQL lifecycle
	tables           map[string]map[string]*ColumnSpec
	structs          map[string]*StructDef
	declaredTables   []string
	schemaMu         sync.RWMutex
	catalogOnce      sync.Once
	catalogErr       error
	catalogPending   bool
	catalogTx        uint64
	catalogRev       string
	transaction      *kitDBRecordTransaction
}

// newDbProxy parses sqlite("app.db", { …schema }, { drop: true }): args[0]=file, args[1]=schema,
// args[2]=options. `drop` is the EXPLICIT opt-in for DESTRUCTIVE migration — dropping columns the
// schema removed AND rebuilding for type changes (CAST may lose data). Without it, both are refused
// and the data is left untouched.
func newDbProxy(tenant *Tenant, scope *requestscope.Scope, engine string, args ...value.Value) value.Value {
	if engine != "sqlite" && engine != "kitdb" {
		return value.Value{K: value.Invalid, V: "db: unsupported storage engine"}
	}
	defaultName := "app.db"
	if engine == "kitdb" {
		defaultName = kitDBDefaultFile
	}
	p := &dbProxy{
		tenant: tenant, scope: scope, engine: engine, dbName: defaultName,
		tables: map[string]map[string]*ColumnSpec{}, structs: map[string]*StructDef{},
		sourceDeclared: true,
	}
	if len(args) > 0 && args[0].K == value.String {
		p.dbName = args[0].String()
	}
	if engine == "kitdb" && strings.EqualFold(kitDBRel(p.dbName), kitDBNodeCatalogFile) {
		return value.Value{
			K: value.Invalid,
			V: fmt.Sprintf("db: KitDB database name %q is reserved for the node catalog", p.dbName),
		}
	}
	if len(args) > 1 && args[1].K == value.Map {
		for tableName, tableVal := range args[1].Map() {
			cols := map[string]*ColumnSpec{}
			var source *StructDef
			if candidate, ok := tableVal.V.(*StructDef); ok && candidate != nil {
				source = candidate
				for colName, spec := range candidate.columns {
					cols[colName] = spec
				}
			} else if tableVal.K == value.Map {
				if engine == "kitdb" {
					return value.Value{K: value.Invalid, V: fmt.Sprintf("db: KitDB struct %q must be declared with struct({ ... })", tableName)}
				}
				for colName, colVal := range tableVal.Map() {
					if spec, ok := colVal.V.(*ColumnSpec); ok {
						cols[colName] = spec
					}
				}
			} else {
				return value.Value{K: value.Invalid, V: fmt.Sprintf("db: schema %q is not a struct", tableName)}
			}
			p.tables[tableName] = cols
			p.structs[tableName] = source
		}
		p.resolveForeignKeys()
		for tableName, columns := range p.tables {
			p.structs[tableName] = bindStructDef(tableName, p.structs[tableName], columns)
		}
		for _, tableName := range sortedTableNames(p.tables) {
			definition := p.structs[tableName]
			if err := validateStructForeignConstraints(definition, p.structs); err != nil {
				return value.Value{K: value.Invalid, V: fmt.Sprintf("db: schema %q: %v", tableName, err)}
			}
		}
		p.declaredTables = sortedTableNames(p.tables)
	}
	if len(args) > 2 && args[2].K == value.Map {
		opts := args[2].Map()
		if d, ok := opts["drop"]; ok && d.K == value.Bool && d.N != 0 {
			p.allowDrop = true
		}
		if m, ok := opts["migrate"]; ok && m.K == value.Bool && m.N != 0 {
			p.migrate = true
		}
		if recovery, ok := opts["recovery"]; ok {
			retain, policy, err := parseKitDBRecoveryOption(recovery)
			if err != nil {
				return value.Value{K: value.Invalid, V: "db: recovery: " + err.Error()}
			}
			p.recoverySet = true
			p.retainHistory = retain
			p.historyRetention = policy
		}
		// A token explicitly exposes this database over HTTP. The registry retains
		// the backend and normalized schema so a KitDB file can never be opened by
		// the SQLite driver merely because both use the same Hrana transport.
		if tok, ok := opts["token"]; ok && !tok.IsNil() && tok.String() != "" {
			access := ""
			if a, ok := opts["access"]; ok && a.K == value.String {
				access = a.String()
			}
			registerServe(p, tok.String(), access)
		}
	}
	p.enableSearchHistoryByDefault()
	registerSchema(p)
	return value.Value{K: value.Proxy, V: p}
}

// Search is a rebuildable projection, but incremental restart-safe catch-up
// requires the source commit history. Enable it automatically for KitDB unless
// the application deliberately opted out with recovery:false.
func (p *dbProxy) enableSearchHistoryByDefault() {
	if p == nil || p.engine != "kitdb" || p.recoverySet || p.retainHistory {
		return
	}
	for _, columns := range p.tables {
		for _, column := range columns {
			if column != nil && column.searchable {
				p.retainHistory = true
				return
			}
		}
	}
}

func parseKitDBRecoveryOption(
	option value.Value,
) (bool, kitdbengine.HistoryRetentionPolicy, error) {
	switch option.K {
	case value.Bool:
		return option.N != 0, kitdbengine.HistoryRetentionPolicy{}, nil
	case value.Map:
		settings := option.Map()
		for name := range settings {
			if name != "maxAge" && name != "maxBytes" {
				return false, kitdbengine.HistoryRetentionPolicy{}, fmt.Errorf(
					"unknown option %q; use maxAge or maxBytes", name,
				)
			}
		}
		policy := kitdbengine.HistoryRetentionPolicy{}
		if raw, found := settings["maxAge"]; found {
			var duration time.Duration
			var err error
			switch raw.K {
			case value.Duration:
				duration = time.Duration(int64(raw.N))
			case value.String:
				duration, err = ParseDuration(raw.String())
			default:
				err = fmt.Errorf("maxAge must be a duration such as \"7d\"")
			}
			if err != nil {
				return false, kitdbengine.HistoryRetentionPolicy{}, err
			}
			if duration <= 0 {
				return false, kitdbengine.HistoryRetentionPolicy{}, fmt.Errorf("maxAge must be positive")
			}
			policy.MaxAge = duration
		}
		if raw, found := settings["maxBytes"]; found {
			bytes, err := parseRetentionBytes(raw)
			if err != nil {
				return false, kitdbengine.HistoryRetentionPolicy{}, err
			}
			policy.MaxBytes = bytes
		}
		if !policy.Enabled() {
			return false, kitdbengine.HistoryRetentionPolicy{}, fmt.Errorf(
				"object requires maxAge or maxBytes",
			)
		}
		if err := policy.Validate(); err != nil {
			return false, kitdbengine.HistoryRetentionPolicy{}, err
		}
		return true, policy, nil
	default:
		return false, kitdbengine.HistoryRetentionPolicy{}, fmt.Errorf(
			"must be true, false, or an object with maxAge/maxBytes",
		)
	}
}

func parseRetentionBytes(raw value.Value) (int64, error) {
	if raw.K == value.Number {
		if math.IsNaN(raw.N) || math.IsInf(raw.N, 0) || raw.N <= 0 ||
			math.Trunc(raw.N) != raw.N || raw.N > math.MaxInt64 {
			return 0, fmt.Errorf("maxBytes must be a positive whole number")
		}
		return int64(raw.N), nil
	}
	if raw.K != value.String {
		return 0, fmt.Errorf("maxBytes must be bytes or a size such as \"2gb\"")
	}
	size := strings.TrimSpace(strings.ToLower(raw.String()))
	index := 0
	for index < len(size) && size[index] >= '0' && size[index] <= '9' {
		index++
	}
	if index == 0 {
		return 0, fmt.Errorf("maxBytes must be bytes or a size such as \"2gb\"")
	}
	number, err := strconv.ParseInt(size[:index], 10, 64)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("maxBytes must be positive")
	}
	multipliers := map[string]int64{
		"": 1, "b": 1,
		"kb": 1 << 10, "kib": 1 << 10,
		"mb": 1 << 20, "mib": 1 << 20,
		"gb": 1 << 30, "gib": 1 << 30,
		"tb": 1 << 40, "tib": 1 << 40,
	}
	multiplier, found := multipliers[strings.TrimSpace(size[index:])]
	if !found {
		return 0, fmt.Errorf("maxBytes has an unknown size unit")
	}
	if number > math.MaxInt64/multiplier {
		return 0, fmt.Errorf("maxBytes is too large")
	}
	return number * multiplier, nil
}

// resolveForeignKeys fills source declarations by pointer identity and catalog
// definitions by their persisted table/column names. A target outside this
// database stays unresolved and is rejected by validateSchema.
func (p *dbProxy) resolveForeignKeys() {
	p.schemaMu.Lock()
	defer p.schemaMu.Unlock()
	p.resolveForeignKeysLocked()
}

func (p *dbProxy) resolveForeignKeysLocked() {
	loc := map[*ColumnSpec][2]string{}
	for tableName, cols := range p.tables {
		for colName, spec := range cols {
			loc[spec] = [2]string{tableName, colName}
		}
	}
	definitionsByID := make(map[string]*StructDef, len(p.structs))
	for _, definition := range p.structs {
		if definition != nil && definition.ID != "" {
			definitionsByID[definition.ID] = definition
		}
	}
	for _, cols := range p.tables {
		for _, spec := range cols {
			if spec.fk != nil && spec.fk.target == nil && spec.fk.targetStructID != "" {
				if targetDefinition := definitionsByID[spec.fk.targetStructID]; targetDefinition != nil {
					if field, found := structFieldByID(targetDefinition, spec.fk.targetFieldID); found {
						spec.fk.table, spec.fk.column = targetDefinition.Name, field.Name
						spec.fk.target = targetDefinition.columns[field.Name]
					}
				}
			}
			if spec.fk != nil && spec.fk.target == nil && spec.fk.table != "" {
				if targetTable := p.tables[spec.fk.table]; targetTable != nil {
					spec.fk.target = targetTable[spec.fk.column]
					if spec.fk.target == nil {
						if targetDefinition := p.structs[spec.fk.table]; targetDefinition != nil {
							if field, found := kitDBField(targetDefinition, spec.fk.column); found {
								spec.fk.column = field.Name
								spec.fk.target = targetTable[field.Name]
							}
						}
					}
				}
			}
			if spec.fk != nil && spec.fk.target != nil {
				if l, ok := loc[spec.fk.target]; ok {
					spec.fk.table, spec.fk.column = l[0], l[1]
					if spec.fk.targetStructID == "" {
						if target := p.structs[l[0]]; target != nil && target.ID != "" {
							spec.fk.targetStructID = target.ID
						}
						if spec.fk.targetStructID == "" {
							spec.fk.targetStructID = stableSchemaID("struct", l[0])
						}
					}
					if spec.fk.targetFieldID == "" {
						if target := p.structs[l[0]]; target != nil {
							if field, found := kitDBField(target, l[1]); found {
								spec.fk.targetFieldID = field.ID
							}
						}
						if spec.fk.targetFieldID == "" {
							spec.fk.targetFieldID = stableSchemaID(
								"field", spec.fk.targetStructID+":"+fieldIdentityName(l[1], spec.fk.target),
							)
						}
					}
				}
			}
		}
	}
}

// schemaSnapshot returns immutable outer-map snapshots. Struct definitions and
// column specs are immutable after publication, so readers can safely retain
// their pointers while a later DDL commit publishes another definition.
func (p *dbProxy) schemaSnapshot() (map[string]map[string]*ColumnSpec, map[string]*StructDef) {
	tables, definitions, _, _ := p.schemaSnapshotWithCatalog()
	return tables, definitions
}

// schemaSnapshotWithCatalog binds an immutable schema-map snapshot to the
// catalog revision that published it. Record transactions use this boundary
// to avoid pairing a kernel snapshot with schema from another DDL commit.
func (p *dbProxy) schemaSnapshotWithCatalog() (
	map[string]map[string]*ColumnSpec,
	map[string]*StructDef,
	uint64,
	string,
) {
	if p == nil {
		return nil, nil, 0, ""
	}
	p.schemaMu.RLock()
	defer p.schemaMu.RUnlock()
	tables := make(map[string]map[string]*ColumnSpec, len(p.tables))
	for name, columns := range p.tables {
		tables[name] = columns
	}
	definitions := make(map[string]*StructDef, len(p.structs))
	for name, definition := range p.structs {
		definitions[name] = definition
	}
	return tables, definitions, p.catalogTx, p.catalogRev
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
	key := tenantScopeKey(p.tenant) + "|" + p.engine + "|" + p.dbName
	schemaRegMu.Lock()
	schemaReg[key] = p
	schemaRegMu.Unlock()
}

// ---- serve registry: which tenant dbs are exposed over libSQL/HTTP, and how ----
//
// Populated by sqlite(...) or kitdb(...) with { token, access } at declare time. The Hrana
// and /_db handlers consult this registry instead of environment variables: exposure is a declared
// application capability, not the side effect of a secret existing.

type serveConfig struct {
	token             string
	access            string // "readonly" | "readwrite"
	engine            string
	database          *dbProxy
	capabilityStorage string // source declaration that owns auth; never persisted as a token
	logicalName       string // PostgreSQL name for a managed database; storage remains stable
	dropping          bool
}

var (
	serveRegMu sync.Mutex
	serveReg   = map[string]serveConfig{} // "appID|domain|dbName" -> backend-aware config
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
func registerServe(database *dbProxy, token, access string) {
	capabilityStorage := ""
	if database != nil {
		capabilityStorage = database.dbName
	}
	registerServeFromCapability(database, token, access, capabilityStorage)
}

func registerServeFromCapability(database *dbProxy, token, access, capabilityStorage string) {
	registerServeWithLogicalName(database, token, access, capabilityStorage, "")
}

func registerManagedServeFromCapability(
	database *dbProxy,
	token string,
	access string,
	capabilityStorage string,
	logicalName string,
) {
	registerServeWithLogicalName(database, token, access, capabilityStorage, logicalName)
}

func registerServeWithLogicalName(
	database *dbProxy,
	token string,
	access string,
	capabilityStorage string,
	logicalName string,
) {
	if database == nil || database.tenant == nil || token == "" {
		return
	}
	capabilityStorage = strings.TrimSpace(capabilityStorage)
	if capabilityStorage == "" {
		capabilityStorage = database.dbName
	}
	if strings.ToLower(strings.TrimSpace(access)) != "readwrite" {
		access = "readonly"
	} else {
		access = "readwrite"
	}
	serveRegMu.Lock()
	serveReg[serveDBKey(database.tenant, database.dbName)] = serveConfig{
		token: token, access: access, engine: database.engine, database: database,
		capabilityStorage: capabilityStorage, logicalName: strings.TrimSpace(logicalName),
	}
	serveRegMu.Unlock()
}

// effectiveServeConfigLocked resolves node-managed databases through the
// current source declaration. Managed entries never become an independent
// credential authority merely because they outlive the generation that
// registered them.
func effectiveServeConfigLocked(t *Tenant, config serveConfig) (serveConfig, bool) {
	if config.database == nil {
		return serveConfig{}, false
	}
	if config.database.sourceDeclared {
		return config, true
	}
	capabilityStorage := strings.TrimSpace(config.capabilityStorage)
	if capabilityStorage == "" {
		return serveConfig{}, false
	}
	capability, found := serveReg[serveDBKey(t, capabilityStorage)]
	if !found || capability.database == nil || !capability.database.sourceDeclared ||
		capability.dropping || capability.engine != config.engine || capability.token == "" {
		return serveConfig{}, false
	}
	config.token = capability.token
	config.access = capability.access
	return config, true
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
		if ok && c.dropping {
			return "", serveConfig{}, false
		}
		if !ok {
			return "", serveConfig{}, false
		}
		c, ok = effectiveServeConfigLocked(t, c)
		if !ok {
			return "", serveConfig{}, false
		}
		return dbName, c, true
	}
	prefix := tenantScopeKey(t) + "|"
	var outName string
	var outCfg serveConfig
	found := 0
	for key, cfg := range serveReg {
		if !strings.HasPrefix(key, prefix) || cfg.dropping {
			continue
		}
		cfg, available := effectiveServeConfigLocked(t, cfg)
		if !available {
			continue
		}
		outName = strings.TrimPrefix(key, prefix)
		outCfg = cfg
		found++
	}
	if found != 1 {
		return "", serveConfig{}, false
	}
	return outName, outCfg, true
}

func beginDropServe(t *Tenant, dbName string, database *dbProxy) (serveConfig, bool, error) {
	if t == nil || database == nil {
		return serveConfig{}, false, fmt.Errorf("kitdb: database capability is unavailable")
	}
	key := serveDBKey(t, dbName)
	serveRegMu.Lock()
	defer serveRegMu.Unlock()
	config, found := serveReg[key]
	if !found || config.database != database || config.engine != "kitdb" {
		return serveConfig{}, false, nil
	}
	if config.dropping {
		return serveConfig{}, true, fmt.Errorf("kitdb: database %q is already being dropped", dbName)
	}
	config.dropping = true
	serveReg[key] = config
	return config, true, nil
}

func cancelDropServe(t *Tenant, dbName string, database *dbProxy) {
	if t == nil || database == nil {
		return
	}
	key := serveDBKey(t, dbName)
	serveRegMu.Lock()
	config, found := serveReg[key]
	if found && config.database == database && config.dropping {
		config.dropping = false
		serveReg[key] = config
	}
	serveRegMu.Unlock()
}

func finishDropServe(t *Tenant, dbName string, database *dbProxy) {
	if t == nil || database == nil {
		return
	}
	key := serveDBKey(t, dbName)
	serveRegMu.Lock()
	config, found := serveReg[key]
	if found && config.database == database && config.dropping {
		delete(serveReg, key)
	}
	serveRegMu.Unlock()

	schemaKey := tenantScopeKey(t) + "|kitdb|" + dbName
	schemaRegMu.Lock()
	if schemaReg[schemaKey] == database {
		delete(schemaReg, schemaKey)
	}
	schemaRegMu.Unlock()
}

func removeManagedServe(t *Tenant, dbName string) {
	if t == nil {
		return
	}
	key := serveDBKey(t, dbName)
	serveRegMu.Lock()
	config, found := serveReg[key]
	if found && config.database != nil && !config.database.sourceDeclared {
		delete(serveReg, key)
	}
	serveRegMu.Unlock()

	schemaKey := tenantScopeKey(t) + "|kitdb|" + dbName
	schemaRegMu.Lock()
	if database := schemaReg[schemaKey]; database != nil && !database.sourceDeclared {
		delete(schemaReg, schemaKey)
	}
	schemaRegMu.Unlock()
}

type namedServeConfig struct {
	name   string
	config serveConfig
}

// listServes returns a detached, deterministic view of one tenant's declared
// remote databases. Callers must still authorize every returned config; this
// helper does not turn registry membership into permission.
func listServes(t *Tenant, engine string) []namedServeConfig {
	if t == nil {
		return nil
	}
	prefix := tenantScopeKey(t) + "|"
	serveRegMu.Lock()
	entries := make([]namedServeConfig, 0)
	for key, config := range serveReg {
		if !strings.HasPrefix(key, prefix) || engine != "" && config.engine != engine {
			continue
		}
		entries = append(entries, namedServeConfig{
			name: strings.TrimPrefix(key, prefix), config: config,
		})
	}
	serveRegMu.Unlock()
	sort.Slice(entries, func(first, second int) bool {
		return entries[first].name < entries[second].name
	})
	return entries
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
	case "insert", "update", "delete", "replace", "create", "drop", "alter", "truncate", "reindex", "vacuum", "analyze", "attach", "detach":
		return true
	}
	return false
}

// OnGet is the whole point: db.vouchers routes here. A known table → a schema-aware handle; an unknown
// name → an in-band error (so a typo fails loudly, not as an empty result).
func (p *dbProxy) OnGet(key string) value.Value {
	if err := p.refreshKitDBCatalogDefinitions(); err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	tables, definitions := p.schemaSnapshot()
	cols, ok := tables[key]
	if !ok {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db: no table %q defined in this database", key)}
	}
	return value.New(&SchemaTable{
		tenant: p.tenant, scope: p.scope, engine: p.engine, dbName: p.dbName,
		allowDrop: p.allowDrop, migrate: p.migrate, table: key, columns: cols, siblings: tables,
		definition: definitions[key], definitions: definitions, transaction: p.transaction,
	})
}

// OnCompare and OnInvoke complete the ProxyHandler interface (a dbProxy that only implemented OnGet
// would fail the v.V.(ProxyHandler) assertion, so nothing would resolve). db itself is not compared
// or called; a bare-property access that arrives as an invoke is routed to the table lookup.
func (p *dbProxy) OnCompare(_ string, _ value.Value) value.Value { return value.Value{K: value.Nil} }
func (p *dbProxy) OnInvoke(method string, args ...value.Value) value.Value {
	if method == "transaction" && p.engine == "kitdb" {
		return p.kitDBTransaction(args...)
	}
	return p.OnGet(method)
}

// ---- SchemaTable: the schema-aware wrapper around the ordinary query builder ----

type SchemaTable struct {
	tenant          *Tenant
	scope           *requestscope.Scope
	engine          string // "sqlite" | "kitdb"
	dbName          string
	allowDrop       bool
	migrate         bool
	table           string
	columns         map[string]*ColumnSpec
	siblings        map[string]map[string]*ColumnSpec // every table in this db (for FK targets to exist)
	definition      *StructDef
	definitions     map[string]*StructDef
	transaction     *kitDBRecordTransaction
	referential     *kitDBReferentialActionContext
	catalogRequired bool

	// Physical index state is loaded from the same durable snapshot as lifecycle
	// metadata. The planner uses indexGenerations, while writes may additionally
	// maintain a source generation until an online replacement cuts over.
	inactiveIndexes  map[string]struct{}
	indexGenerations map[string]uint64
	writeIndexes     []kitDBPhysicalIndex
	indexEpoch       uint64
	rowGeneration    uint64
	writeRows        []kitDBPhysicalRow
	rowEpoch         uint64
	readyTransaction uint64
	statistics       *kitDBStatistics
	statisticsState  string

	q       *query.Query
	failed  bool
	failMsg string

	searchActive           bool   // set by .search("…") — List runs the full-text path instead of the plain query
	searchText             string // the query string passed to .search()
	searchFields           []string
	searchIdentifierPrefix string
	searchHitCount         int
	limitN                 int // .limit(n) captured for the search path (the plain path uses the builder's own)
}

func (t *SchemaTable) fail(msg string) *SchemaTable { t.failed = true; t.failMsg = msg; return t }
func (t *SchemaTable) failVal() value.Value         { return value.Value{K: value.Invalid, V: t.failMsg} }

// source builds the SQL blueprint for SQLite. KitDB does not expose an
// SQL source; it consumes builder state through ExecutionPlan instead.
func (t *SchemaTable) source() *SQLite {
	if t.engine == "kitdb" {
		return nil
	}
	return sqliteForRequest(t.tenant, t.dbName, t.scope)
}

func (t *SchemaTable) builder() *query.Query {
	if t.q == nil {
		if t.engine == "kitdb" {
			t.q = query.New(nil, tenantLambdaExecutor{tenant: t.tenant, requestScope: t.scope}).Table(t.table)
		} else {
			t.q = t.source().Table(t.table)
		}
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
	t.limitN = n
	t.builder().Limit(n)
	return t
}

func (t *SchemaTable) Offset(n int) *SchemaTable {
	if t.failed {
		return t
	}
	if n < 0 {
		n = 0
	}
	t.builder().Offset(n)
	return t
}

func (t *SchemaTable) Skip(n int) *SchemaTable { return t.Offset(n) }

// Search opens a full-text query over the table's .searchable() columns: db.posts.search("react").
// It is a narrowing step, so it chains like the rest — db.posts.search("react").limit(24).list() —
// and List() runs the ranked search instead of the plain query. Variadic so the VM treats it as a
// callable, not a getter.
func (t *SchemaTable) Search(args ...value.Value) *SchemaTable {
	if t.failed {
		return t
	}
	if len(args) == 0 || args[0].K != value.String {
		return t.fail("db: search(text) needs a query string")
	}
	t.searchActive = true
	t.searchText = args[0].String()
	return t
}

// ---- terminals ----

func (t *SchemaTable) List(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	if t.engine == "kitdb" {
		if t.searchActive {
			return t.runSearch()
		}
		return t.kitDBList(args...)
	}
	if t.searchActive {
		return t.runSearch()
	}
	return coerceResult(t.columns, t.builder().List(args...))
}

// All is the collection-style spelling of the terminal; search reads naturally as
// db.posts.search("react").limit(24).all().
func (t *SchemaTable) All(args ...value.Value) value.Value { return t.List(args...) }

func (t *SchemaTable) First(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	if t.engine == "kitdb" {
		return t.kitDBFirst(args...)
	}
	return coerceResult(t.columns, t.builder().First(args...))
}

// Find looks a row up by primary key — the schema knows which column that is.
func (t *SchemaTable) Find(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	if t.engine == "kitdb" {
		return t.kitDBFind(args...)
	}
	return coerceResult(t.columns, t.builder().Find(args...))
}

// Count is COUNT(*) through the builder — not list().length, which would load every row to count it.
func (t *SchemaTable) Count(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	if t.engine == "kitdb" {
		return t.kitDBCount(args...)
	}
	return t.builder().Count(args...)
}

// Exists is a cheap SELECT 1 … LIMIT 1 existence check (returns a bool), not list().length > 0.
func (t *SchemaTable) Exists(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	if t.engine == "kitdb" {
		return t.kitDBExists(args...)
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
	if t.engine == "kitdb" {
		return t.kitDBCreate(row)
	}
	result := coerceResult(t.columns, t.source().Table(t.table).Create(value.New(row)))
	return result
}

// fillRow is the shared create logic for both local SQLite and shared entity storage: validate every
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
		if spec.kind == "enum" && !v.IsNil() {
			if v.K != value.String {
				return nil, fmt.Sprintf("column %q choice must be a string or null", k)
			}
			if !inEnum(spec, v.String()) {
				return nil, fmt.Sprintf("column %q must be one of the choice values %v (got %q)", k, spec.enumVals, v.String())
			}
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
	if t.engine == "kitdb" {
		return t.kitDBUpdate(args...)
	}
	if len(args) > 0 && args[0].K == value.Map {
		row, errMsg := coerceWriteRow(t.columns, args[0].Map())
		if errMsg != "" {
			return value.Value{K: value.Invalid, V: fmt.Sprintf("db.update: table %q %s", t.table, errMsg)}
		}
		applyTouch(t.columns, row) // now().onUpdate() columns refresh on every update
		args[0] = value.New(row)
	}
	result := t.builder().Update(args...)
	return result
}

// Delete removes the matching rows for real (DELETE FROM … WHERE …). The schema DSL declares exactly
// the columns it has, so there is no hidden `deleted_at` soft-delete convention here — the base query
// builder's Delete() is a soft delete that assumes such a column, which a schema table need not have.
// For soft delete, declare a `deleted_at` column and update({ deleted_at: now() }) explicitly.
func (t *SchemaTable) Delete(_ ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	if t.engine == "kitdb" {
		return t.kitDBDelete()
	}
	result := t.builder().Remove()
	return result
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
	if t.engine == "kitdb" {
		return t.kitDBPlan()
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
	if t.engine == "kitdb" {
		return t.ensureKitDBStruct()
	}
	// Migrate EVERY table in this database, not just the one being queried, so a foreign key's target
	// table exists before any insert (SQLite allows creating a child before its parent, but the parent
	// must exist by insert time). Ordering does not matter for CREATE, so a plain name order is fine.
	// Each table is cached individually by its own schema hash.
	tables := t.siblings
	if tables == nil {
		tables = map[string]map[string]*ColumnSpec{t.table: t.columns}
	}
	for _, name := range sortedTableNames(tables) {
		cols := tables[name]
		key := t.engine + ":" + t.tenant.resolve(".data", t.dbName) + "::" + name + "::" + schemaHash(cols)
		if _, done := ensuredTables.Load(key); done {
			continue
		}
		// Do NOT cache on failure: a migration that could not apply (e.g. the ALTER did not take because
		// another process held the database) must be RETRIED on the next request, not silently marked done.
		if err := migrate(t.source().db(), name, cols, false, t.allowDrop); err != nil {
			fmt.Printf("[db.migrate] ERROR table %q: %v — not applied; will retry next request\n", name, err)
			return err
		}
		ensuredTables.Store(key, true)
	}
	return nil
}

// schemaDDL builds CREATE TABLE IF NOT EXISTS from the schema. `extra` prepends raw column definitions
// (the entity world passes `"identity" TEXT NOT NULL` so shared rows can be scoped).
func schemaDDL(table string, columns map[string]*ColumnSpec, extra ...string) string {
	defs := make([]string, 0, len(columns)+len(extra))
	defs = append(defs, extra...)
	for _, n := range orderedColumns(columns) { // lay the table out AS DECLARED, not alphabetically
		defs = append(defs, columnSQL(n, columns[n]))
	}
	defs = append(defs, sqlCompositeForeignConstraints(columns)...)
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %q (%s)", table, strings.Join(defs, ", "))
}

func columnSQL(name string, c *ColumnSpec) string {
	parts := []string{fmt.Sprintf("%q %s", name, storageClass(c.kind))}
	if c.primary {
		parts = append(parts, "PRIMARY KEY")
	}
	// A primary key is NOT NULL — SQLite otherwise lets a non-INTEGER PRIMARY KEY (e.g. our TEXT kitid)
	// hold NULLs, a legacy quirk that surprises clients ("primary key can be null"). Emit NOT NULL for
	// any primary key, not just columns the caller marked notNull.
	if c.notNull || c.primary {
		parts = append(parts, "NOT NULL")
	}
	if c.unique {
		parts = append(parts, "UNIQUE")
	}
	for _, check := range c.checks {
		prefix := ""
		if check.name != "" {
			prefix = fmt.Sprintf("CONSTRAINT %q ", check.name)
		}
		parts = append(parts, fmt.Sprintf(
			"%sCHECK (%q %s %s)", prefix, name, check.operator, sqlLiteral(coerceWrite(c.kind, check.value)),
		))
	}
	// Foreign key: REFERENCES target(column) [ON DELETE …] [ON UPDATE …]. Emitted inline so the rebuild
	// (which reuses columnSQL) preserves the constraint automatically.
	if c.fk != nil && c.fk.name == "" && c.fk.table != "" {
		ref := fmt.Sprintf("REFERENCES %q(%q)", c.fk.table, c.fk.column)
		if strings.TrimSpace(c.fk.onDelete) != "" {
			ref += " ON DELETE " + fkAction(c.fk.onDelete)
		}
		if strings.TrimSpace(c.fk.onUpdate) != "" {
			ref += " ON UPDATE " + fkAction(c.fk.onUpdate)
		}
		parts = append(parts, ref)
	}
	// Defaults are applied in Create() (kitid/defaultNow need engine involvement anyway), so the DDL
	// carries no DEFAULT clause in this spike.
	return strings.Join(parts, " ")
}

func sqlCompositeForeignConstraints(columns map[string]*ColumnSpec) []string {
	groups, err := collectColumnForeignGroups(columns)
	if err != nil {
		return nil
	}
	constraints := make([]string, 0, len(groups))
	for _, group := range groups {
		local := make([]string, len(group.columns))
		target := make([]string, len(group.references))
		for index, column := range group.columns {
			local[index] = fmt.Sprintf("%q", column)
			target[index] = fmt.Sprintf("%q", group.references[index].column)
		}
		reference := group.references[0]
		definition := fmt.Sprintf(
			"CONSTRAINT %q FOREIGN KEY (%s) REFERENCES %q (%s)",
			group.name, strings.Join(local, ", "), reference.table, strings.Join(target, ", "),
		)
		if strings.TrimSpace(reference.onDelete) != "" {
			definition += " ON DELETE " + fkAction(reference.onDelete)
		}
		if strings.TrimSpace(reference.onUpdate) != "" {
			definition += " ON UPDATE " + fkAction(reference.onUpdate)
		}
		constraints = append(constraints, definition)
	}
	return constraints
}

// ---- indexes declared with .index() ----

type indexDef struct {
	name            string
	id              string
	columns         []string // in index order
	filter          []indexCond
	unique          bool
	implicitPrimary bool
}

// partialWhere renders an index's filter conditions to a WHERE clause, sorted by column so the SQL is
// deterministic (order does not matter for AND). Values are emitted as SQL literals (never raw text).
func partialWhere(filter []indexCond) string {
	if len(filter) == 0 {
		return ""
	}
	conds := append([]indexCond{}, filter...)
	sort.Slice(conds, func(i, j int) bool { return conds[i].col < conds[j].col })
	parts := make([]string, 0, len(conds))
	for _, c := range conds {
		if c.val.IsNil() {
			parts = append(parts, fmt.Sprintf("%q IS NULL", c.col))
		} else {
			parts = append(parts, fmt.Sprintf("%q = %s", c.col, sqlLiteral(c.val)))
		}
	}
	return strings.Join(parts, " AND ")
}

// collectIndexes turns per-column .index() memberships into concrete index definitions: an unnamed
// .index() becomes a single-column index named idx_<table>_<col>; columns sharing a name become one
// composite ordered by explicit position when given, else by declaration order (seq). unique/where are
// taken from whichever member set them. The result is sorted by name so it is deterministic.
func collectIndexes(table string, columns map[string]*ColumnSpec) []indexDef {
	type member struct {
		col string
		ref colIndexRef
		seq uint64
	}
	groups := map[string][]member{}
	var out []indexDef
	for _, colName := range orderedColumns(columns) { // stable iteration
		for _, ref := range columns[colName].indexes {
			if ref.name == "" {
				out = append(out, indexDef{
					name:    fmt.Sprintf("idx_%s_%s", table, colName),
					id:      ref.id,
					columns: []string{colName},
					filter:  ref.filter,
				})
				continue
			}
			groups[ref.name] = append(groups[ref.name], member{col: colName, ref: ref, seq: columns[colName].seq})
		}
	}
	for name, ms := range groups {
		allHavePos := true
		for _, m := range ms {
			if m.ref.pos == 0 {
				allHavePos = false
				break
			}
		}
		sort.SliceStable(ms, func(i, j int) bool {
			if allHavePos {
				return ms[i].ref.pos < ms[j].ref.pos
			}
			return ms[i].seq < ms[j].seq
		})
		idx := indexDef{name: name}
		for _, m := range ms {
			if idx.id == "" {
				idx.id = m.ref.id
			}
			idx.columns = append(idx.columns, m.col)
			if len(m.ref.filter) > 0 && len(idx.filter) == 0 {
				idx.filter = m.ref.filter
			}
		}
		out = append(out, idx)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func collectSQLIndexes(table string, columns map[string]*ColumnSpec) []indexDef {
	indexes := collectIndexes(table, columns)
	groups, err := collectColumnUniqueGroups(columns)
	if err != nil {
		return indexes
	}
	for _, group := range groups {
		indexes = append(indexes, indexDef{
			name: group.name, columns: append([]string(nil), group.columns...), unique: true,
		})
	}
	sort.Slice(indexes, func(left, right int) bool { return indexes[left].name < indexes[right].name })
	return indexes
}

func indexSQL(table string, idx indexDef) string {
	cols := make([]string, len(idx.columns))
	for i, c := range idx.columns {
		cols[i] = fmt.Sprintf("%q", c)
	}
	where := ""
	if w := partialWhere(idx.filter); w != "" {
		where = " WHERE " + w
	}
	modifier := ""
	if idx.unique {
		modifier = " UNIQUE"
	}
	return fmt.Sprintf("CREATE%s INDEX IF NOT EXISTS %q ON %q (%s)%s", modifier, idx.name, table, strings.Join(cols, ", "), where)
}

func liveIndexColumns(db *sql.DB, name string) ([]string, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA index_info(%q)", name))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var seqno, cid int
		var col sql.NullString
		if err := rows.Scan(&seqno, &cid, &col); err != nil {
			return nil, err
		}
		cols = append(cols, col.String)
	}
	return cols, rows.Err()
}

// liveIndexSQL returns the CREATE INDEX text SQLite stored for an index (empty if it does not exist).
func liveIndexSQL(db *sql.DB, name string) (string, error) {
	var s sql.NullString
	err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&s)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return s.String, err
}

// normIndexSQL normalizes a CREATE INDEX statement for comparison: lowercase, drop "if not exists",
// collapse whitespace. SQLite stores the sql it was given (without IF NOT EXISTS), so comparing this
// against our generated statement detects any change to columns, order, UNIQUE, or the partial WHERE.
func normIndexSQL(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "if not exists ", "")
	return strings.Join(strings.Fields(s), " ")
}

// syncIndexes brings the table's indexes in line with the schema: CREATE any declared index that is
// missing, and DROP+CREATE one whose definition (columns, order, UNIQUE, or partial WHERE) changed.
// Indexes present on the table but not in the schema are LEFT ALONE (dropping an unknown/manual index
// is not the schema's call — and an index holds no data, so leaving a stale one costs nothing but disk).
func syncIndexes(db *sql.DB, table string, want []indexDef) error {
	for _, idx := range want {
		wantSQL := indexSQL(table, idx)
		liveSQL, err := liveIndexSQL(db, idx.name)
		if err != nil {
			return err
		}
		if liveSQL != "" {
			if normIndexSQL(liveSQL) == normIndexSQL(wantSQL) {
				continue // already matches (columns/order/unique/where all identical)
			}
			if _, err := db.Exec(fmt.Sprintf("DROP INDEX IF EXISTS %q", idx.name)); err != nil {
				return fmt.Errorf("drop index %q: %w", idx.name, err)
			}
		}
		if _, err := db.Exec(wantSQL); err != nil {
			return fmt.Errorf("create index %q: %w", idx.name, err)
		}
	}
	return nil
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

func (t *EntityTable) Offset(n int) *EntityTable {
	if t.failed {
		return t
	}
	if n < 0 {
		n = 0
	}
	t.e = t.builder().Skip(n)
	return t
}

func (t *EntityTable) Skip(n int) *EntityTable { return t.Offset(n) }

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
//                                    the caller opts in with sqlite(..., { drop: true })
// Every change is logged to _kitwork_migrations; re-runs when the schema hash changes.
// The SQLite migration path uses the modernc file format. Indexes /
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
	fieldID     string // stable KitDB identity; never exposed or persisted
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
		if !s.WillApply {
			if s.Destructive {
				return fmt.Sprintf("add field %s %s REFUSED (existing rows cannot be backfilled safely)", s.Column, s.To)
			}
			return fmt.Sprintf("add field %s %s requires explicit migration ({ migrate: true })", s.Column, s.To)
		}
		return fmt.Sprintf("add column %s %s", s.Column, s.To)
	case "field_add":
		if !s.WillApply {
			if s.Destructive {
				return fmt.Sprintf("add field %s %s REFUSED (existing rows cannot be backfilled safely)", s.Column, s.To)
			}
			return fmt.Sprintf("add field %s %s requires explicit migration ({ migrate: true })", s.Column, s.To)
		}
		return fmt.Sprintf("add field %s %s", s.Column, s.To)
	case "rename":
		if !s.WillApply {
			return fmt.Sprintf("rename field %s→%s requires explicit migration ({ migrate: true })", s.From, s.To)
		}
		return fmt.Sprintf("rename field %s→%s", s.From, s.To)
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
	case "primary":
		if s.WillApply {
			return fmt.Sprintf("change primary-key role for %s (empty struct)", s.Column)
		}
		return fmt.Sprintf("change primary-key role for %s REFUSED", s.Column)
	case "field_type":
		if s.WillApply {
			return fmt.Sprintf("change field type %s %s→%s (bounded rewrite + cast)", s.Column, s.From, s.To)
		}
		return fmt.Sprintf("change field type %s %s→%s REFUSED", s.Column, s.From, s.To)
	case "remove":
		if s.WillApply {
			return fmt.Sprintf("remove field %s (bounded rewrite)", s.Column)
		}
		return fmt.Sprintf("remove field %s REFUSED (data would be discarded)", s.Column)
	case "reference":
		if s.WillApply {
			return fmt.Sprintf("change reference for %s (empty struct)", s.Column)
		}
		return fmt.Sprintf("change reference for %s REFUSED (%s→%s)", s.Column, s.From, s.To)
	case "constraint":
		if !s.WillApply {
			return fmt.Sprintf("change constraints for %s requires explicit migration ({ migrate: true })", s.Column)
		}
		return fmt.Sprintf("change constraints for %s", s.Column)
	case "index":
		if !s.WillApply {
			return fmt.Sprintf("rebuild indexes for %s requires explicit migration ({ migrate: true })", s.Column)
		}
		return fmt.Sprintf("rebuild indexes for %s", s.Column)
	case "index_build":
		return fmt.Sprintf("continue resumable indexes for %s after %s", s.Column, s.From)
	case "index_cleanup":
		return fmt.Sprintf("clean retired indexes for %s (%s of %s)", s.Column, s.From, s.To)
	case "metadata":
		if !s.WillApply {
			return fmt.Sprintf("update schema metadata for %s requires explicit migration ({ migrate: true })", s.Column)
		}
		return fmt.Sprintf("update schema metadata for %s", s.Column)
	case "catalog_upgrade":
		return "upgrade KitDB schema catalog to stable field tags"
	case "change":
		return fmt.Sprintf("schema %s→%s requires explicit migration", shortSchemaHash(s.From), shortSchemaHash(s.To))
	case "error":
		return "inspection failed: " + s.From
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
	scope := tenantScopeKey(t)

	schemaRegMu.Lock()
	var proxies []*dbProxy
	for _, p := range schemaReg {
		if p.tenant != nil && tenantScopeKey(p.tenant) == scope {
			proxies = append(proxies, p)
		}
	}
	schemaRegMu.Unlock()
	sort.Slice(proxies, func(i, j int) bool {
		return proxies[i].engine+proxies[i].dbName < proxies[j].engine+proxies[j].dbName
	})

	var plans []TablePlan
	for _, p := range proxies {
		tables, definitions := p.schemaSnapshot()
		if p.engine == "kitdb" {
			path := p.tenant.resolve(".data", filepath.FromSlash(kitDBRel(p.dbName)))
			_, statErr := os.Stat(path)
			var managed *managedKitDB
			var openErr error
			if statErr == nil {
				openErr = p.refreshKitDBCatalogDefinitions()
				if openErr == nil {
					managed, openErr = kitDBForRequest(p.tenant, p.dbName, nil).database()
				}
			}
			for _, name := range p.declaredTables {
				steps := []planStep{{Action: "create", WillApply: true}}
				switch {
				case statErr != nil && !os.IsNotExist(statErr):
					steps = []planStep{{Action: "error", From: statErr.Error()}}
				case openErr != nil:
					steps = []planStep{{Action: "error", From: openErr.Error()}}
				case managed != nil:
					definition := definitions[name]
					planned, err := planKitDBCatalog(managed.database, definition, p.migrate)
					if err != nil {
						steps = []planStep{{Action: "error", From: err.Error()}}
					} else {
						steps = planned
					}
				}
				plans = append(plans, TablePlan{Engine: p.engine, DB: p.dbName, Table: name, Steps: steps})
			}
			if managed != nil {
				managed.Release()
			}
			continue
		}
		// Introspect the live DB only if it already exists — never create a file during a preflight.
		live := map[string]map[string]string{}
		if _, err := os.Stat(p.tenant.resolve(".data", p.dbName)); err == nil {
			if db := (&SchemaTable{tenant: p.tenant, engine: p.engine, dbName: p.dbName}).source().db(); db != nil {
				for name := range tables {
					if cols, err := tableColumns(db, name); err == nil {
						live[name] = cols
					}
				}
			}
		}
		for _, name := range sortedTableNames(tables) {
			steps := planMigration(live[name], tables[name], false, p.allowDrop)
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

// validateSchema catches declaration-time mistakes before any DDL runs. A
// choice default outside its declared values is rejected before any backend.
func validateSchema(columns map[string]*ColumnSpec) error {
	partitionField := ""
	for _, name := range orderedColumns(columns) {
		c := columns[name]
		if c.declareErr != "" {
			return fmt.Errorf("column %q: %s", name, c.declareErr)
		}
		typeInfo, supported := kitdbsql.LookupKind(c.kind)
		if !supported || typeInfo.Kind != c.kind {
			return fmt.Errorf("column %q has unsupported type %q", name, c.kind)
		}
		if c.partition != "" {
			if partitionField != "" {
				return fmt.Errorf("partition() can be declared on only one field per struct (%q and %q)", partitionField, name)
			}
			if typeInfo.Family != kitdbsql.FamilyInteger {
				return fmt.Errorf("column %q: partition() currently requires an integer field", name)
			}
			if c.partition != "hash" && c.partition != "range" {
				return fmt.Errorf("column %q: partition() has unsupported strategy %q", name, c.partition)
			}
			partitionField = name
		}
		if c.analytics {
			switch typeInfo.Family {
			case kitdbsql.FamilyInteger, kitdbsql.FamilySystem, kitdbsql.FamilyFloat, kitdbsql.FamilyBoolean,
				kitdbsql.FamilyText, kitdbsql.FamilyIdentifier, kitdbsql.FamilyChoice:
			default:
				return fmt.Errorf("column %q: analytics() does not support type %q", name, c.kind)
			}
		}
		if c.kind == "enum" && c.hasDefault && !c.def.IsNil() {
			if c.def.K != value.String {
				return fmt.Errorf("column %q choice default must be a string or null", name)
			}
			if !inEnum(c, c.def.String()) {
				return fmt.Errorf("column %q default %q is not one of the choice values %v", name, c.def.String(), c.enumVals)
			}
		}
		if c.fk != nil {
			if c.fk.target == nil || c.fk.table == "" {
				return fmt.Errorf("column %q: ref() target could not be resolved — reference a column of another table in the SAME database", name)
			}
			if c.fk.name == "" && !c.fk.target.primary && !c.fk.target.unique {
				return fmt.Errorf("column %q references %s.%s, which is not a primary key or unique column", name, c.fk.table, c.fk.column)
			}
			if storageClass(c.kind) != storageClass(c.fk.target.kind) {
				return fmt.Errorf("column %q type (%s) does not match its reference target %s.%s (%s)", name, storageClass(c.kind), c.fk.table, c.fk.column, storageClass(c.fk.target.kind))
			}
			if !validFKActionName(c.fk.onDelete) {
				return fmt.Errorf("column %q has unsupported onDelete action %q", name, c.fk.onDelete)
			}
			if !validFKActionName(c.fk.onUpdate) {
				return fmt.Errorf("column %q has unsupported onUpdate action %q", name, c.fk.onUpdate)
			}
			for _, policy := range []struct{ label, action string }{
				{"onDelete", c.fk.onDelete},
				{"onUpdate", c.fk.onUpdate},
			} {
				if normalizeFKActionName(policy.action) == "setnull" && (c.notNull || c.primary) {
					return fmt.Errorf("column %q is NOT NULL, so %s:\"setNull\" is impossible", name, policy.label)
				}
			}
		}
	}
	uniqueGroups, err := collectColumnUniqueGroups(columns)
	if err != nil {
		return err
	}
	uniqueNames := make(map[string]string, len(uniqueGroups))
	for _, group := range uniqueGroups {
		uniqueNames[strings.ToLower(group.name)] = group.name
	}
	for _, column := range orderedColumns(columns) {
		for _, index := range columns[column].indexes {
			if index.name == "" {
				continue
			}
			if unique := uniqueNames[strings.ToLower(index.name)]; unique != "" {
				return fmt.Errorf("index %q conflicts with tuple-unique %q", index.name, unique)
			}
		}
	}
	if _, err := collectColumnForeignGroups(columns); err != nil {
		return err
	}
	return nil
}

// checkpointWAL flushes the write-ahead log into the main database file. A schema change (DDL) that
// lives only in the WAL is NOT reliably visible to a separate SQLite instance/process opening the same
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

func migrate(db *sql.DB, table string, columns map[string]*ColumnSpec, withIdentity, allowDrop bool) (err error) {
	if db == nil {
		return fmt.Errorf("connection unavailable")
	}
	if err := validateSchema(columns); err != nil {
		return fmt.Errorf("schema %q: %w", table, err)
	}
	ensureHistory(db)

	// After the table reaches its final shape (create / alter / rebuild / no-op), sync its .index()
	// definitions — the ONE place that covers every success path, including the rebuild that recreates
	// the table without its indexes. Runs only when the migration itself succeeded.
	defer func() {
		if err == nil {
			if e := syncIndexes(db, table, collectSQLIndexes(table, columns)); e != nil {
				err = e
				return
			}
			checkpointWAL(db)
		}
	}()

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

	// Reorder guard (LOCAL file dbs only — the schema DSL): an ALTER can only APPEND a column, so if the
	// declared layout differs from what appending would produce (an existing table whose columns sit in
	// a different order, or a new column declared in the middle), rebuild to match the declared order.
	// The rebuild preserves ALL data and drops nothing — extras (live columns not in the schema) are
	// kept, appended at the end. The shared/entity world (withIdentity) stays append-only.
	addOrder := newCols
	if !withIdentity {
		live, err := tableColumnsOrdered(db, table)
		if err != nil {
			return fmt.Errorf("introspect order %q: %w", table, err)
		}
		desired := orderedColumns(columns)
		nullablePK, err := liveNullablePK(db, table, columns)
		if err != nil {
			return fmt.Errorf("introspect pk %q: %w", table, err)
		}
		if reorderNeeded(live, desired) || nullablePK {
			// Existing columns are out of declared order, or the primary key is nullable → rebuild to fix
			// the layout / constraint (data preserved, extras kept). rebuild lays every column out via
			// orderedColumns with the current DDL, so new columns land in their declared position and the
			// primary key comes out NOT NULL.
			var keptExtras []string
			for _, name := range live {
				if _, inSchema := columns[name]; !inSchema {
					keptExtras = append(keptExtras, name)
				}
			}
			if err := rebuild(db, table, columns, current, false, false, keptExtras, nil); err != nil {
				return err
			}
			checkpointWAL(db)
			return nil
		}
		// Order is fine — append any new columns in DECLARED order.
		newSet := map[string]bool{}
		for _, n := range newCols {
			newSet[n] = true
		}
		orderedNew := make([]string, 0, len(newCols))
		for _, n := range desired {
			if newSet[n] {
				orderedNew = append(orderedNew, n)
			}
		}
		addOrder = orderedNew
	}

	// Otherwise purely additive: ADD the new columns (static defaults backfill existing rows).
	for _, name := range addOrder {
		stmt := fmt.Sprintf("ALTER TABLE %q ADD COLUMN %s", table, addColumnSQL(name, columns[name]))
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("add %q.%q: %w", table, name, err)
		}
		recordMigration(db, table, "add-column", stmt)
	}

	// Verify the ADDs actually landed. When another process/connection is mid-write on the file, an
	// ALTER has been observed to return no error yet not take effect, which then
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
	for _, name := range orderedColumns(columns) { // rebuild lays columns out AS DECLARED
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
	defs = append(defs, sqlCompositeForeignConstraints(columns)...)

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("rebuild %q begin: %w", table, err)
	}
	fail := func(stage string, e error) error {
		tx.Rollback()
		return fmt.Errorf("rebuild %q rolled back at %s: %w", table, stage, e)
	}
	// Defer foreign-key checks to COMMIT: the DROP-old/RENAME-new dance transiently detaches a table
	// that other tables may reference, which would trip immediate FK enforcement. Referential integrity
	// is intact by commit (the table returns under its original name), so the deferred check passes.
	if _, err := tx.Exec("PRAGMA defer_foreign_keys=ON"); err != nil {
		return fail("defer-fk", err)
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

// liveNullablePK reports whether a column the schema marks as a PRIMARY KEY is currently NULLABLE in
// the live table — SQLite lets a non-INTEGER primary key hold NULLs unless it was declared NOT NULL.
// Such a table is rebuilt so the key becomes NOT NULL; the rebuild is safe because primary-key values
// are never NULL, so the copy cannot violate the new constraint.
func liveNullablePK(db *sql.DB, table string, columns map[string]*ColumnSpec) (bool, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if spec, ok := columns[name]; ok && spec.primary && notnull == 0 {
			return true, nil
		}
	}
	return false, rows.Err()
}

// tableColumnsOrdered returns the LIVE column names in physical (cid) order — used to detect when the
// table's layout no longer matches the schema's declared order.
func tableColumnsOrdered(db *sql.DB, table string) ([]string, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// reorderNeeded reports whether the EXISTING columns sit in a different relative order than the schema
// declares. If so, a data-preserving rebuild is required to lay them out as written (an ALTER cannot
// move a column). New columns (declared but not yet in the table) are ignored — they are simply
// appended by the additive path; only a genuine out-of-order EXISTING column forces a rebuild, so
// adding a column never turns into a rebuild by itself.
func reorderNeeded(live, desired []string) bool {
	pos := make(map[string]int, len(desired))
	for i, n := range desired {
		pos[n] = i
	}
	prev := -1
	for _, n := range live {
		p, ok := pos[n]
		if !ok {
			continue // a column not in the schema (kept extra) — does not constrain order
		}
		if p < prev {
			return true // this column is declared before one that already precedes it → out of order
		}
		prev = p
	}
	return false
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

// orderedColumns returns the column names in DECLARATION order (by seq), so generated DDL lays the
// table out as the schema was written rather than alphabetically. Ties (e.g. columns built outside the
// counter) fall back to name for determinism.
func orderedColumns(columns map[string]*ColumnSpec) []string {
	names := make([]string, 0, len(columns))
	for n := range columns {
		names = append(names, n)
	}
	sort.SliceStable(names, func(i, j int) bool {
		si, sj := columns[names[i]].seq, columns[names[j]].seq
		if si != sj {
			return si < sj
		}
		return names[i] < names[j]
	})
	return names
}

// schemaHash is a deterministic fingerprint of the schema shape — the migration cache key, so an
// edited schema re-runs migrate(). It walks columns in DECLARATION order and includes each column's
// position, so REORDERING the schema changes the hash and triggers a (data-preserving) reorder.
func schemaHash(columns map[string]*ColumnSpec) string {
	h := fnv.New64a()
	for pos, name := range orderedColumns(columns) {
		c := columns[name]
		fmt.Fprintf(h, "%d:%s|%s|%t|%t|%t|%t;", pos, name, c.kind, c.primary, c.notNull, c.unique, c.defaultNow)
		if c.fk != nil {
			fmt.Fprintf(h, "ref:%s.%s|%s|%d|%s|%s;", c.fk.table, c.fk.column, c.fk.name, c.fk.pos, c.fk.onDelete, c.fk.onUpdate)
		}
		for _, check := range c.checks {
			fmt.Fprintf(h, "check:%s|%s|%s;", check.name, check.operator, sqlLiteral(check.value))
		}
	}
	// Indexes are part of the shape too — adding/removing/reordering one, or changing UNIQUE/WHERE,
	// re-runs migrate. A fixed table name keeps the hash stable across runs (only the index set moves it).
	for _, idx := range collectIndexes("t", columns) {
		fmt.Fprintf(h, "idx:%s(%s)|%s;", idx.name, strings.Join(idx.columns, ","), partialWhere(idx.filter))
	}
	if groups, err := collectColumnUniqueGroups(columns); err == nil {
		for _, group := range groups {
			fmt.Fprintf(h, "unique:%s(%s);", group.name, strings.Join(group.columns, ","))
		}
	}
	return fmt.Sprintf("%x", h.Sum64())
}
