package relational

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestFunctionIntrospectionRoundTrip(t *testing.T) {
	e, err := Open(filepath.Join(t.TempDir(), "functions.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for _, q := range []string{
		`CREATE FUNCTION clean(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN upper(trim(s))`,
		`CREATE FUNCTION label(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN coalesce(nullif(trim(s),''),'O''Reilly')`,
		`CREATE FUNCTION sale(price NUMERIC, percent NUMERIC) RETURNS NUMERIC LANGUAGE SQL RETURN round(price * (1 - percent / 100),2)`,
		`CREATE FUNCTION empty_test(s TEXT) RETURNS BOOLEAN LANGUAGE SQL RETURN s IS NULL OR s = ''`,
		`CREATE FUNCTION answer() RETURNS BIGINT LANGUAGE SQL RETURN 9007199254740993`,
		`CREATE FUNCTION quoted("a""b" TEXT) RETURNS TEXT LANGUAGE SQL RETURN "a""b" || '; -- literal, not SQL'`,
	} {
		functionTestExecute(t, e, q)
	}
	catalog, err := e.postgresCatalogSnapshot("shop")
	if err != nil {
		t.Fatal(err)
	}
	catalog.user = "tester"
	for _, function := range catalog.functions {
		oid := postgresCatalogOID("function", function.ID)
		queries := []string{
			fmt.Sprintf(`SELECT pg_get_functiondef(%d)`, oid),
			fmt.Sprintf(`SELECT pg_catalog.pg_get_functiondef(%d::oid)::text AS definition`, oid),
			fmt.Sprintf(`SELECT pg_get_functiondef(p.oid) AS definition FROM pg_catalog.pg_proc p WHERE p.oid = %d`, oid),
			fmt.Sprintf(`SELECT pg_get_functiondef("p"."oid"::oid) FROM pg_proc p WHERE p.proname = '%s'`, function.Name),
		}
		for _, q := range queries {
			result, handled, err := executePostgresCatalogQuery(q, catalog)
			if err != nil || !handled || len(result.Rows) != 1 || result.Columns[0].DataTypeOID != pgwire.OIDText {
				t.Fatalf("%s: %#v %v", q, result, err)
			}
			ddl := string(result.Rows[0][0].Data)
			plan, err := kitdbsql.ParseStatement(ddl)
			if err != nil {
				t.Fatalf("unreadable definition: %s: %v", ddl, err)
			}
			if plan.CreateFunction == nil || plan.CreateFunction.Name != function.Name {
				t.Fatal(ddl)
			}
			functionTestExecute(t, e, ddl)
		}
	}
	if got := functionTestExecute(t, e, `SELECT sale(150000,10)`).Rows[0][0]; fmt.Sprint(got) != "135000" {
		t.Fatal(got)
	}
	if got := functionTestExecute(t, e, `SELECT label(NULL)`).Rows[0][0]; got != "O'Reilly" {
		t.Fatal(got)
	}
	result, handled, err := executePostgresCatalogQuery(`SELECT pg_get_function_identity_arguments(p.oid) AS args, pg_get_function_arguments(p.oid) AS full_args, pg_get_function_result(p.oid) AS result, pg_get_userbyid(p.proowner) AS owner, CASE WHEN p.proisagg THEN 'aggregate' ELSE 'function' END AS kind, CASE WHEN p.prokind = 'a' THEN 'aggregate' ELSE 'function' END AS modern_kind FROM pg_catalog.pg_proc p LEFT JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname <> 'pg_catalog' AND p.proname = 'sale'`, catalog)
	if err != nil || !handled || len(result.Rows) != 1 {
		t.Fatalf("discovery: %#v %v", result, err)
	}
	want := []string{`"price" numeric, "percent" numeric`, `"price" numeric, "percent" numeric`, "numeric", "tester", "function", "function"}
	for i, value := range want {
		if string(result.Rows[0][i].Data) != value || result.Rows[0][i].Null {
			t.Fatalf("column %d = %#v, want %s", i, result.Rows[0][i], value)
		}
	}
	for _, q := range []string{
		`SELECT routine_definition FROM information_schema.routines WHERE routine_name = 'clean'`,
		`SELECT prosrc FROM pg_proc WHERE proname = 'clean'`,
	} {
		result, _, err := executePostgresCatalogQuery(q, catalog)
		if err != nil || len(result.Rows) != 1 || string(result.Rows[0][0].Data) != `RETURN upper(trim("s"))` {
			t.Fatalf("body: %#v %v", result, err)
		}
	}
	for _, q := range []string{`SELECT pg_get_functiondef(0)`, `SELECT pg_get_functiondef(NULL)`} {
		result, _, err := executePostgresCatalogQuery(q, catalog)
		if err != nil || len(result.Rows) != 1 || !result.Rows[0][0].Null {
			t.Fatalf("absent definition: %#v %v", result, err)
		}
	}
	for _, q := range []string{
		`SELECT pg_get_functiondef('not an oid')`,
		`SELECT pg_get_functiondef(4294967296)`,
		`SELECT pg_get_functiondef(p.oid + 1) FROM pg_proc p`,
		`SELECT pg_get_functiondef(p.oid, true) FROM pg_proc p`,
		`SELECT pg_get_functiondef(missing) FROM pg_proc p`,
	} {
		if _, handled, err := executePostgresCatalogQuery(q, catalog); !handled || err == nil {
			t.Fatalf("unsupported expression guessed: %s %v", q, err)
		}
	}
	for _, q := range []string{`SELECT 'pg_get_functiondef(1)'`, `CREATE FUNCTION demo() RETURNS TEXT LANGUAGE SQL RETURN 'pg_get_functiondef(1)'`} {
		if isPostgresCatalogQuery(q) {
			t.Fatalf("misclassified: %s", q)
		}
	}
}

func TestFunctionIntrospectionParametersAndIsolation(t *testing.T) {
	e, err := Open(filepath.Join(t.TempDir(), "source.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	functionTestExecute(t, e, `CREATE FUNCTION echo(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN s`)
	auth, err := e.PostgresAuthenticator(PostgresOptions{Database: "source", User: "reader", Password: "test", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	session, err := auth.Authenticate(context.Background(), pgwire.Startup{Parameters: map[string]string{"database": "source", "user": "reader"}}, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	query := `SELECT pg_get_functiondef($1::oid) AS definition`
	columns, err := session.(pgwire.DescribeSession).Describe(context.Background(), query, nil)
	if err != nil || len(columns) != 1 || columns[0].DataTypeOID != pgwire.OIDText {
		t.Fatalf("describe: %#v %v", columns, err)
	}
	catalog, _ := e.postgresCatalogSnapshot("source")
	oid := postgresCatalogOID("function", catalog.functions[0].ID)
	parameter := pgwire.Parameter{OID: pgwire.OIDOID, Data: []byte(fmt.Sprint(oid))}
	for _, q := range []string{query, `SELECT pg_get_functiondef(oid) FROM pg_proc WHERE oid = $1`} {
		result, err := session.Execute(context.Background(), q, []pgwire.Parameter{parameter})
		if err != nil || len(result.Rows) != 1 || !strings.HasPrefix(string(result.Rows[0][0].Data), `CREATE OR REPLACE FUNCTION "echo"`) {
			t.Fatalf("bound lookup: %#v %v", result, err)
		}
	}
	result, err := session.Execute(context.Background(), `SELECT prosrc FROM pg_proc WHERE oid = $1`, []pgwire.Parameter{{Null: true}})
	if err != nil || len(result.Rows) != 0 {
		t.Fatalf("NULL filter ignored: %#v %v", result, err)
	}
	result, err = session.Execute(context.Background(), `SELECT prosrc FROM pg_proc WHERE proname = $1`, []pgwire.Parameter{{Data: []byte(`echo' OR '1'='1`)}})
	if err != nil || len(result.Rows) != 0 {
		t.Fatalf("literal became SQL: %#v %v", result, err)
	}
	if _, err := session.Execute(context.Background(), query, nil); err == nil {
		t.Fatal("missing parameter ignored")
	}
	if _, err := session.Execute(context.Background(), `BEGIN`, nil); err != nil {
		t.Fatal(err)
	}
	result, err = session.Execute(context.Background(), query, []pgwire.Parameter{parameter})
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0].Null {
		t.Fatalf("transaction metadata: %#v %v", result, err)
	}
	if _, err := session.Execute(context.Background(), `ROLLBACK`, nil); err != nil {
		t.Fatal(err)
	}
	other, err := Open(filepath.Join(t.TempDir(), "other.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	otherCatalog, _ := other.postgresCatalogSnapshot("other")
	result, _, err = executePostgresCatalogQuery(fmt.Sprintf(`SELECT pg_get_functiondef(%d)`, oid), otherCatalog)
	if err != nil || len(result.Rows) != 1 || !result.Rows[0][0].Null {
		t.Fatalf("function crossed database boundary: %#v %v", result, err)
	}
}
