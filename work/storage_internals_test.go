package work

import (
	"fmt"
	"strings"

	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/value"
)

// The engine's own tests reach the storage machinery that app code reached until 22/10/2026 through the
// older spellings of kitwork/database: kitdb(…), sqlite(…), struct and the column builders on `database`.
// That machinery stays the engine's (dbProxy, SchemaTable, the KitDB ORM, its predicates, migrations, its
// PostgreSQL wire serving), and the spec's handle runs on it (ManagedDatabase); the spellings
// are errors now (database_module.go). This test-only module builds the same proxies through the Go API
// (newDbProxy), so the internals keep their coverage while no app can write them:
//
//	import * as router from "kitwork/router";
//	import { internals } from "kitwork";
//	const { kitdb, struct, id, text, int } = internals;
//
// It exists only in the test binary: a method of KitWork declared in a _test.go file.
func (w *KitWork) Internals() *storageInternals {
	return &storageInternals{tenant: w.tenant, scope: w.requestScope}
}

type storageInternals struct {
	tenant *Tenant
	scope  *requestscope.Scope
}

// Sqlite binds named structs to a SQLite file of the site's .data/.
func (s *storageInternals) Sqlite(args ...value.Value) value.Value {
	return newDbProxy(s.tenant, s.scope, "sqlite", args...)
}

// Struct is struct({ … }) of the older column builders.
func (s *storageInternals) Struct() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		return newStructDef(args, false)
	})
}

// File is the query builder over one SQLite file of the site's .data/ (SQLite.Table), without a schema.
func (s *storageInternals) File(name string) *SQLite {
	return sqliteForRequest(s.tenant, name, s.scope)
}

func columnFunc(kind string) value.Value {
	return value.NewFunc(func(_ ...value.Value) value.Value {
		return value.New(&ColumnSpec{kind: kind, seq: nextColSeq()})
	})
}

// Identifiers
func (s *storageInternals) Kitid() value.Value  { return columnFunc("kitid") }
func (s *storageInternals) Uuid() value.Value   { return columnFunc("uuid") }
func (s *storageInternals) Serial() value.Value { return columnFunc("serial") } // INTEGER; auto-increment not wired yet

// Text
func (s *storageInternals) Text() value.Value    { return columnFunc("text") }
func (s *storageInternals) Varchar() value.Value { return paramFunc("varchar") } // length is metadata (no-op on sqlite)
func (s *storageInternals) Char() value.Value    { return paramFunc("char") }

// Numbers
func (s *storageInternals) Integer() value.Value { return columnFunc("integer") }
func (s *storageInternals) Int() value.Value     { return columnFunc("integer") } // alias
func (s *storageInternals) Float() value.Value   { return columnFunc("float") }
func (s *storageInternals) Real() value.Value    { return columnFunc("float") }  // alias
func (s *storageInternals) Double() value.Value  { return columnFunc("float") }  // doublePrecision alias
func (s *storageInternals) Decimal() value.Value { return paramFunc("decimal") } // stored as TEXT (exact), read as number

// Boolean
func (s *storageInternals) Bool() value.Value    { return columnFunc("bool") }
func (s *storageInternals) Boolean() value.Value { return columnFunc("bool") } // alias

// Choice declares one string selected from a finite set. The internal "enum"
// kind is retained so existing catalogs and database files need no migration.
func (s *storageInternals) Choice() value.Value {
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
func (s *storageInternals) Datetime() value.Value { return columnFunc("datetime") }
func (s *storageInternals) Date() value.Value     { return columnFunc("date") }
func (s *storageInternals) Time() value.Value     { return columnFunc("time") }
func (s *storageInternals) Year() value.Value     { return columnFunc("year") }  // INTEGER, auto current year on insert
func (s *storageInternals) Month() value.Value    { return columnFunc("month") } // INTEGER, auto current month
func (s *storageInternals) Day() value.Value      { return columnFunc("day") }   // INTEGER, auto current day

// Structured
func (s *storageInternals) Json() value.Value  { return columnFunc("json") }
func (s *storageInternals) Jsonb() value.Value { return columnFunc("jsonb") } // ≡ json on sqlite (both TEXT)
func (s *storageInternals) Array() value.Value { return columnFunc("array") } // JSON array in TEXT
func (s *storageInternals) Blob() value.Value  { return columnFunc("blob") }
func (s *storageInternals) Bytes() value.Value { return columnFunc("blob") } // alias

// Networking & AI
func (s *storageInternals) Ip() value.Value     { return columnFunc("ip") }  // TEXT (no native type)
func (s *storageInternals) Mac() value.Value    { return columnFunc("mac") } // TEXT
func (s *storageInternals) Vector() value.Value { return paramFunc("vector") }

// Presets — the two universal patterns get a one-word name; compose the rest with modifiers.
func (s *storageInternals) Id() value.Value { // = kitid().primaryKey()
	return value.NewFunc(func(_ ...value.Value) value.Value {
		return value.New(&ColumnSpec{kind: "kitid", primary: true, seq: nextColSeq()})
	})
}
func (s *storageInternals) Now() value.Value { // = datetime().defaultNow()
	return value.NewFunc(func(_ ...value.Value) value.Value {
		return value.New(&ColumnSpec{kind: "datetime", defaultNow: true, seq: nextColSeq()})
	})
}
func (s *storageInternals) Updated() value.Value { // = now().onUpdate() — stamped on insert AND every update
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
func (s *storageInternals) Ref() value.Value {
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

// paramFunc builds a column whose first arg is a size/precision/dimension hint (varchar(50),
// decimal(10,2), vector(1536)). The hint is metadata for v1 — sqlite ignores length/precision, and the
// vector is stored as a JSON array — but it is accepted so schemas read the same across engines.
func paramFunc(kind string) value.Value {
	return value.NewFunc(func(_ ...value.Value) value.Value {
		return value.New(&ColumnSpec{kind: kind, seq: nextColSeq()})
	})
}
