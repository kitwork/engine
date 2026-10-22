package work

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

type kitDBTruth uint8

const (
	kitDBUnknown kitDBTruth = iota
	kitDBFalse
	kitDBTrue
)

type kitDBPredicateResolver func(column string) (value.Value, string, error)

func (t *SchemaTable) kitDBPlanMatches(row map[string]value.Value, plan query.ExecutionPlan) (bool, error) {
	if plan.Predicate == nil {
		return t.kitDBMatches(row, plan.Conditions)
	}
	truth, err := kitDBEvaluatePredicate(plan.Predicate, func(column string) (value.Value, string, error) {
		name := kitDBColumnName(column)
		spec := t.columns[name]
		if spec == nil {
			return value.Value{}, "", fmt.Errorf("kitdb: struct %q has no field %q", t.table, name)
		}
		item, found := row[name]
		if !found {
			item = value.NewNil()
		}
		return item, spec.kind, nil
	})
	return truth == kitDBTrue, err
}

func kitDBEvaluatePredicate(predicate *query.Predicate, resolve kitDBPredicateResolver) (kitDBTruth, error) {
	if predicate == nil {
		return kitDBTrue, nil
	}
	switch predicate.Kind {
	case query.PredicateCondition:
		return kitDBEvaluatePredicateCondition(predicate.Condition, resolve)
	case query.PredicateNot:
		if len(predicate.Children) != 1 {
			return kitDBUnknown, fmt.Errorf("kitdb: NOT predicate expects one child")
		}
		truth, err := kitDBEvaluatePredicate(&predicate.Children[0], resolve)
		if err != nil {
			return kitDBUnknown, err
		}
		return kitDBNegateTruth(truth), nil
	case query.PredicateAnd:
		if len(predicate.Children) < 2 {
			return kitDBUnknown, fmt.Errorf("kitdb: AND predicate expects at least two children")
		}
		result := kitDBTrue
		for index := range predicate.Children {
			truth, err := kitDBEvaluatePredicate(&predicate.Children[index], resolve)
			if err != nil {
				return kitDBUnknown, err
			}
			if truth == kitDBFalse {
				return kitDBFalse, nil
			}
			if truth == kitDBUnknown {
				result = kitDBUnknown
			}
		}
		return result, nil
	case query.PredicateOr:
		if len(predicate.Children) < 2 {
			return kitDBUnknown, fmt.Errorf("kitdb: OR predicate expects at least two children")
		}
		result := kitDBFalse
		for index := range predicate.Children {
			truth, err := kitDBEvaluatePredicate(&predicate.Children[index], resolve)
			if err != nil {
				return kitDBUnknown, err
			}
			if truth == kitDBTrue {
				return kitDBTrue, nil
			}
			if truth == kitDBUnknown {
				result = kitDBUnknown
			}
		}
		return result, nil
	default:
		return kitDBUnknown, fmt.Errorf("kitdb: unsupported predicate node %d", predicate.Kind)
	}
}

func kitDBEvaluatePredicateCondition(condition query.Condition, resolve kitDBPredicateResolver) (kitDBTruth, error) {
	left, kind, err := resolve(condition.Column)
	if err != nil {
		return kitDBUnknown, err
	}
	operator := strings.ToLower(strings.TrimSpace(condition.Operator))
	switch operator {
	case "is null":
		return kitDBBooleanTruth(left.IsNil()), nil
	case "is not null":
		return kitDBBooleanTruth(!left.IsNil()), nil
	}

	if operator == "in" || operator == "not in" {
		truth, err := kitDBEvaluateIn(left, kind, kitDBAnyValue(condition.Value))
		if operator == "not in" {
			truth = kitDBNegateTruth(truth)
		}
		return truth, err
	}
	if operator == "between" || operator == "not between" {
		truth, err := kitDBEvaluateBetween(left, kind, kitDBAnyValue(condition.Value))
		if operator == "not between" {
			truth = kitDBNegateTruth(truth)
		}
		return truth, err
	}

	right := kitDBAnyValue(condition.Value)
	if condition.IsColumn {
		right, _, err = resolve(fmt.Sprint(condition.Value))
		if err != nil {
			return kitDBUnknown, err
		}
	} else if kind != "" {
		right = coerceWrite(kind, right)
	}
	if left.IsNil() || right.IsNil() {
		return kitDBUnknown, nil
	}
	if kitDBUnordered(left, right) {
		switch operator {
		case "", "=", "==", "===", ">", ">=", "<", "<=":
			return kitDBFalse, nil
		case "!=", "!==", "<>":
			return kitDBTrue, nil
		}
	}
	comparison := kitDBCompareKind(kind, left, right)
	switch operator {
	case "", "=", "==", "===":
		return kitDBBooleanTruth(comparison == 0), nil
	case "!=", "!==", "<>":
		return kitDBBooleanTruth(comparison != 0), nil
	case ">":
		return kitDBBooleanTruth(comparison > 0), nil
	case ">=":
		return kitDBBooleanTruth(comparison >= 0), nil
	case "<":
		return kitDBBooleanTruth(comparison < 0), nil
	case "<=":
		return kitDBBooleanTruth(comparison <= 0), nil
	case "like":
		return kitDBBooleanTruth(kitDBLike(left.Text(), right.Text())), nil
	case "not like":
		return kitDBBooleanTruth(!kitDBLike(left.Text(), right.Text())), nil
	default:
		return kitDBUnknown, fmt.Errorf("kitdb: operator %q is not supported", condition.Operator)
	}
}

func kitDBEvaluateIn(left value.Value, kind string, collection value.Value) (kitDBTruth, error) {
	if left.IsNil() {
		return kitDBUnknown, nil
	}
	if collection.K != value.Array {
		return kitDBUnknown, fmt.Errorf("kitdb: IN expects an array")
	}
	hasNull := false
	for _, candidate := range collection.Array() {
		if candidate.IsNil() {
			hasNull = true
			continue
		}
		if kind != "" {
			candidate = coerceWrite(kind, candidate)
		}
		if !kitDBUnordered(left, candidate) && kitDBCompareKind(kind, left, candidate) == 0 {
			return kitDBTrue, nil
		}
	}
	if hasNull {
		return kitDBUnknown, nil
	}
	return kitDBFalse, nil
}

func kitDBEvaluateBetween(left value.Value, kind string, bounds value.Value) (kitDBTruth, error) {
	if left.IsNil() {
		return kitDBUnknown, nil
	}
	if bounds.K != value.Array || len(bounds.Array()) != 2 {
		return kitDBUnknown, fmt.Errorf("kitdb: BETWEEN expects two bounds")
	}
	lower, upper := bounds.Array()[0], bounds.Array()[1]
	if lower.IsNil() || upper.IsNil() {
		return kitDBUnknown, nil
	}
	if kind != "" {
		lower = coerceWrite(kind, lower)
		upper = coerceWrite(kind, upper)
	}
	return kitDBBooleanTruth(
		!kitDBUnordered(left, lower) && !kitDBUnordered(left, upper) &&
			kitDBCompareKind(kind, left, lower) >= 0 && kitDBCompareKind(kind, left, upper) <= 0,
	), nil
}

// kitDBCompareKind compares two stored values of a column of that kind. A decimal is stored as its exact
// text ("35000", "100.5"), which compares as a number, never as text: "35000" > "5" is false as text,
// so amount > 5 and orderBy(desc(amount)) gave wrong rows without a word (22/10/2026). Any other kind,
// and a decimal text that does not read as a number, compares as kitDBCompareValues does.
//
// The two texts are compared digit by digit, never made into numbers: a comparison costs the length of
// the texts, whatever their exponent. Read as big.Rat, the fix's first form, "1e999999" built 10^999999,
// about 19 ms a comparison, so one such value in a filter or a row made every scan pay that for each row
// it read.
func kitDBCompareKind(kind string, left, right value.Value) int {
	if kind == "decimal" {
		if leftNumber, ok := kitDBDecimal(left); ok {
			if rightNumber, ok := kitDBDecimal(right); ok {
				return leftNumber.compare(rightNumber)
			}
		}
	}
	return kitDBCompareValues(left, right)
}

// kitDBDecimalNumber is a decimal read from its text: sign × 0.digits × 10^exponent, its digits without a
// zero in front or behind ("35000" is 0.35 × 10^5, "0.05" is 0.5 × 10^-1); zero has sign 0 and no digits.
type kitDBDecimalNumber struct {
	sign     int
	digits   string
	exponent int64
}

// kitDBDecimalExponentLimit bounds the exponent a decimal text is read with: past it the text is not read
// as a number and compares as text.
const kitDBDecimalExponentLimit = 1 << 40

// kitDBDecimal reads a stored decimal exactly: its text (validKitDBDecimal's form), or a number of the VM
// by the text a write stores it as (numText).
func kitDBDecimal(item value.Value) (kitDBDecimalNumber, bool) {
	switch item.K {
	case value.String:
		return kitDBParseDecimal(item.String())
	case value.Number:
		if math.IsNaN(item.N) || math.IsInf(item.N, 0) {
			return kitDBDecimalNumber{}, false
		}
		return kitDBParseDecimal(strconv.FormatFloat(item.N, 'g', -1, 64))
	}
	return kitDBDecimalNumber{}, false
}

// kitDBParseDecimal reads [+-]digits[.digits][e[+-]digits], with a digit on at least one side of the point.
func kitDBParseDecimal(text string) (kitDBDecimalNumber, bool) {
	position, sign := 0, 1
	if position < len(text) && (text[position] == '+' || text[position] == '-') {
		if text[position] == '-' {
			sign = -1
		}
		position++
	}
	integerStart := position
	for position < len(text) && text[position] >= '0' && text[position] <= '9' {
		position++
	}
	integer := text[integerStart:position]
	fraction := ""
	if position < len(text) && text[position] == '.' {
		position++
		fractionStart := position
		for position < len(text) && text[position] >= '0' && text[position] <= '9' {
			position++
		}
		fraction = text[fractionStart:position]
	}
	if integer == "" && fraction == "" {
		return kitDBDecimalNumber{}, false
	}
	var exponent int64
	if position < len(text) && (text[position] == 'e' || text[position] == 'E') {
		position++
		exponentSign := int64(1)
		if position < len(text) && (text[position] == '+' || text[position] == '-') {
			if text[position] == '-' {
				exponentSign = -1
			}
			position++
		}
		exponentStart := position
		for position < len(text) && text[position] >= '0' && text[position] <= '9' {
			if exponent > kitDBDecimalExponentLimit {
				return kitDBDecimalNumber{}, false
			}
			exponent = exponent*10 + int64(text[position]-'0')
			position++
		}
		if position == exponentStart {
			return kitDBDecimalNumber{}, false
		}
		exponent *= exponentSign
	}
	if position != len(text) {
		return kitDBDecimalNumber{}, false
	}
	// the digits of both sides without the zeros in front; the exponent counts the integer's digits left
	leading := len(integer) - len(strings.TrimLeft(integer, "0"))
	digits := integer[leading:] + fraction
	if len(digits) == len(fraction) {
		trimmed := strings.TrimLeft(fraction, "0")
		leading += len(fraction) - len(trimmed)
		digits = trimmed
	}
	digits = strings.TrimRight(digits, "0")
	if digits == "" {
		return kitDBDecimalNumber{}, true
	}
	return kitDBDecimalNumber{sign: sign, digits: digits, exponent: int64(len(integer)-leading) + exponent}, true
}

// compare is -1, 0 or 1 as number is less than, equal to or more than other.
func (number kitDBDecimalNumber) compare(other kitDBDecimalNumber) int {
	if number.sign != other.sign {
		if number.sign < other.sign {
			return -1
		}
		return 1
	}
	if number.sign == 0 {
		return 0
	}
	magnitude := 0
	switch {
	case number.exponent < other.exponent:
		magnitude = -1
	case number.exponent > other.exponent:
		magnitude = 1
	default:
		magnitude = strings.Compare(number.digits, other.digits)
	}
	return magnitude * number.sign
}

func kitDBNegateTruth(truth kitDBTruth) kitDBTruth {
	switch truth {
	case kitDBTrue:
		return kitDBFalse
	case kitDBFalse:
		return kitDBTrue
	default:
		return kitDBUnknown
	}
}

func kitDBBooleanTruth(matched bool) kitDBTruth {
	if matched {
		return kitDBTrue
	}
	return kitDBFalse
}
