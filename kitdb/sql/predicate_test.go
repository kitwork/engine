package sql

import (
	"strings"
	"testing"
)

func TestParsePredicatePrecedenceAndPlannerFallback(t *testing.T) {
	parsed, err := ParseStatement(`
		SELECT id FROM products
		WHERE (status = 'active' OR title ILIKE '%keyboard%')
		  AND id IN (1, 2, $1)
	`)
	if err != nil {
		t.Fatal(err)
	}
	plan := parsed.Select
	if plan == nil || plan.Predicate == nil {
		t.Fatalf("SELECT predicate = %#v", plan)
	}
	if plan.Predicate.Kind != "binary" || plan.Predicate.Operator != "and" ||
		len(plan.Predicate.Arguments) != 2 {
		t.Fatalf("predicate root = %#v", plan.Predicate)
	}
	if got := plan.Predicate.Arguments[0]; got.Operator != "or" {
		t.Fatalf("parenthesized branch = %#v", got)
	}
	if got := plan.Predicate.Arguments[1]; got.Operator != "in" || len(got.Arguments) != 4 {
		t.Fatalf("IN branch = %#v", got)
	}
	if len(plan.Conditions) != 0 {
		t.Fatalf("unsafe planner extraction = %#v", plan.Conditions)
	}
}

func TestParsePredicateKeepsSafeAndConditions(t *testing.T) {
	parsed, err := ParseStatement(`
		SELECT * FROM products
		WHERE merchant = $1 AND id >= 10 AND deleted_at IS NULL
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Select.Conditions) != 3 {
		t.Fatalf("planner conditions = %#v", parsed.Select.Conditions)
	}
}

func TestParsePredicateKeepsSafeTopLevelConjuncts(t *testing.T) {
	parsed, err := ParseStatement(`
		SELECT * FROM products
		WHERE merchant = $1 AND (id IN (1, 2) OR title ILIKE '%keyboard%')
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Select.Conditions) != 1 || parsed.Select.Conditions[0].Column != "merchant" ||
		parsed.Select.Conditions[0].Operator != "=" {
		t.Fatalf("safe conjunct planner conditions = %#v", parsed.Select.Conditions)
	}
}

func TestExpressionBoundsFailClosed(t *testing.T) {
	deep := `UPDATE products SET value = ` + strings.Repeat("(", maximumExpressionDepth+1) +
		`value` + strings.Repeat(")", maximumExpressionDepth+1) + ` WHERE id = 1`
	if _, err := ParseStatement(deep); err == nil || !strings.Contains(err.Error(), "expression exceeds depth") {
		t.Fatalf("deep expression error = %v", err)
	}

	wide := `SELECT * FROM products WHERE id IN (` + strings.Repeat(`1,`, maximumExpressionNodes) + `1)`
	if _, err := ParseStatement(wide); err == nil || !strings.Contains(err.Error(), "expression exceeds 256 nodes") {
		t.Fatalf("wide expression error = %v", err)
	}
}
