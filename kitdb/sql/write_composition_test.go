package sql

import "testing"

func TestWriteCompositionParsing(t *testing.T) {
	for _, q := range []string{
		`INSERT INTO dst SELECT * FROM src RETURNING id`,
		`INSERT INTO dst SELECT id, upper(label) FROM src WHERE id > $1 ON CONFLICT (id) DO UPDATE SET label = excluded.label WHERE dst.label <> excluded.label RETURNING *`,
		`INSERT INTO dst WITH x AS (SELECT * FROM src) SELECT * FROM x ON CONFLICT DO NOTHING`,
		`INSERT INTO dst DEFAULT VALUES ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO dst VALUES (1,2) ON CONFLICT (id) DO UPDATE SET label = DEFAULT`,
	} {
		p, err := ParseStatement(q)
		if err != nil || p.Insert == nil {
			t.Fatalf("%s: %#v %v", q, p, err)
		}
	}
	for _, q := range []string{
		`INSERT INTO dst VALUES (1) ON CONFLICT DO UPDATE SET id=1`,
		`INSERT INTO dst VALUES (1) ON CONFLICT (id,id) DO NOTHING`,
		`INSERT INTO dst VALUES (1) ON CONFLICT (id) DO UPDATE SET a=1,a=2`,
		`INSERT INTO dst VALUES (1) ON CONFLICT (id) WHERE id>0 DO NOTHING`,
		`INSERT INTO dst VALUES (1) ON CONFLICT ON CONSTRAINT c DO NOTHING`,
		`INSERT INTO dst VALUES (1) ON CONFLICT DO NOTHING SET id=2`,
	} {
		if _, err := ParseStatement(q); err == nil {
			t.Fatalf("accepted %s", q)
		}
	}
}

func TestSQLSavepointParsing(t *testing.T) {
	for _, q := range []string{`SAVEPOINT Mixed`, `RELEASE SAVEPOINT mixed`, `ROLLBACK WORK TO SAVEPOINT mixed`, `ROLLBACK TRANSACTION TO mixed`, `/* comment */ SAVEPOINT "mixed";`} {
		p, err := ParseStatement(q)
		if err != nil || p.Savepoint == nil || p.Savepoint.Name != "mixed" {
			t.Fatalf("%s: %#v %v", q, p, err)
		}
	}
	p, err := ParseStatement(`SAVEPOINT "Mixed"`)
	if err != nil || p.Savepoint.Name != "Mixed" {
		t.Fatal(p, err)
	}
	for _, q := range []string{`SAVEPOINT`, `SAVEPOINT $1`, `ROLLBACK TO`, `RELEASE x y`, `SAVEPOINT x; DELETE FROM t`} {
		if _, err := ParseStatement(q); err == nil {
			t.Fatalf("accepted %s", q)
		}
	}
}
