package token

import (
	"fmt"

	"github.com/kitwork/engine/value"
)

type Kind uint16

const (
	Illegal Kind = iota
	EOF
	Comment

	// --- Dữ liệu (Literals) ---
	Identifier // work, db, cleanup
	Number     // 123, 0.5
	String     // "02:00", 'logs'
	Boolean    // true, false
	Null

	// --- Truy cập & Chaining ---
	Assign   // =
	Dot      // .
	FatArrow // =>

	// --- Dấu ngoặc ---
	LeftParen    // (
	RightParen   // )
	LeftBrace    // {
	RightBrace   // }
	LeftBracket  // [
	RightBracket // ]

	// --- Ngắt câu & Phân tách ---
	Comma     // ,
	Semicolon // ;
	Colon     // :

	// --- Logic & So sánh ---
	LogicalAnd // &&
	LogicalOr  // ||
	LogicalNot // !
	Equal      // ==
	NotEqual   // !=

	Greater      // >
	GreaterEqual //>=
	Less         // <
	LessEqual    //<=

	// --- Số học ---
	Plus  // +
	Minus // -
	Star  // *
	Slash // /

	// --- Từ khóa ---
	Const
	Let
	If
	Else
	Return
)

// String trả về chuỗi đại diện cho Kind (Hữu ích cho Debug/Error Reporting)
func (k Kind) String() string {
	switch k {
	case Illegal:
		return "ILLEGAL"
	case EOF:
		return "EOF"
	case Comment:
		return "COMMENT"
	case Identifier:
		return "IDENTIFIER"
	case Number:
		return "NUMBER"
	case String:
		return "STRING"
	case Boolean:
		return "BOOLEAN"
	case Null:
		return "NULL"
	case Assign:
		return "="
	case Dot:
		return "."
	case FatArrow:
		return "=>"
	case LeftParen:
		return "("
	case RightParen:
		return ")"
	case LeftBrace:
		return "{"
	case RightBrace:
		return "}"
	case LeftBracket:
		return "["
	case RightBracket:
		return "]"
	case Comma:
		return ","
	case Semicolon:
		return ";"
	case Colon:
		return ":"
	case LogicalAnd:
		return "&&"
	case LogicalOr:
		return "||"
	case LogicalNot:
		return "!"
	case Equal:
		return "=="
	case NotEqual:
		return "!="
	case Greater:
		return ">"
	case GreaterEqual:
		return ">="
	case Less:
		return "<"
	case LessEqual:
		return "<="
	case Plus:
		return "+"
	case Minus:
		return "-"
	case Star:
		return "*"
	case Slash:
		return "/"
	case Const:
		return "const"
	case Let:
		return "let"
	case If:
		return "if"
	case Else:
		return "else"
	// case For:
	// 	return "for"
	// case In:
	// 	return "in"
	case Return:
		return "return"
	// case Go:
	// 	return "go"
	// case Defer:
	// 	return "defer"
	default:
		return fmt.Sprintf("KIND(%d)", k)
	}
}

type Token struct {
	Value    value.Value
	Position int32
	Length   int16
	Kind     Kind
}

// String trả về nội dung text của Token (giúp hiển thị khi in AST)
func (t Token) String() string {
	// Nếu token có giá trị (Number, String, Ident), trả về text của giá trị đó
	if !t.Value.IsNil() {
		return t.Value.Text()
	}
	// Nếu không, trả về ký hiệu của Kind (vd: "+", "const")
	return t.Kind.String()
}

var Keywords = map[string]Kind{
	"const": Const,
	"let":   Let,
	"if":    If,
	"else":  Else,
	// "for":    For,
	// "in":     In,
	"return": Return,
	// "go":     Go,
	// "defer":  Defer,
	"true":  Boolean,
	"false": Boolean,
	"null":  Null,
}

func LookupIdentifier(ident string) Kind {
	if k, ok := Keywords[ident]; ok {
		return k
	}
	return Identifier
}

func (k Kind) Precedence() int {
	switch k {
	case Dot:
		return 12
	case LeftBracket:
		return 11
	case LeftParen:
		return 10
	case LogicalNot:
		return 9
	case Star, Slash:
		return 8
	case Plus, Minus:
		return 7
	case Greater, Less, Equal, NotEqual, GreaterEqual, LessEqual:
		return 6
	case LogicalAnd:
		return 5
	case LogicalOr:
		return 4
	case Assign:
		return 3
	default:
		return 0
	}
}

func (k Kind) IsOperator() bool {
	return k.Precedence() > 0
}
