package compiler

import (
	"sync"
	"unsafe"

	"github.com/kitwork/engine/character"
	"github.com/kitwork/engine/token"
	"github.com/kitwork/engine/value"
)

// Khai báo các giá trị toán tử dùng chung để giảm cấp phát RAM khi Stress Test
var (
	valPlus     = value.NewString("+")
	valMinus    = value.NewString("-")
	valStar     = value.NewString("*")
	valSlash    = value.NewString("/")
	valEqual    = value.NewString("==")
	valAssign   = value.NewString("=")
	valKeywords = map[token.Kind]value.Value{}
)

var lexerPool = sync.Pool{
	New: func() any {
		return &Lexer{} // Khởi tạo cấu trúc rỗng
	},
}

func init() {
	// Khởi tạo sẵn Value cho các từ khóa quan trọng
	keywords := []token.Kind{token.Const, token.Let, token.If, token.Else, token.Return}
	for _, k := range keywords {
		valKeywords[k] = value.NewString(k.String())
	}
}

func Keywords(kind token.Kind, ident string) value.Value {
	if val, ok := valKeywords[kind]; ok {
		return val
	} else {
		return value.NewString(ident)
	}
}

type Lexer struct {
	input []byte
	ch    byte // Ký tự hiện tại
	pos   int  // Vị trí của ký tự ch
	next  int  // Vị trí đọc tiếp theo
}

func NewLexer(input string) *Lexer {
	// Chuyển string thành []byte một lần duy nhất.
	// Việc này an toàn vì Lexer chỉ đọc chứ không ghi đè vào mảng này.
	l := &Lexer{input: []byte(input)}
	l.readChar()
	return l
}

func (l *Lexer) b2s(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return *(*string)(unsafe.Pointer(&b))
}

func (l *Lexer) readChar() {
	if l.next >= len(l.input) {
		l.ch = 0
	} else {
		l.ch = l.input[l.next]
	}
	l.pos = l.next
	l.next++
}

func (l *Lexer) peekChar() byte {
	if l.next >= len(l.input) {
		return 0
	}
	return l.input[l.next]
}

func (l *Lexer) NextToken() token.Token {
	var tok token.Token

	l.skipWhitespace()

	// Xử lý chú thích nhanh
	if l.ch == '/' && l.peekChar() == '/' {
		l.skipComment()
		return l.NextToken()
	}

	tok.Position = int32(l.pos)

	// Tra bảng phân loại ký tự (O(1))
	cType := character.Table[l.ch]

	switch cType {
	case character.Alpha:
		ident := l.readIdentifier()

		// 1. Kiểm tra nhanh xem có phải Keyword không
		kind := token.LookupIdentifier(ident)
		tok.Kind = kind
		tok.Length = int16(len(ident))

		// 2. Phân luồng xử lý cực nhanh
		if kind == token.Identifier {
			// Chỉ intern tên biến (x, y, myVar...)
			// Nếu Stress Test chạy đa luồng, bỏ qua intern() sẽ NHANH hơn do không bị nghẽn Lock
			// ident = l.intern(ident)
			tok.Value = value.NewString(ident)
		} else {
			// Đối với Keywords, ta dùng switch-case để gán giá trị tĩnh
			// Giúp tránh việc gọi NewString(ident) tạo object mới trên Heap
			switch kind {
			case token.Boolean:

				if ident[0] == 't' {
					tok.Value = value.TRUE
				} else {
					tok.Value = value.FALSE
				}
			case token.Null:
				tok.Value = value.NULL
			default:
				// Các từ khóa như const, let, if...
				// Nếu Parser không cần text của 'if', bạn thậm chí có thể để Value rỗng
				tok.Value = Keywords(kind, ident)
			}
		}
		return tok
	case character.Digit:
		numStr := l.readNumber()
		tok.Kind = token.Number
		tok.Value = value.ParseNumber(numStr)
		tok.Length = int16(len(numStr))
		return tok

	case character.Quote:
		if l.ch == '`' {
			tok.Kind = token.Template
		} else {
			tok.Kind = token.String
		}
		str := l.readString(l.ch)
		tok.Value = value.NewString(str)
		tok.Length = int16(len(str))
		return tok

	case character.Operator:
		switch l.ch {
		case '=':
			if l.peekChar() == '>' {
				l.readChar()
				tok.Kind = token.FatArrow
				tok.Value = value.NewString("=>")
			} else if l.peekChar() == '=' {
				l.readChar()
				tok.Kind = token.Equal
				tok.Value = valEqual
			} else {
				tok.Kind = token.Assign
				tok.Value = valAssign
			}
		case '!':
			if l.peekChar() == '=' {
				l.readChar()
				tok.Kind = token.NotEqual
				tok.Value = value.NewString("!=")
			} else {
				tok.Kind = token.LogicalNot
				tok.Value = value.NewString("!")
			}
		case '+':
			tok.Kind = token.Plus
			tok.Value = valPlus
		case '-':
			tok.Kind = token.Minus
			tok.Value = valMinus
		case '*':
			tok.Kind = token.Star
			tok.Value = valStar
		case '/':
			tok.Kind = token.Slash
			tok.Value = valSlash
		case '>':
			if l.peekChar() == '=' {
				l.readChar()
				tok.Kind = token.GreaterEqual
				tok.Value = value.NewString(">=")
			} else {
				tok.Kind = token.Greater
				tok.Value = value.NewString(">")
			}
		case '<':
			if l.peekChar() == '=' {
				l.readChar()
				tok.Kind = token.LessEqual
				tok.Value = value.NewString("<=")
			} else {
				tok.Kind = token.Less
				tok.Value = value.NewString("<")
			}
		case '(':
			tok.Kind = token.LeftParen
			tok.Value = value.NewString("(")
		case ')':
			tok.Kind = token.RightParen
			tok.Value = value.NewString(")")
		case '{':
			tok.Kind = token.LeftBrace
			tok.Value = value.NewString("{")
		case '}':
			tok.Kind = token.RightBrace
			tok.Value = value.NewString("}")
		case '[':
			tok.Kind = token.LeftBracket
			tok.Value = value.NewString("[")
		case ']':
			tok.Kind = token.RightBracket
			tok.Value = value.NewString("]")
		case ',':
			tok.Kind = token.Comma
			tok.Value = value.NewString(",")
		case ';':
			tok.Kind = token.Semicolon
			tok.Value = value.NewString(";")
		case ':':
			tok.Kind = token.Colon
			tok.Value = value.NewString(":")
		case '.':
			tok.Kind = token.Dot
			tok.Value = value.NewString(".")
		case '&':
			if l.peekChar() == '&' {
				l.readChar()
				tok.Kind = token.LogicalAnd
				tok.Value = value.NewString("&&")
			}
		case '|':
			if l.peekChar() == '|' {
				l.readChar()
				tok.Kind = token.LogicalOr
				tok.Value = value.NewString("||")
			}
		}

	case character.Space:
		if l.ch == 0 {
			tok.Kind = token.EOF
			return tok
		}

	default:
		tok.Kind = token.Illegal
	}

	l.readChar()
	tok.Length = int16(int32(l.pos) - tok.Position)
	return tok
}

// --- Helpers tối ưu với Table Lookup ---

func (l *Lexer) skipWhitespace() {
	for character.Table[l.ch] == character.Space && l.ch != 0 {
		l.readChar()
	}
}

func (l *Lexer) skipComment() {
	for l.ch != '\n' && l.ch != 0 {
		l.readChar()
	}
	l.skipWhitespace()
}

func (l *Lexer) readString(quote byte) string {
	l.readChar()
	start := l.pos
	for l.ch != quote && l.ch != 0 {
		l.readChar()
	}
	content := l.input[start:l.pos]
	l.readChar()
	return l.b2s(content)
}

func (l *Lexer) readIdentifier() string {
	start := l.pos
	for {
		cType := character.Table[l.ch]
		if cType != character.Alpha && cType != character.Digit {
			break
		}
		l.readChar()
	}

	// Thay vì: return string(l.input[start:l.pos]) -> Tốn thời gian copy
	// Hãy dùng:
	slice := l.input[start:l.pos]
	return l.b2s(slice) // Biến slice thành string ngay lập tức với 0 allocs
}

func (l *Lexer) readNumber() string {
	start := l.pos
	for character.Table[l.ch] == character.Digit || l.ch == '.' {
		l.readChar()
	}

	content := l.input[start:l.pos]

	return l.b2s(content)
}
