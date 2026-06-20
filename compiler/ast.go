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
	HasDefault bool      // true nếu có `export default …` (đã hạ về const DefaultExportName)
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

// VarStatement: const a = 10; let b = 20;
type VarStatement struct {
	Token        Token // const, let
	Names        []*Identifier
	Value        Expression
	DestructMode DestructMode
}

func (vs *VarStatement) statementNode() {}
func (vs *VarStatement) String() string {
	var out bytes.Buffer
	out.WriteString(vs.Token.String() + " ")

	if vs.DestructMode == DestructObject {
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

// ForStatement: for (item in list) { }
type ForStatement struct {
	Token    Token // Dấu 'for'
	Item     *Identifier
	Iterable Expression
	Body     *BlockStatement
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
	Token     Token // Dấu '('
	Function  Expression  // Tên hàm hoặc object.method
	Arguments []Expression
}

func (ce *CallExpression) expressionNode() {}
func (ce *CallExpression) String() string {
	var out bytes.Buffer
	args := []string{}
	for _, a := range ce.Arguments {
		args = append(args, a.String())
	}
	out.WriteString(ce.Function.String())
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
}

func (me *MemberExpression) expressionNode() {}
func (me *MemberExpression) String() string {
	return me.Object.String() + "." + me.Property.String()
}

// IndexExpression: array[index]
type IndexExpression struct {
	Token Token // Dấu '['
	Left  Expression
	Index Expression
}

func (ie *IndexExpression) expressionNode() {}
func (ie *IndexExpression) String() string {
	return "(" + ie.Left.String() + "[" + ie.Index.String() + "])"
}

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
	Token     Token  // Dấu '.'
	Object    Expression   // Đối tượng (ví dụ: "hello")
	Method    *Identifier  // Tên phương thức (ví dụ: upper)
	Arguments []Expression // Các tham số truyền vào
}

func (mce *MethodCallExpression) expressionNode() {}
func (mce *MethodCallExpression) String() string {
	var out bytes.Buffer
	args := []string{}
	for _, a := range mce.Arguments {
		args = append(args, a.String())
	}
	out.WriteString(mce.Object.String())
	out.WriteString(".")
	out.WriteString(mce.Method.String())
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
