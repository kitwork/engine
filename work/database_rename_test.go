package work

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// renameSchema is the shop's schema of one table with the columns given.
func renameSchema(table, columns string) string {
	return `import { table, id, string, integer, decimal } from "kitwork/schema";
import { connection } from "kitwork/database";

const ` + table + ` = table({ ` + columns + ` });

export const db = connection().tables({ ` + table + ` });
`
}

// renameApp is an app whose database kitwork migrate made from first, two rows written in it by SQL.
func renameApp(t *testing.T, table, columns string, rows ...string) (*managedApp, string) {
	t.Helper()
	managed := newManagedAppAsIs(t, map[string]string{".shared/schema/shop.work": renameSchema(table, columns)})
	migrateAppForTest(t, managed.root, "acme", RootLayoutMultiTenant)
	path := filepath.Join(managed.root, "acme", ".database", "application", "kitwork.db")
	file, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, row := range rows {
		if _, err := file.Exec(row); err != nil {
			t.Fatalf("%s: %v", row, err)
		}
	}
	return managed, path
}

// planOf plans kitwork migrate on the app with its schema now columns.
func planOf(t *testing.T, managed *managedApp, table, columns string) (*DatabaseMigration, func()) {
	t.Helper()
	writeAppTree(t, managed.root, map[string]string{"acme/.shared/schema/shop.work": renameSchema(table, columns)})
	plans, release, err := PlanAppMigrations(managed.root, "acme", RootLayoutMultiTenant, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		release()
		t.Fatalf("plans %+v", plans)
	}
	return plans[0], release
}

// fileText is what the file answers to query, the rows joined by "|" and the columns by ",".
func fileText(t *testing.T, path, query string) string {
	t.Helper()
	file, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows, err := file.Query(query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	columns, _ := rows.Columns()
	var out []string
	for rows.Next() {
		values := make([]sql.NullString, len(columns))
		targets := make([]any, len(columns))
		for index := range values {
			targets[index] = &values[index]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		texts := make([]string, len(values))
		for index, value := range values {
			texts[index] = value.String
		}
		out = append(out, strings.Join(texts, ","))
	}
	return strings.Join(out, "|")
}

// A column the declaration renamed: the file lacks the new name and has the old one, of the same type, which no
// table() declares; kitwork migrate cannot tell a rename from a new column, so it asks (KITWORK_DATABASE.md mục 6):
// renamed, the values stay under the new name (ALTER TABLE … RENAME COLUMN); added, the old column stays the
// database's own; added and the old dropped, its values go.
//
// CONTROL (05/11/2026): with renameCandidates finding none, "the question of a renamed column: <nil>".
func TestMigrateAsksAboutARenamedColumn(t *testing.T) {
	const v1 = `id: id(), email: string(), amount: decimal(12, 2)`
	const v2 = `id: id(), email: string(), total: decimal(12, 2)`
	insert := []string{`INSERT INTO orders (id, email, amount) VALUES ('a', 'an@example.com', 1250), ('b', 'binh@example.com', 300)`}
	for _, test := range []struct {
		option     int
		statements string
		columns    string
		kinds      string
	}{
		{0, "ALTER TABLE orders RENAME COLUMN amount TO total;", "id,email,total", "total,id,email"},
		{1, "ALTER TABLE orders ADD COLUMN total numeric(12, 2);", "id,email,amount,total", "amount,total,id,email"},
		{2, "ALTER TABLE orders DROP COLUMN amount; ALTER TABLE orders ADD COLUMN total numeric(12, 2);", "id,email,total", "total,id,email"},
	} {
		managed, path := renameApp(t, "orders", v1, insert...)
		plan, release := planOf(t, managed, "orders", v2)
		question := plan.Question()
		if question == nil || question.Text != ".database/application/kitwork.db: bảng orders, cột total decimal(12, 2) mới; database có cột amount cùng kiểu mà code không khai" ||
			strings.Join(question.Options, " | ") != "đổi tên amount → total (giữ 2 giá trị) | thêm cột total mới, giữ amount (cột riêng của database) | thêm cột total mới, bỏ amount (mất 2 giá trị)" {
			release()
			t.Fatalf("the question of a renamed column: %+v", question)
		}
		if err := question.Answer(test.option); err != nil {
			t.Fatal(err)
		}
		if next := plan.Question(); next != nil {
			t.Fatalf("a second question: %+v", next)
		}
		if got := strings.Join(plan.Statements, " "); got != test.statements {
			t.Fatalf("option %d: statements %q, want %q", test.option, got, test.statements)
		}
		if err := plan.Apply(context.Background()); err != nil {
			t.Fatalf("option %d: %v", test.option, err)
		}
		release()
		if got := fileText(t, path, `SELECT group_concat(name) FROM (SELECT name FROM pragma_table_info('orders') ORDER BY cid)`); got != test.columns {
			t.Fatalf("option %d: orders has %s, want %s", test.option, got, test.columns)
		}
		if got := fileText(t, path, `SELECT group_concat(column_name) FROM (SELECT column_name FROM kitwork_columns WHERE table_name = 'orders' ORDER BY kind, column_name)`); got != test.kinds {
			t.Fatalf("option %d: kinds of %s, want %s", test.option, got, test.kinds)
		}
		if test.option == 0 {
			if got := fileText(t, path, `SELECT total FROM orders ORDER BY id`); got != "1250|300" {
				t.Fatalf("the values renamed: %q", got)
			}
		}
	}
}

// A table the declaration renamed, its columns all in a table no table() declares: kitwork migrate asks, and
// renamed, the rows and their kinds go along (ALTER TABLE … RENAME TO).
//
// CONTROL (05/11/2026): with holdsColumns refusing every table, "the question of a renamed table: <nil>".
func TestMigrateAsksAboutARenamedTable(t *testing.T) {
	managed, path := renameApp(t, "customers", `id: id(), name: string(), visits: integer()`,
		`INSERT INTO customers (id, name, visits) VALUES ('a', 'An', 3)`)
	plan, release := planOf(t, managed, "clients", `id: id(), name: string(), visits: integer()`)
	defer release()
	question := plan.Question()
	if question == nil || question.Text != ".database/application/kitwork.db: bảng clients mới; database có bảng customers mà code không khai, có đủ các cột của nó" ||
		strings.Join(question.Options, " | ") != "đổi tên bảng customers → clients (giữ 1 dòng) | tạo bảng clients mới (customers ở lại, là bảng riêng của database)" {
		t.Fatalf("the question of a renamed table: %+v", question)
	}
	if err := question.Answer(0); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(plan.Statements, " "); got != "ALTER TABLE customers RENAME TO clients;" {
		t.Fatalf("statements %q", got)
	}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fileText(t, path, `SELECT name || ':' || visits FROM clients`); got != "An:3" {
		t.Fatalf("clients holds %q", got)
	}
	if got := fileText(t, path, `SELECT count(*) FROM kitwork_columns WHERE table_name = 'customers'`); got != "0" {
		t.Fatalf("kinds left under customers: %s", got)
	}
}

// A number column whose scale the declaration changed (decimal(12, 3) to (12, 5) and down to (12, 2), decimal to
// integer): kitwork migrate asks to rewrite its values, multiplied exactly or divided rounding half away from zero, as
// PostgreSQL's numeric rounds (12.345 is 12.35, -12.345 is -12.35); a value the new scale would carry past the declared
// precision refuses it all. Refused, the column stays a difference the code is to follow.
//
// CONTROL (06/11/2026): with the rewrite's rounding a plain integer division, "decimal(12, 2): -1234,1234, want
// -12.35,12.35"; with no guard, "decimal(4, 3) took 12.35: <nil>".
func TestMigrateRewritesANumbersScale(t *testing.T) {
	managed, path := renameApp(t, "prices", `id: id(), amount: decimal(12, 3)`,
		`INSERT INTO prices (id, amount) VALUES ('a', 12345), ('b', -12345)`)
	rescale := func(columns string, answer int) *DatabaseMigration {
		t.Helper()
		plan, release := planOf(t, managed, "prices", columns)
		defer release()
		question := plan.Question()
		if question == nil || len(question.Options) != 2 {
			t.Fatalf("the question of a new scale: %+v", question)
		}
		if err := question.Answer(answer); err != nil {
			t.Fatal(err)
		}
		if answer == 0 {
			if err := plan.Apply(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		return plan
	}
	plan := rescale(`id: id(), amount: decimal(12, 5)`, 0)
	if got := strings.Join(plan.Statements, " "); got != "ALTER TABLE prices ALTER COLUMN amount TYPE numeric(12, 5);" {
		t.Fatalf("statements %q", got)
	}
	if got := fileText(t, path, `SELECT group_concat(amount) FROM (SELECT amount FROM prices ORDER BY amount)`); got != "-1234500,1234500" {
		t.Fatalf("decimal(12, 5): %s", got)
	}
	rescale(`id: id(), amount: decimal(12, 2)`, 0)
	if got := fileText(t, path, `SELECT group_concat(amount) FROM (SELECT amount FROM prices ORDER BY amount)`); got != "-1235,1235" {
		t.Fatalf("decimal(12, 2): %s, want -12.35,12.35", got)
	}
	if got := fileText(t, path, `SELECT kind || '(' || precision || ',' || scale || ')' FROM kitwork_columns WHERE table_name = 'prices' AND column_name = 'amount'`); got != "decimal(12,2)" {
		t.Fatalf("the kind after: %s", got)
	}
	// refused: the difference stays
	plan = rescale(`id: id(), amount: integer()`, 1)
	if len(plan.Differ) != 1 || plan.Pending() {
		t.Fatalf("a refused rewrite: differ %q, statements %q", plan.Differ, plan.Statements)
	}
	// past the precision: nothing rewritten
	plan, release := planOf(t, managed, "prices", `id: id(), amount: decimal(4, 3)`)
	defer release()
	if err := plan.Question().Answer(0); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "2 giá trị vượt số chữ số") {
		t.Fatalf("decimal(4, 3) took 12.35: %v", err)
	}
	if got := fileText(t, path, `SELECT group_concat(amount) FROM (SELECT amount FROM prices ORDER BY amount)`); got != "-1235,1235" {
		t.Fatalf("after the refusal: %s", got)
	}
}
