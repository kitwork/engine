package compiler

import (
	"fmt"
	"strings"

	
	"github.com/kitwork/engine/value"
)

// Bảng độ ưu tiên
const (
	_ int = iota
	LOWEST
	ASSIGN      // =
	OR          // ||
	AND         // &&
	EQUALS      // ==
	LESSGREATER // > hoặc <
	SUM         // +
	PRODUCT     // *
	PREFIX      // -X hoặc !X
	CALL        // f(x)
	INDEX       // a[0]
	MEMBER      // a.b
	ARROW       // =>
)

var precedences = map[Kind]int{
	Equal:        EQUALS,
	NotEqual:     EQUALS,
	Less:         LESSGREATER,
	Greater:      LESSGREATER,
	LessEqual:    LESSGREATER,
	GreaterEqual: LESSGREATER,
	Plus:         SUM,
	Minus:        SUM,
	Star:         PRODUCT,
	Slash:        PRODUCT,
	Percent:      PRODUCT,
	Question:     ASSIGN,
	PlusAssign:   ASSIGN,
	MinusAssign:  ASSIGN,
	StarAssign:   ASSIGN,
	SlashAssign:  ASSIGN,
	PlusPlus:     CALL,
	MinusMinus:   CALL,
	LeftParen:    CALL,
	LeftBracket:  INDEX,
	Dot:          MEMBER,
	Assign:       ASSIGN,
	LogicalAnd:   AND,
	LogicalOr:    OR,
	FatArrow:     ARROW,
}

type (
	prefixParseFn func() Expression
	infixParseFn  func(Expression) Expression
)

type Parser struct {
	l      *Lexer
	errors []string

	curToken  Token
	peekToken Token

	prefixParseFns map[Kind]prefixParseFn
	infixParseFns  map[Kind]infixParseFn

	// Module metadata thu thập khi parse (cho bundler native ở package script).
	exports    []string // tên export qua `export const/function` / `export { }`
	hasDefault bool      // có `export default …` (đã hạ về const DefaultExportName)
}

func NewParser(l *Lexer) *Parser {
	p := &Parser{l: l, errors: []string{}}

	p.prefixParseFns = make(map[Kind]prefixParseFn)
	p.infixParseFns = make(map[Kind]infixParseFn)

	// Đăng ký Prefix
	p.registerPrefix(Ident, p.parseIdentifier)
	p.registerPrefix(Number, p.parseLiteral)
	p.registerPrefix(String, p.parseLiteral)
	p.registerPrefix(Boolean, p.parseLiteral)
	p.registerPrefix(Null, p.parseLiteral)
	p.registerPrefix(Template, p.parseTemplateLiteral)
	p.registerPrefix(LogicalNot, p.parsePrefixExpression)
	p.registerPrefix(Minus, p.parsePrefixExpression)
	p.registerPrefix(LeftParen, p.parseGroupedExpression)
	p.registerPrefix(If, p.parseIfExpression)
	// p.registerPrefix(For, p.parseForStatement)
	// p.registerPrefix(Defer, p.parseDeferStatement)
	// p.registerPrefix(Go, p.parseSpawnStatement)
	p.registerPrefix(LeftBracket, p.parseArrayLiteral)
	p.registerPrefix(LeftBrace, p.parseObjectLiteral)
	p.registerPrefix(Function, p.parseFunctionExpression)
	p.registerPrefix(New, p.parseNewExpression)
	p.registerPrefix(PlusPlus, p.parsePrefixUpdate)
	p.registerPrefix(MinusMinus, p.parsePrefixUpdate)
	p.registerPrefix(Void, p.parsePrefixExpression)
	p.registerPrefix(Reserved, p.parseReservedKeyword)

	// Đăng ký Infix
	p.registerInfix(Plus, p.parseInfixExpression)
	p.registerInfix(Minus, p.parseInfixExpression)
	p.registerInfix(Star, p.parseInfixExpression)
	p.registerInfix(Slash, p.parseInfixExpression)
	p.registerInfix(Percent, p.parseInfixExpression)
	p.registerInfix(Equal, p.parseInfixExpression)
	p.registerInfix(NotEqual, p.parseInfixExpression)
	p.registerInfix(Less, p.parseInfixExpression)
	p.registerInfix(Greater, p.parseInfixExpression)
	p.registerInfix(LessEqual, p.parseInfixExpression)
	p.registerInfix(GreaterEqual, p.parseInfixExpression)
	p.registerInfix(Assign, p.parseInfixExpression)
	p.registerInfix(LeftParen, p.parseCallExpression)
	p.registerInfix(Dot, p.parseDotExpression)
	p.registerInfix(LeftBracket, p.parseIndexExpression)
	p.registerInfix(LogicalAnd, p.parseInfixExpression)
	p.registerInfix(LogicalOr, p.parseInfixExpression)
	p.registerInfix(FatArrow, p.parseArrowFunction)
	p.registerInfix(Question, p.parseTernaryExpression)
	p.registerInfix(PlusAssign, p.parseCompoundAssignment)
	p.registerInfix(MinusAssign, p.parseCompoundAssignment)
	p.registerInfix(StarAssign, p.parseCompoundAssignment)
	p.registerInfix(SlashAssign, p.parseCompoundAssignment)
	p.registerInfix(PlusPlus, p.parsePostfixUpdate)
	p.registerInfix(MinusMinus, p.parsePostfixUpdate)

	p.nextToken()
	p.nextToken()
	return p
}

/* =============================================================================
   1. STATEMENTS (Câu lệnh)
   ============================================================================= */

func (p *Parser) ParseProgram() *Program {
	program := &Program{Statements: []Statement{}}
	for p.curToken.Kind != EOF {
		// 1. Bỏ qua dấu chấm phẩy thừa
		if p.curToken.Kind == Semicolon {
			p.nextToken()
			continue
		}

		stmt := p.parseStatement()
		if stmt != nil {
			program.Statements = append(program.Statements, stmt)
		}

		// 2. CHỈ GỌI nextToken Ở ĐÂY để chuẩn bị cho dòng tiếp theo
		p.nextToken()
	}
	program.Exports = p.exports
	program.HasDefault = p.hasDefault
	return program
}

func (p *Parser) parseStatement() Statement {
	switch p.curToken.Kind {
	case Let, Const:
		return p.parseVarStatement()
	case Import:
		return p.parseImportStatement()
	case Export:
		return p.parseExportStatement()
	case Return:
		return p.parseReturnStatement()
	case Function:
		return p.parseFunctionStatement()
	case For:
		return p.parseForStatement()
	default:
		return p.parseExpressionStatement()
	}

}

// Trong parser.go
func (p *Parser) parseVarStatement() Statement {
	stmt := &VarStatement{Token: p.curToken}

	if p.peekTokenIs(LeftBrace) {
		// Destructuring Object: const { a, b } = ...
		p.nextToken() // cur: {
		stmt.DestructMode = DestructObject
		stmt.Names = []*Identifier{}
		for !p.peekTokenIs(RightBrace) {
			p.nextToken() // cur: identifier
			if !p.curTokenIs(Ident) {
				return nil
			}
			stmt.Names = append(stmt.Names, &Identifier{Token: p.curToken, Value: p.curToken.Value.Text()})
			if p.peekTokenIs(Comma) {
				p.nextToken()
			}
		}
		if !p.expectPeek(RightBrace) {
			return nil
		}
	} else if p.peekTokenIs(LeftBracket) {
		// Destructuring Array: const [ a, b ] = ...
		p.nextToken() // cur: [
		stmt.DestructMode = DestructArray
		stmt.Names = []*Identifier{}
		for !p.peekTokenIs(RightBracket) {
			p.nextToken() // cur: identifier
			if !p.curTokenIs(Ident) {
				return nil
			}
			stmt.Names = append(stmt.Names, &Identifier{Token: p.curToken, Value: p.curToken.Value.Text()})
			if p.peekTokenIs(Comma) {
				p.nextToken()
			}
		}
		if !p.expectPeek(RightBracket) {
			return nil
		}
	} else {
		// Standard: const a = ...
		if !p.expectPeek(Ident) {
			return nil
		}
		stmt.Names = []*Identifier{{Token: p.curToken, Value: p.curToken.Value.Text()}}
		stmt.DestructMode = DestructNone
	}

	if !p.expectPeek(Assign) {
		return nil
	}
	// Lúc này curToken đang là '='

	p.nextToken() // Nhảy qua dấu '=' để đứng tại vị trí của giá trị (VD: số 10)

	stmt.Value = p.parseExpression(LOWEST)

	return stmt
}

/* =============================================================================
   MODULES — native import / export

   Strategy: lower to existing AST nodes, reuse all existing machinery.
     import { router, log } from "kitwork"   →  const { router, log } = kitwork()
     import http from "kitwork/http"          →  const http = kitwork().http
     export const x = ...                      →  const x = ...   (export stripped)
     export default expr                       →  expr;           (evaluated)
     export { a, b }                           →  (no-op)
   Anything the native path can't express (relative modules, `as` aliases, …)
   records a parser error so script.Bytecode() falls back to esbuild bundling.
   ============================================================================= */

func isKitworkSpecifier(s string) bool {
	return s == "kitwork" || strings.HasPrefix(s, "kitwork/")
}

func kitworkSubpath(s string) string {
	if strings.HasPrefix(s, "kitwork/") {
		return s[len("kitwork/"):]
	}
	return ""
}

// isRelativeSpecifier báo specifier là module tương đối (file trong tenant)
// → sẽ phát ImportStatement cho bundler native giải quyết.
func isRelativeSpecifier(s string) bool {
	return strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") || strings.HasPrefix(s, "/")
}

// isAppSpecifier báo specifier là module APP-SHARED: bắt đầu bằng `_` (vd `_core/github.kitwork.js`).
// Bundler resolve bằng cách ĐI NGƯỢC LÊN từ file import — bắt gặp `_core` ở cấp domain trước, rồi cấp
// identity (apps/<identity>/_core). Nhờ vậy các domain của một app DÙNG CHUNG `_core` mà không cần
// đường dẫn tương đối `../../_core`, và domain vẫn override được bằng `_core` riêng. Không đụng import
// tương đối cũ (`../_core` vẫn chạy y nguyên).
func isAppSpecifier(s string) bool {
	return strings.HasPrefix(s, "_")
}

// hasAlias báo có ít nhất một binding đổi tên (`imported as local`).
func hasAlias(specs []ImportSpec) bool {
	for _, s := range specs {
		if s.Local != s.Imported {
			return true
		}
	}
	return false
}

// memberConst dựng `const <local> = <obj>.<prop>`.
func memberConst(local string, obj Expression, prop string) Statement {
	return &VarStatement{
		Token:        constToken(),
		Names:        []*Identifier{{Token: Token{Kind: Ident, Value: value.NewString(local)}, Value: local}},
		DestructMode: DestructNone,
		Value: &MemberExpression{
			Token:    Token{Kind: Dot},
			Object:   obj,
			Property: &Identifier{Token: Token{Kind: Ident, Value: value.NewString(prop)}, Value: prop},
		},
	}
}

// constToken builds a synthetic `const` token for lowered declarations.
func constToken() Token {
	return Token{Kind: Const, Value: value.NewString("const")}
}

// kitworkCall builds the expression `kitwork()`.
func (p *Parser) kitworkCall() Expression {
	return &CallExpression{
		Token:     Token{Kind: LeftParen},
		Function:  &Identifier{Token: Token{Kind: Ident, Value: value.NewString("kitwork")}, Value: "kitwork"},
		Arguments: nil,
	}
}

// parseFromSpecifier consumes a contextual `from "specifier"` and returns the
// specifier string. `from` is contextual (a normal identifier), not a keyword.
func (p *Parser) parseFromSpecifier() (string, bool) {
	if !p.peekTokenIs(Ident) || p.peekToken.Value.Text() != "from" {
		p.errors = append(p.errors, "import: expected 'from'")
		return "", false
	}
	p.nextToken() // cur: from
	if !p.expectPeek(String) {
		return "", false
	}
	return p.curToken.Value.Text(), true
}

func (p *Parser) parseImportStatement() Statement {
	// curToken == import
	importTok := p.curToken

	// Side-effect:  import "./mod"
	if p.peekTokenIs(String) {
		p.nextToken() // cur: string
		spec := p.curToken.Value.Text()
		if isRelativeSpecifier(spec) || isAppSpecifier(spec) {
			return &ImportStatement{Token: importTok, Source: spec, SideEffect: true}
		}
		p.errors = append(p.errors, fmt.Sprintf("native import: unsupported side-effect specifier %q", spec))
		return nil
	}

	// Named:   import { a, b as c } from "..."
	if p.peekTokenIs(LeftBrace) {
		p.nextToken() // cur: {
		specs := []ImportSpec{}
		for !p.peekTokenIs(RightBrace) {
			p.nextToken() // cur: tên import
			if !p.curTokenIs(Ident) {
				p.errors = append(p.errors, "import: expected identifier inside { }")
				return nil
			}
			imported := p.curToken.Value.Text()
			local := imported
			// alias tùy chọn: `as local` (`as` là identifier theo ngữ cảnh)
			if p.peekTokenIs(Ident) && p.peekToken.Value.Text() == "as" {
				p.nextToken() // cur: as
				if !p.expectPeek(Ident) {
					return nil
				}
				local = p.curToken.Value.Text() // cur: local
			}
			specs = append(specs, ImportSpec{Imported: imported, Local: local})
			if p.peekTokenIs(Comma) {
				p.nextToken()
			} else if !p.peekTokenIs(RightBrace) {
				p.errors = append(p.errors, "import: unexpected token in named import")
				return nil
			}
		}
		if !p.expectPeek(RightBrace) {
			return nil
		}
		spec, ok := p.parseFromSpecifier()
		if !ok {
			return nil
		}
		if isKitworkSpecifier(spec) {
			if !hasAlias(specs) {
				// → const { a, b } = kitwork()
				names := make([]*Identifier, len(specs))
				for i, s := range specs {
					names[i] = &Identifier{Token: Token{Kind: Ident, Value: value.NewString(s.Local)}, Value: s.Local}
				}
				return &VarStatement{Token: constToken(), Names: names, DestructMode: DestructObject, Value: p.kitworkCall()}
			}
			// có alias → nhóm `const local = kitwork().imported`
			stmts := make([]Statement, len(specs))
			for i, s := range specs {
				stmts[i] = memberConst(s.Local, p.kitworkCall(), s.Imported)
			}
			return &GroupStatement{Statements: stmts}
		}
		if isRelativeSpecifier(spec) || isAppSpecifier(spec) {
			return &ImportStatement{Token: importTok, Names: specs, Source: spec}
		}
		p.errors = append(p.errors, fmt.Sprintf("native import: only 'kitwork', relative, or app-shared (_core/…) modules supported: %q", spec))
		return nil
	}

	// Default:  import name from "..."
	if p.peekTokenIs(Ident) {
		p.nextToken() // cur: local name
		name := &Identifier{Token: p.curToken, Value: p.curToken.Value.Text()}
		spec, ok := p.parseFromSpecifier()
		if !ok {
			return nil
		}
		if isKitworkSpecifier(spec) {
			sub := kitworkSubpath(spec)
			if sub == "" {
				p.errors = append(p.errors, "native import: bare \"kitwork\" has no default export")
				return nil
			}
			// → const name = kitwork().sub
			return &VarStatement{
				Token:        constToken(),
				Names:        []*Identifier{name},
				DestructMode: DestructNone,
				Value: &MemberExpression{
					Token:    Token{Kind: Dot},
					Object:   p.kitworkCall(),
					Property: &Identifier{Token: Token{Kind: Ident, Value: value.NewString(sub)}, Value: sub},
				},
			}
		}
		if isRelativeSpecifier(spec) {
			return &ImportStatement{Token: importTok, Default: name, Source: spec}
		}
		p.errors = append(p.errors, fmt.Sprintf("native import: only 'kitwork' or relative modules supported: %q", spec))
		return nil
	}

	p.errors = append(p.errors, "import: unsupported form")
	return nil
}

func (p *Parser) parseExportStatement() Statement {
	// curToken == export

	// export default <expr>  →  const __kw_default = <expr>
	// (giữ side-effect; bundler đưa __kw_default vào object export dưới khóa "default")
	if p.peekTokenIs(Ident) && p.peekToken.Value.Text() == "default" {
		p.nextToken() // cur: default
		p.nextToken() // cur: start of expression
		p.hasDefault = true
		return &VarStatement{
			Token:        constToken(),
			Names:        []*Identifier{{Token: Token{Kind: Ident, Value: value.NewString(DefaultExportName)}, Value: DefaultExportName}},
			DestructMode: DestructNone,
			Value:        p.parseExpression(LOWEST),
		}
	}

	// export const / export let  → ghi nhận các tên được khai báo
	if p.peekTokenIs(Const) || p.peekTokenIs(Let) {
		p.nextToken() // cur: const/let
		stmt := p.parseVarStatement()
		if vs, ok := stmt.(*VarStatement); ok {
			for _, n := range vs.Names {
				p.exports = append(p.exports, n.Value)
			}
		}
		return stmt
	}

	// export function f(){}  → ghi nhận tên hàm
	if p.peekTokenIs(Function) {
		p.nextToken() // cur: function
		if p.peekTokenIs(Ident) {
			p.exports = append(p.exports, p.peekToken.Value.Text())
		}
		return p.parseFunctionStatement()
	}

	// export { a, b }  — re-export local đã khai báo: ghi nhận tên, không sinh lệnh
	if p.peekTokenIs(LeftBrace) {
		p.nextToken() // cur: {
		for !p.curTokenIs(RightBrace) && !p.curTokenIs(EOF) {
			if p.curTokenIs(Ident) {
				p.exports = append(p.exports, p.curToken.Value.Text())
			}
			p.nextToken()
		}
		return nil
	}

	p.errors = append(p.errors, "export: unsupported form")
	return nil
}

func (p *Parser) parseReturnStatement() Statement {
	stmt := &ReturnStatement{Token: p.curToken}
	p.nextToken()

	if p.curTokenIs(Semicolon) || p.curTokenIs(RightBrace) || p.curTokenIs(EOF) {
		return stmt
	}
	stmt.ReturnValue = p.parseExpression(LOWEST)
	return stmt
}

func (p *Parser) parseExpressionStatement() Statement {
	stmt := &ExpressionStatement{Token: p.curToken}
	stmt.Expression = p.parseExpression(LOWEST)
	// if p.peekTokenIs(Semicolon) {
	// 	p.nextToken()
	// }
	return stmt
}

func (p *Parser) parseBlockStatement() *BlockStatement {
	block := &BlockStatement{Token: p.curToken, Statements: []Statement{}}
	p.nextToken() // Bỏ dấu {

	for !p.curTokenIs(RightBrace) && !p.curTokenIs(EOF) {
		// Bỏ qua dấu chấm phẩy thừa
		if p.curTokenIs(Semicolon) {
			p.nextToken()
			continue
		}

		stmt := p.parseStatement()
		if stmt != nil {
			block.Statements = append(block.Statements, stmt)
		}
		p.nextToken()
	}
	return block
}

/* =============================================================================
   2. EXPRESSIONS (Biểu thức)
   ============================================================================= */

func (p *Parser) parseExpression(precedence int) Expression {
	prefix := p.prefixParseFns[p.curToken.Kind]
	if prefix == nil {
		p.addError(fmt.Sprintf("no prefix parse function for %s", p.curToken.Kind))
		return nil
	}
	leftExp := prefix()

	for !p.peekTokenIs(Semicolon) && precedence < p.peekPrecedence() {
		infix := p.infixParseFns[p.peekToken.Kind]
		if infix == nil {
			return leftExp
		}
		p.nextToken()
		leftExp = infix(leftExp)
	}
	return leftExp
}

func (p *Parser) parseIdentifier() Expression {
	return &Identifier{Token: p.curToken, Value: p.curToken.Value.Text()}
}

func (p *Parser) parseLiteral() Expression {
	return &Literal{Token: p.curToken, Value: p.curToken.Value}
}

func (p *Parser) parseTemplateLiteral() Expression {
	fullText := p.curToken.Value.Text()
	tl := &TemplateLiteral{Token: p.curToken, Parts: []Expression{}}

	start := 0
	for i := 0; i < len(fullText); i++ {
		// Look for ${
		if i+1 < len(fullText) && fullText[i] == '$' && fullText[i+1] == '{' {
			// 1. Add previous string part if exists
			if i > start {
				tl.Parts = append(tl.Parts, &Literal{
					Token: Token{Kind: String},
					Value: value.NewString(fullText[start:i]),
				})
			}

			// 2. Parse expression inside ${ }
			exprStr := ""
			i += 2 // skip ${
			braceCount := 1
			exprStart := i
			for i < len(fullText) && braceCount > 0 {
				if fullText[i] == '{' {
					braceCount++
				} else if fullText[i] == '}' {
					braceCount--
				}
				if braceCount > 0 {
					i++
				}
			}
			exprStr = fullText[exprStart:i]

			// Sub-parse the expression
			subLexer := NewLexer(exprStr)
			subParser := NewParser(subLexer)
			expr := subParser.parseExpression(LOWEST)
			if expr != nil {
				tl.Parts = append(tl.Parts, expr)
			}

			start = i + 1 // skip }
		}
	}

	// Add trailing string part
	if start < len(fullText) {
		tl.Parts = append(tl.Parts, &Literal{
			Token: Token{Kind: String},
			Value: value.NewString(fullText[start:]),
		})
	}

	return tl
}

func (p *Parser) parseInfixExpression(left Expression) Expression {
	// Xử lý toán tử gán riêng biệt
	if p.curToken.Kind == Assign {
		ae := &AssignmentExpression{Token: p.curToken, Name: left}
		p.nextToken()
		ae.Value = p.parseExpression(ASSIGN - 1)
		return ae
	}

	exp := &InfixExpression{
		Token:    p.curToken,
		Operator: p.curToken.Value.Text(),
		Left:     left,
	}

	prec := p.curPrecedence()
	p.nextToken()
	exp.Right = p.parseExpression(prec)
	return exp
}

func (p *Parser) parseDotExpression(left Expression) Expression {
	tok := p.curToken // Dấu '.'
	p.nextToken()     // Sang tên phương thức/thuộc tính

	// Tạo Identifier từ Token hiện tại
	name := &Identifier{
		Token: p.curToken,
		Value: p.curToken.Value.Text(),
	}

	// Kiểm tra xem có phải là gọi Method không: object.method(...)
	if p.peekTokenIs(LeftParen) {
		p.nextToken() // Chuyển curToken sang dấu '('
		return &MethodCallExpression{
			Token:     tok,
			Object:    left,
			Method:    name,
			Arguments: p.parseExpressionList(RightParen),
		}
	}

	// Nếu không có dấu '(', đây là truy cập thuộc tính bình thường
	return &MemberExpression{
		Token:    tok,
		Object:   left,
		Property: name,
	}
}

func (p *Parser) parsePrefixExpression() Expression {
	exp := &PrefixExpression{
		Token:    p.curToken,
		Operator: p.curToken.Value.Text(),
	}
	p.nextToken()
	exp.Right = p.parseExpression(PREFIX)
	return exp
}

// parseNewExpression xử lý cú pháp `new Expr(...)` của JavaScript.
// Kitwork không dùng prototype-based constructor — các builtin (Date, ...)
// tự trả về object khi được gọi, nên `new` chỉ là tiền tố tương thích cú pháp
// và biểu thức phía sau được biên dịch như một lời gọi hàm bình thường.
func (p *Parser) parseNewExpression() Expression {
	p.nextToken() // bỏ qua từ khóa 'new'
	return p.parseExpression(PREFIX)
}

// parseTernaryExpression xử lý `cond ? consequence : alternative`.
func (p *Parser) parseTernaryExpression(cond Expression) Expression {
	exp := &TernaryExpression{Token: p.curToken, Condition: cond}

	p.nextToken()
	exp.Consequence = p.parseExpression(ASSIGN - 1)

	if !p.expectPeek(Colon) {
		return nil
	}

	p.nextToken()
	// ASSIGN-1 cho phép ternary lồng nhau kết hợp phải: a ? b : c ? d : e
	exp.Alternative = p.parseExpression(ASSIGN - 1)
	return exp
}

// parseCompoundAssignment desugar `x += y` thành `x = x + y` (tương tự -=, *=, /=)
// nên không cần opcode mới — tái dùng đường biên dịch Assignment + Infix sẵn có.
func (p *Parser) parseCompoundAssignment(left Expression) Expression {
	tok := p.curToken
	baseOp := string(tok.Value.Text()[0]) // "+=" -> "+"

	p.nextToken()
	right := p.parseExpression(ASSIGN - 1)

	return &AssignmentExpression{
		Token: tok,
		Name:  left,
		Value: &InfixExpression{Token: tok, Left: left, Operator: baseOp, Right: right},
	}
}

// updateExpression dựng `target = target ± 1` dùng chung cho ++ và --.
func updateExpression(tok Token, target Expression) Expression {
	op := "+"
	if tok.Kind == MinusMinus {
		op = "-"
	}
	one := &Literal{Token: Token{Kind: Number}, Value: value.New(1)}
	return &AssignmentExpression{
		Token: tok,
		Name:  target,
		Value: &InfixExpression{Token: tok, Left: target, Operator: op, Right: one},
	}
}

// parsePostfixUpdate xử lý `i++` / `i--`.
// Lưu ý: biểu thức trả về giá trị MỚI (khác JS trả giá trị cũ) — dùng như
// câu lệnh độc lập thì hành vi giống hệt JS.
func (p *Parser) parsePostfixUpdate(left Expression) Expression {
	return updateExpression(p.curToken, left)
}

// parsePrefixUpdate xử lý `++i` / `--i`.
func (p *Parser) parsePrefixUpdate() Expression {
	tok := p.curToken
	p.nextToken()
	target := p.parseExpression(PREFIX)
	return updateExpression(tok, target)
}

// parseReservedKeyword báo lỗi biên dịch thân thiện cho các từ khóa bị loại bỏ
// có chủ đích khỏi ngôn ngữ — kèm hướng dẫn cách viết thay thế theo triết lý Kitwork.
func (p *Parser) parseReservedKeyword() Expression {
	word := p.curToken.Value.Text()
	switch word {
	case "while", "do":
		p.addError(fmt.Sprintf("Kitwork không hỗ trợ vòng điều kiện tuỳ ý '%s' (có thể lặp vô tận). Hãy dùng vòng ĐẾM 'for (let i = 0; i < n; i++)', duyệt 'for (const x of arr)', hoặc .map()/.filter()/.find() trên mảng.", word))
	case "try", "catch", "finally", "throw":
		p.addError(fmt.Sprintf("Kitwork không hỗ trợ '%s' (loại bỏ có chủ đích cho đơn giản). Hãy dùng chuỗi .done(callback) / .fail(callback) để xử lý kết quả và lỗi.", word))
	case "switch":
		p.addError("Kitwork không hỗ trợ 'switch'. Hãy dùng if / else hoặc tra cứu qua object map.")
	case "class":
		p.addError("Kitwork không hỗ trợ 'class'. Hãy dùng object literal và arrow function.")
	default:
		p.addError(fmt.Sprintf("Từ khóa '%s' không được hỗ trợ trong Kitwork.", word))
	}
	return nil
}

func (p *Parser) parseIfExpression() Expression {
	exp := &IfExpression{Token: p.curToken}
	if !p.expectPeek(LeftParen) {
		return nil
	}
	p.nextToken()
	exp.Condition = p.parseExpression(LOWEST)
	if !p.expectPeek(RightParen) {
		return nil
	}

	if p.peekTokenIs(LeftBrace) {
		p.nextToken()
		exp.Consequence = p.parseBlockStatement()
	} else {
		p.nextToken()
		stmt := p.parseStatement()
		exp.Consequence = &BlockStatement{Statements: []Statement{stmt}}
	}

	if p.peekTokenIs(Else) {
		p.nextToken()
		if p.peekTokenIs(LeftBrace) {
			p.nextToken()
			exp.Alternative = p.parseBlockStatement()
		} else {
			p.nextToken()
			stmt := p.parseStatement()
			exp.Alternative = &BlockStatement{Statements: []Statement{stmt}}
		}
	}
	return exp
}

// parseForStatement accepts EXACTLY two shapes, and rejects everything else with a friendly error:
//
//	for (let i = 0; i < n; i++) { … }   // a BOUNDED counting loop  → ForRangeStatement
//	for (const x of arr) { … }          // iterate a collection      → ForStatement (ITER)
//
// It deliberately refuses for(;;) / for(; cond ;) — an arbitrary condition loop is `while` in
// disguise, and Kitwork's guarantee is "no infinite loop by construction". The counter must be
// declared (let/const), the condition must compare THAT counter, and the update must mutate it.
func (p *Parser) parseForStatement() Statement {
	forTok := p.curToken
	if !p.expectPeek(LeftParen) {
		return nil
	}

	// Reject the empty / condition-only forms up front.
	if p.peekTokenIs(Semicolon) || p.peekTokenIs(RightParen) {
		p.addError("Kitwork chỉ hỗ trợ vòng ĐẾM: for (let i = 0; i < n; i++). Dạng for(;;) / for(; điều_kiện ;) không được phép — đó là 'while' trá hình (dùng vòng đếm, hoặc .map()/.filter()).")
		return nil
	}

	// The counter MUST be declared with let/const.
	if !p.peekTokenIs(Let) && !p.peekTokenIs(Const) {
		p.addError("Biến đếm của for phải khai báo bằng 'let'/'const': for (let i = 0; i < n; i++) hoặc for (const x of arr).")
		return nil
	}
	declTok := p.peekToken
	p.nextToken() // cur: let/const
	if !p.expectPeek(Ident) {
		return nil
	}
	counter := &Identifier{Token: p.curToken, Value: p.curToken.Value.Text()}

	// Disambiguate on the token after the counter name: '=' → counting loop, 'of' → iteration.
	switch {
	case p.peekTokenIs(Assign):
		return p.parseCountedFor(forTok, declTok, counter)
	case p.peekTokenIs(Ident) && p.peekToken.Value.Text() == "of":
		return p.parseForOf(forTok, counter)
	default:
		p.addError(fmt.Sprintf("Cú pháp for không hợp lệ sau '%s'. Dùng vòng đếm 'for (let %s = 0; %s < n; %s++)' hoặc duyệt 'for (const %s of arr)'.",
			counter.Value, counter.Value, counter.Value, counter.Value, counter.Value))
		return nil
	}
}

// parseCountedFor parses `for (<decl> i = <init> ; <cond> ; <update>)` and validates that <cond>
// compares the counter and <update> mutates it — so the loop is bounded by construction.
func (p *Parser) parseCountedFor(forTok, declTok Token, counter *Identifier) Statement {
	stmt := &ForRangeStatement{Token: forTok, Counter: counter.Value}

	// Init:  let i = <expr>
	p.nextToken() // cur: '='
	p.nextToken() // cur: init expression
	init := p.parseExpression(LOWEST)
	stmt.Init = &VarStatement{Token: declTok, Names: []*Identifier{counter}, Value: init, DestructMode: DestructNone}
	if !p.expectPeek(Semicolon) {
		return nil
	}

	// Cond:  i < n  (must compare the counter)
	p.nextToken()
	stmt.Cond = p.parseExpression(LOWEST)
	if !condRefsCounter(stmt.Cond, counter.Value) {
		p.addError(fmt.Sprintf("Điều kiện for phải so sánh biến đếm '%s' (ví dụ '%s < n', '%s >= 0'). Điều kiện tuỳ ý là 'while' trá hình và không được phép.", counter.Value, counter.Value, counter.Value))
		return nil
	}
	if !p.expectPeek(Semicolon) {
		return nil
	}

	// Update:  i++ / i-- / i += k / i -= k  (must mutate the counter)
	p.nextToken()
	stmt.Update = p.parseExpression(LOWEST)
	if !updateMutatesCounter(stmt.Update, counter.Value) {
		p.addError(fmt.Sprintf("Bước nhảy for phải cập nhật biến đếm '%s' (%s++, %s--, %s += n).", counter.Value, counter.Value, counter.Value, counter.Value))
		return nil
	}

	if !p.expectPeek(RightParen) {
		return nil
	}
	if !p.expectPeek(LeftBrace) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

// parseForOf parses `for (<decl> x of <iterable>)` — bounded iteration over a collection.
func (p *Parser) parseForOf(forTok Token, item *Identifier) Statement {
	stmt := &ForStatement{Token: forTok, Item: item}
	p.nextToken() // cur: 'of'
	p.nextToken() // cur: iterable expression
	stmt.Iterable = p.parseExpression(LOWEST)
	if !p.expectPeek(RightParen) {
		return nil
	}
	if !p.expectPeek(LeftBrace) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

// condRefsCounter reports whether a for-condition is a comparison against the loop counter.
func condRefsCounter(e Expression, name string) bool {
	inf, ok := e.(*InfixExpression)
	if !ok {
		return false
	}
	switch inf.Operator {
	case "<", "<=", ">", ">=", "!=":
		return isIdent(inf.Left, name) || isIdent(inf.Right, name)
	}
	return false
}

// updateMutatesCounter reports whether a for-update assigns/updates the loop counter (i++/i+=k/…).
func updateMutatesCounter(e Expression, name string) bool {
	as, ok := e.(*AssignmentExpression)
	return ok && isIdent(as.Name, name)
}

func isIdent(e Expression, name string) bool {
	id, ok := e.(*Identifier)
	return ok && id.Value == name
}

func (p *Parser) parseDeferStatement() Expression {
	stmt := &DeferStatement{Token: p.curToken}
	p.nextToken()
	stmt.Fn = p.parseExpression(LOWEST)
	return stmt
}

func (p *Parser) parseSpawnStatement() Expression {
	stmt := &SpawnStatement{Token: p.curToken}
	p.nextToken()
	stmt.Fn = p.parseExpression(LOWEST)
	return stmt
}

func (p *Parser) parseArrayLiteral() Expression {
	return &ArrayLiteral{
		Token:    p.curToken,
		Elements: p.parseExpressionList(RightBracket),
	}
}

func (p *Parser) parseObjectLiteral() Expression {
	obj := &ObjectLiteral{Token: p.curToken, Entries: []ObjectEntry{}}
	for !p.peekTokenIs(RightBrace) {
		p.nextToken()

		if p.curTokenIs(Spread) {
			p.nextToken()
			val := p.parseExpression(LOWEST)
			obj.Entries = append(obj.Entries, ObjectEntry{
				Value:    val,
				IsSpread: true,
			})
		} else {
			key := p.parseExpression(LOWEST)
			if p.peekTokenIs(Colon) {
				p.nextToken()
				p.nextToken()
				val := p.parseExpression(LOWEST)
				obj.Entries = append(obj.Entries, ObjectEntry{
					Key:   key,
					Value: val,
				})
			} else {
				// Shorthand: { name } -> { name: name }
				obj.Entries = append(obj.Entries, ObjectEntry{
					Key:   key,
					Value: key,
				})
			}
		}

		if !p.peekTokenIs(RightBrace) && !p.expectPeek(Comma) {
			return nil
		}
	}
	if !p.expectPeek(RightBrace) {
		return nil
	}
	return obj
}

func (p *Parser) parseCallExpression(left Expression) Expression {
	exp := &CallExpression{Token: p.curToken, Function: left}
	exp.Arguments = p.parseExpressionList(RightParen)
	return exp
}

func (p *Parser) parseIndexExpression(left Expression) Expression {
	exp := &IndexExpression{Token: p.curToken, Left: left}
	p.nextToken()
	exp.Index = p.parseExpression(LOWEST)
	if !p.expectPeek(RightBracket) {
		return nil
	}
	return exp
}

func (p *Parser) parseGroupedExpression() Expression {
	// Nếu là () => ...
	if p.peekTokenIs(RightParen) {
		p.nextToken() // Sang dấu )
		return &ParameterList{Token: p.curToken, Parameters: []*Identifier{}}
	}

	exps := p.parseExpressionList(RightParen)

	// Nếu tiếp sau là =>, biến list này thành ParameterList
	if p.peekTokenIs(FatArrow) {
		params := make([]*Identifier, len(exps))
		for i, e := range exps {
			if id, ok := e.(*Identifier); ok {
				params[i] = id
			}
		}
		return &ParameterList{Token: p.curToken, Parameters: params}
	}

	if len(exps) == 0 {
		return nil
	}
	return exps[0]
}

func (p *Parser) parseArrowFunction(left Expression) Expression {
	tok := p.curToken // =>
	p.nextToken()

	var params []*Identifier
	if id, ok := left.(*Identifier); ok {
		params = []*Identifier{id}
	} else if pl, ok := left.(*ParameterList); ok {
		params = pl.Parameters
	}

	// Xử lý block { } hoặc single expression
	if p.curTokenIs(LeftBrace) {
		return &FunctionLiteral{
			Token:      tok,
			Parameters: params,
			Body:       p.parseBlockStatement(),
		}
	}

	// Single expression body: a => a * 2 -> { return a * 2 }
	body := &BlockStatement{
		Statements: []Statement{
			&ReturnStatement{
				ReturnValue: p.parseExpression(LOWEST),
			},
		},
	}

	return &FunctionLiteral{
		Token:      tok,
		Parameters: params,
		Body:       body,
	}
}

/* =============================================================================
   3. HELPERS
   ============================================================================= */

func (p *Parser) nextToken() {
	p.curToken = p.peekToken
	p.peekToken = p.l.NextToken()
}

func (p *Parser) curTokenIs(k Kind) bool  { return p.curToken.Kind == k }
func (p *Parser) peekTokenIs(k Kind) bool { return p.peekToken.Kind == k }

func (p *Parser) expectPeek(k Kind) bool {
	if p.peekTokenIs(k) {
		p.nextToken()
		return true
	}
	p.addError(fmt.Sprintf("expected %s, got %s (peek token: '%s' at position %d)", k, p.peekToken.Kind, p.peekToken.String(), p.peekToken.Position))
	return false
}

func (p *Parser) parseExpressionList(end Kind) []Expression {
	list := []Expression{}
	if p.peekTokenIs(end) {
		p.nextToken()
		return list
	}
	p.nextToken()
	list = append(list, p.parseExpression(LOWEST))
	for p.peekTokenIs(Comma) {
		p.nextToken()
		p.nextToken()
		list = append(list, p.parseExpression(LOWEST))
	}
	if !p.expectPeek(end) {
		return nil
	}
	return list
}

func (p *Parser) peekPrecedence() int {
	if p, ok := precedences[p.peekToken.Kind]; ok {
		return p
	}
	return LOWEST
}

func (p *Parser) curPrecedence() int {
	if p, ok := precedences[p.curToken.Kind]; ok {
		return p
	}
	return LOWEST
}

func (p *Parser) registerPrefix(k Kind, fn prefixParseFn) { p.prefixParseFns[k] = fn }
func (p *Parser) registerInfix(k Kind, fn infixParseFn)   { p.infixParseFns[k] = fn }
func (p *Parser) addError(msg string) {
	p.errors = append(p.errors, fmt.Sprintf("%s (at pos %d: %q)", msg, p.curToken.Position, p.curToken.String()))
}
func (p *Parser) Errors() []string {
	return p.errors
}

func (p *Parser) parseFunctionStatement() Statement {
	tok := p.curToken

	if !p.expectPeek(Ident) {
		return nil
	}
	name := &Identifier{Token: p.curToken, Value: p.curToken.Value.Text()}

	if !p.expectPeek(LeftParen) {
		return nil
	}

	exps := p.parseExpressionList(RightParen)
	params := make([]*Identifier, len(exps))
	for i, e := range exps {
		if id, ok := e.(*Identifier); ok {
			params[i] = id
		}
	}

	if !p.expectPeek(LeftBrace) {
		return nil
	}

	body := p.parseBlockStatement()

	funcLit := &FunctionLiteral{
		Token:      tok,
		Parameters: params,
		Body:       body,
	}

	return &VarStatement{
		Token: Token{
			Kind:  Const,
			Value: value.NewString("const"),
		},
		Names:        []*Identifier{name},
		Value:        funcLit,
		DestructMode: DestructNone,
	}
}

func (p *Parser) parseFunctionExpression() Expression {
	tok := p.curToken // function

	if p.peekTokenIs(Ident) {
		p.nextToken() // Skip named function expression internal name
	}

	if !p.expectPeek(LeftParen) {
		return nil
	}

	exps := p.parseExpressionList(RightParen)
	params := make([]*Identifier, len(exps))
	for i, e := range exps {
		if id, ok := e.(*Identifier); ok {
			params[i] = id
		}
	}

	if !p.expectPeek(LeftBrace) {
		return nil
	}

	body := p.parseBlockStatement()

	return &FunctionLiteral{
		Token:      tok,
		Parameters: params,
		Body:       body,
	}
}

