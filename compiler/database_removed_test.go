package compiler

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/database/statement"
)

// The older spellings of the database module were removed on 22/10/2026 (KITWORK_DATABASE.md mục 9): an
// import of one, or a read of one off the module's name, is a compile error where it is written, saying
// what the spec writes instead. It holds in a file that imports a database and in one that does not, and
// in a schema file.
func TestDatabaseOldSpellingsAreCompileErrors(t *testing.T) {
	for _, test := range []struct{ source, marker, want string }{
		{"import { kitdb } from \"kitwork/database\";\n", "import { kitdb", `import { kitdb } from "kitwork/database" đã bỏ: database của app khai export const db = connection().schema({ bảng })`},
		{"import { kitdb } from \"kitwork/database\";\n", "import { kitdb", "app.database(path, { port }) của host"},
		{"import { safe, sqlite } from \"kitwork\";\n", "import { safe", `import { sqlite } from "kitwork" đã bỏ: SQLite là connection("tên")`},
		{"import { qrcode } from \"kitwork\";\n", "import { qrcode", `import { qrcode } from "kitwork" đã bỏ: không có spec nên engine gỡ ngày 28/10/2026`},
		{"import { webhook } from \"kitwork\";\n", "import { webhook", `import { webhook } from "kitwork" đã bỏ: không có spec nên engine gỡ ngày 28/10/2026`},
		{"import { insights } from \"kitwork\";\n", "import { insights", `import { insights } from "kitwork" đã bỏ: không có spec nên engine gỡ ngày 28/10/2026`},
		{"import { playground } from \"kitwork\";\n", "import { playground", `import { playground } from "kitwork" đã bỏ: không có spec nên engine gỡ ngày 28/10/2026`},
		{"import { open as file } from \"kitwork/database\";\n", "import { open", `import { open } from "kitwork/database" đã bỏ: database của app khai`},
		{"import { struct, text } from \"kitwork/database\";\n", "import { struct", `import { struct } from "kitwork/database" đã bỏ: struct import từ "kitwork/schema"`},
		{"import { text } from \"kitwork/database\";\n", "import { text", `import { text } from "kitwork/database" đã bỏ: kiểu cột import từ "kitwork/schema": import { text } from "kitwork/schema"`},
		{"import { kitid } from \"kitwork/database\";\n", "import { kitid", `import { kitid } from "kitwork/database" đã bỏ: kiểu cột của "kitwork/schema", chỉ trong app/.shared/schema/: viết id()`},
		{"import { choice } from \"kitwork/database\";\n", "import { choice", `choice() không có trong kiểu cột của "kitwork/schema": id(), text()`},
		{"import { entity } from \"kitwork/database\";\n", "import { entity", "ai thấy dòng nào là policy()"},
		{"import { system } from \"kitwork/database\";\n", "import { system", "ai thấy dòng nào là policy()"},
		{"import { atomic } from \"kitwork/database\";\n", "import { atomic", "giao dịch là db.transaction((database) => …)"},
		{"import { connected } from \"kitwork/database\";\n", "import { connected", `database có tên là connection("tên")`},
		{"import { where } from \"kitwork/database\";\n", "import { where", `import { db } from "app/.shared/schema/<tên>.work" rồi db.list((bảng) => …)`},
		{"import * as database from \"kitwork/database\";\nconst db2 = database.kitdb(\"app.kitdb\", {});\n", "kitdb(", "database.kitdb đã bỏ: database của app khai"},
		{"import { database } from \"kitwork\";\nconst rows = database.table(\"users\").list();\n", "table(", "database.table đã bỏ: câu viết trên database khai trong app/.shared/schema/"},
		{"import { database } from \"kitwork\";\nconst { text, int } = database;\n", "text,", `database.text đã bỏ: kiểu cột import từ "kitwork/schema"`},
		{"import * as database from \"kitwork/database\";\nconst tx = database.atomic;\n", "atomic;", "database.atomic đã bỏ: giao dịch là db.transaction"},
	} {
		for name, source := range map[string]string{"with a database": importDB + test.source, "without one": test.source} {
			_, err := compileRoute(t, source)
			if err == nil || !strings.HasPrefix(err.Error(), at(source, test.marker)+": ") || !strings.Contains(err.Error(), test.want) {
				t.Errorf("%s (%s):\n  got  %v\n  want %s: …%s…", test.source, name, err, at(source, test.marker), test.want)
			}
		}
	}

	// in a schema file the import is refused at its name, not later at the line that calls it
	schema := "import { struct, id, text } from \"kitwork/schema\";\nimport { connection, kitdb } from \"kitwork/database\";\nconst orders = struct({ id: id(), email: text() });\nexport const db = connection().schema({ orders });\nexport const old = kitdb(\"old.kitdb\", { orders });\n"
	_, err := compileRoute(t, importDB+"const all = db.list((orders) => orders);\n", map[string]string{".shared/schema/shop.work": schema})
	if err == nil || !strings.HasPrefix(err.Error(), "../.shared/schema/shop.work:2:1: ") || !strings.Contains(err.Error(), `import { kitdb } from "kitwork/database" đã bỏ`) {
		t.Errorf("kitdb imported into a schema file: %v", err)
	}

	// the words of the spec still read as they did
	for _, source := range []string{
		"import { desc, asc, one, count, sum } from \"kitwork/database\";\n",
		"import * as router from \"kitwork/router\";\nimport { safe, http } from \"kitwork\";\n",
	} {
		if _, err := compileRoute(t, importDB+source); err != nil {
			t.Errorf("%q: %v", source, err)
		}
	}
}

// crmSchema declares a database outside the app (KITWORK_DATABASE.md mục 1): connect({ … }) has no tables,
// only query().
const crmSchema = "import { environment } from \"kitwork\";\nimport { connect } from \"kitwork/database\";\n\nexport const crm = connect({ postgresql: environment.CRM_URL });\n"

const importCRM = "import { crm } from \"app/.shared/schema/crm.work\";\n"

// A handle of connect({ … }) answers query(sql, [giá trị]) as written, and nothing else; query() on the
// app's own database needs a permission of application.work that the spec leaves open (mục 8.9).
func TestDatabaseConnectHandle(t *testing.T) {
	crm := map[string]string{".shared/schema/crm.work": crmSchema}
	for _, source := range []string{
		"const rows = crm.query(\"SELECT id FROM customers WHERE id = $1\", [7]);\n",
		"const count = crm.query(\"SELECT 1\").length;\n",
		"const ids = crm.query(\"SELECT id FROM customers\", []).map((row) => row.id);\n",
		"const pick = (id) => crm.query(\"SELECT id FROM customers WHERE id = $1\", [id]);\n",
	} {
		bytecode, err := compileRoute(t, importCRM+source, crm)
		if err != nil {
			t.Errorf("%q: %v", source, err)
			continue
		}
		if found := templates(t, bytecode); len(found) != 0 {
			t.Errorf("%q: a query is not translated, got templates %v", source, found)
		}
	}

	for _, test := range []struct{ source, marker, want string }{
		{"const rows = crm.list((customers) => customers);\n", "list(", "crm.list không có: crm là database ngoài khai bằng connect(…), chỉ có crm.query(sql, [giá trị])"},
		{"const rows = crm.query();\n", "query(", "crm.query nhận câu SQL và mảng giá trị"},
		{"const rows = crm.query(\"a\", [], 3);\n", "query(", "crm.query nhận câu SQL và mảng giá trị"},
		{"const table = crm.customers;\n", "customers", "crm.customers không có: crm là database ngoài khai bằng connect(…), chỉ có crm.query(sql, [giá trị])"},
		{"const keep = [crm];\n", "crm]", "chỉ dùng làm gốc câu"},
		{"const sum = db.query(\"select 1\", []);\n", "query(", "db." + statement.ManagedQuery},
	} {
		source := importCRM + importDB + test.source
		_, err := compileRoute(t, source, crm)
		if err == nil || !strings.HasPrefix(err.Error(), at(source, test.marker)+": ") || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s:\n  got  %v\n  want %s: …%s…", test.source, err, at(source, test.marker), test.want)
		}
	}
}

// connection("warehouse") declares a database the host names in app.databases: the compiler reads its
// tables as it reads the app's own and translates its statements the same way.
func TestDatabaseNamedConnectionTranslates(t *testing.T) {
	warehouse := "import { struct, id, text, integer } from \"kitwork/schema\";\nimport { connection } from \"kitwork/database\";\n\nconst stocks = struct({ id: id(), item: text().unique(), quantity: integer() });\n\nexport const warehouse = connection(\"warehouse\").schema({ stocks });\n"
	files := map[string]string{".shared/schema/warehouse.work": warehouse}
	source := "import { warehouse } from \"app/.shared/schema/warehouse.work\";\nconst low = warehouse.list((stocks) => stocks.quantity < 5);\n"
	bytecode, err := compileRoute(t, source, files)
	if err != nil {
		t.Fatal(err)
	}
	found := templates(t, bytecode)
	if len(found) != 1 || found[0].Table != "stocks" || found[0].Where == nil || found[0].Where.Op != "<" {
		t.Fatalf("warehouse.list: %v, want one statement on stocks with quantity < 5", found)
	}
	_, err = compileRoute(t, "import { warehouse } from \"app/.shared/schema/warehouse.work\";\nconst low = warehouse.list((stocks) => stocks.weight < 5);\n", files)
	if err == nil || !strings.Contains(err.Error(), "bảng stocks không có cột weight") {
		t.Fatalf("a column stocks has not: %v", err)
	}
	root := databaseApp(t, files)
	catalog, err := SchemaCatalog(root, filepath.Join(root, ".shared", "schema", "warehouse.work"))
	if err != nil {
		t.Fatal(err)
	}
	if catalog["warehouse"] == nil || catalog["warehouse"].Connection != "warehouse" || catalog["warehouse"].External != "" {
		t.Fatalf("the catalog of warehouse.work: %#v", catalog["warehouse"])
	}
}
