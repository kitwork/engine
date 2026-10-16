package render

import (
	"strconv"
	"strings"

	"github.com/kitwork/engine/value"
)

type expressionKind uint8

const (
	expressionPath expressionKind = iota
	expressionLiteral
	expressionTernary
	expressionNullish
	expressionOr
	expressionAnd
	expressionEqual
	expressionNotEqual
	expressionGreaterEqual
	expressionLessEqual
	expressionGreater
	expressionLess
	expressionAdd
	expressionSubtract
	expressionMultiply
	expressionDivide
	expressionModulo
	expressionMethod
	// the kinds only a {{ if }} condition writes (condition.go): !a, -a, +a, and a key of a value that
	// is no path ("text".length, a.b().c)
	expressionNot
	expressionNegate
	expressionNumber
	expressionMember
)

type expression struct {
	kind  expressionKind
	value value.Value
	parts []string
	args  []*expression

	left  *expression
	right *expression
	alt   *expression
}

type renderScope struct {
	parent *renderScope
	values map[string]value.Value
	// failed hears an expression whose value is a failure, on the root scope of a render (failure):
	// the expression as the engine reads it and the failure's message. nil writes the message where
	// the value would stand, the page's own way of saying it while developing.
	failed func(expression, message string)

	firstKey    string
	firstValue  value.Value
	secondKey   string
	secondValue value.Value
}

func (scope *renderScope) reset(parent *renderScope) {
	scope.parent = parent
	scope.firstKey = ""
	scope.firstValue = value.Value{}
	scope.secondKey = ""
	scope.secondValue = value.Value{}
	clear(scope.values)
}

func (scope *renderScope) bind(key string, item value.Value) {
	if key == "" {
		return
	}
	if scope.firstKey == "" {
		scope.firstKey = key
		scope.firstValue = item
		return
	}
	scope.secondKey = key
	scope.secondValue = item
}

// failure is the hook of the render this scope belongs to for an expression that failed (the root's
// failed), nil when the render writes the failure's message into the page.
func (scope *renderScope) failure() func(expression, message string) {
	for current := scope; current != nil; current = current.parent {
		if current.failed != nil {
			return current.failed
		}
	}
	return nil
}

func (scope *renderScope) get(key string) (value.Value, bool) {
	for current := scope; current != nil; current = current.parent {
		if current.secondKey != "" && key == current.secondKey {
			return current.secondValue, true
		}
		if current.firstKey != "" && key == current.firstKey {
			return current.firstValue, true
		}
		if item, ok := current.values[key]; ok {
			return item, true
		}
	}
	return value.Value{}, false
}

func (scope *renderScope) set(key string, item value.Value) {
	if scope == nil {
		return
	}
	if scope.secondKey != "" && key == scope.secondKey {
		scope.secondValue = item
		return
	}
	if scope.firstKey != "" && key == scope.firstKey {
		scope.firstValue = item
		return
	}
	if scope.values == nil {
		scope.values = make(map[string]value.Value)
	}
	scope.values[key] = item
}

var expressionOperators = [...]struct {
	token string
	kind  expressionKind
}{
	{token: "==", kind: expressionEqual},
	{token: "!=", kind: expressionNotEqual},
	{token: ">=", kind: expressionGreaterEqual},
	{token: "<=", kind: expressionLessEqual},
	{token: ">", kind: expressionGreater},
	{token: "<", kind: expressionLess},
	{token: "+", kind: expressionAdd},
	{token: "-", kind: expressionSubtract},
	{token: "*", kind: expressionMultiply},
	{token: "/", kind: expressionDivide},
	{token: "%", kind: expressionModulo},
}

func compileOutputExpression(raw string) (*expression, bool) {
	if strings.HasPrefix(raw, "raw(") && strings.HasSuffix(raw, ")") {
		return compileExpression(raw[4 : len(raw)-1]), true
	}
	return compileExpression(raw), false
}

func compileExpression(raw string) *expression {
	source := strings.TrimSpace(raw)
	if len(source) >= 2 &&
		((source[0] == '"' && source[len(source)-1] == '"') ||
			(source[0] == '\'' && source[len(source)-1] == '\'')) {
		return &expression{
			kind:  expressionLiteral,
			value: value.New(source[1 : len(source)-1]),
		}
	}
	if number, err := strconv.ParseFloat(source, 64); err == nil {
		return &expression{kind: expressionLiteral, value: value.New(number)}
	}

	question := findSplitIndex(source, func(index int) bool {
		return source[index] == '?'
	}, false)
	if question >= 0 && (question+1 >= len(source) || source[question+1] != '?') {
		remainder := source[question+1:]
		colonRelative := findSplitIndex(remainder, func(index int) bool {
			return remainder[index] == ':'
		}, false)
		if colonRelative >= 0 {
			colon := question + 1 + colonRelative
			return &expression{
				kind:  expressionTernary,
				left:  compileExpression(source[:question]),
				right: compileExpression(source[question+1 : colon]),
				alt:   compileExpression(source[colon+1:]),
			}
		}
	}

	if index := findSplitIndex(source, func(index int) bool {
		return source[index] == '?' && index+1 < len(source) && source[index+1] == '?'
	}, false); index >= 0 {
		return &expression{
			kind:  expressionNullish,
			left:  compileExpression(source[:index]),
			right: compileExpression(source[index+2:]),
		}
	}
	if index := findSplitIndex(source, func(index int) bool {
		return source[index] == '|' && index+1 < len(source) && source[index+1] == '|'
	}, false); index >= 0 {
		return &expression{
			kind:  expressionOr,
			left:  compileExpression(source[:index]),
			right: compileExpression(source[index+2:]),
		}
	}
	if index := findSplitIndex(source, func(index int) bool {
		return source[index] == '&' && index+1 < len(source) && source[index+1] == '&'
	}, false); index >= 0 {
		return &expression{
			kind:  expressionAnd,
			left:  compileExpression(source[:index]),
			right: compileExpression(source[index+2:]),
		}
	}

	for _, operator := range expressionOperators {
		index := findSplitIndex(source, func(index int) bool {
			return strings.HasPrefix(source[index:], operator.token)
		}, true)
		if index > 0 {
			return &expression{
				kind:  operator.kind,
				left:  compileExpression(source[:index]),
				right: compileExpression(source[index+len(operator.token):]),
			}
		}
	}

	// Method call: <receiver>.<name>(<args>). The receiver compiles recursively,
	// so chains like src.highlight().upper() resolve left-to-right. This is the
	// single mechanism for value methods — no pipe/filter DSL.
	if open := matchFinalParen(source); open > 0 {
		head := source[:open]
		if dot := strings.LastIndex(head, "."); dot > 0 {
			name := head[dot+1:]
			if isIdentifier(name) {
				return &expression{
					kind:  expressionMethod,
					left:  compileExpression(head[:dot]),
					parts: []string{name},
					args:  compileArgs(source[open+1 : len(source)-1]),
				}
			}
		}
	}

	if source == "." {
		return &expression{
			kind:  expressionPath,
			parts: []string{"."},
		}
	}
	return &expression{
		kind:  expressionPath,
		parts: strings.Split(source, "."),
	}
}

// matchFinalParen returns the index of the '(' that pairs with the final ')' of
// source, or -1 when source does not end in a balanced call. Quotes and escapes
// are respected so parentheses inside string arguments do not confuse matching.
func matchFinalParen(source string) int {
	if source == "" || source[len(source)-1] != ')' {
		return -1
	}
	var stack []int
	inDouble, inSingle := false, false
	for i := 0; i < len(source); i++ {
		char := source[i]
		if char == '\\' && i+1 < len(source) {
			i++
			continue
		}
		if char == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if char == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if inDouble || inSingle {
			continue
		}
		if char == '(' {
			stack = append(stack, i)
		} else if char == ')' {
			if len(stack) == 0 {
				return -1
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if i == len(source)-1 {
				return open
			}
		}
	}
	return -1
}

func isIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		char := name[i]
		switch {
		case char == '_', char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

func compileArgs(raw string) []*expression {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var args []*expression
	depth := 0
	inDouble, inSingle := false, false
	start := 0
	for i := 0; i < len(raw); i++ {
		char := raw[i]
		if char == '\\' && i+1 < len(raw) {
			i++
			continue
		}
		if char == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if char == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if inDouble || inSingle {
			continue
		}
		switch char {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',':
			if depth == 0 {
				args = append(args, compileExpression(raw[start:i]))
				start = i + 1
			}
		}
	}
	return append(args, compileExpression(raw[start:]))
}

func resolveExpression(
	compiled *expression,
	data value.Value,
	scope *renderScope,
) value.Value {
	if compiled == nil {
		return value.Value{}
	}

	// A failure an operand meets is the expression's failure, as it ends a script's run before the
	// operator (16/10/2026, with E3): {{ item.nope() == 3 }} and {{ item.nope() || "x" }} say why, where
	// they wrote false and x, and so does a condition. The arithmetic operators already passed one on.
	switch compiled.kind {
	case expressionLiteral:
		return compiled.value
	case expressionTernary:
		test := resolveExpression(compiled.left, data, scope)
		if test.K == value.Invalid {
			return test
		}
		if test.Truthy() {
			return resolveExpression(compiled.right, data, scope)
		}
		return resolveExpression(compiled.alt, data, scope)
	case expressionNullish:
		left := resolveExpression(compiled.left, data, scope)
		if left.K == value.Nil {
			return resolveExpression(compiled.right, data, scope)
		}
		return left
	case expressionOr:
		left := resolveExpression(compiled.left, data, scope)
		if left.K != value.Invalid && !left.Truthy() {
			return resolveExpression(compiled.right, data, scope)
		}
		return left
	case expressionAnd:
		left := resolveExpression(compiled.left, data, scope)
		if left.Truthy() {
			return resolveExpression(compiled.right, data, scope)
		}
		return left
	case expressionNot:
		operand := resolveExpression(compiled.left, data, scope)
		if operand.K == value.Invalid {
			return operand
		}
		return value.New(!operand.Truthy())
	case expressionNegate:
		// -a as the VM compiles it: a * -1, so a text or an object is a TYPE failure there too
		return resolveExpression(compiled.left, data, scope).Mul(value.New(-1))
	case expressionNumber:
		// +a as the VM compiles it (STEP 0): the number JavaScript's ToNumber makes of a
		return value.ToNumber(resolveExpression(compiled.left, data, scope))
	case expressionMember:
		return traverse(resolveExpression(compiled.left, data, scope), compiled.parts)
	case expressionPath:
		return resolvePath(compiled.parts, data, scope)
	case expressionMethod:
		receiver := resolveExpression(compiled.left, data, scope)
		args := make([]value.Value, len(compiled.args))
		for index, argument := range compiled.args {
			args[index] = resolveExpression(argument, data, scope)
		}
		// A bare highlight()/highlightScript() adopts the folder-scoped $highlight
		// default (set by router.highlight) so HTML and JS panels share one theme;
		// an explicit argument always wins.
		if len(args) == 0 && (compiled.parts[0] == "highlight" || compiled.parts[0] == "highlightScript") {
			if theme, ok := scope.get("$highlight"); ok && !theme.IsBlank() {
				args = append(args, theme)
			}
		}
		result := receiver.Invoke(compiled.parts[0], args...)
		// A name the receiver has no function for is not a function (14/10/2026): the call is a
		// runtime error, and a template prints a runtime error where the value would go, as it
		// prints any other. The path written before the dot says which call it was. A receiver
		// that is already a failure passes on as it is, named once by the call that failed.
		if message, ok := value.NotAFunctionFrom(result); ok && receiver.K != value.Invalid {
			return value.InvalidFailure(value.NotAFunctionCode, expressionSource(compiled.left)+"."+message)
		}
		return result
	}

	left := resolveExpression(compiled.left, data, scope)
	if left.K == value.Invalid {
		return left
	}
	right := resolveExpression(compiled.right, data, scope)
	if right.K == value.Invalid {
		return right
	}
	switch compiled.kind {
	case expressionEqual:
		return value.New(left.Equal(right))
	case expressionNotEqual:
		return value.New(!left.Equal(right))
	case expressionGreaterEqual:
		return value.New(left.GreaterEqual(right))
	case expressionLessEqual:
		return value.New(left.LessEqual(right))
	case expressionGreater:
		return value.New(left.Greater(right))
	case expressionLess:
		return value.New(left.Less(right))
	case expressionAdd:
		return left.Add(right)
	case expressionSubtract:
		return left.Sub(right)
	case expressionMultiply:
		return left.Mul(right)
	case expressionDivide:
		return left.Div(right)
	case expressionModulo:
		return left.Mod(right)
	default:
		return value.Value{}
	}
}

func resolvePath(
	parts []string,
	data value.Value,
	scope *renderScope,
) value.Value {
	if len(parts) == 1 && parts[0] == "." {
		return data
	}
	if len(parts) > 1 && parts[0] == "" {
		return traverse(data, parts[1:])
	}
	if len(parts) > 0 {
		if scoped, ok := scope.get(parts[0]); ok {
			if len(parts) > 1 {
				return traverse(scoped, parts[1:])
			}
			return scoped
		}
	}

	result := traverse(data, parts)
	if !result.IsNil() || len(parts) == 0 || !strings.HasPrefix(parts[0], "$") {
		return result
	}
	fallback := make([]string, len(parts))
	copy(fallback, parts)
	fallback[0] = strings.TrimPrefix(fallback[0], "$")
	return traverse(data, fallback)
}
