package relational

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReferentialDefaultDeleteUpdateAndUpsert(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		for _, event := range []string{"delete", "update", "upsert"} {
			t.Run(fmt.Sprintf("%t/%s", indexed, event), func(t *testing.T) {
				e := triggerTestOpen(t)
				reverseFKRun(t, e, `CREATE TABLE p(id INTEGER PRIMARY KEY, code INTEGER UNIQUE)`,
					`CREATE TABLE c(id INTEGER PRIMARY KEY, code INTEGER DEFAULT 0 REFERENCES p(code) ON DELETE SET DEFAULT ON UPDATE SET DEFAULT)`,
					`CREATE TABLE audit(id SERIAL PRIMARY KEY, old_code INTEGER, new_code INTEGER)`,
					`CREATE TRIGGER changed AFTER UPDATE ON c FOR EACH ROW INSERT INTO audit(old_code,new_code) VALUES(OLD.code,NEW.code)`,
					`INSERT INTO p VALUES(0,0),(1,1),(2,2)`, `INSERT INTO c VALUES(1,1),(2,2),(3,NULL)`)
				if indexed {
					reverseFKRun(t, e, `CREATE INDEX c_code ON c(code)`)
				}
				query := `DELETE FROM p WHERE id=1 RETURNING id`
				if event == "update" {
					query = `UPDATE p SET code=3 WHERE id=1 RETURNING id`
				}
				if event == "upsert" {
					query = `INSERT INTO p VALUES(1,3) ON CONFLICT(id) DO UPDATE SET code=excluded.code RETURNING id`
				}
				result := functionTestExecute(t, e, query)
				if result.Affected != 1 || fmt.Sprint(result.Rows) != "[[1]]" {
					t.Fatal(result)
				}
				actionRows(t, e, `SELECT * FROM c ORDER BY id`, "[[1 0] [2 2] [3 <nil>]]")
				actionRows(t, e, `SELECT old_code,new_code FROM audit`, "[[1 0]]")
				for _, invalid := range []string{`DELETE FROM p WHERE id=0`, `UPDATE p SET code=9 WHERE id=0`, `INSERT INTO p VALUES(0,9) ON CONFLICT(id) DO UPDATE SET code=excluded.code`} {
					// DEFAULT stays 0, but the referenced 0 would no longer exist.
					reverseFKFail(t, e, invalid)
					actionRows(t, e, `SELECT code FROM p WHERE id=0`, "[[0]]")
					actionRows(t, e, `SELECT code FROM c WHERE id=1`, "[[0]]")
					triggerTestCount(t, e, "audit", 1)
				}
			})
		}
	}
}

func TestReferentialDefaultCompositeDomainAndCascade(t *testing.T) {
	e := triggerTestOpen(t)
	reverseFKRun(t, e,
		`CREATE DOMAIN fallback_amount AS NUMERIC(20,2) DEFAULT 9007199254740993.25 CHECK(VALUE>0)`,
		`CREATE TABLE p(id INTEGER PRIMARY KEY, tenant INTEGER, code NUMERIC(20,2), UNIQUE(tenant,code))`,
		`CREATE TABLE c(id INTEGER PRIMARY KEY, tenant INTEGER DEFAULT 0, code fallback_amount, UNIQUE(tenant,code), FOREIGN KEY(tenant,code) REFERENCES p(tenant,code) ON UPDATE SET DEFAULT ON DELETE SET DEFAULT)`,
		`CREATE TABLE g(id INTEGER PRIMARY KEY, tenant INTEGER, code NUMERIC(20,2), FOREIGN KEY(tenant,code) REFERENCES c(tenant,code) ON UPDATE CASCADE)`,
		`CREATE TABLE override_default(id INTEGER PRIMARY KEY, tenant INTEGER DEFAULT 0, code fallback_amount DEFAULT 7.5, FOREIGN KEY(tenant,code) REFERENCES p(tenant,code) ON DELETE SET DEFAULT)`,
		`INSERT INTO p VALUES(0,0,9007199254740993.25),(1,1,1.25),(2,0,7.5)`,
		`INSERT INTO c VALUES(1,1,1.25),(2,NULL,1.25)`, `INSERT INTO g VALUES(1,1,1.25)`, `INSERT INTO override_default VALUES(1,1,1.25)`, `DELETE FROM p WHERE id=1`)
	actionRows(t, e, `SELECT tenant,code FROM c ORDER BY id`, "[[0 9007199254740993.25] [<nil> 1.25]]")
	actionRows(t, e, `SELECT tenant,code FROM g`, "[[0 9007199254740993.25]]")
	actionRows(t, e, `SELECT tenant,code FROM override_default`, "[[0 7.5]]")
	// Defaults obey the child type's canonical padding, not the parent width.
	reverseFKRun(t, e, `CREATE TABLE chars(id INTEGER PRIMARY KEY, code CHAR(4) UNIQUE)`,
		`CREATE TABLE short_chars(id INTEGER PRIMARY KEY, code CHAR(2) DEFAULT 'Z' REFERENCES chars(code) ON DELETE SET DEFAULT)`,
		`INSERT INTO chars VALUES(1,'A'),(2,'Z')`, `INSERT INTO short_chars VALUES(1,'A')`, `DELETE FROM chars WHERE id=1`)
	actionRows(t, e, `SELECT code FROM short_chars`, "[[Z ]]")
}

func TestReferentialDefaultConstraintRollback(t *testing.T) {
	for _, definition := range []string{
		`code INTEGER DEFAULT 9 REFERENCES p(code) ON DELETE SET DEFAULT`,
		`code INTEGER NOT NULL REFERENCES p(code) ON DELETE SET DEFAULT`,
		`code INTEGER DEFAULT 0 CHECK(code>0) REFERENCES p(code) ON DELETE SET DEFAULT`,
		`code INTEGER DEFAULT 0 UNIQUE REFERENCES p(code) ON DELETE SET DEFAULT`,
	} {
		t.Run(definition, func(t *testing.T) {
			e := triggerTestOpen(t)
			reverseFKRun(t, e, `CREATE TABLE p(id INTEGER PRIMARY KEY, code INTEGER UNIQUE)`, `CREATE TABLE c(id INTEGER PRIMARY KEY, `+definition+`)`,
				`INSERT INTO p VALUES(0,0),(1,1),(2,2)`, `INSERT INTO c VALUES(1,1),(2,2)`)
			if r, err := e.Execute(context.Background(), `DELETE FROM p WHERE id>0`); err == nil || r.Affected != 0 {
				t.Fatal(r, err)
			}
			actionRows(t, e, `SELECT * FROM p ORDER BY id`, "[[0 0] [1 1] [2 2]]")
			actionRows(t, e, `SELECT * FROM c ORDER BY id`, "[[1 1] [2 2]]")
		})
	}
	e := triggerTestOpen(t)
	reverseFKRun(t, e, `CREATE TABLE p(id INTEGER PRIMARY KEY)`,
		`CREATE TABLE c(id INTEGER PRIMARY KEY, pid INTEGER REFERENCES p(id) ON DELETE SET DEFAULT)`,
		`INSERT INTO p VALUES(1)`, `INSERT INTO c VALUES(1,1)`, `DELETE FROM p`)
	actionRows(t, e, `SELECT pid FROM c`, "[[<nil>]]")
}

func TestReferentialDefaultSelfReferenceAndClock(t *testing.T) {
	e := triggerTestOpen(t)
	reverseFKRun(t, e, `CREATE TABLE tree(id INTEGER PRIMARY KEY, parent_id INTEGER DEFAULT 0 REFERENCES tree(id) ON DELETE SET DEFAULT)`,
		`INSERT INTO tree VALUES(0,NULL),(1,0),(2,1),(3,1)`, `DELETE FROM tree WHERE id=1`)
	actionRows(t, e, `SELECT * FROM tree ORDER BY id`, "[[0 <nil>] [2 0] [3 0]]")
	reverseFKFail(t, e, `DELETE FROM tree WHERE id=0`)
	reverseFKRun(t, e, `DELETE FROM tree`)
	triggerTestCount(t, e, "tree", 0)
	// Admit both days so crossing UTC midnight cannot make a valid test fail.
	start := time.Now().UTC()
	today, tomorrow := start.Format("2006-01-02"), start.AddDate(0, 0, 1).Format("2006-01-02")
	reverseFKRun(t, e, `CREATE TABLE days(id INTEGER PRIMARY KEY, day DATE UNIQUE)`,
		`CREATE TABLE events(id INTEGER PRIMARY KEY, day DATE DEFAULT CURRENT_DATE REFERENCES days(day) ON DELETE SET DEFAULT)`,
		fmt.Sprintf(`INSERT INTO days VALUES(0,'%s'),(1,'%s'),(2,'2000-01-01')`, today, tomorrow),
		`INSERT INTO events VALUES(1,'2000-01-01'),(2,'2000-01-01')`, `DELETE FROM days WHERE id=2`)
	result := functionTestExecute(t, e, `SELECT day FROM events ORDER BY id`)
	if len(result.Rows) != 2 || fmt.Sprint(result.Rows[0]) != fmt.Sprint(result.Rows[1]) {
		t.Fatal(result)
	}
	day := fmt.Sprint(result.Rows[0][0])
	if day != today && day != tomorrow {
		t.Fatal("clock default did not use the statement clock", result)
	}
}

func TestReferentialDefaultByteBudget(t *testing.T) {
	e := triggerTestOpen(t)
	text := strings.Repeat("x", 512)
	reverseFKRun(t, e, `CREATE TABLE p(id INTEGER PRIMARY KEY, code TEXT UNIQUE)`,
		`CREATE TABLE c(id INTEGER PRIMARY KEY, code TEXT DEFAULT '`+text+`' REFERENCES p(code) ON DELETE SET DEFAULT)`,
		`INSERT INTO p VALUES(1,'old')`, `INSERT INTO c VALUES(1,'old')`)
	ctx := context.Background()
	tx, err := e.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	parent, err := tx.schema("p")
	if err != nil {
		t.Fatal(err)
	}
	checks := &foreignKeyChecks{}
	bindings, err := tx.reverseForeignKeys(ctx, checks, parent)
	if err != nil || len(bindings) != 1 {
		t.Fatal(bindings, err)
	}
	binding := bindings[0]
	conditions, possible := binding.conditions([]any{"old"})
	if !possible {
		t.Fatal("cannot probe old value")
	}
	checks.bytes = maximumForeignKeyBytes - 256
	actions := make(map[string]*referentialRowAction)
	if err := tx.collectReferentialRows(ctx, checks, actions, binding, conditions, "set default", nil); !errors.Is(err, ErrForeignKeyCheckLimit) {
		t.Fatal(err)
	}
	if len(actions) != 0 || checks.actionRows != 0 {
		t.Fatal("over-budget default reached the action queue", actions, checks)
	}
	field := binding.local[0]
	checks = &foreignKeyChecks{bytes: maximumForeignKeyBytes - len(field.Default) - len(text)}
	value, err := tx.referentialDefault(ctx, checks, field)
	if err != nil || value != text || checks.bytes != maximumForeignKeyBytes {
		t.Fatal(value, checks, err)
	}
	if _, err := tx.referentialDefault(ctx, checks, field); !errors.Is(err, ErrForeignKeyCheckLimit) {
		t.Fatal(err)
	}
	actionRows(t, e, `SELECT code FROM c`, "[[old]]")
}

func TestReferentialDefaultSequencesSavepointsAndDuplicatePaths(t *testing.T) {
	e := triggerTestOpen(t)
	s := e.NewSession()
	ctx := context.Background()
	for _, q := range []string{
		`CREATE SEQUENCE fallback AS INTEGER START 10 CACHE 1`, `CREATE TABLE p(id INTEGER PRIMARY KEY)`,
		`CREATE TABLE c(id INTEGER PRIMARY KEY, pid INTEGER DEFAULT nextval('fallback'), CONSTRAINT a FOREIGN KEY(pid) REFERENCES p(id) ON DELETE SET DEFAULT, CONSTRAINT b FOREIGN KEY(pid) REFERENCES p(id) ON DELETE SET DEFAULT)`,
		`CREATE INDEX c_pid ON c(pid)`, `CREATE TABLE audit(id SERIAL PRIMARY KEY, value INTEGER)`,
		`CREATE TRIGGER changed AFTER UPDATE ON c FOR EACH ROW INSERT INTO audit(value) VALUES(NEW.pid)`,
		`INSERT INTO p VALUES(1),(10),(11),(12),(13)`, `INSERT INTO c VALUES(1,1),(2,1)`,
	} {
		if _, err := s.Execute(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	tx, err := s.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	sequenceQuery(t, tx, `SAVEPOINT before_delete`)
	sequenceQuery(t, tx, `DELETE FROM p WHERE id=1`)
	sequenceQuery(t, tx, `SELECT SUM(pid) FROM c`, int64(21))
	sequenceQuery(t, tx, `SELECT currval('fallback')`, int64(11))
	sequenceQuery(t, tx, `ROLLBACK TO before_delete`)
	sequenceQuery(t, tx, `SELECT SUM(pid) FROM c`, int64(2))
	sequenceQuery(t, tx, `SELECT currval('fallback')`, int64(11))
	if _, err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	triggerTestCount(t, e, "audit", 0)
	sequenceQuery(t, s, `DELETE FROM p WHERE id=1`)
	sequenceQuery(t, s, `SELECT SUM(pid) FROM c`, int64(25))
	sequenceQuery(t, s, `SELECT currval('fallback')`, int64(13))
	triggerTestCount(t, e, "audit", 2)
	// Literal DEFAULT can coincide with an old tuple replaced by another parent.
	reverseFKRun(t, e, `CREATE TABLE replacement(id INTEGER PRIMARY KEY, code INTEGER UNIQUE)`,
		`CREATE TABLE same_value(id INTEGER PRIMARY KEY, code INTEGER DEFAULT 1 REFERENCES replacement(code) ON UPDATE SET DEFAULT)`,
		`INSERT INTO replacement VALUES(1,1),(2,2)`, `INSERT INTO same_value VALUES(1,1)`, `UPDATE replacement SET code=3-code`)
	actionRows(t, e, `SELECT code FROM same_value`, "[[1]]")
}

func TestReferentialDefaultSequenceBoundsFailureAndIdentity(t *testing.T) {
	e := triggerTestOpen(t)
	s := e.NewSession()
	ctx := context.Background()
	for _, q := range []string{`CREATE SEQUENCE fallback AS INTEGER START 10 CACHE 1`, `CREATE TABLE p(id INTEGER PRIMARY KEY)`,
		`CREATE TABLE c(id INTEGER PRIMARY KEY, pid INTEGER DEFAULT nextval('fallback') REFERENCES p(id) ON DELETE SET DEFAULT)`,
		`INSERT INTO p VALUES(1)`, `INSERT INTO c VALUES(1,1),(2,1)`} {
		if _, err := s.Execute(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	maximum := e.maximumMutationRows
	e.maximumMutationRows = 1
	if _, err := s.Execute(ctx, `DELETE FROM p`); !errors.Is(err, ErrForeignKeyCheckLimit) {
		t.Fatal(err)
	}
	e.maximumMutationRows = maximum
	sequenceQuery(t, s, `SELECT currval('fallback')`, int64(10)) // no allocation for the rejected second row
	triggerTestCount(t, e, "p", 1)
	actionRows(t, e, `SELECT pid FROM c ORDER BY id`, "[[1] [1]]")
	if _, err := s.Execute(ctx, `DELETE FROM p`); !errors.Is(err, ErrForeignKeyViolation) {
		t.Fatal(err)
	}
	sequenceQuery(t, s, `SELECT currval('fallback')`, int64(12))
	actionRows(t, e, `SELECT pid FROM c ORDER BY id`, "[[1] [1]]")
	// Read-only rejection cannot evaluate a sequence-backed action.
	tx, err := s.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Execute(ctx, `DELETE FROM p`); err == nil {
		t.Fatal("read-only action accepted")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	sequenceQuery(t, s, `SELECT currval('fallback')`, int64(12))
	for _, q := range []string{
		`CREATE TABLE identity_child(id INTEGER PRIMARY KEY, pid INTEGER GENERATED ALWAYS AS IDENTITY REFERENCES p(id) ON DELETE SET DEFAULT)`,
		`INSERT INTO identity_child(id,pid) OVERRIDING SYSTEM VALUE VALUES(1,1)`, `INSERT INTO p VALUES(20)`,
		`SELECT setval('identity_child_pid_seq',20,false)`, `DELETE FROM c`, `DELETE FROM p WHERE id=1`,
	} {
		if _, err := s.Execute(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	sequenceQuery(t, s, `SELECT pid FROM identity_child`, int64(20))
	sequenceQuery(t, s, `SELECT currval('identity_child_pid_seq')`, int64(20))
	// Physical primary-key rewriting is still deliberately unavailable.
	reverseFKRun(t, e, `CREATE TABLE keyed(pid INTEGER PRIMARY KEY DEFAULT 20 REFERENCES p(id) ON DELETE SET DEFAULT)`, `INSERT INTO keyed VALUES(20)`)
	if _, err := e.Execute(ctx, `DELETE FROM p WHERE id=20`); !errors.Is(err, ErrReferentialActionUnsupported) {
		t.Fatal(err)
	}
}

func TestReferentialDefaultConflict(t *testing.T) {
	for _, parentFirst := range []bool{true, false} {
		t.Run(fmt.Sprint(parentFirst), func(t *testing.T) {
			e := triggerTestOpen(t)
			reverseFKRun(t, e, `CREATE TABLE p(id INTEGER PRIMARY KEY)`, `CREATE TABLE c(id INTEGER PRIMARY KEY, pid INTEGER DEFAULT 0 REFERENCES p(id) ON DELETE SET DEFAULT)`, `INSERT INTO p VALUES(0),(1)`, `INSERT INTO c VALUES(1,1)`)
			a, err := e.BeginTransaction(context.Background(), TransactionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Rollback()
			b, err := e.BeginTransaction(context.Background(), TransactionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer b.Rollback()
			if _, err := a.Execute(context.Background(), `DELETE FROM p WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			if _, err := b.Execute(context.Background(), `DELETE FROM p WHERE id=0`); err != nil {
				t.Fatal(err)
			}
			first, second := a, b
			if !parentFirst {
				first, second = b, a
			}
			if _, err := first.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := second.Commit(context.Background()); !errors.Is(err, ErrTransactionConflict) {
				t.Fatal(err)
			}
			want := "[[0]]"
			if !parentFirst {
				want = "[[1]]"
			}
			actionRows(t, e, `SELECT pid FROM c`, want)
			actionRows(t, e, `SELECT id FROM p`, want)
		})
	}
}

func TestReferentialDefaultRecovery(t *testing.T) {
	const childEnv = "KITDB_DEFAULT_CRASH"
	if path := os.Getenv(childEnv); path != "" {
		e, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		reverseFKRun(t, e, `CREATE TABLE p(id INTEGER PRIMARY KEY, code INTEGER UNIQUE)`,
			`CREATE TABLE c(id INTEGER PRIMARY KEY, code INTEGER DEFAULT 0 REFERENCES p(code) ON UPDATE SET DEFAULT ON DELETE SET DEFAULT)`,
			`CREATE INDEX c_code ON c(code)`, `CREATE TABLE audit(id SERIAL PRIMARY KEY, value INTEGER)`,
			`CREATE TRIGGER changed AFTER UPDATE ON c FOR EACH ROW INSERT INTO audit(value) VALUES(NEW.code)`,
			`INSERT INTO p VALUES(0,0),(1,1),(2,2),(3,3)`, `INSERT INTO c VALUES(1,1),(2,2),(3,3)`,
			`UPDATE p SET code=11 WHERE id=1`, `DELETE FROM p WHERE id=2`)
		tx, err := e.BeginTransaction(context.Background(), TransactionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Execute(context.Background(), `DELETE FROM p WHERE id=3`); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "default.kitdb")
	child := exec.Command(os.Args[0], "-test.run=^TestReferentialDefaultRecovery$")
	child.Env = append(os.Environ(), childEnv+"="+path)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	e, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	actionRows(t, e, `SELECT * FROM p ORDER BY id`, "[[0 0] [1 11] [3 3]]")
	actionRows(t, e, `SELECT * FROM c ORDER BY id`, "[[1 0] [2 0] [3 3]]")
	triggerTestCount(t, e, "audit", 2)
	reverseFKFail(t, e, `DELETE FROM p WHERE id=0`)
	reverseFKRun(t, e, `ALTER TABLE p RENAME COLUMN code TO label`, `ALTER TABLE p RENAME TO renamed`, `DELETE FROM renamed WHERE id=3`)
	actionRows(t, e, `SELECT code FROM c ORDER BY id`, "[[0] [0] [0]]")
	triggerTestCount(t, e, "audit", 3)
}
