package work

import (
	"database/sql"
	"fmt"

	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/value"
	_ "github.com/lib/pq"
)

func (w *KitWork) Database(vals ...value.Value) *Database {
	fmt.Println(1233)
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

func (d *Database) Table(table string) *DBQuery {
	return &DBQuery{
		db:    d.db(),
		table: table,
	}
}
