package work

import (
	"fmt"
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
	comparison := kitDBCompareValues(left, right)
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
		if kitDBCompareValues(left, candidate) == 0 {
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
		kitDBCompareValues(left, lower) >= 0 && kitDBCompareValues(left, upper) <= 0,
	), nil
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
