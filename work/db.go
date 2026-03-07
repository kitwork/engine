package work

import (
	"database/sql"
	"fmt"

	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/value"
	_ "github.com/lib/pq"
)

func (w *KitWork) Database(vals ...value.Value) *Database {
	db := &Database{
		tenant: w.tenant,
		config: &database.Config{},
	}
	if len(vals) > 0 {
		vals[0].To(db.config)
	}
	return db
}

type Database struct {
	tenant *Tenant
	config *database.Config
	sqlDB  *sql.DB
}

func (d *Database) Connect() *Database {
	if d.sqlDB == nil {
		var err error
		d.sqlDB, err = d.config.Connect()
		if err != nil {
			fmt.Println(err)
		}
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

func (d *Database) NewQuery() *Query {
	return &Query{
		db: d.db(),
		vm: d.tenant.vm,
	}
}

func (d *Database) Table(table string) *Query {
	return d.NewQuery().Table(table)
}

func (d *Database) Select(fields ...string) *Query {
	return d.NewQuery().Select(fields...)
}

func (d *Database) Where(args ...value.Value) *Query {
	return d.NewQuery().Where(args...)
}

func (d *Database) Limit(limit int) *Query {
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
