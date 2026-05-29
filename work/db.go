package work

import (
	"database/sql"
	"fmt"

	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/value"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
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
	if d.sqlDB == nil {
		d.tenant.dbMu.Lock()
		if d.tenant.databases == nil {
			d.tenant.databases = make(map[string]*sql.DB)
		}
		if dbConn, exists := d.tenant.databases["default"]; exists {
			d.sqlDB = dbConn
		}
		d.tenant.dbMu.Unlock()
	}
	return d
}

func (d *Database) Connected() *Database {
	return d.Connection()
}

func (d *Database) Connect(vals ...value.Value) *Database {
	if d.tenant.databases == nil {
		d.tenant.databases = make(map[string]*sql.DB)
	}

	var alias string = "default"
	var configToConnect *database.Config

	if len(vals) == 1 {
		v := vals[0]
		if v.K == value.String {
			// database.connect("alias") -> GET
			alias = v.String()
		} else if v.K == value.Map {
			// database.connect({ alias: "alias", ... }) -> GET or SET
			m, _ := v.Interface().(map[string]interface{})
			if m != nil {
				if a, ok := m["alias"].(string); ok {
					alias = a
				}
				_, hasType := m["type"]
				_, hasHost := m["host"]
				if hasType || hasHost {
					var dbCfg database.Config
					v.To(&dbCfg)
					if dbCfg.Alias == "" {
						dbCfg.Alias = alias
					}
					configToConnect = &dbCfg
				}
			}
		}
	} else if len(vals) >= 2 {
		// database.connect("alias", { ... }) -> SET
		alias = vals[0].String()
		var dbCfg database.Config
		vals[1].To(&dbCfg)
		dbCfg.Alias = alias
		configToConnect = &dbCfg
	}

	if alias == "" {
		alias = "default"
	}

	d.tenant.dbMu.Lock()
	defer d.tenant.dbMu.Unlock()

	// GET Operation
	if configToConnect == nil {
		if dbConn, exists := d.tenant.databases[alias]; exists {
			d.sqlDB = dbConn
		} else {
			if dbCfg, ok := database.Configs[alias]; ok {
				dbConn, err := dbCfg.Connect()
				if err != nil {
					fmt.Printf("[DB] Failed to connect to configured database '%s': %v\n", alias, err)
				} else {
					d.tenant.databases[alias] = dbConn
					d.sqlDB = dbConn
				}
			} else if alias == "default" {
				sqlitePath := d.tenant.resolve("kitwork.db")
				fmt.Printf("[DB] Default connection not found. Initializing fallback SQLite at: %s\n", sqlitePath)
				sqliteCfg := &database.Config{
					Alias: "default",
					Type:  "sqlite",
					Host:  sqlitePath,
					Name:  sqlitePath,
				}
				dbConn, err := sqliteCfg.Connect()
				if err != nil {
					fmt.Printf("[DB] Failed to connect SQLite fallback database: %v\n", err)
				} else {
					d.tenant.databases["default"] = dbConn
					d.sqlDB = dbConn
				}
			} else {
				fmt.Printf("Database connection with alias '%s' not found\n", alias)
			}
		}
		return d
	}

	// SET Operation
	dbConn, err := configToConnect.Connect()
	if err != nil {
		fmt.Printf("Failed to connect database for alias '%s': %v\n", alias, err)
		return d
	}

	d.tenant.databases[alias] = dbConn
	d.sqlDB = dbConn
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
