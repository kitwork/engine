package relational

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

// Rows must already be authority-filtered before emit. No caller expressions
// run against hidden rows; retained results and candidate scans stay bounded.
func queryCatalogView(ctx context.Context, source string, args []any, schema kitdbsql.Schema, maximum int, visit func(func(map[string]any) error) error) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := accountCatalogReadOnly(source); err != nil {
		return Result{}, err
	}
	statement, err := kitdbsql.ParseStatement(source)
	if err != nil {
		return Result{}, err
	}
	plan := statement.Select
	if plan == nil || !strings.EqualFold(plan.Table, schema.Name) || plan.Source != nil || len(plan.CommonTables) > 0 || len(plan.Joins) > 0 || len(plan.SetOperations) > 0 || len(plan.GroupBy) > 0 || plan.Having != nil || plan.Search != nil || plan.Distinct || plan.HasAfter {
		return Result{}, pgwire.NewError("0A000", "catalog views require a single-view SELECT")
	}
	count := len(plan.Projection) == 1 && plan.Projection[0].Count
	var columns []Column
	var names []string
	if count {
		if plan.Projection[0].Name != "" && plan.Projection[0].Name != "*" {
			return Result{}, pgwire.NewError("0A000", "catalog views only support COUNT(*)")
		}
		label := plan.Projection[0].Alias
		if label == "" {
			label = "count"
		}
		columns = []Column{{Name: label, Kind: "bigint"}}
	} else {
		for _, projection := range plan.Projection {
			if projection.Aggregate != "" || projection.Expression != nil {
				return Result{}, pgwire.NewError("0A000", "catalog projections require fields or COUNT(*)")
			}
		}
		columns, names, err = bindProjection(schema, plan.Projection)
		if err != nil {
			return Result{}, err
		}
	}
	predicate, err := bindPredicate(schema, plan.Predicate, args)
	if err != nil {
		return Result{}, err
	}
	for _, order := range plan.Order {
		if order.Expression != nil {
			return Result{}, pgwire.NewError("0A000", "catalog ORDER BY requires a field")
		}
		if _, _, ok := schema.FieldByName(unqualifiedColumn(order.Column)); !ok {
			return Result{}, fmt.Errorf("catalog has no order field %q", order.Column)
		}
	}
	var rows []map[string]any
	visited := 0
	err = visit(func(row map[string]any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		visited++
		if visited > 65536 {
			return pgwire.NewError("54000", "catalog candidate limit exceeded")
		}
		matches, err := predicateMatches(row, predicate)
		if err != nil || !matches {
			return err
		}
		if len(rows) >= maximum {
			return pgwire.NewError("54000", "catalog result limit exceeded; narrow WHERE")
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	if count {
		if plan.Offset > 0 || plan.HasLimit && plan.Limit == 0 {
			return Result{Columns: columns, CommandTag: "SELECT 0"}, nil
		}
		return Result{Columns: columns, Rows: [][]any{{int64(len(rows))}}, CommandTag: "SELECT 1"}, nil
	}
	sort.SliceStable(rows, func(i, j int) bool {
		for _, order := range plan.Order {
			field, _, _ := schema.FieldByName(unqualifiedColumn(order.Column))
			compare := compareValues(rows[i][field], rows[j][field])
			if compare != 0 {
				if order.Descending {
					return compare > 0
				}
				return compare < 0
			}
		}
		return false
	})
	start := min(plan.Offset, len(rows))
	end := len(rows)
	if plan.HasLimit {
		end = start + min(plan.Limit, len(rows)-start)
	}
	result := Result{Columns: columns, CommandTag: fmt.Sprintf("SELECT %d", end-start)}
	for _, row := range rows[start:end] {
		result.Rows = append(result.Rows, projectRow(schema, row, names))
	}
	return result, nil
}
