package relational

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPostgresManagerCatalogUsesSourceRelation(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "shop.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if _, err := engine.Execute(context.Background(), `CREATE TABLE products (id BIGINT PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	catalog, err := engine.postgresCatalogSnapshot("shop")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, query string
		wantRows    int
	}{
		{"tableplus_functions", `SELECT pg_catalog.pg_get_userbyid(p.proowner) AS owner,p.oid AS oid,
            pg_get_function_identity_arguments(p.oid) AS args,n.nspname AS function_schema,
            p.proname AS function_name,
            CASE WHEN p.proisagg THEN 'aggregate' ELSE 'function' END AS function_type
            FROM pg_catalog.pg_proc p
            LEFT JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
            WHERE n.nspname<>'pg_catalog' AND n.nspname<>'information_schema'`, 0},
		{"tableplus_tables", `(SELECT table_name, table_schema, table_type FROM information_schema.tables)
            UNION (SELECT c.relname::text AS table_name, n.nspname::text AS table_schema, 'MATERIALIZED VIEW'
            FROM pg_catalog.pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind = 'm')`, 1},
		{"namespaces", `SELECT nspname FROM pg_catalog.pg_namespace
            WHERE nspname != 'pg_catalog' AND nspname != 'information_schema'`, 1},
		{"projected_comparison_is_not_a_filter", `SELECT CASE WHEN nspname <> 'public' THEN 'system' ELSE 'user' END AS kind
            FROM pg_catalog.pg_namespace`, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, handled, err := executePostgresCatalogQuery(test.query, catalog)
			if err != nil || !handled || len(result.Rows) != test.wantRows {
				t.Fatalf("catalog result: rows=%d want=%d handled=%t err=%v result=%+v", len(result.Rows), test.wantRows, handled, err, result)
			}
		})
	}
}
