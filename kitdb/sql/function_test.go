package sql

import "testing"

func TestParseSQLFunctions(t *testing.T) {
	statement, err := ParseStatement(`CREATE OR REPLACE FUNCTION clean(s TEXT, n BIGINT) RETURNS TEXT LANGUAGE SQL RETURN upper(trim(s))`)
	if err != nil {
		t.Fatal(err)
	}
	plan := statement.CreateFunction
	if plan == nil || !plan.Replace || plan.Name != "clean" || len(plan.Parameters) != 2 || plan.Parameters[1].Kind != "bigint" || plan.Body.Operator != "upper" {
		t.Fatalf("plan = %#v", plan)
	}
	drop, err := ParseStatement(`DROP FUNCTION IF EXISTS clean(TEXT, BIGINT) RESTRICT`)
	if err != nil || drop.DropFunction == nil || !drop.DropFunction.HasSignature || len(drop.DropFunction.ArgumentKinds) != 2 {
		t.Fatalf("drop = %#v %v", drop, err)
	}
	decimal, err := ParseStatement(`CREATE FUNCTION add_tax(n NUMERIC) RETURNS NUMERIC LANGUAGE SQL RETURN n + 0.1`)
	if err != nil || decimal.CreateFunction == nil || decimal.CreateFunction.ReturnKind != "decimal" {
		t.Fatalf("decimal function = %#v %v", decimal.CreateFunction, err)
	}
	for _, query := range []string{
		`CREATE FUNCTION public.clean(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN s`,
		`CREATE FUNCTION clean(s TEXT, s TEXT) RETURNS TEXT LANGUAGE SQL RETURN s`,
		`CREATE FUNCTION clean(s VARCHAR(30)) RETURNS TEXT LANGUAGE SQL RETURN s`,
		`CREATE OR REPLACE TABLE things (id INTEGER)`,
		`CREATE OR REPLACE INDEX things ON items(id)`,
		`CREATE FUNCTION clean(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN s DELETE FROM things`,
		`DROP FUNCTION clean CASCADE`,
	} {
		if _, err := ParseStatement(query); err == nil {
			t.Fatalf("accepted %s", query)
		}
	}
}
