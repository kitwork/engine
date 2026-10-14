package compiler

import (
	"bytes"
	"strings"

	"github.com/kitwork/engine/value"
)

// Node là interface gốc cho mọi thành phần trong cây
type Node interface {
	String() string
}

// Statement đại diện cho các câu lệnh (không trả về giá trị trực tiếp)
type Statement interface {
	Node
	statementNode()
}

// Expression đại diện cho các biểu thức (trả về giá trị)
type Expression interface {
	Node
	expressionNode()
}

/* =============================================================================
   1. CẤU TRÚC GỐC & CÁC CÂU LỆNH (STATEMENTS)
   ============================================================================= */

// Program là nút gốc chứa toàn bộ script
type Program struct {
	Statements []Statement
	Exports    []string // tên export qua `export const/function` hoặc `export { }`
	HasDefault bool     // true nếu có `export default …` (đã hạ về const DefaultExportName)
}

// DefaultExportName là biến tổng hợp mà `export default <expr>` được hạ xuống.
const DefaultExportName = "__kw_default"

// ImportSpec là một binding có tên trong import: `imported as local`
// (Local == Imported khi không có alias).
type ImportSpec struct {
	Imported string // tên trong export của module
	Local    string // tên biến cục bộ
}

// ImportStatement đại diện cho một import MODULE TƯƠNG ĐỐI chưa giải quyết
// (vd `import { x } from "./helper.kitwork.js"`). Import từ "kitwork" KHÔNG dùng
// node này — chúng được hạ thẳng về VarStatement trong parser. Bundler ở package
// script sẽ giải quyết các node này (IIFE-wrap) trước khi compile.
type ImportStatement struct {
	Token      Token
	Names      []ImportSpec // import có tên: import { a, b as c } from "..."
	Default    *Identifier  // import mặc định: import x from "..." (nil nếu không có)
	Source     string       // specifier (đường dẫn tương đối)
	SideEffect bool         // import "..."  (không binding)
}

func (is *ImportStatement) statementNode() {}
func (is *ImportStatement) String() string {
	var out bytes.Buffer
	out.WriteString("import ")
	if is.SideEffect {
		out.WriteString("\"" + is.Source + "\";")
		return out.String()
	}
	if is.Default != nil {
		out.WriteString(is.Default.Value)
		if len(is.Names) > 0 {
			out.WriteString(", ")
		}
	}
	if len(is.Names) > 0 {
		out.WriteString("{ ")
		for i, n := range is.Names {
			if n.Local != n.Imported {
				out.WriteString(n.Imported + " as " + n.Local)
			} else {
				out.WriteString(n.Imported)
			}
			if i < len(is.Names)-1 {
				out.WriteString(", ")
			}
		}
		out.WriteString(" }")
	}
	out.WriteString(" from \"" + is.Source + "\";")
	return out.String()
}

// GroupStatement là một nhóm câu lệnh được compile TẠI CHỖ (không tạo scope mới,
// không RETURN) — dùng để một lệnh nguồn (vd import có alias) hạ xuống nhiều
// VarStatement.
type GroupStatement struct {
	Statements []Statement
}

func (gs *GroupStatement) statementNode() {}
func (gs *GroupStatement) String() string {
	var out bytes.Buffer
	for _, s := range gs.Statements {
		out.WriteString(s.String())
	}
	return out.String()
}

func (p *Program) String() string {
	var out bytes.Buffer
	for _, s := range p.Statements {
		out.WriteString(s.String())
	}
	return out.String()
}

type DestructMode int

const (
	DestructNone   DestructMode = 0
	DestructObject DestructMode = 1
	DestructArray  DestructMode = 2
)

// VarStatement: const a = 10; let b = 20; const { a, b: c = 1 } = o;
type VarStatement struct {
	Token Token // const, let
	// Names are the variables the statement declares, in source order: with a pattern, every name
	// in it (the targets, not the keys: const { value: user } = … declares user).
	Names        []*Identifier
	Value        Expression
	DestructMode DestructMode
	// Pattern is the destructuring the source wrote; nil for a plain name, and for the { a, b } that
	// an import lowers to (the compiler reads Names then).
	Pattern *BindingPattern
	// Function: function f() {…}, which declares f as a variable a program may assign, as in
	// JavaScript; any other const may not be (scope.go).
	Function bool
}

func (vs *VarStatement) statementNode() {}
func (vs *VarStatement) String() string {
	var out bytes.Buffer
	out.WriteString(vs.Token.String() + " ")

	if vs.Pattern != nil {
		out.WriteString(vs.Pattern.String())
	} else if vs.DestructMode == DestructObject {
		out.WriteString("{ ")
		for i, n := range vs.Names {
			out.WriteString(n.String())
			if i < len(vs.Names)-1 {
				out.WriteString(", ")
			}
		}
		out.WriteString(" }")
	} else if vs.DestructMode == DestructArray {
		out.WriteString("[ ")
		for i, n := range vs.Names {
			out.WriteString(n.String())
			if i < len(vs.Names)-1 {
				out.WriteString(", ")
			}
		}
		out.WriteString(" ]")
	} else if len(vs.Names) > 0 {
		out.WriteString(vs.Names[0].String())
	}

	out.WriteString(" = ")
	if vs.Value != nil {
		out.WriteString(vs.Value.String())
	}
	out.WriteString(";")
	return out.String()
}

// BindingPattern is the left side of a destructuring (14/10/2026): { a, b: c, d = 1, e: { f } } or
// [a, b = 2, [c]]. It is a declaration's (const { … } = o) and an assignment's (({ … } = o)); the
// compiler reads each element off the value with the opcodes a member read uses, no new one.
type BindingPattern struct {
	Token    Token // '{' or '['
	Array    bool
	Elements []BindingElement
}

// BindingElement is one place of a pattern: the key it reads (the index, in an array pattern), and
// either a variable or a nested pattern, with the value used when what it reads is missing or null.
type BindingElement struct {
	Key      string // the property an object pattern reads; "" in an array pattern
	KeyToken Token
	Name     *Identifier     // the variable bound; nil when Pattern is set
	Pattern  *BindingPattern // a nested pattern
	Default  Expression      // nil when none
}

// Names lists the variables the pattern binds, nested ones included, in source order.
func (bp *BindingPattern) Names() []*Identifier {
	var names []*Identifier
	for _, element := range bp.Elements {
		if element.Pattern != nil {
			names = append(names, element.Pattern.Names()...)
		} else if element.Name != nil {
			names = append(names, element.Name)
		}
	}
	return names
}

func (bp *BindingPattern) expressionNode() {}
func (bp *BindingPattern) String() string {
	parts := make([]string, 0, len(bp.Elements))
	for _, element := range bp.Elements {
		target := ""
		if element.Pattern != nil {
			target = element.Pattern.String()
		} else if element.Name != nil {
			target = element.Name.Value
		}
		part := target
		if !bp.Array && (element.Name == nil || element.Name.Value != element.Key) {
			part = element.Key + ": " + target
		}
		if element.Default != nil {
			part += " = " + element.Default.String()
		}
		parts = append(parts, part)
	}
	if bp.Array {
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// ExpressionStatement: Dùng cho các lệnh đứng độc lập (vd: call();)
type ExpressionStatement struct {
	Token      Token
	Expression Expression
}

func (es *ExpressionStatement) statementNode() {}
func (es *ExpressionStatement) String() string {
	if es.Expression != nil {
		return es.Expression.String() + ";"
	}
	return ""
}

// BlockStatement: Code nằm trong dấu { }
type BlockStatement struct {
	Token      Token // Dấu '{'
	Statements []Statement
}

func (bs *BlockStatement) statementNode() {}
func (bs *BlockStatement) String() string {
	var out bytes.Buffer
	for _, s := range bs.Statements {
		out.WriteString(s.String())
	}
	return out.String()
}

// ReturnStatement: return 10;
type ReturnStatement struct {
	Token       Token
	ReturnValue Expression
}

// BreakStatement exits the nearest switch or bounded loop. Labels are not part
// of the Kitwork subset.
type BreakStatement struct {
	Token Token
}

func (bs *BreakStatement) statementNode() {}
func (bs *BreakStatement) String() string { return "break;" }

// SwitchCase keeps source order because JavaScript switch bodies may fall
// through. Test is nil for the single default clause.
type SwitchCase struct {
	Token      Token
	Test       Expression
	Statements []Statement
}

// SwitchStatement evaluates Discriminant once, dispatches cases in source
// order, and preserves ordinary JavaScript fallthrough semantics.
type SwitchStatement struct {
	Token        Token
	Discriminant Expression
	Cases        []SwitchCase
}

func (ss *SwitchStatement) statementNode() {}
func (ss *SwitchStatement) String() string {
	var out bytes.Buffer
	out.WriteString("switch (")
	if ss.Discriminant != nil {
		out.WriteString(ss.Discriminant.String())
	}
	out.WriteString(") {")
	for _, clause := range ss.Cases {
		if clause.Test == nil {
			out.WriteString("default:")
		} else {
			out.WriteString("case ")
			out.WriteString(clause.Test.String())
			out.WriteString(":")
		}
		for _, statement := range clause.Statements {
			out.WriteString(statement.String())
		}
	}
	out.WriteString("}")
	return out.String()
}

func (rs *ReturnStatement) statementNode() {}
func (rs *ReturnStatement) String() string {
	var out bytes.Buffer
	out.WriteString(rs.Token.String() + " ")
	if rs.ReturnValue != nil {
		out.WriteString(rs.ReturnValue.String())
	}
	out.WriteString(";")
	return out.String()
}

// ForStatement: for (item of list) { } — iterates a bounded collection (compiled to the ITER opcode).
type ForStatement struct {
	Token    Token // Dấu 'for'
	Item     *Identifier
	Iterable Expression
	Body     *BlockStatement
	// Const: for (const x of …), whose x the body may not assign; let lets it.
	Const bool
}

// ForRangeStatement: a BOUNDED counting loop — for (let i = 0; i < n; i++) { … }.
// The parser accepts ONLY this exact shape (a declared counter, a condition comparing that counter,
// and an update that mutates that counter), so an unbounded / while-style loop (for(;;), for(;c;))
// cannot be written — the "no infinite loop" guarantee holds by construction. MaxEnergy is the
// backstop for the one pathological case syntax can't catch (a bound that grows inside the body).
type ForRangeStatement struct {
	Token   Token      // 'for'
	Counter string     // the loop variable name (i)
	Init    Statement  // let i = <expr>
	Cond    Expression // i < n
	Update  Expression // i = i + 1  (desugared from i++ / i += k)
	Body    *BlockStatement
}

func (fs *ForRangeStatement) statementNode()  {}
func (fs *ForRangeStatement) expressionNode() {}
func (fs *ForRangeStatement) String() string {
	var out bytes.Buffer
	out.WriteString("for (")
	if fs.Init != nil {
		out.WriteString(fs.Init.String())
	}
	out.WriteString("; ")
	if fs.Cond != nil {
		out.WriteString(fs.Cond.String())
	}
	out.WriteString("; ")
	if fs.Update != nil {
		out.WriteString(fs.Update.String())
	}
	out.WriteString(") ")
	out.WriteString(fs.Body.String())
	return out.String()
}

func (fs *ForStatement) statementNode()  {}
func (fs *ForStatement) expressionNode() {}
func (fs *ForStatement) String() string {
	var out bytes.Buffer
	out.WriteString("for (")
	out.WriteString(fs.Item.String())
	out.WriteString(" in ")
	out.WriteString(fs.Iterable.String())
	out.WriteString(") ")
	out.WriteString(fs.Body.String())
	return out.String()
}

// DeferStatement: defer () => { }
type DeferStatement struct {
	Token Token // Dấu 'defer'
	Fn    Expression
}

func (ds *DeferStatement) statementNode()  {}
func (ds *DeferStatement) expressionNode() {}
func (ds *DeferStatement) String() string {
	return "defer " + ds.Fn.String() + ";"
}

/* =============================================================================
   2. BIỂU THỨC (EXPRESSIONS)
   ============================================================================= */

// Identifier: Tên biến (db, user, task)
type Identifier struct {
	Token Token
	Value string
}

func (i *Identifier) expressionNode() {}
func (i *Identifier) String() string  { return i.Value }

// Literal: Giá trị thô (10, "hello", true, null)
type Literal struct {
	Token Token
	Value value.Value // Thùng 24-byte
}

func (l *Literal) expressionNode() {}
func (l *Literal) String() string  { return l.Value.Text() }

// PrefixExpression: !true, -5
type PrefixExpression struct {
	Token    Token
	Operator string
	Right    Expression
}

func (pe *PrefixExpression) expressionNode() {}
func (pe *PrefixExpression) String() string {
	return "(" + pe.Operator + pe.Right.String() + ")"
}

// InfixExpression: a + b, x > y, a == b
type InfixExpression struct {
	Token    Token
	Left     Expression
	Operator string
	Right    Expression
}

func (ie *InfixExpression) expressionNode() {}
func (ie *InfixExpression) String() string {
	return "(" + ie.Left.String() + " " + ie.Operator + " " + ie.Right.String() + ")"
}

// IfExpression: if (cond) { con } else { alt }
type IfExpression struct {
	Token       Token
	Condition   Expression
	Consequence *BlockStatement
	Alternative *BlockStatement
}

func (ie *IfExpression) expressionNode() {}
func (ie *IfExpression) String() string {
	var out bytes.Buffer
	out.WriteString("if ")
	out.WriteString(ie.Condition.String())
	out.WriteString(" ")
	out.WriteString(ie.Consequence.String())
	if ie.Alternative != nil {
		out.WriteString(" else ")
		out.WriteString(ie.Alternative.String())
	}
	return out.String()
}

// CallExpression: f(a, b)
type CallExpression struct {
	Token     Token      // Dấu '('
	Function  Expression // Tên hàm hoặc object.method
	Arguments []Expression
	Optional  bool // f?.(a): no call, and null, when f is null
	// Close is where its closing ) is: the source between ( and ) is what a compile error quotes
	// (safe(findUser(id)) says to write safe(() => findUser(id))). 0 when the parser did not read it.
	Close int32
}

func (ce *CallExpression) expressionNode() {}
func (ce *CallExpression) String() string {
	var out bytes.Buffer
	args := []string{}
	for _, a := range ce.Arguments {
		args = append(args, a.String())
	}
	out.WriteString(ce.Function.String())
	if ce.Optional {
		out.WriteString("?.")
	}
	out.WriteString("(")
	out.WriteString(strings.Join(args, ", "))
	out.WriteString(")")
	return out.String()
}

// MemberExpression: object.property
type MemberExpression struct {
	Token    Token // Dấu '.'
	Object   Expression
	Property *Identifier
	Optional bool // object?.property
}

func (me *MemberExpression) expressionNode() {}
func (me *MemberExpression) String() string {
	if me.Optional {
		return me.Object.String() + "?." + me.Property.String()
	}
	return me.Object.String() + "." + me.Property.String()
}

// IndexExpression: array[index]
type IndexExpression struct {
	Token    Token // Dấu '['
	Left     Expression
	Index    Expression
	Optional bool // array?.[index]
}

func (ie *IndexExpression) expressionNode() {}
func (ie *IndexExpression) String() string {
	if ie.Optional {
		return "(" + ie.Left.String() + "?.[" + ie.Index.String() + "])"
	}
	return "(" + ie.Left.String() + "[" + ie.Index.String() + "])"
}

// ChainExpression is an optional chain as a whole (14/10/2026): a?.b.c(d), x.y?.[k], f?.(). When a link
// written ?. finds null or undefined on its left, the rest of the chain does not run and the whole
// expression is null, as in JavaScript. Parentheses end a chain: (a?.b).c reads c of whatever (a?.b) is.
type ChainExpression struct {
	Expression Expression // a MemberExpression, IndexExpression, CallExpression or MethodCallExpression
}

func (ce *ChainExpression) expressionNode() {}
func (ce *ChainExpression) String() string  { return ce.Expression.String() }

// TernaryExpression: cond ? consequence : alternative
type TernaryExpression struct {
	Token       Token
	Condition   Expression
	Consequence Expression
	Alternative Expression
}

func (te *TernaryExpression) expressionNode() {}
func (te *TernaryExpression) String() string {
	return "(" + te.Condition.String() + " ? " + te.Consequence.String() + " : " + te.Alternative.String() + ")"
}

// AssignmentExpression: a = 10
type AssignmentExpression struct {
	Token Token
	Name  Expression // Thường là Identifier hoặc IndexExpression
	Value Expression
}

func (ae *AssignmentExpression) expressionNode() {}
func (ae *AssignmentExpression) String() string {
	return "(" + ae.Name.String() + " = " + ae.Value.String() + ")"
}

/* =============================================================================
   3. CẤU TRÚC DỮ LIỆU & HÀM (COMPLEX)
   ============================================================================= */

// ArrayLiteral: [1, 2, 3]
type ArrayLiteral struct {
	Token    Token
	Elements []Expression
}

func (al *ArrayLiteral) expressionNode() {}
func (al *ArrayLiteral) String() string {
	var out bytes.Buffer
	elements := []string{}
	for _, el := range al.Elements {
		elements = append(elements, el.String())
	}
	out.WriteString("[")
	out.WriteString(strings.Join(elements, ", "))
	out.WriteString("]")
	return out.String()
}

// ObjectEntry đại diện cho một phần tử trong Object (cặp key-value hoặc spread)
type ObjectEntry struct {
	Key      Expression
	Value    Expression
	IsSpread bool
}

// ObjectLiteral: { "key": "value", ...obj }
type ObjectLiteral struct {
	Token   Token
	Entries []ObjectEntry
}

// SpreadExpression: ...obj
type SpreadExpression struct {
	Token Token // Dấu '...'
	Value Expression
}

func (se *SpreadExpression) expressionNode() {}
func (se *SpreadExpression) String() string  { return "..." + se.Value.String() }

func (ol *ObjectLiteral) expressionNode() {}
func (ol *ObjectLiteral) String() string {
	var out bytes.Buffer
	parts := []string{}
	for _, entry := range ol.Entries {
		if entry.IsSpread {
			parts = append(parts, "..."+entry.Value.String())
		} else {
			parts = append(parts, entry.Key.String()+":"+entry.Value.String())
		}
	}
	out.WriteString("{")
	out.WriteString(strings.Join(parts, ", "))
	out.WriteString("}")
	return out.String()
}

// ParameterList: Dùng tạm để chứa danh sách tham số trước khi định nghĩa Lambda
type ParameterList struct {
	Token      Token
	Parameters []*Identifier
}

func (pl *ParameterList) expressionNode() {}
func (pl *ParameterList) String() string {
	params := []string{}
	for _, p := range pl.Parameters {
		params = append(params, p.String())
	}
	return "(" + strings.Join(params, ", ") + ")"
}

// FunctionLiteral: (x, y) => { }
type FunctionLiteral struct {
	Token      Token
	Parameters []*Identifier
	Body       *BlockStatement
	Address    int // Compiled bytecode address
	DebugName  string
}

func (fl *FunctionLiteral) expressionNode() {}
func (fl *FunctionLiteral) String() string {
	var out bytes.Buffer
	params := []string{}
	for _, p := range fl.Parameters {
		params = append(params, p.String())
	}
	out.WriteString("(")
	out.WriteString(strings.Join(params, ", "))
	out.WriteString(") ")
	out.WriteString(fl.Body.String())
	return out.String()
}

// SpawnStatement: go () => { }
type SpawnStatement struct {
	Token Token // Dấu 'go'
	Fn    Expression
}

func (ss *SpawnStatement) statementNode()  {}
func (ss *SpawnStatement) expressionNode() {}
func (ss *SpawnStatement) String() string {
	return "go " + ss.Fn.String() + ";"
}

// MethodCallExpression: object.method(args...)
type MethodCallExpression struct {
	Token     Token        // Dấu '.'
	Object    Expression   // Đối tượng (ví dụ: "hello")
	Method    *Identifier  // Tên phương thức (ví dụ: upper)
	Arguments []Expression // Các tham số truyền vào
	// Optional is object?.method(…): nothing is called, and the chain is null, when object is null.
	Optional bool
	// OptionalCall is object.method?.(…): nothing is called when object.method is null.
	OptionalCall bool
}

func (mce *MethodCallExpression) expressionNode() {}
func (mce *MethodCallExpression) String() string {
	var out bytes.Buffer
	args := []string{}
	for _, a := range mce.Arguments {
		args = append(args, a.String())
	}
	out.WriteString(mce.Object.String())
	if mce.Optional {
		out.WriteString("?.")
	} else {
		out.WriteString(".")
	}
	out.WriteString(mce.Method.String())
	if mce.OptionalCall {
		out.WriteString("?.")
	}
	out.WriteString("(")
	out.WriteString(strings.Join(args, ", "))
	out.WriteString(")")
	return out.String()
}

// TemplateLiteral: `Hello ${user.name}`
type TemplateLiteral struct {
	Token Token
	Parts []Expression // Alternating Literal (strings) and Expressions
}

func (tl *TemplateLiteral) expressionNode() {}
func (tl *TemplateLiteral) String() string {
	var out bytes.Buffer
	out.WriteString("`")
	for _, p := range tl.Parts {
		if lit, ok := p.(*Literal); ok && lit.Token.Kind == String {
			out.WriteString(lit.Value.Text())
		} else {
			out.WriteString("${")
			out.WriteString(p.String())
			out.WriteString("}")
		}
	}
	out.WriteString("`")
	return out.String()
}
