package compiler

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kitwork/engine/database/statement"
	"github.com/kitwork/engine/value"
)

// readSchemaModule reads a file of .shared/schema/ (KITWORK_DATABASE.md mục 1): its imports of Kitwork,
// its tables (const orders = struct({ … })) and its databases (export const db = connection().schema({
// orders })). It reads the syntax and never guesses: a statement it cannot read is a compile error at that
// statement, so what the compiler checks a statement against is what the file declares. The engine checks
// again against the live schema when the statement first runs.
func readSchemaModule(program *Program, place func(Token) string) (map[string]*statement.Database, error) {
	reader := &schemaReader{place: place, words: map[string]kitworkWord{}, structs: map[string]*statement.Table{}}
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
			reader.fail(node.Token, fmt.Sprintf("import %q trong .shared/schema/ chưa có trong engine hôm nay (lát 5: struct từ file khác; lát 6: queue cho after()); khai bảng ngay trong file này", node.Source))
		case *VarStatement:
			if len(node.Names) != 1 || node.DestructMode != DestructNone || node.Token.Kind != Const {
				reader.fail(node.Token, "file trong .shared/schema/ chỉ khai struct và connection(): mỗi dòng là const tên = struct({ … }) hay export const db = connection().schema({ … })")
				continue
			}
			name := node.Names[0]
			switch {
			case reader.isStructCall(node.Value):
				table := reader.readStruct(node.Value)
				if table != nil {
					reader.structs[name.Value] = table
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
				reader.fail(name.Token, fmt.Sprintf("file trong .shared/schema/ chỉ khai struct và connection(): %s không phải struct({ … }) hay connection()….schema({ … }); hàm và hằng dùng chung đặt trong .shared/", name.Value))
			}
		default:
			reader.fail(statementToken(node), "file trong .shared/schema/ chỉ khai struct và connection(), không chạy gì khi nạp")
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
	place   func(Token) string
	words   map[string]kitworkWord
	structs map[string]*statement.Table
	order   []string
	err     error
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
				reader.words[name.Value] = kitworkWord{module: module, imported: imported}
			}
			return true
		}
		if member, ok := node.Value.(*MemberExpression); ok && node.DestructMode == DestructNone && len(node.Names) == 1 {
			if module, ok := kitworkModulePath(member.Object); ok && member.Property != nil {
				reader.words[node.Names[0].Value] = kitworkWord{module: module, imported: member.Property.Value}
				return true
			}
		}
	}
	return false
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

// callee is the word a call names: struct(…) by an import of struct, schema.struct(…) by a namespace.
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

func (reader *schemaReader) isStructCall(e Expression) bool {
	word, _, _, ok := reader.callee(e)
	return ok && word.imported == "struct"
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

// readStruct reads struct({ cột }) into a table without a name: the key in schema({ … }) names it.
func (reader *schemaReader) readStruct(e Expression) *statement.Table {
	word, token, arguments, _ := reader.callee(e)
	if word.module != "schema" {
		reader.fail(token, `struct import từ "kitwork/schema": import { struct, id, text } from "kitwork/schema"`)
		return nil
	}
	if len(arguments) != 1 {
		reader.fail(token, "struct nhận một object cột: struct({ id: id(), name: text() })")
		return nil
	}
	fields, ok := arguments[0].(*ObjectLiteral)
	if !ok {
		reader.fail(token, "struct nhận một object cột viết ngay trong lời gọi: struct({ id: id(), name: text() })")
		return nil
	}
	table := &statement.Table{}
	seen := map[string]bool{}
	primary := 0
	for _, entry := range fields.Entries {
		if entry.IsSpread {
			reader.fail(fields.Token, "struct({ ...x }) chưa có trong engine hôm nay: viết từng cột")
			return nil
		}
		name, nameToken, ok := objectKeyName(entry.Key)
		if !ok {
			reader.fail(fields.Token, "tên cột viết bằng chữ: struct({ email: text() })")
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
		reader.fail(token, "struct cần ít nhất một cột")
		return nil
	}
	if primary == 0 {
		reader.fail(token, "bảng không có id() chưa có trong engine hôm nay: thêm id: id()")
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
			reader.fail(at, fmt.Sprintf("%s().%s không có; bổ nghĩa cột: nullable, unique, default, search, index, created, updated", word.imported, method))
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
			column.Unique = true
		case "search":
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

// readDeclaration reads connection()….schema({ bảng }): the tables, named by their keys.
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
		reader.fail(token, "connect(…) chưa có trong engine hôm nay (lát 8); database của app là connection().schema({ … })")
		return nil
	}
	if len(arguments) != 0 {
		reader.fail(token, "connection(\"tên\") chưa có trong engine hôm nay (lát 8, cùng app.databases của host); database của app là connection() không tên")
		return nil
	}
	if len(links) == 0 || links[len(links)-1].Method.Value != "schema" {
		reader.fail(name.Token, fmt.Sprintf("câu chưa kết thúc: %s = connection()….schema({ bảng }), schema là hành động đứng cuối", name.Value))
		return nil
	}
	for _, link := range links[:len(links)-1] {
		switch link.Method.Value {
		case "policy", "before", "after":
			reader.fail(link.Method.Token, fmt.Sprintf(".%s(…) chưa có trong engine hôm nay (lát 6)", link.Method.Value))
		case "schema":
			reader.fail(link.Method.Token, "schema là hành động, đứng cuối và một lần")
		default:
			reader.fail(link.Method.Token, fmt.Sprintf("connection().%s không có; khai: connection() [.policy]* [.before]* [.after]* .schema({ bảng })", link.Method.Value))
		}
		return nil
	}
	schema := links[len(links)-1]
	if len(schema.Arguments) != 1 {
		reader.fail(schema.Method.Token, "schema nhận một object bảng: .schema({ orders })")
		return nil
	}
	tables, ok := schema.Arguments[0].(*ObjectLiteral)
	if !ok {
		reader.fail(schema.Method.Token, "schema nhận một object bảng viết ngay trong lời gọi: .schema({ orders })")
		return nil
	}
	database := &statement.Database{Export: name.Value}
	seen := map[string]bool{}
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
		case strings.HasPrefix(tableName, "kitwork_"):
			reader.fail(at, fmt.Sprintf("%s: tiền tố kitwork_ là của bảng engine", tableName))
			return nil
		case seen[tableName]:
			reader.fail(at, fmt.Sprintf("bảng %s khai hai lần", tableName))
			return nil
		}
		seen[tableName] = true
		var table *statement.Table
		switch value := entry.Value.(type) {
		case *Identifier:
			table = reader.structs[value.Value]
			if table == nil {
				reader.fail(value.Token, fmt.Sprintf("%s không phải struct khai trong file này: const %s = struct({ … }) trước schema", value.Value, value.Value))
				return nil
			}
			if first, twice := used[value.Value]; twice {
				reader.fail(value.Token, fmt.Sprintf("struct %s đã là bảng %s: một struct một bảng", value.Value, first))
				return nil
			}
			used[value.Value] = tableName
		default:
			if !reader.isStructCall(entry.Value) {
				reader.fail(at, fmt.Sprintf("bảng %s là một struct: .schema({ %s }) với const %s = struct({ … })", tableName, tableName, tableName))
				return nil
			}
			table = reader.readStruct(entry.Value)
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
