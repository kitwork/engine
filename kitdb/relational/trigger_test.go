package relational

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func triggerTestOpen(t *testing.T) *Engine {
	t.Helper()
	e, err := Open(filepath.Join(t.TempDir(), "triggers.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func triggerTestFail(t *testing.T, e *Engine, query, message string) {
	t.Helper()
	if _, err := e.Execute(context.Background(), query); err == nil || message != "" && !strings.Contains(err.Error(), message) {
		t.Fatalf("%s: expected error containing %q, got %v", query, message, err)
	}
}

func triggerTestCount(t *testing.T, e *Engine, table string, want int64) {
	t.Helper()
	if got := functionTestExecute(t, e, `SELECT COUNT(*) FROM `+table).Rows[0][0]; got != want {
		t.Fatalf("%s count = %v, want %d", table, got, want)
	}
}

func TestSQLTriggerLifecycle(t *testing.T) {
	e := triggerTestOpen(t)
	run := func(query string) Result { return functionTestExecute(t, e, query) }
	run(`CREATE DOMAIN amount AS NUMERIC(12,2) CHECK (VALUE >= 0)`)
	run(`CREATE TABLE products (id INTEGER PRIMARY KEY, price amount, name TEXT)`)
	run(`CREATE TABLE audit (id SERIAL PRIMARY KEY, product_id INTEGER, old_price amount, new_price amount, event TEXT NOT NULL, label TEXT DEFAULT 'default')`)
	run(`CREATE TRIGGER inserted AFTER INSERT ON products FOR EACH ROW INSERT INTO audit (id, product_id, new_price, event, label) VALUES (DEFAULT, NEW.id, NEW.price, 'insert', lower(NEW.name))`)
	run(`CREATE TRIGGER changed AFTER UPDATE ON products FOR EACH ROW WHEN (OLD.price <> NEW.price) INSERT INTO audit (product_id, old_price, new_price, event) VALUES (NEW.id, OLD.price, NEW.price, 'update')`)
	run(`CREATE TRIGGER removed AFTER DELETE ON products FOR EACH ROW INSERT INTO audit (product_id, old_price, event) VALUES (OLD.id, OLD.price, 'delete')`)
	run(`CREATE TRIGGER skipped AFTER INSERT ON products FOR EACH ROW WHEN (NULL) INSERT INTO audit (event) VALUES ('never')`)
	result := run(`INSERT INTO products (id,price,name) VALUES (1,10,'ALICE'),(2,20,'BOB') RETURNING id`)
	if len(result.Rows) != 2 || result.CommandTag != "INSERT 0 2" {
		t.Fatalf("source result includes trigger rows: %#v", result)
	}
	triggerTestCount(t, e, "audit", 2)
	run(`UPDATE products SET name = 'ignored' WHERE id = 1`)
	run(`UPDATE products SET price = 11 WHERE id = 1`)
	run(`DELETE FROM products WHERE id = 2`)
	run(`DELETE FROM products WHERE id = 999`)
	rows := run(`SELECT product_id, old_price, new_price, event, label FROM audit ORDER BY id`).Rows
	if got := fmt.Sprint(rows); got != "[[1 <nil> 10 insert alice] [2 <nil> 20 insert bob] [1 10 11 update default] [2 20 <nil> delete default]]" {
		t.Fatal(got)
	}
	triggerTestFail(t, e, `DROP TABLE audit`, "trigger")
	triggerTestFail(t, e, `ALTER TABLE products DROP COLUMN price`, "depends on it")
	triggerTestFail(t, e, `ALTER TABLE products DROP COLUMN name`, "trigger")
	triggerTestFail(t, e, `ALTER TABLE audit DROP COLUMN product_id`, "trigger")
	run(`ALTER TABLE products RENAME COLUMN price TO cost`)
	run(`ALTER TABLE audit RENAME COLUMN product_id TO source_id`)
	run(`ALTER TABLE products RENAME TO items`)
	run(`ALTER TABLE audit RENAME TO history`)
	run(`UPDATE items SET cost = 12 WHERE id = 1`)
	triggerTestCount(t, e, "history", 5)
	for _, checkpoint := range []bool{false, true} {
		if checkpoint {
			if _, err := e.Checkpoint(); err != nil {
				t.Fatal(err)
			}
		}
		path := e.database.Path()
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
		var err error
		e, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = e.Close() })
		run(`UPDATE items SET cost = cost + 1 WHERE id = 1`)
	}
	triggerTestCount(t, e, "history", 7)
	run(`DROP TRIGGER changed ON items`)
	run(`DROP TRIGGER IF EXISTS changed ON items`)
	run(`UPDATE items SET cost = 50`)
	triggerTestCount(t, e, "history", 7)
	run(`DROP TABLE items`)
	catalog, err := e.database.Catalog()
	if err != nil || len(catalog.Triggers) != 0 {
		t.Fatalf("owned triggers not removed: %#v %v", catalog.Triggers, err)
	}
	run(`DROP TABLE history`)
	if _, err := e.executeWithSequences(context.Background(), `CREATE TRIGGER nope AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id) VALUES (1)`, nil, nil, true); err == nil {
		t.Fatal("read-only accepted trigger DDL")
	}
}

func TestSQLTriggerAtomicityAndChains(t *testing.T) {
	e := triggerTestOpen(t)
	run := func(q string) Result { return functionTestExecute(t, e, q) }
	run(`CREATE TABLE source (id INTEGER PRIMARY KEY, n INTEGER)`)
	run(`CREATE TABLE audit (id INTEGER PRIMARY KEY, n INTEGER CHECK (n > 0))`)
	run(`CREATE TABLE downstream (id INTEGER PRIMARY KEY, n INTEGER CHECK (n < 10))`)
	run(`CREATE TRIGGER record AFTER INSERT ON source FOR EACH ROW INSERT INTO audit (id,n) VALUES (NEW.id,NEW.n)`)
	run(`CREATE TRIGGER record AFTER INSERT ON audit FOR EACH ROW INSERT INTO downstream (id,n) VALUES (NEW.id,NEW.n)`)
	triggerTestFail(t, e, `INSERT INTO source (id,n) VALUES (1,1),(2,-1)`, "trigger")
	triggerTestFail(t, e, `INSERT INTO source (id,n) VALUES (1,1),(2,10)`, "trigger")
	for _, table := range []string{"source", "audit", "downstream"} {
		triggerTestCount(t, e, table, 0)
	}
	run(`INSERT INTO source (id,n) VALUES (1,1)`)
	triggerTestFail(t, e, `INSERT INTO source (id,n) VALUES (2,2),(3,3),(1,1)`, "")
	for _, table := range []string{"source", "audit", "downstream"} {
		triggerTestCount(t, e, table, 1)
	}
	tx, err := e.BeginTransaction(context.Background(), TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Execute(context.Background(), `INSERT INTO source (id,n) VALUES (2,2)`); err != nil {
		t.Fatal(err)
	}
	point, err := tx.Savepoint()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Execute(context.Background(), `INSERT INTO source (id,n) VALUES (3,3),(4,40)`); err == nil {
		t.Fatal("accepted failing cascade")
	}
	for _, table := range []string{"source", "audit", "downstream"} {
		result, err := tx.Execute(context.Background(), `SELECT COUNT(*) FROM `+table)
		if err != nil || result.Rows[0][0] != int64(2) {
			t.Fatalf("partial cascade in %s: %#v %v", table, result, err)
		}
	}
	if _, err := tx.Execute(context.Background(), `INSERT INTO source (id,n) VALUES (5,5)`); err != nil {
		t.Fatal(err)
	}
	if err := tx.RollbackTo(point); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"source", "audit", "downstream"} {
		triggerTestCount(t, e, table, 2)
	}
	run(`CREATE TRIGGER changed AFTER UPDATE ON source FOR EACH ROW INSERT INTO audit (id,n) VALUES (NEW.id+100, NEW.n)`)
	run(`CREATE TRIGGER removed AFTER DELETE ON source FOR EACH ROW INSERT INTO audit (id,n) VALUES (OLD.id+200, -OLD.n)`)
	triggerTestFail(t, e, `UPDATE source SET n = 20`, "trigger")
	triggerTestFail(t, e, `DELETE FROM source`, "trigger")
	if got := fmt.Sprint(run(`SELECT n FROM source ORDER BY id`).Rows); got != "[[1] [2]]" {
		t.Fatal(got)
	}
	for _, table := range []string{"source", "audit", "downstream"} {
		triggerTestCount(t, e, table, 2)
	}
	// Kernel dependency validation protects the graph even without SQL DDL.
	catalog, _ := e.database.Catalog()
	ktx, err := e.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer ktx.Rollback()
	if err := ktx.DeleteStruct(catalog.Triggers[0].TargetStruct); err != nil {
		t.Fatal(err)
	}
	if _, err := ktx.Commit(); !errors.Is(err, kitdbengine.ErrInvalidCatalog) {
		t.Fatalf("kernel deleted trigger target: %v", err)
	}
}

func TestSQLTriggerValidationAndBounds(t *testing.T) {
	e := triggerTestOpen(t)
	run := func(q string) Result { return functionTestExecute(t, e, q) }
	run(`CREATE TABLE a (id INTEGER PRIMARY KEY, n INTEGER, j JSON)`)
	run(`CREATE TABLE b (id INTEGER PRIMARY KEY, n INTEGER)`)
	before, _ := e.database.CatalogVersion()
	for _, query := range []string{
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id) VALUES (OLD.id)`,
		`CREATE TRIGGER t AFTER DELETE ON a FOR EACH ROW INSERT INTO b (id) VALUES (NEW.id)`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id) VALUES (id)`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id) VALUES (NEW.missing)`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id) VALUES (NEW.j)`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id) VALUES ($1)`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id) VALUES (now())`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id) VALUES (nextval('s'))`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW WHEN (NEW.id) INSERT INTO b (id) VALUES (NEW.id)`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id) VALUES ('oops')`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id,id) VALUES (NEW.id,NEW.id)`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO b (missing) VALUES (NEW.id)`,
		`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO a (id) VALUES (NEW.id)`,
	} {
		triggerTestFail(t, e, query, "")
	}
	after, _ := e.database.CatalogVersion()
	if after.Revision != before.Revision {
		t.Fatal("invalid trigger published catalog")
	}
	run(`CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id,n) VALUES (NEW.id,NEW.n)`)
	triggerTestFail(t, e, `CREATE TRIGGER cycle AFTER DELETE ON b FOR EACH ROW WHEN (FALSE) INSERT INTO a (id) VALUES (OLD.id)`, "cyclic")
	triggerTestFail(t, e, `CREATE TRIGGER t AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id) VALUES (NEW.id)`, "already exists")
	catalog, _ := e.database.Catalog()
	bad := catalog.Triggers[0]
	bad.Hash = "invalid"
	if _, err := decodeStoredTrigger(bad); err == nil {
		t.Fatal("accepted bad trigger hash")
	}
	other := triggerTestOpen(t)
	functionTestExecute(t, other, `CREATE TABLE a (id INTEGER PRIMARY KEY)`)
	functionTestExecute(t, other, `INSERT INTO a (id) VALUES (1)`)
	isolated, _ := other.database.Catalog()
	if len(isolated.Triggers) != 0 {
		t.Fatal("trigger leaked across databases")
	}
	// Typed plans use exactly the same path, including OLD/NEW validation.
	run(`DROP TRIGGER t ON a`)
	plan, err := kitdbsql.ParseStatement(`CREATE TRIGGER native AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id) VALUES (NEW.id)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ExecutePlan(context.Background(), &plan); err != nil {
		t.Fatal(err)
	}
	run(`INSERT INTO a (id) VALUES (1)`)
	triggerTestCount(t, e, "b", 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Execute(ctx, `INSERT INTO a (id) VALUES (2)`); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	triggerTestCount(t, e, "a", 1)
}

func TestSQLTriggerCascadeDepthAndEvaluationBudget(t *testing.T) {
	t.Run("depth", func(t *testing.T) {
		e := triggerTestOpen(t)
		for i := 0; i <= maximumTriggerDepth+1; i++ {
			functionTestExecute(t, e, fmt.Sprintf(`CREATE TABLE t%d (id INTEGER PRIMARY KEY)`, i))
		}
		for i := 0; i <= maximumTriggerDepth; i++ {
			functionTestExecute(t, e, fmt.Sprintf(`CREATE TRIGGER copy AFTER INSERT ON t%d FOR EACH ROW INSERT INTO t%d (id) VALUES (NEW.id)`, i, i+1))
		}
		triggerTestFail(t, e, `INSERT INTO t0 (id) VALUES (1)`, "depth")
		for i := 0; i <= maximumTriggerDepth+1; i++ {
			triggerTestCount(t, e, fmt.Sprintf("t%d", i), 0)
		}
		functionTestExecute(t, e, fmt.Sprintf(`DROP TRIGGER copy ON t%d`, maximumTriggerDepth))
		functionTestExecute(t, e, `INSERT INTO t0 (id) VALUES (1)`)
		for i := 0; i <= maximumTriggerDepth; i++ {
			triggerTestCount(t, e, fmt.Sprintf("t%d", i), 1)
		}
	})
	t.Run("evaluations", func(t *testing.T) {
		e := triggerTestOpen(t)
		functionTestExecute(t, e, `CREATE TABLE a (id INTEGER PRIMARY KEY)`)
		functionTestExecute(t, e, `CREATE TABLE b (id INTEGER, kind INTEGER, PRIMARY KEY (id,kind))`)
		for i := 1; i <= 2; i++ {
			functionTestExecute(t, e, fmt.Sprintf(`CREATE TRIGGER t%d AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id,kind) VALUES (NEW.id,%d)`, i, i))
		}
		rows := make([][]kitdbsql.Literal, maximumTriggerEvaluations/2+1)
		for i := range rows {
			rows[i] = []kitdbsql.Literal{{Kind: kitdbsql.LiteralNumber, Text: fmt.Sprint(i + 1)}}
		}
		plan := &kitdbsql.ParsedStatement{Kind: kitdbsql.StatementInsert, Insert: &kitdbsql.InsertStatement{Table: "a", Columns: []string{"id"}, Rows: rows}}
		if _, err := e.ExecutePlan(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "evaluations") {
			t.Fatalf("budget: %v", err)
		}
		triggerTestCount(t, e, "a", 0)
		triggerTestCount(t, e, "b", 0)
		functionTestExecute(t, e, `INSERT INTO a (id) VALUES (1)`)
		triggerTestCount(t, e, "b", 2)
	})
}

func TestSQLTriggerCrashAndBackup(t *testing.T) {
	if path := os.Getenv("KITDB_TRIGGER_CRASH_CHILD"); path != "" {
		e, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		functionTestExecute(t, e, `CREATE TABLE a (id INTEGER PRIMARY KEY)`)
		functionTestExecute(t, e, `CREATE TABLE b (id INTEGER PRIMARY KEY)`)
		functionTestExecute(t, e, `CREATE TRIGGER copy AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id) VALUES (NEW.id)`)
		functionTestExecute(t, e, `INSERT INTO a (id) VALUES (1)`)
		tx, err := e.BeginTransaction(context.Background(), TransactionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Execute(context.Background(), `INSERT INTO a (id) VALUES (2)`); err != nil {
			t.Fatal(err)
		}
		os.Exit(32)
	}
	path := filepath.Join(t.TempDir(), "crash.kitdb")
	child := exec.Command(os.Args[0], "-test.run=^TestSQLTriggerCrashAndBackup$")
	child.Env = append(os.Environ(), "KITDB_TRIGGER_CRASH_CHILD="+path)
	output, err := child.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 32 {
		t.Fatalf("child: %v %s", err, output)
	}
	e, err := OpenWithOptions(path, Options{Kernel: kitdbengine.OpenOptions{RetainHistory: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	triggerTestCount(t, e, "a", 1)
	triggerTestCount(t, e, "b", 1)
	functionTestExecute(t, e, `INSERT INTO a (id) VALUES (3)`)
	triggerTestCount(t, e, "b", 2)
	anchor := filepath.Join(t.TempDir(), "backup.kitdb")
	if _, err := e.database.CreateBackupAnchor(context.Background(), anchor); err != nil {
		t.Fatal(err)
	}
	if _, err := kitdbengine.VerifyBackupAnchor(context.Background(), anchor); err != nil {
		t.Fatal(err)
	}
	backup, err := Open(anchor)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	functionTestExecute(t, backup, `INSERT INTO a (id) VALUES (4)`)
	triggerTestCount(t, backup, "a", 3)
	triggerTestCount(t, backup, "b", 3)
	triggerTestCount(t, e, "a", 2)
	triggerTestCount(t, e, "b", 2)
}

func TestSQLTriggerConstraintsAndScalarPrecision(t *testing.T) {
	e := triggerTestOpen(t)
	run := func(q string) Result { return functionTestExecute(t, e, q) }
	run(`CREATE DOMAIN positive AS INTEGER NOT NULL CHECK (VALUE > 0)`)
	run(`CREATE TABLE parents (id INTEGER PRIMARY KEY)`)
	run(`INSERT INTO parents (id) VALUES (1)`)
	run(`CREATE TABLE source (id BIGINT PRIMARY KEY, parent INTEGER, value INTEGER, code TEXT, amount NUMERIC(24,2), uuid UUID, moment TIMESTAMPTZ)`)
	run(`CREATE TABLE target (id BIGINT PRIMARY KEY, parent INTEGER REFERENCES parents(id), value positive, code TEXT NOT NULL UNIQUE, amount NUMERIC(24,2), uuid UUID, moment TIMESTAMPTZ)`)
	run(`CREATE TRIGGER copy AFTER INSERT ON source FOR EACH ROW INSERT INTO target (id,parent,value,code,amount,uuid,moment) VALUES (NEW.id,NEW.parent,NEW.value,NEW.code,NEW.amount,NEW.uuid,NEW.moment)`)
	run(`INSERT INTO source (id,parent,value,code,amount,uuid,moment) VALUES (9007199254740993,1,1,'code',9007199254740993.25,'01234567-89ab-cdef-0123-456789abcdef','2026-09-05T10:30:00+07:00')`)
	if a, b := run(`SELECT * FROM source`).Rows, run(`SELECT * FROM target`).Rows; !reflect.DeepEqual(a, b) {
		t.Fatalf("trigger changed scalar values: %#v != %#v", a, b)
	}
	for _, q := range []string{
		`INSERT INTO source (id,parent,value,code) VALUES (2,2,1,'other')`,
		`INSERT INTO source (id,parent,value,code) VALUES (2,1,-1,'other')`,
		`INSERT INTO source (id,parent,value,code) VALUES (2,1,NULL,'other')`,
		`INSERT INTO source (id,parent,value,code) VALUES (2,1,1,NULL)`,
		`INSERT INTO source (id,parent,value,code) VALUES (2,1,1,'code')`,
	} {
		triggerTestFail(t, e, q, "trigger")
	}
	triggerTestCount(t, e, "source", 1)
	triggerTestCount(t, e, "target", 1)
}

func TestSQLTriggerConflictsAndConcurrentWriters(t *testing.T) {
	e := triggerTestOpen(t)
	functionTestExecute(t, e, `CREATE TABLE a (id INTEGER PRIMARY KEY)`)
	functionTestExecute(t, e, `CREATE TABLE b (id INTEGER PRIMARY KEY)`)
	functionTestExecute(t, e, `CREATE TRIGGER copy AFTER INSERT ON a FOR EACH ROW INSERT INTO b (id) VALUES (NEW.id)`)
	first, err := e.BeginTransaction(context.Background(), TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback()
	second, err := e.BeginTransaction(context.Background(), TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Rollback()
	if _, err := first.Execute(context.Background(), `INSERT INTO a (id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Execute(context.Background(), `INSERT INTO a (id) VALUES (2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Commit(context.Background()); !errors.Is(err, ErrTransactionConflict) {
		t.Fatalf("expected conflict: %v", err)
	}
	triggerTestCount(t, e, "a", 1)
	triggerTestCount(t, e, "b", 1)
	var workers sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for i := 0; i < 20; i++ {
				id := worker*20 + i + 10
				for attempt := 0; attempt < 100; attempt++ {
					_, err := e.Execute(context.Background(), `INSERT INTO a (id) VALUES ($1)`, id)
					if err == nil {
						break
					}
					if !errors.Is(err, ErrTransactionConflict) || attempt == 99 {
						t.Errorf("insert %d: %v", id, err)
						return
					}
				}
			}
		}(worker)
	}
	workers.Wait()
	triggerTestCount(t, e, "a", 81)
	triggerTestCount(t, e, "b", 81)
	if a, b := functionTestExecute(t, e, `SELECT id FROM a ORDER BY id`).Rows, functionTestExecute(t, e, `SELECT id FROM b ORDER BY id`).Rows; !reflect.DeepEqual(a, b) {
		t.Fatal("source/trigger writes diverged")
	}
}
