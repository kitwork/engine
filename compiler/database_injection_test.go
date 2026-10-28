package compiler

import (
	"strings"
	"testing"
)

// A value glued into the SQL of query(…), by ${…}, +, concat or join, written in the call or built in a
// name of the same file, is a compile error naming the value (KITWORK_DATABASE.md mục 2, "SQL thô"). SQL
// that is constant text, or that the compiler cannot see built, compiles.
func TestDatabaseQueryInjection(t *testing.T) {
	crm := map[string]string{".shared/schema/crm.work": crmSchema}
	for _, source := range []string{
		"const base = \"SELECT id FROM customers\";\nconst rows = crm.query(base + \" ORDER BY id\");\n",
		"const rows = crm.query(`SELECT id\n  FROM customers\n  WHERE id = $1`, [7]);\n",
		"const limit = 10;\nconst rows = crm.query(\"SELECT id FROM customers LIMIT \" + limit);\n",
		"const run = (sql) => crm.query(sql, []);\n",
		"const pick = (id) => {\n  let sql = \"SELECT id FROM customers\";\n  sql += \" ORDER BY id\";\n  return crm.query(sql, []);\n};\n",
		"const pick = (id) => crm.query(id ? \"SELECT id FROM customers WHERE id = $1\" : \"SELECT id FROM customers\", [id]);\n",
		"const greeting = (name) => \"Xin chào \" + name;\nconst rows = crm.query(\"SELECT 1\");\n",
	} {
		if _, err := compileRoute(t, importCRM+source, crm); err != nil {
			t.Errorf("%q: %v", source, err)
		}
	}

	for _, test := range []struct{ source, call, glued, want string }{
		{"const pick = (id) => crm.query(`SELECT * FROM customers WHERE id = ${id}`);\n", "query(", "id}", "dán id vào chữ SQL"},
		{"const pick = (day) => crm.query(\"SELECT * FROM orders WHERE day = \" + day);\n", "query(", "day);", "dán day vào chữ SQL"},
		{"const pick = (id) => {\n  const sql = \"SELECT * FROM customers WHERE id = \" + id;\n  return crm.query(sql, []);\n};\n", "query(", "id;", "dán id vào chữ SQL"},
		{"const pick = (id) => {\n  let sql = \"SELECT * FROM customers\";\n  sql += \" WHERE id = \" + id;\n  return crm.query(sql);\n};\n", "query(", "id;", "dán id vào chữ SQL"},
		{"const table = \"customers\";\nconst rows = crm.query(`SELECT * FROM ${table}`);\n", "query(", "table}", "dán table vào chữ SQL"},
		{"const pick = (id) => crm.query(id ? \"SELECT * FROM customers WHERE id = \" + id : \"SELECT 1\");\n", "query(", "id :", "dán id vào chữ SQL"},
		{"const pick = (id) => crm.query(\"SELECT * FROM customers WHERE id = \" + id).map((row) => row.id);\n", "query(", "id).map", "dán id vào chữ SQL"},
		{"const pick = (id) => crm.query(\"SELECT * FROM customers WHERE id = \".concat(id));\n", "query(", "id))", "dán id vào chữ SQL"},
		{"const pick = (id) => crm.query([\"SELECT * FROM customers WHERE id =\", id].join(\" \"));\n", "query(", "id]", "dán id vào chữ SQL"},
	} {
		source := importCRM + test.source
		_, err := compileRoute(t, source, crm)
		want := at(source, test.call) + ": crm.query(…) " + test.want + " (" + at(source, test.glued) + ") trước khi gọi, thành SQL injection"
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "viết crm.query(\"… $1\", [") {
			t.Errorf("%s:\n  got  %v\n  want …%s…", test.source, err, want)
		}
	}
}
