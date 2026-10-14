package compiler

import "fmt"

// nestingLimit bounds how deep code may nest: parentheses, blocks, functions, operators, a chain
// a.b.c… or 1 + 1 + … (each link is one level of the tree). The parser, the resolver and the code
// generator all walk the tree recursively, so a file nested as deep as it is long would overflow the Go
// stack of the shared host while it compiles at boot: one tenant's file ending every tenant's process
// (14/10/2026). Past the limit the file is a compile error. Real code stays far below it.
const nestingLimit = 1000

// nestingMessage is the compile error of code nested past nestingLimit.
var nestingMessage = fmt.Sprintf("mã lồng quá sâu (hơn %d tầng ngoặc, khối, hàm, toán tử hay mắt xích a.b.c): tách bớt thành biến hoặc hàm", nestingLimit)

// enterNesting counts one level of the parser's own recursion; false (and the error, once) past the
// limit. Every true is matched by leaveNesting.
func (p *Parser) enterNesting() bool {
	if p.nesting >= nestingLimit {
		if !p.nestingReported {
			p.nestingReported = true
			p.addErrorAt(p.curToken, nestingMessage)
		}
		return false
	}
	p.nesting++
	return true
}

func (p *Parser) leaveNesting() { p.nesting-- }

// checkNesting walks the program's tree without recursion and reports the first node deeper than
// nestingLimit: a chain or an operator run nests the tree without the parser recursing, and everything
// after the parser walks it recursively.
func (p *Parser) checkNesting(program *Program) {
	if p.nestingReported || program == nil {
		return
	}
	type pending struct {
		node  Node
		depth int
	}
	stack := make([]pending, 0, 64)
	for _, statement := range program.Statements {
		stack = append(stack, pending{statement, 1})
	}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if top.depth > nestingLimit {
			p.nestingReported = true
			p.addErrorAt(Token{Position: getNodePosition(top.node), Source: getNodeSource(top.node)}, nestingMessage)
			return
		}
		for _, child := range nodeChildren(top.node) {
			stack = append(stack, pending{child, top.depth + 1})
		}
	}
}

// nodeChildren lists the nodes directly under node that are there (no nil).
func nodeChildren(node Node) []Node {
	var children []Node
	add := func(child Node) {
		if child != nil {
			children = append(children, child)
		}
	}
	expression := func(child Expression) {
		if child != nil {
			children = append(children, child)
		}
	}
	block := func(child *BlockStatement) {
		if child != nil {
			children = append(children, child)
		}
	}
	pattern := func(child *BindingPattern) {
		if child != nil {
			children = append(children, child)
		}
	}
	switch n := node.(type) {
	case *GroupStatement:
		for _, statement := range n.Statements {
			add(statement)
		}
	case *VarStatement:
		if n == nil {
			return nil
		}
		expression(n.Value)
		pattern(n.Pattern)
	case *BindingPattern:
		if n == nil {
			return nil
		}
		for _, element := range n.Elements {
			expression(element.Default)
			pattern(element.Pattern)
		}
	case *ExpressionStatement:
		if n != nil {
			expression(n.Expression)
		}
	case *BlockStatement:
		if n == nil {
			return nil
		}
		for _, statement := range n.Statements {
			add(statement)
		}
	case *ReturnStatement:
		if n != nil {
			expression(n.ReturnValue)
		}
	case *SwitchStatement:
		if n == nil {
			return nil
		}
		expression(n.Discriminant)
		for _, clause := range n.Cases {
			expression(clause.Test)
			for _, statement := range clause.Statements {
				add(statement)
			}
		}
	case *ForStatement:
		if n == nil {
			return nil
		}
		expression(n.Iterable)
		block(n.Body)
	case *ForRangeStatement:
		if n == nil {
			return nil
		}
		add(n.Init)
		expression(n.Cond)
		expression(n.Update)
		block(n.Body)
	case *DeferStatement:
		if n != nil {
			expression(n.Fn)
		}
	case *SpawnStatement:
		if n != nil {
			expression(n.Fn)
		}
	case *PrefixExpression:
		if n != nil {
			expression(n.Right)
		}
	case *InfixExpression:
		if n != nil {
			expression(n.Left)
			expression(n.Right)
		}
	case *IfExpression:
		if n == nil {
			return nil
		}
		expression(n.Condition)
		block(n.Consequence)
		block(n.Alternative)
	case *TernaryExpression:
		if n != nil {
			expression(n.Condition)
			expression(n.Consequence)
			expression(n.Alternative)
		}
	case *CallExpression:
		if n == nil {
			return nil
		}
		expression(n.Function)
		for _, argument := range n.Arguments {
			expression(argument)
		}
	case *MethodCallExpression:
		if n == nil {
			return nil
		}
		expression(n.Object)
		for _, argument := range n.Arguments {
			expression(argument)
		}
	case *MemberExpression:
		if n != nil {
			expression(n.Object)
		}
	case *IndexExpression:
		if n != nil {
			expression(n.Left)
			expression(n.Index)
		}
	case *ChainExpression:
		if n != nil {
			expression(n.Expression)
		}
	case *AssignmentExpression:
		if n != nil {
			expression(n.Name)
			expression(n.Value)
		}
	case *ArrayLiteral:
		if n == nil {
			return nil
		}
		for _, element := range n.Elements {
			expression(element)
		}
	case *ObjectLiteral:
		if n == nil {
			return nil
		}
		for _, entry := range n.Entries {
			if entry.Key != entry.Value {
				expression(entry.Key)
			}
			expression(entry.Value)
		}
	case *SpreadExpression:
		if n != nil {
			expression(n.Value)
		}
	case *FunctionLiteral:
		if n != nil {
			block(n.Body)
		}
	case *TemplateLiteral:
		if n == nil {
			return nil
		}
		for _, part := range n.Parts {
			expression(part)
		}
	}
	return children
}
