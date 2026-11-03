package compiler

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kitwork/engine/database/statement"
	"github.com/kitwork/engine/value"
)

// readSchemaModule reads a file of .shared/schema/ (KITWORK_DATABASE.md mục 1): its imports of Kitwork,
// its tables (const orders = table({ … })) and its databases (export const db = connection().tables({
// orders }), connection().schema("billing").tables({ invoices })). It reads the syntax and never guesses: a statement it cannot read is a compile error at that
// statement, so what the compiler checks a statement against is what the file declares. The engine checks
// again against the live schema when the statement first runs.
func readSchemaModule(program *Program, place func(Token) string) (map[string]*statement.Database, error) {
	reader := &schemaReader{place: place, words: map[string]kitworkWord{}, tables: map[string]*statement.Table{}}
	exported := make(map[string]bool, len(program.Exports))
	for _, name := range program.Exports {
		exported[name] = true
	}
	// imports first, as JavaScript hoists them
	var rest []Statement
	for _, node := range program.Statements {
		if !reader.kitworkImport(node) {
			rest = append(rest, node)
		}
	}
	databases := map[string]*statement.Database{}
	for _, node := range rest {
		if reader.err != nil {
			break
		}
		switch node := node.(type) {
		case *ImportStatement:
			reader.fail(node.Token, fmt.Sprintf("import %q trong .shared/schema/ chưa có trong engine hôm nay (lát 5: bảng từ file khác; lát 6: queue cho after()); khai bảng ngay trong file này", node.Source))
		case *VarStatement:
			if len(node.Names) != 1 || node.DestructMode != DestructNone || node.Token.Kind != Const {
				reader.fail(node.Token, "file trong .shared/schema/ chỉ khai table và connection(): mỗi dòng là const tên = table({ … }) hay export const db = connection().tables({ … })")
				continue
			}
			name := node.Names[0]
			switch {
			case reader.isTableCall(node.Value):
				table := reader.readTable(node.Value)
				if table != nil {
					reader.tables[name.Value] = table
					reader.order = append(reader.order, name.Value)
				}
			case reader.isDeclaration(node.Value):
				database := reader.readDeclaration(name, node.Value)
				if database == nil {
					continue
				}
				if !exported[name.Value] {
					reader.fail(name.Token, fmt.Sprintf("database %s khai mà không export thì không file nào dùng được: viết export const %s = connection()…", name.Value, name.Value))
					continue
				}
				databases[name.Value] = database
			default:
				reader.fail(name.Token, fmt.Sprintf("file trong .shared/schema/ chỉ khai table và connection(): %s không phải table({ … }) hay connection()….tables({ … }); hàm và hằng dùng chung đặt trong .shared/", name.Value))
			}
		default:
			reader.fail(statementToken(node), "file trong .shared/schema/ chỉ khai table và connection(), không chạy gì khi nạp")
		}
	}
	if reader.err != nil {
		return nil, reader.err
	}
	return databases, nil
}

// kitworkWord is a name a schema file imports from Kitwork: the module (schema, database; "" for
// "kitwork") and the name it imports there ("" for a namespace or a default import of the module).
type kitworkWord struct {
	module   string
	imported string
}

type schemaReader struct {
	place  func(Token) string
	words  map[string]kitworkWord
	tables map[string]*statement.Table
	order  []string
	err    error
}

func (reader *schemaReader) fail(token Token, message string) {
	if reader.err == nil {
		reader.err = fmt.Errorf("%s: %s", reader.place(token), message)
	}
}

// kitworkImport records an import of Kitwork as the parser lowered it (const { a } = kitwork().schema;
// const a = kitwork().schema.b; const schema = kitwork().schema), and reports whether the statement is one.
func (reader *schemaReader) kitworkImport(node Statement) bool {
	switch node := node.(type) {
	case *GroupStatement:
		for _, inner := range node.Statements {
			if !reader.kitworkImport(inner) {
				return false
			}
		}
		return len(node.Statements) > 0
	case *VarStatement:
		if node.Pattern != nil {
			return false
		}
		if module, ok := kitworkModulePath(node.Value); ok {
			for _, name := range node.Names {
				imported := ""
				if node.DestructMode == DestructObject {
					imported = name.Value
				}
				reader.removedImport(name.Token, module, imported)
				reader.words[name.Value] = kitworkWord{module: module, imported: imported}
			}
			return true
		}
		if member, ok := node.Value.(*MemberExpression); ok && node.DestructMode == DestructNone && len(node.Names) == 1 {
			if module, ok := kitworkModulePath(member.Object); ok && member.Property != nil {
				reader.removedImport(member.Property.Token, module, member.Property.Value)
				reader.words[node.Names[0].Value] = kitworkWord{module: module, imported: member.Property.Value}
				return true
			}
		}
	}
	return false
}

// removedImport refuses an older word of the database module imported into a schema file (import { kitdb }
// from "kitwork/database"), with what the spec writes instead: the file would otherwise fail later, at the
// line that calls it, saying only that it declares neither table nor connection.
func (reader *schemaReader) removedImport(token Token, module, imported string) {
	if refused := removedImportMessage(module, imported); refused != "" {
		reader.fail(token, refused)
	}
}

// kitworkModulePath reports e as kitwork() ("") or kitwork().<sub> (sub).
func kitworkModulePath(e Expression) (string, bool) {
	if isKitworkCall(e) {
		return "", true
	}
	member, ok := e.(*MemberExpression)
	if !ok || member.Optional || member.Property == nil || !isKitworkCall(member.Object) {
		return "", false
	}
	return member.Property.Value, true
}

func isKitworkCall(e Expression) bool {
	call, ok := e.(*CallExpression)
	if !ok || len(call.Arguments) != 0 {
		return false
	}
	callee, ok := call.Function.(*Identifier)
	return ok && callee.Value == "kitwork"
}

// callee is the word a call names: table(…) by an import of table, schema.table(…) by a namespace.
// token is where the name is written; ok is false when the call names no word of Kitwork.
func (reader *schemaReader) callee(e Expression) (word kitworkWord, token Token, arguments []Expression, ok bool) {
	switch node := e.(type) {
	case *CallExpression:
		name, isName := node.Function.(*Identifier)
		if !isName || node.Optional {
			return kitworkWord{}, Token{}, nil, false
		}
		word, found := reader.words[name.Value]
		if !found || word.imported == "" {
			return kitworkWord{}, name.Token, node.Arguments, false
		}
		return word, name.Token, node.Arguments, true
	case *MethodCallExpression:
		namespace, isName := node.Object.(*Identifier)
		if !isName || node.Optional || node.OptionalCall {
			return kitworkWord{}, Token{}, nil, false
		}
		word, found := reader.words[namespace.Value]
		if !found || word.imported != "" {
			return kitworkWord{}, Token{}, nil, false
		}
		return kitworkWord{module: word.module, imported: node.Method.Value}, node.Method.Token, node.Arguments, true
	}
	return kitworkWord{}, Token{}, nil, false
}

// isTableCall reports a call of table, or of struct, the word it replaced, which readTable refuses saying so.
func (reader *schemaReader) isTableCall(e Expression) bool {
	word, _, _, ok := reader.callee(e)
	return ok && (word.imported == "table" || word.imported == "struct")
}

// isDeclaration reports a chain that starts at connection() or connect() of kitwork/database.
func (reader *schemaReader) isDeclaration(e Expression) bool {
	for {
		if word, _, _, ok := reader.callee(e); ok && word.module == "database" && (word.imported == "connection" || word.imported == "connect") {
			return true
		}
		call, ok := e.(*MethodCallExpression)
		if !ok {
			return false
		}
		e = call.Object
	}
}

// readTable reads table({ cột }) into a table without a name: the key in tables({ … }) names it.
func (reader *schemaReader) readTable(e Expression) *statement.Table {
	word, token, arguments, _ := reader.callee(e)
	if word.imported == "struct" {
		reader.fail(token, statement.TableRenamed)
		return nil
	}
	if word.module != "schema" {
		reader.fail(token, `table import từ "kitwork/schema": import { table, id, text } from "kitwork/schema"`)
		return nil
	}
	if len(arguments) != 1 {
		reader.fail(token, "table nhận một object cột: table({ id: id(), name: text() })")
		return nil
	}
	fields, ok := arguments[0].(*ObjectLiteral)
	if !ok {
		reader.fail(token, "table nhận một object cột viết ngay trong lời gọi: table({ id: id(), name: text() })")
		return nil
	}
	table := &statement.Table{}
	seen := map[string]bool{}
	primary := 0
	for _, entry := range fields.Entries {
		if entry.IsSpread {
			reader.fail(fields.Token, "table({ ...x }) chưa có trong engine hôm nay: viết từng cột")
			return nil
		}
		name, nameToken, ok := objectKeyName(entry.Key)
		if !ok {
			reader.fail(fields.Token, "tên cột viết bằng chữ: table({ email: text() })")
			return nil
		}
		if seen[name] {
			reader.fail(nameToken, fmt.Sprintf("cột %s khai hai lần", name))
			return nil
		}
		seen[name] = true
		column, ok := reader.readColumn(name, entry.Value)
		if !ok {
			return nil
		}
		if column.Primary {
			primary++
			if primary > 1 {
				reader.fail(nameToken, fmt.Sprintf("bảng có hai id(): %s là id() thứ hai", name))
				return nil
			}
		}
		table.Columns = append(table.Columns, column)
	}
	if len(table.Columns) == 0 {
		reader.fail(token, "table cần ít nhất một cột")
		return nil
	}
	if primary == 0 {
		reader.fail(token, "bảng không có id() chưa có trong engine hôm nay: thêm id: id()")
		return nil
	}
	if message := statement.SearchReserved(table); message != "" {
		reader.fail(token, message)
		return nil
	}
	return table
}

// readColumn reads one column: a builder of kitwork/schema, then its modifiers (text().nullable()).
func (reader *schemaReader) readColumn(name string, e Expression) (statement.Column, bool) {
	column := statement.Column{Name: name}
	var modifiers []*MethodCallExpression
	base := e
	for {
		call, ok := base.(*MethodCallExpression)
		if !ok {
			break
		}
		if namespace, isName := call.Object.(*Identifier); isName {
			if word, found := reader.words[namespace.Value]; found && word.imported == "" {
				break // schema.text(): the builder itself
			}
		}
		modifiers = append(modifiers, call)
		base = call.Object
	}
	if member, ok := base.(*MemberExpression); ok {
		reader.fail(member.Token, fmt.Sprintf("khoá ngoại %s chưa có trong engine hôm nay (lát 5)", member.String()))
		return column, false
	}
	word, token, arguments, ok := reader.callee(base)
	if !ok {
		at := token
		if name, isName := base.(*Identifier); isName {
			at = name.Token
			if word, found := reader.words[name.Value]; found && word.module == "schema" {
				reader.fail(at, fmt.Sprintf("cột %s: viết %s()", column.Name, name.Value))
				return column, false
			}
		}
		if at.Source == "" {
			at = expressionToken(base)
		}
		reader.fail(at, fmt.Sprintf("cột %s viết bằng một kiểu của kitwork/schema: id(), text(), integer(), float(), decimal(12, 2), boolean(), date(), json(), timestamp()", column.Name))
		return column, false
	}
	builder, known := statement.Builders[word.imported]
	switch {
	case known && word.module == "schema":
	case word.module == "schema" && statement.BuildersNotYet[word.imported] != 0:
		reader.fail(token, fmt.Sprintf("%s() chưa có trong engine hôm nay (lát %d)", word.imported, statement.BuildersNotYet[word.imported]))
		return column, false
	case word.imported == "number":
		reader.fail(token, statement.NumberBuilder)
		return column, false
	case statement.LegacyBuilders[word.imported] != "":
		reader.fail(token, fmt.Sprintf("%s() là cách viết cũ: %s", word.imported, statement.LegacyBuilders[word.imported]))
		return column, false
	case known:
		reader.fail(token, fmt.Sprintf(`%s() import từ "kitwork/schema"`, word.imported))
		return column, false
	default:
		reader.fail(token, fmt.Sprintf("%s() không phải kiểu cột của kitwork/schema: id(), text(), integer(), float(), decimal(12, 2), boolean(), date(), json(), timestamp()", word.imported))
		return column, false
	}
	column.Kind = builder.Kind
	column.Primary = builder.Primary
	column.Generated = builder.Primary
	if builder.Parameters > 0 {
		numbers := make([]int, 0, len(arguments))
		for _, argument := range arguments {
			literal, isLiteral := argument.(*Literal)
			if !isLiteral || literal.Value.K != value.Number || literal.Value.N != float64(int(literal.Value.N)) || literal.Value.N < 0 {
				break
			}
			numbers = append(numbers, int(literal.Value.N))
		}
		if len(arguments) != builder.Parameters || len(numbers) != builder.Parameters || numbers[0] < 1 || numbers[1] > numbers[0] {
			reader.fail(token, fmt.Sprintf("%s nhận độ dài và số lẻ là hai số nguyên: %s(12, 2)", word.imported, word.imported))
			return column, false
		}
		column.Precision, column.Scale = numbers[0], numbers[1]
	} else if len(arguments) != 0 {
		reader.fail(token, fmt.Sprintf("%s() không nhận đối số", word.imported))
		return column, false
	}
	for index := len(modifiers) - 1; index >= 0; index-- {
		modifier := modifiers[index]
		method := modifier.Method.Value
		at := modifier.Method.Token
		if spelling := statement.LegacyModifiers[method]; spelling != "" {
			reader.fail(at, fmt.Sprintf(".%s() là cách viết cũ: %s", method, spelling))
			return column, false
		}
		rule, known := statement.Modifiers[method]
		if !known {
			reader.fail(at, fmt.Sprintf("%s().%s không có; bổ nghĩa cột: nullable, unique, default, searchable, index, created, updated", word.imported, method))
			return column, false
		}
		if rule.OnlyTimestamp && !builder.Timestamp {
			reader.fail(at, fmt.Sprintf(".%s() chỉ đi sau timestamp(): %s: timestamp().%s()", method, column.Name, method))
			return column, false
		}
		switch method {
		case "nullable":
			column.Nullable = true
		case "unique":
			// .unique() alone; .unique("tên") a group of columns unique together (the upsert's target)
			group := ""
			if len(modifier.Arguments) > 0 {
				if literal, ok := modifier.Arguments[0].(*Literal); ok && literal.Value.K == value.String {
					group = literal.Value.Text()
				}
			}
			if group == "" {
				column.Unique = true
			} else {
				column.UniqueGroup = group
			}
		case "searchable":
			if len(modifier.Arguments) != 0 {
				reader.fail(at, ".searchable() không nhận đối số")
				return column, false
			}
			column.Search = true
		case "default":
			if len(modifier.Arguments) != 1 {
				reader.fail(at, ".default nhận một giá trị: .default(\"moi\")")
				return column, false
			}
			column.HasDefault = true
		case "index":
			for _, argument := range modifier.Arguments {
				if _, isFunction := argument.(*FunctionLiteral); isFunction {
					reader.fail(at, "chỉ mục một phần .index(\"tên\", (bảng) => …) chưa có trong engine hôm nay")
					return column, false
				}
			}
		case "created", "updated":
			if len(modifier.Arguments) != 0 {
				reader.fail(at, fmt.Sprintf(".%s() không nhận đối số", method))
				return column, false
			}
			column.WriteProtected = true
		}
	}
	if column.Primary && (column.Nullable || column.HasDefault) {
		reader.fail(token, fmt.Sprintf("cột %s là id(): không nullable, không default", column.Name))
		return column, false
	}
	return column, true
}

// readDeclaration reads connection()….tables({ bảng }), with .schema("tên") picking the schema: the tables, named
// by their keys.
func (reader *schemaReader) readDeclaration(name *Identifier, e Expression) *statement.Database {
	var links []*MethodCallExpression
	base := e
	for {
		if _, _, _, ok := reader.callee(base); ok {
			break
		}
		call := base.(*MethodCallExpression)
		links = append([]*MethodCallExpression{call}, links...)
		base = call.Object
	}
	word, token, arguments, _ := reader.callee(base)
	if word.imported == "connect" {
		return reader.readConnect(name, token, arguments, links)
	}
	connectionName := ""
	switch len(arguments) {
	case 0:
	case 1:
		literal, isLiteral := arguments[0].(*Literal)
		if !isLiteral || literal.Value.K != value.String || strings.TrimSpace(literal.Value.Text()) == "" {
			reader.fail(token, `connection nhận tên database viết bằng chuỗi: connection("warehouse"), tên có trong app.databases({ … }) của application.work`)
			return nil
		}
		connectionName = literal.Value.Text()
		if reserved := statement.ReservedConnection(connectionName); reserved != "" {
			reader.fail(literal.Token, reserved)
			return nil
		}
	default:
		reader.fail(token, `connection nhận một tên: connection() là database của app, connection("warehouse") là tên host khai trong app.databases({ … })`)
		return nil
	}
	last := len(links) - 1
	if last < 0 || links[last].Method.Value != "tables" {
		if last >= 0 && links[last].Method.Value == "schema" && len(links[last].Arguments) == 1 {
			if _, isObject := links[last].Arguments[0].(*ObjectLiteral); isObject {
				reader.fail(links[last].Method.Token, statement.TablesRenamed)
				return nil
			}
		}
		reader.fail(name.Token, fmt.Sprintf("câu chưa kết thúc: %s = connection()….tables({ bảng }), tables là hành động đứng cuối", name.Value))
		return nil
	}
	schemaName := ""
	for index, link := range links[:last] {
		switch link.Method.Value {
		case "schema":
			schemaName = reader.readSchemaName(link, index, connectionName)
			if schemaName == "" {
				return nil
			}
			continue
		case "policy", "before", "after":
			reader.fail(link.Method.Token, fmt.Sprintf(".%s(…) chưa có trong engine hôm nay (lát 6)", link.Method.Value))
		case "tables":
			reader.fail(link.Method.Token, "tables là hành động, đứng cuối và một lần")
		default:
			reader.fail(link.Method.Token, fmt.Sprintf(`connection().%s không có; khai: connection() [.schema("tên")] [.policy]* [.before]* [.after]* .tables({ bảng })`, link.Method.Value))
		}
		return nil
	}
	schema := links[last]
	if len(schema.Arguments) != 1 {
		reader.fail(schema.Method.Token, "tables nhận một object bảng: .tables({ orders })")
		return nil
	}
	tables, ok := schema.Arguments[0].(*ObjectLiteral)
	if !ok {
		reader.fail(schema.Method.Token, "tables nhận một object bảng viết ngay trong lời gọi: .tables({ orders })")
		return nil
	}
	database := &statement.Database{Export: name.Value, Connection: connectionName, Schema: schemaName}
	seen := map[string]string{}
	used := map[string]string{}
	for _, entry := range tables.Entries {
		if entry.IsSpread {
			reader.fail(tables.Token, "schema({ ...x }) không có: viết từng bảng")
			return nil
		}
		tableName, at, ok := objectKeyName(entry.Key)
		if !ok {
			reader.fail(tables.Token, "tên bảng viết bằng chữ: .schema({ orders })")
			return nil
		}
		switch {
		case tableName == "session":
			reader.fail(at, "session là tên dành riêng của phiên (tham số của policy), không làm tên bảng: bảng lưu phiên là sessions")
			return nil
		case statement.EngineTable(tableName):
			reader.fail(at, fmt.Sprintf("%s: tiền tố kitwork_ hay _kitwork_ (viết hoa hay thường) là của bảng engine", tableName))
			return nil
		case seen[strings.ToLower(tableName)] == tableName:
			reader.fail(at, fmt.Sprintf("bảng %s khai hai lần", tableName))
			return nil
		case seen[strings.ToLower(tableName)] != "":
			reader.fail(at, fmt.Sprintf("bảng %s khai hai lần: %s là cùng tên, tên bảng không phân biệt chữ hoa, thường", tableName, seen[strings.ToLower(tableName)]))
			return nil
		}
		seen[strings.ToLower(tableName)] = tableName
		var table *statement.Table
		switch value := entry.Value.(type) {
		case *Identifier:
			table = reader.tables[value.Value]
			if table == nil {
				reader.fail(value.Token, fmt.Sprintf("%s không phải bảng khai trong file này: const %s = table({ … }) trước tables", value.Value, value.Value))
				return nil
			}
			if first, twice := used[value.Value]; twice {
				reader.fail(value.Token, fmt.Sprintf("table %s đã là bảng %s: một table một bảng", value.Value, first))
				return nil
			}
			used[value.Value] = tableName
		default:
			if !reader.isTableCall(entry.Value) {
				reader.fail(at, fmt.Sprintf("bảng %s là một table: .tables({ %s }) với const %s = table({ … })", tableName, tableName, tableName))
				return nil
			}
			table = reader.readTable(entry.Value)
			if table == nil {
				return nil
			}
		}
		named := *table
		named.Name = tableName
		named.Columns = append([]statement.Column(nil), table.Columns...)
		database.Tables = append(database.Tables, named)
	}
	if len(database.Tables) == 0 {
		reader.fail(schema.Method.Token, "schema cần ít nhất một bảng")
		return nil
	}
	return database
}

// readSchemaName reads .schema("billing"), which picks the schema the tables go in: right after connection(…),
// once, a name written in place so the compiler knows the file without running anything. "" when it failed.
func (reader *schemaReader) readSchemaName(link *MethodCallExpression, index int, connectionName string) string {
	if index != 0 {
		reader.fail(link.Method.Token, `.schema("tên") đứng ngay sau connection(…), trước policy, before, after`)
		return ""
	}
	if len(link.Arguments) == 1 {
		if _, isObject := link.Arguments[0].(*ObjectLiteral); isObject {
			reader.fail(link.Method.Token, statement.TablesRenamed)
			return ""
		}
	}
	var literal *Literal
	if len(link.Arguments) == 1 {
		literal, _ = link.Arguments[0].(*Literal)
	}
	if literal == nil || literal.Value.K != value.String {
		reader.fail(link.Method.Token, statement.SchemaName(""))
		return ""
	}
	name := literal.Value.Text()
	if refused := statement.SchemaName(name); refused != "" {
		reader.fail(literal.Token, refused)
		return ""
	}
	if connectionName != "" {
		reader.fail(link.Method.Token, fmt.Sprintf(`connection(%q).schema(…) chưa có trong engine hôm nay: schema chạy trên database của app, connection()`, connectionName))
		return ""
	}
	return name
}

// readConnect reads connect({ postgresql: … }): a database outside the app, which has no schema, policy,
// before or after (it is someone else's) and answers only query(). The value of the connector is code the
// file runs when it loads (environment.CRM_URL), never read here, so nothing of the connection is in the
// compiled program.
func (reader *schemaReader) readConnect(name *Identifier, token Token, arguments []Expression, links []*MethodCallExpression) *statement.Database {
	if len(links) != 0 {
		link := links[0]
		reader.fail(link.Method.Token, fmt.Sprintf("connect(…) không có .%s: database ngoài là của người khác, chỉ có %s.query(sql, [giá trị])", link.Method.Value, name.Value))
		return nil
	}
	shape := "connect nhận một object có một bộ nối: connect({ postgresql: environment.CRM_URL }), bộ nối là postgresql hay mysql"
	if len(arguments) != 1 {
		reader.fail(token, shape)
		return nil
	}
	source, ok := arguments[0].(*ObjectLiteral)
	if !ok || len(source.Entries) != 1 || source.Entries[0].IsSpread {
		reader.fail(token, shape)
		return nil
	}
	connector, at, ok := objectKeyName(source.Entries[0].Key)
	switch {
	case !ok:
		reader.fail(token, shape)
		return nil
	case connector == "sqlite":
		reader.fail(at, `connect({ sqlite: … }) không có: connect nối ra database ngoài (postgresql, mysql); SQLite là connection("tên") với { sqlite: đường_dẫn } trong app.databases({ … }) của application.work`)
		return nil
	case !statement.Connectors[connector]:
		reader.fail(at, fmt.Sprintf("connect({ %s: … }): %s", connector, shape))
		return nil
	}
	return &statement.Database{Export: name.Value, External: connector}
}

// objectKeyName is the name of an object's key written as a name or a string.
func objectKeyName(key Expression) (string, Token, bool) {
	switch key := key.(type) {
	case *Identifier:
		return key.Value, key.Token, true
	case *Literal:
		if key.Value.K == value.String {
			return key.Value.Text(), key.Token, true
		}
	}
	return "", Token{}, false
}

// expressionToken is a token of where an expression starts.
func expressionToken(e Expression) Token {
	return Token{Source: getNodeSource(e), Position: startPosition(e)}
}

// statementToken is a token of where a statement starts.
func statementToken(s Statement) Token {
	return Token{Source: getNodeSource(s), Position: getNodePosition(s)}
}

// sortedExports lists a module's exports in order, for an error that lists them.
func sortedExports(exports []string) string {
	names := append([]string(nil), exports...)
	sort.Strings(names)
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		if name == DefaultExportName {
			continue
		}
		quoted = append(quoted, name)
	}
	if len(quoted) == 0 {
		return "(không có gì)"
	}
	return strings.Join(quoted, ", ")
}
