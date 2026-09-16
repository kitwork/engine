package relational

import (
	"fmt"
	"time"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

// Typed callers bypass the SQL parser, but not shape and allocation bounds.
func validateInsertShape(plan *kitdbsql.InsertStatement) (count, width int, err error) {
	if plan == nil {
		return 0, 0, fmt.Errorf("kitdb SQL: invalid INSERT plan")
	}
	sources := 0
	if len(plan.Rows) != 0 {
		sources++
		count = len(plan.Rows)
	}
	if len(plan.Values) != 0 {
		sources++
		count = len(plan.Values)
	}
	if plan.DefaultRows != 0 {
		sources++
		count = plan.DefaultRows
	}
	if plan.Select != nil {
		sources++
	}
	if sources == 1 && plan.Select != nil {
		return 0, 0, nil
	}
	if sources != 1 || count < 1 || count > kitdbsql.MaximumInsertRows {
		return 0, 0, fmt.Errorf("kitdb SQL: INSERT requires one row source with 1 to %d rows", kitdbsql.MaximumInsertRows)
	}
	if plan.DefaultRows != 0 {
		return count, 0, nil
	}
	for i := 0; i < count; i++ {
		n := 0
		if len(plan.Values) != 0 {
			n = len(plan.Values[i])
		} else {
			n = len(plan.Rows[i])
		}
		if n == 0 || i > 0 && n != width || len(plan.Columns) != 0 && n != len(plan.Columns) {
			return 0, 0, fmt.Errorf("kitdb SQL: INSERT row %d has an invalid value count", i+1)
		}
		width = n
	}
	return count, width, nil
}

func evaluateInsertValue(expression *kitdbsql.ExpressionPlan, parameters []any, now time.Time) (any, error) {
	if expression.Kind == "literal" {
		return resolveLiteralAt(expression.Literal, parameters, now)
	}
	// VALUES has no row scope. Reuse scalar typing/binding, without exposing
	// target-table fields or introducing a second expression interpreter.
	schema := kitdbsql.Schema{}
	if _, _, err := scalarExpressionKind(schema, expression); err != nil {
		return nil, err
	}
	bound, err := bindPredicateAt(schema, expression, parameters, now)
	if err != nil {
		return nil, err
	}
	return evaluateBoundPredicate(nil, bound)
}
