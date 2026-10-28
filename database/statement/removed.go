package statement

import (
	"fmt"
	"strings"
)

// The older spellings of the database module, removed on 22/10/2026 (KITWORK_DATABASE.md mục 9): the words
// kitwork/database answered before connection() and connect() became its only declarations, and sqlite of
// the module kitwork. Each is refused by its name where the compiler sees it (an import, database.<word>)
// and where the engine meets it (a module read by a way the compiler did not see), in the same words: what
// the spec writes instead. There is no second spelling that still runs.

// schemaFile is where an app's databases are declared and imported from.
const schemaFile = "app/.shared/schema/<tên>.work"

// columnWords lists the column builders of kitwork/schema, for an error that points to them.
const columnWords = "id(), text(), integer(), float(), decimal(12, 2), boolean(), date(), json(), timestamp()"

const (
	declareDatabase = "database của app khai export const db = connection().schema({ bảng }) trong " + schemaFile
	statementOnDB   = "câu viết trên database khai trong app/.shared/schema/: import { db } from \"" + schemaFile + "\" rồi db.list((bảng) => …)"
	declareSQLite   = "SQLite là connection(\"tên\") trong app/.shared/schema/, với tên đó khai { sqlite: đường_dẫn } trong app.databases({ … }) của application.work; SQL thô trên database ngoài là connect({ … }).query(sql, [giá trị])"
	noEntity        = "mỗi app có database của nó, connection() trong app/.shared/schema/; ai thấy dòng nào là policy() (chưa có trong engine hôm nay, lát 6)"
)

// removedWords are the older words of kitwork/database that are not column builders, with what the spec
// writes instead.
var removedWords = map[string]string{
	"kitdb": declareDatabase + "; mở KitDB ra ngoài qua Hrana hay PostgreSQL wire (token, access) là app.database(path, { port }) của host trong application.work; " +
		"{ drop: true }, { migrate: true } không còn: phần thêm tự migrate lúc khai, phần mất dữ liệu bị từ chối; " +
		"giữ lịch sử ({ recovery }) là việc của host (KITWORK_DATABASE.md mục 7.4), chưa có trong engine hôm nay",
	"open":      declareDatabase,
	"define":    declareDatabase,
	"sqlite":    declareSQLite,
	"struct":    `struct import từ "kitwork/schema": import { struct, id, text } from "kitwork/schema", chỉ trong app/.shared/schema/`,
	"entity":    noEntity,
	"system":    noEntity,
	"connected": "database có tên là connection(\"tên\") trong app/.shared/schema/, tên đó khai trong app.databases({ … }) của application.work; tên host không khai là lỗi boot",
	"atomic":    "giao dịch là db.transaction((database) => …) trên database khai trong app/.shared/schema/ (chưa có trong engine hôm nay, lát 4)",
	"table":     statementOnDB,
	"select":    statementOnDB,
	"where":     statementOnDB,
	"limit":     statementOnDB,
	"find":      statementOnDB,
	"first":     statementOnDB,
	"list":      statementOnDB,
	"exists":    statementOnDB,
	"create":    statementOnDB,
	"update":    statementOnDB,
	"save":      statementOnDB,
	"delete":    statementOnDB,
	"remove":    statementOnDB,
	"orderBy":   statementOnDB,
	"groupBy":   statementOnDB,
	"join":      statementOnDB,
}

// spelledColumns are the column builders the older module had under the spec's own name.
var spelledColumns = map[string]bool{
	"id": true, "text": true, "integer": true, "float": true, "decimal": true, "boolean": true, "date": true, "json": true,
}

// otherColumns are the column builders of the older module that the spec's vocabulary has not.
var otherColumns = map[string]bool{
	"uuid": true, "serial": true, "varchar": true, "char": true, "choice": true, "time": true, "year": true,
	"month": true, "day": true, "array": true, "blob": true, "bytes": true, "ip": true, "mac": true, "vector": true,
}

// RemovedWord is what the spec writes for word, an older word of kitwork/database, and whether it is one.
func RemovedWord(word string) (string, bool) {
	if spelling, ok := removedWords[word]; ok {
		return spelling, true
	}
	switch {
	case spelledColumns[word]:
		return fmt.Sprintf(`kiểu cột import từ "kitwork/schema": import { %s } from "kitwork/schema", chỉ trong app/.shared/schema/`, word), true
	case LegacyBuilders[word] != "":
		return fmt.Sprintf(`kiểu cột của "kitwork/schema", chỉ trong app/.shared/schema/: %s`, LegacyBuilders[word]), true
	case otherColumns[word]:
		return fmt.Sprintf(`%s() không có trong kiểu cột của "kitwork/schema": %s`, word, columnWords), true
	}
	return "", false
}

// helpersRemoved are helpers of the module kitwork the engine took out on 28/10/2026 because no spec stood
// behind them (KITWORK_ENGINE.md mục 7). One needed again is written as a spec first, then a module.
var helpersRemoved = map[string]bool{"qrcode": true, "napas": true, "jwt": true, "webhook": true, "insights": true, "playground": true}

const helperRemoved = "không có spec nên engine gỡ ngày 28/10/2026; cần lại thì viết spec trước, rồi thêm thành một module (KITWORK_ENGINE.md mục 7)"

// RemovedKitworkWord is what the spec writes for word, an older word of the module kitwork itself
// (import { sqlite } from "kitwork"), and whether it is one.
func RemovedKitworkWord(word string) (string, bool) {
	if word == "sqlite" {
		return declareSQLite, true
	}
	if helpersRemoved[word] {
		return helperRemoved, true
	}
	return "", false
}

// EngineTable is whether name is a table of the engine, written in any case (SQLite reads table names without
// case): kitwork_runs, kitwork_definitions, kitwork_schedules, kitwork_schema, kitwork_history, and on a SQLite
// file the ledger of its migrations, _kitwork_migrations. schema({ … }) declares none of them.
func EngineTable(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "kitwork_") || strings.HasPrefix(lower, "_kitwork_")
}

// ReservedConnection is why name cannot be a connection("tên"): a name of the host's catalogue the spec
// keeps for what it holds (KITWORK_DATABASE.md mục 1, KITWORK_STORAGE.md, KITWORK_FILE.md); "" for any
// other name.
func ReservedConnection(name string) string {
	switch name {
	case "application":
		return `connection("application") không có: database của app là connection() không tên`
	case "storage":
		return `"storage" là tên dành cho datastore() (KITWORK_STORAGE.md), không dùng làm connection("storage")`
	case "files":
		return `"files" là tên dành cho nơi lưu file dùng chung (KITWORK_FILE.md), không dùng làm connection("files")`
	}
	return ""
}

// Connectors are the connectors of connect({ … }), a database outside the app (KITWORK_DATABASE.md mục 1):
// the key names the connector, its value is the URL.
var Connectors = map[string]bool{"postgresql": true, "mysql": true}
