package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kitwork/engine/kitdb/pgwire"
)

// A table's CHECKs as PostgreSQL names them (check.go): its own name when written, else <table>_<column>_check for an
// expression of one column, wherever it is written, <table>_check for one of more or none, a number after check for a
// name the table has, in the order written; what SQLite reports of each, its name or its expression's text.
//
// CONTROL (08/11/2026, again 09/11 with the columns read): with every unnamed CHECK <table>_check, "#0 =
// {orders_check qty >= 1 qty}, want {orders_qty_check qty >= 1 qty}"; with no number after a name taken, "#3 =
// {orders_High_check high < 1000 High}, want {orders_High_check1 high < 1000 High}".
func TestCheckConstraintNames(t *testing.T) {
	create := `CREATE TABLE "orders" ("qty" INTEGER CHECK (qty >= 1), low INTEGER CHECK ( low < "High" ), "High" INTEGER ` +
		`CHECK (High > 0) CHECK (high < 1000), CONSTRAINT "Positive" CHECK (qty > 0), name TEXT, CHECK (length (name) >= 1), ` +
		`CHECK (low < high), CHECK (1 = 1), CHECK (orders.qty < 99)) STRICT`
	type named struct{ name, reported, columns string }
	want := []named{
		{"orders_qty_check", "qty >= 1", "qty"},
		{"orders_check", `low < "High"`, "low,High"},
		{"orders_High_check", "High > 0", "High"},
		{"orders_High_check1", "high < 1000", "High"},
		{"Positive", "Positive", "qty"},
		{"orders_name_check", "length (name) >= 1", "name"},
		{"orders_check1", "low < high", "low,High"},
		{"orders_check2", "1 = 1", ""},
		{"orders_qty_check1", "orders.qty < 99", "qty"},
	}
	checks := checkConstraints(create)
	if len(checks) != len(want) {
		t.Fatalf("%d CHECKs: %v, want %d", len(checks), checks, len(want))
	}
	for index := range want {
		check := checks[index]
		if got := (named{check.name, check.reported, strings.Join(check.columns, ",")}); got != want[index] {
			t.Errorf("#%d = %v, want %v", index, got, want[index])
		}
	}
	if checks := checkConstraints("CREATE TABLE broken ("); checks != nil {
		t.Errorf("a CREATE TABLE that does not read: %v", checks)
	}
}

// A name PostgreSQL makes is at most 63 bytes: the longer of table and column is cut a byte at a time, never inside
// a character.
func TestObjectName(t *testing.T) {
	if got := objectName(strings.Repeat("a", 50), strings.Repeat("b", 10), "check"); got != strings.Repeat("a", 46)+"_bbbbbbbbbb_check" ||
		len(got) != 63 {
		t.Errorf("50 and 10 bytes: %q (%d bytes)", got, len(got))
	}
	// equal lengths: the second is cut first, as makeObjectName does
	if got := objectName(strings.Repeat("a", 40), strings.Repeat("b", 40), "check1"); got != strings.Repeat("a", 28)+"_"+strings.Repeat("b", 27)+"_check1" {
		t.Errorf("40 and 40 bytes: %q (%d bytes)", got, len(got))
	}
	if got := objectName(strings.Repeat("a", 40), strings.Repeat("b", 10), "check"); len(got) != 57 {
		t.Errorf("40 and 10 bytes fit: %q (%d bytes)", got, len(got))
	}
	if got := objectName(strings.Repeat("đ", 40), "", "check"); got != strings.Repeat("đ", 28)+"_check" {
		t.Errorf("80 bytes of đ: %q (%d bytes)", got, len(got))
	}
	if got := objectName("notes", "text", "check"); got != "notes_text_check" {
		t.Errorf("short: %q", got)
	}
}

// A write that breaks a CHECK is told as PostgreSQL tells it, by itself and inside a block: 23514, new row for relation
// "notes" violates check constraint "notes_text_check", the schema, the table and the constraint in their fields. A
// UNIQUE's message says "constraint failed" once. A CHECK that reads a decimal column other than its own is refused
// when the table is made: the file compares the stored integer.
//
// CONTROL (08/11/2026): with checkViolation not called, "&pgwire.Error{Code:\"23514\", Message:\"CHECK constraint
// failed: length (text) >= 1\", Schema:\"\", …}"; with the prefix kept, "the UNIQUE's message: constraint failed: UNIQUE
// constraint failed: notes.code"; with checkReadsDecimal not called, "CREATE TABLE t (p numeric(15, 2), CHECK (p <=
// 100)) was taken: <nil>"; with the structure file reading only a CHECK not named, "the structure file wrote a named
// CHECK of a decimal as it is".
func TestBrokenCheckThroughThePort(t *testing.T) {
	fixture := newWriteFixture(t)
	access := newAccess()
	access.admin = true
	session := fixture.session(t, access, Options{})
	run(t, session, `CREATE TABLE notes (n bigint, code text UNIQUE, text text CHECK (length(text) >= 1), `+
		`price numeric(12, 2) CHECK (price >= 0), CONSTRAINT sensible CHECK (n < 1000))`)
	run(t, session, `INSERT INTO notes (n, code, text, price) VALUES (1, 'a', 'x', 1)`)
	broken := func(query, constraint string) {
		t.Helper()
		_, err := session.Execute(context.Background(), query, nil)
		var protocol *pgwire.Error
		want := `new row for relation "notes" violates check constraint "` + constraint + `"`
		if !errors.As(err, &protocol) || protocol.Code != "23514" || protocol.Message != want || protocol.Schema != "public" ||
			protocol.Table != "notes" || protocol.Constraint != constraint {
			t.Fatalf("%s: %#v, want 23514 %s", query, err, want)
		}
	}
	broken(`INSERT INTO notes (n, text, price) VALUES (2, '', 1)`, "notes_text_check")
	broken(`INSERT INTO notes (n, text, price) VALUES (3, 'y', -0.01)`, "notes_price_check")
	broken(`UPDATE notes SET n = 5000 WHERE n = 1`, "sensible")
	run(t, session, "BEGIN")
	broken(`INSERT INTO public.notes (n, text) VALUES (4, '')`, "notes_text_check")
	run(t, session, "ROLLBACK")
	refused(t, session, `INSERT INTO notes (n, code, text) VALUES (6, 'a', 'z')`, "23505", "UNIQUE constraint failed: notes.code")
	if _, err := session.Execute(context.Background(), `INSERT INTO notes (n, code, text) VALUES (6, 'a', 'z')`, nil); strings.Contains(err.Error(), "constraint failed: UNIQUE") {
		t.Fatalf("the UNIQUE's message: %v, want constraint failed said once", err)
	}

	for _, text := range []string{
		`CREATE TABLE t (p numeric(15, 2), CHECK (p <= 100))`,
		`CREATE TABLE t (p numeric(15, 2), CONSTRAINT small CHECK (p <= 100))`,
		`CREATE TABLE t (p numeric(15, 2), q bigint CHECK (q < p))`,
	} {
		if _, err := TranslateSchema(text); err == nil || !strings.Contains(err.Error(), "a CHECK reads decimal column p") {
			t.Errorf("%s was taken: %v", text, err)
		}
	}
	refused(t, session, `ALTER TABLE notes ADD COLUMN cap bigint CHECK (cap > price)`, "0A000", "a CHECK reads decimal column price")
	for _, text := range []string{
		`CREATE TABLE t (p numeric(15, 2) CHECK (p <= 100), q bigint CHECK (q > 0), CHECK (q < 10), FOREIGN KEY (p) REFERENCES u (p))`,
		`CREATE TABLE t (p numeric(15, 0), CHECK (p <= 100))`,
	} {
		if _, err := TranslateSchema(text); err != nil {
			t.Errorf("%s: %v", text, err)
		}
	}
	if table, why := renderTable(`CREATE TABLE "t" ("p" INTEGER, CONSTRAINT "small" CHECK (p <= 10000)) STRICT`,
		map[string]columnKind{"p": {kind: "decimal", precision: 15, scale: 2}}); why == "" {
		t.Errorf("the structure file wrote a named CHECK of a decimal as it is: %s", table)
	}
}

// The catalog lists a table's CHECKs as PostgreSQL does (pg_constraint contype 'c', information_schema's
// check_constraints and table_constraints), by the names a broken one is told with, so a tool such as TablePlus shows
// them; a decimal column's own CHECK is written with the values its stored integers stand for.
//
// CONTROL (09/11/2026): with readChecks not called, "pg_constraint's CHECKs of notes = \"\""; with the decimal's
// numbers left as stored, "notes_price_check,c,CHECK (price >= 0 AND price <= 100000)|…"; with a CHECK listed among
// the keys' columns, "key_column_usage of notes = \"3\", want none".
func TestCatalogListsTheChecks(t *testing.T) {
	fixture := newWriteFixture(t)
	access := newAccess()
	access.admin = true
	session := fixture.session(t, access, Options{})
	run(t, session, `CREATE TABLE notes (n bigint, text text CHECK (length(text) >= 1), `+
		`price numeric(12, 2) CHECK (price >= 0 AND price <= 1000), CONSTRAINT sensible CHECK (n < 1000))`)
	query := func(statement string) string {
		t.Helper()
		return cells(mustRun(t, session, statement))
	}
	if got := query(`SELECT conname, contype, pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = 'notes'::regclass AND contype = 'c' ORDER BY conname`); got !=
		"notes_price_check,c,CHECK (price >= 0 AND price <= 1000)|notes_text_check,c,CHECK (length (text) >= 1)|sensible,c,CHECK (n < 1000)" {
		t.Fatalf("pg_constraint's CHECKs of notes = %q", got)
	}
	if got := query(`SELECT conkey FROM pg_constraint WHERE conname = 'sensible'`); got != "{1}" {
		t.Errorf("conkey of sensible = %q, want {1}", got)
	}
	if got := query(`SELECT check_clause FROM information_schema.check_constraints WHERE constraint_name = 'notes_price_check'`); got != "(price >= 0 AND price <= 1000)" {
		t.Errorf("information_schema.check_constraints = %q", got)
	}
	if got := query(`SELECT count(*) FROM information_schema.table_constraints WHERE table_name = 'notes' AND constraint_type = 'CHECK'`); got != "3" {
		t.Errorf("CHECKs in information_schema.table_constraints = %q, want 3", got)
	}
	if got := query(`SELECT count(*) FROM information_schema.key_column_usage WHERE table_name = 'notes'`); got != "0" {
		t.Errorf("key_column_usage of notes = %q, want none: a CHECK is no key", got)
	}
}
