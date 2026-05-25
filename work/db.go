package work

import (
	"database/sql"
	"fmt"

	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/value"
	_ "github.com/lib/pq"
)

func (w *KitWork) Database() *Database {
	return &Database{
		tenant: w.tenant,
		config: &database.Config{},
	}
}

type Database struct {
	tenant *Tenant
	config *database.Config
	sqlDB  *sql.DB
	tx     *sql.Tx
}

func (d *Database) Connection() *Database {
	if d.sqlDB == nil && database.Default != nil {
		d.sqlDB = database.Default
	}
	return d
}

func (d *Database) Connected() *Database {
	if d.sqlDB == nil && database.Default != nil {
		d.sqlDB = database.Default
	}
	return d
}

func (d *Database) Connect(vals ...value.Value) *Database {
	// Nếu có truyền config vào, cập nhật config trước
	if len(vals) > 0 {
		vals[0].To(d.config)
	}

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
	var exec sqlExecutor = d.db()
	if d.tx != nil {
		exec = d.tx
	}
	return &Query{
		db: exec,
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

func (d *Database) OrderBy(col string, dir ...string) *Query {
	return d.NewQuery().OrderBy(col, dir...)
}

func (d *Database) GroupBy(cols ...string) *Query {
	return d.NewQuery().GroupBy(cols...)
}

func (d *Database) Join(args ...value.Value) *Query {
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
		tenant: d.tenant,
		config: d.config,
		sqlDB:  d.sqlDB,
		tx:     tx,
	}
	txVal := value.New(txDb)

	// Tự động Rollback nếu có Panic xảy ra trong khi chạy kịch bản
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	result := d.tenant.vm.ExecuteLambda(lambda, []value.Value{txVal})

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
