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
	"testing"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestInsertSelectSnapshotAndComposition(t *testing.T) {
	e := triggerTestOpen(t)
	run := func(q string, args ...any) Result { return functionTestExecute(t, e, q, args...) }
	run(`CREATE FUNCTION clean(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN upper(trim(s))`)
	run(`CREATE TABLE src (id BIGINT PRIMARY KEY, label TEXT, amount NUMERIC(22,2))`)
	run(`CREATE TABLE dst (id BIGINT PRIMARY KEY, label TEXT UNIQUE, amount NUMERIC(22,2))`)
	run(`INSERT INTO src VALUES (1,' a ',9007199254740993.25),(2,' b ',2.50)`)
	q := `INSERT INTO dst (id,label,amount) SELECT id,clean(label),amount FROM src WHERE id > $1 RETURNING id,clean(label),amount + 1`
	got := run(q, 0)
	if fmt.Sprint(got.Rows) != "[[1 A 9007199254740994.25] [2 B 3.5]]" || got.Affected != 2 {
		t.Fatal(got)
	}
	columns, err := e.Describe(context.Background(), q)
	if err != nil || !reflect.DeepEqual(columns, got.Columns) {
		t.Fatal(columns, err)
	}
	got = run(`INSERT INTO dst SELECT id,label,amount FROM src WHERE id < 0 RETURNING *`)
	if got.CommandTag != "INSERT 0 0" || len(got.Columns) != 3 {
		t.Fatal(got)
	}
	got = run(`INSERT INTO src SELECT id + 10,label,amount FROM src RETURNING id`)
	if got.Affected != 2 {
		t.Fatal(got)
	}
	triggerTestCount(t, e, "src", 4)
	run(`CREATE TABLE totals (label TEXT PRIMARY KEY, total NUMERIC(22,2))`)
	run(`INSERT INTO totals SELECT label,SUM(amount) FROM src GROUP BY label`)
	if got := run(`SELECT total FROM totals ORDER BY label`); fmt.Sprint(got.Rows) != "[[18014398509481986.5] [5]]" {
		t.Fatal(got)
	}
	run(`CREATE TABLE copied (id BIGINT PRIMARY KEY, label TEXT, amount NUMERIC(22,2))`)
	run(`INSERT INTO copied WITH picked AS (SELECT * FROM src WHERE id < 10) SELECT * FROM picked UNION ALL SELECT * FROM src WHERE id > 10`)
	triggerTestCount(t, e, "copied", 4)
	tx, err := e.BeginTransaction(context.Background(), TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Execute(context.Background(), `INSERT INTO src VALUES (99,'tx',1)`); err != nil {
		t.Fatal(err)
	}
	if got, err := tx.Execute(context.Background(), `INSERT INTO copied SELECT * FROM src WHERE id=99 RETURNING id`); err != nil || fmt.Sprint(got.Rows) != "[[99]]" {
		t.Fatal(got, err)
	}
	triggerTestCount(t, e, "copied", 4)
	for _, q := range []string{`INSERT INTO dst (id,label) SELECT * FROM src WHERE id<0`, `INSERT INTO dst (unknown) SELECT id FROM src WHERE id<0`, `INSERT INTO dst SELECT id+100,label,amount FROM src RETURNING 1 / (id-111)`} {
		if _, err := e.Execute(context.Background(), q); err == nil {
			t.Fatalf("accepted %s", q)
		}
		triggerTestCount(t, e, "dst", 2)
	}
}

func TestUpsertConstraintsTriggersAndRollback(t *testing.T) {
	e := triggerTestOpen(t)
	run := func(q string, args ...any) Result { return functionTestExecute(t, e, q, args...) }
	run(`CREATE FUNCTION clean(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN upper(s)`)
	run(`CREATE TABLE items (id INTEGER PRIMARY KEY, sku TEXT UNIQUE, n INTEGER CHECK (n>=0), amount NUMERIC(22,2))`)
	run(`CREATE INDEX items_n ON items (n)`)
	run(`CREATE TABLE audit (id SERIAL PRIMARY KEY, item INTEGER, event TEXT)`)
	for _, event := range []string{"INSERT", "UPDATE"} {
		run(fmt.Sprintf(`CREATE TRIGGER audit_%s AFTER %s ON items FOR EACH ROW INSERT INTO audit(item,event) VALUES (NEW.id,'%s')`, event, event, event))
	}
	run(`INSERT INTO items VALUES (1,'A',10,9007199254740993.25)`)
	got := run(`INSERT INTO items VALUES (9,'A',3,0.25),(2,'B',4,1) ON CONFLICT (sku) DO UPDATE SET n=items.n+excluded.n,amount=items.amount+excluded.amount,sku=clean(excluded.sku) RETURNING *`)
	if got.Affected != 2 || fmt.Sprint(got.Rows) != "[[1 A 13 9007199254740993.5] [2 B 4 1]]" {
		t.Fatal(got)
	}
	got = run(`INSERT INTO items VALUES (1,'ignored',0,0) ON CONFLICT (id) DO UPDATE SET n=excluded.n WHERE excluded.n > items.n RETURNING id`)
	if got.Affected != 0 || len(got.Columns) != 1 || len(got.Rows) != 0 {
		t.Fatal(got)
	}
	got = run(`INSERT INTO items VALUES (1,'x',1,1),(8,'A',1,1),(3,NULL,1,1),(4,NULL,1,1) ON CONFLICT DO NOTHING RETURNING id`)
	if got.Affected != 2 || fmt.Sprint(got.Rows) != "[[3] [4]]" {
		t.Fatal(got)
	}
	baseline := run(`SELECT * FROM items ORDER BY id`).Rows
	audit := run(`SELECT * FROM audit ORDER BY id`).Rows
	for _, q := range []string{
		`INSERT INTO items VALUES (1,'A',1,1),(2,'B',0,1) ON CONFLICT(id) DO UPDATE SET n=excluded.n RETURNING 10/n`,
		`INSERT INTO items VALUES (1,'A',1,1),(1,'A',2,1) ON CONFLICT(id) DO UPDATE SET n=excluded.n`,
		`INSERT INTO items VALUES (9,'X',1,1),(9,'Y',2,1) ON CONFLICT(id) DO UPDATE SET n=excluded.n`,
		`INSERT INTO items VALUES (1,'B',1,1) ON CONFLICT(id) DO UPDATE SET sku=excluded.sku`,
		`INSERT INTO items VALUES (10,'A',1,1) ON CONFLICT(id) DO NOTHING`,
		`INSERT INTO items VALUES (1,'A',-1,1) ON CONFLICT DO NOTHING`,
		`INSERT INTO items VALUES (5,'C',1,1) ON CONFLICT(n) DO NOTHING`,
		`INSERT INTO items SELECT * FROM items WHERE id<0 ON CONFLICT(id) DO UPDATE SET n=other.n`,
		`INSERT INTO items SELECT * FROM items WHERE id<0 ON CONFLICT(id) DO UPDATE SET id=excluded.id`,
	} {
		t.Run(q, func(t *testing.T) {
			if result, err := e.Execute(context.Background(), q); err == nil || len(result.Rows) != 0 {
				t.Fatalf("accepted %s: %#v %v", q, result, err)
			}
			if got := run(`SELECT * FROM items ORDER BY id`).Rows; !reflect.DeepEqual(got, baseline) {
				t.Fatal(got)
			}
			if got := run(`SELECT * FROM audit ORDER BY id`).Rows; !reflect.DeepEqual(got, audit) {
				t.Fatal(got)
			}
			if got := run(`SELECT id FROM items WHERE n=13`).Rows; fmt.Sprint(got) != "[[1]]" {
				t.Fatal(got)
			}
		})
	}
	got = run(`INSERT INTO items SELECT id,sku,n,amount FROM items ON CONFLICT(id) DO UPDATE SET n=items.n+1 RETURNING id`)
	if got.Affected != 4 {
		t.Fatal(got)
	}
	triggerTestCount(t, e, "items", 4)
}

func TestUpsertCompositeAndForeignKeys(t *testing.T) {
	e := triggerTestOpen(t)
	run := func(q string) Result { return functionTestExecute(t, e, q) }
	run(`CREATE TABLE parent (id INTEGER PRIMARY KEY)`)
	run(`INSERT INTO parent VALUES (1)`)
	run(`CREATE TABLE child (tenant INTEGER, id INTEGER, label TEXT, parent INTEGER REFERENCES parent(id), PRIMARY KEY(tenant,id), UNIQUE(tenant,label))`)
	run(`INSERT INTO child VALUES(1,1,'A',1),(2,1,'A',1)`)
	got := run(`INSERT INTO child VALUES(1,99,'A',1) ON CONFLICT(label,tenant) DO UPDATE SET label=excluded.label||'B' RETURNING tenant,id,label`)
	if fmt.Sprint(got.Rows) != "[[1 1 AB]]" {
		t.Fatal(got)
	}
	run(`INSERT INTO child VALUES(2,1,'C',1) ON CONFLICT(id,tenant) DO UPDATE SET label=excluded.label`)
	if _, err := e.Execute(context.Background(), `INSERT INTO child VALUES(1,1,'AB',99) ON CONFLICT(tenant,id) DO UPDATE SET parent=excluded.parent`); err == nil {
		t.Fatal("FK bypass")
	}
	if got := run(`SELECT parent FROM child ORDER BY tenant`).Rows; fmt.Sprint(got) != "[[1] [1]]" {
		t.Fatal(got)
	}
	run(`CREATE TABLE selfref (id INTEGER PRIMARY KEY, parent INTEGER REFERENCES selfref(id))`)
	run(`INSERT INTO selfref VALUES(1,2),(2,1) ON CONFLICT(id) DO NOTHING`)
	run(`INSERT INTO selfref VALUES(1,3),(3,1) ON CONFLICT(id) DO UPDATE SET parent=excluded.parent`)
	triggerTestCount(t, e, "selfref", 3)
	run(`CREATE TABLE unique_parent(id INTEGER PRIMARY KEY, code TEXT UNIQUE)`)
	run(`CREATE TABLE unique_child(id INTEGER PRIMARY KEY, parent TEXT REFERENCES unique_parent(code))`)
	run(`INSERT INTO unique_parent VALUES(1,'A')`)
	run(`INSERT INTO unique_child VALUES(1,'A')`)
	for _, q := range []string{`UPDATE unique_parent SET code='B' WHERE id=1`, `INSERT INTO unique_parent VALUES(1,'B') ON CONFLICT(id) DO UPDATE SET code=excluded.code`} {
		if _, err := e.Execute(context.Background(), q); err == nil {
			t.Fatal("orphaned child", q)
		}
	}
	run(`INSERT INTO unique_parent VALUES(1,'A') ON CONFLICT(id) DO UPDATE SET code=excluded.code`)
	if got := run(`SELECT code FROM unique_parent`).Rows; fmt.Sprint(got) != "[[A]]" {
		t.Fatal(got)
	}
}

func TestWriteCompositionConflictAndRecovery(t *testing.T) {
	const childEnv = "KITDB_WRITE_COMPOSITION_CRASH"
	if path := os.Getenv(childEnv); path != "" {
		e, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		run := func(q string) { functionTestExecute(t, e, q) }
		run(`CREATE TABLE items(id INTEGER PRIMARY KEY,n INTEGER)`)
		run(`CREATE TABLE audit(id SERIAL PRIMARY KEY,item INTEGER)`)
		run(`CREATE TRIGGER created AFTER INSERT ON items FOR EACH ROW INSERT INTO audit(item) VALUES(NEW.id)`)
		run(`INSERT INTO items SELECT 1,10 ON CONFLICT(id) DO NOTHING`)
		run(`INSERT INTO items SELECT id,n+1 FROM items ON CONFLICT(id) DO UPDATE SET n=excluded.n`)
		tx, err := e.BeginTransaction(context.Background(), TransactionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{`SAVEPOINT x`, `INSERT INTO items SELECT id+1,n FROM items`, `ROLLBACK TO x`, `INSERT INTO items VALUES(9,99)`} {
			if _, err := tx.Execute(context.Background(), q); err != nil {
				t.Fatal(err)
			}
		}
		// No Close/Rollback/defers: parent opens a fresh process-written file.
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "composition.kitdb")
	child := exec.Command(os.Args[0], "-test.run=^TestWriteCompositionConflictAndRecovery$")
	child.Env = append(os.Environ(), childEnv+"="+path)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child: %s %v", out, err)
	}
	e, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if got := functionTestExecute(t, e, `SELECT * FROM items`).Rows; fmt.Sprint(got) != "[[1 11]]" {
		t.Fatal(got)
	}
	triggerTestCount(t, e, "audit", 1)
	tx, err := e.BeginTransaction(context.Background(), TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	functionTestExecute(t, e, `UPDATE items SET n=20 WHERE id=1`)
	if _, err := tx.Execute(context.Background(), `INSERT INTO items VALUES(1,1) ON CONFLICT(id) DO UPDATE SET n=items.n+excluded.n`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(context.Background()); !errors.Is(err, ErrTransactionConflict) {
		t.Fatal(err)
	}
	if got := functionTestExecute(t, e, `SELECT n FROM items`).Rows; fmt.Sprint(got) != "[[20]]" {
		t.Fatal(got)
	}
}

func TestInsertSelectRowLimitAtomic(t *testing.T) {
	e := triggerTestOpen(t)
	functionTestExecute(t, e, `CREATE TABLE src(id INTEGER PRIMARY KEY)`)
	functionTestExecute(t, e, `CREATE TABLE dst(id INTEGER PRIMARY KEY)`)
	rows := make([][]kitdbsql.Literal, kitdbsql.MaximumInsertRows)
	for i := range rows {
		rows[i] = []kitdbsql.Literal{{Kind: kitdbsql.LiteralNumber, Text: fmt.Sprint(i)}}
	}
	plan := &kitdbsql.ParsedStatement{Kind: kitdbsql.StatementInsert, Insert: &kitdbsql.InsertStatement{Table: "src", Rows: rows}}
	if _, err := e.ExecutePlan(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	functionTestExecute(t, e, `INSERT INTO src VALUES(10001)`)
	e.maximumResultRows = 20000
	if _, err := e.Execute(context.Background(), `INSERT INTO dst SELECT * FROM src`); err == nil {
		t.Fatal("accepted >10000 rows")
	}
	triggerTestCount(t, e, "dst", 0)
	if got := functionTestExecute(t, e, `INSERT INTO dst SELECT * FROM src LIMIT 10000`); got.Affected != 10000 {
		t.Fatal(got)
	}
	triggerTestCount(t, e, "dst", 10000)
}

func TestSQLSavepointNative(t *testing.T) {
	e := triggerTestOpen(t)
	functionTestExecute(t, e, `CREATE TABLE items (id SERIAL PRIMARY KEY, label TEXT UNIQUE)`)
	if _, err := e.Execute(context.Background(), `SAVEPOINT x`); !errors.Is(err, errSavepointTransaction) {
		t.Fatal(err)
	}
	tx, err := e.BeginTransaction(context.Background(), TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	run := func(q string) Result {
		got, err := tx.Execute(context.Background(), q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return got
	}
	run(`INSERT INTO items(label) VALUES('base')`)
	run(`SAVEPOINT x`)
	run(`INSERT INTO items(label) VALUES('one')`)
	run(`SAVEPOINT x`)
	run(`INSERT INTO items(label) VALUES('two')`)
	run(`ROLLBACK TO x`)
	if got := run(`SELECT label FROM items ORDER BY id`).Rows; fmt.Sprint(got) != "[[base] [one]]" {
		t.Fatal(got)
	}
	run(`RELEASE x`)
	run(`ROLLBACK TO x`)
	if got := run(`SELECT label FROM items ORDER BY id`).Rows; fmt.Sprint(got) != "[[base]]" {
		t.Fatal(got)
	}
	run(`SAVEPOINT y`)
	run(`SAVEPOINT z`)
	run(`ROLLBACK TO y`)
	if _, err := tx.Execute(context.Background(), `RELEASE z`); !errors.Is(err, errSavepointMissing) {
		t.Fatal(err)
	}
	run(`RELEASE y`)
	run(`RELEASE x`)
	got := run(`INSERT INTO items(label) VALUES('next') RETURNING id`)
	if fmt.Sprint(got.Rows) != "[[4]]" {
		t.Fatal("sequence rewound", got)
	}
	for i := 0; i < maximumSQLSavepoints; i++ {
		run(`SAVEPOINT repeated`)
	}
	if _, err := tx.Execute(context.Background(), `SAVEPOINT over`); err == nil {
		t.Fatal("unbounded savepoints")
	}
	for i := 0; i < maximumSQLSavepoints; i++ {
		run(`RELEASE repeated`)
	}
	if _, err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	triggerTestCount(t, e, "items", 2)
	ro, err := e.BeginTransaction(context.Background(), TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Rollback()
	for _, q := range []string{`SAVEPOINT x`, `ROLLBACK TO x`, `RELEASE x`} {
		if _, err := ro.Execute(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInsertSelectTypedBounds(t *testing.T) {
	e := triggerTestOpen(t)
	functionTestExecute(t, e, `CREATE TABLE items(id INTEGER PRIMARY KEY)`)
	selectPlan, _ := kitdbsql.ParseStatement(`INSERT INTO items SELECT 1`)
	selectPlan.Insert.Rows = [][]kitdbsql.Literal{{{Kind: kitdbsql.LiteralNumber, Text: "2"}}}
	if _, err := e.ExecutePlan(context.Background(), &selectPlan); err == nil {
		t.Fatal("mixed source accepted")
	}
	p, _ := kitdbsql.ParseStatement(`INSERT INTO items SELECT 1 ON CONFLICT (id) DO NOTHING`)
	p.Insert.Conflict.Assignments = []kitdbsql.Assignment{{Column: "id"}}
	if _, err := e.ExecutePlan(context.Background(), &p); err == nil {
		t.Fatal("malformed conflict accepted")
	}
	functionTestExecute(t, e, `INSERT INTO items VALUES(1)`)
	e.maximumResultRows = 1
	if _, err := e.Execute(context.Background(), `INSERT INTO items SELECT id+1 FROM items UNION ALL SELECT id+2 FROM items`); err == nil {
		t.Fatal("source truncated")
	}
	triggerTestCount(t, e, "items", 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Execute(ctx, `INSERT INTO items SELECT id+1 FROM items`); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := e.Execute(context.Background(), `INSERT INTO items SELECT * FROM items WHERE * SEARCH 'x'`); err == nil || !strings.Contains(err.Error(), "SEARCH") {
		t.Fatal(err)
	}
}
