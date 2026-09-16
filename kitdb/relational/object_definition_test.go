package relational

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/pgwire"
)

func createObjectFixture(t *testing.T, e *Engine) {
	t.Helper()
	for _, query := range []string{
		`CREATE DOMAIN amount AS NUMERIC(24,2) DEFAULT 0 NOT NULL CONSTRAINT amount_min CHECK (VALUE >= 0) CHECK (VALUE <= 9007199254740993.25)`,
		`CREATE DOMAIN label AS VARCHAR(20) DEFAULT 'O''Reilly' CHECK (VALUE <> '')`,
		`CREATE SEQUENCE invoice_ids AS BIGINT START 9007199254740993 INCREMENT 2 CACHE 1`,
		`CREATE TABLE products (id BIGINT PRIMARY KEY, price amount, sku TEXT UNIQUE, CONSTRAINT positive_id CHECK (id > 0))`,
		`CREATE TABLE audit (event_id SERIAL PRIMARY KEY, product_id BIGINT REFERENCES products(id) ON DELETE RESTRICT, old_price NUMERIC(24,2), new_price NUMERIC(24,2))`,
		`CREATE TRIGGER price_change AFTER UPDATE ON products FOR EACH ROW WHEN (OLD.price <> NEW.price) INSERT INTO audit (product_id,old_price,new_price) VALUES (NEW.id,OLD.price,NEW.price)`,
		`CREATE FUNCTION normalize_sku(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN upper(trim(s))`,
		`INSERT INTO products (id,price,sku) VALUES (1,10,'a')`,
		`UPDATE products SET price = 15 WHERE id = 1`,
	} {
		functionTestExecute(t, e, query)
	}
}

func objectCatalogQuery(t *testing.T, catalog postgresCatalogSnapshot, query string) pgwire.Result {
	t.Helper()
	result, handled, err := executePostgresCatalogQuery(query, catalog)
	if err != nil || !handled {
		t.Fatalf("%s: handled=%t err=%v", query, handled, err)
	}
	return result
}

func objectDefinitions(t *testing.T, e *Engine) map[string]string {
	t.Helper()
	catalog, err := e.postgresCatalogSnapshot("objects")
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]string)
	for _, query := range []string{
		`SELECT tgname AS name, pg_get_triggerdef(oid,true) AS definition FROM pg_catalog.pg_trigger`,
		`SELECT typname AS name, kitdb_get_domaindef(oid) AS definition FROM pg_catalog.pg_type WHERE typtype = 'd'`,
		`SELECT relname AS name, kitdb_get_sequencedef(seqrelid) AS definition FROM pg_sequence s JOIN pg_class c ON c.oid = s.seqrelid WHERE c.relname = 'invoice_ids'`,
		`SELECT proname AS name, pg_get_functiondef(oid) AS definition FROM pg_proc`,
	} {
		rows := objectCatalogQuery(t, catalog, query)
		for _, row := range rows.Rows {
			if len(row) != 2 || row[0].Null || row[1].Null || rows.Columns[1].DataTypeOID != pgwire.OIDText {
				t.Fatalf("invalid definition result: %#v", rows)
			}
			result[string(row[0].Data)] = string(row[1].Data)
		}
	}
	if len(result) != 5 {
		t.Fatalf("definitions: %#v", result)
	}
	return result
}

func TestObjectDefinitionRoundTrip(t *testing.T) {
	e := triggerTestOpen(t)
	createObjectFixture(t, e)
	definitions := objectDefinitions(t, e)
	clone := triggerTestOpen(t)
	for _, name := range []string{"amount", "label", "invoice_ids", "normalize_sku"} {
		functionTestExecute(t, clone, definitions[name])
	}
	for _, query := range []string{
		`CREATE TABLE products (id BIGINT PRIMARY KEY, price amount, sku TEXT UNIQUE, CONSTRAINT positive_id CHECK (id > 0))`,
		`CREATE TABLE audit (event_id SERIAL PRIMARY KEY, product_id BIGINT REFERENCES products(id) ON DELETE RESTRICT, old_price NUMERIC(24,2), new_price NUMERIC(24,2))`,
		definitions["price_change"],
		`INSERT INTO products (id,price,sku) VALUES (1,10,'a')`,
		`UPDATE products SET price = 15 WHERE id = 1`,
	} {
		functionTestExecute(t, clone, query)
	}
	if after := objectDefinitions(t, clone); !reflect.DeepEqual(definitions, after) {
		t.Fatalf("definition round trip changed: %#v != %#v", definitions, after)
	}
	for _, table := range []string{"products", "audit"} {
		want := functionTestExecute(t, e, "SELECT * FROM "+table).Rows
		got := functionTestExecute(t, clone, "SELECT * FROM "+table).Rows
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("%s behavior differs: %#v != %#v", table, want, got)
		}
	}
	triggerTestFail(t, clone, `INSERT INTO products (id,price,sku) VALUES (2,-1,'b')`, "check")
	sequenceQuery(t, clone, `SELECT nextval('invoice_ids')`, int64(9007199254740993))
	functionTestExecute(t, e, `ALTER TABLE products RENAME COLUMN price TO total`)
	functionTestExecute(t, e, `ALTER TABLE audit RENAME COLUMN new_price TO total_after`)
	renamed := objectDefinitions(t, e)["price_change"]
	if !strings.Contains(renamed, `NEW."total"`) || !strings.Contains(renamed, `"total_after"`) || strings.Contains(renamed, `NEW."price"`) {
		t.Fatalf("trigger lost field-tag rename: %s", renamed)
	}
	functionTestExecute(t, e, `DROP TRIGGER price_change ON products`)
	functionTestExecute(t, e, renamed)
	functionTestExecute(t, e, `UPDATE products SET total = 20 WHERE id = 1`)
	triggerTestCount(t, e, "audit", 2)
}

func TestObjectDefinitionCatalogConstraints(t *testing.T) {
	e := triggerTestOpen(t)
	createObjectFixture(t, e)
	catalog, err := e.postgresCatalogSnapshot("objects")
	if err != nil {
		t.Fatal(err)
	}
	catalog.user = "reader"
	sequences := objectCatalogQuery(t, catalog, `SELECT pg_get_userbyid(c.relowner) AS owner, c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind='S' AND c.relname='invoice_ids'`)
	if len(sequences.Rows) != 1 || string(sequences.Rows[0][0].Data) != "reader" || string(sequences.Rows[0][1].Data) != "invoice_ids" {
		t.Fatalf("sequence discovery: %#v", sequences)
	}
	domains := objectCatalogQuery(t, catalog, `SELECT domain_name, constraint_name, is_deferrable FROM information_schema.domain_constraints WHERE domain_name = 'amount'`)
	if len(domains.Rows) != 2 || string(domains.Rows[0][2].Data) != "NO" {
		t.Fatalf("domain constraints: %#v", domains)
	}
	checks := objectCatalogQuery(t, catalog, `SELECT d.domain_name, c.check_clause FROM information_schema.domain_constraints d JOIN information_schema.check_constraints c ON c.constraint_name=d.constraint_name AND c.constraint_schema=d.constraint_schema AND c.constraint_catalog=d.constraint_catalog WHERE d.domain_name='label'`)
	if len(checks.Rows) != 1 || string(checks.Rows[0][0].Data) != "label" || string(checks.Rows[0][1].Data) != `(VALUE <> '')` {
		t.Fatalf("domain check join: %#v", checks)
	}
	var amount *storedDomain
	for _, domain := range catalog.domains {
		if domain.Name == "amount" {
			amount = domain
		}
	}
	q := fmt.Sprintf(`SELECT c.conname, c.conrelid, c.contypid, pg_get_constraintdef(c.oid,true) AS definition FROM pg_constraint c WHERE c.contypid=%d`, postgresCatalogOID("domain", amount.ID))
	constraints := objectCatalogQuery(t, catalog, q)
	if len(constraints.Rows) != 2 || string(constraints.Rows[0][1].Data) != "0" {
		t.Fatalf("domain constraints confused with table constraints: %#v", constraints)
	}
	if !strings.Contains(string(constraints.Rows[1][3].Data), "9007199254740993.25") {
		t.Fatalf("decimal precision lost: %#v", constraints)
	}
	for _, relation := range catalog.relations {
		for _, constraint := range catalogConstraintsFor(catalog, relation) {
			q := fmt.Sprintf(`SELECT pg_get_constraintdef(%d)`, constraint.constraint)
			result := objectCatalogQuery(t, catalog, q)
			value := string(result.Rows[0][0].Data)
			if result.Rows[0][0].Null || value == "" || strings.Contains(value, "<") && constraint.kind != "c" {
				t.Fatalf("invalid constraint: %s", value)
			}
			if constraint.kind == "f" && (!strings.Contains(value, `REFERENCES "products" ("id")`) || !strings.Contains(value, "ON DELETE RESTRICT")) {
				t.Fatalf("foreign key reconstruction: %s", value)
			}
		}
	}
	for _, helper := range []string{"pg_get_triggerdef", "pg_get_constraintdef", "kitdb_get_domaindef", "kitdb_get_sequencedef"} {
		for _, value := range []string{"0", "NULL"} {
			result := objectCatalogQuery(t, catalog, "SELECT "+helper+"("+value+")")
			if len(result.Rows) != 1 || !result.Rows[0][0].Null {
				t.Fatalf("unknown %s: %#v", helper, result)
			}
		}
		for _, value := range []string{"'invalid'", "4294967296", "1+1", "missing"} {
			if _, _, err := executePostgresCatalogQuery("SELECT "+helper+"("+value+")", catalog); err == nil {
				t.Fatalf("invalid argument accepted: %s %s", helper, value)
			}
		}
	}
	for _, seq := range catalog.sequences {
		if seq.OwnerStruct != "" {
			if _, _, err := executePostgresCatalogQuery(fmt.Sprintf(`SELECT kitdb_get_sequencedef(%d)`, postgresCatalogOID("sequence", seq.ID)), catalog); err == nil {
				t.Fatal("owned sequence silently exported without its dependency")
			}
		}
	}
	oid := postgresCatalogOID("trigger", catalog.triggers[0].ID)
	catalog.triggers = append(catalog.triggers, catalog.triggers[0])
	if _, _, err := executePostgresCatalogQuery(fmt.Sprintf(`SELECT pg_get_triggerdef(%d)`, oid), catalog); err == nil {
		t.Fatal("ambiguous OID accepted")
	}
}

func TestObjectDefinitionCrashRestore(t *testing.T) {
	if path := os.Getenv("KITDB_OBJECT_CRASH_CHILD"); path != "" {
		e, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		createObjectFixture(t, e)
		sequenceQuery(t, e, `SELECT nextval('invoice_ids')`, int64(9007199254740993))
		tx, err := e.BeginTransaction(context.Background(), TransactionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Execute(context.Background(), `UPDATE products SET price=99 WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		os.Exit(34)
	}
	path := filepath.Join(t.TempDir(), "objects.kitdb")
	child := exec.Command(os.Args[0], "-test.run=^TestObjectDefinitionCrashRestore$")
	child.Env = append(os.Environ(), "KITDB_OBJECT_CRASH_CHILD="+path)
	output, err := child.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 34 {
		t.Fatalf("child: %v %s", err, output)
	}
	e, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	definitions := objectDefinitions(t, e)
	triggerTestCount(t, e, "audit", 1)
	if got := fmt.Sprint(functionTestExecute(t, e, `SELECT price FROM products WHERE id=1`).Rows[0][0]); got != "15" {
		t.Fatalf("uncommitted update recovered: %s", got)
	}
	if _, err := e.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if after := objectDefinitions(t, e); !reflect.DeepEqual(definitions, after) {
		t.Fatal("checkpoint/reopen changed definitions")
	}
	anchorPath := filepath.Join(t.TempDir(), "anchor.kitdb")
	anchor, err := e.database.CreateBackupAnchor(context.Background(), anchorPath)
	if err != nil {
		t.Fatal(err)
	}
	restoredPath := filepath.Join(t.TempDir(), "restored.kitdb")
	if _, err := kitdbengine.RestoreToTransaction(context.Background(), anchorPath, "", restoredPath, anchor.Transaction); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if after := objectDefinitions(t, restored); !reflect.DeepEqual(definitions, after) {
		t.Fatal("restore changed definitions")
	}
	triggerTestCount(t, restored, "audit", 1)
	functionTestExecute(t, restored, `UPDATE products SET price=20 WHERE id=1`)
	triggerTestCount(t, restored, "audit", 2)
	triggerTestCount(t, e, "audit", 1)
	sequenceQuery(t, restored, `SELECT nextval('invoice_ids')`, int64(9007199254740995))
	sequenceQuery(t, e, `SELECT nextval('invoice_ids')`, int64(9007199254740995))
	triggerTestFail(t, restored, `DROP DOMAIN amount`, "depend")
	triggerTestFail(t, restored, `DROP TABLE audit`, "trigger")
	functionTestExecute(t, restored, `DROP TRIGGER price_change ON products`)
	functionTestExecute(t, restored, `DROP TABLE audit`)
	functionTestExecute(t, restored, `DROP TABLE products`)
	functionTestExecute(t, restored, `DROP DOMAIN amount`)
	functionTestExecute(t, restored, `DROP SEQUENCE invoice_ids`)
	catalog, err := restored.postgresCatalogSnapshot("objects")
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`SELECT tgname FROM pg_trigger`, `SELECT sequence_name FROM information_schema.sequences`, `SELECT domain_name FROM information_schema.domains WHERE domain_name='amount'`} {
		if rows := objectCatalogQuery(t, catalog, query).Rows; len(rows) != 0 {
			t.Fatalf("dropped object still visible: %s %#v", query, rows)
		}
	}
}

func TestObjectDefinitionPostgresWire(t *testing.T) {
	e := triggerTestOpen(t)
	createObjectFixture(t, e)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	done := make(chan error, 1)
	go func() {
		done <- e.ServePostgres(ctx, listener, PostgresServerOptions{PostgresOptions: PostgresOptions{Database: "objects", User: "reader", Password: "test-only", ReadOnly: true}})
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
	db, err := sql.Open("postgres", fmt.Sprintf("postgres://reader:test-only@%s/objects?sslmode=disable", listener.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var oid int64
	if err := db.QueryRowContext(ctx, `SELECT t.oid FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON t.tgrelid=c.oid JOIN pg_catalog.pg_namespace n ON c.relnamespace=n.oid WHERE t.tgname=$1 AND n.nspname='public'`, "price_change").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	var definition string
	if err := db.QueryRowContext(ctx, `SELECT pg_get_triggerdef($1::oid,$2) AS definition`, oid, true).Scan(&definition); err != nil || !strings.HasPrefix(definition, `CREATE TRIGGER "price_change"`) {
		t.Fatalf("definition: %s %v", definition, err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER price_change ON products`); err == nil {
		t.Fatal("read-only DDL allowed")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_trigger WHERE oid=$1`, nil).Scan(&count); err != nil || count != 0 {
		t.Fatalf("NULL filter: %d %v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_trigger WHERE tgname=$1`, `price_change' OR '1'='1`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("literal injection: %d %v", count, err)
	}
	var domain string
	if err := db.QueryRowContext(ctx, `SELECT kitdb_get_domaindef(oid) FROM pg_type WHERE typname=$1`, "amount").Scan(&domain); err != nil || !strings.Contains(domain, "9007199254740993.25") {
		t.Fatalf("domain: %s %v", domain, err)
	}
	var owner, name string
	var start, cache int64
	if err := db.QueryRowContext(ctx, `SELECT pg_get_userbyid(c.relowner),c.relname,s.seqstart,s.seqcache FROM pg_sequence s JOIN pg_class c ON c.oid=s.seqrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relname=$1`, "invoice_ids").Scan(&owner, &name, &start, &cache); err != nil || owner != "reader" || name != "invoice_ids" || start != 9007199254740993 || cache != 1 {
		t.Fatalf("sequence: %s %s %d %d %v", owner, name, start, cache, err)
	}
	var body string
	if err := db.QueryRowContext(ctx, `SELECT pg_get_constraintdef(oid,$1) FROM pg_constraint WHERE conname=$2 AND conrelid=0`, true, "amount_min").Scan(&body); err != nil || !strings.Contains(body, `VALUE >= 0`) {
		t.Fatalf("domain check: %s %v", body, err)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Current DDL admission waits for the transaction's schema read gate.
	// Read the pinned catalog while DROP is queued, then release the gate.
	dropDone := make(chan error, 1)
	go func() {
		_, err := e.Execute(ctx, `DROP TRIGGER price_change ON products`)
		dropDone <- err
	}()
	dropFinished := false
	defer func() {
		_ = tx.Rollback()
		if !dropFinished {
			select {
			case <-dropDone:
			case <-time.After(5 * time.Second):
				t.Error("queued DDL did not finish")
			}
		}
	}()
	var snapshotDefinition string
	if err := tx.QueryRowContext(ctx, `SELECT pg_get_triggerdef($1)`, oid).Scan(&snapshotDefinition); err != nil || snapshotDefinition != definition {
		t.Fatalf("catalog snapshot changed during transaction: %s %v", snapshotDefinition, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-dropDone:
		dropFinished = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DROP did not resume after transaction rollback")
	}
	var missing sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT pg_get_triggerdef($1)`, oid).Scan(&missing); err != nil || missing.Valid {
		t.Fatalf("dropped trigger leaked into new snapshot: %#v %v", missing, err)
	}
	other := triggerTestOpen(t)
	otherCatalog, err := other.postgresCatalogSnapshot("other")
	if err != nil {
		t.Fatal(err)
	}
	if result := objectCatalogQuery(t, otherCatalog, fmt.Sprintf(`SELECT pg_get_triggerdef(%d)`, oid)); !result.Rows[0][0].Null {
		t.Fatal("trigger crossed database boundary")
	}
}
