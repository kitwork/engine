package compiler

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/database/statement"
)

// writeSchema has a column unique alone (email), a group unique together (linkId, day) and a table with neither.
const writeSchema = `import { table, id, string, integer, decimal, date } from "kitwork/schema";
import { connection } from "kitwork/database";

const orders = table({ id: id(), email: string().unique(), item: string(), amount: decimal(12, 2), stock: integer(), note: string().nullable() });
const clickDays = table({ id: id(), linkId: string().unique("linkDay"), day: date().unique("linkDay"), count: integer() });
const notes = table({ id: id(), body: string() });

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
		{`db.where((orders) => orders.email === email).update((orders) => ({ item: orders.amount }));`, "cột item là string(), orders.amount là decimal(12, 2)"},
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

// history and at() compile to one template each (KITWORK_DATABASE.md mục 2, "Thời điểm và lịch sử"): history takes
// its condition, at() a moment written in the code or from outside, before a read; each mistake is a compile error.
//
// CONTROL (03/11/2026): with at() taken in a write, the template is what fails, "câu database hỏng: at() với update".
// The engine's statement package refuses it on its own as well, so a template written by hand cannot carry it.
func TestDatabaseHistoryAndAtTranslate(t *testing.T) {
	changes, err := compileWrite(t, `db.history((orders) => orders.email === email);`)
	if err != nil {
		t.Fatal(err)
	}
	if changes.Action != statement.ActionHistory || changes.Where == nil {
		t.Fatalf("history = %s", statement.Encode(changes))
	}
	then, err := compileWrite(t, `db.at("2026-10-16T23:00:00+07:00").list((orders) => orders.stock > 0);`)
	if err != nil {
		t.Fatal(err)
	}
	if got := statement.Encode(then); !strings.Contains(got, `"at":{"op":"literal","value":"2026-10-16T23:00:00+07:00"}`) {
		t.Fatalf("at() = %s", got)
	}
	counted, err := compileWrite(t, `db.where((orders) => orders.stock > 0).at(today).count();`)
	if err != nil {
		t.Fatal(err)
	}
	if counted.At == nil || counted.At.Op != statement.OpHole {
		t.Fatalf("at(today) = %s", statement.Encode(counted))
	}
	for _, test := range []struct{ line, want string }{
		{`db.where((orders) => orders.email === email).at(today).update((orders) => ({ stock: 1 }));`, "at() chỉ đi với câu đọc (list, first, count): update là câu ghi"},
		{`db.at(today).history((orders) => orders.email === email);`, "at() chỉ đi với câu đọc (list, first, count): history là mọi thay đổi theo thời gian"},
		{`db.at(today).at(today).list((orders) => orders);`, "at() viết một lần trong câu"},
		{`db.at((orders) => orders).list((orders) => orders);`, "at nhận một thời điểm, không phải lambda"},
		{`db.orderBy((orders) => orders.stock).history((orders) => orders);`, "orderBy không đi với history"},
		{`db.at(today);`, "câu chưa kết thúc"},
	} {
		_, err := compileWrite(t, test.line)
		if err == nil || !strings.Contains(err.Error(), test.want) || !strings.HasPrefix(err.Error(), "router.work:5:") {
			t.Errorf("%s\n  got  %v\n  want router.work:5:…: %s", test.line, err, test.want)
		}
	}
}

// db.transaction((database) => { … }) lowers to db.#transaction(function) (KITWORK_DATABASE.md mục 3): the function is
// code, and each statement on its parameter is translated as one on db. A write through db inside the function, a
// transaction inside one, a function not written in place or with other than one parameter are compile errors.
//
// CONTROL (03/11/2026): with the parameter not bound as a handle, the statements inside are not translated,
// "the transaction's function holds 0 templates, want 2"; with a write through db inside not looked at, "got <nil>".
func TestDatabaseTransactionTranslate(t *testing.T) {
	bytecode, err := compileRoute(t, writeHead+`const order = db.transaction((database) => {
  const created = database.insert((orders) => ({ email, item: "x", amount: 1, stock: 2 }));
  database.where((orders) => orders.email === email).update((orders) => ({ stock: orders.stock - 1 }));
  return created;
});
`, map[string]string{".shared/schema/shop.work": writeSchema})
	if err != nil {
		t.Fatal(err)
	}
	if found := templates(t, bytecode); len(found) != 2 || found[0].Action != statement.ActionInsert || found[1].Action != statement.ActionUpdate {
		t.Fatalf("the transaction's function holds %d templates, want 2", len(found))
	}
	lowered := false
	for _, constant := range bytecode.Program.Constants() {
		lowered = lowered || constant.Text() == statement.TransactionMethod
	}
	if !lowered {
		t.Fatal("db.transaction is not lowered to #transaction")
	}
	for _, test := range []struct{ line, want string }{
		{`db.transaction((database) => { db.insert((orders) => ({ email, item: "x", amount: 1, stock: 1 })); });`, "ghi qua db bên trong db.transaction(…): viết trên handle của giao dịch"},
		{`db.transaction((database) => { database.transaction((inner) => 1); });`, "database.transaction(…) lồng trong giao dịch là lỗi"},
		{`const work = (database) => 1;
db.transaction(work);`, "transaction nhận một hàm viết ngay trong câu"},
		{`db.transaction((a, b) => 1);`, "hàm của transaction nhận một tham số"},
		{`db.where((orders) => orders.stock > 0).transaction((database) => 1);`, "transaction đứng một mình"},
	} {
		_, err := compileWrite(t, test.line)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s\n  got  %v\n  want %s", test.line, err, test.want)
		}
	}
}
