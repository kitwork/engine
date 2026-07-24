package database

import (
	"database/sql"

	"github.com/kitwork/engine/capabilities"
	query "github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

type DatabaseAdapter struct {
	scope capabilities.Scope
	name  string
}

func NewDatabaseAdapter(scope capabilities.Scope) *DatabaseAdapter {
	return &DatabaseAdapter{
		scope: scope,
		name:  "app.db",
	}
}

func (d *DatabaseAdapter) DB() *sql.DB {
	return d.scope.DB(d.name)
}

func (d *DatabaseAdapter) Table(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.Value{K: value.Invalid, V: "database: table name required"}
	}
	tableName := args[0].Text()
	db := d.DB()
	if db == nil {
		return value.Value{K: value.Invalid, V: "database: db connection unavailable"}
	}
	q := query.NewQuery(nil, db).Table(tableName)
	return value.New(q)
}

func init() {
	capabilities.DefaultRegistry.Register("database", func(scope capabilities.Scope) value.Value {
		return value.New(NewDatabaseAdapter(scope))
	})
}
