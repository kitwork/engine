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
	sqlDB *sql.DB
	tx    *sql.Tx
}

func NewDatabaseAdapter(scope capabilities.Scope) *DatabaseAdapter {
	return &DatabaseAdapter{
		scope: scope,
		name:  "default",
	}
}

func (d *DatabaseAdapter) Connection(name ...string) *DatabaseAdapter {
	alias := "default"
	if len(name) > 0 && name[0] != "" {
		alias = name[0]
	}
	d.name = alias
	if d.scope != nil {
		d.sqlDB = d.scope.DB(alias)
	}
	return d
}

func (d *DatabaseAdapter) Connect(name ...string) *DatabaseAdapter {
	return d.Connection(name...)
}

func (d *DatabaseAdapter) DB() *sql.DB {
	if d.sqlDB != nil {
		return d.sqlDB
	}
	if d.scope == nil {
		return nil
	}
	d.sqlDB = d.scope.DB(d.name)
	return d.sqlDB
}

func (d *DatabaseAdapter) NewQuery() *query.Query {
	var exec query.Executor = d.DB()
	if d.tx != nil {
		exec = d.tx
	}
	return query.New(exec, nil)
}

func (d *DatabaseAdapter) Table(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.Value{K: value.Invalid, V: "database: table name required"}
	}
	tableName := args[0].Text()
	q := d.NewQuery().Table(tableName)
	return value.New(q)
}

func (d *DatabaseAdapter) Select(fields ...string) *query.Query {
	return d.NewQuery().Select(fields...)
}

func (d *DatabaseAdapter) Where(args ...value.Value) *query.Query {
	return d.NewQuery().Where(args...)
}

func (d *DatabaseAdapter) Limit(limit int) *query.Query {
	return d.NewQuery().Limit(limit)
}

func (d *DatabaseAdapter) OrderBy(col string, dir ...string) *query.Query {
	return d.NewQuery().OrderBy(col, dir...)
}

func (d *DatabaseAdapter) GroupBy(cols ...string) *query.Query {
	return d.NewQuery().GroupBy(cols...)
}

func (d *DatabaseAdapter) Join(args ...value.Value) *query.Query {
	return d.NewQuery().Join(args...)
}

func (d *DatabaseAdapter) Find(args ...value.Value) value.Value {
	return d.NewQuery().Find(args...)
}

func (d *DatabaseAdapter) First(args ...value.Value) value.Value {
	return d.NewQuery().First(args...)
}

func (d *DatabaseAdapter) List(args ...value.Value) value.Value {
	return d.NewQuery().List(args...)
}

func (d *DatabaseAdapter) Count(args ...value.Value) value.Value {
	return d.NewQuery().Count(args...)
}

func (d *DatabaseAdapter) Exists(args ...value.Value) value.Value {
	return d.NewQuery().Exists(args...)
}

func (d *DatabaseAdapter) Create(args ...value.Value) value.Value {
	return d.NewQuery().Create(args...)
}

func (d *DatabaseAdapter) Update(args ...value.Value) value.Value {
	return d.NewQuery().Update(args...)
}

func (d *DatabaseAdapter) Save(args ...value.Value) value.Value {
	return d.NewQuery().Save(args...)
}

func (d *DatabaseAdapter) Delete() value.Value {
	return d.NewQuery().Delete()
}

func (d *DatabaseAdapter) Remove() value.Value {
	return d.NewQuery().Remove()
}

func (d *DatabaseAdapter) Atomic(args ...value.Value) value.Value {
	if len(args) == 0 || !args[0].IsCallable() {
		return value.Value{K: value.Nil}
	}
	dbConn := d.DB()
	if dbConn == nil {
		return value.Value{K: value.Invalid, V: "database not connected"}
	}
	tx, err := dbConn.Begin()
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}

	txAdapter := &DatabaseAdapter{
		scope: d.scope,
		name:  d.name,
		sqlDB: d.sqlDB,
		tx:    tx,
	}
	txVal := value.New(txAdapter)

	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	var result value.Value
	if lambda, ok := args[0].V.(*value.Lambda); ok {
		if runtimeSeam, ok := d.scope.(capabilities.Runtime); ok {
			_, err := runtimeSeam.Execute(nil, lambda, []value.Value{txVal})
			if err != nil {
				tx.Rollback()
				return value.Value{K: value.Invalid, V: err.Error()}
			}
		}
	} else {
		result = args[0].Call("atomic", txVal)
	}

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

func Register(registry *capabilities.Registry) {
	registry.RegisterWithLifetime("database", capabilities.LifetimeTransient, func(scope capabilities.Scope) value.Value {
		return value.New(NewDatabaseAdapter(scope))
	})
}

func init() {
	Register(capabilities.DefaultRegistry)
}
