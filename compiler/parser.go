package compiler

import (
	"fmt"

	"github.com/kitwork/engine/token"
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

var precedences = map[token.Kind]int{
	token.Equal:       EQUALS,
	token.NotEqual:    EQUALS,
	token.Less:        LESSGREATER,
	token.Greater:     LESSGREATER,
	token.Plus:        SUM,
	token.Minus:       SUM,
	token.Star:        PRODUCT,
	token.Slash:       PRODUCT,
	token.LeftParen:   CALL,
	token.LeftBracket: INDEX,
	token.Dot:         MEMBER,
	token.Assign:      ASSIGN,
	token.LogicalAnd:  AND,
	token.LogicalOr:   OR,
	token.FatArrow:    ARROW,
}

type (
	prefixParseFn func() Expression
	infixParseFn  func(Expression) Expression
)

type Parser struct {
	l      *Lexer
	errors []string

	curToken  token.Token
	peekToken token.Token

	prefixParseFns map[token.Kind]prefixParseFn
	infixParseFns  map[token.Kind]infixParseFn
}

func NewParser(l *Lexer) *Parser {
	p := &Parser{l: l, errors: []string{}}

	p.prefixParseFns = make(map[token.Kind]prefixParseFn)
	p.infixParseFns = make(map[token.Kind]infixParseFn)

	// Đăng ký Prefix
	p.registerPrefix(token.Identifier, p.parseIdentifier)
	p.registerPrefix(token.Number, p.parseLiteral)
	p.registerPrefix(token.String, p.parseLiteral)
	p.registerPrefix(token.Boolean, p.parseLiteral)
	p.registerPrefix(token.Null, p.parseLiteral)
	p.registerPrefix(token.LogicalNot, p.parsePrefixExpression)
	p.registerPrefix(token.Minus, p.parsePrefixExpression)
	p.registerPrefix(token.LeftParen, p.parseGroupedExpression)
	p.registerPrefix(token.If, p.parseIfExpression)
	// p.registerPrefix(token.For, p.parseForStatement)
	// p.registerPrefix(token.Defer, p.parseDeferStatement)
	// p.registerPrefix(token.Go, p.parseSpawnStatement)
	p.registerPrefix(token.LeftBracket, p.parseArrayLiteral)
	p.registerPrefix(token.LeftBrace, p.parseObjectLiteral)

	// Đăng ký Infix
	p.registerInfix(token.Plus, p.parseInfixExpression)
	p.registerInfix(token.Minus, p.parseInfixExpression)
	p.registerInfix(token.Star, p.parseInfixExpression)
	p.registerInfix(token.Slash, p.parseInfixExpression)
	p.registerInfix(token.Equal, p.parseInfixExpression)
	p.registerInfix(token.NotEqual, p.parseInfixExpression)
	p.registerInfix(token.Less, p.parseInfixExpression)
	p.registerInfix(token.Greater, p.parseInfixExpression)
	p.registerInfix(token.Assign, p.parseInfixExpression)
	p.registerInfix(token.LeftParen, p.parseCallExpression)
	p.registerInfix(token.Dot, p.parseDotExpression)
	p.registerInfix(token.LeftBracket, p.parseIndexExpression)
	p.registerInfix(token.LogicalAnd, p.parseInfixExpression)
	p.registerInfix(token.LogicalOr, p.parseInfixExpression)
	p.registerInfix(token.FatArrow, p.parseArrowFunction)

	p.nextToken()
	p.nextToken()
	return p
}

/* =============================================================================
   1. STATEMENTS (Câu lệnh)
   ============================================================================= */

func (p *Parser) ParseProgram() *Program {
	program := &Program{Statements: []Statement{}}
	for p.curToken.Kind != token.EOF {
		// 1. Bỏ qua dấu chấm phẩy thừa
		if p.curToken.Kind == token.Semicolon {
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
	return program
}

func (p *Parser) parseStatement() Statement {
	switch p.curToken.Kind {
	case token.Let, token.Const:
		return p.parseVarStatement()
	case token.Return:
		return p.parseReturnStatement()
	// case token.If, token.For, token.Defer, token.Go:
	// 	// Trong cấu trúc này, If, For, Defer và Go là Expression/Statement linh hoạt
	// 	return p.parseExpressionStatement()
	default:
		return p.parseExpressionStatement()
	}

}

// Trong parser.go
func (p *Parser) parseVarStatement() Statement {
	stmt := &VarStatement{Token: p.curToken}

	if p.peekTokenIs(token.LeftBrace) {
		// Destructuring Object: const { a, b } = ...
		p.nextToken() // cur: {
		stmt.DestructMode = DestructObject
		stmt.Names = []*Identifier{}
		for !p.peekTokenIs(token.RightBrace) {
			p.nextToken() // cur: identifier
			if !p.curTokenIs(token.Identifier) {
				return nil
			}
			stmt.Names = append(stmt.Names, &Identifier{Token: p.curToken, Value: p.curToken.Value.Text()})
			if p.peekTokenIs(token.Comma) {
				p.nextToken()
			}
		}
		if !p.expectPeek(token.RightBrace) {
			return nil
		}
	} else if p.peekTokenIs(token.LeftBracket) {
		// Destructuring Array: const [ a, b ] = ...
		p.nextToken() // cur: [
		stmt.DestructMode = DestructArray
		stmt.Names = []*Identifier{}
		for !p.peekTokenIs(token.RightBracket) {
			p.nextToken() // cur: identifier
			if !p.curTokenIs(token.Identifier) {
				return nil
			}
			stmt.Names = append(stmt.Names, &Identifier{Token: p.curToken, Value: p.curToken.Value.Text()})
			if p.peekTokenIs(token.Comma) {
				p.nextToken()
			}
		}
		if !p.expectPeek(token.RightBracket) {
			return nil
		}
	} else {
		// Standard: const a = ...
		if !p.expectPeek(token.Identifier) {
			return nil
		}
		stmt.Names = []*Identifier{{Token: p.curToken, Value: p.curToken.Value.Text()}}
		stmt.DestructMode = DestructNone
	}

	if !p.expectPeek(token.Assign) {
		return nil
	}
	// Lúc này curToken đang là '='

	p.nextToken() // Nhảy qua dấu '=' để đứng tại vị trí của giá trị (VD: số 10)

	stmt.Value = p.parseExpression(LOWEST)

	return stmt
}

func (p *Parser) parseReturnStatement() Statement {
	stmt := &ReturnStatement{Token: p.curToken}
	p.nextToken()
	stmt.ReturnValue = p.parseExpression(LOWEST)
	// if p.peekTokenIs(token.Semicolon) {
	// 	p.nextToken()
	// }
	return stmt
}

func (p *Parser) parseExpressionStatement() Statement {
	stmt := &ExpressionStatement{Token: p.curToken}
	stmt.Expression = p.parseExpression(LOWEST)
	// if p.peekTokenIs(token.Semicolon) {
	// 	p.nextToken()
	// }
	return stmt
}

func (p *Parser) parseBlockStatement() *BlockStatement {
	block := &BlockStatement{Token: p.curToken, Statements: []Statement{}}
	p.nextToken() // Bỏ dấu {

	for !p.curTokenIs(token.RightBrace) && !p.curTokenIs(token.EOF) {
		// Bỏ qua dấu chấm phẩy thừa
		if p.curTokenIs(token.Semicolon) {
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

	for !p.peekTokenIs(token.Semicolon) && precedence < p.peekPrecedence() {
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

func (p *Parser) parseInfixExpression(left Expression) Expression {
	// Xử lý toán tử gán riêng biệt
	if p.curToken.Kind == token.Assign {
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
	if p.peekTokenIs(token.LeftParen) {
		p.nextToken() // Chuyển curToken sang dấu '('
		return &MethodCallExpression{
			Token:     tok,
			Object:    left,
			Method:    name,
			Arguments: p.parseExpressionList(token.RightParen),
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

func (p *Parser) parseIfExpression() Expression {
	exp := &IfExpression{Token: p.curToken}
	if !p.expectPeek(token.LeftParen) {
		return nil
	}
	p.nextToken()
	exp.Condition = p.parseExpression(LOWEST)
	if !p.expectPeek(token.RightParen) {
		return nil
	}

	if p.peekTokenIs(token.LeftBrace) {
		p.nextToken()
		exp.Consequence = p.parseBlockStatement()
	} else {
		p.nextToken()
		stmt := p.parseStatement()
		exp.Consequence = &BlockStatement{Statements: []Statement{stmt}}
	}

	if p.peekTokenIs(token.Else) {
		p.nextToken()
		if p.peekTokenIs(token.LeftBrace) {
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

func (p *Parser) parseForStatement() Expression {
	exp := &ForStatement{Token: p.curToken}

	if !p.expectPeek(token.LeftParen) {
		return nil
	}

	if !p.expectPeek(token.Identifier) {
		return nil
	}
	exp.Item = &Identifier{Token: p.curToken, Value: p.curToken.Value.Text()}

	// if !p.expectPeek(token.In) {
	// 	return nil
	// }

	p.nextToken()
	exp.Iterable = p.parseExpression(LOWEST)

	if !p.expectPeek(token.RightParen) {
		return nil
	}

	if !p.expectPeek(token.LeftBrace) {
		return nil
	}
	exp.Body = p.parseBlockStatement()

	return exp
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
		Elements: p.parseExpressionList(token.RightBracket),
	}
}

func (p *Parser) parseObjectLiteral() Expression {
	obj := &ObjectLiteral{Token: p.curToken, Pairs: make(map[Expression]Expression)}
	for !p.peekTokenIs(token.RightBrace) {
		p.nextToken()
		key := p.parseExpression(LOWEST)
		if !p.expectPeek(token.Colon) {
			return nil
		}
		p.nextToken()
		val := p.parseExpression(LOWEST)
		obj.Pairs[key] = val
		if !p.peekTokenIs(token.RightBrace) && !p.expectPeek(token.Comma) {
			return nil
		}
	}
	if !p.expectPeek(token.RightBrace) {
		return nil
	}
	return obj
}

func (p *Parser) parseCallExpression(left Expression) Expression {
	exp := &CallExpression{Token: p.curToken, Function: left}
	exp.Arguments = p.parseExpressionList(token.RightParen)
	return exp
}

func (p *Parser) parseIndexExpression(left Expression) Expression {
	exp := &IndexExpression{Token: p.curToken, Left: left}
	p.nextToken()
	exp.Index = p.parseExpression(LOWEST)
	if !p.expectPeek(token.RightBracket) {
		return nil
	}
	return exp
}

func (p *Parser) parseGroupedExpression() Expression {
	// Nếu là () => ...
	if p.peekTokenIs(token.RightParen) {
		p.nextToken() // Sang dấu )
		return &ParameterList{Token: p.curToken, Parameters: []*Identifier{}}
	}

	exps := p.parseExpressionList(token.RightParen)

	// Nếu tiếp sau là =>, biến list này thành ParameterList
	if p.peekTokenIs(token.FatArrow) {
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
	if p.curTokenIs(token.LeftBrace) {
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

func (p *Parser) curTokenIs(k token.Kind) bool  { return p.curToken.Kind == k }
func (p *Parser) peekTokenIs(k token.Kind) bool { return p.peekToken.Kind == k }

func (p *Parser) expectPeek(k token.Kind) bool {
	if p.peekTokenIs(k) {
		p.nextToken()
		return true
	}
	p.addError(fmt.Sprintf("expected %s, got %s", k, p.peekToken.Kind))
	return false
}

func (p *Parser) parseExpressionList(end token.Kind) []Expression {
	list := []Expression{}
	if p.peekTokenIs(end) {
		p.nextToken()
		return list
	}
	p.nextToken()
	list = append(list, p.parseExpression(LOWEST))
	for p.peekTokenIs(token.Comma) {
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

func (p *Parser) registerPrefix(k token.Kind, fn prefixParseFn) { p.prefixParseFns[k] = fn }
func (p *Parser) registerInfix(k token.Kind, fn infixParseFn)   { p.infixParseFns[k] = fn }
func (p *Parser) addError(msg string)                           { p.errors = append(p.errors, msg) }
func (p *Parser) Errors() []string {
	return p.errors
}
