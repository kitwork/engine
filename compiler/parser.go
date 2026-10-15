package compiler

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

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
	// ?? đứng ngang ||, như JavaScript; trộn hai thứ không ngoặc là lỗi (parseInfixExpression).
	NullCoalescing: OR,
	OptionalChain:  MEMBER,
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
	hasDefault bool     // có `export default …` (đã hạ về const DefaultExportName)
	breakDepth int      // switch/loop nesting; function bodies reset this boundary
	loopDepth  int      // loop nesting, what continue needs; function bodies reset it too

	// text là toàn bộ nguồn mà Token.Position đếm vào — để báo lỗi dạng file:dòng:cột. Parser con của
	// một ${…} trong template nhận text của file chứa nó.
	text []byte

	// grouped holds the expressions the source wrapped in parentheses: (a ?? b) || c is allowed where
	// a ?? b || c is not, and (a?.b).c ends the optional chain at the parenthesis.
	grouped map[Expression]bool
	// openChain holds the links (member, index, call) of an optional chain not closed yet: the chain
	// becomes one ChainExpression when the next token no longer continues it.
	openChain map[Expression]bool
	// leftStart is where the expression an infix function is handed begins in the source (its first
	// token, a parenthesis included): parseExpression sets it just before the call.
	leftStart int32
	// nesting is how deep the parser recurses now (nesting.go); nestingReported: the error is said.
	nesting         int
	nestingReported bool
}

func NewParser(l *Lexer) *Parser {
	p := &Parser{l: l, errors: []string{}}
	if l.base == 0 {
		p.text = l.input
	}

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
	p.registerInfix(NullCoalescing, p.parseInfixExpression)
	p.registerInfix(OptionalChain, p.parseOptionalChain)
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
	p.checkNesting(program)
	return program
}

func (p *Parser) parseStatement() Statement {
	if !p.enterNesting() {
		return nil
	}
	defer p.leaveNesting()
	if p.curTokenIs(Ident) {
		switch p.curToken.Value.Text() {
		case "break":
			return p.parseBreakStatement()
		case "continue":
			return p.parseContinueStatement()
		case "case", "default":
			p.addError(fmt.Sprintf("'%s' chỉ hợp lệ bên trong switch", p.curToken.Value.Text()))
			return nil
		}
	}
	switch p.curToken.Kind {
	case LeftBrace:
		// a { that starts a statement is a block, as in JavaScript: { const z = 1; … }, case 1: { … }
		return p.parseBlockStatement()
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
	case Switch:
		return p.parseSwitchStatement()
	default:
		return p.parseExpressionStatement()
	}

}

// Trong parser.go
func (p *Parser) parseVarStatement() Statement {
	stmt := &VarStatement{Token: p.curToken}

	if p.peekTokenIs(LeftBrace) || p.peekTokenIs(LeftBracket) {
		// Destructuring: const { a, b: c, d = 1, e: { f } } = …, const [a, b = 2] = …
		p.nextToken() // cur: { or [
		pattern := p.parseBindingPattern()
		if pattern == nil {
			return nil
		}
		stmt.Pattern = pattern
		stmt.Names = pattern.Names()
		stmt.DestructMode = DestructObject
		if pattern.Array {
			stmt.DestructMode = DestructArray
		}
	} else {
		// Standard: const a = ...
		if !p.expectBindingName() {
			return nil
		}
		stmt.Names = []*Identifier{{Token: p.curToken, Value: p.curToken.Value.Text()}}
		stmt.DestructMode = DestructNone
	}

	if !p.peekTokenIs(Assign) {
		switch {
		case stmt.Pattern != nil:
			p.addErrorAt(p.peekToken, "mẫu tách cần một giá trị để tách: "+stmt.Token.String()+" "+stmt.Pattern.String()+" = …")
			return nil
		case stmt.Token.Kind == Let:
			// let y; is let y = undefined, which is null in Kitwork, as in JavaScript (14/10/2026)
			name := stmt.Names[0]
			nullToken := tokenWithValue(name.Token, Null, value.Value{K: value.Nil})
			stmt.Value = &Literal{Token: nullToken, Value: nullToken.Value}
			return stmt
		default:
			p.addErrorAt(p.peekToken, "const cần giá trị ngay khi khai báo, như JavaScript: const "+stmt.Names[0].Value+" = …; chưa có giá trị thì let "+stmt.Names[0].Value+";")
			return nil
		}
	}
	p.nextToken()
	// Lúc này curToken đang là '='

	p.nextToken() // Nhảy qua dấu '=' để đứng tại vị trí của giá trị (VD: số 10)

	stmt.Value = p.parseExpression(LOWEST)

	return stmt
}

/* =============================================================================
   TÁCH OBJECT VÀ MẢNG (14/10/2026)
   const { value: user, error: userError } = safe(() => …): đổi tên, khoá là từ khoá hay chuỗi, giá trị
   mặc định, mẫu lồng nhau, như JavaScript. Hai chỗ khác JS, đúng hai luật của Kitwork: mặc định dùng khi
   khoá vắng HOẶC null (Kitwork không có undefined riêng), và mẫu lồng gặp null thì mọi tên bên trong là
   null (thành viên của null là null). Chưa có, là lỗi biên dịch nói cách viết thay: phần còn lại
   (...tên), chỗ trống ([, b]), khoá tính toán ({ [k]: v }), tách trong tham số và trong for…of.
   ============================================================================= */

// parseBindingPattern reads { … } or [ … ] of a declaration; cur is the opening bracket, and is the
// closing one on return. nil after an error.
func (p *Parser) parseBindingPattern() *BindingPattern {
	pattern := &BindingPattern{Token: p.curToken, Array: p.curTokenIs(LeftBracket)}
	end, example := RightBrace, "const { a, b: c, d = 1 } = o"
	if pattern.Array {
		end, example = RightBracket, "const [a, b = 2] = arr"
	}
	for !p.peekTokenIs(end) {
		p.nextToken() // cur: the element
		element, ok := p.parseBindingElement(pattern.Array)
		if !ok {
			return nil
		}
		pattern.Elements = append(pattern.Elements, element)
		if p.peekTokenIs(Comma) {
			p.nextToken()
		} else if !p.peekTokenIs(end) {
			p.addErrorAt(p.peekToken, fmt.Sprintf("các phần của mẫu tách ngăn bằng dấu phẩy: %s", example))
			return nil
		}
	}
	p.nextToken() // cur: } or ]
	return pattern
}

// parseBindingElement reads one element of a pattern, cur at its first token.
func (p *Parser) parseBindingElement(array bool) (BindingElement, bool) {
	element := BindingElement{KeyToken: p.curToken}
	switch {
	case p.curTokenIs(Spread):
		p.addErrorAt(p.curToken, "Kitwork chưa có phần còn lại khi tách (...tên); đọc từng khoá ra, hoặc dùng o.omit(\"khoá\") cho object, arr.slice(n) cho mảng")
		return element, false
	case array && p.curTokenIs(Comma):
		p.addErrorAt(p.curToken, "Kitwork chưa có chỗ trống khi tách mảng ([, b]); viết const b = arr[1]")
		return element, false
	case array:
		if !p.parseBindingTarget(&element) {
			return element, false
		}
	case p.curTokenIs(LeftBracket):
		p.addErrorAt(p.curToken, "Kitwork chưa có khoá tính toán khi tách object ({ [k]: v }); viết const v = o[k]")
		return element, false
	default:
		key, ok := p.patternKey(p.curToken)
		if !ok {
			return element, false
		}
		element.Key = key
		if p.peekTokenIs(Colon) {
			p.nextToken() // cur: ':'
			p.nextToken() // cur: the target
			if !p.parseBindingTarget(&element) {
				return element, false
			}
		} else {
			// { a } is { a: a }: the key is the name, so it must be one.
			if p.curTokenIs(String) {
				p.addErrorAt(p.curToken, fmt.Sprintf("khoá chuỗi cần một tên đích: { %q: tên }", key))
				return element, false
			}
			if !p.bindingName(p.curToken, "mẫu tách object nhận tên biến: const { a, b: c } = o") {
				return element, false
			}
			element.Name = &Identifier{Token: p.curToken, Value: key}
		}
	}
	if p.peekTokenIs(Assign) {
		p.nextToken() // cur: '='
		p.nextToken() // cur: the default
		element.Default = p.parseExpression(LOWEST)
		if element.Default == nil {
			return element, false
		}
		if element.Name != nil {
			nameFunction(element.Default, element.Name.Value)
		}
	}
	return element, true
}

// patternKey is the property an object pattern element reads: a name, a reserved word ({ default: d },
// as after a dot) or a string ({ "content-type": t }).
func (p *Parser) patternKey(tok Token) (string, bool) {
	switch {
	case tok.Kind == Ident:
		return tok.Value.Text(), true
	case tok.Kind == String:
		return tok.Value.Text(), true
	case tok.Kind == Number:
		p.addErrorAt(tok, fmt.Sprintf("khoá số khi tách object chưa có; viết khoá chuỗi { \"%s\": tên }", tok.Value.Text()))
		return "", false
	}
	if word, ok := p.keywordSpelling(tok); ok {
		if !p.peekTokenIs(Colon) {
			// { default } would bind a variable named by a keyword.
			p.reservedNameError(tok, word)
			return "", false
		}
		return word, true
	}
	p.addErrorAt(tok, "mẫu tách object nhận tên, ngăn bằng dấu phẩy: const { a, b: c } = o")
	return "", false
}

// parseBindingTarget reads what a pattern element binds, cur at it: a variable, or a nested pattern.
func (p *Parser) parseBindingTarget(element *BindingElement) bool {
	if p.curTokenIs(LeftBrace) || p.curTokenIs(LeftBracket) {
		element.Pattern = p.parseBindingPattern()
		return element.Pattern != nil
	}
	if p.curTokenIs(Spread) {
		p.addErrorAt(p.curToken, "Kitwork chưa có phần còn lại khi tách (...tên); đọc từng khoá ra, hoặc dùng o.omit(\"khoá\") cho object, arr.slice(n) cho mảng")
		return false
	}
	if !p.bindingName(p.curToken, "sau dấu : là tên biến hoặc một mẫu { … } hay [ … ]: const { a: tên } = o") {
		return false
	}
	element.Name = &Identifier{Token: p.curToken, Value: p.curToken.Value.Text()}
	return true
}

// assignmentPattern turns the left side of ({ … } = o) or [ … ] = arr, parsed as a literal, into the
// pattern it spells. The targets are variables (or nested patterns): a member target is not there yet.
func (p *Parser) assignmentPattern(left Expression) (*BindingPattern, bool) {
	switch node := left.(type) {
	case *ObjectLiteral:
		pattern := &BindingPattern{Token: node.Token}
		for _, entry := range node.Entries {
			if entry.IsSpread {
				at := Token{Position: getNodePosition(entry.Value), Source: getNodeSource(entry.Value)}
				p.addErrorAt(at, "Kitwork chưa có phần còn lại khi tách (...tên); đọc từng khoá ra, hoặc dùng o.omit(\"khoá\")")
				return nil, false
			}
			element := BindingElement{}
			target := entry.Value
			if entry.Value == entry.Key {
				// { a } or { a = 1 }: the key is the name.
				target = entry.Key
				if assign, ok := entry.Key.(*AssignmentExpression); ok {
					if id, ok := assign.Name.(*Identifier); ok {
						element.Key, element.KeyToken = id.Value, id.Token
					}
				}
			}
			if element.Key == "" {
				key, keyToken, ok := p.literalPatternKey(entry.Key)
				if !ok {
					return nil, false
				}
				element.Key, element.KeyToken = key, keyToken
			}
			if !p.assignmentTarget(target, &element) {
				return nil, false
			}
			pattern.Elements = append(pattern.Elements, element)
		}
		return pattern, true
	case *ArrayLiteral:
		pattern := &BindingPattern{Token: node.Token, Array: true}
		for _, item := range node.Elements {
			element := BindingElement{}
			if !p.assignmentTarget(item, &element) {
				return nil, false
			}
			pattern.Elements = append(pattern.Elements, element)
		}
		return pattern, true
	}
	return nil, true
}

// literalPatternKey reads the key of an object literal entry used as a pattern.
func (p *Parser) literalPatternKey(key Expression) (string, Token, bool) {
	at := Token{Position: getNodePosition(key), Source: getNodeSource(key)}
	switch node := key.(type) {
	case *Identifier:
		return node.Value, node.Token, true
	case *Literal:
		if node.Value.IsString() {
			return node.Value.Text(), node.Token, true
		}
		if node.Value.IsNumber() {
			p.addErrorAt(at, fmt.Sprintf("khoá số khi tách object chưa có; viết khoá chuỗi { \"%s\": tên }", node.Value.Text()))
			return "", at, false
		}
	case *ArrayLiteral:
		p.addErrorAt(at, "Kitwork chưa có khoá tính toán khi tách object ({ [k]: v } = o); viết v = o[k]")
		return "", at, false
	}
	p.addErrorAt(at, "mẫu tách object nhận tên, ngăn bằng dấu phẩy: ({ a, b: c } = o)")
	return "", at, false
}

// assignmentTarget reads what one entry of an assignment pattern binds: a variable, a nested pattern,
// either with a default (x = 1, { b } = {}).
func (p *Parser) assignmentTarget(target Expression, element *BindingElement) bool {
	if target == nil {
		return false // its own error is already reported
	}
	if assign, ok := target.(*AssignmentExpression); ok && assign.Token.Kind == Assign {
		element.Default = assign.Value
		target = assign.Name
	}
	switch node := target.(type) {
	case *Identifier:
		element.Name = node
		if element.Default != nil {
			nameFunction(element.Default, node.Value)
		}
		return true
	case *BindingPattern:
		element.Pattern = node
		return true
	case *ObjectLiteral, *ArrayLiteral:
		nested, ok := p.assignmentPattern(node)
		if !ok {
			return false
		}
		element.Pattern = nested
		return true
	}
	at := Token{Position: startPosition(target), Source: getNodeSource(target)}
	switch target.(type) {
	case *MemberExpression, *IndexExpression:
		p.addErrorAt(at, fmt.Sprintf("Kitwork chưa có gán vào %s khi tách; tách vào một tên rồi gán: ({ a } = o); %s = a", target.String(), target.String()))
	default:
		// ({ a: 1 } = o): 1 takes no value; the place after : names the variable
		p.addErrorAt(at, fmt.Sprintf("không gán được vào %s khi tách: sau dấu hai chấm là tên nhận giá trị, ({ a: tên } = o)", target.String()))
	}
	return false
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
func memberConst(local string, obj Expression, prop string, origin Token) Statement {
	return &VarStatement{
		Token: constToken(origin),
		Names: []*Identifier{{
			Token: tokenWithValue(origin, Ident, value.NewString(local)),
			Value: local,
		}},
		DestructMode: DestructNone,
		Value: &MemberExpression{
			Token:  tokenWithValue(origin, Dot, value.Value{}),
			Object: obj,
			Property: &Identifier{
				Token: tokenWithValue(origin, Ident, value.NewString(prop)),
				Value: prop,
			},
		},
	}
}

// constToken builds a synthetic `const` token for lowered declarations.
func constToken(origins ...Token) Token {
	var origin Token
	if len(origins) > 0 {
		origin = origins[0]
	}
	return tokenWithValue(origin, Const, value.NewString("const"))
}

// kitworkModule returns the object a `kitwork`-family NAMED import destructures from: `kitwork()` for
// the bare specifier, or `kitwork().<sub>` for a subpath like "kitwork/database" — so named imports
// honor the subpath the same way default imports already do (`import http from "kitwork/http"`).
func (p *Parser) kitworkModule(spec string, origin Token) Expression {
	base := p.kitworkCall(origin)
	sub := kitworkSubpath(spec)
	if sub == "" {
		return base
	}
	return &MemberExpression{
		Token:  tokenWithValue(origin, Dot, value.Value{}),
		Object: base,
		Property: &Identifier{
			Token: tokenWithValue(origin, Ident, value.NewString(sub)),
			Value: sub,
		},
	}
}

// kitworkCall builds the expression `kitwork()`.
func (p *Parser) kitworkCall(origins ...Token) Expression {
	var origin Token
	if len(origins) > 0 {
		origin = origins[0]
	}
	return &CallExpression{
		Token: tokenWithValue(origin, LeftParen, value.Value{}),
		Function: &Identifier{
			Token: tokenWithValue(origin, Ident, value.NewString("kitwork")),
			Value: "kitwork",
		},
		Arguments: nil,
	}
}

func tokenWithValue(origin Token, kind Kind, item value.Value) Token {
	return Token{
		Kind:     kind,
		Value:    item,
		Source:   origin.Source,
		Position: origin.Position,
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
			aliased := isContextualToken(p.peekToken, "as")
			// Tên được import là tên thuộc tính, như JS: `import { function as f }` là
			// `const f = ….function`. Không có `as` thì chính nó thành tên biến, nên từ khoá bị chặn.
			if !aliased {
				if word, reserved := p.reservedWord(p.curToken); reserved {
					p.reservedImportError(p.curToken, word, fmt.Sprintf("import { %s as tên_khác } from …", word))
					return nil
				}
			}
			if !p.curTokenIs(Ident) && !(aliased && p.isKeyword(p.curToken)) {
				p.errors = append(p.errors, "import: expected identifier inside { }")
				return nil
			}
			imported := p.wordOf(p.curToken)
			local := imported
			// alias tùy chọn: `as local` (`as` là identifier theo ngữ cảnh)
			if aliased {
				p.nextToken() // cur: as
				if word, reserved := p.reservedWord(p.peekToken); reserved {
					p.reservedImportError(p.peekToken, word, fmt.Sprintf("import { %s as tên_khác } from …", imported))
					return nil
				}
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
			// import { safe } from "kitwork" binds the global safe itself (kitwork().safe), not a safe
			// of the bundle's own, so checkSafeCall stays on (14/10/2026)
			globalSafe := func(s ImportSpec) bool {
				return kitworkSubpath(spec) == "" && s.Imported == "safe" && s.Local == "safe"
			}
			if !hasAlias(specs) {
				// → const { a, b } = kitwork()   (or kitwork().<sub> for a subpath specifier)
				names := make([]*Identifier, len(specs))
				kitworkSafe := false
				for i, s := range specs {
					names[i] = &Identifier{
						Token: tokenWithValue(importTok, Ident, value.NewString(s.Local)),
						Value: s.Local,
					}
					kitworkSafe = kitworkSafe || globalSafe(s)
				}
				return &VarStatement{
					Token:        constToken(importTok),
					Names:        names,
					DestructMode: DestructObject,
					Value:        p.kitworkModule(spec, importTok),
					KitworkSafe:  kitworkSafe,
				}
			}
			// có alias → nhóm `const local = kitwork().imported`
			stmts := make([]Statement, len(specs))
			for i, s := range specs {
				stmts[i] = memberConst(s.Local, p.kitworkModule(spec, importTok), s.Imported, importTok)
				if globalSafe(s) {
					stmts[i].(*VarStatement).KitworkSafe = true
				}
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
	if word, reserved := p.reservedWord(p.peekToken); reserved {
		p.reservedImportError(p.peekToken, word, "import tên_khác from …")
		return nil
	}
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
				Token:        constToken(importTok),
				Names:        []*Identifier{name},
				DestructMode: DestructNone,
				Value: &MemberExpression{
					Token:  tokenWithValue(importTok, Dot, value.Value{}),
					Object: p.kitworkCall(importTok),
					Property: &Identifier{
						Token: tokenWithValue(importTok, Ident, value.NewString(sub)),
						Value: sub,
					},
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

func (p *Parser) parseBreakStatement() Statement {
	stmt := &BreakStatement{Token: p.curToken}
	if p.breakDepth == 0 {
		p.addError("'break' chỉ hợp lệ bên trong switch hoặc vòng for")
	}
	if p.peekTokenIs(Semicolon) {
		p.nextToken()
		return stmt
	}
	if p.peekTokenIs(RightBrace) || p.peekTokenIs(EOF) ||
		isContextualToken(p.peekToken, "case") ||
		isContextualToken(p.peekToken, "default") {
		return stmt
	}
	p.addError("Kitwork không hỗ trợ labeled break; hãy dùng 'break;'")
	return stmt
}

// parseContinueStatement reads continue; (14/10/2026; it was the compile error "Kitwork chưa có
// continue"): the nearest loop goes on with its next iteration, the counting for after its update, as in
// JavaScript. Outside a loop (a switch alone, a function made in a loop) it is JavaScript's SyntaxError.
func (p *Parser) parseContinueStatement() Statement {
	stmt := &ContinueStatement{Token: p.curToken}
	if p.loopDepth == 0 {
		p.addError("'continue' chỉ hợp lệ bên trong vòng for (một switch không có vòng for bao ngoài thì không), như JavaScript")
	}
	if p.peekTokenIs(Semicolon) {
		p.nextToken()
		return stmt
	}
	// a line break ends it, as JavaScript inserts the semicolon there: a label would stay on its line
	if p.peekTokenIs(RightBrace) || p.peekTokenIs(EOF) || p.lineBreakBeforePeek() ||
		isContextualToken(p.peekToken, "case") ||
		isContextualToken(p.peekToken, "default") {
		return stmt
	}
	p.addError("Kitwork không hỗ trợ continue có nhãn; hãy dùng 'continue;'")
	return stmt
}

// lineBreakBeforePeek reports a line break (a comment's included) between the current token and the
// next one.
func (p *Parser) lineBreakBeforePeek() bool {
	start := int(p.curToken.Position-p.l.base) + int(p.curToken.Length)
	end := int(p.peekToken.Position - p.l.base)
	if start < 0 || end > len(p.l.input) || start > end {
		return false
	}
	return strings.IndexByte(string(p.l.input[start:end]), '\n') >= 0
}

func (p *Parser) parseSwitchStatement() Statement {
	stmt := &SwitchStatement{Token: p.curToken, Cases: []SwitchCase{}}
	if !p.expectPeek(LeftParen) {
		return nil
	}
	p.nextToken()
	stmt.Discriminant = p.parseExpression(LOWEST)
	if stmt.Discriminant == nil || !p.expectPeek(RightParen) || !p.expectPeek(LeftBrace) {
		return nil
	}

	p.breakDepth++
	defer func() { p.breakDepth-- }()
	seenDefault := false
	p.nextToken()
	for !p.curTokenIs(RightBrace) && !p.curTokenIs(EOF) {
		if p.curTokenIs(Semicolon) {
			p.nextToken()
			continue
		}

		clause := SwitchCase{Token: p.curToken, Statements: []Statement{}}
		switch {
		case isContextualToken(p.curToken, "case"):
			p.nextToken()
			clause.Test = p.parseExpression(LOWEST)
			if clause.Test == nil || !p.expectPeek(Colon) {
				return nil
			}
		case isContextualToken(p.curToken, "default"):
			if seenDefault {
				p.addError("switch chỉ được có một default")
				return nil
			}
			seenDefault = true
			if !p.expectPeek(Colon) {
				return nil
			}
		default:
			p.addError("switch chỉ chấp nhận 'case' hoặc 'default' ở cấp thân trực tiếp")
			return nil
		}

		p.nextToken()
		for !p.curTokenIs(RightBrace) && !p.curTokenIs(EOF) &&
			!isContextualToken(p.curToken, "case") &&
			!isContextualToken(p.curToken, "default") {
			if p.curTokenIs(Semicolon) {
				p.nextToken()
				continue
			}
			caseStatement := p.parseStatement()
			if caseStatement != nil {
				clause.Statements = append(clause.Statements, caseStatement)
			}
			p.nextToken()
		}
		stmt.Cases = append(stmt.Cases, clause)
	}
	if p.curTokenIs(EOF) {
		p.addError("switch thiếu dấu '}' kết thúc")
		return nil
	}
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

func (p *Parser) parseBreakableBlock() *BlockStatement {
	p.breakDepth++
	defer func() { p.breakDepth-- }()
	return p.parseBlockStatement()
}

// parseLoopBody reads the body of a for, where break and continue are allowed.
func (p *Parser) parseLoopBody() *BlockStatement {
	p.loopDepth++
	defer func() { p.loopDepth-- }()
	return p.parseBreakableBlock()
}

func (p *Parser) parseFunctionBlock() *BlockStatement {
	outerBreakDepth, outerLoopDepth := p.breakDepth, p.loopDepth
	p.breakDepth, p.loopDepth = 0, 0
	defer func() { p.breakDepth, p.loopDepth = outerBreakDepth, outerLoopDepth }()
	return p.parseBlockStatement()
}

func isContextualToken(token Token, word string) bool {
	return token.Kind == Ident && token.Value.Text() == word
}

/* =============================================================================
   2. EXPRESSIONS (Biểu thức)
   ============================================================================= */

func (p *Parser) parseExpression(precedence int) Expression {
	if !p.enterNesting() {
		return nil
	}
	defer p.leaveNesting()
	// Một từ khoá đứng trơ trước `,` `)` `=>` `]` `}` `;` hay hết nguồn — (new) => …, f(function), [new],
	// { ...new }, ${function} — là đang được dùng làm tên. true/false/null/undefined là giá trị nên đi tiếp
	// xuống parseLiteral.
	if p.curToken.Kind != Boolean && p.curToken.Kind != Null &&
		(p.peekTokenIs(Comma) || p.peekTokenIs(RightParen) || p.peekTokenIs(FatArrow) ||
			p.peekTokenIs(RightBracket) || p.peekTokenIs(RightBrace) || p.peekTokenIs(Semicolon) || p.peekTokenIs(EOF)) {
		if word, ok := p.keywordSpelling(p.curToken); ok {
			p.reservedNameError(p.curToken, word)
			return nil
		}
	}
	prefix := p.prefixParseFns[p.curToken.Kind]
	if prefix == nil {
		if p.curTokenIs(Spread) {
			p.addErrorAt(p.curToken, "Kitwork chưa có ... ở chỗ này: chỉ trải object được ({ ...o }); phần còn lại khi tách, tham số (...args) và trải mảng chưa làm, nối mảng thì a.concat(b)")
			return nil
		}
		p.addError(fmt.Sprintf("không đọc được %q ở đầu một biểu thức", p.curToken.String()))
		return nil
	}
	start := p.curToken.Position
	leftExp := prefix()

	for !p.peekTokenIs(Semicolon) && precedence < p.peekPrecedence() {
		infix := p.infixParseFns[p.peekToken.Kind]
		if infix == nil {
			return p.closeChain(leftExp)
		}
		p.nextToken()
		p.leftStart = start
		leftExp = infix(leftExp)
		if !p.continuesChain() {
			leftExp = p.closeChain(leftExp)
		}
	}
	return p.closeChain(leftExp)
}

// continuesChain reports whether the next token adds a link to the expression just read: . ?. [ (
func (p *Parser) continuesChain() bool {
	return p.peekTokenIs(Dot) || p.peekTokenIs(OptionalChain) || p.peekTokenIs(LeftBracket) || p.peekTokenIs(LeftParen)
}

// link records that node, a member, index or call on object, belongs to an optional chain still open:
// object's, or the one node starts (optional).
func (p *Parser) link(node, object Expression, optional bool) {
	if node == nil || (!optional && !p.openChain[object]) {
		return
	}
	if p.openChain == nil {
		p.openChain = map[Expression]bool{}
	}
	delete(p.openChain, object)
	p.openChain[node] = true
}

// closeChain wraps an optional chain that nothing continues into one ChainExpression, the unit a ?.
// that finds null skips to the end of.
func (p *Parser) closeChain(e Expression) Expression {
	if e == nil || !p.openChain[e] {
		return e
	}
	delete(p.openChain, e)
	return &ChainExpression{Expression: e}
}

func (p *Parser) parseIdentifier() Expression {
	if operatorWords[p.curToken.Value.Text()] {
		return p.parseTypeofExpression()
	}
	// break/case/default/continue chỉ có nghĩa ở đầu câu lệnh (parseStatement, switch). Ở chỗ biểu thức
	// chúng là đọc hoặc gán một biến không khai báo được (default = 5, [case], ${break}), nên cũng là lỗi
	// từ khoá.
	if message, notYet := notYetWords[p.curToken.Value.Text()]; notYet {
		p.addErrorAt(p.curToken, message)
		return nil
	}
	if word := p.curToken.Value.Text(); statementWords[word] {
		p.reservedNameError(p.curToken, word)
		return nil
	}
	return &Identifier{Token: p.curToken, Value: p.curToken.Value.Text()}
}

// parseTypeofExpression reads typeof x (14/10/2026; it was the compile error "Kitwork chưa có typeof"):
// JavaScript's name of x's kind, a string, bound as tightly as ! and - are (typeof a.b is typeof (a.b),
// typeof x === "string" compares the string). Standing where a name would (typeof = 1, (typeof) => …)
// it is the keyword error.
func (p *Parser) parseTypeofExpression() Expression {
	if p.peekTokenIs(Comma) || p.peekTokenIs(RightParen) || p.peekTokenIs(FatArrow) || p.peekTokenIs(Assign) ||
		p.peekTokenIs(RightBracket) || p.peekTokenIs(RightBrace) || p.peekTokenIs(Semicolon) || p.peekTokenIs(EOF) ||
		p.peekTokenIs(Colon) {
		p.reservedNameError(p.curToken, p.curToken.Value.Text())
		return nil
	}
	return p.parsePrefixExpression()
}

func (p *Parser) parseLiteral() Expression {
	return &Literal{Token: p.curToken, Value: p.curToken.Value}
}

func (p *Parser) parseTemplateLiteral() Expression {
	templateToken := p.curToken
	fullText := templateToken.Value.Text()
	tl := &TemplateLiteral{Token: templateToken, Parts: []Expression{}}

	start := 0
	for i := 0; i < len(fullText); i++ {
		// Look for ${
		if i+1 < len(fullText) && fullText[i] == '$' && fullText[i+1] == '{' {
			// 1. Add previous string part if exists
			if i > start {
				tl.Parts = append(tl.Parts, &Literal{
					Token: Token{
						Kind:     String,
						Source:   templateToken.Source,
						Position: templateToken.Position + 1 + int32(start),
					},
					Value: value.NewString(fullText[start:i]),
				})
			}

			// 2. Parse expression inside ${ }
			exprStr := ""
			i += 2 // skip ${
			braceCount := 1
			exprStart := i
			for i < len(fullText) && braceCount > 0 {
				switch fullText[i] {
				case '{':
					braceCount++
				case '}':
					braceCount--
				case '"', '\'':
					// a brace inside a string of the expression is text: `a${"}"}b` (14/10/2026)
					quote := fullText[i]
					for i++; i < len(fullText) && fullText[i] != quote; i++ {
						if fullText[i] == '\\' {
							i++
						}
					}
				}
				if braceCount > 0 {
					i++
				}
			}
			if braceCount != 0 {
				p.addError("unterminated template interpolation")
				return nil
			}
			exprStr = fullText[exprStart:i]

			// Sub-parse the expression
			subLexer := newLexerAt(
				exprStr,
				templateToken.Source,
				p.templateSourcePosition(templateToken, exprStart),
			)
			subParser := NewParser(subLexer)
			subParser.text = p.text
			subParser.nesting = p.nesting
			expr := subParser.parseExpression(LOWEST)
			if errors := subParser.Errors(); len(errors) > 0 {
				p.addError("invalid template expression: " + errors[0])
				return nil
			}
			if expr == nil {
				p.addError("template interpolation requires an expression")
				return nil
			}
			tl.Parts = append(tl.Parts, expr)

			start = i + 1 // skip }
		}
	}

	// Add trailing string part
	if start < len(fullText) {
		tl.Parts = append(tl.Parts, &Literal{
			Token: Token{
				Kind:     String,
				Source:   templateToken.Source,
				Position: templateToken.Position + 1 + int32(start),
			},
			Value: value.NewString(fullText[start:]),
		})
	}

	return tl
}

// templateSourcePosition đổi chỗ offset trong chữ đã giải escape của template về vị trí byte trong nguồn.
// Mỗi escape (\n, é…) ngắn đi khi giải, nên cộng thẳng offset vào vị trí dấu ` thì lỗi trong ${…}
// chỉ lùi về trước. Đi lại nguồn thô từng ký tự, đúng như unescapeJSString của lexer.
func (p *Parser) templateSourcePosition(template Token, offset int) int32 {
	start := int(template.Position-p.l.base) + 1
	if start < 0 || start > len(p.l.input) {
		return template.Position + 1 + int32(offset)
	}
	raw := p.l.b2s(p.l.input[start:])
	consumed, produced := 0, 0
	for produced < offset && consumed < len(raw) {
		character, _, tail, err := strconv.UnquoteChar(raw[consumed:], '`')
		if err != nil {
			produced++
			consumed++
			continue
		}
		produced += len(string(character))
		consumed = len(raw) - len(tail)
	}
	return template.Position + 1 + int32(consumed)
}

func (p *Parser) parseInfixExpression(left Expression) Expression {
	// Xử lý toán tử gán riêng biệt
	if p.curToken.Kind == Assign {
		if !p.assignable(left, p.curToken) {
			return nil
		}
		ae := &AssignmentExpression{Token: p.curToken, Name: left}
		// ({ a, b: c } = o), [a, b] = arr: the literal on the left is a pattern.
		pattern, ok := p.assignmentPattern(left)
		if !ok {
			return nil
		}
		if pattern != nil {
			ae.Name = pattern
		}
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
	if p.mixesNullish(exp) {
		p.addErrorAt(exp.Token, "không trộn ?? với && hay || khi không có ngoặc (JavaScript cũng báo lỗi): viết (a ?? b) || c hoặc a ?? (b || c)")
		return nil
	}
	return exp
}

// mixesNullish reports a ?? written next to && or || with no parenthesis between them: JavaScript
// refuses a ?? b || c, since nothing says which one goes first.
func (p *Parser) mixesNullish(exp *InfixExpression) bool {
	var other []string
	switch exp.Operator {
	case "??":
		other = []string{"&&", "||"}
	case "&&", "||":
		other = []string{"??"}
	default:
		return false
	}
	for _, side := range []Expression{exp.Left, exp.Right} {
		infix, ok := side.(*InfixExpression)
		if !ok || p.grouped[side] {
			continue
		}
		for _, operator := range other {
			if infix.Operator == operator {
				return true
			}
		}
	}
	return false
}

// assignable refuses an optional chain on the left of =, +=, ++: a?.b = 1 is a syntax error in
// JavaScript too.
func (p *Parser) assignable(left Expression, operator Token) bool {
	if chain, ok := left.(*ChainExpression); ok {
		p.addErrorAt(operator, fmt.Sprintf("không gán được vào %s: ?. chỉ để đọc; kiểm rồi gán, như if (o) o.x = …", chain.String()))
		return false
	}
	return true
}

func (p *Parser) parseDotExpression(left Expression) Expression {
	tok := p.curToken // Dấu '.'
	start := p.leftStart
	p.nextToken() // Sang tên phương thức/thuộc tính

	// Tạo Identifier từ Token hiện tại. Từ khoá sau dấu chấm là tên thuộc tính như JS (o.function,
	// o.default, o.undefined): lấy đúng chữ trong nguồn, vì token true/false/null/undefined không giữ chữ.
	property := p.curToken.Value.Text()
	if word, ok := p.keywordSpelling(p.curToken); ok {
		property = word
	}
	name := &Identifier{
		Token: p.curToken,
		Value: property,
	}

	// Kiểm tra xem có phải là gọi Method không: object.method(...)
	if p.peekTokenIs(LeftParen) {
		p.nextToken() // Chuyển curToken sang dấu '('
		call := &MethodCallExpression{
			Token:     tok,
			Object:    left,
			Method:    name,
			Arguments: p.parseExpressionList(RightParen),
		}
		if property == "safe" && call.Arguments != nil && len(call.Arguments) == 0 {
			p.addErrorAt(name.Token, removedSafeMessage(p.sourceBetween(start, tok.Position)))
		}
		p.link(call, left, false)
		return call
	}

	// Nếu không có dấu '(', đây là truy cập thuộc tính bình thường
	member := &MemberExpression{
		Token:    tok,
		Object:   left,
		Property: name,
	}
	p.link(member, left, false)
	return member
}

// parseOptionalChain reads what follows ?.: a name (o?.x, o?.x(…)), [ (o?.[k]) or ( (f?.(…), o.x?.(…)).
// Each link is read only when what is on its left is not null or undefined; otherwise the rest of the
// chain is skipped and it is null (ChainExpression).
func (p *Parser) parseOptionalChain(left Expression) Expression {
	tok := p.curToken // ?.
	start := p.leftStart
	var node Expression
	switch {
	case p.peekTokenIs(LeftParen):
		p.nextToken() // cur: (
		paren := p.curToken
		args := p.parseExpressionList(RightParen)
		if member, ok := left.(*MemberExpression); ok && !p.grouped[left] {
			// o.x?.(…) calls x as o.x(…) does, when o.x is not null.
			delete(p.openChain, left)
			node = &MethodCallExpression{Token: member.Token, Object: member.Object, Method: member.Property,
				Arguments: args, Optional: member.Optional, OptionalCall: true}
			// o.safe?.() is the removed .safe() as much as o.safe() is
			if member.Property.Value == "safe" && args != nil && len(args) == 0 {
				p.addErrorAt(member.Property.Token, removedSafeMessage(p.sourceBetween(start, member.Token.Position)))
			}
		} else {
			node = &CallExpression{Token: paren, Function: left, Arguments: args, Optional: true}
		}
	case p.peekTokenIs(LeftBracket):
		p.nextToken() // cur: [
		index := &IndexExpression{Token: p.curToken, Left: left, Optional: true}
		p.nextToken()
		index.Index = p.parseExpression(LOWEST)
		if !p.expectPeek(RightBracket) {
			return nil
		}
		node = index
	default:
		p.nextToken() // cur: the name
		property, ok := p.keywordSpelling(p.curToken)
		if !ok {
			if !p.curTokenIs(Ident) {
				p.addErrorAt(tok, "sau ?. là một tên, [ hay (: o?.x, o?.[k], f?.()")
				return nil
			}
			property = p.curToken.Value.Text()
		}
		name := &Identifier{Token: p.curToken, Value: property}
		if p.peekTokenIs(LeftParen) {
			p.nextToken() // cur: (
			call := &MethodCallExpression{Token: tok, Object: left, Method: name,
				Arguments: p.parseExpressionList(RightParen), Optional: true}
			// o?.safe() is the removed .safe() as much as o.safe() is
			if property == "safe" && call.Arguments != nil && len(call.Arguments) == 0 {
				p.addErrorAt(name.Token, removedSafeMessage(p.sourceBetween(start, tok.Position)))
			}
			node = call
		} else {
			node = &MemberExpression{Token: tok, Object: left, Property: name, Optional: true}
		}
	}
	p.link(node, left, true)
	return node
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
	if !p.assignable(left, tok) {
		return nil
	}

	p.nextToken()
	right := p.parseExpression(ASSIGN - 1)

	return &AssignmentExpression{
		Token: tok,
		Name:  left,
		Value: &InfixExpression{Token: tok, Left: left, Operator: baseOp, Right: right},
	}
}

// updateExpression dựng `target = target ± 1` dùng chung cho ++ và --, đánh dấu trước hay sau toán hạng:
// compiler cho x++ giá trị cũ, ++x giá trị mới khi giá trị được dùng (14/10/2026).
func updateExpression(tok Token, target Expression, kind UpdateKind) Expression {
	op := "+"
	if tok.Kind == MinusMinus {
		op = "-"
	}
	one := &Literal{Token: Token{Kind: Number}, Value: value.New(1)}
	return &AssignmentExpression{
		Token:  tok,
		Name:   target,
		Value:  &InfixExpression{Token: tok, Left: target, Operator: op, Right: one},
		Update: kind,
	}
}

// parsePostfixUpdate xử lý `i++` / `i--`: giá trị là i CŨ, như JavaScript (14/10/2026; trước đó là giá trị
// mới, để tiết kiệm byte khi bundle lớn nhất sát trần 65 535 byte của bytecode 2). Làm câu lệnh hay bước
// nhảy của for thì giá trị bị bỏ, và mã dịch ra y như trước.
func (p *Parser) parsePostfixUpdate(left Expression) Expression {
	if !p.assignable(left, p.curToken) {
		return nil
	}
	return updateExpression(p.curToken, left, PostfixUpdate)
}

// parsePrefixUpdate xử lý `++i` / `--i`: giá trị là i MỚI.
func (p *Parser) parsePrefixUpdate() Expression {
	tok := p.curToken
	p.nextToken()
	target := p.parseExpression(PREFIX)
	if !p.assignable(target, tok) {
		return nil
	}
	return updateExpression(tok, target, PrefixUpdate)
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
	if p.peekTokenIs(LeftBrace) || p.peekTokenIs(LeftBracket) {
		p.addErrorAt(p.peekToken, "Kitwork chưa có tách trong for (for (const { a } of list)); duyệt từng phần tử rồi tách trong thân: for (const item of list) { const { a } = item; … }")
		return nil
	}
	if !p.expectBindingName() {
		return nil
	}
	counter := &Identifier{Token: p.curToken, Value: p.curToken.Value.Text()}

	// Disambiguate on the token after the counter name: '=' → counting loop, 'of' → iteration.
	switch {
	case p.peekTokenIs(Assign):
		return p.parseCountedFor(forTok, declTok, counter)
	case p.peekTokenIs(Ident) && p.peekToken.Value.Text() == "of":
		statement := p.parseForOf(forTok, counter)
		if loop, ok := statement.(*ForStatement); ok && loop != nil {
			loop.Const = declTok.Kind == Const
		}
		return statement
	case p.peekTokenIs(Ident) && p.peekToken.Value.Text() == "in":
		p.addErrorAt(p.peekToken, "Kitwork chưa có for…in: duyệt khoá thì for (const k of Object.keys(o)), duyệt phần tử thì for (const x of arr)")
		return nil
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
	stmt.Body = p.parseLoopBody()
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
	stmt.Body = p.parseLoopBody()
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
	// the token is read before the elements are: in one composite literal Go may read p.curToken after
	// the call that moved it, and the literal's place was its closing bracket
	open := p.curToken
	return &ArrayLiteral{
		Token:    open,
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
			if val == nil {
				p.addError("object spread requires an expression")
				return nil
			}
			obj.Entries = append(obj.Entries, ObjectEntry{
				Value:    val,
				IsSpread: true,
			})
		} else {
			var key Expression
			entryToken := p.curToken
			if word, ok := p.keywordSpelling(p.curToken); ok && p.peekTokenIs(Colon) {
				// Từ khoá trước ':' là tên thuộc tính, như JS: { function: … } chính là { "function": … },
				// cùng một nút Literal chuỗi nên ra cùng bytecode.
				keyToken := tokenWithValue(p.curToken, String, value.NewString(word))
				keyToken.Length = p.curToken.Length
				key = &Literal{Token: keyToken, Value: keyToken.Value}
			} else if p.curTokenIs(Ident) && languageWord(p.curToken.Value.Text()) && p.peekTokenIs(Colon) {
				// break/case/default/continue/typeof trước ':' cũng là tên thuộc tính. parseIdentifier từ chối
				// hay đọc khác chúng, nên dựng
				// thẳng đúng nút Identifier nó vẫn trả về trước đây: bytecode không đổi.
				key = &Identifier{Token: p.curToken, Value: p.curToken.Value.Text()}
			} else if word, reserved := p.reservedWord(p.curToken); reserved && p.peekTokenIs(LeftParen) {
				// { function() { … } } là phương thức viết tắt của JS; Kitwork chưa có, trước đây nó lặng lẽ
				// thành một khoá rỗng giữ hàm.
				p.addErrorAt(p.curToken, fmt.Sprintf("Kitwork chưa có phương thức viết tắt trong object ({ %s() { … } }); viết { %s: (…) => … }", word, word))
				return nil
			} else if word, reserved := p.reservedWord(p.curToken); reserved &&
				(p.peekTokenIs(Comma) || p.peekTokenIs(RightBrace)) {
				// { function } là viết tắt của { function: function }: đọc một biến không thể có.
				p.addErrorAt(p.curToken, fmt.Sprintf("{ %s } là viết tắt của { %s: %s }, tức đọc biến tên %s; mà %s là từ khoá của ngôn ngữ, không làm tên biến được. Viết rõ { %s: giá_trị } hoặc { \"%s\": giá_trị }",
					word, word, word, word, word, word, word))
				return nil
			} else {
				key = p.parseExpression(LOWEST)
			}
			if key == nil {
				p.addError("object key requires an expression")
				return nil
			}
			if _, isFunction := key.(*FunctionLiteral); isFunction {
				// { function foo() { … } }, { (x) => 1 }: một hàm đứng chỗ khoá, không có tên khoá nào.
				p.addErrorAt(entryToken, "Kitwork chưa có phương thức viết tắt trong object; hàm phải đứng sau một khoá: { tên: (…) => … }")
				return nil
			}
			if p.peekTokenIs(Colon) {
				p.nextToken()
				p.nextToken()
				val := p.parseExpression(LOWEST)
				if val == nil {
					p.addError("object value requires an expression")
					return nil
				}
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
	if exp.Arguments != nil {
		exp.Close = p.curToken.Position
	}
	p.link(exp, left, false)
	return exp
}

// sourceBetween is the source from start up to end (a position, end excluded) as one line: a run of
// spaces and line breaks is one space, and none next to . , ) ] or after ( [ (the line breaks of a
// chain written one call a line go away). "" when the parser has no source.
func (p *Parser) sourceBetween(start, end int32) string {
	if p.text == nil || start < 0 || end > int32(len(p.text)) || start >= end {
		return ""
	}
	return oneLine(string(p.text[start:end]))
}

// oneLine is code as one line, for a message that quotes it as the fix to copy: a string is copied as
// it is written, its spaces and escapes included, and a // comment is left out (kept on the line it
// would hide the rest of it), so the copy means what the code means.
func oneLine(text string) string {
	var out strings.Builder
	space := false
	runes := []rune(strings.TrimSpace(text))
	for i := 0; i < len(runes); i++ {
		char := runes[i]
		if char == '/' && i+1 < len(runes) && runes[i+1] == '/' {
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
			space = true
			continue
		}
		if char == ' ' || char == '\t' || char == '\n' || char == '\r' {
			space = true
			continue
		}
		if space {
			written := out.String()
			last := byte(0)
			if len(written) > 0 {
				last = written[len(written)-1]
			}
			if !strings.ContainsRune(".,)]", char) && last != '(' && last != '[' && last != '.' {
				out.WriteByte(' ')
			}
			space = false
		}
		if char == '"' || char == '\'' || char == '`' {
			end := i + 1
			for end < len(runes) && runes[end] != char {
				if runes[end] == '\\' {
					end++
				}
				end++
			}
			if end >= len(runes) {
				end = len(runes) - 1
			}
			out.WriteString(string(runes[i : end+1]))
			i = end
			continue
		}
		out.WriteRune(char)
	}
	return out.String()
}

// removedSafeTextLimit bounds the receiver quoted in the error of x.safe(), in characters.
const removedSafeTextLimit = 100

// removedSafeMessage is the error of x.safe(), gone since 14/10/2026: it quotes the receiver in the
// new spelling, so the fix is a copy. A receiver longer than removedSafeTextLimit keeps its head (cut
// back to a space) and its tail (from a dot on) with … between.
func removedSafeMessage(receiver string) string {
	if receiver == "" {
		receiver = "…"
	}
	if runes := []rune(receiver); len(runes) > removedSafeTextLimit {
		head := string(runes[:60])
		if cut := strings.LastIndex(head, " "); cut > 0 {
			head = head[:cut+1]
		}
		tail := string(runes[len(runes)-40:])
		if cut := strings.Index(tail, "."); cut >= 0 && cut < len(tail)-1 {
			tail = tail[cut+1:]
		}
		receiver = head + "…" + tail
	}
	if strings.HasPrefix(receiver, "{") {
		receiver = "(" + receiver + ")"
	}
	return ".safe() đã bỏ (14/10/2026): viết safe(() => " + receiver + "), nhận { value, error }"
}

func (p *Parser) parseIndexExpression(left Expression) Expression {
	exp := &IndexExpression{Token: p.curToken, Left: left}
	p.nextToken()
	exp.Index = p.parseExpression(LOWEST)
	if !p.expectPeek(RightBracket) {
		return nil
	}
	p.link(exp, left, false)
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
			id, reserved := p.parameterName(e)
			if reserved {
				return &ParameterList{Token: p.curToken}
			}
			if id == nil {
				if !p.patternParameterError(e) {
					p.addError("arrow function parameters must be identifiers")
				}
				return &ParameterList{Token: p.curToken}
			}
			params[i] = id
		}
		return &ParameterList{Token: p.curToken, Parameters: params}
	}

	if len(exps) == 0 {
		return nil
	}
	if len(exps) > 1 && exps[1] != nil {
		// (a, b) not followed by => is JavaScript's comma operator, which Kitwork does not have: it kept
		// a and dropped b without a word (14/10/2026)
		at := Token{Position: startPosition(exps[1]), Source: getNodeSource(exps[1])}
		p.addErrorAt(at, "Kitwork không có toán tử dấu phẩy: (a, b) chỉ là tham số của hàm mũi tên (a, b) => …; mỗi việc viết thành một câu lệnh")
		return nil
	}
	if exps[0] != nil {
		if p.grouped == nil {
			p.grouped = map[Expression]bool{}
		}
		p.grouped[exps[0]] = true
	}
	return exps[0]
}

// patternParameterError reports a destructuring parameter, ({ id }) => … or ([a, b]) => …, which
// Kitwork does not have yet; false when e is something else.
func (p *Parser) patternParameterError(e Expression) bool {
	at := Token{Position: startPosition(e), Source: getNodeSource(e)}
	kind := ""
	switch node := e.(type) {
	case *ObjectLiteral:
		kind = "object"
	case *ArrayLiteral:
		kind = "mảng"
		if len(node.Elements) > 0 && node.Elements[0] != nil {
			// an array literal's own place is its closing bracket; its first element is nearer the start
			at.Position = startPosition(node.Elements[0])
		}
	case *AssignmentExpression:
		// ({ a } = {}) => …, ([a] = []) => …: the left side was read as a pattern already.
		if pattern, ok := node.Name.(*BindingPattern); ok && node.Token.Kind == Assign {
			kind = "object"
			if pattern.Array {
				kind = "mảng"
			}
		} else if node.Token.Kind == Assign {
			p.addErrorAt(at, fmt.Sprintf("Kitwork chưa có giá trị mặc định cho tham số (%s); viết trong thân: const x = tham_số ?? mặc_định", node.String()))
			return true
		}
	}
	if kind == "" {
		return false
	}
	p.addErrorAt(at, fmt.Sprintf("Kitwork chưa có tách %s trong tham số; nhận cả giá trị rồi tách trong thân: (o) => { const { a, b } = o; … }", kind))
	return true
}

func (p *Parser) parseArrowFunction(left Expression) Expression {
	tok := p.curToken // =>
	p.nextToken()

	var params []*Identifier
	if pl, ok := left.(*ParameterList); ok {
		params = pl.Parameters
	} else if id, reserved := p.parameterName(left); reserved {
		return nil
	} else if id != nil {
		params = []*Identifier{id}
	} else {
		p.addError("arrow function parameters must be identifiers")
		return nil
	}

	// Xử lý block { } hoặc single expression
	if p.curTokenIs(LeftBrace) {
		return &FunctionLiteral{
			Token:      tok,
			Parameters: params,
			Body:       p.parseFunctionBlock(),
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
	p.addErrorAt(p.peekToken, fmt.Sprintf("cần %s ở đây, gặp %q", k, p.peekToken.String()))
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
		if p.peekTokenIs(end) {
			p.addError(fmt.Sprintf("trailing comma is not supported before %s", end))
			return nil
		}
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

// addError reports msg at the current token, file:line:column first, as addErrorAt does (14/10/2026;
// it gave a byte offset).
func (p *Parser) addError(msg string) {
	p.addErrorAt(p.curToken, msg)
}
func (p *Parser) Errors() []string {
	return p.errors
}

// addErrorAt báo lỗi tại tok dạng file:dòng:cột — cột đếm ký tự từ 1, như chẩn đoán lúc chạy của compiler.
//
// Only the first error is shown (parseProgramSource): a later one, most often what the first left
// behind, is kept without its place, since finding a place reads the source from its start and a file
// that fails at every level (code nested past nestingLimit) would read it once per level.
func (p *Parser) addErrorAt(tok Token, msg string) {
	if len(p.errors) > 0 {
		p.errors = append(p.errors, msg)
		return
	}
	p.errors = append(p.errors, p.location(tok)+": "+msg)
}

// location trả về chỗ tok bắt đầu dạng file:dòng:cột (chỉ dòng:cột khi nguồn không có tên). Cột đếm
// theo ký tự như trình soạn thảo hiện, không theo byte (14/10/2026): chữ có dấu trước chỗ lỗi không làm
// cột lệch. Thiếu nguồn thì lùi về vị trí byte.
func (p *Parser) location(tok Token) string {
	pos := int(tok.Position)
	if p.text == nil || pos < 0 || pos > len(p.text) {
		return fmt.Sprintf("pos %d", pos)
	}
	line, lineStart := 1, 0
	for index := 0; index < pos; index++ {
		if p.text[index] == '\n' {
			line++
			lineStart = index + 1
		}
	}
	where := fmt.Sprintf("%d:%d", line, utf8.RuneCount(p.text[lineStart:pos])+1)
	if tok.Source != "" {
		where = tok.Source + ":" + where
	}
	return where
}

/* =============================================================================
   4. TỪ KHOÁ: làm tên thuộc tính được, làm tên biến thì không
   Như JS (IdentifierName): sau dấu chấm và trước ':' trong object, mọi từ khoá là tên thuộc tính —
   { function: 1 } là { "function": 1 }, o.function là o["function"], cùng bytecode. Chỗ đặt tên biến
   (const/let, tham số, tên hàm, biến đếm for, tên import, viết tắt { x }) thì từ khoá là lỗi biên dịch.
   ============================================================================= */

// statementWords là các chữ parser cho nghĩa riêng ở đầu câu lệnh (break;, continue;, case …:, default:).
// Lexer để chúng là identifier nên vẫn làm tên thuộc tính được, nhưng một biến mang tên đó không bao giờ
// dùng được, nên chúng cũng không làm tên biến.
var statementWords = map[string]bool{"break": true, "case": true, "default": true, "continue": true}

// operatorWords là các chữ parser đọc thành toán tử đứng trước toán hạng: typeof x (14/10/2026). Như
// statementWords, chúng làm tên thuộc tính được ({ typeof: 1 }, o.typeof), tên biến thì không.
var operatorWords = map[string]bool{"typeof": true}

// languageWord: một chữ lexer để là identifier mà parser cho nghĩa riêng, nên không làm tên biến.
func languageWord(word string) bool {
	return statementWords[word] || operatorWords[word] || notYetWords[word] != ""
}

// notYetWords are JavaScript words Kitwork does not have yet (14/10/2026): each was a name nothing
// declares, so delete o.a left o as it was and the rest of the line was another statement. Each is a
// compile error saying how to write it. They stay property names: o.delete("a"), { in: 1 }, and are no
// variable names. continue and typeof were here until they were made (14/10/2026, the same day).
var notYetWords = map[string]string{
	"instanceof": "Kitwork chưa có instanceof (không có lớp): một mảng thì Array.isArray(x)",
	"delete":     "Kitwork chưa có delete: bỏ một khoá thì o.delete(\"khoá\") (sửa o), hoặc o.omit(\"khoá\") (object mới)",
	"in":         "Kitwork chưa có toán tử in: hỏi một khoá thì o.has(\"khoá\")",
}

// keywordSpelling trả về chữ của một token từ khoá trong nguồn ("function", "true", "undefined",
// "while"…). Token true/false/null/undefined không giữ chữ nên đọc lại từ input. ok là false với mọi
// token không phải từ khoá của lexer (kể cả identifier).
func (p *Parser) keywordSpelling(tok Token) (string, bool) {
	if tok.Kind == Ident || tok.Length <= 0 {
		return "", false
	}
	start := int(tok.Position - p.l.base)
	end := start + int(tok.Length)
	if start < 0 || end > len(p.l.input) {
		return "", false
	}
	word := string(p.l.input[start:end])
	if kind, ok := Keywords[word]; !ok || kind != tok.Kind {
		return "", false
	}
	return word, true
}

func (p *Parser) isKeyword(tok Token) bool {
	_, ok := p.keywordSpelling(tok)
	return ok
}

// wordOf trả về chữ của một identifier hoặc từ khoá.
func (p *Parser) wordOf(tok Token) string {
	if word, ok := p.keywordSpelling(tok); ok {
		return word
	}
	return tok.Value.Text()
}

// reservedWord báo tok có phải chữ không làm tên biến được không: từ khoá của lexer, hoặc statementWords.
func (p *Parser) reservedWord(tok Token) (string, bool) {
	if tok.Kind == Ident {
		word := tok.Value.Text()
		return word, languageWord(word)
	}
	return p.keywordSpelling(tok)
}

func (p *Parser) reservedNameError(tok Token, word string) {
	p.addErrorAt(tok, fmt.Sprintf("%s là từ khoá của ngôn ngữ, không làm tên biến được; làm khoá object thì viết thẳng { %s: … } hoặc { \"%s\": … }, đọc thì o.%s",
		word, word, word, word))
}

// reservedImportError là lỗi từ khoá cho một tên import: cách viết đúng là đặt tên khác, không phải khoá object.
func (p *Parser) reservedImportError(tok Token, word, rename string) {
	p.addErrorAt(tok, fmt.Sprintf("%s là từ khoá của ngôn ngữ, không làm tên biến được; đặt tên khác: %s", word, rename))
}

// bindingName kiểm tok (đang là curToken) làm tên biến được: từ khoá thì báo lỗi từ khoá, thứ khác không
// phải identifier thì báo otherwise.
func (p *Parser) bindingName(tok Token, otherwise string) bool {
	if word, reserved := p.reservedWord(tok); reserved {
		p.reservedNameError(tok, word)
		return false
	}
	if tok.Kind != Ident {
		p.addErrorAt(tok, otherwise)
		return false
	}
	return true
}

// expectBindingName như expectPeek(Ident) cho một tên biến, nhưng từ khoá được báo bằng lỗi từ khoá.
func (p *Parser) expectBindingName() bool {
	if word, reserved := p.reservedWord(p.peekToken); reserved {
		p.reservedNameError(p.peekToken, word)
		return false
	}
	return p.expectPeek(Ident)
}

// parameterName trả về identifier mà một tham số đã parse đặt tên. Từ khoá thì báo lỗi, reserved = true
// (true/false/null/undefined parse thành Literal; break/case/default thì parseIdentifier đã báo); biểu
// thức khác không phải tên thì trả về nil, false để nơi gọi xử lý như trước.
func (p *Parser) parameterName(e Expression) (*Identifier, bool) {
	switch node := e.(type) {
	case *Identifier:
		return node, false
	case *Literal:
		if word, ok := p.keywordSpelling(node.Token); ok {
			p.reservedNameError(node.Token, word)
			return nil, true
		}
	}
	return nil, false
}

// functionParameters đổi danh sách tham số của `function (…)` thành identifier. Từ khoá thì false;
// biểu thức khác để nil như trước, compiler sẽ từ chối.
func (p *Parser) functionParameters(exps []Expression) ([]*Identifier, bool) {
	params := make([]*Identifier, len(exps))
	for index, e := range exps {
		id, reserved := p.parameterName(e)
		if reserved {
			return nil, false
		}
		if id == nil && p.patternParameterError(e) {
			return nil, false
		}
		params[index] = id
	}
	return params, true
}

func (p *Parser) parseFunctionStatement() Statement {
	tok := p.curToken

	if !p.expectBindingName() {
		return nil
	}
	name := &Identifier{Token: p.curToken, Value: p.curToken.Value.Text()}

	if !p.expectPeek(LeftParen) {
		return nil
	}

	params, ok := p.functionParameters(p.parseExpressionList(RightParen))
	if !ok {
		return nil
	}

	if !p.expectPeek(LeftBrace) {
		return nil
	}

	body := p.parseFunctionBlock()

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
		Function:     true,
	}
}

func (p *Parser) parseFunctionExpression() Expression {
	tok := p.curToken // function

	if word, reserved := p.reservedWord(p.peekToken); reserved {
		p.reservedNameError(p.peekToken, word)
		return nil
	}
	if p.peekTokenIs(Ident) {
		p.nextToken() // Skip named function expression internal name
	}

	if !p.expectPeek(LeftParen) {
		return nil
	}

	params, ok := p.functionParameters(p.parseExpressionList(RightParen))
	if !ok {
		return nil
	}

	if !p.expectPeek(LeftBrace) {
		return nil
	}

	body := p.parseFunctionBlock()

	return &FunctionLiteral{
		Token:      tok,
		Parameters: params,
		Body:       body,
	}
}
