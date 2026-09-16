package relational

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func actionRows(t *testing.T, e *Engine, query, want string) {
	t.Helper()
	if got := fmt.Sprint(functionTestExecute(t, e, query).Rows); got != want {
		t.Fatalf("%s: got %s want %s", query, got, want)
	}
}

func TestReferentialActionDeleteChain(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		t.Run(fmt.Sprint(indexed), func(t *testing.T) {
			e := triggerTestOpen(t)
			reverseFKRun(t, e,
				`CREATE TABLE p(id INTEGER PRIMARY KEY)`,
				`CREATE TABLE c(id INTEGER PRIMARY KEY, pid INTEGER REFERENCES p(id) ON DELETE CASCADE)`,
				`CREATE TABLE g(id INTEGER PRIMARY KEY, cid INTEGER REFERENCES c(id) ON DELETE CASCADE)`,
				`CREATE TABLE optional(id INTEGER PRIMARY KEY, pid INTEGER REFERENCES p(id) ON DELETE SET NULL)`,
				`CREATE TABLE audit(id SERIAL PRIMARY KEY, kind TEXT, source INTEGER)`,
				`CREATE TRIGGER p_deleted AFTER DELETE ON p FOR EACH ROW INSERT INTO audit(kind,source) VALUES('p',OLD.id)`,
				`CREATE TRIGGER c_deleted AFTER DELETE ON c FOR EACH ROW INSERT INTO audit(kind,source) VALUES('c',OLD.id)`,
				`CREATE TRIGGER g_deleted AFTER DELETE ON g FOR EACH ROW INSERT INTO audit(kind,source) VALUES('g',OLD.id)`,
				`CREATE TRIGGER o_updated AFTER UPDATE ON optional FOR EACH ROW INSERT INTO audit(kind,source) VALUES('o',NEW.id)`,
				`INSERT INTO p VALUES(1),(2)`, `INSERT INTO c VALUES(10,1),(11,1),(20,2)`,
				`INSERT INTO g VALUES(100,10),(101,11),(200,20)`, `INSERT INTO optional VALUES(1,1),(2,2),(3,NULL)`)
			if indexed {
				reverseFKRun(t, e, `CREATE INDEX c_pid ON c(pid)`, `CREATE INDEX g_cid ON g(cid)`, `CREATE INDEX o_pid ON optional(pid)`)
			}
			result := functionTestExecute(t, e, `DELETE FROM p WHERE id=1 RETURNING id`)
			if result.Affected != 1 || fmt.Sprint(result.Rows) != "[[1]]" {
				t.Fatal(result)
			}
			actionRows(t, e, `SELECT * FROM p`, "[[2]]")
			actionRows(t, e, `SELECT * FROM c`, "[[20 2]]")
			actionRows(t, e, `SELECT * FROM g`, "[[200 20]]")
			actionRows(t, e, `SELECT * FROM optional ORDER BY id`, "[[1 <nil>] [2 2] [3 <nil>]]")
			actionRows(t, e, `SELECT kind,COUNT(*) FROM audit GROUP BY kind ORDER BY kind`, "[[c 2] [g 2] [o 1] [p 1]]")
			triggerTestCount(t, e, "c", 1)
			triggerTestCount(t, e, "g", 1)
			// Deleted identities and ordinary index entries are reusable.
			reverseFKRun(t, e, `INSERT INTO p VALUES(1)`, `INSERT INTO c VALUES(10,1)`, `INSERT INTO g VALUES(100,10)`, `DELETE FROM p WHERE id=1`)
			triggerTestCount(t, e, "c", 1)
		})
	}
}

func TestReferentialActionUpdateCompositeAndSwap(t *testing.T) {
	e := triggerTestOpen(t)
	reverseFKRun(t, e,
		`CREATE TABLE p(id INTEGER PRIMARY KEY, tenant INTEGER, code BIGINT, UNIQUE(tenant,code))`,
		`CREATE TABLE c(id INTEGER PRIMARY KEY, tenant INTEGER, code BIGINT, UNIQUE(tenant,code), FOREIGN KEY(tenant,code) REFERENCES p(tenant,code) ON UPDATE CASCADE ON DELETE CASCADE)`,
		`CREATE TABLE g(id INTEGER PRIMARY KEY, tenant INTEGER, code BIGINT, FOREIGN KEY(tenant,code) REFERENCES c(tenant,code) ON UPDATE CASCADE ON DELETE SET NULL)`,
		`CREATE INDEX g_key ON g(tenant,code)`,
		`INSERT INTO p VALUES(1,1,10),(2,1,20),(3,2,10)`,
		`INSERT INTO c VALUES(1,1,10),(2,1,20),(3,2,10)`,
		`INSERT INTO g VALUES(1,1,10),(2,1,20),(3,2,10),(4,NULL,10)`)
	result := functionTestExecute(t, e, `UPDATE p SET code=30-code WHERE tenant=1`)
	if result.Affected != 2 {
		t.Fatal(result)
	}
	actionRows(t, e, `SELECT * FROM c ORDER BY id`, "[[1 1 20] [2 1 10] [3 2 10]]")
	actionRows(t, e, `SELECT * FROM g ORDER BY id`, "[[1 1 20] [2 1 10] [3 2 10] [4 <nil> 10]]")
	reverseFKRun(t, e, `INSERT INTO p VALUES(1,1,9007199254740993) ON CONFLICT(id) DO UPDATE SET code=excluded.code`)
	actionRows(t, e, `SELECT code FROM g WHERE id=1`, "[[9007199254740993]]")
	reverseFKRun(t, e, `DELETE FROM p WHERE id=1`)
	actionRows(t, e, `SELECT tenant,code FROM g WHERE id=1`, "[[<nil> <nil>]]")
	triggerTestCount(t, e, "c", 2)
	// A nullable composite tuple never participates in cascade matching.
	actionRows(t, e, `SELECT tenant,code FROM g WHERE id=4`, "[[<nil> 10]]")
}

func TestReferentialActionSetNullAndNarrowKeys(t *testing.T) {
	e := triggerTestOpen(t)
	reverseFKRun(t, e, `CREATE TABLE p(id INTEGER PRIMARY KEY, code INTEGER UNIQUE)`,
		`CREATE TABLE c(id INTEGER PRIMARY KEY, code INTEGER REFERENCES p(code) ON UPDATE SET NULL)`,
		`INSERT INTO p VALUES(1,1)`, `INSERT INTO c VALUES(1,1)`, `UPDATE p SET code=2`)
	actionRows(t, e, `SELECT code FROM c`, "[[<nil>]]")
	reverseFKRun(t, e, `CREATE TABLE required(id INTEGER PRIMARY KEY, pid INTEGER NOT NULL REFERENCES p(id) ON DELETE SET NULL)`, `INSERT INTO required VALUES(1,1)`)
	if result, err := e.Execute(context.Background(), `DELETE FROM p`); err == nil || result.Affected != 0 {
		t.Fatal(result, err)
	}
	triggerTestCount(t, e, "p", 1)
	actionRows(t, e, `SELECT pid FROM required`, "[[1]]")
	reverseFKRun(t, e, `CREATE TABLE numbers(id INTEGER PRIMARY KEY, code NUMERIC(20,3) UNIQUE)`,
		`CREATE TABLE rounded(id INTEGER PRIMARY KEY, code NUMERIC(20,2) REFERENCES numbers(code) ON UPDATE CASCADE)`,
		`INSERT INTO numbers VALUES(1,1.24)`, `INSERT INTO rounded VALUES(1,1.24)`)
	// A rounded value equal to the old child key must not skip the FK failure.
	reverseFKFail(t, e, `UPDATE numbers SET code=1.235`)
	actionRows(t, e, `SELECT code FROM numbers`, "[[1.24]]")
	reverseFKRun(t, e, `UPDATE numbers SET code=9007199254740993.25`)
	actionRows(t, e, `SELECT code FROM rounded`, "[[9007199254740993.25]]")
	reverseFKRun(t, e, `CREATE TABLE chars(id INTEGER PRIMARY KEY, code CHAR(4) UNIQUE)`,
		`CREATE TABLE short_chars(id INTEGER PRIMARY KEY, code CHAR(2) REFERENCES chars(code) ON UPDATE CASCADE)`,
		`INSERT INTO chars VALUES(1,'A')`, `INSERT INTO short_chars VALUES(1,'A')`, `UPDATE chars SET code='B'`)
	actionRows(t, e, `SELECT code FROM short_chars`, "[[B ]]")
}

func TestReferentialActionSelfCyclesAndDiamond(t *testing.T) {
	e := triggerTestOpen(t)
	reverseFKRun(t, e, `CREATE TABLE tree(id INTEGER PRIMARY KEY, code INTEGER UNIQUE, parent INTEGER REFERENCES tree(code) ON UPDATE CASCADE ON DELETE CASCADE)`,
		`INSERT INTO tree VALUES(1,1,1),(2,2,1),(3,3,2)`, `UPDATE tree SET code=10 WHERE id=1`)
	actionRows(t, e, `SELECT * FROM tree ORDER BY id`, "[[1 10 10] [2 2 10] [3 3 2]]")
	reverseFKRun(t, e, `DELETE FROM tree WHERE id=1`)
	triggerTestCount(t, e, "tree", 0)
	reverseFKRun(t, e, `INSERT INTO tree VALUES(1,1,2),(2,2,1)`, `DELETE FROM tree WHERE id=1`)
	triggerTestCount(t, e, "tree", 0)
	reverseFKRun(t, e,
		`CREATE TABLE p(id INTEGER PRIMARY KEY)`,
		`CREATE TABLE a(id INTEGER PRIMARY KEY REFERENCES p(id) ON DELETE CASCADE)`,
		`CREATE TABLE b(id INTEGER PRIMARY KEY REFERENCES p(id) ON DELETE CASCADE)`,
		`CREATE TABLE c(id INTEGER PRIMARY KEY, aid INTEGER REFERENCES a(id) ON DELETE CASCADE, bid INTEGER REFERENCES b(id) ON DELETE CASCADE)`,
		`CREATE TABLE audit(id SERIAL PRIMARY KEY, deleted INTEGER)`,
		`CREATE TRIGGER once_only AFTER DELETE ON c FOR EACH ROW INSERT INTO audit(deleted) VALUES(OLD.id)`,
		`INSERT INTO p VALUES(1)`, `INSERT INTO a VALUES(1)`, `INSERT INTO b VALUES(1)`, `INSERT INTO c VALUES(1,1,1)`, `DELETE FROM p`)
	triggerTestCount(t, e, "a", 0)
	triggerTestCount(t, e, "b", 0)
	triggerTestCount(t, e, "c", 0)
	triggerTestCount(t, e, "audit", 1)
}

func TestReferentialActionFailureRollback(t *testing.T) {
	e := triggerTestOpen(t)
	reverseFKRun(t, e, `CREATE TABLE p(id INTEGER PRIMARY KEY, code INTEGER UNIQUE)`,
		`CREATE TABLE c(id INTEGER PRIMARY KEY, pid INTEGER REFERENCES p(id) ON DELETE CASCADE, code INTEGER REFERENCES p(code) ON UPDATE CASCADE CHECK(code<10))`,
		`CREATE TABLE g(id INTEGER PRIMARY KEY, cid INTEGER REFERENCES c(id) ON DELETE RESTRICT)`,
		`CREATE TABLE audit(id SERIAL PRIMARY KEY, deleted INTEGER)`,
		`CREATE TRIGGER c_deleted AFTER DELETE ON c FOR EACH ROW INSERT INTO audit(deleted) VALUES(OLD.id)`,
		`INSERT INTO p VALUES(1,1),(2,2)`, `INSERT INTO c VALUES(1,1,1),(2,2,2)`, `INSERT INTO g VALUES(1,2)`)
	for _, q := range []string{`DELETE FROM p`, `UPDATE p SET code=code+10`, `INSERT INTO p VALUES(1,11) ON CONFLICT(id) DO UPDATE SET code=excluded.code`, `DELETE FROM p WHERE id=1 RETURNING 1/(id-1)`} {
		if result, err := e.Execute(context.Background(), q); err == nil || result.Affected != 0 {
			t.Fatal(q, result, err)
		}
		triggerTestCount(t, e, "p", 2)
		triggerTestCount(t, e, "c", 2)
		triggerTestCount(t, e, "g", 1)
		triggerTestCount(t, e, "audit", 0)
		actionRows(t, e, `SELECT code FROM p ORDER BY id`, "[[1] [2]]")
	}
	reverseFKRun(t, e, `DELETE FROM g`)
	tx, err := e.BeginTransaction(context.Background(), TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, q := range []string{`SAVEPOINT before_delete`, `DELETE FROM p`, `ROLLBACK TO before_delete`} {
		if _, err := tx.Execute(context.Background(), q); err != nil {
			t.Fatal(q, err)
		}
	}
	if _, err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	triggerTestCount(t, e, "p", 2)
	triggerTestCount(t, e, "c", 2)
	triggerTestCount(t, e, "audit", 0)
}

func TestReferentialActionBoundsAndUnsupported(t *testing.T) {
	e := triggerTestOpen(t)
	reverseFKRun(t, e, `CREATE TABLE p(id INTEGER PRIMARY KEY, code INTEGER UNIQUE)`,
		`CREATE TABLE c(id INTEGER PRIMARY KEY, pid INTEGER REFERENCES p(id) ON DELETE CASCADE)`,
		`CREATE INDEX c_pid ON c(pid)`, `INSERT INTO p VALUES(1,1)`, `INSERT INTO c VALUES(1,1),(2,1),(3,1)`)
	old := e.maximumMutationRows
	e.maximumMutationRows = 2
	if _, err := e.Execute(context.Background(), `DELETE FROM p`); !errors.Is(err, ErrForeignKeyCheckLimit) {
		t.Fatal(err)
	}
	e.maximumMutationRows = old
	triggerTestCount(t, e, "p", 1)
	triggerTestCount(t, e, "c", 3)
	reverseFKRun(t, e, `CREATE TABLE keyed(code INTEGER PRIMARY KEY REFERENCES p(code) ON UPDATE CASCADE)`, `INSERT INTO keyed VALUES(1)`)
	if _, err := e.Execute(context.Background(), `UPDATE p SET code=2`); !errors.Is(err, ErrReferentialActionUnsupported) {
		t.Fatal(err)
	}
	actionRows(t, e, `SELECT code FROM p`, "[[1]]")
	// Explicitly reject contradictory update paths, not whichever runs last.
	reverseFKRun(t, e, `CREATE TABLE pair(id INTEGER PRIMARY KEY, a INTEGER UNIQUE, b INTEGER UNIQUE)`,
		`CREATE TABLE dual(id INTEGER PRIMARY KEY, v INTEGER, CONSTRAINT via_a FOREIGN KEY(v) REFERENCES pair(a) ON UPDATE CASCADE, CONSTRAINT via_b FOREIGN KEY(v) REFERENCES pair(b) ON UPDATE CASCADE)`,
		`INSERT INTO pair VALUES(1,1,1)`, `INSERT INTO dual VALUES(1,1)`)
	if _, err := e.Execute(context.Background(), `UPDATE pair SET a=2,b=3`); !errors.Is(err, ErrReferentialActionUnsupported) {
		t.Fatal(err)
	}
	actionRows(t, e, `SELECT v FROM dual`, "[[1]]")
	reverseFKRun(t, e, `UPDATE pair SET a=2,b=2`)
	actionRows(t, e, `SELECT v FROM dual`, "[[2]]")
}

func TestReferentialActionConflictAndRecovery(t *testing.T) {
	const childEnv = "KITDB_ACTION_CRASH"
	if path := os.Getenv(childEnv); path != "" {
		e, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		reverseFKRun(t, e, `CREATE TABLE p(id INTEGER PRIMARY KEY, code INTEGER UNIQUE)`,
			`CREATE TABLE c(id INTEGER PRIMARY KEY, code INTEGER REFERENCES p(code) ON UPDATE CASCADE ON DELETE CASCADE)`,
			`CREATE INDEX c_code ON c(code)`, `INSERT INTO p VALUES(1,1),(2,2)`, `INSERT INTO c VALUES(1,1),(2,2)`,
			`UPDATE p SET code=10 WHERE id=1`, `DELETE FROM p WHERE id=2`)
		tx, err := e.BeginTransaction(context.Background(), TransactionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Execute(context.Background(), `DELETE FROM p`); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "actions.kitdb")
	child := exec.Command(os.Args[0], "-test.run=^TestReferentialActionConflictAndRecovery$")
	child.Env = append(os.Environ(), childEnv+"="+path)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	e, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	actionRows(t, e, `SELECT * FROM p`, "[[1 10]]")
	actionRows(t, e, `SELECT * FROM c`, "[[1 10]]")
	for _, parentFirst := range []bool{true, false} {
		parent, err := e.BeginTransaction(context.Background(), TransactionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer parent.Rollback()
		child, err := e.BeginTransaction(context.Background(), TransactionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer child.Rollback()
		if _, err := parent.Execute(context.Background(), `DELETE FROM p`); err != nil {
			t.Fatal(err)
		}
		if _, err := child.Execute(context.Background(), `INSERT INTO c VALUES(3,10)`); err != nil {
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
			reverseFKRun(t, e, `INSERT INTO p VALUES(1,10)`, `INSERT INTO c VALUES(1,10)`)
		} else {
			reverseFKRun(t, e, `DELETE FROM c WHERE id=3`)
		}
	}
	reverseFKRun(t, e, `ALTER TABLE p RENAME COLUMN code TO label`, `ALTER TABLE p RENAME TO renamed`, `UPDATE renamed SET label=20`)
	actionRows(t, e, `SELECT code FROM c`, "[[20]]")
}

func TestReferentialActionDeterministicModel(t *testing.T) {
	e := triggerTestOpen(t)
	reverseFKRun(t, e, `CREATE TABLE p(id INTEGER PRIMARY KEY, code INTEGER UNIQUE)`, `CREATE TABLE c(id INTEGER PRIMARY KEY, code INTEGER REFERENCES p(code) ON UPDATE CASCADE ON DELETE CASCADE)`, `CREATE INDEX c_code ON c(code)`)
	var rows [][]kitdbsql.Literal
	for i := 1; i <= 32; i++ {
		rows = append(rows, []kitdbsql.Literal{{Kind: kitdbsql.LiteralNumber, Text: fmt.Sprint(i)}, {Kind: kitdbsql.LiteralNumber, Text: fmt.Sprint(i)}})
	}
	for _, table := range []string{"p", "c"} {
		if _, err := e.ExecutePlan(context.Background(), &kitdbsql.ParsedStatement{Kind: kitdbsql.StatementInsert, Insert: &kitdbsql.InsertStatement{Table: table, Rows: rows}}); err != nil {
			t.Fatal(err)
		}
	}
	model := make(map[int]int)
	for i := 1; i <= 32; i++ {
		model[i] = i
	}
	check := func() {
		t.Helper()
		var want [][]int
		for i := 1; i <= 32; i++ {
			if value, ok := model[i]; ok {
				want = append(want, []int{i, value})
			}
		}
		for _, table := range []string{"p", "c"} {
			actionRows(t, e, `SELECT * FROM `+table+` ORDER BY id`, fmt.Sprint(want))
		}
	}
	for step := 0; step < 20; step++ {
		reverseFKRun(t, e, fmt.Sprintf(`UPDATE p SET code=code+100 WHERE id %% 3=%d`, step%3))
		for id := range model {
			if id%3 == step%3 {
				model[id] += 100
			}
		}
		check()
	}
	for rem := 0; rem < 3; rem++ {
		reverseFKRun(t, e, fmt.Sprintf(`DELETE FROM p WHERE id %% 3=%d`, rem))
		for id := range model {
			if id%3 == rem {
				delete(model, id)
			}
		}
		check()
	}
}

func TestReferentialActionRoundBoundary(t *testing.T) {
	e := triggerTestOpen(t)
	reverseFKRun(t, e, `CREATE TABLE tree(id INTEGER PRIMARY KEY, parent INTEGER REFERENCES tree(id) ON DELETE CASCADE)`, `CREATE INDEX parent_lookup ON tree(parent)`)
	var rows [][]kitdbsql.Literal
	for i := 1; i <= maximumReferentialRounds+1; i++ {
		parent := kitdbsql.Literal{Kind: kitdbsql.LiteralNull}
		if i > 1 {
			parent = kitdbsql.Literal{Kind: kitdbsql.LiteralNumber, Text: fmt.Sprint(i - 1)}
		}
		rows = append(rows, []kitdbsql.Literal{{Kind: kitdbsql.LiteralNumber, Text: fmt.Sprint(i)}, parent})
	}
	if _, err := e.ExecutePlan(context.Background(), &kitdbsql.ParsedStatement{Kind: kitdbsql.StatementInsert, Insert: &kitdbsql.InsertStatement{Table: "tree", Rows: rows}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(context.Background(), `DELETE FROM tree WHERE id=1`); !errors.Is(err, ErrForeignKeyCheckLimit) {
		t.Fatal(err)
	}
	triggerTestCount(t, e, "tree", maximumReferentialRounds+1)
	reverseFKRun(t, e, fmt.Sprintf(`DELETE FROM tree WHERE id=%d`, maximumReferentialRounds+1), `DELETE FROM tree WHERE id=1`)
	triggerTestCount(t, e, "tree", 0)
}
