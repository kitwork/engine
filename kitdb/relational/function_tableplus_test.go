package relational

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/pgwire"
)

// Keep the client's compact syntax: previous probes used only one WHEN and
// missed the error which prevented TablePlus from populating its function tree.
const tablePlusFunctionListing = `SELECT pg_catalog.pg_get_userbyid(p.proowner) as owner,p.oid AS oid,pg_get_function_identity_arguments(p.oid)AS args,n.nspname AS function_schema,p.proname AS function_name,CASE WHEN p.prokind = 'a'THEN'aggregate' WHEN p.prokind='w'THEN'window' WHEN p.prokind='p'THEN'procedure' ELSE'function'END AS function_type FROM pg_catalog.pg_proc p LEFT JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname<>'pg_catalog'AND n.nspname<>'information_schema';`

const tablePlusLegacyFunctionListing = `SELECT pg_catalog.pg_get_userbyid(p.proowner) as owner,p.oid AS oid,pg_get_function_identity_arguments(p.oid)AS args,n.nspname AS function_schema,p.proname AS function_name,CASE WHEN p.proisagg THEN'aggregate' WHEN p.proiswindow THEN'window' WHEN p.prorettype='pg_catalog.trigger'::pg_catalog.regtype THEN'trigger' ELSE'function'END AS function_type FROM pg_catalog.pg_proc p LEFT JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname<>'pg_catalog'AND n.nspname<>'information_schema';`

func TestFunctionTablePlusListing(t *testing.T) {
	e, err := Open(filepath.Join(t.TempDir(), "functions.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	functionTestExecute(t, e, `CREATE FUNCTION clean(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN upper(trim(s))`)
	functionTestExecute(t, e, `CREATE FUNCTION total(price NUMERIC, quantity INTEGER) RETURNS NUMERIC LANGUAGE SQL RETURN price * quantity`)
	catalog, err := e.postgresCatalogSnapshot("shop")
	if err != nil {
		t.Fatal(err)
	}
	catalog.user = "tester"
	for _, q := range []string{tablePlusFunctionListing, tablePlusLegacyFunctionListing} {
		result, handled, err := executePostgresCatalogQuery(q, catalog)
		if err != nil || !handled || len(result.Rows) != 2 {
			t.Fatalf("listing: %#v %v", result, err)
		}
		for i, name := range []string{"owner", "oid", "args", "function_schema", "function_name", "function_type"} {
			if len(result.Columns) != 6 || result.Columns[i].Name != name {
				t.Fatalf("column %d: %#v", i, result.Columns)
			}
			wantOID := uint32(pgwire.OIDText)
			if i == 1 {
				wantOID = pgwire.OIDOID
			}
			if result.Columns[i].DataTypeOID != wantOID {
				t.Fatalf("column %s wire type = %d, want %d", name, result.Columns[i].DataTypeOID, wantOID)
			}
		}
		seen := make(map[string]bool)
		for _, row := range result.Rows {
			name := string(row[4].Data)
			var function *storedFunction
			for _, candidate := range catalog.functions {
				if candidate.Name == name {
					function = candidate
				}
			}
			if function == nil || seen[name] {
				t.Fatalf("unexpected function %q", name)
			}
			seen[name] = true
			want := []string{"tester", fmt.Sprint(postgresCatalogOID("function", function.ID)), functionArgumentsSQL(function), "public", name, "function"}
			for i, value := range want {
				if row[i].Null || string(row[i].Data) != value {
					t.Fatalf("%s column %d = %#v, want %s", name, i, row[i], value)
				}
			}
		}
	}
	q := `SELECT p.oid AS oid,CASE WHEN p.provolatile='v'THEN'VOLATILE' WHEN p.provolatile='s'THEN'STABLE' WHEN p.provolatile='i'THEN'IMMUTABLE' ELSE'VOLATILE'END AS function_volatility,l.lanname AS function_lang,n.nspname AS function_schema,p.proname AS function_name,CASE WHEN p.proisagg THEN'aggregate' WHEN p.prorettype='pg_catalog.trigger'::pg_catalog.regtype THEN'trigger' ELSE'function'END AS function_type FROM pg_catalog.pg_proc p LEFT JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace LEFT JOIN pg_catalog.pg_language l ON l.oid=p.prolang WHERE n.nspname<>'pg_catalog'AND n.nspname<>'information_schema';`
	result, handled, err := executePostgresCatalogQuery(q, catalog)
	if err != nil || !handled || len(result.Rows) != 2 {
		t.Fatalf("details: %#v %v", result, err)
	}
	for _, row := range result.Rows {
		if string(row[1].Data) != "IMMUTABLE" || string(row[2].Data) != "sql" || string(row[5].Data) != "function" {
			t.Fatalf("details: %#v", row)
		}
	}
	for _, function := range catalog.functions {
		q := fmt.Sprintf(`select pg_get_functiondef(%d)AS create_function;`, postgresCatalogOID("function", function.ID))
		result, handled, err := executePostgresCatalogQuery(q, catalog)
		if err != nil || !handled || len(result.Rows) != 1 || result.Columns[0].Name != "create_function" || string(result.Rows[0][0].Data) != functionDefinitionSQL(function) {
			t.Fatalf("definition: %#v %v", result, err)
		}
		q = fmt.Sprintf(`SELECT p.prosrc as create_function FROM pg_proc p JOIN pg_namespace n ON p.pronamespace=n.oid WHERE p.proname='%s' AND n.nspname='public'`, function.Name)
		result, handled, err = executePostgresCatalogQuery(q, catalog)
		if err != nil || !handled || len(result.Rows) != 1 || string(result.Rows[0][0].Data) != "RETURN "+functionExpressionSQL(function.Body) {
			t.Fatalf("legacy definition: %#v %v", result, err)
		}
	}
}

func TestFunctionCatalogCaseBranches(t *testing.T) {
	dataset := postgresFunctions(postgresCatalogSnapshot{})
	for _, test := range []struct {
		expression string
		row        map[string]any
		want       any
	}{
		{`CASE WHEN p.prokind='a' THEN 'aggregate' WHEN p.prokind='w' THEN 'window' WHEN p.prokind='p' THEN 'procedure' ELSE 'function' END`, map[string]any{"prokind": "w"}, "window"},
		{`CASE WHEN p.prokind='a' THEN 'aggregate' WHEN p.prokind='p' THEN 'procedure' ELSE 'function' END`, map[string]any{"prokind": "p"}, "procedure"},
		{`CASE WHEN proisagg THEN 'first' WHEN proiswindow THEN 'second' ELSE 'none' END`, map[string]any{"proisagg": true, "proiswindow": true}, "first"},
		{`CASE WHEN proisagg THEN 'first' WHEN proiswindow THEN 'second' ELSE 'none' END`, map[string]any{"proiswindow": true}, "second"},
		{`CASE WHEN proisagg THEN 'first' END`, map[string]any{"proisagg": false}, nil},
		{`CASE WHEN proisagg THEN NULL ELSE 'no' END`, map[string]any{"proisagg": true}, nil},
		{`CASE WHEN prokind = NULL THEN 'bad' ELSE 'null does not match' END`, map[string]any{"prokind": nil}, "null does not match"},
		{`CASE WHEN prokind = 'f' THEN 'when' ELSE 'end' END`, map[string]any{"prokind": "f"}, "when"},
		{`CASE WHEN prorettype = 'pg_catalog.trigger'::pg_catalog.regtype THEN 'trigger' ELSE 'function' END`, map[string]any{"prorettype": uint32(2279)}, "trigger"},
		{`CASE WHEN prorettype = 'text'::regtype THEN 'text' ELSE 'other' END`, map[string]any{"prorettype": pgwire.OIDText}, "text"},
	} {
		t.Run(test.expression, func(t *testing.T) {
			projection, handled, err := compileFunctionCatalogProjection(test.expression, dataset)
			if err != nil || !handled || projection.evaluate == nil {
				t.Fatalf("compile: %v %v", handled, err)
			}
			value, err := projection.evaluate(test.row)
			if err != nil || value != test.want {
				t.Fatalf("got %#v %v, want %#v", value, err, test.want)
			}
		})
	}
	for _, expression := range []string{
		`CASE END`, `CASE WHEN`, `CASE WHEN p.`, `CASE WHEN 'proisagg' THEN 'yes' END`,
		`CASE WHEN unknown THEN 'yes' ELSE 'no' END`, `CASE WHEN prokind THEN 'yes' END`,
		`CASE WHEN proisagg THEN 1 END`, `CASE WHEN proisagg THEN 'yes' ELSE 'no'`,
		`CASE WHEN prokind='f' THEN 'yes' END junk`,
		`CASE WHEN prokind='f' OR proisagg THEN 'yes' END`,
		`CASE WHEN prokind='f'::regtype THEN 'yes' END`,
		`CASE WHEN prorettype='fake.trigger'::regtype THEN 'yes' END`,
		`CASE WHEN prorettype='trigger'::fake.regtype THEN 'yes' END`,
		`CASE WHEN prorettype='unknown'::regtype THEN 'yes' END`,
		`CASE ` + strings.Repeat(`WHEN proisagg THEN 'yes' `, 17) + `END`,
	} {
		if _, handled, err := compileFunctionCatalogProjection(expression, dataset); !handled || err == nil {
			t.Fatalf("unsupported CASE guessed: %s %v", expression, err)
		}
	}
	if _, _, err := compileFunctionCatalogProjection(`CASE `+strings.Repeat(`WHEN proisagg THEN 'yes' `, 16)+`END`, dataset); err != nil {
		t.Fatalf("branch limit boundary: %v", err)
	}
}

func TestFunctionTablePlusPostgresWire(t *testing.T) {
	e, err := Open(filepath.Join(t.TempDir(), "functions.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	done := make(chan error, 1)
	go func() {
		done <- e.ServePostgres(ctx, listener, PostgresServerOptions{PostgresOptions: PostgresOptions{Database: "shop", User: "tester", Password: "test-only"}})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	})
	db, err := sql.Open("postgres", fmt.Sprintf("postgres://tester:test-only@%s/shop?sslmode=disable", listener.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION clean(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN upper(trim(s))`); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{tablePlusFunctionListing, tablePlusLegacyFunctionListing} {
		var owner, args, schema, name, kind string
		var oid int64
		if err := db.QueryRowContext(ctx, query).Scan(&owner, &oid, &args, &schema, &name, &kind); err != nil {
			t.Fatal(err)
		}
		if owner != "tester" || oid <= 0 || args != `"s" text` || schema != "public" || name != "clean" || kind != "function" {
			t.Fatalf("listing: %s %d %s %s %s %s", owner, oid, args, schema, name, kind)
		}
		var definition string
		if err := db.QueryRowContext(ctx, `SELECT pg_get_functiondef($1::oid) AS create_function`, oid).Scan(&definition); err != nil || !strings.HasPrefix(definition, `CREATE OR REPLACE FUNCTION "clean"`) {
			t.Fatalf("definition: %s %v", definition, err)
		}
	}
}
