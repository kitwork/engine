package work

import (
	"fmt"

	"github.com/kitwork/engine/database"
	query "github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

// Shared-database access, scoped to the calling app's identity.
//
// `sqlite` gives a site its own file, which is isolated because nothing else can reach it. This is
// the other case: ONE Postgres, many apps, rows separated by an `identity` column. There the
// isolation has to be produced rather than assumed, and the only reliable way to do that is to make
// an unscoped query IMPOSSIBLE TO WRITE — the same tactic the language uses when it refuses
// `while`, rather than trying to detect runaway loops afterwards.
//
// So this type never hands out the underlying *query.Query. Holding one means holding .Raw(), and a
// single raw string would undo every condition below it. Every method here returns *Entities, and
// identity predicate is attached at open() before an author can touch anything:
//
//	import { entity } from "kitwork";
//	entity.table("posts").where("status", "published").list()   → … WHERE identity = <this app> AND …
//	entity.table("posts").create({ title: "x" })                → identity is set by the engine
//	entity.table("posts").where("id", 3).delete()               → cannot reach another app's row
//
// The identity comes from the tenant, never from an argument. There is no API to name a different
// one, so a site cannot ask for another site's rows even by mistake.
//
// An app with no identity — a single-tenant layout, or a call made outside a request — gets an
// error instead of an unscoped query. That was a deliberate choice: silently returning every row
// would turn a missing identity into a data leak, and the failure would look like a feature.
const identityColumn = "identity"

// Entities is the JS-facing handle: one table of the shared database, permanently bound to this
// app's identity.
type Entities struct {
	tenant   *Tenant
	table    string
	identity string
	err      error // a construction failure, surfaced by the first terminal call

	q *query.Query
}

// Entity opens this app's slice of the shared database. The table is named FIRST so the identity
// predicate is attached before any author-supplied condition, leaving no code path that builds a
// query without one.
func (w *KitWork) Entity() *EntityScope {
	return &EntityScope{tenant: w.tenant}
}

type EntityScope struct{ tenant *Tenant }

// Table binds a table. Errors are carried rather than thrown so that a mistake surfaces at the point the query runs, with the rest of the chain intact — the same shape as the other builders.
func (e *EntityScope) Table(table string) *Entities {
	d := &Entities{tenant: e.tenant, table: table}

	switch {
	case e.tenant == nil:
		d.err = fmt.Errorf("entity: no tenant in scope")
		return d
	case table == "":
		d.err = fmt.Errorf("entity: table name is required")
		return d
	case database.System == nil:
		d.err = fmt.Errorf("entity: no shared database is connected (system)")
		return d
	}

	d.identity = e.tenant.appID()
	if d.identity == "" {
		// Refusing here is the whole point: an app with no identity has no slice of a shared table,
		// and answering with everyone's rows would be a leak wearing the shape of a result.
		d.err = fmt.Errorf("entity: this app has no identity, so a shared query cannot be scoped — " +
			"use sqlite for site-local data, or run under apps/<identity>/<domain>")
		return d
	}

	d.q = query.New(database.System, e.tenant.vm).
		Table(table).
		Where(value.New(identityColumn), value.New(d.identity))
	return d
}

// ---- narrowing: every method returns *Entities, never the query underneath ----

func (d *Entities) Where(args ...value.Value) *Entities {
	if d.q != nil {
		// Author conditions are ANDed onto the identity predicate that is already there, so no
		// combination of them can widen the result past this app.
		d.q = d.q.Where(args...)
	}
	return d
}

func (d *Entities) OrderBy(column string, direction ...string) *Entities {
	if d.q != nil {
		d.q = d.q.OrderBy(column, direction...)
	}
	return d
}

func (d *Entities) GroupBy(columns ...string) *Entities {
	if d.q != nil {
		d.q = d.q.GroupBy(columns...)
	}
	return d
}

func (d *Entities) Select(fields ...string) *Entities {
	if d.q != nil {
		d.q = d.q.Select(fields...)
	}
	return d
}

func (d *Entities) Limit(n int) *Entities {
	if d.q != nil {
		d.q = d.q.Limit(n)
	}
	return d
}

func (d *Entities) Skip(n int) *Entities {
	if d.q != nil {
		d.q = d.q.Skip(n)
	}
	return d
}

// ---- terminals ----

func (d *Entities) List(args ...value.Value) value.Value {
	return d.run(func() value.Value { return d.q.List(args...) })
}
func (d *Entities) Find(args ...value.Value) value.Value {
	return d.run(func() value.Value { return d.q.Find(args...) })
}
func (d *Entities) First(args ...value.Value) value.Value {
	return d.run(func() value.Value { return d.q.First(args...) })
}
func (d *Entities) Count(args ...value.Value) value.Value {
	return d.run(func() value.Value { return d.q.Count(args...) })
}
func (d *Entities) Exists(args ...value.Value) value.Value {
	return d.run(func() value.Value { return d.q.Exists(args...) })
}

// Create stamps the identity itself. Any identity in the payload is REPLACED, not merged: letting
// an author choose it would make the column a suggestion instead of a boundary.
func (d *Entities) Create(args ...value.Value) value.Value {
	return d.run(func() value.Value {
		return d.q.Create(d.stamped(args)...)
	})
}

// Update and Delete inherit the identity predicate attached at open(), so a row belonging to
// another app is not merely hidden from reads — it cannot be written or removed either.
func (d *Entities) Update(args ...value.Value) value.Value {
	return d.run(func() value.Value {
		return d.q.Update(d.stamped(args)...)
	})
}

func (d *Entities) Delete() value.Value { return d.run(func() value.Value { return d.q.Delete() }) }
func (d *Entities) Remove() value.Value { return d.run(func() value.Value { return d.q.Remove() }) }

// stamped forces the identity onto a written payload.
func (d *Entities) stamped(args []value.Value) []value.Value {
	if len(args) == 0 || !args[0].IsMap() {
		return args
	}
	row, ok := args[0].Interface().(map[string]interface{})
	if !ok {
		return args
	}
	clone := make(map[string]interface{}, len(row)+1)
	for k, v := range row {
		clone[k] = v
	}
	// Written LAST, so it overwrites rather than merges: an identity in the payload is copied above
	// and then replaced here. Skipping it during the copy as well would look like a second guard,
	// but this line is the only one doing the work — verified by removing it and watching the
	// "cannot plant a row under another identity" test fail.
	clone[identityColumn] = d.identity

	out := make([]value.Value, len(args))
	copy(out, args)
	out[0] = value.New(clone)
	return out
}

func (d *Entities) run(fn func() value.Value) value.Value {
	if d.err != nil {
		return value.Value{K: value.Invalid, V: d.err.Error()}
	}
	if d.q == nil {
		return value.Value{K: value.Invalid, V: "entity: query was not initialised"}
	}
	return fn()
}
