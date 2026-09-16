package relational

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type boundPredicate struct {
	kind             string
	operator         string
	field            *kitdbsql.Field
	literal          any
	arguments        []*boundPredicate
	function         *boundFunction
	castKind         string
	precision        int
	scale            int
	timePrecision    *int
	textLength       *int
	maximumTextBytes int
}

func bindPredicate(
	schema kitdbsql.Schema,
	plan *kitdbsql.CheckPlan,
	parameters []any,
) (*boundPredicate, error) {
	return bindPredicateAt(schema, plan, parameters, time.Now().UTC())
}

func bindPredicateAt(
	schema kitdbsql.Schema,
	plan *kitdbsql.CheckPlan,
	parameters []any,
	now time.Time,
) (*boundPredicate, error) {
	if plan == nil {
		return nil, nil
	}
	result := &boundPredicate{
		kind: plan.Kind, operator: strings.ToLower(plan.Operator), castKind: plan.CastKind,
		precision: plan.Precision, scale: plan.Scale, timePrecision: plan.TimePrecision,
		textLength: plan.TextLength,
	}
	switch plan.Kind {
	case "field":
		_, field, found := schema.FieldByName(unqualifiedColumn(plan.Field))
		if !found {
			return nil, fmt.Errorf("kitdb SQL: table %q has no WHERE field %q", schema.Name, plan.Field)
		}
		result.field = &field
	case "literal":
		value, err := resolveLiteralAt(plan.Literal, parameters, now)
		if err != nil {
			return nil, err
		}
		result.literal = value
	case "unary", "binary", "function", "cast":
	default:
		return nil, fmt.Errorf("kitdb SQL: unsupported predicate node %q", plan.Kind)
	}
	result.arguments = make([]*boundPredicate, len(plan.Arguments))
	for index := range plan.Arguments {
		argument, err := bindPredicateAt(schema, &plan.Arguments[index], parameters, now)
		if err != nil {
			return nil, err
		}
		result.arguments[index] = argument
	}
	if plan.Function != nil {
		var err error
		result.function, err = bindUserFunction(plan)
		if err != nil {
			return nil, err
		}
	}
	if result.kind == "binary" && len(result.arguments) == 2 {
		if err := coerceArithmeticParameters(schema, plan, result.arguments); err != nil {
			return nil, err
		}
		if err := coercePredicatePair(result.operator, result.arguments[0], result.arguments[1]); err != nil {
			return nil, err
		}
		if err := coerceDecimalExpressionPair(result.operator, result.arguments[0], result.arguments[1]); err != nil {
			return nil, err
		}
	}
	if result.kind == "function" && (result.operator == "in" || result.operator == "not in") {
		if len(result.arguments) < 2 {
			return nil, fmt.Errorf("kitdb SQL: IN requires an expression and at least one value")
		}
		if result.arguments[0].field != nil {
			for _, argument := range result.arguments[1:] {
				if argument.kind != "literal" {
					continue
				}
				coerced, err := coerceField(*result.arguments[0].field, argument.literal)
				if err != nil {
					return nil, err
				}
				argument.literal = readField(*result.arguments[0].field, coerced)
			}
		}
	}
	if result.kind == "function" && (result.operator == "between" || result.operator == "not between") {
		if len(result.arguments) != 3 {
			return nil, fmt.Errorf("kitdb SQL: BETWEEN requires an expression and two bounds")
		}
		if result.arguments[0].field != nil {
			for _, argument := range result.arguments[1:] {
				if argument.kind != "literal" {
					continue
				}
				coerced, err := coerceField(*result.arguments[0].field, argument.literal)
				if err != nil {
					return nil, err
				}
				argument.literal = readField(*result.arguments[0].field, coerced)
			}
		}
	}
	return result, nil
}

func arithmeticParameter(plan *kitdbsql.CheckPlan) bool {
	return plan.Kind == "literal" && plan.Literal.Kind == kitdbsql.LiteralParameter
}

func coerceArithmeticParameters(schema kitdbsql.Schema, plan *kitdbsql.CheckPlan, arguments []*boundPredicate) error {
	switch plan.Operator {
	case "+", "-", "*", "/", "%":
	default:
		return nil
	}
	for index := range plan.Arguments {
		parameter := arguments[index]
		if !arithmeticParameter(&plan.Arguments[index]) || parameter.literal == nil {
			continue
		}
		// Untyped wire parameters arrive as text. Infer only from the other
		// numeric expression, never from digits in a text field or SQL literal.
		if _, text := parameter.literal.(string); !text {
			continue
		}
		kind, field, err := scalarExpressionKind(schema, &plan.Arguments[1-index])
		if err != nil || !isNumericExpressionKind(kind) {
			continue
		}
		if field == nil {
			field = &kitdbsql.Field{Kind: kind}
		}
		value, err := coerceField(*field, parameter.literal)
		if err != nil {
			return err
		}
		parameter.literal = readField(*field, value)
		if isDecimalExpressionKind(kind) {
			parameter.literal = exactDecimal(value.(string))
		}
	}
	return nil
}

func coerceDecimalExpressionPair(operator string, left, right *boundPredicate) error {
	switch operator {
	case "+", "-", "*", "/", "%":
	default:
		return nil
	}
	for _, pair := range [][2]*boundPredicate{{left, right}, {right, left}} {
		fieldSide, literalSide := pair[0], pair[1]
		if fieldSide == nil || fieldSide.field == nil || literalSide == nil || literalSide.kind != "literal" {
			continue
		}
		typeInfo, found := kitdbsql.LookupKind(fieldSide.field.Kind)
		if !found || typeInfo.Family != kitdbsql.FamilyDecimal || literalSide.literal == nil {
			continue
		}
		coerced, err := coerceField(*fieldSide.field, literalSide.literal)
		if err != nil {
			return err
		}
		literalSide.literal = exactDecimal(coerced.(string))
	}
	return nil
}

func coercePredicatePair(operator string, left, right *boundPredicate) error {
	if left == nil || right == nil {
		return nil
	}
	if !isPredicateComparison(operator) && !strings.Contains(operator, "like") {
		return nil
	}
	if left.field != nil && right.field != nil && !exactUUIDFieldsCompatible(*left.field, *right.field) {
		return fmt.Errorf("kitdb SQL: UUID comparison requires matching exact UUID fields or an explicit cast")
	}
	var field *kitdbsql.Field
	var literal *boundPredicate
	if left.field != nil && right.kind == "literal" {
		field, literal = left.field, right
	} else if right.field != nil && left.kind == "literal" {
		field, literal = right.field, left
	}
	if field == nil {
		return nil
	}
	if operator == "like" || operator == "not like" || operator == "ilike" || operator == "not ilike" {
		if field.Kind == "uuid" && field.ExactUUID {
			return fmt.Errorf("kitdb SQL: LIKE on UUID requires an explicit CAST AS TEXT")
		}
		if _, ok := literal.literal.(string); !ok && literal.literal != nil {
			return fmt.Errorf("kitdb SQL: LIKE pattern must be text")
		}
		return nil
	}
	coerced, err := coerceField(*field, literal.literal)
	if err != nil {
		return err
	}
	literal.literal = readField(*field, coerced)
	return nil
}

func isPredicateComparison(operator string) bool {
	switch operator {
	case "=", "!=", "<>", "<", "<=", ">", ">=":
		return true
	default:
		return false
	}
}

func predicateMatches(row map[string]any, predicate *boundPredicate) (bool, error) {
	if predicate == nil {
		return true, nil
	}
	value, err := evaluateBoundPredicate(row, predicate)
	if err != nil {
		return false, err
	}
	truth, err := checkTruthOf(value)
	return truth == checkTrue, err
}

func evaluateBoundPredicate(row map[string]any, predicate *boundPredicate) (value any, err error) {
	if predicate == nil {
		return nil, nil
	}
	if predicate.maximumTextBytes > 0 {
		defer func() {
			if text, ok := value.(string); ok && len(text) > predicate.maximumTextBytes {
				value, err = nil, fmt.Errorf("kitdb SQL: function text exceeds %d bytes", predicate.maximumTextBytes)
			}
		}()
	}
	switch predicate.kind {
	case "field":
		value := readField(*predicate.field, row[predicate.field.Name])
		if typeInfo, found := kitdbsql.LookupKind(predicate.field.Kind); found && value != nil {
			if exactTemporalFieldKind(predicate.field.Kind) {
				text, ok := value.(string)
				if !ok {
					return nil, fmt.Errorf("kitdb SQL: temporal field %q has invalid runtime value", predicate.field.Name)
				}
				parsed, err := parseTemporal(predicate.field.Kind, text, predicate.field.TimePrecision)
				if err != nil {
					return nil, fmt.Errorf("kitdb SQL: temporal field %q: %w", predicate.field.Name, err)
				}
				return parsed, nil
			}
			if typeInfo.Family != kitdbsql.FamilyDecimal {
				return value, nil
			}
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("kitdb SQL: decimal field %q has invalid runtime value", predicate.field.Name)
			}
			return exactDecimal(text), nil
		}
		return value, nil
	case "literal":
		return predicate.literal, nil
	case "cast":
		if len(predicate.arguments) != 1 {
			return nil, fmt.Errorf("kitdb SQL: CAST needs one value")
		}
		value, err := evaluateBoundPredicate(row, predicate.arguments[0])
		if err != nil || value == nil {
			return value, err
		}
		return castScalarValue(value, predicate)
	case "unary":
		if len(predicate.arguments) != 1 {
			return nil, fmt.Errorf("kitdb SQL: unary predicate needs one argument")
		}
		child, err := evaluateBoundPredicate(row, predicate.arguments[0])
		if err != nil {
			return nil, err
		}
		switch predicate.operator {
		case "is null":
			return child == nil, nil
		case "is not null":
			return child != nil, nil
		case "not":
			truth, err := checkTruthOf(child)
			if err != nil || truth == checkUnknown {
				return nil, err
			}
			return truth == checkFalse, nil
		case "+", "-":
			if temporal, ok := child.(exactTemporal); ok {
				if temporal.kind != "interval" {
					return nil, fmt.Errorf("kitdb SQL: unary %s does not accept %s", predicate.operator, strings.ToUpper(temporal.kind))
				}
				if predicate.operator == "+" {
					return temporal, nil
				}
				interval, err := negateInterval(temporal.interval)
				if err != nil {
					return nil, err
				}
				return exactTemporal{kind: "interval", interval: interval, text: formatTemporalInterval(interval)}, nil
			}
			return evaluateNumericUnary(predicate.operator, child)
		default:
			return nil, fmt.Errorf("kitdb SQL: unsupported unary predicate %q", predicate.operator)
		}
	case "binary":
		if len(predicate.arguments) != 2 {
			return nil, fmt.Errorf("kitdb SQL: binary predicate needs two arguments")
		}
		left, err := evaluateBoundPredicate(row, predicate.arguments[0])
		if err != nil {
			return nil, err
		}
		right, err := evaluateBoundPredicate(row, predicate.arguments[1])
		if err != nil {
			return nil, err
		}
		if predicate.operator == "and" || predicate.operator == "or" {
			return evaluateCheckBoolean(predicate.operator, left, right)
		}
		if predicate.operator == "+" || predicate.operator == "-" || predicate.operator == "*" ||
			predicate.operator == "/" || predicate.operator == "%" {
			if result, handled, err := evaluateTemporalBinary(predicate.operator, left, right); handled {
				return result, err
			}
			return evaluateNumericBinary(predicate.operator, left, right)
		}
		if predicate.operator == "||" {
			if left == nil || right == nil {
				return nil, nil
			}
			leftText, leftOK := expressionText(predicate.arguments[0], left)
			rightText, rightOK := expressionText(predicate.arguments[1], right)
			if !leftOK || !rightOK {
				return nil, fmt.Errorf("kitdb SQL: || operands must be text")
			}
			if predicate.maximumTextBytes > 0 && len(leftText) > predicate.maximumTextBytes-len(rightText) {
				return nil, fmt.Errorf("kitdb SQL: function text exceeds %d bytes", predicate.maximumTextBytes)
			}
			return leftText + rightText, nil
		}
		if strings.Contains(predicate.operator, "like") {
			if left == nil || right == nil {
				return nil, nil
			}
			text, textOK := left.(string)
			pattern, patternOK := right.(string)
			if !textOK || !patternOK {
				return nil, fmt.Errorf("kitdb SQL: LIKE operands must be text")
			}
			insensitive := strings.Contains(predicate.operator, "ilike")
			matched := sqlLike(text, pattern, insensitive)
			if strings.HasPrefix(predicate.operator, "not ") {
				matched = !matched
			}
			return matched, nil
		}
		if left == nil || right == nil {
			return nil, nil
		}
		comparison := comparePredicateValues(predicate.arguments[0], predicate.arguments[1], left, right)
		switch predicate.operator {
		case "=":
			return comparison == 0, nil
		case "!=", "<>":
			return comparison != 0, nil
		case "<":
			return comparison < 0, nil
		case "<=":
			return comparison <= 0, nil
		case ">":
			return comparison > 0, nil
		case ">=":
			return comparison >= 0, nil
		default:
			return nil, fmt.Errorf("kitdb SQL: unsupported comparison %q", predicate.operator)
		}
	case "function":
		switch predicate.operator {
		case "in", "not in":
			if len(predicate.arguments) < 2 {
				return nil, fmt.Errorf("kitdb SQL: IN needs values")
			}
			left, err := evaluateBoundPredicate(row, predicate.arguments[0])
			if err != nil || left == nil {
				return nil, err
			}
			unknown := false
			for _, argument := range predicate.arguments[1:] {
				value, err := evaluateBoundPredicate(row, argument)
				if err != nil {
					return nil, err
				}
				if value == nil {
					unknown = true
					continue
				}
				if comparePredicateValues(predicate.arguments[0], argument, left, value) == 0 {
					return predicate.operator == "in", nil
				}
			}
			if unknown {
				return nil, nil
			}
			return predicate.operator == "not in", nil
		case "between", "not between":
			if len(predicate.arguments) != 3 {
				return nil, fmt.Errorf("kitdb SQL: BETWEEN needs two bounds")
			}
			item, err := evaluateBoundPredicate(row, predicate.arguments[0])
			if err != nil {
				return nil, err
			}
			lower, err := evaluateBoundPredicate(row, predicate.arguments[1])
			if err != nil {
				return nil, err
			}
			upper, err := evaluateBoundPredicate(row, predicate.arguments[2])
			if err != nil {
				return nil, err
			}
			if item == nil || lower == nil || upper == nil {
				return nil, nil
			}
			matched := comparePredicateValues(predicate.arguments[0], predicate.arguments[1], item, lower) >= 0 &&
				comparePredicateValues(predicate.arguments[0], predicate.arguments[2], item, upper) <= 0
			if predicate.operator == "not between" {
				matched = !matched
			}
			return matched, nil
		default:
			return evaluateScalarFunction(row, predicate)
		}
	default:
		return nil, fmt.Errorf("kitdb SQL: unsupported predicate node %q", predicate.kind)
	}
}

func evaluateScalarFunction(row map[string]any, expression *boundPredicate) (any, error) {
	if expression.operator == "coalesce" && expression.function == nil {
		if len(expression.arguments) == 0 {
			return nil, fmt.Errorf("kitdb SQL: COALESCE expects at least one argument")
		}
		for _, argument := range expression.arguments {
			value, err := evaluateBoundPredicate(row, argument)
			if err != nil || value != nil {
				return value, err
			}
		}
		return nil, nil
	}
	arguments := make([]any, len(expression.arguments))
	for index, argument := range expression.arguments {
		item, err := evaluateBoundPredicate(row, argument)
		if err != nil {
			return nil, err
		}
		arguments[index] = item
	}
	if expression.function != nil {
		return evaluateUserFunction(expression.function, arguments)
	}
	requireArguments := func(count int) error {
		if len(arguments) != count {
			return fmt.Errorf("kitdb SQL: %s expects %d arguments", strings.ToUpper(expression.operator), count)
		}
		return nil
	}
	switch expression.operator {
	case "nullif":
		if err := requireArguments(2); err != nil {
			return nil, err
		}
		if arguments[0] == nil || arguments[1] == nil {
			return arguments[0], nil
		}
		if comparePredicateValues(expression.arguments[0], expression.arguments[1], arguments[0], arguments[1]) == 0 {
			return nil, nil
		}
		return arguments[0], nil
	case "lower", "upper", "trim", "ltrim", "rtrim":
		if err := requireArguments(1); err != nil {
			return nil, err
		}
		if arguments[0] == nil {
			return nil, nil
		}
		text, ok := expressionText(expression.arguments[0], arguments[0])
		if !ok {
			return nil, fmt.Errorf("kitdb SQL: %s expects text", strings.ToUpper(expression.operator))
		}
		switch expression.operator {
		case "lower":
			return strings.ToLower(text), nil
		case "upper":
			return strings.ToUpper(text), nil
		case "ltrim":
			return strings.TrimLeft(text, " \t\r\n"), nil
		case "rtrim":
			return strings.TrimRight(text, " \t\r\n"), nil
		default:
			return strings.TrimSpace(text), nil
		}
	case "length":
		if err := requireArguments(1); err != nil {
			return nil, err
		}
		if arguments[0] == nil {
			return nil, nil
		}
		switch item := arguments[0].(type) {
		case string:
			if exactCharacterPredicate(expression.arguments[0]) {
				item = strings.TrimRight(item, " ")
			}
			return int64(utf8.RuneCountInString(item)), nil
		case []byte:
			return int64(len(item)), nil
		default:
			return nil, fmt.Errorf("kitdb SQL: LENGTH expects text or bytes")
		}
	case "abs":
		if err := requireArguments(1); err != nil {
			return nil, err
		}
		item, err := evaluateNumericUnary("+", arguments[0])
		if err != nil || item == nil {
			return item, err
		}
		if integer, ok := item.(int64); ok {
			if integer == math.MinInt64 {
				return nil, fmt.Errorf("kitdb SQL: integer overflow")
			}
			if integer < 0 {
				integer = -integer
			}
			return integer, nil
		}
		if decimal, ok := item.(exactDecimal); ok {
			if strings.HasPrefix(string(decimal), "-") {
				return exactDecimal(strings.TrimPrefix(string(decimal), "-")), nil
			}
			return decimal, nil
		}
		number := item.(float64)
		return math.Abs(number), nil
	case "round":
		if len(arguments) < 1 || len(arguments) > 2 {
			return nil, fmt.Errorf("kitdb SQL: ROUND expects one or two arguments")
		}
		if arguments[0] == nil || len(arguments) == 2 && arguments[1] == nil {
			return nil, nil
		}
		scale := int64(0)
		if len(arguments) == 2 {
			var err error
			scale, err = integerValue(arguments[1])
			if err != nil || scale < -kitdbsql.MaximumDecimalPrecision || scale > kitdbsql.MaximumDecimalPrecision {
				return nil, fmt.Errorf(
					"kitdb SQL: ROUND scale must be between -%d and %d",
					kitdbsql.MaximumDecimalPrecision, kitdbsql.MaximumDecimalPrecision,
				)
			}
		}
		if decimal, ok := arguments[0].(exactDecimal); ok {
			number, err := parseDecimalNumber(string(decimal))
			if err != nil {
				return nil, fmt.Errorf("kitdb SQL: ROUND: %w", err)
			}
			number, err = roundDecimalNumber(number, int(scale))
			if err != nil {
				return nil, fmt.Errorf("kitdb SQL: ROUND: %w", err)
			}
			return decimalRuntimeResult(number)
		}
		number, numeric := runtimeNumericValue(arguments[0])
		if !numeric {
			return nil, fmt.Errorf("kitdb SQL: ROUND expects a numeric value")
		}
		factor := math.Pow10(int(scale))
		result := math.Round(number*factor) / factor
		if math.IsNaN(result) || math.IsInf(result, 0) {
			return nil, fmt.Errorf("kitdb SQL: numeric result is not finite")
		}
		return result, nil
	case "date_trunc":
		if err := requireArguments(2); err != nil {
			return nil, err
		}
		if arguments[0] == nil || arguments[1] == nil {
			return nil, nil
		}
		unit, ok := arguments[0].(string)
		if !ok {
			return nil, fmt.Errorf("kitdb SQL: DATE_TRUNC unit must be text")
		}
		value, ok := arguments[1].(exactTemporal)
		if !ok {
			return nil, fmt.Errorf("kitdb SQL: DATE_TRUNC value must be temporal")
		}
		return truncateTemporal(unit, value)
	case "date_part":
		if err := requireArguments(2); err != nil {
			return nil, err
		}
		if arguments[0] == nil || arguments[1] == nil {
			return nil, nil
		}
		unit, ok := arguments[0].(string)
		if !ok {
			return nil, fmt.Errorf("kitdb SQL: DATE_PART unit must be text")
		}
		value, ok := arguments[1].(exactTemporal)
		if !ok {
			return nil, fmt.Errorf("kitdb SQL: DATE_PART value must be temporal")
		}
		return temporalPart(unit, value)
	default:
		return nil, fmt.Errorf("kitdb SQL: unsupported function %s", strings.ToUpper(expression.operator))
	}
}

func evaluateNumericUnary(operator string, item any) (any, error) {
	if item == nil {
		return nil, nil
	}
	if integer, integerRuntime := runtimeIntegerValue(item); integerRuntime {
		if operator == "+" {
			return integer, nil
		}
		if integer == math.MinInt64 {
			return nil, fmt.Errorf("kitdb SQL: integer overflow")
		}
		return -integer, nil
	}
	if decimal, ok := item.(exactDecimal); ok {
		if operator == "+" || decimal == "0" {
			return decimal, nil
		}
		if strings.HasPrefix(string(decimal), "-") {
			return exactDecimal(strings.TrimPrefix(string(decimal), "-")), nil
		}
		return exactDecimal("-" + string(decimal)), nil
	}
	number, numeric := runtimeNumericValue(item)
	if !numeric {
		return nil, fmt.Errorf("kitdb SQL: unary %s requires a numeric value", operator)
	}
	if operator == "-" {
		number = -number
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return nil, fmt.Errorf("kitdb SQL: numeric result is not finite")
	}
	return number, nil
}

func evaluateNumericBinary(operator string, left, right any) (any, error) {
	if left == nil || right == nil {
		return nil, nil
	}
	if result, handled, err := exactDecimalBinary(operator, left, right); handled {
		if err != nil {
			return nil, fmt.Errorf("kitdb SQL: %w", err)
		}
		return result, nil
	}
	leftInteger, leftIsInteger := runtimeIntegerValue(left)
	rightInteger, rightIsInteger := runtimeIntegerValue(right)
	if leftIsInteger && rightIsInteger && operator != "/" {
		switch operator {
		case "+":
			if rightInteger > 0 && leftInteger > math.MaxInt64-rightInteger ||
				rightInteger < 0 && leftInteger < math.MinInt64-rightInteger {
				return nil, fmt.Errorf("kitdb SQL: integer overflow")
			}
			return leftInteger + rightInteger, nil
		case "-":
			if rightInteger < 0 && leftInteger > math.MaxInt64+rightInteger ||
				rightInteger > 0 && leftInteger < math.MinInt64+rightInteger {
				return nil, fmt.Errorf("kitdb SQL: integer overflow")
			}
			return leftInteger - rightInteger, nil
		case "*":
			if leftInteger != 0 && rightInteger != 0 {
				if leftInteger == -1 && rightInteger == math.MinInt64 ||
					rightInteger == -1 && leftInteger == math.MinInt64 {
					return nil, fmt.Errorf("kitdb SQL: integer overflow")
				}
				product := leftInteger * rightInteger
				if product/rightInteger != leftInteger {
					return nil, fmt.Errorf("kitdb SQL: integer overflow")
				}
				return product, nil
			}
			return int64(0), nil
		case "%":
			if rightInteger == 0 {
				return nil, fmt.Errorf("kitdb SQL: division by zero")
			}
			return leftInteger % rightInteger, nil
		}
	}
	leftNumber, leftNumeric := runtimeNumericValue(left)
	rightNumber, rightNumeric := runtimeNumericValue(right)
	if !leftNumeric || !rightNumeric {
		return nil, fmt.Errorf("kitdb SQL: %s requires numeric operands", operator)
	}
	if (operator == "/" || operator == "%") && rightNumber == 0 {
		return nil, fmt.Errorf("kitdb SQL: division by zero")
	}
	var result float64
	switch operator {
	case "+":
		result = leftNumber + rightNumber
	case "-":
		result = leftNumber - rightNumber
	case "*":
		result = leftNumber * rightNumber
	case "/":
		result = leftNumber / rightNumber
	case "%":
		result = math.Mod(leftNumber, rightNumber)
	default:
		return nil, fmt.Errorf("kitdb SQL: unsupported numeric operator %q", operator)
	}
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return nil, fmt.Errorf("kitdb SQL: numeric result is not finite")
	}
	return result, nil
}

func runtimeIntegerValue(item any) (int64, bool) {
	switch item.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		integer, err := integerValue(item)
		return integer, err == nil
	default:
		return 0, false
	}
}

func runtimeNumericValue(item any) (float64, bool) {
	if integer, ok := runtimeIntegerValue(item); ok {
		return float64(integer), true
	}
	switch number := item.(type) {
	case exactDecimal:
		parsed, err := strconv.ParseFloat(string(number), 64)
		return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
	case float32:
		return float64(number), true
	case float64:
		return number, !math.IsNaN(number) && !math.IsInf(number, 0)
	default:
		return 0, false
	}
}

func comparePredicateValues(leftPlan, rightPlan *boundPredicate, left, right any) int {
	for _, plan := range []*boundPredicate{leftPlan, rightPlan} {
		if plan == nil {
			continue
		}
		if exactCharacterPredicate(plan) {
			if comparison, comparable := compareCharacterField(kitdbsql.Field{}, left, right); comparable {
				return comparison
			}
		}
		if plan.field == nil {
			continue
		}
		typeInfo, found := kitdbsql.LookupKind(plan.field.Kind)
		if found && (typeInfo.Family == kitdbsql.FamilyDecimal || exactTemporalFieldKind(plan.field.Kind)) {
			return compareFieldValues(*plan.field, left, right)
		}
	}
	return compareValues(left, right)
}

func sqlLike(text, pattern string, insensitive bool) bool {
	if insensitive {
		text, pattern = strings.ToLower(text), strings.ToLower(pattern)
	}
	input := []rune(text)
	tokens := sqlLikeTokens(pattern)
	inputIndex, tokenIndex := 0, 0
	starToken, starInput := -1, -1
	for inputIndex < len(input) {
		if tokenIndex < len(tokens) && (tokens[tokenIndex].single ||
			(!tokens[tokenIndex].many && tokens[tokenIndex].value == input[inputIndex])) {
			inputIndex++
			tokenIndex++
			continue
		}
		if tokenIndex < len(tokens) && tokens[tokenIndex].many {
			starToken, starInput = tokenIndex, inputIndex
			tokenIndex++
			continue
		}
		if starToken >= 0 {
			starInput++
			inputIndex, tokenIndex = starInput, starToken+1
			continue
		}
		return false
	}
	for tokenIndex < len(tokens) && tokens[tokenIndex].many {
		tokenIndex++
	}
	return tokenIndex == len(tokens)
}

type likeToken struct {
	value  rune
	many   bool
	single bool
}

func sqlLikeTokens(pattern string) []likeToken {
	runes := []rune(pattern)
	tokens := make([]likeToken, 0, len(runes))
	escaped := false
	for _, current := range runes {
		if escaped {
			tokens = append(tokens, likeToken{value: current})
			escaped = false
			continue
		}
		if current == '\\' {
			escaped = true
			continue
		}
		switch current {
		case '%':
			if len(tokens) == 0 || !tokens[len(tokens)-1].many {
				tokens = append(tokens, likeToken{many: true})
			}
		case '_':
			tokens = append(tokens, likeToken{single: true})
		default:
			tokens = append(tokens, likeToken{value: current})
		}
	}
	if escaped {
		tokens = append(tokens, likeToken{value: '\\'})
	}
	return tokens
}
