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
)

type expression struct {
	kind  expressionKind
	value value.Value
	parts []string

	left  *expression
	right *expression
	alt   *expression
}

type renderScope struct {
	parent *renderScope
	values map[string]value.Value

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

func resolveExpression(
	compiled *expression,
	data value.Value,
	scope *renderScope,
) value.Value {
	if compiled == nil {
		return value.Value{}
	}

	switch compiled.kind {
	case expressionLiteral:
		return compiled.value
	case expressionTernary:
		if resolveExpression(compiled.left, data, scope).Truthy() {
			return resolveExpression(compiled.right, data, scope)
		}
		return resolveExpression(compiled.alt, data, scope)
	case expressionNullish:
		left := resolveExpression(compiled.left, data, scope)
		if left.IsBlank() {
			return resolveExpression(compiled.right, data, scope)
		}
		return left
	case expressionOr:
		left := resolveExpression(compiled.left, data, scope)
		if !left.Truthy() {
			return resolveExpression(compiled.right, data, scope)
		}
		return left
	case expressionAnd:
		left := resolveExpression(compiled.left, data, scope)
		if left.Truthy() {
			return resolveExpression(compiled.right, data, scope)
		}
		return left
	case expressionPath:
		return resolvePath(compiled.parts, data, scope)
	}

	left := resolveExpression(compiled.left, data, scope)
	right := resolveExpression(compiled.right, data, scope)
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
