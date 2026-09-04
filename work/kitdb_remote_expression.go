package work

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

const (
	kitSQLExpressionNodeLimit        = 256
	kitSQLExpressionDepthLimit       = 24
	kitSQLExpressionArgumentLimit    = 16
	kitSQLExpressionSetLimit         = 128
	kitDBRemoteExpressionInputLimit  = 10_000
	kitDBRemoteExpressionOrderLimit  = 8
	kitDBRemoteExpressionResultLimit = kitDBRemoteSelectRowLimit
)

type kitSQLExpressionKind uint8

const (
	kitSQLExpressionLiteral kitSQLExpressionKind = iota + 1
	kitSQLExpressionReference
	kitSQLExpressionStar
	kitSQLExpressionUnary
	kitSQLExpressionBinary
	kitSQLExpressionFunction
	kitSQLExpressionCase
)

type kitSQLCaseBranch struct {
	when *kitSQLExpression
	then *kitSQLExpression
}

// kitSQLExpression is a bounded, immutable-after-planning SQL expression IR.
// References are resolved once to canonical field identifiers before rows are scanned.
type kitSQLExpression struct {
	kind       kitSQLExpressionKind
	literal    value.Value
	reference  string
	operator   string
	arguments  []*kitSQLExpression
	caseBase   *kitSQLExpression
	branches   []kitSQLCaseBranch
	fallback   *kitSQLExpression
	resultKind string
	resolved   bool
}

type kitSQLExpressionReferenceResolver func(string) (string, string, error)
type kitSQLExpressionValueResolver func(string) (value.Value, error)

func (parser *kitSQLParser) expression() (*kitSQLExpression, error) {
	expression, err := parser.expressionOr()
	if err != nil {
		return nil, err
	}
	if err := validateKitSQLExpression(expression); err != nil {
		return nil, err
	}
	return expression, nil
}

func (parser *kitSQLParser) nestedExpression(
	parse func() (*kitSQLExpression, error),
) (*kitSQLExpression, error) {
	parser.expressionDepth++
	if parser.expressionDepth > kitSQLExpressionDepthLimit {
		parser.expressionDepth--
		return nil, fmt.Errorf("kitdb SQL: expression exceeds depth %d", kitSQLExpressionDepthLimit)
	}
	expression, err := parse()
	parser.expressionDepth--
	return expression, err
}

func (parser *kitSQLParser) expressionOr() (*kitSQLExpression, error) {
	left, err := parser.expressionAnd()
	if err != nil {
		return nil, err
	}
	for parser.acceptKeyword("or") {
		right, err := parser.expressionAnd()
		if err != nil {
			return nil, err
		}
		left = kitSQLBinaryExpression("or", left, right)
	}
	return left, nil
}

func (parser *kitSQLParser) expressionAnd() (*kitSQLExpression, error) {
	left, err := parser.expressionNot()
	if err != nil {
		return nil, err
	}
	for parser.acceptKeyword("and") {
		right, err := parser.expressionNot()
		if err != nil {
			return nil, err
		}
		left = kitSQLBinaryExpression("and", left, right)
	}
	return left, nil
}

func (parser *kitSQLParser) expressionNot() (*kitSQLExpression, error) {
	if parser.acceptKeyword("not") {
		child, err := parser.nestedExpression(parser.expressionNot)
		if err != nil {
			return nil, err
		}
		return &kitSQLExpression{kind: kitSQLExpressionUnary, operator: "not", arguments: []*kitSQLExpression{child}}, nil
	}
	if parser.acceptSymbol("*") {
		if !parser.acceptKeyword("search") {
			return nil, fmt.Errorf("kitdb SQL: * is valid in WHERE only as * SEARCH")
		}
		query, err := parser.expressionConcatenation()
		if err != nil {
			return nil, err
		}
		return newKitSQLSearchExpression(nil, query), nil
	}
	if parser.acceptKeyword("search") {
		query, err := parser.expressionConcatenation()
		if err != nil {
			return nil, err
		}
		return newKitSQLSearchExpression(nil, query), nil
	}
	return parser.expressionComparison()
}

func (parser *kitSQLParser) expressionComparison() (*kitSQLExpression, error) {
	if search, matched, err := parser.expressionSearchTuple(); matched || err != nil {
		return search, err
	}
	left, err := parser.expressionConcatenation()
	if err != nil {
		return nil, err
	}
	if parser.acceptKeyword("is") {
		operator := "is null"
		if parser.acceptKeyword("not") {
			operator = "is not null"
		}
		if err := parser.expectKeyword("null"); err != nil {
			return nil, err
		}
		return &kitSQLExpression{kind: kitSQLExpressionUnary, operator: operator, arguments: []*kitSQLExpression{left}}, nil
	}
	if parser.acceptKeyword("search") {
		query, err := parser.expressionConcatenation()
		if err != nil {
			return nil, err
		}
		return newKitSQLSearchExpression([]*kitSQLExpression{left}, query), nil
	}

	negated := parser.acceptKeyword("not")
	switch {
	case parser.acceptKeyword("in"):
		operator := "in"
		if negated {
			operator = "not in"
		}
		if err := parser.expectSymbol("("); err != nil {
			return nil, err
		}
		arguments := []*kitSQLExpression{left}
		if parser.acceptSymbol(")") {
			return nil, fmt.Errorf("kitdb SQL: IN requires at least one value")
		}
		for {
			item, err := parser.nestedExpression(parser.expressionOr)
			if err != nil {
				return nil, err
			}
			arguments = append(arguments, item)
			if len(arguments)-1 > kitSQLExpressionSetLimit {
				return nil, fmt.Errorf("kitdb SQL: IN exceeds %d values", kitSQLExpressionSetLimit)
			}
			if parser.acceptSymbol(")") {
				break
			}
			if err := parser.expectSymbol(","); err != nil {
				return nil, err
			}
		}
		return &kitSQLExpression{kind: kitSQLExpressionFunction, operator: operator, arguments: arguments}, nil
	case parser.acceptKeyword("between"):
		operator := "between"
		if negated {
			operator = "not between"
		}
		lower, err := parser.nestedExpression(parser.expressionConcatenation)
		if err != nil {
			return nil, err
		}
		if err := parser.expectKeyword("and"); err != nil {
			return nil, err
		}
		upper, err := parser.nestedExpression(parser.expressionConcatenation)
		if err != nil {
			return nil, err
		}
		return &kitSQLExpression{kind: kitSQLExpressionFunction, operator: operator, arguments: []*kitSQLExpression{left, lower, upper}}, nil
	case parser.acceptKeyword("like"):
		operator := "like"
		if negated {
			operator = "not like"
		}
		right, err := parser.expressionConcatenation()
		if err != nil {
			return nil, err
		}
		return kitSQLBinaryExpression(operator, left, right), nil
	default:
		if negated {
			return nil, fmt.Errorf("kitdb SQL: expected IN, BETWEEN, or LIKE after NOT")
		}
	}

	operator := parser.peek()
	if operator.kind != kitSQLTokenSymbol || !kitSQLComparisonOperator(operator.text) {
		return left, nil
	}
	parser.take()
	right, err := parser.expressionConcatenation()
	if err != nil {
		return nil, err
	}
	return kitSQLBinaryExpression(operator.text, left, right), nil
}

func (parser *kitSQLParser) expressionConcatenation() (*kitSQLExpression, error) {
	left, err := parser.expressionAdditive()
	if err != nil {
		return nil, err
	}
	for parser.acceptSymbol("||") {
		right, err := parser.expressionAdditive()
		if err != nil {
			return nil, err
		}
		left = kitSQLBinaryExpression("||", left, right)
	}
	return left, nil
}

func (parser *kitSQLParser) expressionAdditive() (*kitSQLExpression, error) {
	left, err := parser.expressionMultiplicative()
	if err != nil {
		return nil, err
	}
	for {
		operator := ""
		switch {
		case parser.acceptSymbol("+"):
			operator = "+"
		case parser.acceptSymbol("-"):
			operator = "-"
		default:
			return left, nil
		}
		right, err := parser.expressionMultiplicative()
		if err != nil {
			return nil, err
		}
		left = kitSQLBinaryExpression(operator, left, right)
	}
}

func (parser *kitSQLParser) expressionMultiplicative() (*kitSQLExpression, error) {
	left, err := parser.expressionUnary()
	if err != nil {
		return nil, err
	}
	for {
		operator := ""
		switch {
		case parser.acceptSymbol("*"):
			operator = "*"
		case parser.acceptSymbol("/"):
			operator = "/"
		case parser.acceptSymbol("%"):
			operator = "%"
		default:
			return left, nil
		}
		right, err := parser.expressionUnary()
		if err != nil {
			return nil, err
		}
		left = kitSQLBinaryExpression(operator, left, right)
	}
}

func (parser *kitSQLParser) expressionUnary() (*kitSQLExpression, error) {
	for _, operator := range []string{"+", "-"} {
		if parser.acceptSymbol(operator) {
			child, err := parser.nestedExpression(parser.expressionUnary)
			if err != nil {
				return nil, err
			}
			return &kitSQLExpression{kind: kitSQLExpressionUnary, operator: operator, arguments: []*kitSQLExpression{child}}, nil
		}
	}
	return parser.expressionPrimary()
}

func (parser *kitSQLParser) expressionPrimary() (*kitSQLExpression, error) {
	if parser.acceptSymbol("(") {
		nested, err := parser.nestedExpression(parser.expressionOr)
		if err != nil {
			return nil, err
		}
		if err := parser.expectSymbol(")"); err != nil {
			return nil, err
		}
		return nested, nil
	}
	if parser.acceptKeyword("case") {
		return parser.expressionCase()
	}
	token := parser.take()
	switch token.kind {
	case kitSQLTokenPlaceholder:
		item, err := parser.resolvePlaceholder(token.text)
		if err != nil {
			return nil, err
		}
		return &kitSQLExpression{kind: kitSQLExpressionLiteral, literal: item}, nil
	case kitSQLTokenString:
		return &kitSQLExpression{kind: kitSQLExpressionLiteral, literal: value.New(token.text)}, nil
	case kitSQLTokenNumber:
		number, err := strconvParseKitSQLNumber(token.text)
		if err != nil {
			return nil, err
		}
		kind := "integer"
		if strings.Contains(token.text, ".") {
			kind = "float"
		}
		return &kitSQLExpression{
			kind: kitSQLExpressionLiteral, literal: value.New(number), resultKind: kind,
		}, nil
	case kitSQLTokenIdentifier:
		switch strings.ToLower(token.text) {
		case "null":
			return &kitSQLExpression{kind: kitSQLExpressionLiteral, literal: value.NewNil()}, nil
		case "true":
			return &kitSQLExpression{kind: kitSQLExpressionLiteral, literal: value.New(true)}, nil
		case "false":
			return &kitSQLExpression{kind: kitSQLExpressionLiteral, literal: value.New(false)}, nil
		}
		if parser.acceptSymbol("(") {
			return parser.expressionFunction(token.text)
		}
		reference := token.text
		for parser.acceptSymbol(".") {
			next := parser.take()
			if next.kind != kitSQLTokenIdentifier || next.text == "" {
				return nil, fmt.Errorf("kitdb SQL: expected identifier after dot")
			}
			reference += "." + next.text
		}
		return &kitSQLExpression{kind: kitSQLExpressionReference, reference: reference}, nil
	default:
		return nil, fmt.Errorf("kitdb SQL: expected an expression, got %q", token.text)
	}
}

func strconvParseKitSQLNumber(text string) (float64, error) {
	number, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, fmt.Errorf("kitdb SQL: invalid number %q", text)
	}
	return number, nil
}

func (parser *kitSQLParser) expressionFunction(name string) (*kitSQLExpression, error) {
	name = strings.ToLower(name)
	arguments := make([]*kitSQLExpression, 0, 2)
	if parser.acceptSymbol(")") {
		return &kitSQLExpression{kind: kitSQLExpressionFunction, operator: name}, nil
	}
	for {
		var argument *kitSQLExpression
		if parser.acceptSymbol("*") {
			argument = &kitSQLExpression{kind: kitSQLExpressionStar}
		} else {
			var err error
			argument, err = parser.nestedExpression(parser.expressionOr)
			if err != nil {
				return nil, err
			}
		}
		arguments = append(arguments, argument)
		if len(arguments) > kitSQLExpressionArgumentLimit {
			return nil, fmt.Errorf("kitdb SQL: %s exceeds %d arguments", strings.ToUpper(name), kitSQLExpressionArgumentLimit)
		}
		if parser.acceptSymbol(")") {
			break
		}
		if err := parser.expectSymbol(","); err != nil {
			return nil, err
		}
	}
	return &kitSQLExpression{kind: kitSQLExpressionFunction, operator: name, arguments: arguments}, nil
}

func (parser *kitSQLParser) expressionCase() (*kitSQLExpression, error) {
	expression := &kitSQLExpression{kind: kitSQLExpressionCase}
	if !parser.acceptKeyword("when") {
		base, err := parser.nestedExpression(parser.expressionOr)
		if err != nil {
			return nil, err
		}
		expression.caseBase = base
		if err := parser.expectKeyword("when"); err != nil {
			return nil, err
		}
	}
	for {
		condition, err := parser.nestedExpression(parser.expressionOr)
		if err != nil {
			return nil, err
		}
		if err := parser.expectKeyword("then"); err != nil {
			return nil, err
		}
		result, err := parser.nestedExpression(parser.expressionOr)
		if err != nil {
			return nil, err
		}
		expression.branches = append(expression.branches, kitSQLCaseBranch{when: condition, then: result})
		if len(expression.branches) > kitSQLExpressionArgumentLimit {
			return nil, fmt.Errorf("kitdb SQL: CASE exceeds %d branches", kitSQLExpressionArgumentLimit)
		}
		if !parser.acceptKeyword("when") {
			break
		}
	}
	if parser.acceptKeyword("else") {
		fallback, err := parser.nestedExpression(parser.expressionOr)
		if err != nil {
			return nil, err
		}
		expression.fallback = fallback
	}
	if err := parser.expectKeyword("end"); err != nil {
		return nil, err
	}
	return expression, nil
}

func kitSQLBinaryExpression(operator string, left, right *kitSQLExpression) *kitSQLExpression {
	return &kitSQLExpression{kind: kitSQLExpressionBinary, operator: strings.ToLower(operator), arguments: []*kitSQLExpression{left, right}}
}

func validateKitSQLExpression(expression *kitSQLExpression) error {
	nodes := 0
	var walk func(*kitSQLExpression, int) error
	walk = func(current *kitSQLExpression, depth int) error {
		if current == nil {
			return fmt.Errorf("kitdb SQL: expression contains an empty node")
		}
		nodes++
		if nodes > kitSQLExpressionNodeLimit {
			return fmt.Errorf("kitdb SQL: expression exceeds %d nodes", kitSQLExpressionNodeLimit)
		}
		if depth > kitSQLExpressionDepthLimit {
			return fmt.Errorf("kitdb SQL: expression exceeds depth %d", kitSQLExpressionDepthLimit)
		}
		for _, argument := range current.arguments {
			if err := walk(argument, depth+1); err != nil {
				return err
			}
		}
		if current.caseBase != nil {
			if err := walk(current.caseBase, depth+1); err != nil {
				return err
			}
		}
		for _, branch := range current.branches {
			if err := walk(branch.when, depth+1); err != nil {
				return err
			}
			if err := walk(branch.then, depth+1); err != nil {
				return err
			}
		}
		if current.fallback != nil {
			return walk(current.fallback, depth+1)
		}
		return nil
	}
	return walk(expression, 1)
}

func kitSQLExpressionContainsAggregate(expression *kitSQLExpression) bool {
	if expression == nil {
		return false
	}
	if expression.kind == kitSQLExpressionFunction && kitSQLAggregateName(expression.operator) {
		return true
	}
	for _, argument := range expression.arguments {
		if kitSQLExpressionContainsAggregate(argument) {
			return true
		}
	}
	if kitSQLExpressionContainsAggregate(expression.caseBase) || kitSQLExpressionContainsAggregate(expression.fallback) {
		return true
	}
	for _, branch := range expression.branches {
		if kitSQLExpressionContainsAggregate(branch.when) || kitSQLExpressionContainsAggregate(branch.then) {
			return true
		}
	}
	return false
}

func kitSQLAggregateName(name string) bool {
	switch strings.ToLower(name) {
	case "count", "sum", "avg", "min", "max":
		return true
	default:
		return false
	}
}

func resolveKitSQLExpression(expression *kitSQLExpression, resolve kitSQLExpressionReferenceResolver) (string, error) {
	if expression == nil {
		return "", fmt.Errorf("kitdb SQL: expression is unavailable")
	}
	if expression.resolved {
		return expression.resultKind, nil
	}
	resolveChild := func(child *kitSQLExpression) (string, error) {
		return resolveKitSQLExpression(child, resolve)
	}
	var result string
	switch expression.kind {
	case kitSQLExpressionLiteral:
		result = expression.resultKind
		if result == "" {
			result = kitSQLLogicalKind(expression.literal)
		}
	case kitSQLExpressionReference:
		if resolve == nil {
			return "", fmt.Errorf("kitdb SQL: expression references %q without a FROM source", expression.reference)
		}
		canonical, kind, err := resolve(expression.reference)
		if err != nil {
			return "", err
		}
		expression.reference, result = canonical, kind
	case kitSQLExpressionStar:
		return "", fmt.Errorf("kitdb SQL: * is valid only inside COUNT(*)")
	case kitSQLExpressionUnary:
		if len(expression.arguments) != 1 {
			return "", fmt.Errorf("kitdb SQL: unary %s expects one operand", strings.ToUpper(expression.operator))
		}
		kind, err := resolveChild(expression.arguments[0])
		if err != nil {
			return "", err
		}
		switch expression.operator {
		case "+", "-":
			if !kitSQLNumericKind(kind) && kind != "null" {
				return "", fmt.Errorf("kitdb SQL: unary %s requires a numeric value, got %s", expression.operator, kind)
			}
			result = kind
		case "not", "is null", "is not null":
			result = "bool"
		default:
			return "", fmt.Errorf("kitdb SQL: unsupported unary operator %q", expression.operator)
		}
	case kitSQLExpressionBinary:
		if len(expression.arguments) != 2 {
			return "", fmt.Errorf("kitdb SQL: binary %s expects two operands", expression.operator)
		}
		left, err := resolveChild(expression.arguments[0])
		if err != nil {
			return "", err
		}
		right, err := resolveChild(expression.arguments[1])
		if err != nil {
			return "", err
		}
		switch expression.operator {
		case "+", "-", "*", "/", "%":
			if (!kitSQLNumericKind(left) && left != "null") || (!kitSQLNumericKind(right) && right != "null") {
				return "", fmt.Errorf("kitdb SQL: %s requires numeric operands, got %s and %s", expression.operator, left, right)
			}
			result = kitSQLNumericResultKind(expression.operator, left, right)
		case "||":
			if !kitSQLConcatenableKind(left) || !kitSQLConcatenableKind(right) {
				return "", fmt.Errorf("kitdb SQL: || supports scalar values only, got %s and %s", left, right)
			}
			result = "text"
		case "and", "or", "=", "==", "!=", "<>", ">", ">=", "<", "<=", "like", "not like":
			result = "bool"
		default:
			return "", fmt.Errorf("kitdb SQL: unsupported binary operator %q", expression.operator)
		}
	case kitSQLExpressionFunction:
		kind, err := resolveKitSQLFunction(expression, resolveChild)
		if err != nil {
			return "", err
		}
		result = kind
	case kitSQLExpressionCase:
		if expression.caseBase != nil {
			if _, err := resolveChild(expression.caseBase); err != nil {
				return "", err
			}
		}
		result = "null"
		for _, branch := range expression.branches {
			if _, err := resolveChild(branch.when); err != nil {
				return "", err
			}
			kind, err := resolveChild(branch.then)
			if err != nil {
				return "", err
			}
			result = kitSQLMergeKinds(result, kind)
		}
		if expression.fallback != nil {
			kind, err := resolveChild(expression.fallback)
			if err != nil {
				return "", err
			}
			result = kitSQLMergeKinds(result, kind)
		}
	default:
		return "", fmt.Errorf("kitdb SQL: unsupported expression node %d", expression.kind)
	}
	if result == "" {
		result = "any"
	}
	expression.resultKind = result
	expression.resolved = true
	return result, nil
}

func resolveKitSQLFunction(
	expression *kitSQLExpression,
	resolve func(*kitSQLExpression) (string, error),
) (string, error) {
	name := strings.ToLower(expression.operator)
	if name == kitSQLSearchExpressionOperator {
		return "", fmt.Errorf("kitdb SQL: SEARCH is valid only as a SELECT WHERE predicate")
	}
	if kitSQLAggregateName(name) {
		return "", fmt.Errorf("kitdb SQL: aggregate %s must be a complete projection", strings.ToUpper(name))
	}
	if name == "in" || name == "not in" {
		if len(expression.arguments) < 2 {
			return "", fmt.Errorf("kitdb SQL: IN requires at least one value")
		}
		for _, argument := range expression.arguments {
			if _, err := resolve(argument); err != nil {
				return "", err
			}
		}
		return "bool", nil
	}
	if name == "between" || name == "not between" {
		if len(expression.arguments) != 3 {
			return "", fmt.Errorf("kitdb SQL: BETWEEN expects two bounds")
		}
		for _, argument := range expression.arguments {
			if _, err := resolve(argument); err != nil {
				return "", err
			}
		}
		return "bool", nil
	}

	arguments := make([]string, len(expression.arguments))
	for index, argument := range expression.arguments {
		kind, err := resolve(argument)
		if err != nil {
			return "", err
		}
		arguments[index] = kind
	}
	arity := func(minimum, maximum int) error {
		if len(arguments) < minimum || len(arguments) > maximum {
			if minimum == maximum {
				return fmt.Errorf("kitdb SQL: %s expects %d argument(s)", strings.ToUpper(name), minimum)
			}
			return fmt.Errorf("kitdb SQL: %s expects %d to %d arguments", strings.ToUpper(name), minimum, maximum)
		}
		return nil
	}
	switch name {
	case "coalesce":
		if err := arity(2, kitSQLExpressionArgumentLimit); err != nil {
			return "", err
		}
		result := "null"
		for _, kind := range arguments {
			result = kitSQLMergeKinds(result, kind)
		}
		return result, nil
	case "ifnull", "nullif":
		if err := arity(2, 2); err != nil {
			return "", err
		}
		return arguments[0], nil
	case "lower", "upper", "trim":
		if err := arity(1, 1); err != nil {
			return "", err
		}
		if arguments[0] != "text" && arguments[0] != "null" && arguments[0] != "any" {
			return "", fmt.Errorf("kitdb SQL: %s requires text, got %s", strings.ToUpper(name), arguments[0])
		}
		return "text", nil
	case "length":
		if err := arity(1, 1); err != nil {
			return "", err
		}
		if arguments[0] != "text" && arguments[0] != "blob" && arguments[0] != "null" && arguments[0] != "any" {
			return "", fmt.Errorf("kitdb SQL: LENGTH requires text or bytes, got %s", arguments[0])
		}
		return "integer", nil
	case "abs":
		if err := arity(1, 1); err != nil {
			return "", err
		}
		if !kitSQLNumericKind(arguments[0]) && arguments[0] != "null" {
			return "", fmt.Errorf("kitdb SQL: ABS requires a numeric value, got %s", arguments[0])
		}
		return arguments[0], nil
	case "round":
		if err := arity(1, 2); err != nil {
			return "", err
		}
		for _, kind := range arguments {
			if !kitSQLNumericKind(kind) && kind != "null" {
				return "", fmt.Errorf("kitdb SQL: ROUND requires numeric arguments, got %s", kind)
			}
		}
		return "float", nil
	default:
		return "", fmt.Errorf("kitdb SQL: unsupported function %s", strings.ToUpper(name))
	}
}

func evaluateKitSQLExpression(expression *kitSQLExpression, resolve kitSQLExpressionValueResolver) (value.Value, error) {
	if expression == nil {
		return value.Value{}, fmt.Errorf("kitdb SQL: expression is unavailable")
	}
	switch expression.kind {
	case kitSQLExpressionLiteral:
		return expression.literal, nil
	case kitSQLExpressionReference:
		if resolve == nil {
			return value.Value{}, fmt.Errorf("kitdb SQL: expression references %q without a FROM source", expression.reference)
		}
		return resolve(expression.reference)
	case kitSQLExpressionUnary:
		return evaluateKitSQLUnary(expression, resolve)
	case kitSQLExpressionBinary:
		return evaluateKitSQLBinary(expression, resolve)
	case kitSQLExpressionFunction:
		return evaluateKitSQLFunction(expression, resolve)
	case kitSQLExpressionCase:
		return evaluateKitSQLCase(expression, resolve)
	case kitSQLExpressionStar:
		return value.Value{}, fmt.Errorf("kitdb SQL: * cannot be evaluated as a value")
	default:
		return value.Value{}, fmt.Errorf("kitdb SQL: unsupported expression node %d", expression.kind)
	}
}

func evaluateKitSQLUnary(expression *kitSQLExpression, resolve kitSQLExpressionValueResolver) (value.Value, error) {
	item, err := evaluateKitSQLExpression(expression.arguments[0], resolve)
	if err != nil {
		return value.Value{}, err
	}
	switch expression.operator {
	case "is null":
		return value.New(item.IsNil()), nil
	case "is not null":
		return value.New(!item.IsNil()), nil
	case "not":
		truth, err := kitSQLExpressionTruth(item)
		if err != nil {
			return value.Value{}, err
		}
		return kitSQLTruthValue(kitDBNegateTruth(truth)), nil
	case "+", "-":
		if item.IsNil() {
			return value.NewNil(), nil
		}
		if item.K != value.Number {
			return value.Value{}, fmt.Errorf("kitdb SQL: unary %s requires a number", expression.operator)
		}
		if expression.operator == "-" {
			return value.New(-item.N), nil
		}
		return item, nil
	default:
		return value.Value{}, fmt.Errorf("kitdb SQL: unsupported unary operator %q", expression.operator)
	}
}

func evaluateKitSQLBinary(expression *kitSQLExpression, resolve kitSQLExpressionValueResolver) (value.Value, error) {
	operator := expression.operator
	left, err := evaluateKitSQLExpression(expression.arguments[0], resolve)
	if err != nil {
		return value.Value{}, err
	}
	if operator == "and" || operator == "or" {
		leftTruth, err := kitSQLExpressionTruth(left)
		if err != nil {
			return value.Value{}, err
		}
		if operator == "and" && leftTruth == kitDBFalse {
			return value.New(false), nil
		}
		if operator == "or" && leftTruth == kitDBTrue {
			return value.New(true), nil
		}
		right, err := evaluateKitSQLExpression(expression.arguments[1], resolve)
		if err != nil {
			return value.Value{}, err
		}
		rightTruth, err := kitSQLExpressionTruth(right)
		if err != nil {
			return value.Value{}, err
		}
		if operator == "and" {
			if rightTruth == kitDBFalse {
				return value.New(false), nil
			}
			if leftTruth == kitDBUnknown || rightTruth == kitDBUnknown {
				return value.NewNil(), nil
			}
			return value.New(true), nil
		}
		if rightTruth == kitDBTrue {
			return value.New(true), nil
		}
		if leftTruth == kitDBUnknown || rightTruth == kitDBUnknown {
			return value.NewNil(), nil
		}
		return value.New(false), nil
	}
	right, err := evaluateKitSQLExpression(expression.arguments[1], resolve)
	if err != nil {
		return value.Value{}, err
	}
	if left.IsNil() || right.IsNil() {
		return value.NewNil(), nil
	}
	switch operator {
	case "+", "-", "*", "/", "%":
		if left.K != value.Number || right.K != value.Number {
			return value.Value{}, fmt.Errorf("kitdb SQL: %s requires numeric operands", operator)
		}
		var result value.Value
		switch operator {
		case "+":
			result = left.Add(right)
		case "-":
			result = left.Sub(right)
		case "*":
			result = left.Mul(right)
		case "/":
			result = left.Div(right)
			if !result.IsNil() && kitSQLIntegerKind(expression.arguments[0].resultKind) &&
				kitSQLIntegerKind(expression.arguments[1].resultKind) {
				result.N = math.Trunc(result.N)
			}
		case "%":
			result = left.Mod(right)
		}
		if result.K == value.Invalid || (result.K == value.Number && (math.IsNaN(result.N) || math.IsInf(result.N, 0))) {
			return value.Value{}, fmt.Errorf("kitdb SQL: invalid result for %s", operator)
		}
		return result, nil
	case "||":
		if !kitSQLConcatenable(left) || !kitSQLConcatenable(right) {
			return value.Value{}, fmt.Errorf("kitdb SQL: || supports scalar values only")
		}
		return value.New(left.Text() + right.Text()), nil
	case "like", "not like":
		matched := kitDBLike(left.Text(), right.Text())
		if operator == "not like" {
			matched = !matched
		}
		return value.New(matched), nil
	case "=", "==", "!=", "<>", ">", ">=", "<", "<=":
		comparison, comparable := kitSQLExpressionCompare(left, right)
		if !comparable {
			if operator == "=" || operator == "==" {
				return value.New(false), nil
			}
			if operator == "!=" || operator == "<>" {
				return value.New(true), nil
			}
			return value.Value{}, fmt.Errorf("kitdb SQL: values of kind %s and %s are not order-comparable", left.K, right.K)
		}
		switch operator {
		case "=", "==":
			return value.New(comparison == 0), nil
		case "!=", "<>":
			return value.New(comparison != 0), nil
		case ">":
			return value.New(comparison > 0), nil
		case ">=":
			return value.New(comparison >= 0), nil
		case "<":
			return value.New(comparison < 0), nil
		default:
			return value.New(comparison <= 0), nil
		}
	default:
		return value.Value{}, fmt.Errorf("kitdb SQL: unsupported binary operator %q", operator)
	}
}

func evaluateKitSQLFunction(expression *kitSQLExpression, resolve kitSQLExpressionValueResolver) (value.Value, error) {
	name := expression.operator
	if name == "coalesce" || name == "ifnull" {
		for _, argument := range expression.arguments {
			item, err := evaluateKitSQLExpression(argument, resolve)
			if err != nil {
				return value.Value{}, err
			}
			if !item.IsNil() {
				return item, nil
			}
		}
		return value.NewNil(), nil
	}
	if name == "in" || name == "not in" {
		left, err := evaluateKitSQLExpression(expression.arguments[0], resolve)
		if err != nil {
			return value.Value{}, err
		}
		if left.IsNil() {
			return value.NewNil(), nil
		}
		hasNull := false
		matched := false
		for _, argument := range expression.arguments[1:] {
			candidate, err := evaluateKitSQLExpression(argument, resolve)
			if err != nil {
				return value.Value{}, err
			}
			if candidate.IsNil() {
				hasNull = true
				continue
			}
			comparison, comparable := kitSQLExpressionCompare(left, candidate)
			if comparable && comparison == 0 {
				matched = true
				break
			}
		}
		if matched {
			return value.New(name == "in"), nil
		}
		if hasNull {
			return value.NewNil(), nil
		}
		return value.New(name == "not in"), nil
	}
	if name == "between" || name == "not between" {
		items := make([]value.Value, 3)
		for index, argument := range expression.arguments {
			item, err := evaluateKitSQLExpression(argument, resolve)
			if err != nil {
				return value.Value{}, err
			}
			if item.IsNil() {
				return value.NewNil(), nil
			}
			items[index] = item
		}
		lower, lowerOK := kitSQLExpressionCompare(items[0], items[1])
		upper, upperOK := kitSQLExpressionCompare(items[0], items[2])
		if !lowerOK || !upperOK {
			return value.Value{}, fmt.Errorf("kitdb SQL: BETWEEN values are not comparable")
		}
		matched := lower >= 0 && upper <= 0
		if name == "not between" {
			matched = !matched
		}
		return value.New(matched), nil
	}

	arguments := make([]value.Value, len(expression.arguments))
	for index, argument := range expression.arguments {
		item, err := evaluateKitSQLExpression(argument, resolve)
		if err != nil {
			return value.Value{}, err
		}
		arguments[index] = item
	}
	switch name {
	case "nullif":
		if arguments[0].IsNil() {
			return value.NewNil(), nil
		}
		if arguments[1].IsNil() {
			return arguments[0], nil
		}
		comparison, comparable := kitSQLExpressionCompare(arguments[0], arguments[1])
		if comparable && comparison == 0 {
			return value.NewNil(), nil
		}
		return arguments[0], nil
	case "lower", "upper", "trim":
		if arguments[0].IsNil() {
			return value.NewNil(), nil
		}
		if arguments[0].K != value.String {
			return value.Value{}, fmt.Errorf("kitdb SQL: %s requires text", strings.ToUpper(name))
		}
		switch name {
		case "lower":
			return value.New(strings.ToLower(arguments[0].String())), nil
		case "upper":
			return value.New(strings.ToUpper(arguments[0].String())), nil
		default:
			return value.New(strings.TrimSpace(arguments[0].String())), nil
		}
	case "length":
		if arguments[0].IsNil() {
			return value.NewNil(), nil
		}
		switch arguments[0].K {
		case value.String:
			return value.New(utf8.RuneCountInString(arguments[0].String())), nil
		case value.Bytes:
			return value.New(len(arguments[0].Bytes())), nil
		default:
			return value.Value{}, fmt.Errorf("kitdb SQL: LENGTH requires text or bytes")
		}
	case "abs":
		if arguments[0].IsNil() {
			return value.NewNil(), nil
		}
		if arguments[0].K != value.Number {
			return value.Value{}, fmt.Errorf("kitdb SQL: ABS requires a number")
		}
		return value.New(math.Abs(arguments[0].N)), nil
	case "round":
		if arguments[0].IsNil() || (len(arguments) == 2 && arguments[1].IsNil()) {
			return value.NewNil(), nil
		}
		if arguments[0].K != value.Number || (len(arguments) == 2 && arguments[1].K != value.Number) {
			return value.Value{}, fmt.Errorf("kitdb SQL: ROUND requires numeric arguments")
		}
		digits := 0
		if len(arguments) == 2 {
			digits = int(arguments[1].N)
			if arguments[1].N != float64(digits) || digits < -15 || digits > 15 {
				return value.Value{}, fmt.Errorf("kitdb SQL: ROUND precision must be an integer from -15 to 15")
			}
		}
		precision := digits
		if precision < 0 {
			precision = -precision
		}
		factor := math.Pow10(precision)
		result := arguments[0].N
		if digits >= 0 {
			result = math.Round(result*factor) / factor
		} else {
			result = math.Round(result/factor) * factor
		}
		return value.New(result), nil
	default:
		return value.Value{}, fmt.Errorf("kitdb SQL: unsupported function %s", strings.ToUpper(name))
	}
}

func evaluateKitSQLCase(expression *kitSQLExpression, resolve kitSQLExpressionValueResolver) (value.Value, error) {
	var base value.Value
	var err error
	if expression.caseBase != nil {
		base, err = evaluateKitSQLExpression(expression.caseBase, resolve)
		if err != nil {
			return value.Value{}, err
		}
	}
	for _, branch := range expression.branches {
		condition, err := evaluateKitSQLExpression(branch.when, resolve)
		if err != nil {
			return value.Value{}, err
		}
		matched := false
		if expression.caseBase == nil {
			truth, err := kitSQLExpressionTruth(condition)
			if err != nil {
				return value.Value{}, err
			}
			matched = truth == kitDBTrue
		} else if !base.IsNil() && !condition.IsNil() {
			comparison, comparable := kitSQLExpressionCompare(base, condition)
			matched = comparable && comparison == 0
		}
		if matched {
			return evaluateKitSQLExpression(branch.then, resolve)
		}
	}
	if expression.fallback != nil {
		return evaluateKitSQLExpression(expression.fallback, resolve)
	}
	return value.NewNil(), nil
}

func kitSQLExpressionTruth(item value.Value) (kitDBTruth, error) {
	if item.IsNil() {
		return kitDBUnknown, nil
	}
	switch item.K {
	case value.Bool, value.Number:
		return kitDBBooleanTruth(item.N != 0), nil
	default:
		return kitDBUnknown, fmt.Errorf("kitdb SQL: condition requires a boolean or numeric value, got %s", item.K)
	}
}

func kitSQLTruthValue(truth kitDBTruth) value.Value {
	switch truth {
	case kitDBTrue:
		return value.New(true)
	case kitDBFalse:
		return value.New(false)
	default:
		return value.NewNil()
	}
}

func kitSQLExpressionCompare(left, right value.Value) (int, bool) {
	if left.K != right.K {
		return 0, false
	}
	switch left.K {
	case value.Number, value.Bool, value.Time, value.Duration, value.String, value.Bytes:
		return kitDBCompareValues(left, right), true
	default:
		return 0, false
	}
}

func kitSQLConcatenable(item value.Value) bool {
	switch item.K {
	case value.Number, value.Bool, value.Time, value.Duration, value.String, value.Bytes:
		return true
	default:
		return false
	}
}

func kitSQLConcatenableKind(kind string) bool {
	switch kind {
	case "json", "jsonb", "array", "vector":
		return false
	default:
		return true
	}
}

func kitSQLLogicalKind(item value.Value) string {
	if item.IsNil() {
		return "null"
	}
	return kitSQLScalarKind(item)
}

func kitSQLNumericKind(kind string) bool {
	return kitDBNumericAggregateKind(kind)
}

func kitSQLNumericResultKind(operator, left, right string) string {
	if left == "null" {
		return right
	}
	if right == "null" {
		return left
	}
	if operator == "/" {
		return "float"
	}
	if kitSQLIntegerKind(left) && kitSQLIntegerKind(right) {
		if left == right {
			return left
		}
		// The original integer kind is the Kitwork VM's frozen 53-bit domain.
		// Never narrow a mixed legacy expression to a PostgreSQL-width kind.
		if left == "integer" || right == "integer" {
			return "integer"
		}
		if left == "int32" || right == "int32" {
			return "int32"
		}
		return "smallint"
	}
	return "float"
}

func kitSQLIntegerKind(kind string) bool {
	switch kind {
	case "integer", "smallint", "int32", "serial", "year", "month", "day":
		return true
	default:
		return false
	}
}

func kitSQLMergeKinds(left, right string) string {
	if left == "" || left == "null" {
		return right
	}
	if right == "" || right == "null" {
		return left
	}
	if left == right {
		return left
	}
	if kitSQLNumericKind(left) && kitSQLNumericKind(right) {
		return "float"
	}
	return "any"
}

type kitDBRemoteExpressionProjection struct {
	expression *kitSQLExpression
}

type kitDBRemoteExpressionOrder struct {
	expression *kitSQLExpression
	direction  string
}

type kitDBRemoteExpressionPlan struct {
	projections []kitDBRemoteExpressionProjection
	columns     []kitDBRemoteColumn
	orders      []kitDBRemoteExpressionOrder
	filter      *kitSQLExpression
	sourceOrder bool
}

type kitDBRemoteExpressionRow struct {
	projected []value.Value
	ordered   []value.Value
	key       []byte
}

type kitDBRemoteUpdateExpression struct {
	field      string
	expression *kitSQLExpression
}

type kitDBRemoteUpdateExpressionPlan struct {
	base        map[string]value.Value
	fixed       map[string]value.Value
	expressions []kitDBRemoteUpdateExpression
}

func kitDBRemoteMutationExpressionResolver(
	table *SchemaTable,
	statement kitSQLStatement,
) kitSQLExpressionReferenceResolver {
	return func(requested string) (string, string, error) {
		qualifier, _ := kitSQLReferenceParts(requested)
		if qualifier != "" && !strings.EqualFold(qualifier, statement.table) &&
			!strings.EqualFold(qualifier, table.table) {
			return "", "", fmt.Errorf("kitdb SQL: unknown table qualifier %q", qualifier)
		}
		name, field, err := kitDBRemoteField(table.definition, requested)
		return name, field.Kind, err
	}
}

func prepareKitDBRemoteMutationFilter(
	table *SchemaTable,
	statement kitSQLStatement,
) (*kitSQLExpression, error) {
	filter := statement.whereExpression
	if filter == nil {
		return nil, nil
	}
	if kitSQLExpressionContainsAggregate(filter) {
		return nil, fmt.Errorf("kitdb SQL: aggregate expressions are not valid in WHERE")
	}
	if _, err := resolveKitSQLExpression(
		filter,
		kitDBRemoteMutationExpressionResolver(table, statement),
	); err != nil {
		return nil, err
	}
	return filter, nil
}

func prepareKitDBRemoteUpdateExpressionPlan(
	table *SchemaTable,
	statement kitSQLStatement,
) (*kitDBRemoteUpdateExpressionPlan, error) {
	columns := append([]string(nil), statement.updateColumns...)
	if len(columns) == 0 {
		for requested := range statement.values {
			columns = append(columns, requested)
		}
		for requested := range statement.updateExpressions {
			columns = append(columns, requested)
		}
		sort.Strings(columns)
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("kitdb SQL: UPDATE has no assignments")
	}

	plan := &kitDBRemoteUpdateExpressionPlan{base: make(map[string]value.Value, len(columns))}
	assigned := make(map[string]struct{}, len(columns))
	resolve := kitDBRemoteMutationExpressionResolver(table, statement)
	for _, requested := range columns {
		field, definitionField, err := kitDBRemoteField(table.definition, requested)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(field)
		if _, duplicate := assigned[key]; duplicate {
			return nil, fmt.Errorf("kitdb SQL: duplicate UPDATE column %q", requested)
		}
		assigned[key] = struct{}{}
		if item, found := statement.values[requested]; found {
			item, err = coerceKitDBSQLScalar(definitionField, item)
			if err != nil {
				return nil, err
			}
			plan.base[field] = item
			continue
		}
		expression := statement.updateExpressions[requested]
		if expression == nil {
			return nil, fmt.Errorf("kitdb SQL: UPDATE assignment for %q is unavailable", requested)
		}
		if kitSQLExpressionContainsAggregate(expression) {
			return nil, fmt.Errorf("kitdb SQL: aggregate expressions are not valid in UPDATE SET")
		}
		if _, err := resolveKitSQLExpression(expression, resolve); err != nil {
			return nil, fmt.Errorf("kitdb SQL: UPDATE SET %s: %w", field, err)
		}
		plan.expressions = append(plan.expressions, kitDBRemoteUpdateExpression{
			field: field, expression: expression,
		})
	}

	touches := make(map[string]value.Value)
	applyTouch(table.columns, touches)
	for field, item := range touches {
		if _, explicit := assigned[strings.ToLower(field)]; !explicit {
			plan.base[field] = item
		}
	}
	if len(plan.expressions) == 0 {
		fixed, err := table.kitDBUpdateChanges([]value.Value{value.New(plan.base)})
		if err != nil {
			return nil, err
		}
		plan.fixed = fixed
	}
	return plan, nil
}

func kitDBRemoteStoredRowResolver(
	table *SchemaTable,
	row kitDBStoredRow,
) kitSQLExpressionValueResolver {
	return func(reference string) (value.Value, error) {
		spec := table.columns[reference]
		if spec == nil {
			return value.Value{}, fmt.Errorf("kitdb SQL: unresolved expression field %q", reference)
		}
		item, found := row.values[reference]
		if !found {
			item = value.NewNil()
		}
		return coerceRead(spec.kind, item), nil
	}
}

func kitDBRemoteMutationMatcher(
	ctx context.Context,
	table *SchemaTable,
	filter *kitSQLExpression,
	operation string,
	returning bool,
) kitDBMutationMatcher {
	scanned, matched := 0, 0
	return func(row kitDBStoredRow) (bool, error) {
		scanned++
		if scanned == 1 || scanned&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if filter != nil {
			item, err := evaluateKitSQLExpression(filter, kitDBRemoteStoredRowResolver(table, row))
			if err != nil {
				return false, fmt.Errorf("kitdb SQL: WHERE expression: %w", err)
			}
			truth, err := kitSQLExpressionTruth(item)
			if err != nil {
				return false, fmt.Errorf("kitdb SQL: WHERE expression: %w", err)
			}
			if truth != kitDBTrue {
				return false, nil
			}
		}
		if returning {
			matched++
			if matched > query.DefaultDBMaxLimit {
				return false, fmt.Errorf(
					"kitdb SQL: %s RETURNING is bounded to %d rows",
					operation, query.DefaultDBMaxLimit,
				)
			}
		}
		return true, nil
	}
}

func (plan *kitDBRemoteUpdateExpressionPlan) updater(
	ctx context.Context,
	table *SchemaTable,
) kitDBMutationUpdater {
	if plan != nil && plan.fixed != nil {
		return func(kitDBStoredRow) (map[string]value.Value, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return plan.fixed, nil
		}
	}
	return func(row kitDBStoredRow) (map[string]value.Value, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		changes := cloneKitDBRow(plan.base)
		resolve := kitDBRemoteStoredRowResolver(table, row)
		for _, assignment := range plan.expressions {
			item, err := evaluateKitSQLExpression(assignment.expression, resolve)
			if err != nil {
				return nil, fmt.Errorf("kitdb SQL: UPDATE SET %s: %w", assignment.field, err)
			}
			changes[assignment.field] = item
		}
		return table.kitDBUpdateChanges([]value.Value{value.New(changes)})
	}
}

func kitDBRemoteUsesExpressionEngine(statement kitSQLStatement) bool {
	if statement.whereExpression != nil || statement.havingExpression != nil {
		return true
	}
	for _, projection := range statement.projections {
		if projection.expression != nil {
			return true
		}
	}
	for _, order := range statement.orders {
		if order.expression != nil {
			return true
		}
		projection, found, _ := kitDBRemoteProjectionAlias(statement.projections, order.column)
		if found && projection.expression != nil {
			return true
		}
	}
	return false
}

func prepareKitDBRemoteExpressionPlan(table *SchemaTable, statement kitSQLStatement) (*kitDBRemoteExpressionPlan, error) {
	if len(statement.orders) > kitDBRemoteExpressionOrderLimit {
		return nil, fmt.Errorf("kitdb SQL: SELECT exceeds %d ORDER BY expressions", kitDBRemoteExpressionOrderLimit)
	}
	plan := &kitDBRemoteExpressionPlan{sourceOrder: len(statement.orders) != 0}
	aliases := make(map[string][]*kitSQLExpression)
	resolve := func(requested string) (string, string, error) {
		name, field, err := kitDBRemoteField(table.definition, requested)
		return name, field.Kind, err
	}
	if statement.whereExpression != nil {
		if _, err := resolveKitSQLExpression(statement.whereExpression, resolve); err != nil {
			return nil, err
		}
		plan.filter = statement.whereExpression
	}
	for _, projection := range statement.projections {
		if projection.all {
			qualifier, field := kitSQLReferenceParts(projection.field)
			if field != "*" {
				return nil, fmt.Errorf("kitdb SQL: invalid star projection %q", projection.field)
			}
			if qualifier != "" && !strings.EqualFold(qualifier, statement.tableAlias) &&
				!strings.EqualFold(qualifier, table.table) {
				return nil, fmt.Errorf("kitdb SQL: unknown table qualifier %q", qualifier)
			}
			for _, field := range table.definition.Fields {
				expression := &kitSQLExpression{
					kind: kitSQLExpressionReference, reference: field.Name,
					resultKind: field.Kind, resolved: true,
				}
				plan.projections = append(plan.projections, kitDBRemoteExpressionProjection{expression: expression})
				plan.columns = append(plan.columns, kitDBRemoteColumn{name: field.Name, kind: field.Kind})
			}
			continue
		}
		if projection.aggregate != "" {
			return nil, fmt.Errorf("kitdb SQL: aggregate expressions require GROUP BY planning")
		}
		expression := projection.expression
		if expression == nil {
			expression = &kitSQLExpression{kind: kitSQLExpressionReference, reference: projection.field}
		}
		kind, err := resolveKitSQLExpression(expression, resolve)
		if err != nil {
			return nil, err
		}
		name := projection.alias
		if name == "" {
			name = projection.label
		}
		if name == "" && expression.kind == kitSQLExpressionReference {
			name = expression.reference
		}
		if name == "" {
			name = fmt.Sprintf("column%d", len(plan.columns)+1)
		}
		if kind == "null" || kind == "any" {
			kind = "text"
		}
		plan.projections = append(plan.projections, kitDBRemoteExpressionProjection{expression: expression})
		plan.columns = append(plan.columns, kitDBRemoteColumn{name: name, kind: kind})
		if projection.alias != "" {
			key := strings.ToLower(projection.alias)
			aliases[key] = append(aliases[key], expression)
		}
	}
	for _, order := range statement.orders {
		expression := order.expression
		if expression == nil {
			matches := aliases[strings.ToLower(order.column)]
			if len(matches) > 1 {
				return nil, fmt.Errorf("kitdb SQL: ORDER BY alias %q is ambiguous", order.column)
			}
			if len(matches) == 1 {
				expression = matches[0]
			} else {
				expression = &kitSQLExpression{kind: kitSQLExpressionReference, reference: order.column}
			}
		}
		if !expression.resolved {
			if _, err := resolveKitSQLExpression(expression, resolve); err != nil {
				return nil, err
			}
		}
		if expression.kind != kitSQLExpressionReference {
			plan.sourceOrder = false
		}
		plan.orders = append(plan.orders, kitDBRemoteExpressionOrder{expression: expression, direction: order.direction})
	}
	return plan, nil
}

func executeKitDBRemoteExpressionSelect(
	ctx context.Context,
	table *SchemaTable,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	if err := ensureKitDBRemoteTableReady(table); err != nil {
		return kitDBRemoteResult{}, err
	}
	if statement.predicate != nil {
		predicate, err := canonicalKitSQLPredicate(table.definition, *statement.predicate)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		table.builder().WherePredicate(predicate)
	}
	expressionPlan, err := prepareKitDBRemoteExpressionPlan(table, statement)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	result := kitDBRemoteResult{columns: expressionPlan.columns, rows: [][]value.Value{}}
	limit := query.DefaultDBLimit
	if statement.hasLimit {
		limit = statement.limit
	}
	if limit > kitDBRemoteExpressionResultLimit {
		limit = kitDBRemoteExpressionResultLimit
	}
	if limit == 0 {
		return result, nil
	}
	if expressionPlan.sourceOrder {
		for _, order := range expressionPlan.orders {
			table.OrderBy(order.expression.reference, order.direction)
		}
		if table.failed {
			return kitDBRemoteResult{}, fmt.Errorf("%s", table.failMsg)
		}
	}
	sourcePlan := table.builder().ExecutionPlan()
	sourcePlan.Limit, sourcePlan.Offset = 0, 0
	access, err := table.planKitDBAccess(sourcePlan)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	needsSort := len(expressionPlan.orders) != 0 && (!expressionPlan.sourceOrder || !access.orderCovered)
	rows := make([]kitDBRemoteExpressionRow, 0, min(limit+statement.offset, kitDBRemoteExpressionInputLimit))
	distinct := make(map[string]struct{})
	seen := 0
	qualified := 0
	err = table.scanKitDBRowsUsing(sourcePlan, access, func(row kitDBStoredRow) (bool, error) {
		seen++
		if needsSort && seen > kitDBRemoteExpressionInputLimit {
			return false, fmt.Errorf("kitdb SQL: expression ORDER BY exceeds %d source rows", kitDBRemoteExpressionInputLimit)
		}
		if seen&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		resolve := func(reference string) (value.Value, error) {
			spec := table.columns[reference]
			if spec == nil {
				return value.Value{}, fmt.Errorf("kitdb SQL: unresolved expression field %q", reference)
			}
			item, found := row.values[reference]
			if !found {
				item = value.NewNil()
			}
			return coerceRead(spec.kind, item), nil
		}
		if expressionPlan.filter != nil {
			matched, err := evaluateKitSQLExpression(expressionPlan.filter, resolve)
			if err != nil {
				return false, fmt.Errorf("kitdb SQL: WHERE expression: %w", err)
			}
			truth, err := kitSQLExpressionTruth(matched)
			if err != nil {
				return false, fmt.Errorf("kitdb SQL: WHERE expression: %w", err)
			}
			if truth != kitDBTrue {
				return false, nil
			}
		}
		projected := make([]value.Value, len(expressionPlan.projections))
		for index, projection := range expressionPlan.projections {
			item, err := evaluateKitSQLExpression(projection.expression, resolve)
			if err != nil {
				return false, fmt.Errorf("kitdb SQL: projection %d: %w", index+1, err)
			}
			projected[index] = item
		}
		if statement.distinct {
			key, err := kitSQLExpressionRowKey(projected)
			if err != nil {
				return false, err
			}
			if _, duplicate := distinct[key]; duplicate {
				return false, nil
			}
			if len(distinct) >= kitDBRemoteExpressionInputLimit {
				return false, fmt.Errorf("kitdb SQL: DISTINCT exceeds %d expression rows", kitDBRemoteExpressionInputLimit)
			}
			distinct[key] = struct{}{}
		}
		qualified++
		if !needsSort && qualified <= statement.offset {
			return false, nil
		}
		encoded := kitDBRemoteExpressionRow{projected: projected, key: bytes.Clone(row.key)}
		if needsSort {
			encoded.ordered = make([]value.Value, len(expressionPlan.orders))
			for index, order := range expressionPlan.orders {
				item, err := evaluateKitSQLExpression(order.expression, resolve)
				if err != nil {
					return false, fmt.Errorf("kitdb SQL: ORDER BY expression %d: %w", index+1, err)
				}
				encoded.ordered[index] = item
			}
		}
		rows = append(rows, encoded)
		return !needsSort && len(rows) >= limit, nil
	})
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	if needsSort {
		sort.SliceStable(rows, func(left, right int) bool {
			for index, order := range expressionPlan.orders {
				comparison := kitDBCompareValues(rows[left].ordered[index], rows[right].ordered[index])
				if comparison == 0 {
					continue
				}
				if strings.EqualFold(order.direction, "desc") {
					return comparison > 0
				}
				return comparison < 0
			}
			return bytes.Compare(rows[left].key, rows[right].key) < 0
		})
		offset := min(statement.offset, len(rows))
		rows = rows[offset:]
		if len(rows) > limit {
			rows = rows[:limit]
		}
	}
	result.rows = make([][]value.Value, len(rows))
	for index, row := range rows {
		result.rows[index] = row.projected
	}
	return result, nil
}

func explainKitDBRemoteExpressionSelect(
	ctx context.Context,
	table *SchemaTable,
	statement kitSQLStatement,
	columns []kitDBRemoteColumn,
) (kitDBRemoteResult, error) {
	if err := ensureKitDBRemoteTableReady(table); err != nil {
		return kitDBRemoteResult{}, err
	}
	if statement.predicate != nil {
		predicate, err := canonicalKitSQLPredicate(table.definition, *statement.predicate)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		table.builder().WherePredicate(predicate)
	}
	expressionPlan, err := prepareKitDBRemoteExpressionPlan(table, statement)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if expressionPlan.sourceOrder {
		for _, order := range expressionPlan.orders {
			table.OrderBy(order.expression.reference, order.direction)
		}
		if table.failed {
			return kitDBRemoteResult{}, fmt.Errorf("%s", table.failMsg)
		}
	}
	sourcePlan := table.builder().ExecutionPlan()
	sourcePlan.Limit, sourcePlan.Offset = 0, 0
	access, err := table.planKitDBAccess(sourcePlan)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	detail := kitDBExplainAccessDetail(table.table, access, sourcePlan) + "; PROJECT EXPRESSIONS"
	if expressionPlan.filter != nil {
		detail += "; FILTER EXPRESSION"
	}
	if statement.distinct {
		detail += "; HASH DISTINCT"
	}
	if len(expressionPlan.orders) != 0 {
		if expressionPlan.sourceOrder && access.orderCovered {
			detail += "; INDEX ORDER"
		} else {
			detail += fmt.Sprintf("; TEMP EXPRESSION SORT; INPUT CAP %d", kitDBRemoteExpressionInputLimit)
		}
	} else {
		detail += "; EARLY STOP"
	}
	limit := query.DefaultDBLimit
	if statement.hasLimit {
		limit = statement.limit
	}
	if limit > kitDBRemoteExpressionResultLimit {
		limit = kitDBRemoteExpressionResultLimit
	}
	detail += fmt.Sprintf("; LIMIT %d", limit)
	if statement.offset > 0 {
		detail += fmt.Sprintf(" OFFSET %d", statement.offset)
	}
	return kitDBRemoteResult{
		columns: columns,
		rows: [][]value.Value{{
			value.New(0), value.New(0), value.New(0), value.New(detail),
		}},
	}, nil
}

func kitSQLExpressionRowKey(items []value.Value) (string, error) {
	encoded, err := json.Marshal(value.New(items))
	if err != nil {
		return "", fmt.Errorf("kitdb SQL: cannot encode DISTINCT expression row: %w", err)
	}
	return string(encoded), nil
}

func kitSQLPredicateFromExpression(expression *kitSQLExpression) (*query.Predicate, bool) {
	if expression == nil {
		return nil, false
	}
	switch expression.kind {
	case kitSQLExpressionUnary:
		if len(expression.arguments) != 1 {
			return nil, false
		}
		if expression.operator == "not" {
			child, exact := kitSQLPredicateFromExpression(expression.arguments[0])
			if !exact {
				return nil, false
			}
			return &query.Predicate{Kind: query.PredicateNot, Children: []query.Predicate{*child}}, true
		}
		if expression.operator == "is null" || expression.operator == "is not null" {
			left, ok := kitSQLPredicateReference(expression.arguments[0])
			if !ok {
				return nil, false
			}
			predicate := kitSQLConditionPredicate(query.Condition{
				Column: left, Operator: expression.operator, Value: value.NewNil(), Logic: "AND",
			})
			return &predicate, true
		}
	case kitSQLExpressionBinary:
		if len(expression.arguments) != 2 {
			return nil, false
		}
		if expression.operator == "and" || expression.operator == "or" {
			left, leftExact := kitSQLPredicateFromExpression(expression.arguments[0])
			right, rightExact := kitSQLPredicateFromExpression(expression.arguments[1])
			if !leftExact || !rightExact {
				return nil, false
			}
			kind := query.PredicateAnd
			if expression.operator == "or" {
				kind = query.PredicateOr
			}
			predicate := kitSQLLogicalPredicate(kind, *left, *right)
			return &predicate, true
		}
		left, ok := kitSQLPredicateReference(expression.arguments[0])
		if !ok {
			return nil, false
		}
		condition := query.Condition{Column: left, Operator: expression.operator, Logic: "AND"}
		right := expression.arguments[1]
		if reference, found := kitSQLPredicateReference(right); found {
			condition.Value, condition.IsColumn = reference, true
		} else if constant, found := kitSQLConstantExpressionValue(right); found {
			condition.Value = constant
		} else {
			return nil, false
		}
		predicate := kitSQLConditionPredicate(condition)
		return &predicate, true
	case kitSQLExpressionFunction:
		if expression.operator == "in" || expression.operator == "not in" {
			if len(expression.arguments) < 2 {
				return nil, false
			}
			left, ok := kitSQLPredicateReference(expression.arguments[0])
			if !ok {
				return nil, false
			}
			items := make([]value.Value, len(expression.arguments)-1)
			for index, argument := range expression.arguments[1:] {
				constant, found := kitSQLConstantExpressionValue(argument)
				if !found {
					return nil, false
				}
				items[index] = constant
			}
			predicate := kitSQLConditionPredicate(query.Condition{
				Column: left, Operator: expression.operator, Value: value.New(items), Logic: "AND",
			})
			return &predicate, true
		}
		if expression.operator == "between" || expression.operator == "not between" {
			if len(expression.arguments) != 3 {
				return nil, false
			}
			left, ok := kitSQLPredicateReference(expression.arguments[0])
			if !ok {
				return nil, false
			}
			lower, lowerFound := kitSQLConstantExpressionValue(expression.arguments[1])
			upper, upperFound := kitSQLConstantExpressionValue(expression.arguments[2])
			if !lowerFound || !upperFound {
				return nil, false
			}
			predicate := kitSQLConditionPredicate(query.Condition{
				Column: left, Operator: expression.operator,
				Value: value.New([]value.Value{lower, upper}), Logic: "AND",
			})
			return &predicate, true
		}
	}
	return nil, false
}

func kitSQLConstantExpressionValue(expression *kitSQLExpression) (value.Value, bool) {
	if expression == nil || kitSQLExpressionContainsAggregate(expression) || kitSQLExpressionContainsReference(expression) {
		return value.Value{}, false
	}
	if _, err := resolveKitSQLExpression(expression, nil); err != nil {
		return value.Value{}, false
	}
	item, err := evaluateKitSQLExpression(expression, nil)
	return item, err == nil
}

func kitSQLExpressionContainsReference(expression *kitSQLExpression) bool {
	if expression == nil {
		return false
	}
	if expression.kind == kitSQLExpressionReference || expression.kind == kitSQLExpressionStar {
		return true
	}
	for _, argument := range expression.arguments {
		if kitSQLExpressionContainsReference(argument) {
			return true
		}
	}
	if kitSQLExpressionContainsReference(expression.caseBase) || kitSQLExpressionContainsReference(expression.fallback) {
		return true
	}
	for _, branch := range expression.branches {
		if kitSQLExpressionContainsReference(branch.when) || kitSQLExpressionContainsReference(branch.then) {
			return true
		}
	}
	return false
}

func kitSQLPredicateReference(expression *kitSQLExpression) (string, bool) {
	if expression == nil {
		return "", false
	}
	if expression.kind == kitSQLExpressionReference {
		return expression.reference, expression.reference != ""
	}
	if expression.kind != kitSQLExpressionFunction || !kitSQLAggregateName(expression.operator) ||
		len(expression.arguments) != 1 {
		return "", false
	}
	argument := expression.arguments[0]
	if argument.kind == kitSQLExpressionStar && expression.operator == "count" {
		return kitSQLAggregateReference(expression.operator, "*"), true
	}
	if argument.kind == kitSQLExpressionReference {
		return kitSQLAggregateReference(expression.operator, argument.reference), true
	}
	return "", false
}

func kitSQLPlannerPredicateFromExpression(expression *kitSQLExpression) *query.Predicate {
	if predicate, exact := kitSQLPredicateFromExpression(expression); exact {
		return predicate
	}
	if expression == nil || expression.kind != kitSQLExpressionBinary || expression.operator != "and" ||
		len(expression.arguments) != 2 {
		return nil
	}
	left := kitSQLPlannerPredicateFromExpression(expression.arguments[0])
	right := kitSQLPlannerPredicateFromExpression(expression.arguments[1])
	switch {
	case left == nil:
		return right
	case right == nil:
		return left
	default:
		predicate := kitSQLLogicalPredicate(query.PredicateAnd, *left, *right)
		return &predicate
	}
}
