package relational

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type aggregateBinding struct {
	function string
	field    *kitdbsql.Field
}

type aggregateState struct {
	count         int64
	sum           float64
	value         any
	has           bool
	exact         *integerSum
	decimal       *decimalSum
	retainedBytes int
}

type aggregateGroup struct {
	values map[string]any
	states []aggregateState
}

func bindAggregateShape(
	schema kitdbsql.Schema,
	plan *kitdbsql.SelectStatement,
) ([]kitdbsql.Field, []aggregateBinding, error) {
	groupFields := make([]kitdbsql.Field, len(plan.GroupBy))
	for index, requested := range plan.GroupBy {
		_, field, found := schema.FieldByName(unqualifiedColumn(requested))
		if !found {
			return nil, nil, fmt.Errorf("kitdb SQL: GROUP BY has no field %q", requested)
		}
		groupFields[index] = field
	}
	bindings := make([]aggregateBinding, len(plan.Projection))
	for index, projection := range plan.Projection {
		function := strings.ToLower(projection.Aggregate)
		if function == "" && projection.Count {
			function = "count"
		}
		bindings[index].function = function
		if projection.Name == "" {
			continue
		}
		_, field, found := schema.FieldByName(unqualifiedColumn(projection.Name))
		if !found {
			return nil, nil, fmt.Errorf("kitdb SQL: aggregate has no field %q", projection.Name)
		}
		bindings[index].field = &field
	}
	return groupFields, bindings, nil
}

func aggregateGroupWorkingBytes(
	key string,
	row map[string]any,
	fields []kitdbsql.Field,
	bindings []aggregateBinding,
	sourceRetained bool,
) int {
	bytes := 128 + len(key) + len(bindings)*64 + 64 + len(fields)*48
	nodes := 0
	for _, field := range fields {
		bytes += len(field.Name)
		if !sourceRetained {
			bytes += materializedValueBytesBounded(row[field.Name], 0, &nodes)
		}
	}
	for _, binding := range bindings {
		switch {
		case exactDecimalAggregate(binding.field, binding.function):
			bytes += maximumDecimalText + 128
		case exactIntegerAggregate(binding.field, binding.function):
			bytes += 256
		}
	}
	return bytes
}

func aggregateGroupValues(row map[string]any, fields []kitdbsql.Field) map[string]any {
	values := make(map[string]any, len(fields))
	for _, field := range fields {
		values[field.Name] = row[field.Name]
	}
	return values
}

func updateAggregateStateAccounted(
	working *materializationWorkingSet,
	state *aggregateState,
	function string,
	value any,
	field *kitdbsql.Field,
	star bool,
) error {
	if working == nil || value == nil || function != "min" && function != "max" {
		return updateAggregateState(state, function, value, field, star)
	}
	replace := !state.has
	if state.has && function == "min" {
		replace = compareAggregateValues(field, value, state.value) < 0
	}
	if state.has && function == "max" {
		replace = compareAggregateValues(field, value, state.value) > 0
	}
	if !replace {
		return nil
	}
	nodes := 0
	next := 32 + materializedValueBytesBounded(value, 0, &nodes)
	previous := state.retainedBytes
	if err := working.replace(previous, next, "GROUP BY aggregate state"); err != nil {
		return err
	}
	if err := updateAggregateState(state, function, value, field, star); err != nil {
		_ = working.replace(next, previous, "GROUP BY aggregate state")
		return err
	}
	state.retainedBytes = next
	return nil
}

func selectHasAggregates(plan *kitdbsql.SelectStatement) bool {
	if plan == nil {
		return false
	}
	if len(plan.GroupBy) != 0 || plan.Having != nil {
		return true
	}
	for _, projection := range plan.Projection {
		if projection.Aggregate != "" || projection.Count {
			return true
		}
	}
	return false
}

func selectUsesCatalogCount(plan *kitdbsql.SelectStatement) bool {
	if plan == nil || len(plan.GroupBy) != 0 || len(plan.Conditions) != 0 ||
		plan.Predicate != nil || plan.Having != nil || len(plan.Projection) != 1 {
		return false
	}
	projection := plan.Projection[0]
	return projection.Name == "" && (projection.Count || strings.EqualFold(projection.Aggregate, "count"))
}

func describeAggregateSelect(schema kitdbsql.Schema, plan *kitdbsql.SelectStatement) ([]Column, error) {
	if plan == nil || !selectHasAggregates(plan) {
		return nil, fmt.Errorf("kitdb SQL: aggregate plan is unavailable")
	}
	if plan.Distinct {
		return nil, fmt.Errorf("kitdb SQL: SELECT DISTINCT with aggregates is not enabled yet")
	}
	grouped := make(map[string]struct{}, len(plan.GroupBy))
	for _, requested := range plan.GroupBy {
		canonical, _, found := schema.FieldByName(unqualifiedColumn(requested))
		if !found {
			return nil, fmt.Errorf("kitdb SQL: table %q has no GROUP BY field %q", schema.Name, requested)
		}
		grouped[canonical] = struct{}{}
	}
	columns := make([]Column, len(plan.Projection))
	for index, projection := range plan.Projection {
		function := strings.ToLower(projection.Aggregate)
		if function == "" && projection.Count {
			function = "count"
		}
		if function == "" {
			canonical, field, found := schema.FieldByName(unqualifiedColumn(projection.Name))
			if !found {
				return nil, fmt.Errorf("kitdb SQL: table %q has no field %q", schema.Name, projection.Name)
			}
			if _, found := grouped[canonical]; !found {
				return nil, fmt.Errorf("kitdb SQL: field %q must appear in GROUP BY or an aggregate", canonical)
			}
			label := projection.Alias
			if label == "" {
				label = canonical
			}
			columns[index] = columnForField(label, field)
			continue
		}
		if function != "count" && function != "sum" && function != "avg" && function != "min" && function != "max" {
			return nil, fmt.Errorf("kitdb SQL: unsupported aggregate %q", function)
		}
		kind := "integer"
		if projection.Name != "" {
			_, field, found := schema.FieldByName(unqualifiedColumn(projection.Name))
			if !found {
				return nil, fmt.Errorf("kitdb SQL: table %q has no aggregate field %q", schema.Name, projection.Name)
			}
			if function == "sum" || function == "avg" {
				typeInfo, _ := kitdbsql.LookupKind(field.Kind)
				if typeInfo.Family != kitdbsql.FamilyInteger && typeInfo.Family != kitdbsql.FamilyFloat &&
					typeInfo.Family != kitdbsql.FamilyDecimal {
					return nil, fmt.Errorf("kitdb SQL: %s requires a numeric field", strings.ToUpper(function))
				}
				kind = "float"
				if typeInfo.Family == kitdbsql.FamilyDecimal {
					kind = "decimal"
				}
				if exactIntegerAggregate(&field, function) {
					kind = integerAggregateResultKind(&field, function)
				}
			} else if function == "min" || function == "max" {
				kind = field.Kind
			}
		} else if function != "count" {
			return nil, fmt.Errorf("kitdb SQL: %s requires a field", strings.ToUpper(function))
		}
		label := projection.Alias
		if label == "" {
			label = function
		}
		columns[index] = Column{Name: label, Kind: kind}
		if (function == "min" || function == "max") && projection.Name != "" {
			_, field, _ := schema.FieldByName(unqualifiedColumn(projection.Name))
			columns[index] = columnForField(label, field)
		}
	}
	if plan.Having != nil {
		outputSchema, err := aggregateOutputSchema(columns)
		if err != nil {
			return nil, err
		}
		if err := validateAggregateHavingReferences(outputSchema, plan.Having); err != nil {
			return nil, err
		}
	}
	return columns, nil
}

func (transaction *Transaction) executeAggregateSelect(
	ctx context.Context,
	plan *kitdbsql.SelectStatement,
	parameters []any,
	observe bool,
	working *materializationWorkingSet,
) (Result, error) {
	schema, err := transaction.schema(plan.Table)
	if err != nil {
		return Result{}, err
	}
	columns, err := describeAggregateSelect(schema, plan)
	if err != nil {
		return Result{}, err
	}
	conditions, err := bindConditions(schema, plan.Conditions, parameters)
	if err != nil {
		return Result{}, err
	}
	predicate, err := bindPredicate(schema, plan.Predicate, parameters)
	if err != nil {
		return Result{}, err
	}
	groupFields, bindings, err := bindAggregateShape(schema, plan)
	if err != nil {
		return Result{}, err
	}
	if selectUsesCatalogCount(plan) && bindings[0].function == "count" && bindings[0].field == nil {
		if count, found, err := readTableCount(transaction, schema); err != nil {
			return Result{}, err
		} else if found && count <= math.MaxInt64 {
			rows := [][]any{{int64(count)}}
			rows = sliceAggregateRows(rows, plan)
			return Result{
				Columns: columns, Rows: rows, CommandTag: fmt.Sprintf("SELECT %d", len(rows)),
				Execution: &ExecutionStats{
					Path: "catalog-count", RowsMatched: count, RowsFromMetadata: count,
				},
			}, nil
		}
	}
	generation, err := activeRowGeneration(transaction, schema)
	if err != nil {
		return Result{}, err
	}
	if len(groupFields) != 0 {
		access, handled, err := transaction.planIndexOnlyGroup(schema, plan, groupFields, bindings)
		if err != nil {
			return Result{}, err
		}
		if handled {
			return transaction.executeIndexOnlyGroup(
				ctx, access, plan, columns, bindings, parameters, observe, working,
			)
		}
	}
	access, err := transaction.planRowAccess(schema, generation, conditions, nil)
	if err != nil {
		return Result{}, err
	}
	if covering, handled, err := transaction.planIndexOnlyAggregate(
		schema, plan, groupFields, bindings, conditions, predicate,
	); err != nil {
		return Result{}, err
	} else if handled {
		return transaction.executeIndexOnlyAggregate(
			ctx, covering, plan, columns, bindings, groupFields, conditions, predicate,
			parameters, observe, working,
		)
	}
	if access.kind == rowAccessScan && transaction.engine.batchAggregates {
		if len(groupFields) != 0 {
			if result, handled, err := transaction.executeBatchGroup(ctx, schema, plan, columns, bindings, groupFields, predicate, generation, parameters, observe, working); handled {
				return result, err
			}
		}
		if result, handled, err := transaction.executeBatchAggregate(ctx, schema, plan, columns, bindings, predicate, generation, observe, working); handled {
			return result, err
		}
	}
	stats := rowAccessExecutionStats(access, observe)
	stream, err := newAggregateStream(groupFields, bindings, transaction.engine.maximumResultRows, working)
	if err != nil {
		return Result{}, err
	}
	visited := 0
	err = transaction.walkAccessRowsObserved(access, stats, func(_, encoded []byte) (bool, error) {
		visited++
		if visited&255 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		decoded, err := decodeRow(schema, encoded)
		if err != nil {
			return false, err
		}
		if !matchesAll(decoded.values, conditions) {
			return false, nil
		}
		matched, err := predicateMatches(decoded.values, predicate)
		if err != nil {
			return false, err
		}
		if !matched {
			return false, nil
		}
		if stats != nil {
			stats.RowsMatched++
		}
		return false, stream.add(decoded.values)
	})
	if err != nil {
		return Result{}, err
	}
	return stream.finish(ctx, columns, plan, parameters, stats)
}

// Both scalar and batch grouping share SQL-level post-aggregation semantics.
func finishAggregateRows(ctx context.Context, rows [][]any, columns []Column, plan *kitdbsql.SelectStatement, parameters []any, stats *ExecutionStats, working *materializationWorkingSet) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if plan.Having != nil {
		outputSchema, err := aggregateOutputSchema(columns)
		if err != nil {
			return Result{}, err
		}
		having, err := bindPredicate(outputSchema, plan.Having, parameters)
		if err != nil {
			return Result{}, fmt.Errorf("kitdb SQL: HAVING: %w", err)
		}
		filtered := rows[:0]
		for position, row := range rows {
			if position&255 == 0 {
				if err := ctx.Err(); err != nil {
					return Result{}, err
				}
			}
			matched, err := aggregateHavingMatches(row, columns, having, working)
			if err != nil {
				return Result{}, fmt.Errorf("kitdb SQL: HAVING: %w", err)
			}
			if matched {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	if len(plan.Order) != 0 {
		indexes, descending, err := bindAggregateOrder(plan, columns)
		if err != nil {
			return Result{}, err
		}
		sort.SliceStable(rows, func(left, right int) bool {
			for orderIndex, columnIndex := range indexes {
				comparison := compareColumnValues(columns[columnIndex], rows[left][columnIndex], rows[right][columnIndex])
				if comparison == 0 {
					continue
				}
				if descending[orderIndex] {
					return comparison > 0
				}
				return comparison < 0
			}
			return false
		})
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	rows = sliceAggregateRows(rows, plan)
	return Result{
		Columns: columns, Rows: rows, CommandTag: fmt.Sprintf("SELECT %d", len(rows)), Execution: stats,

		materializationWorkingAccounted: working != nil,
	}, nil
}

func aggregateHavingMatches(
	row []any,
	columns []Column,
	having *boundPredicate,
	working *materializationWorkingSet,
) (bool, error) {
	bytes := 0
	if working != nil {
		bytes = 64 + len(columns)*48
		nodes := 0
		for index, column := range columns {
			bytes += len(column.Name) + materializedValueBytesBounded(row[index], 0, &nodes)
		}
		if err := working.reserve(bytes, "GROUP BY HAVING row"); err != nil {
			return false, err
		}
		defer working.release(bytes)
	}
	values := make(map[string]any, len(columns))
	for index, column := range columns {
		values[column.Name] = row[index]
	}
	return predicateMatches(values, having)
}

func compareColumnValues(column Column, left, right any) int {
	return compareFieldValues(kitdbsql.Field{
		Kind: column.Kind, Precision: column.Precision, Scale: column.Scale,
		TimePrecision: column.TimePrecision, TextLength: column.TextLength, ExactUUID: column.ExactUUID,
	}, left, right)
}

func aggregateOutputSchema(columns []Column) (kitdbsql.Schema, error) {
	fields := make([]kitdbsql.Field, len(columns))
	seen := make(map[string]struct{}, len(columns))
	for index, column := range columns {
		key := strings.ToLower(column.Name)
		if _, duplicate := seen[key]; duplicate {
			return kitdbsql.Schema{}, fmt.Errorf(
				"kitdb SQL: HAVING requires unique projected aliases; %q is ambiguous", column.Name,
			)
		}
		seen[key] = struct{}{}
		fields[index] = kitdbsql.Field{
			ID: fmt.Sprintf("aggregate-field-%d", index+1), Tag: uint32(index + 1),
			Name: column.Name, Position: index + 1, Kind: column.Kind,
			Precision: column.Precision, Scale: column.Scale,
			TimePrecision: column.TimePrecision, TextLength: column.TextLength, ExactUUID: column.ExactUUID,
		}
	}
	return kitdbsql.Schema{
		ID: "aggregate-output", Name: "aggregate", Version: kitdbsql.CurrentSchemaVersion, Fields: fields,
	}, nil
}

func validateAggregateHavingReferences(schema kitdbsql.Schema, expression *kitdbsql.CheckPlan) error {
	if expression == nil {
		return nil
	}
	stack := []*kitdbsql.CheckPlan{expression}
	for len(stack) != 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		if current.Kind == "field" {
			if _, _, found := schema.FieldByName(unqualifiedColumn(current.Field)); !found {
				return fmt.Errorf("kitdb SQL: HAVING has no projected field or alias %q", current.Field)
			}
		}
		for index := range current.Arguments {
			stack = append(stack, &current.Arguments[index])
		}
	}
	return nil
}

func aggregateGroupKey(row map[string]any, fields []kitdbsql.Field) (string, error) {
	if len(fields) == 0 {
		return "", nil
	}
	values := make([]any, len(fields))
	for index, field := range fields {
		values[index] = row[field.Name]
		// Legacy rows can encode a declared BOOLEAN as 0/1, or INTEGER as an
		// integral float. Group by the logical value, not that old encoding.
		if info, found := kitdbsql.LookupKind(field.Kind); found &&
			(info.Family == kitdbsql.FamilyInteger || info.Family == kitdbsql.FamilySystem || info.Family == kitdbsql.FamilyBoolean) {
			values[index] = readField(field, values[index])
		}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("kitdb SQL: encode GROUP BY key: %w", err)
	}
	return string(encoded), nil
}

func updateAggregateState(
	state *aggregateState,
	function string,
	value any,
	field *kitdbsql.Field,
	star bool,
) error {
	switch function {
	case "count":
		if star || value != nil {
			state.count++
		}
	case "sum", "avg":
		if value == nil {
			return nil
		}
		if exactDecimalAggregate(field, function) {
			text, ok := decimalTextFromValue(value)
			if !ok {
				return fmt.Errorf("kitdb SQL: %s encountered an invalid decimal value", strings.ToUpper(function))
			}
			if state.decimal == nil {
				state.decimal = new(decimalSum)
			}
			if err := state.decimal.add(text); err != nil {
				return fmt.Errorf("kitdb SQL: %s: %w", strings.ToUpper(function), err)
			}
			state.count++
			state.has = true
			return nil
		}
		if exactIntegerAggregate(field, function) {
			integer, err := integerValue(value)
			if err != nil {
				return err
			}
			if state.exact == nil {
				state.exact = new(integerSum)
			}
			state.exact.add(integer)
			state.count++
			state.has = true
			return nil
		}
		number, err := floatValue(value)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return fmt.Errorf("kitdb SQL: %s encountered a non-finite numeric value", strings.ToUpper(function))
		}
		state.sum += number
		state.count++
		state.has = true
	case "min":
		if value != nil && (!state.has || compareAggregateValues(field, value, state.value) < 0) {
			state.value, state.has = value, true
		}
	case "max":
		if value != nil && (!state.has || compareAggregateValues(field, value, state.value) > 0) {
			state.value, state.has = value, true
		}
	default:
		return fmt.Errorf("kitdb SQL: unsupported aggregate %q", function)
	}
	return nil
}

func compareAggregateValues(field *kitdbsql.Field, left, right any) int {
	if field != nil {
		return compareFieldValues(*field, left, right)
	}
	return compareValues(left, right)
}

func aggregateStateValue(state aggregateState, function string, field *kitdbsql.Field) (any, error) {
	if state.decimal != nil {
		return state.decimal.result(state.count, function)
	}
	if state.exact != nil {
		return state.exact.aggregateResult(state.count, function, field)
	}
	switch function {
	case "count":
		return state.count, nil
	case "sum":
		if state.has {
			return state.sum, nil
		}
	case "avg":
		if state.count != 0 {
			return state.sum / float64(state.count), nil
		}
	case "min", "max":
		if state.has {
			if field != nil {
				return readField(*field, state.value), nil
			}
			return state.value, nil
		}
	}
	return nil, nil
}

func bindAggregateOrder(plan *kitdbsql.SelectStatement, columns []Column) ([]int, []bool, error) {
	indexes := make([]int, len(plan.Order))
	descending := make([]bool, len(plan.Order))
	for orderIndex, order := range plan.Order {
		found := -1
		for columnIndex, column := range columns {
			if column.Name == order.Column || strings.EqualFold(column.Name, order.Column) {
				found = columnIndex
				break
			}
		}
		if found < 0 {
			return nil, nil, fmt.Errorf("kitdb SQL: aggregate ORDER BY field %q must be projected", order.Column)
		}
		indexes[orderIndex], descending[orderIndex] = found, order.Descending
	}
	return indexes, descending, nil
}

func sliceAggregateRows(rows [][]any, plan *kitdbsql.SelectStatement) [][]any {
	if plan.Offset >= len(rows) {
		return rows[:0]
	}
	rows = rows[plan.Offset:]
	limit := len(rows)
	if plan.HasLimit && plan.Limit < limit {
		limit = plan.Limit
	}
	return rows[:limit]
}
