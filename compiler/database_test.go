package compiler

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kitwork/engine/database/statement"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// shopSchema is the schema of KITWORK_DATABASE.md's slice 1, as the spec writes it.
const shopSchema = `import { struct, id, text, decimal, date, timestamp } from "kitwork/schema";
import { connection } from "kitwork/database";

const orders = struct({ id: id(), email: text(), item: text(), amount: decimal(12, 2), note: text().nullable(), day: date().index(), createdAt: timestamp().created() });

export const db = connection().schema({ orders });
`

// databaseApp writes an app with the shop schema and files (paths from the app's folder; a file named
// .shared/schema/shop.work replaces the schema), and gives its folder.
func databaseApp(t *testing.T, files map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "acme")
	all := map[string]string{".shared/schema/shop.work": shopSchema}
	for name, source := range files {
		all[name] = source
	}
	for name, source := range all {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// compileRoute compiles source as shop.localhost/router.work of an app with the shop schema and the
// files given.
func compileRoute(t *testing.T, source string, files ...map[string]string) (*Bytecode, error) {
	t.Helper()
	all := map[string]string{"shop.localhost/router.work": source}
	for _, more := range files {
		for name, text := range more {
			all[name] = text
		}
	}
	root := databaseApp(t, all)
	return CompileFileInApp(root, filepath.Join(root, "shop.localhost", "router.work"))
}

// templates are the constants of a program that decode as statements.
func templates(t *testing.T, bytecode *Bytecode) []*statement.Statement {
	t.Helper()
	var found []*statement.Statement
	for _, constant := range bytecode.Program.Constants() {
		if constant.K != value.String || !strings.HasPrefix(constant.Text(), `{"v":`) {
			continue
		}
		decoded, err := statement.Decode(constant.Text())
		if err != nil {
			t.Fatalf("template %s: %v", constant.Text(), err)
		}
		found = append(found, decoded)
	}
	return found
}

// lambdaParameters lists the parameters of every function constant of a program.
func lambdaParameters(bytecode *Bytecode) [][]string {
	var found [][]string
	for _, constant := range bytecode.Program.Constants() {
		if lambda, ok := constant.V.(*value.Lambda); ok && constant.K == value.Func {
			found = append(found, lambda.Params)
		}
	}
	return found
}

// hasLambda reports a function constant whose parameters are exactly parameters.
func hasLambda(bytecode *Bytecode, parameters ...string) bool {
	for _, found := range lambdaParameters(bytecode) {
		if reflect.DeepEqual(found, parameters) {
			return true
		}
	}
	return false
}

// at is where marker first stands in source, as router.work:line:column (columns in characters).
func at(source, marker string) string {
	index := strings.Index(source, marker)
	if index < 0 {
		return "router.work:?"
	}
	line := strings.Count(source[:index], "\n") + 1
	start := strings.LastIndex(source[:index], "\n") + 1
	return "router.work:" + strconv.Itoa(line) + ":" + strconv.Itoa(utf8.RuneCountInString(source[start:index])+1)
}

const importDB = "import { db } from \"app/.shared/schema/shop.work\";\n"

// A statement on a handle imported from .shared/schema/ compiles to one call: LOAD db, PUSH the template,
// LOAD each hole, PUSH "#statement", INVOKE 1+holes. Its lambda is never compiled: no function constant
// has the table for a parameter, and && keeps both sides in the template.
func TestDatabaseStatementLowersToOneInvoke(t *testing.T) {
	source := importDB + "const today = \"2026-10-22\";\nconst rows = db.list((orders) => orders.day === today && orders.amount > 5);\n"
	bytecode, err := compileRoute(t, source)
	if err != nil {
		t.Fatal(err)
	}
	found := templates(t, bytecode)
	if len(found) != 1 {
		t.Fatalf("%d templates, want one", len(found))
	}
	got := found[0]
	hole := 0
	want := &statement.Statement{
		Version: statement.Version, Action: "list", Table: "orders",
		Where: &statement.Node{Op: "&&", Args: []*statement.Node{
			{Op: "===", Args: []*statement.Node{{Op: "column", Column: "day"}, {Op: "hole", Hole: &hole}}},
			{Op: ">", Args: []*statement.Node{{Op: "column", Column: "amount"}, statement.Literal(5)}},
		}},
		Holes: []statement.Hole{{Site: at(source, "today &&")}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("template:\n%s\nwant\n%s", statement.Encode(got), statement.Encode(want))
	}
	if hasLambda(bytecode, "orders") {
		t.Fatalf("a function constant takes orders: the statement's lambda was compiled (%v)", lambdaParameters(bytecode))
	}
	inspection, err := runtime.InspectProgram(bytecode.Program)
	if err != nil {
		t.Fatal(err)
	}
	var tail []string
	for _, instruction := range inspection.Instructions {
		tail = append(tail, instruction.Name+" "+instruction.ConstantPreview)
	}
	call := -1
	for index := range tail {
		if strings.HasPrefix(tail[index], `PUSH "{\"v\":1`) {
			call = index
		}
	}
	if call < 1 || len(tail) < call+4 ||
		tail[call-1] != `LOAD "db"` || tail[call+1] != `LOAD "today"` || tail[call+2] != `PUSH "#statement"` ||
		!strings.HasPrefix(tail[call+3], "INVOKE") || inspection.Instructions[call+3].Operands[0] != 2 {
		t.Fatalf("the statement does not compile to LOAD db; PUSH template; LOAD today; PUSH \"#statement\"; INVOKE 2:\n%s", strings.Join(tail, "\n"))
	}
}

// The task's three statements give byte-exact templates; compiling twice gives the same constants and
// checksum, and no constant holds the machine's path.
func TestDatabaseTemplateGolden(t *testing.T) {
	source := importDB + `import { desc } from "kitwork/database";
const today = "2026-10-22";
const email = "an@example.com";
const item = "cà phê";
const first = db.list((orders) => orders.day === today && orders.amount > 5);
const top = db.where((orders) => orders.email === email).orderBy((orders) => desc(orders.amount)).list(10);
const created = db.insert((orders) => ({ email, item, amount: 35000, day: today }));
`
	root := databaseApp(t, map[string]string{"shop.localhost/router.work": source})
	entry := filepath.Join(root, "shop.localhost", "router.work")
	bytecode, err := CompileFileInApp(root, entry)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, constant := range bytecode.Program.Constants() {
		if constant.K == value.String && strings.HasPrefix(constant.Text(), `{"v":`) {
			got = append(got, constant.Text())
		}
	}
	want := []string{
		`{"v":1,"action":"list","table":"orders","where":{"op":"&&","args":[{"op":"===","args":[{"op":"column","column":"day"},{"op":"hole","hole":0}]},{"op":">","args":[{"op":"column","column":"amount"},{"op":"literal","value":5}]}]},"holes":[{"site":"` + at(source, "today &&") + `"}]}`,
		`{"v":1,"action":"list","table":"orders","where":{"op":"===","args":[{"op":"column","column":"email"},{"op":"hole","hole":0}]},"order":[{"column":"amount","desc":true}],"limit":{"op":"literal","value":10},"holes":[{"site":"` + at(source, "email).") + `"}]}`,
		`{"v":1,"action":"insert","table":"orders","values":[{"column":"email","value":{"op":"hole","hole":0}},{"column":"item","value":{"op":"hole","hole":1}},{"column":"amount","value":{"op":"literal","value":35000}},{"column":"day","value":{"op":"hole","hole":2}}],"holes":[{"site":"` + at(source, "email, item,") + `"},{"site":"` + at(source, "item, amount") + `"},{"site":"` + at(source, "today }") + `"}]}`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("templates:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	again, err := CompileFileInApp(root, entry)
	if err != nil {
		t.Fatal(err)
	}
	if again.Program.Checksum() != bytecode.Program.Checksum() || !reflect.DeepEqual(again.Program.Constants(), bytecode.Program.Constants()) {
		t.Fatalf("two compiles differ: %s and %s", again.Program.Checksum(), bytecode.Program.Checksum())
	}
	for _, constant := range bytecode.Program.Constants() {
		if constant.K == value.String && (strings.Contains(constant.Text(), root) || strings.Contains(constant.Text(), filepath.ToSlash(root))) {
			t.Fatalf("constant %q holds the app's absolute path", constant.Text())
		}
	}
}

// A name a statement uses is checked against the schema file when it compiles, at its place in the route:
// a table, a column, an insert's key, a column the engine fills, the required columns of an insert.
func TestDatabaseNamesCheckedAtCompileTime(t *testing.T) {
	head := importDB + "const today = \"2026-10-22\";\nconst x = 1;\n"
	for _, test := range []struct {
		line, marker, want string
	}{
		{`db.list((order) => order.day === today);`, "order)", "không có bảng order; bảng của db: orders"},
		{`db.list((orders) => orders.emial === "x");`, "emial", "bảng orders không có cột emial; cột: id, email, item, amount, note, day, createdAt"},
		{`db.insert((orders) => ({ emial: "x", item: "y", day: today }));`, "emial", "bảng orders không có cột emial"},
		{`db.insert((orders) => ({ email: "x", item: "y", day: today, createdAt: x }));`, "createdAt:", "createdAt tự điền lúc ghi"},
		{`db.insert((orders) => ({ amount: 1, day: today }));`, "({ amount", "insert vào orders thiếu cột bắt buộc: email, item (cột được bỏ trống khai .nullable())"},
		// an engine limit, not a rule of the spec (mục 1 leaves how an id is made open): said as one
		{`db.insert((orders) => ({ id: "k1", email: "x", item: "y", amount: 1, day: today }));`, "id:", "id là id(), engine sinh lúc insert; ghi id tường minh chưa có trong engine hôm nay (cách sinh id còn mở"},
	} {
		source := head + test.line + "\n"
		_, err := compileRoute(t, source)
		marker := test.marker
		if strings.HasPrefix(marker, "({") {
			marker = marker[1:]
		}
		prefix := at(source, marker) + ": "
		if err == nil || !strings.HasPrefix(err.Error(), prefix) || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s\n  got  %v\n  want %s%s", test.line, err, prefix, test.want)
		}
	}
	if _, err := compileRoute(t, head+`db.insert((orders) => ({ email: "x", item: "y", amount: 1, day: today }));`+"\n"); err != nil {
		t.Fatalf("an insert without the nullable note: %v", err)
	}
}

// Each mistake of the grammar is a compile error with its reason; the forms of the slice compile.
func TestDatabaseGrammarErrors(t *testing.T) {
	head := importDB + "import { desc, asc, one, like, search } from \"kitwork/database\";\nconst today = \"2026-10-22\";\nconst email = \"a\";\nconst ids = [\"a\"];\n"
	for _, test := range []struct{ line, want string }{
		{`const q = db.where((orders) => orders.day === today);`, "câu chưa kết thúc: cần list, first, count hay insert"},
		{`db.list((orders) => orders).where((orders) => orders.day === today);`, "sau hành động không nối gì: list(…) đã chạy câu; .where(…)"},
		{`db.find(1);`, "db.find không có: viết db.first((bảng) => bảng.id === id)"},
		{`const x = db.orders;`, "db.orders không có: câu viết db.list((bảng) => …)"},
		{`db.update((orders) => ({ note: "x" }));`, "db.update chưa có trong engine hôm nay (lát 3"},
		{`db.list((orders) => orders.note.trim() === "");`, "trên bảng và cột không gọi phương thức"},
		{`db.list((orders) => orders.email === orders.item);`, "so hai cột của bảng chưa có"},
		{`db.list((orders) => orders.email === email.trim());`, "không dịch được `email.trim()`: hàm không chạy trong câu; tính trước câu, đặt vào một const"},
		{`db.list((orders) => orders.email == email);`, "viết ===: trong câu database chỉ có === và !=="},
		{`db.orderBy((orders) => desc(orders.amount) || asc(orders.id)).list(5);`, "|| trong orderBy không có"},
		{`db.list((orders, users) => orders.id === "a");`, "nhiều bảng trong một câu chưa có"},
		{`db.list((session) => session.id === "a");`, "session là phiên"},
		{`db.list((orders) => orders.item === like("%x%"));`, "like(…) chưa có trong engine hôm nay (lát 2)"},
		{`db.transaction((database) => 1);`, "db.transaction chưa có trong engine hôm nay (lát 4"},
		{`const d = desc(1);`, "desc chỉ có nghĩa trong lambda của câu database"},
		{`const r = db?.list((orders) => orders);`, "?. không có trên câu database"},
		{`db.list((orders) => orders.note);`, "viết orders.note === true"},
		{`db.list((orders) => orders.amount + 1 > 5);`, "phép tính trên cột chưa có"},
		{`db.list((orders) => orders.email === (() => email)());`, "không dịch được"},
		// a word of the lambda out of its place says where it goes (mục 2, "lỗi chỉ đường"), never "compute it
		// before the statement"; the whole table compares with null (LEFT JOIN) or search
		{`db.list((orders) => orders === search(email));`, "router.work:6:32: search(…) chưa có trong engine hôm nay (lát 2)"},
		{`db.list((orders) => orders === null);`, "router.work:6:28: so cả bảng là LEFT JOIN, chưa có trong engine hôm nay (lát 5)"},
		{`db.list((orders) => orders === email);`, "router.work:6:28: cả bảng chỉ so với null (LEFT JOIN, lát 5) hay search(chữ) (lát 2): so một cột, orders.cột === giá trị"},
		{`db.list((orders) => desc(orders.amount));`, "router.work:6:21: desc(…) chỉ có trong orderBy: (orders) => desc(orders.amount)"},
		{`db.list((orders) => one(ids));`, "router.work:6:21: one(…) chỉ đứng bên phải === hay !==: orders.id === one(ids)"},
		{`db.list((orders) => like("%x%"));`, "router.work:6:21: like(…) chưa có trong engine hôm nay (lát 2)"},
		{`db.orderBy((orders) => orders.amount).list((orders) => !asc(orders.id));`, "router.work:6:57: asc(…) chỉ có trong orderBy"},
		{`db.list((orders) => orders.id === any(ids));`, "router.work:6:35: any(…) không có: viết orders.cột === one(mảng)"},
		{`db.list((orders) => orders.id === oneOf(ids));`, "router.work:6:35: oneOf(…) không có: viết orders.cột === one(mảng)"},
		{`db.list((orders) => ids.includes(orders.id));`, "router.work:6:25: includes không có trong câu database: viết orders.id === one(ids)"},
	} {
		_, err := compileRoute(t, head+test.line+"\n")
		if err == nil || !strings.Contains(err.Error(), test.want) || !strings.HasPrefix(err.Error(), "router.work:6:") {
			t.Errorf("%s\n  got  %v\n  want router.work:6:…: %s", test.line, err, test.want)
		}
	}
	// a variable of the code with a word's name is not the word: a call of it is code, refused as code
	if _, err := compileRoute(t, importDB+"const count = (x) => 1;\nconst ids = [1];\ndb.list((orders) => orders.id === count(ids));\n"); err == nil ||
		!strings.Contains(err.Error(), "không dịch được `count(ids)`") {
		t.Errorf("a function of the code named count: %v", err)
	}
	// desc without its import
	if _, err := compileRoute(t, importDB+"db.orderBy((orders) => desc(orders.amount)).list(5);\n"); err == nil ||
		!strings.Contains(err.Error(), `desc chưa import: import { desc } from "kitwork/database"`) {
		t.Errorf("desc without its import: %v", err)
	}

	for _, line := range []string{
		`const ids2 = db.list((orders) => orders).map((row) => row.id);`,
		`const all = db.list((orders) => orders);`,
		`const n = db.count((orders) => orders.id === one(ids));`,
		`const big = db.first((orders) => 5 < orders.amount && orders.note !== null);`,
	} {
		if _, err := compileRoute(t, head+line+"\n"); err != nil {
			t.Errorf("%s: %v", line, err)
		}
	}
	bytecode, err := compileRoute(t, head+"const sorted = db.where((orders) => orders.day === today).orderBy((orders) => desc(orders.amount)).orderBy((orders) => asc(orders.id) && orders.item).list();\n")
	if err != nil {
		t.Fatal(err)
	}
	found := templates(t, bytecode)
	if len(found) != 1 || !reflect.DeepEqual(found[0].Order, []statement.Key{{Column: "amount", Descending: true}, {Column: "id"}, {Column: "item"}}) {
		t.Fatalf("two orderBy: %v, want amount desc, id, item in the order written", found)
	}
	// 5 < orders.amount is turned around: amount > 5
	bytecode, err = compileRoute(t, head+"const big = db.first((orders) => 5 < orders.amount);\n")
	if err != nil {
		t.Fatal(err)
	}
	if found := templates(t, bytecode); len(found) != 1 || found[0].Where.Op != ">" {
		t.Fatalf("5 < orders.amount: %v, want amount > 5", found)
	}
}

// The schema file declares struct and connection() only, in the spec's words; anything else is a compile
// error at its place in that file. struct() and connection() are refused outside .shared/schema/.
func TestDatabaseSchemaModuleRules(t *testing.T) {
	route := importDB + "const all = db.list((orders) => orders);\n"
	schema := func(columns, declaration string) string {
		return "import { struct, id, text, decimal, date, timestamp, kitid, number, bigint, computed } from \"kitwork/schema\";\n" +
			"import { connection, connect } from \"kitwork/database\";\n" +
			"const users = struct({ id: id(), name: text() });\n" +
			"const orders = struct({ " + columns + " });\n" +
			declaration + "\n"
	}
	exported := "export const db = connection().schema({ orders });"
	columns := "id: id(), email: text()"
	for _, test := range []struct {
		name, source, marker, want string
	}{
		{"kitid", schema("id: kitid(), email: text()", exported), "kitid()", "kitid() là cách viết cũ: viết id()"},
		{"notNull", schema("id: id(), email: text().notNull()", exported), "notNull", ".notNull() là cách viết cũ: cột bắt buộc là mặc định"},
		{"number", schema("id: id(), total: number()", exported), "number()", "number() không có"},
		{"bigint", schema("id: id(), total: bigint()", exported), "bigint()", "bigint() chưa có trong engine hôm nay (lát 9)"},
		{"computed", schema("id: id(), total: computed((orders) => 1)", exported), "computed(", "computed() chưa có trong engine hôm nay (lát 9)"},
		{"foreign key", schema("id: id(), userId: users.id", exported), ".id", "khoá ngoại users.id chưa có trong engine hôm nay (lát 5)"},
		{"connection(application)", schema(columns, `export const db = connection("application").schema({ orders });`), `"application"`, `connection("application") không có: database của app là connection() không tên`},
		{"connection(storage)", schema(columns, `export const db = connection("storage").schema({ orders });`), `"storage"`, `"storage" là tên dành cho datastore()`},
		{"connection(files)", schema(columns, `export const db = connection("files").schema({ orders });`), `"files"`, `"files" là tên dành cho nơi lưu file dùng chung`},
		{"connection(name) not a string", schema(columns, "export const db = connection(`w`).schema({ orders });"), "connection(`", `connection nhận tên database viết bằng chuỗi`},
		{"connection with two names", schema(columns, `export const db = connection("a", "b").schema({ orders });`), `connection("a"`, `connection nhận một tên`},
		{"policy", schema(columns, `export const db = connection().policy((orders, session) => true).schema({ orders });`), "policy", ".policy(…) chưa có trong engine hôm nay (lát 6)"},
		{"connect sqlite", schema(columns, "export const db = connection().schema({ orders });\nexport const crm = connect({ sqlite: \"x\" });"), "sqlite:", "connect({ sqlite: … }) không có"},
		{"connect unknown connector", schema(columns, "export const db = connection().schema({ orders });\nexport const crm = connect({ redis: \"x\" });"), "redis:", "connect({ redis: … }): connect nhận một object có một bộ nối"},
		{"connect two connectors", schema(columns, "export const db = connection().schema({ orders });\nexport const crm = connect({ postgresql: \"x\", mysql: \"y\" });"), "connect({", "connect nhận một object có một bộ nối"},
		{"connect with a schema", schema(columns, "export const db = connection().schema({ orders });\nexport const crm = connect({ postgresql: \"x\" }).schema({ users });"), "schema({ users })", "connect(…) không có .schema: database ngoài là của người khác, chỉ có crm.query(sql, [giá trị])"},
		{"connect not exported", schema(columns, "export const db = connection().schema({ orders });\nconst crm = connect({ postgresql: \"x\" });"), "crm = connect", "database crm khai mà không export"},
		{"not exported", schema(columns, `const db = connection().schema({ orders });`), "db = connection", "database db khai mà không export"},
		{"session table", schema(columns, `export const db = connection().schema({ session: orders });`), "session:", "session là tên dành riêng"},
		{"kitwork_ table", schema(columns, `export const db = connection().schema({ kitwork_x: orders });`), "kitwork_x", "tiền tố kitwork_ là của bảng engine"},
		{"created on text", schema("id: id(), note: text().created()", exported), "created", ".created() chỉ đi sau timestamp()"},
		{"spread", schema("...users", exported), "{ ...users", "struct({ ...x }) chưa có trong engine hôm nay"},
		{"helper", schema(columns, "const helper = 5;\n"+exported), "helper", "file trong .shared/schema/ chỉ khai struct và connection(): helper"},
		{"no id", schema("email: text()", exported), "struct({ email", "bảng không có id() chưa có trong engine hôm nay"},
		{"no schema", schema(columns, `export const db = connection();`), "db = connection", "câu chưa kết thúc"},
	} {
		_, err := compileRoute(t, route, map[string]string{".shared/schema/shop.work": test.source})
		index := strings.Index(test.source, test.marker)
		line := strings.Count(test.source[:index], "\n") + 1
		start := strings.LastIndex(test.source[:index], "\n") + 1
		place := "../.shared/schema/shop.work:" + strconv.Itoa(line) + ":" + strconv.Itoa(utf8.RuneCountInString(test.source[start:index])+1)
		if test.marker == "{ ...users" || test.marker == "struct({ email" {
			// the error stands at the struct's object or its name
			place = "../.shared/schema/shop.work:" + strconv.Itoa(line) + ":"
		}
		if err == nil || !strings.HasPrefix(err.Error(), place) || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s:\n  got  %v\n  want %s…: %s", test.name, err, place, test.want)
		}
	}

	// outside .shared/schema/: at the import
	for _, line := range []string{
		`import { struct, text } from "kitwork/schema";`,
		`import { connection } from "kitwork/database";`,
		`import * as schema from "kitwork/schema";`,
	} {
		source := importDB + line + "\n"
		_, err := compileRoute(t, source)
		if err == nil || !strings.HasPrefix(err.Error(), "router.work:2:1: ") || !strings.Contains(err.Error(), "chỉ có trong .shared/schema/ của app") {
			t.Errorf("%s in router.work: %v", line, err)
		}
	}
	// the module by its own name counts as the module: kitwork's schema is kitwork/schema, and connection or
	// connect read off a name of kitwork/database is refused where it is read
	for _, test := range []struct{ source, marker, want string }{
		{"import { schema } from \"kitwork\";\nconst users = schema.struct({});\n", "import { schema }", `import { schema } from "kitwork" chỉ có trong .shared/schema/ của app`},
		{"import * as database from \"kitwork/database\";\nconst c = database.connection();\n", "connection()", "database.connection chỉ có trong .shared/schema/ của app"},
		{"import { database } from \"kitwork\";\nconst c = database.connect({ postgresql: \"x\" });\n", "connect(", "database.connect chỉ có trong .shared/schema/ của app"},
		{"import { database as d } from \"kitwork\";\nconst c = d.connection;\n", "connection;", "d.connection chỉ có trong .shared/schema/ của app"},
		{"import * as database from \"kitwork/database\";\nconst { connection } = database;\n", "connection }", "database.connection chỉ có trong .shared/schema/ của app"},
		{"import * as database from \"kitwork/database\";\nconst { desc, connect: open } = database;\n", "connect:", "database.connect chỉ có trong .shared/schema/ của app"},
	} {
		for name, source := range map[string]string{"with a database": importDB + test.source, "without one": test.source} {
			_, err := compileRoute(t, source)
			if err == nil || !strings.HasPrefix(err.Error(), at(source, test.marker)+": ") || !strings.Contains(err.Error(), test.want) {
				t.Errorf("%s (%s):\n  got  %v\n  want %s: %s", test.source, name, err, at(source, test.marker), test.want)
			}
		}
	}
	// what else the module holds is read as before (its older words are compile errors:
	// TestDatabaseOldSpellingsAreCompileErrors)
	for _, source := range []string{
		"import { database } from \"kitwork\";\nconst k = database.desc;\n",
		"import * as database from \"kitwork/database\";\nconst connection = 1;\nconst c = { connection };\nconst n = c.connection;\n",
	} {
		if _, err := compileRoute(t, source); err != nil {
			t.Errorf("%q: %v", source, err)
		}
	}
	// in .shared/schema/ the names declare, as the imports do
	namespaces := "import * as schema from \"kitwork/schema\";\nimport * as database from \"kitwork/database\";\nconst orders = schema.struct({ id: schema.id(), email: schema.text() });\nexport const db = database.connection().schema({ orders });\n"
	if _, err := compileRoute(t, route, map[string]string{".shared/schema/shop.work": namespaces}); err != nil {
		t.Errorf("a schema file written with namespaces: %v", err)
	}

	// a file of no app has no .shared/schema/
	path := filepath.Join(t.TempDir(), "router.work")
	if err := os.WriteFile(path, []byte(`import { struct } from "kitwork/schema";`+"\nconst orders = struct({});\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileFile(path); err == nil || !strings.Contains(err.Error(), "chỉ có trong .shared/schema/ của app") {
		t.Errorf("kitwork/schema in a file of no app: %v", err)
	}
	// the folder's spelling is exact: a struct of .Shared/Schema/ is refused where it stands (an app of its
	// own, so the folder is made with that spelling on a disk that does not tell case apart)
	app := filepath.Join(t.TempDir(), "acme")
	for name, text := range map[string]string{
		".Shared/Schema/other.work":  shopSchema,
		"shop.localhost/router.work": "import { db } from \"app/.Shared/Schema/other.work\";\n",
	} {
		file := filepath.Join(app, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := CompileFileInApp(app, filepath.Join(app, "shop.localhost", "router.work"))
	if err == nil || !strings.Contains(err.Error(), "../.Shared/Schema/other.work:1:1: ") || !strings.Contains(err.Error(), "chỉ có trong .shared/schema/ của app") {
		t.Errorf("a schema in .Shared/Schema/: %v", err)
	}
}

// A statement is translated on the name an import of a schema file declares, followed by binding, not by
// spelling: a parameter or a block's const of the same name is another variable, a const of the handle
// is the handle, and a module of .shared/ that imports the handle has its statements translated too. A
// handle read as a value is a compile error, and so is an import of a name the schema file does not
// export.
func TestDatabaseBindingIdentityAndEscapes(t *testing.T) {
	for name, source := range map[string]string{
		"a parameter":       importDB + "const f = (db) => db.list((orders) => orders);\nconst all = db.list((orders) => orders);\n",
		"a block's const":   importDB + "if (true) {\n  const db = { list: (f) => 1 };\n  db.list((orders) => orders);\n}\nconst all = db.list((orders) => orders);\n",
		"a module's export": "import { db } from \"app/.shared/lib/x.work\";\nconst all = db.list((orders) => orders);\n",
	} {
		bytecode, err := compileRoute(t, source, map[string]string{".shared/lib/x.work": "export const db = { list: (f) => 1 };\n"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !hasLambda(bytecode, "orders") {
			t.Errorf("%s: the lambda on a variable that is not the import was not compiled as ordinary code", name)
		}
	}
	for name, test := range map[string]struct {
		source string
		files  map[string]string
	}{
		"a const of the handle":  {source: importDB + "const d = db;\nconst e = d;\nconst all = e.list((orders) => orders);\n"},
		"an alias of the import": {source: "import { db as shop } from \"app/.shared/schema/shop.work\";\nconst all = shop.list((orders) => orders);\n"},
		"a module of .shared/": {
			source: "import { all } from \"app/.shared/lib/report.work\";\nconst rows = all();\n",
			files:  map[string]string{".shared/lib/report.work": importDB + "export const all = () => db.list((orders) => orders);\n"},
		},
	} {
		bytecode, err := compileRoute(t, test.source, test.files)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if hasLambda(bytecode, "orders") || len(templates(t, bytecode)) != 1 {
			t.Errorf("%s: not translated (%v)", name, lambdaParameters(bytecode))
		}
	}
	for _, line := range []string{
		"const f = (x) => x;\nf(db);",
		"const o = { db };",
		"const a = [db];",
		"const g = () => db;",
		"let d = db;",
		"const k = typeof db;",
		"const { list } = db;",
	} {
		_, err := compileRoute(t, importDB+line+"\n")
		if err == nil || !strings.Contains(err.Error(), "db là database khai ở .shared/schema/shop.work: chỉ dùng làm gốc câu") {
			t.Errorf("%q: %v", line, err)
		}
	}
	_, err := compileRoute(t, "import { db } from \"app/.shared/lib/again.work\";\n", map[string]string{
		".shared/lib/again.work": importDB + "export { db };\n",
	})
	if err == nil || !strings.Contains(err.Error(), "export lại db chưa có trong engine hôm nay") {
		t.Errorf("export { db } of a module: %v", err)
	}
	_, err = compileRoute(t, "import { dbb } from \"app/.shared/schema/shop.work\";\n")
	if err == nil || err.Error() != "router.work:1:1: shop.work không export dbb; có: db" {
		t.Errorf("import { dbb }: %v", err)
	}
}

// What a statement reads from outside its lambda is compiled where the statement is, in the code around
// it: a statement in a loop makes no closure and no environment of the loop.
func TestDatabaseHolesNeedNoClosure(t *testing.T) {
	source := importDB + "const today = \"2026-10-22\";\nconst items = [{ email: \"a\", name: \"x\" }];\nconst run = () => {\n  for (const item of items) {\n    db.insert((orders) => ({ email: item.email, item: item.name, amount: 1, day: today }));\n  }\n};\n"
	root := databaseApp(t, map[string]string{"shop.localhost/router.work": source})
	program, _, sources, err := prepareFile(root, filepath.Join(root, "shop.localhost", "router.work"))
	if err != nil {
		t.Fatal(err)
	}
	c := newCompilerWithSources(sources)
	resolution, err := c.resolveProgram(program)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolution.environments) != 0 {
		t.Fatalf("the loop hands environments %v: a hole made a closure", resolution.environments)
	}
	for _, loop := range resolution.loops {
		if loop.environment != "" {
			t.Fatalf("the loop has an environment %s", loop.environment)
		}
	}
	bytecode, err := compilePreparedFile(program, nil, sources)
	if err != nil {
		t.Fatal(err)
	}
	for _, parameters := range lambdaParameters(bytecode) {
		if len(parameters) != 0 {
			t.Fatalf("function constants %v: only the modules and run, none with a parameter", lambdaParameters(bytecode))
		}
	}
	inspection, err := runtime.InspectProgram(bytecode.Program)
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	for index := 0; index+2 < len(inspection.Instructions); index++ {
		first, second, third := inspection.Instructions[index], inspection.Instructions[index+1], inspection.Instructions[index+2]
		if first.Name == "LOAD" && strings.HasPrefix(first.ConstantPreview, `"item#`) && second.Name == "PUSH" &&
			(second.ConstantPreview == `"email"` || second.ConstantPreview == `"name"`) && third.Name == "GET" {
			reads++
		}
	}
	if reads != 2 {
		t.Fatalf("item.email and item.name compile as %d reads of the loop's item, want 2", reads)
	}
}

// A program that imports no database resolves and compiles as before: a local db with a list method runs
// its lambda as any function, and the program's bytes are pinned.
func TestDatabaseProgramsWithoutHandlesUnchanged(t *testing.T) {
	source := "const db = { list: (f) => 1 };\nvar result = db.list((orders) => orders.x === 1);\n"
	bytecode, err := CompileSource(source)
	if err != nil {
		t.Fatal(err)
	}
	if !hasLambda(bytecode, "orders") {
		t.Fatalf("the lambda of a local db was not compiled (%v)", lambdaParameters(bytecode))
	}
	// what schema 11 compiled the same source to, before statements were translated
	const pinned = "d3ae9759f05d64423f3808f93dd4da621a1b77b1a4733a01c84673494fbfb946"
	if got := bytecode.Program.Checksum(); got != pinned {
		t.Fatalf("checksum %s, want %s", got, pinned)
	}
	// in an app, with an import of another module, the same
	inApp, err := compileRoute(t, "import { x } from \"app/.shared/lib/x.work\";\n"+source, map[string]string{".shared/lib/x.work": "export const x = 1;\n"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasLambda(inApp, "orders") || len(templates(t, inApp)) != 0 {
		t.Fatalf("a program with no database import was translated")
	}
}

// The artifact cache keys on the schema file's source: a second compile is the same artifact, and after
// a column is renamed in the schema the route's statement fails at its place, with the new columns.
func TestDatabaseFileCacheSeesSchemaEdits(t *testing.T) {
	source := importDB + "const all = db.list((orders) => orders.email === \"a\");\n"
	root := databaseApp(t, map[string]string{"shop.localhost/router.work": source})
	entry := filepath.Join(root, "shop.localhost", "router.work")
	cache := NewFileCache(filepath.Join(t.TempDir(), "bytecode"))
	first, err := cache.CompileFileInApp(root, entry)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cache.CompileFileInApp(root, entry)
	if err != nil {
		t.Fatal(err)
	}
	if first.Program.Checksum() != second.Program.Checksum() {
		t.Fatalf("a second compile gave another program")
	}
	renamed := strings.Replace(shopSchema, "email: text()", "mail: text()", 1)
	if err := os.WriteFile(filepath.Join(root, ".shared", "schema", "shop.work"), []byte(renamed), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = cache.CompileFileInApp(root, entry)
	want := at(source, "email ===") + ": bảng orders không có cột email; cột: id, mail, item, amount, note, day, createdAt"
	if err == nil || err.Error() != want {
		t.Fatalf("after renaming email: %v, want %s", err, want)
	}
}

// The template's format is part of the compiler's schema: a change of one without the other would give
// an artifact the engine reads another way.
func TestStatementVersionPairsWithSchema(t *testing.T) {
	if statement.Version != 1 || CompilerSchemaVersion != 14 {
		t.Fatalf("statement format %d with compiler schema %d: a change of the format bumps the schema (fingerprint.go) and this pair",
			statement.Version, CompilerSchemaVersion)
	}
}
