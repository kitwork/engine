package relational

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type compoundOrder struct {
	column     int
	descending bool
}

func describeCompoundSelect(
	plan *kitdbsql.SelectStatement,
	describe func(*kitdbsql.SelectStatement) ([]Column, error),
) ([]Column, error) {
	branches, err := compoundSelectBranches(plan)
	if err != nil {
		return nil, err
	}
	var columns []Column
	for index, branch := range branches {
		branchColumns, err := describe(branch)
		if err != nil {
			return nil, fmt.Errorf("kitdb SQL: UNION ALL branch %d: %w", index+1, err)
		}
		if index == 0 {
			columns = branchColumns
			continue
		}
		if err := validateCompoundColumns(columns, branchColumns, index+1); err != nil {
			return nil, err
		}
	}
	if _, err := bindCompoundOrders(columns, plan.Order); err != nil {
		return nil, err
	}
	return columns, nil
}

func (transaction *Transaction) describeSelect(plan *kitdbsql.SelectStatement) ([]Column, error) {
	return describeSelectFromCatalog(transaction.catalog, plan, nil)
}

func (transaction *Transaction) executeCompoundSelect(
	ctx context.Context,
	plan *kitdbsql.SelectStatement,
	parameters []any,
	observe bool,
) (Result, error) {
	return transaction.executeCompoundSelectInScope(
		ctx, plan, parameters, observe, nil,
		newMaterializationBudget(transaction.engine.maximumResultRows),
		nil,
	)
}

func (transaction *Transaction) executeCompoundSelectInScope(
	ctx context.Context,
	plan *kitdbsql.SelectStatement,
	parameters []any,
	observe bool,
	scope *queryScope,
	budget *materializationBudget,
	working *materializationWorkingSet,
) (Result, error) {
	branches, err := compoundSelectBranches(plan)
	if err != nil {
		return Result{}, err
	}
	columns, err := describeCompoundSelect(plan, func(branch *kitdbsql.SelectStatement) ([]Column, error) {
		return describeSelectFromCatalog(transaction.catalog, branch, scope)
	})
	if err != nil {
		return Result{}, err
	}
	orders, err := bindCompoundOrders(columns, plan.Order)
	if err != nil {
		return Result{}, err
	}

	workingLimit := transaction.engine.maximumResultRows
	if plan.HasLimit && len(orders) == 0 {
		if plan.Offset > transaction.engine.maximumResultRows-plan.Limit {
			return Result{}, fmt.Errorf(
				"kitdb SQL: UNION ALL OFFSET plus LIMIT exceeds the bounded %d-row working set",
				transaction.engine.maximumResultRows,
			)
		}
		workingLimit = plan.Offset + plan.Limit
	}
	rows := make([][]any, 0, min(workingLimit, 256))
	stats := (*ExecutionStats)(nil)
	if observe {
		stats = &ExecutionStats{Path: "union-all"}
	}
	for index, branch := range branches {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if plan.HasLimit && len(orders) == 0 && len(rows) >= workingLimit {
			break
		}
		branchPlan := *branch
		if plan.HasLimit && len(orders) == 0 {
			branchPlan.HasLimit = true
			branchPlan.Limit = workingLimit - len(rows)
		}
		result, err := transaction.executeSelectInScope(ctx, &branchPlan, parameters, observe, scope, budget, working)
		if err != nil {
			return Result{}, fmt.Errorf("kitdb SQL: UNION ALL branch %d: %w", index+1, err)
		}
		if err := validateCompoundColumns(columns, result.Columns, index+1); err != nil {
			return Result{}, err
		}
		if len(result.Rows) > transaction.engine.maximumResultRows-len(rows) {
			return Result{}, fmt.Errorf(
				"kitdb SQL: UNION ALL working set exceeds %d rows; add a narrower WHERE or LIMIT",
				transaction.engine.maximumResultRows,
			)
		}
		if err := working.reserve(len(result.Rows)*24, "UNION ALL branch row references"); err != nil {
			return Result{}, err
		}
		rows = append(rows, result.Rows...)
	}

	if len(orders) != 0 {
		sort.SliceStable(rows, func(left, right int) bool {
			for _, order := range orders {
				comparison := compareColumnValues(columns[order.column], rows[left][order.column], rows[right][order.column])
				if comparison == 0 {
					continue
				}
				if order.descending {
					return comparison > 0
				}
				return comparison < 0
			}
			return false
		})
	}
	start := min(plan.Offset, len(rows))
	end := len(rows)
	if plan.HasLimit {
		end = min(start+plan.Limit, end)
	}
	rows = rows[start:end]
	return Result{
		Columns: columns, Rows: rows, CommandTag: fmt.Sprintf("SELECT %d", len(rows)), Execution: stats,

		materializationWorkingAccounted: working != nil,
	}, nil
}

func (transaction *Transaction) executeCompoundExplain(
	ctx context.Context,
	plan *kitdbsql.SelectStatement,
	parameters []any,
) (Result, error) {
	branches, err := compoundSelectBranches(plan)
	if err != nil {
		return Result{}, err
	}
	if _, err := describeCompoundSelect(plan, transaction.describeSelect); err != nil {
		return Result{}, err
	}
	detail := fmt.Sprintf("branches=%d", len(branches))
	if len(plan.Order) != 0 {
		detail += " order=sort"
	}
	if plan.HasLimit {
		detail += fmt.Sprintf(" limit=%d", plan.Limit)
	}
	if plan.Offset != 0 {
		detail += fmt.Sprintf(" offset=%d", plan.Offset)
	}
	rows := [][]any{{int64(0), "union all", detail}}
	for _, branch := range branches {
		explained, err := transaction.executeExplain(ctx, branch, parameters)
		if err != nil {
			return Result{}, err
		}
		for _, row := range explained.Rows {
			if len(row) == 0 {
				continue
			}
			copy := append([]any(nil), row...)
			copy[0] = int64(len(rows))
			rows = append(rows, copy)
		}
	}
	return Result{Columns: explainColumns(), Rows: rows, CommandTag: "EXPLAIN"}, nil
}

func compoundSelectBranches(plan *kitdbsql.SelectStatement) ([]*kitdbsql.SelectStatement, error) {
	if plan == nil || len(plan.SetOperations) == 0 {
		return nil, fmt.Errorf("kitdb SQL: invalid UNION ALL plan")
	}
	if len(plan.SetOperations) > kitdbsql.MaximumSetOperations {
		return nil, fmt.Errorf("kitdb SQL: SELECT exceeds %d set operations", kitdbsql.MaximumSetOperations)
	}
	first := selectCore(plan)
	branches := make([]*kitdbsql.SelectStatement, 0, len(plan.SetOperations)+1)
	branches = append(branches, &first)
	for _, operation := range plan.SetOperations {
		if operation.Kind != "union all" || operation.Select == nil {
			return nil, fmt.Errorf("kitdb SQL: unsupported set operation %q", operation.Kind)
		}
		if len(operation.Select.SetOperations) != 0 {
			return nil, fmt.Errorf("kitdb SQL: nested set-operation plans are not supported")
		}
		branch := selectCore(operation.Select)
		branches = append(branches, &branch)
	}
	return branches, nil
}

func selectCore(plan *kitdbsql.SelectStatement) kitdbsql.SelectStatement {
	result := *plan
	result.CommonTables = nil
	result.SetOperations = nil
	result.Order = nil
	result.Limit = 0
	result.Offset = 0
	result.HasLimit = false
	result.After = kitdbsql.Literal{}
	result.HasAfter = false
	return result
}

func validateCompoundColumns(expected, actual []Column, branch int) error {
	if len(expected) != len(actual) {
		return fmt.Errorf(
			"kitdb SQL: UNION ALL branch %d returns %d columns; expected %d",
			branch, len(actual), len(expected),
		)
	}
	for index := range expected {
		if !compoundColumnTypesEqual(expected[index], actual[index]) {
			return fmt.Errorf(
				"kitdb SQL: UNION ALL branch %d column %d has type %s; expected %s",
				branch, index+1, compoundColumnType(actual[index]), compoundColumnType(expected[index]),
			)
		}
	}
	return nil
}

func compoundColumnTypesEqual(left, right Column) bool {
	return left.Kind == right.Kind &&
		left.Precision == right.Precision && left.Scale == right.Scale &&
		optionalIntEqual(left.TimePrecision, right.TimePrecision) &&
		optionalIntEqual(left.TextLength, right.TextLength) &&
		left.ExactUUID == right.ExactUUID
}

func optionalIntEqual(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func compoundColumnType(column Column) string {
	result := strings.ToUpper(column.Kind)
	if column.Kind == "decimal" && column.Precision != 0 {
		return fmt.Sprintf("%s(%d,%d)", result, column.Precision, column.Scale)
	}
	if column.TextLength != nil {
		return fmt.Sprintf("%s(%d)", result, *column.TextLength)
	}
	if column.TimePrecision != nil {
		return fmt.Sprintf("%s(%d)", result, *column.TimePrecision)
	}
	return result
}

func bindCompoundOrders(columns []Column, plans []kitdbsql.Order) ([]compoundOrder, error) {
	result := make([]compoundOrder, len(plans))
	for index, plan := range plans {
		columnIndex := -1
		if plan.Expression != nil {
			if plan.Expression.Kind != "literal" || plan.Expression.Literal.Kind != kitdbsql.LiteralNumber {
				return nil, fmt.Errorf("kitdb SQL: UNION ALL ORDER BY accepts output names or positions only")
			}
			position, err := strconv.Atoi(plan.Expression.Literal.Text)
			if err != nil || position < 1 || position > len(columns) {
				return nil, fmt.Errorf("kitdb SQL: UNION ALL ORDER BY position %q is out of range", plan.Expression.Literal.Text)
			}
			columnIndex = position - 1
		} else {
			name := unqualifiedColumn(plan.Column)
			for candidate := range columns {
				if !strings.EqualFold(columns[candidate].Name, name) {
					continue
				}
				if columnIndex >= 0 {
					return nil, fmt.Errorf("kitdb SQL: UNION ALL ORDER BY field %q is ambiguous", plan.Column)
				}
				columnIndex = candidate
			}
			if columnIndex < 0 {
				return nil, fmt.Errorf("kitdb SQL: UNION ALL has no output field %q", plan.Column)
			}
		}
		result[index] = compoundOrder{column: columnIndex, descending: plan.Descending}
	}
	return result, nil
}
