package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kitwork/engine/kitdb/pgwire"
)

// newCatalogFixture is one database of two files for the catalog: kitwork.db (public) with a rowid key, a text key, a
// UNIQUE constraint, a plain and a partial index, two foreign keys and a view; billing.db (billing) with one table.
func newCatalogFixture(t *testing.T) (Target, *sql.DB) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "application")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	public := filepath.Join(directory, "kitwork.db")
	billing := filepath.Join(directory, "billing.db")
	writer := createFile(t, public,
		"CREATE TABLE warehouses (id INTEGER PRIMARY KEY, name TEXT NOT NULL)",
		"CREATE TABLE products (id TEXT PRIMARY KEY, sku TEXT NOT NULL UNIQUE, title TEXT, price INTEGER, status TEXT DEFAULT 'active')",
		"CREATE INDEX idx_products_sku ON products (sku)",
		"CREATE INDEX idx_products_active ON products (status) WHERE status = 'active'",
		"CREATE TABLE warehouse_items (id INTEGER PRIMARY KEY, warehouse_id INTEGER NOT NULL REFERENCES warehouses (id) ON DELETE CASCADE, product_id TEXT REFERENCES products (id), amount INTEGER)",
		"CREATE VIEW cheap AS SELECT id, title FROM products WHERE price < 100",
		"INSERT INTO warehouses VALUES (1, 'Hà Nội')",
		"INSERT INTO products VALUES ('p1', 'KIT-1', 'Alpha', 125, 'active'), ('p2', 'KIT-2', 'Beta', 50, 'gone'), ('p3', 'KIT-3', 'Gamma', 75, 'active')",
		"INSERT INTO warehouse_items VALUES (1, 1, 'p1', 1250)",
	)
	createFile(t, billing, "CREATE TABLE invoices (id INTEGER PRIMARY KEY, total INTEGER)")
	return Target{Name: "shop.example/application", Schemas: map[string]string{"public": public, "billing": billing}}, writer
}

// catalogColumns is what kitwork_columns would say: warehouse_items.amount is decimal(12,2).
type catalogColumns struct{}

func (catalogColumns) Column(schema, table, column string) (string, int, int, bool) {
	if schema+"."+table+"."+column == "public.warehouse_items.amount" {
		return "decimal", 12, 2, true
	}
	return "", 0, 0, false
}

func openCatalogSession(t *testing.T, target Target) *Session {
	t.Helper()
	session, err := Open(context.Background(), target, newAccess("public.products"), catalogColumns{}, Options{User: "tester"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// The statements TablePlus sends to list and open a database (as the KitDB port's tests recorded them), word for
// word: they run, and answer what a PostgreSQL with the same tables would.
//
// CONTROL: with the translator returning the statement as written, "SELECT current_database(): no such function:
// current_database"; with catalogRows leaving out the files' relations, "tables = """; with authorize refusing the
// TEMP database like a file outside the schemas, the table list fails with 42501.
func TestCatalogAnswersTablePlus(t *testing.T) {
	target, _ := newCatalogFixture(t)
	session := openCatalogSession(t, target)
	run := func(query string, parameters ...pgwire.Parameter) string {
		t.Helper()
		return cells(mustRun(t, session, query, parameters...))
	}

	if got := run("SELECT current_database()"); got != "shop.example/application" {
		t.Fatalf("current_database() = %q", got)
	}
	tables := run(`(SELECT table_name, table_schema, table_type FROM information_schema.tables) UNION (SELECT c.relname::text AS table_name, n.nspname::text AS table_schema, 'MATERIALIZED VIEW' FROM pg_catalog.pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind = 'm') ORDER BY table_schema, table_name`)
	if want := "invoices,billing,BASE TABLE|cheap,public,VIEW|products,public,BASE TABLE|warehouse_items,public,BASE TABLE|warehouses,public,BASE TABLE"; tables != want {
		t.Fatalf("tables = %q, want %q", tables, want)
	}
	if got := run(`SELECT nspname FROM pg_catalog.pg_namespace WHERE nspname != 'pg_catalog' AND nspname != 'information_schema' ORDER BY nspname`); got != "billing|public" {
		t.Fatalf("schemas = %q", got)
	}
	for _, functions := range []string{
		`SELECT pg_catalog.pg_get_userbyid(p.proowner) as owner,p.oid AS oid,pg_get_function_identity_arguments(p.oid)AS args,n.nspname AS function_schema,p.proname AS function_name,CASE WHEN p.prokind = 'a'THEN'aggregate' WHEN p.prokind='w'THEN'window' WHEN p.prokind='p'THEN'procedure' ELSE'function'END AS function_type FROM pg_catalog.pg_proc p LEFT JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname<>'pg_catalog'AND n.nspname<>'information_schema';`,
		`SELECT pg_catalog.pg_get_userbyid(p.proowner) as owner,p.oid AS oid,pg_get_function_identity_arguments(p.oid)AS args,n.nspname AS function_schema,p.proname AS function_name,CASE WHEN p.proisagg THEN'aggregate' WHEN p.proiswindow THEN'window' WHEN p.prorettype='pg_catalog.trigger'::pg_catalog.regtype THEN'trigger' ELSE'function'END AS function_type FROM pg_catalog.pg_proc p LEFT JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname<>'pg_catalog'AND n.nspname<>'information_schema';`,
	} {
		if result := mustRun(t, session, functions); len(result.Rows) != 0 || len(result.Columns) != 6 {
			t.Fatalf("functions: %d rows, %d columns, want none and 6", len(result.Rows), len(result.Columns))
		}
	}

	columns := run(`SELECT table_name, column_name, ordinal_position, is_nullable, data_type, udt_name
FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = $1
ORDER BY ordinal_position`, textParameters("products")...)
	if want := "products,id,1,NO,text,text|products,sku,2,NO,text,text|products,title,3,YES,text,text|products,price,4,YES,bigint,int8|products,status,5,YES,text,text"; columns != want {
		t.Fatalf("columns = %q, want %q", columns, want)
	}
	if got := run(`SELECT column_default FROM information_schema.columns WHERE table_name = 'products' AND column_name = 'status'`); got != "'active'" {
		t.Fatalf("column_default = %q", got)
	}
	itemsOID := run(`SELECT oid FROM pg_class WHERE relkind='r' AND relname='warehouse_items'`)
	attributes := run(`SELECT a.attname, a.attnum, a.attnotnull, t.typname,
       pg_catalog.format_type(a.atttypid, a.atttypmod) AS formatted_type
FROM pg_catalog.pg_attribute AS a
JOIN pg_catalog.pg_type AS t ON t.oid = a.atttypid
WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`, textParameters(itemsOID)...)
	if want := "id,1,t,int8,bigint|warehouse_id,2,t,int8,bigint|product_id,3,f,text,text|amount,4,f,numeric,numeric(12,2)"; attributes != want {
		t.Fatalf("attributes = %q, want %q", attributes, want)
	}

	primary := run(`
SELECT kc.ordinal_position AS ordinal_position,
       tc.constraint_name AS constraint_name,
       kc.column_name AS column_name
FROM information_schema.table_constraints tc,
     information_schema.key_column_usage kc
WHERE tc.constraint_type='PRIMARY KEY'
  AND kc.table_name=tc.table_name
  AND kc.table_schema=tc.table_schema
  AND kc.constraint_name=tc.constraint_name
  AND tc.table_schema='public'
  AND tc.table_name='products'
ORDER BY kc.ordinal_position ASC`)
	if primary != "1,products_pkey,id" {
		t.Fatalf("primary key = %q", primary)
	}
	if got := run(`SELECT constraint_name, constraint_type FROM information_schema.table_constraints WHERE table_schema='public' AND table_name='products' AND constraint_type='UNIQUE'`); got != "products_sku_key,UNIQUE" {
		t.Fatalf("unique = %q", got)
	}

	indexes := strings.Split(run(`
SELECT ix.relname AS index_name,
       upper(am.amname) AS index_algorithm,
       indisunique AS is_unique,
       pg_get_indexdef(indexrelid) AS index_definition,
       REPLACE(regexp_replace(regexp_replace(pg_get_indexdef(indexrelid),' WHERE .+',''),'.*\((.*)\)','\1'),' ','') AS column_name,
       CASE WHEN position(' WHERE ' IN pg_get_indexdef(indexrelid))>0
            THEN regexp_replace(pg_get_indexdef(indexrelid),'.+WHERE ','') ELSE '' END AS condition,
       pg_catalog.obj_description(i.indexrelid,'pg_class') AS comment
FROM pg_index i
JOIN pg_class t ON t.oid=i.indrelid
JOIN pg_class ix ON ix.oid=i.indexrelid
JOIN pg_namespace n ON t.relnamespace=n.oid
JOIN pg_am AS am ON ix.relam=am.oid
WHERE t.relname='products' AND n.nspname='public'`), "|")
	sort.Strings(indexes)
	if want := []string{
		"idx_products_active,BTREE,f,CREATE INDEX idx_products_active ON public.products USING btree (status) WHERE status = 'active',status,status = 'active',NULL",
		"idx_products_sku,BTREE,f,CREATE INDEX idx_products_sku ON public.products USING btree (sku),sku,,NULL",
		"products_pkey,BTREE,t,CREATE UNIQUE INDEX products_pkey ON public.products USING btree (id),id,,NULL",
		"products_sku_key,BTREE,t,CREATE UNIQUE INDEX products_sku_key ON public.products USING btree (sku),sku,,NULL",
	}; strings.Join(indexes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("indexes =\n%s\nwant\n%s", strings.Join(indexes, "\n"), strings.Join(want, "\n"))
	}

	foreign := strings.Split(run(fmt.Sprintf(`
SELECT c.conname AS constraint_name,
       tf.schema AS child_schema,
       tf.name AS child_name,
       (SELECT STRING_AGG(QUOTE_IDENT(a.attname),',' ORDER BY t.seq)
          FROM (SELECT ROW_NUMBER() OVER (ROWS UNBOUNDED PRECEDING) AS seq,attnum
                  FROM UNNEST(c.conkey) AS t(attnum)) AS t
          INNER JOIN pg_attribute AS a ON a.attrelid=c.conrelid AND a.attnum=t.attnum) AS child_column,
       tt.schema AS parent_schema,
       tt.name AS parent_name,
       (SELECT STRING_AGG(QUOTE_IDENT(a.attname),',' ORDER BY t.seq)
          FROM (SELECT ROW_NUMBER() OVER (ROWS UNBOUNDED PRECEDING) AS seq,attnum
                  FROM UNNEST(c.confkey) AS t(attnum)) AS t
          INNER JOIN pg_attribute AS a ON a.attrelid=c.confrelid AND a.attnum=t.attnum) AS parent_column,
       CASE confupdtype WHEN 'r' THEN 'restrict' WHEN 'c' THEN 'cascade' WHEN 'n' THEN 'set null'
            WHEN 'd' THEN 'set default' WHEN 'a' THEN 'no action' ELSE NULL END AS on_update,
       CASE confdeltype WHEN 'r' THEN 'restrict' WHEN 'c' THEN 'cascade' WHEN 'n' THEN 'set null'
            WHEN 'd' THEN 'set default' WHEN 'a' THEN 'no action' ELSE NULL END AS on_delete
FROM pg_catalog.pg_constraint AS c
INNER JOIN (SELECT pg_class.oid,QUOTE_IDENT(pg_namespace.nspname) AS schema,
                   QUOTE_IDENT(pg_class.relname) AS name
              FROM pg_class INNER JOIN pg_namespace ON pg_class.relnamespace=pg_namespace.oid) AS tf
        ON tf.oid=c.conrelid
INNER JOIN (SELECT pg_class.oid,QUOTE_IDENT(pg_namespace.nspname) AS schema,
                   QUOTE_IDENT(pg_class.relname) AS name
              FROM pg_class INNER JOIN pg_namespace ON pg_class.relnamespace=pg_namespace.oid) AS tt
        ON tt.oid=c.confrelid
WHERE tf.oid='%s' AND c.contype='f'`, itemsOID)), "|")
	sort.Strings(foreign)
	if want := []string{
		"warehouse_items_product_id_fkey,public,warehouse_items,product_id,public,products,id,no action,no action",
		"warehouse_items_warehouse_id_fkey,public,warehouse_items,warehouse_id,public,warehouses,id,no action,cascade",
	}; strings.Join(foreign, "\n") != strings.Join(want, "\n") {
		t.Fatalf("foreign keys =\n%s\nwant\n%s", strings.Join(foreign, "\n"), strings.Join(want, "\n"))
	}

	if got := run(`SELECT reltuples::int8 as count from pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
where nspname='public'AND relname='products'`); got != "-1" {
		t.Fatalf("reltuples before ANALYZE = %q", got)
	}
}

// What else clients write against the catalog: typed result columns, reg* casts both ways, ANY over an array or a
// parameter, regular expressions, ILIKE, E” strings, definitions, settings, PostgreSQL's names for lone calls; and
// the catalog follows the files: a table made by another connection is listed at the next statement, ANALYZE gives
// reltuples, and a transaction block keeps the catalog it began with.
//
// CONTROL: with refreshCatalog never filling again once filled, "after another connection made a table: tables =
// "products|warehouse_items|warehouses", want … extra"; with kitwork_pg_lookup keeping a rowid, the oid of
// pg_get_constraintdef(oid) is the lookup's own rowid: "pg_get_constraintdef = "NULL"".
func TestCatalogQueries(t *testing.T) {
	target, writer := newCatalogFixture(t)
	session := openCatalogSession(t, target)
	run := func(query string, parameters ...pgwire.Parameter) string {
		t.Helper()
		return cells(mustRun(t, session, query, parameters...))
	}

	typed := mustRun(t, session, "SELECT c.oid, c.relname, c.relkind, c.relhasindex, c.relnatts FROM pg_class c WHERE c.relname = 'products'")
	types := make([]uint32, len(typed.Columns))
	for index, column := range typed.Columns {
		types[index] = column.DataTypeOID
	}
	if fmt.Sprint(types) != fmt.Sprint([]uint32{pgwire.OIDOID, oidName, oidChar, pgwire.OIDBool, pgwire.OIDInt2}) {
		t.Fatalf("catalog column types = %v", types)
	}
	if row := cells(typed); !strings.HasSuffix(row, ",products,r,t,5") {
		t.Fatalf("pg_class row = %q", row)
	}

	productsOID := run("SELECT 'products'::regclass")
	if got := run("SELECT oid FROM pg_class WHERE relname = 'products'"); got != productsOID {
		t.Fatalf("'products'::regclass = %q, pg_class says %q", productsOID, got)
	}
	if got := run("SELECT 'public.products'::regclass = '\"public\".\"products\"'::regclass"); got != "1" {
		t.Fatalf("qualified and quoted names of one table differ: %q", got)
	}
	if got := run("SELECT c.oid::regclass::text FROM pg_class c WHERE c.relname IN ('invoices', 'products') ORDER BY 1"); got != "billing.invoices|products" {
		t.Fatalf("oid::regclass = %q", got)
	}
	if got := run("SELECT attname FROM pg_attribute WHERE attrelid = 'products'::regclass AND attnum > 0 ORDER BY attnum"); got != "id|sku|title|price|status" {
		t.Fatalf("attributes by regclass = %q", got)
	}
	if got := run("SELECT 'pg_class'::regclass, 'integer'::regtype, 'pg_catalog.int4'::regtype::int"); got != "1259,23,23" {
		t.Fatalf("catalog reg* = %q", got)
	}
	if got := run("SELECT relname FROM pg_class WHERE relkind = ANY(ARRAY['r','v']) AND relnamespace = 2200 ORDER BY relname"); got != "cheap|products|warehouse_items|warehouses" {
		t.Fatalf("= ANY(ARRAY[…]) = %q", got)
	}
	if got := run("SELECT relname FROM pg_class WHERE relkind = ANY($1) ORDER BY relname", textParameters("{v}")...); got != "cheap" {
		t.Fatalf("= ANY($1) = %q", got)
	}
	if got := run("SELECT relname FROM pg_class WHERE relnamespace = 2200 AND relkind <> ALL('{i,v}') ORDER BY relname"); got != "products|warehouse_items|warehouses" {
		t.Fatalf("<> ALL = %q", got)
	}
	if got := run("SELECT nspname FROM pg_namespace WHERE nspname !~ '^pg_' AND nspname <> 'information_schema' ORDER BY 1"); got != "billing|public" {
		t.Fatalf("!~ = %q", got)
	}
	if got := run("SELECT nspname FROM pg_namespace WHERE nspname ~* '^PG_C'"); got != "pg_catalog" {
		t.Fatalf("~* = %q", got)
	}
	if got := run("SELECT table_name FROM information_schema.tables WHERE table_name ILIKE 'PROD%'"); got != "products" {
		t.Fatalf("ILIKE = %q", got)
	}
	if got := run(`SELECT E'a\tb', E'it''s'`); got != "a\tb,it's" {
		t.Fatalf("E'' strings = %q", got)
	}
	if got := run("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'warehouse_items_warehouse_id_fkey'"); got != "FOREIGN KEY (warehouse_id) REFERENCES warehouses(id) ON DELETE CASCADE" {
		t.Fatalf("pg_get_constraintdef = %q", got)
	}
	if got := run("SELECT pg_get_viewdef('cheap'::regclass)"); got != "SELECT id, title FROM products WHERE price < 100" {
		t.Fatalf("pg_get_viewdef = %q", got)
	}
	if got := run("SELECT current_setting('server_version'), pg_catalog.format_type(1700, 786438), quote_ident('Hà Nội'), quote_ident('name')"); got != ServerVersion+",numeric(12,2),\"Hà Nội\",name" {
		t.Fatalf("functions = %q", got)
	}
	named := mustRun(t, session, "SELECT current_database(), current_user, version(), relname::text FROM pg_class WHERE relname = 'products'")
	var names []string
	for _, column := range named.Columns {
		names = append(names, column.Name)
	}
	if fmt.Sprint(names) != "[current_database current_user version relname]" {
		t.Fatalf("names of lone calls = %v", names)
	}
	if got := cells(named); !strings.HasPrefix(got, "shop.example/application,tester,PostgreSQL 16.0 (Kitwork, SQLite ") {
		t.Fatalf("lone calls = %q", got)
	}
	if got := run("SELECT rolname FROM pg_roles"); got != "tester" {
		t.Fatalf("roles = %q", got)
	}

	// The catalog follows the files.
	listed := func() string {
		return run("SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' AND table_type = 'BASE TABLE' ORDER BY 1")
	}
	if _, err := writer.Exec("CREATE TABLE extra (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if got := listed(); got != "extra|products|warehouse_items|warehouses" {
		t.Fatalf("after another connection made a table: tables = %q, want … extra", got)
	}
	if _, err := writer.Exec("ANALYZE"); err != nil {
		t.Fatal(err)
	}
	if got := run("SELECT reltuples::int8 FROM pg_class WHERE relname = 'products'"); got != "3" {
		t.Fatalf("reltuples after ANALYZE = %q", got)
	}
	mustRun(t, session, "BEGIN")
	if _, err := writer.Exec("CREATE TABLE later (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if got := listed(); got != "extra|products|warehouse_items|warehouses" {
		t.Fatalf("inside the block: tables = %q, want the catalog the block began with", got)
	}
	mustRun(t, session, "COMMIT")
	if got := listed(); got != "extra|later|products|warehouse_items|warehouses" {
		t.Fatalf("after the block: tables = %q", got)
	}

	// The catalog is read-only like everything else, and a user's grants still decide what rows they read.
	expectCode(t, session, "DELETE FROM pg_class", "25006", "DELETE")
	expectCode(t, session, "INSERT INTO temp.pg_namespace (nspname) VALUES ('x')", "25006", "INSERT")
	expectCode(t, session, "SELECT * FROM warehouses", "42501", "permission denied for table public.warehouses")
}

// A database tool over the wire (lib/pq, simple and extended protocols): the table list and a column's type.
//
// CONTROL: with columnTypeOf ignoring Columns (kitwork_columns), "format_type = "bigint", relhasindex = true".
func TestCatalogOverTheWire(t *testing.T) {
	target, _ := newCatalogFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	server := pgwire.Server{Authenticator: catalogAuthenticator{target: target}}
	go func() { served <- server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	client, err := sql.Open("postgres", fmt.Sprintf(
		"host=127.0.0.1 port=%d user=tester password=secret dbname=shop.example/application sslmode=disable",
		listener.Addr().(*net.TCPAddr).Port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	client.SetMaxOpenConns(1)

	rows, err := client.Query(`SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' ORDER BY table_name`)
	if err != nil {
		t.Fatalf("table list: %v", err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(names) != "[cheap products warehouse_items warehouses]" {
		t.Fatalf("table list = %v", names)
	}
	var formatted string
	var indexed bool
	if err := client.QueryRow(`SELECT format_type(a.atttypid, a.atttypmod), c.relhasindex FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid WHERE c.relname = $1 AND a.attname = $2`,
		"warehouse_items", "amount").Scan(&formatted, &indexed); err != nil {
		t.Fatalf("column type: %v", err)
	}
	if formatted != "numeric(12,2)" || !indexed {
		t.Fatalf("format_type = %q, relhasindex = %v", formatted, indexed)
	}
}

type catalogAuthenticator struct{ target Target }

func (authenticator catalogAuthenticator) Authenticate(ctx context.Context, startup pgwire.Startup, password string) (pgwire.Session, error) {
	if password != "secret" {
		return nil, pgwire.NewError("28P01", "password authentication failed for user "+startup.User())
	}
	return Open(ctx, authenticator.target, newAccess("public.products"), catalogColumns{}, Options{User: startup.User()})
}

// A failed statement reaches Options.Log with its error.
//
// CONTROL: with Execute not deferring logFailure, "logged = []".
func TestFailedStatementsAreLogged(t *testing.T) {
	target, _ := newCatalogFixture(t)
	var logged []string
	session, err := Open(context.Background(), target, newAccess(), catalogColumns{}, Options{
		Log: func(statement string, err error) { logged = append(logged, statement+" → "+codeOf(err)) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	mustRun(t, session, "SELECT 1")
	expectCode(t, session, "SELECT missing FROM nowhere", "42P01", "nowhere")
	if fmt.Sprint(logged) != "[SELECT missing FROM nowhere → 42P01]" {
		t.Fatalf("logged = %v", logged)
	}
}

// The rest of PostgreSQL's syntax the translator reads, each against what PostgreSQL answers (booleans of a function
// come back as 1 and 0).
//
// CONTROL: with unnestCalls never matching, "SELECT value, ordinality FROM unnest(…: near "ORDINALITY": syntax
// error"; with patternOperators leaving ~ alone, "SELECT 'a' ~ '^A', …: near "~": syntax error".
func TestTranslatedSyntax(t *testing.T) {
	target, _ := newCatalogFixture(t)
	session := openCatalogSession(t, target)
	for _, check := range []struct{ query, want string }{
		{`SELECT 'x'::varchar(255), 1.5::double precision, '2026-01-01'::timestamp with time zone, 'y'::"char"`, "x,1.5,2026-01-01,y"},
		{`SELECT ~5, 7 & ~2`, "-6,5"},
		{`SELECT value, ordinality FROM unnest('{a,b}'::text[]) WITH ORDINALITY AS t(value, ordinality)`, "a,1|b,2"},
		{`SELECT ARRAY[1,2], cardinality(ARRAY[]::int[]), (ARRAY['a','b'])[2]`, "{1,2},0,b"},
		{`SELECT E'line\nnext', E'tab\there', E'\x41à'`, "line\nnext,tab\there,Aà"},
		{`SELECT 'a' ~ '^A', 'a' ~* '^A', 'abc' !~~ 'a%', 'abc' ~~ 'a%'`, "0,1,0,1"},
		{`SELECT position('b' IN 'abc'), strpos('abc', 'c')`, "2,3"},
		{`SELECT left('Hà Nội', 2), right('Hà Nội', 3), split_part('a,b,c', ',', 2), lpad('7', 3, '0'), initcap('hà nội')`, "Hà,Nội,b,007,Hà Nội"},
		{`SELECT now() IS NOT NULL, pg_backend_pid() > 0`, "1,1"},
		{`SELECT x FROM (SELECT 1 AS x UNION SELECT 3) AS s WHERE x = ANY('{1,2}') ORDER BY x`, "1"},
		{`SELECT array_agg(relname ORDER BY relname) FROM pg_class WHERE relnamespace = 2200 AND relkind = 'r'`, "{products,warehouse_items,warehouses}"},
		{`SELECT array_to_string('{a,NULL,c}'::text[], '-', '*'), string_to_array('a,b', ',')`, "a-*-c,{a,b}"},
		{`SELECT pg_size_pretty(10485760), quote_literal('it''s'), current_schema, current_schemas(false)`, "10 MB,'it''s',public,{public}"},
	} {
		if got := cells(mustRun(t, session, check.query)); got != check.want {
			t.Fatalf("%s = %q, want %q", check.query, got, check.want)
		}
	}
}
