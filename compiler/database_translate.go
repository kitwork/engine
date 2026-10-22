package compiler

import (
	"fmt"
	"strings"

	"github.com/kitwork/engine/database/statement"
	"github.com/kitwork/engine/value"
)

// Database statements are translated when they compile (KITWORK_DATABASE.md mục 6): a chain written on a
// handle imported from .shared/schema/,
//
//	db.where((orders) => orders.email === email).orderBy((orders) => desc(orders.amount)).list(10)
//
// becomes one call of the handle, LOAD db; PUSH "<template>"; LOAD email; PUSH "#statement"; INVOKE 2.
// The template (database/statement) holds the table, the condition as a tree, the order and the bounds;
// what the lambdas read from outside them (email) is a hole, a value computed in place, before the
// statement, in the code around it. The lambdas are never compiled, so they never run and make no
// closure: a condition keeps both sides of && and ||, which a lambda run on a proxy cannot (a proxy is
// always true). Every table and column is checked against the schema file's struct({ … }) where the
// statement is written; the engine checks again against the live schema when it first runs.
//
// Only a program that imports a database is touched: every other program resolves and compiles as it
// did before them.

// declarationSite refuses kitwork/schema, and connection() or connect() of kitwork/database, outside the
// app's .shared/schema/ (KITWORK_DATABASE.md mục 1): an app's databases are declared in one folder. It
// runs for every program, and only ever fails, so a program that imports neither keeps its bytes. The
// module's own name counts as the module: import { schema } from "kitwork" is refused as kitwork/schema
// is, and const { connection } = database, database a name of kitwork/database, as database.connection.
func (r *resolver) declarationSite(node *VarStatement) {
	if r.inSchemaModule {
		return
	}
	if node.Pattern != nil {
		// const { connection } = database, database a name of kitwork/database
		if namespace, isName := node.Value.(*Identifier); isName && !node.Pattern.Array {
			for _, element := range node.Pattern.Elements {
				if r.namespaceDeclaration(namespace, &Identifier{Token: element.KeyToken, Value: element.Key}) {
					return
				}
			}
		}
		return
	}
	module, ok := kitworkModulePath(node.Value)
	var names []string
	if ok {
		if node.DestructMode == DestructObject {
			for _, name := range node.Names {
				names = append(names, name.Value)
			}
		}
	} else if member, isMember := node.Value.(*MemberExpression); isMember && member.Property != nil {
		module, ok = kitworkModulePath(member.Object)
		names = []string{member.Property.Value}
	}
	if !ok {
		return
	}
	refused := ""
	switch module {
	case "schema":
		refused = `import từ "kitwork/schema"`
	case "database":
		for _, name := range names {
			if name == "connection" || name == "connect" {
				refused = fmt.Sprintf(`import { %s } from "kitwork/database"`, name)
			}
		}
	case "":
		// the module kitwork: its schema is kitwork/schema
		for _, name := range names {
			if name == "schema" {
				refused = `import { schema } from "kitwork"`
			}
		}
	}
	if refused != "" {
		r.fail(fmt.Errorf("%s: %s chỉ có trong .shared/schema/ của app: struct(), connection(), connect() chỉ khai ở đó (KITWORK_DATABASE.md mục 1); file khác dùng database bằng import { db } from \"app/.shared/schema/<tên>.work\"",
			r.where(node.Token), refused))
	}
}

// databaseModule reports a const bound to kitwork/database itself: import * as database from
// "kitwork/database" (const database = kitwork().database), import { database } from "kitwork".
func databaseModule(node *VarStatement, name *Identifier) bool {
	if node.Pattern != nil {
		return false
	}
	module, ok := kitworkModulePath(node.Value)
	switch {
	case !ok:
		return false
	case node.DestructMode == DestructNone:
		return module == "database"
	case node.DestructMode == DestructObject:
		return module == "" && name.Value == "database"
	}
	return false
}

// namespaceDeclaration refuses connection or connect read off kitwork/database by its name outside the
// app's .shared/schema/ (database.connection()), as declarationSite refuses their import. It runs for every
// program and only ever fails.
func (r *resolver) namespaceDeclaration(object Expression, property *Identifier) bool {
	if r.inSchemaModule || property == nil || (property.Value != "connection" && property.Value != "connect") {
		return false
	}
	namespace, ok := object.(*Identifier)
	if !ok {
		return false
	}
	if found := r.lookup(namespace.Value); found == nil || !found.databaseModule {
		return false
	}
	r.fail(fmt.Errorf("%s: %s.%s chỉ có trong .shared/schema/ của app: struct(), connection(), connect() chỉ khai ở đó (KITWORK_DATABASE.md mục 1); file khác dùng database bằng import { db } from \"app/.shared/schema/<tên>.work\"",
		r.where(property.Token), namespace.Value, property.Value))
	return true
}

// databaseAlias reports a const that names a handle again (const d = db): d is a handle too. Its value is
// the one place a handle is read as a value.
func (r *resolver) databaseAlias(node *VarStatement) *DatabaseImport {
	if !r.databases || node.Token.Kind != Const || node.DestructMode != DestructNone || node.Pattern != nil || len(node.Names) != 1 {
		return nil
	}
	name, ok := node.Value.(*Identifier)
	if !ok {
		return nil
	}
	found := r.lookup(name.Value)
	if found == nil || found.database == nil {
		return nil
	}
	r.aliases[name] = true
	return found.database
}

// markDatabase marks a declared variable as a handle (an import of a database, or an alias of one) or as
// a lambda word of kitwork/database (desc, one).
func (r *resolver) markDatabase(node *VarStatement, name *Identifier, declared *binding, alias *DatabaseImport) {
	if declared == nil {
		return
	}
	declared.databaseModule = databaseModule(node, name)
	if r.bundle == nil {
		return
	}
	if imported := r.bundle.Imports[name]; imported != nil {
		declared.database = imported
	}
	if alias != nil {
		declared.database = alias
	}
	if !r.databases || node.Pattern != nil {
		return
	}
	if module, ok := kitworkModulePath(node.Value); ok && module == "database" && node.DestructMode == DestructObject {
		if statement.LambdaWords[name.Value] {
			declared.word = name.Value
		}
		return
	}
	if member, ok := node.Value.(*MemberExpression); ok && member.Property != nil && node.DestructMode == DestructNone {
		if module, ok := kitworkModulePath(member.Object); ok && module == "database" && statement.LambdaWords[member.Property.Value] {
			declared.word = member.Property.Value
		}
	}
}

// databaseReference refuses a handle read as a value, and a lambda word used outside a lambda. A handle
// is only the root of a statement: a statement is translated where it is written, on the name imported
// from the schema file, so a handle passed to a function, put in an object or a let, or returned would
// reach code where nothing is translated.
func (r *resolver) databaseReference(name *Identifier, found *binding) bool {
	if found.database != nil && !r.handleUse && !r.aliases[name] {
		r.fail(fmt.Errorf("%s: %s là database khai ở %s: chỉ dùng làm gốc câu (%s.list((bảng) => …)); truyền %s qua hàm, gán vào object, mảng hay let, hay trả về chưa có",
			r.where(name.Token), name.Value, found.database.Module, name.Value, name.Value))
		return false
	}
	if found.word != "" {
		r.fail(fmt.Errorf("%s: %s chỉ có nghĩa trong lambda của câu database (KITWORK_DATABASE.md mục 2)", r.where(name.Token), name.Value))
		return false
	}
	return true
}

// handleMember refuses a property or an item of a handle: db.orders, db["orders"].
func (r *resolver) handleMember(object Expression, token Token, property *Identifier) bool {
	root, ok := object.(*Identifier)
	if !ok {
		return false
	}
	found := r.lookup(root.Value)
	if found == nil || found.database == nil {
		return false
	}
	what := root.Value + "[…]"
	if property != nil {
		what = root.Value + "." + property.Value
		token = property.Token
	}
	r.fail(fmt.Errorf("%s: %s không có: câu viết %s.list((bảng) => …), bảng là tham số của lambda", r.where(token), what, root.Value))
	return true
}

// databaseStatement translates node when it is the action of a statement on a handle, and refuses a
// chain on a handle the grammar does not have. It reports whether it took node: false leaves node to the
// resolver, as for any call (a method of the statement's result: db.list(…).map(…)).
func (r *resolver) databaseStatement(node *MethodCallExpression) bool {
	var links []*MethodCallExpression
	var e Expression = node
	for {
		call, ok := e.(*MethodCallExpression)
		if !ok {
			break
		}
		links = append([]*MethodCallExpression{call}, links...)
		e = call.Object
	}
	root, ok := e.(*Identifier)
	if !ok {
		return false
	}
	found := r.lookup(root.Value)
	if found == nil || found.database == nil {
		return false
	}
	for index, link := range links {
		word := link.Method.Value
		at := r.where(link.Method.Token)
		if link.Optional || link.OptionalCall {
			r.fail(fmt.Errorf("%s: ?. không có trên câu database: %s luôn có, viết %s.%s(…)", at, root.Value, root.Value, word))
			return true
		}
		switch {
		case statement.Descriptions[word]:
			if index == len(links)-1 {
				r.fail(fmt.Errorf("%s: câu chưa kết thúc: cần list, first, count hay insert ở cuối; chỉ hành động mới chạy câu", at))
				return true
			}
		case statement.Actions[word]:
			if index == len(links)-1 {
				r.translateStatement(root, found.database, links)
				return true
			}
			next := links[index+1]
			if statement.AfterAction(next.Method.Value) {
				r.fail(fmt.Errorf("%s: sau hành động không nối gì: %s(…) đã chạy câu; .%s(…) viết trước hành động", r.where(next.Method.Token), word, next.Method.Value))
				return true
			}
			return false
		case statement.NotYet[word] != 0:
			r.fail(fmt.Errorf("%s: %s.%s chưa có trong engine hôm nay (lát %d của KITWORK_DATABASE.md)", at, root.Value, word, statement.NotYet[word]))
			return true
		case statement.Absent[word] != "":
			r.fail(fmt.Errorf("%s: %s.%s không có: viết %s", at, root.Value, word, statement.Absent[word]))
			return true
		default:
			r.fail(fmt.Errorf("%s: %s.%s không có; câu của database: %s", at, root.Value, word, statement.Words()))
			return true
		}
	}
	return false
}

// translator translates one statement.
type translator struct {
	r      *resolver
	root   *Identifier
	handle *DatabaseImport
	table  *statement.Table
	// parameter is the table's name in the lambda being read.
	parameter string
	s         *statement.Statement
	holes     []Expression
	failed    bool
}

func (t *translator) fail(token Token, format string, arguments ...any) {
	if !t.failed {
		t.failed = true
		t.r.fail(fmt.Errorf("%s: %s", t.r.where(token), fmt.Sprintf(format, arguments...)))
	}
}

// translateStatement translates the chain root.links[0]…links[n-1], the last link its action, and puts
// the call that replaces the action in the resolution.
func (r *resolver) translateStatement(root *Identifier, handle *DatabaseImport, links []*MethodCallExpression) {
	action := links[len(links)-1]
	t := &translator{r: r, root: root, handle: handle, s: &statement.Statement{Version: statement.Version, Action: action.Method.Value}}
	for _, link := range links {
		if t.failed {
			return
		}
		switch link.Method.Value {
		case "where":
			if body := t.lambda(link); body != nil {
				t.s.Where = conjunction(t.s.Where, t.condition(body))
			}
		case "orderBy":
			if body := t.lambda(link); body != nil {
				t.s.Order = append(t.s.Order, t.keys(body)...)
			}
		case statement.ActionList:
			t.list(link)
		case statement.ActionFirst, statement.ActionCount:
			if len(link.Arguments) > 0 {
				if body := t.lambda(link); body != nil {
					t.s.Where = conjunction(t.s.Where, t.condition(body))
				}
			}
		case statement.ActionInsert:
			if len(links) > 1 {
				t.fail(link.Method.Token, "insert không đi sau where hay orderBy: %s.insert((bảng) => ({ … }))", root.Value)
				return
			}
			if body := t.lambda(link); body != nil {
				t.insert(body)
			}
		}
	}
	if t.failed {
		return
	}
	if t.table == nil {
		t.fail(action.Method.Token, "câu cần một lambda có tham số là bảng: %s.%s((bảng) => …)", root.Value, action.Method.Value)
		return
	}
	if err := handle.Database.CheckNames(t.s); err != nil {
		t.fail(action.Method.Token, "%v", err)
		return
	}
	// INVOKE takes 1 + holes arguments in one byte
	if len(t.holes) > 254 {
		t.fail(action.Method.Token, "câu có %d giá trị lấy từ ngoài lambda, quá 254: tính trước câu, gom vào một object", len(t.holes))
		return
	}
	template := statement.Encode(t.s)
	literal := &Literal{
		Token: Token{Kind: String, Value: value.NewString(template), Source: action.Token.Source, Position: action.Token.Position},
		Value: value.NewString(template),
	}
	replacement := &MethodCallExpression{
		Token:     action.Token,
		Object:    root,
		Method:    &Identifier{Token: action.Method.Token, Value: statement.Method},
		Arguments: append([]Expression{literal}, t.holes...),
	}
	r.handleUse = true
	r.reference(root, false)
	r.handleUse = false
	for _, hole := range t.holes {
		r.expression(hole)
	}
	r.result.statements[action] = replacement
}

// lambda reads the lambda of a link: one parameter, the table, and a body that is one expression. It
// gives the expression, or nil after an error.
func (t *translator) lambda(link *MethodCallExpression) Expression {
	word := link.Method.Value
	if len(link.Arguments) != 1 {
		t.fail(link.Method.Token, "%s nhận một lambda có tham số là bảng: %s.%s((bảng) => …)", word, t.root.Value, word)
		return nil
	}
	function, ok := link.Arguments[0].(*FunctionLiteral)
	if !ok {
		t.fail(expressionToken(link.Arguments[0]), "%s nhận một lambda viết ngay trong câu: %s.%s((bảng) => …); lambda của câu là bảng mà compiler đọc, không phải hàm chạy", word, t.root.Value, word)
		return nil
	}
	switch {
	case len(function.Parameters) == 0:
		t.fail(function.Token, "lambda của câu cần tham số là bảng: (orders) => …")
		return nil
	case len(function.Parameters) > 1:
		t.fail(function.Parameters[1].Token, "nhiều bảng trong một câu chưa có trong engine hôm nay (lát 5): một tham số, là bảng")
		return nil
	}
	parameter := function.Parameters[0]
	if strings.HasPrefix(parameter.Value, "{") || strings.HasPrefix(parameter.Value, "[") {
		t.fail(parameter.Token, "tham số của lambda là bảng, không tách: (orders) => orders.email")
		return nil
	}
	if parameter.Value == "session" {
		t.fail(parameter.Token, "session là phiên, tham số dành riêng của policy (chưa có trong engine hôm nay); tham số của câu là bảng")
		return nil
	}
	if function.Body == nil || len(function.Body.Statements) != 1 {
		t.fail(function.Token, "lambda của câu là một biểu thức: (orders) => orders.day === today")
		return nil
	}
	returned, ok := function.Body.Statements[0].(*ReturnStatement)
	if !ok || returned.ReturnValue == nil {
		t.fail(function.Token, "lambda của câu là một biểu thức: (orders) => orders.day === today")
		return nil
	}
	if t.table == nil {
		table, err := t.handle.Database.Table(parameter.Value)
		if err != nil {
			t.fail(parameter.Token, "%v", err)
			return nil
		}
		t.table = table
		t.s.Table = table.Name
	} else if parameter.Value != t.table.Name {
		t.fail(parameter.Token, "một câu một bảng: lambda trước là %s, lambda này là %s (nhiều bảng chưa có trong engine hôm nay, lát 5)", t.table.Name, parameter.Value)
		return nil
	}
	t.parameter = parameter.Value
	return returned.ReturnValue
}

// conjunction joins two conditions by &&; either may be nil.
func conjunction(left, right *statement.Node) *statement.Node {
	switch {
	case right == nil:
		return left
	case left == nil:
		return right
	}
	return &statement.Node{Op: statement.OpAnd, Args: []*statement.Node{left, right}}
}

// condition translates a lambda's body in the place of a condition.
func (t *translator) condition(e Expression) *statement.Node {
	if t.failed {
		return nil
	}
	switch node := e.(type) {
	case *InfixExpression:
		switch node.Operator {
		case "&&", "||":
			left := t.condition(node.Left)
			right := t.condition(node.Right)
			return &statement.Node{Op: node.Operator, Args: []*statement.Node{left, right}}
		case "===", "!==", "<", "<=", ">", ">=":
			if t.mentions(node) {
				return t.comparison(node)
			}
		case "==", "!=":
			if t.mentions(node) {
				t.fail(node.Token, "viết %s=: trong câu database chỉ có === và !==", node.Operator)
				return nil
			}
		}
	case *PrefixExpression:
		if node.Operator == "!" && t.mentions(node) {
			return &statement.Node{Op: statement.OpNot, Args: []*statement.Node{t.condition(node.Right)}}
		}
	case *Identifier:
		if node.Value == t.parameter {
			return &statement.Node{Op: statement.OpTable}
		}
	case *MemberExpression:
		if column, ok := t.column(node); ok {
			t.fail(node.Token, "viết %s.%s === true: điều kiện so cột với một giá trị", t.parameter, column)
			return nil
		}
	case *TernaryExpression:
		if t.mentions(node) {
			t.fail(node.Token, "? : trên cột chưa có trong engine hôm nay (lát 2)")
			return nil
		}
	}
	if t.failed || t.absent(e) {
		return nil
	}
	if word, name, imported := t.lambdaWord(e); word != "" {
		// desc(orders.amount), one(ids) in the place of a condition
		t.misplacedWord(word, name, imported)
		return nil
	}
	if !t.mentions(e) {
		// a value from outside the lambda in the place of a condition: true or false when it runs
		if offender := t.impure(e); offender != nil {
			t.untranslatable(offender)
			return nil
		}
		return &statement.Node{Op: statement.OpTruth, Args: []*statement.Node{t.hole(e)}}
	}
	t.untranslatable(e)
	return nil
}

// comparison translates column OP value, or value OP column (turned around).
func (t *translator) comparison(node *InfixExpression) *statement.Node {
	operator := node.Operator
	left, leftColumn := t.column(node.Left)
	right, rightColumn := t.column(node.Right)
	if t.failed {
		return nil
	}
	var column string
	var other Expression
	switch {
	case leftColumn && rightColumn:
		t.fail(node.Token, "so hai cột của bảng chưa có trong engine hôm nay (lát 2)")
		return nil
	case leftColumn:
		column, other = left, node.Right
	case rightColumn:
		column, other = right, node.Left
		operator = turned(operator)
	default:
		other := node.Right
		if t.whole(node.Right) {
			other = node.Left
		}
		switch {
		case !t.whole(node.Left) && !t.whole(node.Right):
			t.fail(node.Token, "phép tính trên cột chưa có trong engine hôm nay (lát 2): so một cột với một giá trị")
		case isNullLiteral(other):
			t.fail(node.Token, "so cả bảng là LEFT JOIN, chưa có trong engine hôm nay (lát 5)")
		default:
			// posts === search(q) is full text over the table (KITWORK_DATABASE.md mục 2), not a join
			if word, name, imported := t.lambdaWord(other); word != "" {
				t.misplacedWord(word, name, imported)
			} else {
				t.fail(node.Token, "cả bảng chỉ so với null (LEFT JOIN, lát 5) hay search(chữ) (lát 2): so một cột, %s.cột %s giá trị", t.parameter, operator)
			}
		}
		return nil
	}
	compared := t.value(other, operator, true)
	if compared == nil {
		return nil
	}
	return &statement.Node{Op: operator, Args: []*statement.Node{{Op: statement.OpColumn, Column: column}, compared}}
}

// turned is the operator of a comparison read the other way round: 5 < orders.amount is
// orders.amount > 5.
func turned(operator string) string {
	switch operator {
	case "<":
		return ">"
	case "<=":
		return ">="
	case ">":
		return "<"
	case ">=":
		return "<="
	}
	return operator
}

// column reports e as a column of the table (orders.amount), checked against the schema. Any other use of
// the table on e's surface (orders.note.length, orders[k], orders.email.trim()) is an error.
func (t *translator) column(e Expression) (string, bool) {
	switch node := e.(type) {
	case *MemberExpression:
		if object, ok := node.Object.(*Identifier); ok && object.Value == t.parameter {
			if node.Optional {
				t.fail(node.Token, "?. không có trên bảng: viết %s.%s", t.parameter, node.Property.Value)
				return "", false
			}
			if _, err := t.table.Column(node.Property.Value); err != nil {
				t.fail(node.Property.Token, "%v", err)
				return "", false
			}
			return node.Property.Value, true
		}
		if t.mentions(node.Object) {
			t.fail(node.Token, "trên bảng và cột không đọc thuộc tính, không gọi phương thức: %s", node.String())
		}
	case *IndexExpression:
		if t.mentions(node.Left) {
			t.fail(node.Token, "trên bảng và cột không đọc theo chỉ số: viết %s.tên_cột", t.parameter)
		}
	case *MethodCallExpression:
		if t.mentions(node.Object) {
			t.fail(node.Method.Token, "trên bảng và cột không gọi phương thức: %s", node.String())
		}
	}
	return "", false
}

// whole reports e as the table itself.
func (t *translator) whole(e Expression) bool {
	name, ok := e.(*Identifier)
	return ok && name.Value == t.parameter
}

// value translates the value side of a comparison, or a value of an insert's data: null, a literal, a
// word (one), or a value from outside the lambda (a hole).
func (t *translator) value(e Expression, operator string, compared bool) *statement.Node {
	if t.failed {
		return nil
	}
	if t.mentions(e) {
		if compared {
			t.fail(expressionToken(e), "phép tính trên cột chưa có trong engine hôm nay (lát 2): so một cột với một giá trị")
		} else {
			t.fail(expressionToken(e), "dữ liệu insert không đọc bảng: tính giá trị trước câu, đặt vào một const")
		}
		return nil
	}
	switch node := e.(type) {
	case *Literal:
		switch {
		case node.Token.Kind == Null || node.Value.K == value.Nil:
			if compared && operator != "===" && operator != "!==" {
				t.fail(node.Token, "so với null chỉ bằng === hay !==")
				return nil
			}
			return &statement.Node{Op: statement.OpNull}
		case node.Value.K == value.Number:
			return statement.Literal(node.Value.N)
		case node.Value.K == value.String:
			return statement.Literal(node.Value.Text())
		case node.Value.K == value.Bool:
			return statement.Literal(node.Value.N != 0)
		}
	case *PrefixExpression:
		if literal, ok := node.Right.(*Literal); ok && node.Operator == "-" && literal.Value.K == value.Number {
			return statement.Literal(-literal.Value.N)
		}
	case *CallExpression:
		word, name, imported := t.lambdaWord(node)
		switch {
		case word == "one" && imported && compared && (operator == "===" || operator == "!=="):
			if len(node.Arguments) != 1 {
				t.fail(name.Token, "one nhận một mảng: one(ids)")
				return nil
			}
			if t.mentions(node.Arguments[0]) {
				t.fail(name.Token, "one nhận một mảng giá trị, không đọc bảng")
				return nil
			}
			if offender := t.impure(node.Arguments[0]); offender != nil {
				t.untranslatable(offender)
				return nil
			}
			return &statement.Node{Op: statement.OpOne, Args: []*statement.Node{t.hole(node.Arguments[0])}}
		case word != "":
			t.misplacedWord(word, name, imported)
			return nil
		case t.absent(node):
			return nil
		}
	}
	if offender := t.impure(e); offender != nil {
		t.untranslatable(offender)
		return nil
	}
	return t.hole(e)
}

// untranslatable is the error of what a statement cannot hold: a call, a function, an assignment.
func (t *translator) untranslatable(e Expression) {
	if name, ok := e.(*Identifier); ok {
		t.fail(name.Token, "%s chỉ có nghĩa trong lambda của câu database, ở chỗ của nó (one ở vế phải, desc trong orderBy)", name.Value)
		return
	}
	t.fail(expressionToken(e), "không dịch được `%s`: hàm không chạy trong câu; tính trước câu, đặt vào một const", e.String())
}

// lambdaWord reports e as a call of a word of kitwork/database (desc(…), one(…)): the word and its name as
// written. imported is false for a word written without its import, a name nothing declares; a variable of
// the code with a word's name (a count of its own) is not a word.
func (t *translator) lambdaWord(e Expression) (string, *Identifier, bool) {
	call, ok := e.(*CallExpression)
	if !ok {
		return "", nil, false
	}
	name, ok := call.Function.(*Identifier)
	if !ok {
		return "", nil, false
	}
	found := t.r.lookup(name.Value)
	switch {
	case found != nil && found.word != "":
		return found.word, name, true
	case found == nil && statement.LambdaWords[name.Value]:
		return name.Value, name, false
	}
	return "", nil, false
}

// misplacedWord is the error of a word of kitwork/database where it means nothing: like, one and search
// stand on the right of === or !==, desc and asc in orderBy (KITWORK_DATABASE.md mục 2, "lỗi chỉ đường").
func (t *translator) misplacedWord(word string, name *Identifier, imported bool) {
	switch {
	case !imported:
		t.fail(name.Token, "%s chưa import: import { %s } from \"kitwork/database\"", word, word)
	case statement.LambdaWordsNotYet[word] != 0:
		t.fail(name.Token, "%s(…) chưa có trong engine hôm nay (lát %d)", word, statement.LambdaWordsNotYet[word])
	case word == "desc" || word == "asc":
		t.fail(name.Token, "%s(…) chỉ có trong orderBy: (%s) => %s(%s.amount)", word, t.parameter, word, t.parameter)
	default:
		t.fail(name.Token, "%s(…) chỉ đứng bên phải === hay !==: %s.id === %s(ids)", word, t.parameter, word)
	}
}

// absent refuses, with the spec's spelling, the forms of "is in" the spec has not (KITWORK_DATABASE.md mục
// 9): any(ids), oneOf(ids), ids.includes(orders.id). Each is bảng.cột === one(mảng).
func (t *translator) absent(e Expression) bool {
	switch node := e.(type) {
	case *CallExpression:
		name, ok := node.Function.(*Identifier)
		if !ok || (name.Value != "any" && name.Value != "oneOf") || t.r.lookup(name.Value) != nil {
			return false
		}
		t.fail(name.Token, "%s(…) không có: viết %s.cột === one(mảng)", name.Value, t.parameter)
		return true
	case *MethodCallExpression:
		if node.Method.Value != "includes" || !t.mentions(node) {
			return false
		}
		if len(node.Arguments) == 1 && !t.mentions(node.Object) {
			if member, ok := node.Arguments[0].(*MemberExpression); ok && t.mentions(member) {
				t.fail(node.Method.Token, "includes không có trong câu database: viết %s === one(%s)", member.String(), node.Object.String())
				return true
			}
		}
		t.fail(node.Method.Token, "includes không có trong câu database: viết %s.cột === one(mảng)", t.parameter)
		return true
	}
	return false
}

// isNullLiteral reports e as null written in the lambda.
func isNullLiteral(e Expression) bool {
	literal, ok := e.(*Literal)
	return ok && (literal.Token.Kind == Null || literal.Value.K == value.Nil)
}

// hole registers e as a value from outside the lambda. It is compiled where the statement is, before the
// call; the template keeps where it is written.
func (t *translator) hole(e Expression) *statement.Node {
	index := len(t.holes)
	t.holes = append(t.holes, e)
	t.s.Holes = append(t.s.Holes, statement.Hole{Site: t.r.where(expressionToken(e))})
	return statement.HoleNode(index)
}

// mentions reports e reading the table.
func (t *translator) mentions(e Expression) bool {
	found := false
	walkExpression(e, func(node Expression) bool {
		if name, ok := node.(*Identifier); ok && name.Value == t.parameter {
			found = true
		}
		return !found
	})
	return found
}

// impure is the first part of e a statement cannot compute ahead: a call, a function, an assignment, an
// optional chain, a lambda word read as a value. nil when e only reads.
func (t *translator) impure(e Expression) Expression {
	switch node := e.(type) {
	case nil:
		return nil
	case *Identifier:
		if found := t.r.lookup(node.Value); found != nil && found.word != "" {
			return node
		}
		return nil
	case *Literal:
		return nil
	case *TemplateLiteral:
		for _, part := range node.Parts {
			if offender := t.impure(part); offender != nil {
				return offender
			}
		}
		return nil
	case *ArrayLiteral:
		for _, element := range node.Elements {
			if offender := t.impure(element); offender != nil {
				return offender
			}
		}
		return nil
	case *ObjectLiteral:
		for _, entry := range node.Entries {
			if _, isName := entry.Key.(*Identifier); !isName && !entry.IsSpread {
				if offender := t.impure(entry.Key); offender != nil {
					return offender
				}
			}
			if offender := t.impure(entry.Value); offender != nil {
				return offender
			}
		}
		return nil
	case *SpreadExpression:
		return t.impure(node.Value)
	case *MemberExpression:
		if node.Optional {
			return node
		}
		return t.impure(node.Object)
	case *IndexExpression:
		if node.Optional {
			return node
		}
		if offender := t.impure(node.Left); offender != nil {
			return offender
		}
		return t.impure(node.Index)
	case *PrefixExpression:
		return t.impure(node.Right)
	case *InfixExpression:
		if offender := t.impure(node.Left); offender != nil {
			return offender
		}
		return t.impure(node.Right)
	case *TernaryExpression:
		for _, part := range []Expression{node.Condition, node.Consequence, node.Alternative} {
			if offender := t.impure(part); offender != nil {
				return offender
			}
		}
		return nil
	}
	return e
}

// keys translates orderBy's body: a column, desc(column), asc(column), joined by &&.
func (t *translator) keys(e Expression) []statement.Key {
	if t.failed {
		return nil
	}
	switch node := e.(type) {
	case *InfixExpression:
		switch node.Operator {
		case "&&":
			return append(t.keys(node.Left), t.keys(node.Right)...)
		case "||":
			t.fail(node.Token, "|| trong orderBy không có: hai khoá nối bằng &&, desc(%s.amount) && asc(%s.id)", t.parameter, t.parameter)
			return nil
		}
	case *CallExpression:
		word, name, imported := t.lambdaWord(node)
		switch {
		case (word == "desc" || word == "asc") && imported:
			if len(node.Arguments) != 1 {
				t.fail(name.Token, "%s nhận một cột: %s(%s.amount)", word, word, t.parameter)
				return nil
			}
			column, ok := t.column(node.Arguments[0])
			if !ok {
				if !t.failed {
					t.fail(name.Token, "%s nhận một cột của bảng: %s(%s.amount)", word, word, t.parameter)
				}
				return nil
			}
			return []statement.Key{{Column: column, Descending: word == "desc"}}
		case word != "" && !imported:
			t.misplacedWord(word, name, imported)
			return nil
		case word != "":
			t.fail(name.Token, "%s(…) không có trong orderBy: cột, desc(cột), asc(cột)", word)
			return nil
		}
	}
	if column, ok := t.column(e); ok {
		return []statement.Key{{Column: column}}
	}
	if !t.failed {
		t.fail(expressionToken(e), "orderBy nhận cột của bảng: (%s) => desc(%s.amount)", t.parameter, t.parameter)
	}
	return nil
}

// list reads list's argument: a number is the limit, { page, limit } a page, a lambda the condition, any
// other value is told apart when it runs.
func (t *translator) list(link *MethodCallExpression) {
	switch len(link.Arguments) {
	case 0:
		return
	case 1:
	default:
		t.fail(link.Method.Token, "list nhận một trong ba: số, { page, limit } hay lambda; cần vừa lọc vừa trang thì where trước")
		return
	}
	argument := link.Arguments[0]
	switch node := argument.(type) {
	case *FunctionLiteral:
		if body := t.lambda(link); body != nil {
			t.s.Where = conjunction(t.s.Where, t.condition(body))
		}
		return
	case *Literal:
		if node.Value.K != value.Number {
			t.fail(node.Token, "list nhận một trong ba: số, { page, limit } hay lambda")
			return
		}
		t.s.Limit = statement.Literal(node.Value.N)
		return
	case *ObjectLiteral:
		bounds := map[string]Expression{}
		for _, entry := range node.Entries {
			name, _, ok := objectKeyName(entry.Key)
			if entry.IsSpread || !ok || (name != "page" && name != "limit") || bounds[name] != nil {
				t.fail(node.Token, "list nhận { page, limit }: list({ page: 2, limit: 20 })")
				return
			}
			bounds[name] = entry.Value
		}
		if bounds["page"] == nil || bounds["limit"] == nil {
			t.fail(node.Token, "list nhận { page, limit }: list({ page: 2, limit: 20 })")
			return
		}
		t.s.Page = t.bound(bounds["page"])
		t.s.Limit = t.bound(bounds["limit"])
		return
	}
	if t.mentions(argument) {
		t.fail(expressionToken(argument), "list nhận số, { page, limit } hay lambda")
		return
	}
	if offender := t.impure(argument); offender != nil {
		t.untranslatable(offender)
		return
	}
	t.s.Bounds = t.hole(argument)
}

// bound is a number of { page, limit }: a literal or a value from outside.
func (t *translator) bound(e Expression) *statement.Node {
	if literal, ok := e.(*Literal); ok && literal.Value.K == value.Number {
		return statement.Literal(literal.Value.N)
	}
	if offender := t.impure(e); offender != nil {
		t.untranslatable(offender)
		return nil
	}
	return t.hole(e)
}

// insert translates an insert's data: an object whose keys are columns.
func (t *translator) insert(body Expression) {
	data, ok := body.(*ObjectLiteral)
	if !ok {
		t.fail(expressionToken(body), "insert nhận một object dữ liệu: %s.insert((%s) => ({ … }))", t.root.Value, t.parameter)
		return
	}
	seen := map[string]bool{}
	keys := make([]string, 0, len(data.Entries))
	for _, entry := range data.Entries {
		if entry.IsSpread {
			t.fail(data.Token, "...spread trong dữ liệu insert chưa có trong engine hôm nay (lát 3): viết từng cột")
			return
		}
		name, at, ok := objectKeyName(entry.Key)
		if !ok {
			t.fail(data.Token, "khoá của dữ liệu insert là tên cột viết bằng chữ")
			return
		}
		if seen[name] {
			t.fail(at, "cột %s viết hai lần trong dữ liệu insert", name)
			return
		}
		seen[name] = true
		column, err := t.table.Column(name)
		if err != nil {
			t.fail(at, "%v", err)
			return
		}
		if err := column.CheckWrite(); err != nil {
			t.fail(at, "%v", err)
			return
		}
		written := t.value(entry.Value, "", false)
		if written == nil {
			return
		}
		keys = append(keys, name)
		t.s.Values = append(t.s.Values, statement.Field{Column: name, Value: written})
	}
	if err := t.table.CheckInsert(keys); err != nil {
		t.fail(data.Token, "%v", err)
	}
}

// walkExpression visits e and what it holds, outer first, while visit returns true. A function inside is
// not entered: its names are its own.
func walkExpression(e Expression, visit func(Expression) bool) {
	if e == nil || !visit(e) {
		return
	}
	switch node := e.(type) {
	case *PrefixExpression:
		walkExpression(node.Right, visit)
	case *InfixExpression:
		walkExpression(node.Left, visit)
		walkExpression(node.Right, visit)
	case *TernaryExpression:
		walkExpression(node.Condition, visit)
		walkExpression(node.Consequence, visit)
		walkExpression(node.Alternative, visit)
	case *CallExpression:
		walkExpression(node.Function, visit)
		for _, argument := range node.Arguments {
			walkExpression(argument, visit)
		}
	case *MethodCallExpression:
		walkExpression(node.Object, visit)
		for _, argument := range node.Arguments {
			walkExpression(argument, visit)
		}
	case *MemberExpression:
		walkExpression(node.Object, visit)
	case *IndexExpression:
		walkExpression(node.Left, visit)
		walkExpression(node.Index, visit)
	case *ChainExpression:
		walkExpression(node.Expression, visit)
	case *AssignmentExpression:
		walkExpression(node.Name, visit)
		walkExpression(node.Value, visit)
	case *ArrayLiteral:
		for _, element := range node.Elements {
			walkExpression(element, visit)
		}
	case *ObjectLiteral:
		for _, entry := range node.Entries {
			if _, isName := entry.Key.(*Identifier); !isName && !entry.IsSpread {
				walkExpression(entry.Key, visit)
			}
			walkExpression(entry.Value, visit)
		}
	case *SpreadExpression:
		walkExpression(node.Value, visit)
	case *TemplateLiteral:
		for _, part := range node.Parts {
			walkExpression(part, visit)
		}
	}
}
