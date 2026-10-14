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
		alias:        "default",
	}
}

type Database struct {
	tenant       *Tenant
	requestScope *requestscope.Scope
	config       *database.Config
	sqlDB        *sql.DB
	tx           *sql.Tx
	alias        string

	preset *database.Config
}

// Connection returns an independent handle for one host-declared alias. It
// never mutates the database capability object, so several aliases can be used
// safely in the same handler.
func (d *Database) Connection(vals ...value.Value) *Database {
	handle := d.clone()
	alias, ok := databaseAlias(vals)
	if !ok {
		fmt.Println("[DB] database.connection expects zero arguments or one alias string")
		return handle
	}
	handle.alias = alias
	handle.preset = nil
	handle.config = &database.Config{Alias: alias}
	connection, err := handle.resolveAlias(alias)
	if err != nil {
		fmt.Printf("[DB] Failed to resolve database '%s': %v\n", alias, err)
		return handle
	}
	handle.sqlDB = connection
	return handle
}

// Connected reports whether a host-declared alias can be resolved. Resolving a
// lazy owned KitDB root may prepare that host-owned database.
func (d *Database) Connected(vals ...value.Value) bool {
	alias, ok := databaseAlias(vals)
	if !ok {
		return false
	}
	connection, err := d.resolveAlias(alias)
	return err == nil && connection != nil
}

// Connect is reserved for a future policy-bounded external connector. Alias
// lookup belongs to Connection; accepting an alias here would make the two
// APIs ambiguous again. External connections must currently be declared by
// the host with app.connect/app.postgresql and retrieved with Connection.
func (d *Database) Connect(vals ...value.Value) *Database {
	handle := d.clone()
	if len(vals) == 1 && vals[0].K == value.String {
		fmt.Println("[DB] database.connect no longer accepts an alias; use database.connection(alias)")
		return handle
	}
	fmt.Println("[DB] database.connect external configuration is not enabled for tenant code; declare app.connect(...) and use database.connection(alias)")
	return handle
}

func (d *Database) clone() *Database {
	if d == nil {
		return &Database{config: &database.Config{}}
	}
	return &Database{
		tenant:       d.tenant,
		requestScope: d.requestScope,
		config:       d.config,
		preset:       d.preset,
		alias:        d.alias,
	}
}

func databaseAlias(vals []value.Value) (string, bool) {
	if len(vals) == 0 {
		return "default", true
	}
	if len(vals) != 1 || vals[0].K != value.String {
		return "", false
	}
	alias := strings.TrimSpace(vals[0].Text())
	if alias == "" {
		alias = "default"
	}
	return alias, true
}

func (d *Database) resolveAlias(alias string) (*sql.DB, error) {
	connection, err := database.ResolveOwned(alias)
	if err != nil {
		return nil, err
	}
	configured, external := database.Configs[alias]
	if connection != nil && external {
		return nil, fmt.Errorf("connection alias %q is ambiguous between an owned KitDB and an external database", alias)
	}
	if connection != nil {
		return connection, nil
	}
	if external {
		if d.tenant == nil {
			return nil, fmt.Errorf("tenant database runtime is unavailable")
		}
		if connection := d.tenant.lookupDatabase(&configured); connection != nil {
			return connection, nil
		}
		return d.tenant.openDatabase(&configured)
	}
	if alias == "default" {
		return nil, fmt.Errorf("default connection is not configured; use sqlite for local data or declare alias %q", alias)
	}
	return nil, fmt.Errorf("connection alias %q is not configured", alias)
}

func (d *Database) ensureConnection() *sql.DB {
	if d == nil {
		return nil
	}
	if d.sqlDB != nil {
		return d.sqlDB
	}
	if d.preset != nil {
		if conn := d.tenant.lookupDatabase(d.preset); conn != nil {
			d.sqlDB = conn
			return d.sqlDB
		}
		if t := strings.ToLower(d.preset.Type); t == "sqlite" || t == "sqlite3" {
			// Local SQLite files need their parent .data/ folder to exist before
			// the driver opens the file.
			if dir := filepath.Dir(d.preset.Name); dir != "." && dir != "" {
				_ = os.MkdirAll(dir, 0o755)
			}
		}
		conn, err := d.tenant.openDatabase(d.preset)
		if err != nil {
			fmt.Printf("[DB] Failed to connect %s: %v\n", d.preset.Alias, err)
			return nil
		}
		d.sqlDB = conn
		return d.sqlDB
	}
	alias := d.alias
	if alias == "" {
		alias = "default"
	}
	connection, err := d.resolveAlias(alias)
	if err != nil {
		fmt.Printf("[DB] Failed to resolve database '%s': %v\n", alias, err)
		return nil
	}
	d.sqlDB = connection
	return d.sqlDB
}

func (d *Database) db() *sql.DB {
	return d.ensureConnection()
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
		return value.InvalidFailure(value.UnavailableCode, "database not connected")
	}
	tx, err := dbConn.Begin()
	if err != nil {
		return query.DatabaseFailure("database: begin", err)
	}

	txDb := &Database{
		tenant:       d.tenant,
		requestScope: d.requestScope,
		config:       d.config,
		sqlDB:        d.sqlDB,
		tx:           tx,
		alias:        d.alias,
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
		return query.DatabaseFailure("database: commit", err)
	}

	return result
}
