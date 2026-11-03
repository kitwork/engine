package compiler

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/database/statement"
)

// writeSchema has a column unique alone (email), a group unique together (linkId, day) and a table with neither.
const writeSchema = `import { table, id, text, integer, decimal, date } from "kitwork/schema";
import { connection } from "kitwork/database";

const orders = table({ id: id(), email: text().unique(), item: text(), amount: decimal(12, 2), stock: integer(), note: text().nullable() });
const clickDays = table({ id: id(), linkId: text().unique("linkDay"), day: date().unique("linkDay"), count: integer() });
const notes = table({ id: id(), body: text() });

export const db = connection().tables({ orders, clickDays, notes });
`

const writeHead = importDB + "const email = \"a@x.vn\";\nconst linkId = \"l1\";\nconst today = \"2026-11-03\";\n"

func compileWrite(t *testing.T, line string) (*statement.Statement, error) {
	t.Helper()
	bytecode, err := compileRoute(t, writeHead+line+"\n", map[string]string{".shared/schema/shop.work": writeSchema})
	if err != nil {
		return nil, err
	}
	found := templates(t, bytecode)
	if len(found) != 1 {
		t.Fatalf("%s: %d templates", line, len(found))
	}
	return found[0], nil
}

// update, delete and upsert compile to one template each (KITWORK_DATABASE.md mục 2, lát 3): update's data may read a
// column of the row it changes, alone or with + or - and a number; delete takes its condition; upsert's target is the
// .unique() column or the .unique("tên") group its data writes, and its data may read a number column (null on the row
// it inserts, read as 0).
func TestDatabaseWritesTranslate(t *testing.T) {
	update, err := compileWrite(t, `db.where((orders) => orders.email === email).update((orders) => ({ amount: orders.amount + 1000, note: null }));`)
	if err != nil {
		t.Fatal(err)
	}
	if got := statement.Encode(update); !strings.Contains(got, `"action":"update"`) ||
		!strings.Contains(got, `"values":[{"column":"amount","value":{"op":"+","args":[{"op":"column","column":"amount"},{"op":"literal","value":1000}]}},{"column":"note","value":{"op":"null"}}]`) ||
		!strings.Contains(got, `"where":{"op":"===","args":[{"op":"column","column":"email"},{"op":"hole","hole":0}]}`) {
		t.Fatalf("update = %s", got)
	}
	remove, err := compileWrite(t, `db.delete((orders) => orders.stock < 1);`)
	if err != nil {
		t.Fatal(err)
	}
	if got := statement.Encode(remove); !strings.Contains(got, `"action":"delete"`) || !strings.Contains(got, `"where":{"op":"<"`) {
		t.Fatalf("delete = %s", got)
	}
	click, err := compileWrite(t, `db.upsert((clickDays) => ({ linkId, day: today, count: clickDays.count + 1 }));`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(click.Target, ",") != "linkId,day" || click.Values[2].Value.Op != statement.OpAdd {
		t.Fatalf("upsert of a group = %s", statement.Encode(click))
	}
	order, err := compileWrite(t, `db.upsert((orders) => ({ email, item: "Cà phê", amount: 35000, stock: orders.stock - 1 }));`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(order.Target, ",") != "email" {
		t.Fatalf("upsert of a unique column = %s", statement.Encode(order))
	}
}

// Each mistake of update, delete and upsert is a compile error with its reason.
//
// CONTROL (03/11/2026): with an update allowed without where, its template is what fails, "câu database hỏng: update
// không có điều kiện"; with the data's columns not checked (CheckData), the update of item from orders.amount and the
// upsert reading orders.item compile, "got <nil>".
func TestDatabaseWriteErrors(t *testing.T) {
	for _, test := range []struct{ line, want string }{
		{`db.update((orders) => ({ note: "x" }));`, "update cần điều kiện: db.where((orders) => …).update((orders) => ({ … }))"},
		{`db.where((orders) => orders.email === email).orderBy((orders) => orders.stock).delete();`, "orderBy không đi với delete"},
		{`db.where((orders) => orders.email === email).update((orders) => ({ amount: orders.amount * 2 }));`, "phép * trên cột chưa có trong engine hôm nay"},
		{`db.where((orders) => orders.email === email).update((orders) => ({ item: orders.amount }));`, "cột item là text(), orders.amount là decimal(12, 2)"},
		{`db.where((orders) => orders.email === email).update((orders) => ({ item: orders.item + "x" }));`, "+ và - trong dữ liệu là phép tính số"},
		{`db.where((orders) => orders.email === email).update((orders) => ({ id: "x" }));`, "id là id(), engine sinh lúc insert"},
		{`db.upsert((notes) => ({ body: "x" }));`, "upsert vào notes cần một đích so trùng: bảng không có cột .unique()"},
		{`db.upsert((orders) => ({ item: "x", amount: 1, stock: 1, email: orders.item }));`, "upsert đọc orders.item, là null khi chưa có dòng"},
		{`db.upsert((clickDays) => ({ linkId, count: 1, day: today, id: "x" }));`, "id là id()"},
		{`db.where((orders) => orders.stock > 0).upsert((orders) => ({ email, item: "x", amount: 1, stock: 1 }));`, "upsert không đi sau where hay orderBy"},
		{`db.upsert((orders) => ({ email, amount: 1, stock: 1 }));`, "insert vào orders thiếu cột bắt buộc: item"},
	} {
		_, err := compileWrite(t, test.line)
		if err == nil || !strings.Contains(err.Error(), test.want) || !strings.HasPrefix(err.Error(), "router.work:5:") {
			t.Errorf("%s\n  got  %v\n  want router.work:5:…: %s", test.line, err, test.want)
		}
	}
}
