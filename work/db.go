package work

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/database"
	requestscope "github.com/kitwork/engine/request"
	query "github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

// Database is NOT served by the capability registry, unlike collection/jwt/shortbase/qrcode.
// capabilities/database exists and registers itself, but it is a narrower re-implementation and
// delegating to it would silently regress two things:
//
//   - Connections. The adapter resolves an alias through Scope.DB(), which for a tenant means
//     sqliteFor() — a SQLite file under .data/. It never consults database.Configs, so every
//     configured Postgres/MySQL connection would quietly become an empty local SQLite file.
//   - Lambda predicates. The adapter builds queries with query.New(exec, nil). Every "magic"
//     branch in the query builder is gated on `q.vm != nil`, so the lambda is never compiled to
//     SQL. It does not error either: measured, `table("users").where(u => u.active)` builds
//     `SELECT * FROM "users" WHERE "" IS NULL LIMIT 60` — an EMPTY column identifier — which the
//     driver either rejects or evaluates to nothing. Kitwork's signature query form would break.
//
// A previous version called w.Capability("database") and discarded the result, which read like
// wiring that was nearly finished. It was not: it was a no-op in front of the real implementation
// below. Removed so the code states the truth. Reviving the delegation means teaching the adapter
// about database.Configs and giving it the tenant VM first.
func (w *KitWork) Database() *Database {
	return &Database{
		tenant:       w.tenant,
		requestScope: w.requestScope,
		config:       &database.Config{},
	}
}

type Database struct {
	tenant       *Tenant
	requestScope *requestscope.Scope
	config       *database.Config
	sqlDB        *sql.DB
	tx           *sql.Tx

	preset *database.Config
}

func (d *Database) Connection() *Database {
	if d.sqlDB == nil {
		if config, ok := database.Configs["default"]; ok {
			d.sqlDB = d.tenant.lookupDatabase(&config)
		}
	}
	return d
}

func (d *Database) Connected() *Database {
	return d.Connection()
}

func (d *Database) Connect(vals ...value.Value) *Database {
	if len(vals) == 0 && d.sqlDB != nil {
		return d
	}

	if len(vals) == 0 && d.preset != nil {
		if conn := d.tenant.lookupDatabase(d.preset); conn != nil {
			d.sqlDB = conn
			return d
		}
		if t := strings.ToLower(d.preset.Type); t == "sqlite" || t == "sqlite3" || t == "turso" {
			// Local file backends (sqlite AND turso) need their parent .data/ folder to exist before
			// the driver opens the file.
			if dir := filepath.Dir(d.preset.Name); dir != "." && dir != "" {
				_ = os.MkdirAll(dir, 0o755)
			}
		}
		conn, err := d.tenant.openDatabase(d.preset)
		if err != nil {
			fmt.Printf("[DB] Failed to connect %s: %v\n", d.preset.Alias, err)
			return d
		}
		d.sqlDB = conn
		return d
	}

	alias := "default"
	switch {
	case len(vals) == 0:
	case len(vals) == 1 && vals[0].K == value.String:
		alias = vals[0].String()
	default:
		// The public contract accepts only a host-declared alias. Letting tenant code submit
		// type/host/user/password here bypasses connection policy and turns the database API into
		// an unrestricted network dialer.
		fmt.Println("[DB] Dynamic connection configuration is not allowed; configure an alias in the host manifest")
		return d
	}

	if alias == "" {
		alias = "default"
	}

	if dbCfg, ok := database.Configs[alias]; ok {
		if dbConn := d.tenant.lookupDatabase(&dbCfg); dbConn != nil {
			d.sqlDB = dbConn
		} else {
			dbConn, err := d.tenant.openDatabase(&dbCfg)
			if err != nil {
				fmt.Printf("[DB] Failed to connect to configured database '%s': %v\n", alias, err)
			} else {
				d.sqlDB = dbConn
			}
		}
	} else if alias == "default" {
		// Storage choice is part of the app contract. Creating a local SQLite database here
		// made a missing shared/external connection look like a healthy but empty database.
		// Site-local storage remains available through the explicit `sqlite` API.
		fmt.Println("[DB] Default connection is not configured; use sqlite for local data or configure database alias 'default'")
	} else {
		fmt.Printf("Database connection with alias '%s' not found\n", alias)
	}
	return d
}

func (d *Database) db() *sql.DB {
	return d.Connect().sqlDB
}

func (d *Database) Config(config *database.Config) *Database {
	d.config = config
	return d
}

func (d *Database) NewQuery() *query.Query {
	var exec query.Executor
	if dbConn := d.db(); dbConn != nil {
		// Do not assign a typed nil *sql.DB to the Executor interface: the interface itself would
		// compare non-nil and query execution would panic inside database/sql.
		exec = dbConn
	}
	if d.tx != nil {
		exec = d.tx
	}
	return query.New(exec, tenantLambdaExecutor{tenant: d.tenant, requestScope: d.requestScope})
}

func (d *Database) Table(table string) *query.Query {
	return d.NewQuery().Table(table)
}

func (d *Database) Select(fields ...string) *query.Query {
	return d.NewQuery().Select(fields...)
}

func (d *Database) Where(args ...value.Value) *query.Query {
	return d.NewQuery().Where(args...)
}

func (d *Database) Limit(limit int) *query.Query {
	return d.NewQuery().Limit(limit)
}

func (d *Database) Find(args ...value.Value) value.Value {
	return d.NewQuery().Find(args...)
}

func (d *Database) First(args ...value.Value) value.Value {
	return d.NewQuery().First(args...)
}

func (d *Database) List(args ...value.Value) value.Value {
	return d.NewQuery().List(args...)
}

func (d *Database) Count(args ...value.Value) value.Value {
	return d.NewQuery().Count(args...)
}

func (d *Database) Exists(args ...value.Value) value.Value {
	return d.NewQuery().Exists(args...)
}

func (d *Database) Create(args ...value.Value) value.Value {
	return d.NewQuery().Create(args...)
}

func (d *Database) Update(args ...value.Value) value.Value {
	return d.NewQuery().Update(args...)
}

func (d *Database) Save(args ...value.Value) value.Value {
	return d.NewQuery().Save(args...)
}

func (d *Database) Delete() value.Value {
	return d.NewQuery().Delete()
}

func (d *Database) Remove() value.Value {
	return d.NewQuery().Remove()
}

func (d *Database) OrderBy(col string, dir ...string) *query.Query {
	return d.NewQuery().OrderBy(col, dir...)
}

func (d *Database) GroupBy(cols ...string) *query.Query {
	return d.NewQuery().GroupBy(cols...)
}

func (d *Database) Join(args ...value.Value) *query.Query {
	return d.NewQuery().Join(args...)
}

func (d *Database) Atomic(args ...value.Value) value.Value {
	if len(args) == 0 || args[0].K != value.Func {
		return value.Value{K: value.Nil}
	}
	lambda, ok := args[0].V.(*value.Lambda)
	if !ok {
		return value.Value{K: value.Nil}
	}

	dbConn := d.db()
	if dbConn == nil {
		return value.Value{K: value.Invalid, V: "database not connected"}
	}
	tx, err := dbConn.Begin()
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}

	txDb := &Database{
		tenant:       d.tenant,
		requestScope: d.requestScope,
		config:       d.config,
		sqlDB:        d.sqlDB,
		tx:           tx,
	}
	txVal := value.New(txDb)

	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	result := (tenantLambdaExecutor{tenant: d.tenant, requestScope: d.requestScope}).ExecuteLambda(lambda, []value.Value{txVal})

	if result.K == value.Invalid {
		tx.Rollback()
		return result
	}

	if err := tx.Commit(); err != nil {
		tx.Rollback()
		return value.Value{K: value.Invalid, V: err.Error()}
	}

	return result
}
