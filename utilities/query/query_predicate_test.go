package query

import (
	"testing"

	"github.com/kitwork/engine/value"
)

func TestExecutionPlanPreservesAndClonesGroupedPredicate(t *testing.T) {
	left := Predicate{
		Kind:      PredicateCondition,
		Condition: Condition{Column: "status", Operator: "=", Value: value.New("active")},
	}
	right := Predicate{
		Kind:      PredicateCondition,
		Condition: Condition{Column: "price", Operator: ">=", Value: value.New(10)},
	}
	predicate := Predicate{Kind: PredicateAnd, Children: []Predicate{left, right}}
	builder := NewQuery(nil, nil).WherePredicate(predicate)

	first := builder.ExecutionPlan()
	if first.Predicate == nil || first.Predicate.Kind != PredicateAnd || len(first.Conditions) != 2 {
		t.Fatalf("unexpected execution plan: %#v", first)
	}
	predicate.Children[0].Condition.Column = "caller-mutated"
	first.Predicate.Children[1].Condition.Column = "plan-mutated"

	second := builder.ExecutionPlan()
	if second.Predicate.Children[0].Condition.Column != "status" ||
		second.Predicate.Children[1].Condition.Column != "price" {
		t.Fatalf("predicate alias leaked into builder: %#v", second.Predicate)
	}
}

func TestConjunctiveConditionsRefusesOrAndNot(t *testing.T) {
	leaf := func(column string) Predicate {
		return Predicate{
			Kind:      PredicateCondition,
			Condition: Condition{Column: column, Operator: "=", Value: value.New(1)},
		}
	}
	for _, predicate := range []Predicate{
		{Kind: PredicateOr, Children: []Predicate{leaf("a"), leaf("b")}},
		{Kind: PredicateNot, Children: []Predicate{leaf("a")}},
	} {
		if conditions, ok := ConjunctiveConditions(&predicate); ok || conditions != nil {
			t.Fatalf("unsafe predicate became planner conditions: %#v", conditions)
		}
	}
}
