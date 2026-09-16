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

func reverseFKRun(t *testing.T, e *Engine, queries ...string) {
	t.Helper()
	for _, q := range queries {
		functionTestExecute(t, e, q)
	}
}

func reverseFKFail(t *testing.T, e *Engine, q string) {
	t.Helper()
	got, err := e.Execute(context.Background(), q)
	if !errors.Is(err, ErrForeignKeyViolation) || len(got.Rows) != 0 {
		t.Fatalf("%s: %#v %v", q, got, err)
	}
}

func TestReverseForeignKeyUpdateDeleteUpsert(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		t.Run(fmt.Sprint(indexed), func(t *testing.T) {
			e := triggerTestOpen(t)
			reverseFKRun(t, e,
				`CREATE TABLE parents(id INTEGER PRIMARY KEY, code TEXT UNIQUE, n INTEGER)`,
				`CREATE TABLE children(id INTEGER PRIMARY KEY, parent TEXT REFERENCES parents(code) ON UPDATE RESTRICT ON DELETE RESTRICT)`,
				`INSERT INTO parents VALUES(1,'A',1),(2,'B',2),(3,'C',3)`,
				`INSERT INTO children VALUES(1,'A'),(2,NULL)`,
				`CREATE TABLE audit(id SERIAL PRIMARY KEY, parent INTEGER, event TEXT)`,
				`CREATE TRIGGER changed AFTER UPDATE ON parents FOR EACH ROW INSERT INTO audit(parent,event) VALUES(NEW.id,'update')`,
				`CREATE TRIGGER removed AFTER DELETE ON parents FOR EACH ROW INSERT INTO audit(parent,event) VALUES(OLD.id,'delete')`)
			if indexed {
				reverseFKRun(t, e, `CREATE INDEX children_parent ON children(parent)`)
			}
			baseline := functionTestExecute(t, e, `SELECT * FROM parents ORDER BY id`).Rows
			for _, q := range []string{
				`UPDATE parents SET code=code||'X' RETURNING *`,
				`DELETE FROM parents RETURNING *`,
				`INSERT INTO parents VALUES(4,'D',4),(2,'BX',2),(1,'AX',1) ON CONFLICT(id) DO UPDATE SET code=excluded.code RETURNING *`,
			} {
				reverseFKFail(t, e, q)
				if got := functionTestExecute(t, e, `SELECT * FROM parents ORDER BY id`).Rows; !reflect.DeepEqual(got, baseline) {
					t.Fatal(got)
				}
				triggerTestCount(t, e, "parents", 3)
				triggerTestCount(t, e, "audit", 0)
			}
			reverseFKRun(t, e, `UPDATE parents SET n=n+1 WHERE id=1`, `UPDATE parents SET code=code WHERE id=1`,
				`UPDATE parents SET code='BB' WHERE id=2`,
				`INSERT INTO parents VALUES(3,'CC',3) ON CONFLICT(id) DO UPDATE SET code=excluded.code`,
				`DELETE FROM parents WHERE id=2`)
			triggerTestCount(t, e, "parents", 2)
			triggerTestCount(t, e, "audit", 5)
			reverseFKRun(t, e, `DELETE FROM children WHERE parent='A'`, `UPDATE parents SET code='AA' WHERE id=1`, `DELETE FROM parents`)
			triggerTestCount(t, e, "parents", 0)
			triggerTestCount(t, e, "children", 1)
		})
	}
}

func TestReverseForeignKeyCompositeNullAndTypes(t *testing.T) {
	e := triggerTestOpen(t)
	reverseFKRun(t, e, `CREATE TABLE parents(id INTEGER PRIMARY KEY, tenant TEXT, code BIGINT, UNIQUE(tenant,code))`,
		`CREATE TABLE children(id INTEGER PRIMARY KEY, tenant TEXT, code SMALLINT, FOREIGN KEY(tenant,code) REFERENCES parents(tenant,code))`,
		`INSERT INTO parents VALUES(1,'A',7),(2,'B',7),(3,'A',NULL),(4,NULL,7),(5,'A',40000)`,
		`INSERT INTO children VALUES(1,'A',7),(2,'A',NULL),(3,NULL,7)`,
		`CREATE INDEX children_tuple ON children(code,tenant)`)
	reverseFKFail(t, e, `DELETE FROM parents WHERE id=1`)
	reverseFKFail(t, e, `UPDATE parents SET tenant='Z' WHERE id=1`)
	reverseFKRun(t, e, `DELETE FROM parents WHERE id>1`)
	triggerTestCount(t, e, "parents", 1)
	reverseFKRun(t, e, `CREATE TABLE decimal_parent(id INTEGER PRIMARY KEY, code NUMERIC(20,3) UNIQUE)`,
		`CREATE TABLE decimal_child(id INTEGER PRIMARY KEY, code NUMERIC(20,2) REFERENCES decimal_parent(code))`,
		`INSERT INTO decimal_parent VALUES(1,1.235),(2,1.24),(3,9007199254740993.25)`,
		`INSERT INTO decimal_child VALUES(1,1.24),(2,9007199254740993.25)`,
		`CREATE INDEX decimal_child_code ON decimal_child(code)`,
		`DELETE FROM decimal_parent WHERE id=1`)
	reverseFKFail(t, e, `DELETE FROM decimal_parent WHERE id=2`)
	reverseFKFail(t, e, `UPDATE decimal_parent SET code=code+1 WHERE id=3`)
	reverseFKRun(t, e, `CREATE TABLE char_parent(id INTEGER PRIMARY KEY, code CHAR(4) UNIQUE)`,
		`CREATE TABLE char_child(id INTEGER PRIMARY KEY, code CHAR(2) REFERENCES char_parent(code))`,
		`INSERT INTO char_parent VALUES(1,'A'),(2,'LONG')`,
		`INSERT INTO char_child VALUES(1,'A')`,
		`CREATE INDEX char_child_code ON char_child(code)`,
		`DELETE FROM char_parent WHERE id=2`)
	reverseFKFail(t, e, `DELETE FROM char_parent WHERE id=1`)
	reverseFKRun(t, e, `UPDATE char_parent SET code='A ' WHERE id=1`)
}

func TestReverseForeignKeyNoActionAndRestrict(t *testing.T) {
	for _, action := range []string{"NO ACTION", "RESTRICT"} {
		t.Run(action, func(t *testing.T) {
			e := triggerTestOpen(t)
			reverseFKRun(t, e, `CREATE TABLE parents(id INTEGER PRIMARY KEY, code INTEGER UNIQUE)`,
				fmt.Sprintf(`CREATE TABLE children(id INTEGER PRIMARY KEY, code INTEGER REFERENCES parents(code) ON UPDATE %s)`, action),
				`INSERT INTO parents VALUES(1,10),(2,20)`, `INSERT INTO children VALUES(1,10),(2,20)`)
			queries := []string{`UPDATE parents SET code=30-code`, `INSERT INTO parents VALUES(1,30),(2,10),(1,20) ON CONFLICT(id) DO UPDATE SET code=excluded.code`}
			// A plain batch UPDATE can exchange a unique tuple in the existing
			// engine. NO ACTION checks final keys, RESTRICT checks old references.
			if action == "RESTRICT" {
				reverseFKFail(t, e, queries[0])
			} else {
				reverseFKRun(t, e, queries[0])
			}
			if _, err := e.Execute(context.Background(), queries[1]); err == nil {
				t.Fatal("repeated upsert target accepted")
			}
			// Upsert updates and inserts can replace a removed parent key.
			q := `INSERT INTO parents VALUES(1,99),(3,10) ON CONFLICT(id) DO UPDATE SET code=excluded.code`
			if action == "RESTRICT" {
				reverseFKFail(t, e, q)
			} else {
				// After the exchange row 1 owns 20, so replace 20 instead.
				reverseFKRun(t, e, `INSERT INTO parents VALUES(1,99),(3,20) ON CONFLICT(id) DO UPDATE SET code=excluded.code`)
			}
		})
	}
}

func TestReverseForeignKeySelfReferences(t *testing.T) {
	for _, action := range []string{"NO ACTION", "RESTRICT"} {
		t.Run(action, func(t *testing.T) {
			e := triggerTestOpen(t)
			reverseFKRun(t, e, fmt.Sprintf(`CREATE TABLE tree(id INTEGER PRIMARY KEY, code INTEGER UNIQUE, parent INTEGER REFERENCES tree(code) ON UPDATE %s ON DELETE %s)`, action, action),
				`INSERT INTO tree VALUES(1,10,20),(2,20,10)`, `CREATE INDEX tree_parent ON tree(parent)`)
			reverseFKFail(t, e, `DELETE FROM tree WHERE id=1`)
			reverseFKFail(t, e, `UPDATE tree SET code=code+100`)
			reverseFKRun(t, e, `UPDATE tree SET code=code+100,parent=parent+100`, `DELETE FROM tree`)
			triggerTestCount(t, e, "tree", 0)
			reverseFKRun(t, e, `INSERT INTO tree VALUES(1,10,10)`, `INSERT INTO tree VALUES(1,20,20) ON CONFLICT(id) DO UPDATE SET code=excluded.code,parent=excluded.parent`)
			triggerTestCount(t, e, "tree", 1)
			reverseFKRun(t, e, `DELETE FROM tree`)
		})
	}
}

func TestReverseForeignKeyRollbackConflictAndRename(t *testing.T) {
	e := triggerTestOpen(t)
	reverseFKRun(t, e, `CREATE TABLE parents(id INTEGER PRIMARY KEY, code TEXT UNIQUE)`, `CREATE TABLE children(id INTEGER PRIMARY KEY, code TEXT REFERENCES parents(code))`,
		`INSERT INTO parents VALUES(1,'A')`, `INSERT INTO children VALUES(1,'A')`)
	tx, err := e.BeginTransaction(context.Background(), TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, q := range []string{`SAVEPOINT before_delete`, `DELETE FROM children`, `UPDATE parents SET code='B'`, `ROLLBACK TO before_delete`} {
		if _, err := tx.Execute(context.Background(), q); err != nil {
			t.Fatal(q, err)
		}
	}
	if _, err := tx.Execute(context.Background(), `DELETE FROM parents`); !errors.Is(err, ErrForeignKeyViolation) {
		t.Fatal(err)
	}
	if _, err := tx.Execute(context.Background(), `UPDATE parents SET code='B'`); !errors.Is(err, ErrForeignKeyViolation) {
		t.Fatal(err)
	}
	if _, err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	reverseFKRun(t, e, `DELETE FROM children`)
	// Both transactions validate against the same snapshot. Whichever commits
	// second must fail, including a child insert raced against a parent delete.
	for _, parentFirst := range []bool{true, false} {
		parent, err := e.BeginTransaction(context.Background(), TransactionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		child, err := e.BeginTransaction(context.Background(), TransactionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parent.Execute(context.Background(), `DELETE FROM parents`); err != nil {
			t.Fatal(err)
		}
		if _, err := child.Execute(context.Background(), `INSERT INTO children VALUES(1,'A')`); err != nil {
			t.Fatal(err)
		}
		first, second := parent, child
		if !parentFirst {
			first, second = child, parent
		}
		if _, err := first.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := second.Commit(context.Background()); !errors.Is(err, ErrTransactionConflict) {
			t.Fatal(err)
		}
		if parentFirst {
			reverseFKRun(t, e, `INSERT INTO parents VALUES(1,'A')`)
		} else {
			reverseFKRun(t, e, `DELETE FROM children`)
		}
	}
	reverseFKRun(t, e, `INSERT INTO children VALUES(1,'A')`, `ALTER TABLE parents RENAME COLUMN code TO label`, `ALTER TABLE parents RENAME TO renamed`)
	reverseFKFail(t, e, `UPDATE renamed SET label='B'`)
	reverseFKFail(t, e, `DELETE FROM renamed`)
	reverseFKRun(t, e, `DELETE FROM children`, `UPDATE renamed SET label='B'`, `DELETE FROM renamed`)
}

func TestReverseForeignKeySharedBudget(t *testing.T) {
	e := triggerTestOpen(t)
	reverseFKRun(t, e, `CREATE TABLE parents(id INTEGER PRIMARY KEY, code INTEGER UNIQUE)`, `CREATE TABLE children(id INTEGER PRIMARY KEY, code INTEGER REFERENCES parents(code))`,
		`INSERT INTO parents VALUES(1000,1000)`)
	var rows [][]kitdbsql.Literal
	for i := 1; i <= 51; i++ {
		rows = append(rows, []kitdbsql.Literal{{Kind: kitdbsql.LiteralNumber, Text: fmt.Sprint(i)}, {Kind: kitdbsql.LiteralNumber, Text: fmt.Sprint(i)}})
	}
	plan := &kitdbsql.ParsedStatement{Kind: kitdbsql.StatementInsert, Insert: &kitdbsql.InsertStatement{Table: "parents", Rows: rows}}
	if _, err := e.ExecutePlan(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	rows = nil
	for i := 1; i <= 2000; i++ {
		rows = append(rows, []kitdbsql.Literal{{Kind: kitdbsql.LiteralNumber, Text: fmt.Sprint(i)}, {Kind: kitdbsql.LiteralNumber, Text: "1000"}})
	}
	plan.Insert = &kitdbsql.InsertStatement{Table: "children", Rows: rows}
	if _, err := e.ExecutePlan(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DELETE FROM parents WHERE id<1000`, `UPDATE parents SET code=code+2000 WHERE id<1000`, `INSERT INTO parents SELECT id,code+2000 FROM parents WHERE id<1000 ON CONFLICT(id) DO UPDATE SET code=excluded.code`} {
		if result, err := e.Execute(context.Background(), q); !errors.Is(err, ErrForeignKeyCheckLimit) || result.Affected != 0 {
			t.Fatal(q, result, err)
		}
		triggerTestCount(t, e, "parents", 52)
		triggerTestCount(t, e, "children", 2000)
		if got := functionTestExecute(t, e, `SELECT code FROM parents WHERE id=1`).Rows; fmt.Sprint(got) != "[[1]]" {
			t.Fatal(got)
		}
	}
	reverseFKRun(t, e, `CREATE INDEX children_code ON children(code)`, `UPDATE parents SET code=code+2000 WHERE id<1000`, `DELETE FROM parents WHERE id<1000`)
	triggerTestCount(t, e, "parents", 1)
	// Exact ledger boundaries and cancellation do not need a huge fixture.
	checks := &foreignKeyChecks{probes: maximumForeignKeyProbes - 1, entries: maximumForeignKeyEntries - 1, bytes: maximumForeignKeyBytes - 2}
	if err := checks.probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := checks.probe(context.Background()); !errors.Is(err, ErrForeignKeyCheckLimit) {
		t.Fatal(err)
	}
	if err := checks.admit(context.Background(), []byte("a"), []byte("b")); err != nil {
		t.Fatal(err)
	}
	if err := checks.admit(context.Background(), nil, []byte("c")); !errors.Is(err, ErrForeignKeyCheckLimit) {
		t.Fatal(err)
	}
	if err := checks.admit(context.Background(), []byte("a"), nil); !errors.Is(err, ErrForeignKeyCheckLimit) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Execute(ctx, `DELETE FROM parents`); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestReverseForeignKeyRecovery(t *testing.T) {
	const childEnv = "KITDB_REVERSE_FK_CRASH"
	if path := os.Getenv(childEnv); path != "" {
		e, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		reverseFKRun(t, e, `CREATE TABLE parents(id INTEGER PRIMARY KEY, code TEXT UNIQUE)`, `CREATE TABLE children(id INTEGER PRIMARY KEY, code TEXT REFERENCES parents(code))`,
			`CREATE INDEX children_code ON children(code)`, `INSERT INTO parents VALUES(1,'A'),(2,'B')`, `INSERT INTO children VALUES(1,'A')`,
			`UPDATE parents SET code='BB' WHERE id=2`, `DELETE FROM parents WHERE id=2`)
		reverseFKFail(t, e, `DELETE FROM parents`)
		tx, err := e.BeginTransaction(context.Background(), TransactionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{`DELETE FROM children`, `DELETE FROM parents`} {
			if _, err := tx.Execute(context.Background(), q); err != nil {
				t.Fatal(err)
			}
		}
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "reverse.kitdb")
	child := exec.Command(os.Args[0], "-test.run=^TestReverseForeignKeyRecovery$")
	child.Env = append(os.Environ(), childEnv+"="+path)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child: %s %v", out, err)
	}
	e, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if got := functionTestExecute(t, e, `SELECT * FROM parents`).Rows; fmt.Sprint(got) != "[[1 A]]" {
		t.Fatal(got)
	}
	reverseFKFail(t, e, `UPDATE parents SET code='B'`)
	reverseFKFail(t, e, `DELETE FROM parents`)
	reverseFKRun(t, e, `DELETE FROM children`, `DELETE FROM parents`)
	triggerTestCount(t, e, "parents", 0)
}

func TestReverseForeignKeyIndexAccess(t *testing.T) {
	for _, index := range []string{"", "PRIMARY", "UNIQUE", "INDEX", "COMPOSITE", "PARTIAL"} {
		t.Run(index, func(t *testing.T) {
			e := triggerTestOpen(t)
			unique := ""
			if index == "UNIQUE" {
				unique = " UNIQUE"
			}
			idType := "INTEGER PRIMARY KEY"
			if index == "PRIMARY" {
				idType = "INTEGER"
				unique = " PRIMARY KEY"
			}
			reverseFKRun(t, e, `CREATE TABLE p(id INTEGER PRIMARY KEY)`, fmt.Sprintf(`CREATE TABLE c(id %s, pid INTEGER%s REFERENCES p(id), n INTEGER)`, idType, unique), `INSERT INTO p VALUES(1),(2),(3)`, `INSERT INTO c VALUES(1,1,1),(2,2,2)`)
			if index == "INDEX" {
				reverseFKRun(t, e, `CREATE INDEX lookup ON c(pid)`)
			}
			if index == "COMPOSITE" {
				reverseFKRun(t, e, `CREATE INDEX lookup ON c(pid,n)`)
			}
			if index == "PARTIAL" {
				reverseFKRun(t, e, `CREATE INDEX lookup ON c(pid) WHERE n=1`)
			}
			tx, err := e.BeginTransaction(context.Background(), TransactionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			checks := &foreignKeyChecks{}
			ctx := context.WithValue(context.Background(), foreignKeyCheckKey{}, checks)
			p, err := tx.schema("p")
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.validateReverseReferences(ctx, p, []referenceChange{{old: map[string]any{"id": int64(3)}}}, "delete"); err != nil {
				t.Fatal(err)
			}
			// NO ACTION sees the parent still present, so stage its removal first.
			key, err := rowKey(p, map[string]any{"id": int64(3)}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Delete(key); err != nil {
				t.Fatal(err)
			}
			if err := tx.validateReverseReferences(ctx, p, []referenceChange{{old: map[string]any{"id": int64(3)}}}, "delete"); err != nil {
				t.Fatal(err)
			}
			want := 2
			if index == "INDEX" || index == "COMPOSITE" {
				want = 0
			}
			if index == "UNIQUE" || index == "PRIMARY" {
				want = 1
			}
			if checks.entries != want {
				t.Fatalf("entries=%d want=%d", checks.entries, want)
			}
			if _, err := tx.Execute(context.Background(), `DELETE FROM p WHERE id=2`); !errors.Is(err, ErrForeignKeyViolation) {
				t.Fatal(err)
			}
		})
	}
}

func TestReverseForeignKeyPrimaryUpdatesRemainExplicitlyUnsupported(t *testing.T) {
	e := triggerTestOpen(t)
	reverseFKRun(t, e, `CREATE TABLE p(id INTEGER PRIMARY KEY)`, `INSERT INTO p VALUES(1)`)
	if _, err := e.Execute(context.Background(), `UPDATE p SET id=2`); err == nil || !strings.Contains(err.Error(), "primary field") {
		t.Fatal(err)
	}
}
