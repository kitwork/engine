package statement

import "strings"

// Schema of a database (KITWORK_DATABASE.md mục 1, "Schema", 29/10/2026): managed as Postgres, a database
// holds schemas and a schema holds tables. Each schema is a file of its own in the database's folder, so
// connection().schema("billing").tables({ invoices }) puts invoices in a file beside the public one, which
// connection().tables({ … }) declares.

// TableRenamed is the error of struct({ … }), which table({ … }) replaced on 29/10/2026.
const TableRenamed = `struct({ … }) đổi thành table({ … }) (29/10/2026): import { table, id, text } from "kitwork/schema"; const orders = table({ id: id(), … })`

// TablesRenamed is the error of .schema({ bảng }), which .tables({ bảng }) replaced on 29/10/2026: .schema
// now picks a schema by its name.
const TablesRenamed = `.schema({ bảng }) đổi thành .tables({ bảng }) (29/10/2026): connection().tables({ orders }); .schema("tên") nay chọn schema: connection().schema("billing").tables({ invoices })`

// PublicFile is the SQLite file of a database's public schema (KITWORK_KITDB.md mục 3, 29/10/2026).
const PublicFile = "kitwork.db"

// SchemaFileName is the SQLite file a schema lives in, in its database's folder (.database/application/ for
// the app's own): kitwork.db for public (""), billing.db for .schema("billing").
func SchemaFileName(schema string) string {
	if schema == "" {
		return PublicFile
	}
	return schema + ".db"
}

// SchemaName checks the name connection().schema("…") is given: "" when it may name a schema, else the
// error. Lowercase only, as Postgres folds a name it is given and a file system may not tell Billing from
// billing; public, main and temp are taken, and kitwork, the file of public.
func SchemaName(name string) string {
	switch strings.ToLower(name) {
	case "":
		return `schema nhận tên viết bằng chuỗi: connection().schema("billing").tables({ … })`
	case "public":
		return `public là schema mặc định: bỏ .schema("public"), viết connection().tables({ … })`
	case "main", "temp":
		return name + ` là tên SQLite giữ cho mình: đặt tên schema khác`
	case "kitwork":
		return `kitwork.db là file của schema public: đặt tên schema khác`
	}
	if len(name) > 63 {
		return "tên schema dài tối đa 63 ký tự, như Postgres"
	}
	for index, character := range name {
		letter := character >= 'a' && character <= 'z'
		if letter || index > 0 && (character >= '0' && character <= '9' || character == '_') {
			continue
		}
		return `tên schema viết chữ thường, số và gạch dưới, bắt đầu bằng chữ: "billing", "sales_2026"`
	}
	return ""
}
