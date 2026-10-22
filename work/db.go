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
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

// Database is the engine's handle on one SQL file it opens for its own tables (SQLite's preset): the SQL
// backend of SchemaTable and the scheduler's store. It gives the query builder (Table) over that file.
//
// It was also the older kitwork/database module until 22/10/2026 (database.table/where/atomic/connection,
// and through them ctx.database and import { sqlite }), each removed with an error naming what the spec
// writes (database_module.go, KITWORK_DATABASE.md mục 9). App code reaches a database only through what
// its .shared/schema/ declares: connection(), connection("tên") of the host's app.databases, connect().
type Database struct {
	tenant       *Tenant
	requestScope *requestscope.Scope
	sqlDB        *sql.DB
	preset       *database.Config
}

// ensureConnection opens the preset file once, into the app's pool, and keeps the handle.
func (d *Database) ensureConnection() *sql.DB {
	if d == nil {
		return nil
	}
	if d.sqlDB != nil {
		return d.sqlDB
	}
	if d.preset == nil {
		return nil
	}
	if conn := d.tenant.lookupDatabase(d.preset); conn != nil {
		d.sqlDB = conn
		return d.sqlDB
	}
	if t := strings.ToLower(d.preset.Type); t == "sqlite" || t == "sqlite3" {
		// Local SQLite files need their parent folder to exist before the driver opens the file.
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

func (d *Database) db() *sql.DB {
	return d.ensureConnection()
}

func (d *Database) NewQuery() *query.Query {
	var exec query.Executor
	if dbConn := d.db(); dbConn != nil {
		// Do not assign a typed nil *sql.DB to the Executor interface: the interface itself would
		// compare non-nil and query execution would panic inside database/sql.
		exec = dbConn
	}
	return query.New(exec, tenantLambdaExecutor{tenant: d.tenant, requestScope: d.requestScope})
}

func (d *Database) Table(table string) *query.Query {
	return d.NewQuery().Table(table)
}
