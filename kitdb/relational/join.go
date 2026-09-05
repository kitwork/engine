package relational

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const (
	maximumJoinInputRows      = 10_000
	maximumJoinCandidatePairs = 20_000
	maximumJoinProjections    = 64
	maximumJoinOrders         = 8
)

type joinSource struct {
	table  string
	alias  string
	schema kitdbsql.Schema
}

type joinColumn struct {
	source int
	field  kitdbsql.Field
}

type boundJoin struct {
	kind       string
	target     int
	equalities []boundJoinEquality
}

type boundJoinEquality struct {
	targetField kitdbsql.Field
	value       joinColumn
}

type boundJoinCondition struct {
	column   joinColumn
	operator string
	value    any
}

type boundJoinPredicate struct {
	kind      string
	operator  string
	column    *joinColumn
	literal   any
	arguments []*boundJoinPredicate
}

type joinEnvironment struct {
	rows []map[string]any
}

type joinOutputRow struct {
	values []any
	order  []any
}

type joinOrder struct {
	projection int
	column     *joinColumn
	descending bool
}

func joinEnvironmentWorkingBytes(width int) int {
	return 64 + width*8
}

func describeJoinSelect(catalog kitdbengine.CatalogSnapshot, plan *kitdbsql.SelectStatement) ([]Column, error) {
	sources, err := bindJoinSources(catalog, plan)
	if err != nil {
		return nil, err
	}
	if _, err := bindJoins(sources, plan.Joins); err != nil {
		return nil, err
	}
	if selectHasAggregates(plan) {
		aggregate, err := bindJoinAggregate(sources, plan)
		if err != nil {
			return nil, err
		}
		return aggregate.columns, nil
	}
	columns, _, err := bindJoinProjection(sources, plan.Projection)
	if err != nil {
		return nil, err
	}
	for _, condition := range plan.Conditions {
		if _, err := resolveJoinColumn(sources, condition.Column); err != nil {
			return nil, err
		}
	}
	if _, err := bindJoinOrders(sources, columns, plan.Order); err != nil {
		return nil, err
	}
	return columns, nil
}

func (transaction *Transaction) executeJoinSelect(
	ctx context.Context,
	plan *kitdbsql.SelectStatement,
	parameters []any,
	observe bool,
	working *materializationWorkingSet,
) (Result, error) {
	sources, err := bindJoinSources(transaction.catalog, plan)
	if err != nil {
		return Result{}, err
	}
	joins, err := bindJoins(sources, plan.Joins)
	if err != nil {
		return Result{}, err
	}
	var columns []Column
	var projection []joinColumn
	var aggregate *joinAggregate
	var stream *aggregateStream
	if selectHasAggregates(plan) {
		aggregate, err = bindJoinAggregate(sources, plan)
		if err != nil {
			return Result{}, err
		}
		columns = aggregate.columns
		if working == nil {
			working = newMaterializationWorkingSet(newMaterializationBudget(transaction.engine.maximumResultRows))
			defer working.close()
		}
		stream, err = newAggregateStream(aggregate.fields, aggregate.bindings, transaction.engine.maximumResultRows, working)
	} else {
		columns, projection, err = bindJoinProjection(sources, plan.Projection)
	}
	if err != nil {
		return Result{}, err
	}
	conditions, err := bindJoinConditions(sources, plan.Conditions, parameters)
	if err != nil {
		return Result{}, err
	}
	predicate, err := bindJoinPredicatePlan(sources, plan.Predicate, parameters)
	if err != nil {
		return Result{}, err
	}
	var orders []joinOrder
	if aggregate == nil {
		orders, err = bindJoinOrders(sources, columns, plan.Order)
		if err != nil {
			return Result{}, err
		}
	}
	limit := transaction.engine.maximumResultRows
	if plan.HasLimit {
		if plan.Limit > transaction.engine.maximumResultRows {
			return Result{}, fmt.Errorf(
				"kitdb SQL: LIMIT %d exceeds this server's result limit of %d",
				plan.Limit, transaction.engine.maximumResultRows,
			)
		}
		limit = plan.Limit
	}
	if plan.HasLimit && plan.Limit == 0 {
		return Result{Columns: columns, CommandTag: "SELECT 0"}, nil
	}
	baseConditions := make([]boundCondition, 0, len(conditions))
	for _, condition := range conditions {
		if condition.column.source == 0 {
			baseConditions = append(baseConditions, boundCondition{
				field: condition.column.field, operator: condition.operator, value: condition.value,
			})
		}
	}
	generation, err := activeRowGeneration(transaction, sources[0].schema)
	if err != nil {
		return Result{}, err
	}
	access, err := transaction.planRowAccess(sources[0].schema, generation, baseConditions, nil)
	if err != nil {
		return Result{}, err
	}
	stats := rowAccessExecutionStats(access, observe)
	if stats != nil {
		stats.Path = "nested-loop-join"
		if aggregate != nil {
			stats.Path = "nested-loop-join-aggregate"
		}
	}
	ordered := len(orders) != 0
	outputs := make([]joinOutputRow, 0, min(limit, 256))
	seen := make(map[string]struct{})
	sourceRows := 0
	candidatePairs := 0
	matched := 0
	overflow := false
	err = transaction.walkAccessRowsObserved(access, stats, func(_, encoded []byte) (bool, error) {
		sourceRows++
		if sourceRows > maximumJoinInputRows {
			return false, fmt.Errorf(
				"kitdb SQL: JOIN source exceeds the bounded %d-row input",
				maximumJoinInputRows,
			)
		}
		if sourceRows == 1 || sourceRows&255 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		decoded, err := decodeRow(sources[0].schema, encoded)
		if err != nil {
			return false, err
		}
		if !matchesAll(decoded.values, baseConditions) {
			return false, nil
		}
		scratch := working.child()
		if scratch != nil {
			defer scratch.close()
			if _, err := scratch.reserveMap(decoded.values, "JOIN source row"); err != nil {
				return false, err
			}
			if err := scratch.reserve(joinEnvironmentWorkingBytes(1), "JOIN intermediate environments"); err != nil {
				return false, err
			}
		}
		environments := []joinEnvironment{{rows: []map[string]any{decoded.values}}}
		for _, join := range joins {
			next := make([]joinEnvironment, 0, len(environments))
			for _, environment := range environments {
				expanded, err := transaction.expandJoin(ctx, sources, join, environment, stats, &candidatePairs, scratch)
				if err != nil {
					return false, err
				}
				next = append(next, expanded...)
				if len(next) > maximumJoinCandidatePairs {
					return false, fmt.Errorf(
						"kitdb SQL: JOIN intermediate exceeds the bounded %d-row candidate limit",
						maximumJoinCandidatePairs,
					)
				}
			}
			environments = next
			if len(environments) == 0 {
				break
			}
		}
		for _, environment := range environments {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if !matchesJoinConditions(environment, conditions) {
				continue
			}
			matchedPredicate, err := joinPredicateMatches(environment, predicate)
			if err != nil {
				return false, err
			}
			if !matchedPredicate {
				continue
			}
			if stats != nil {
				stats.RowsMatched++
			}
			if aggregate != nil {
				if err := aggregate.add(stream, environment); err != nil {
					return false, err
				}
				continue
			}
			values := projectJoinEnvironment(environment, projection)
			if plan.Distinct {
				encoded, err := json.Marshal(values)
				if err != nil {
					return false, err
				}
				identity := string(encoded)
				if _, duplicate := seen[identity]; duplicate {
					continue
				}
				if err := working.reserve(len(identity)+64, "JOIN DISTINCT identities"); err != nil {
					return false, err
				}
				seen[identity] = struct{}{}
			}
			matched++
			if !ordered && matched <= plan.Offset {
				continue
			}
			if len(outputs) >= transaction.engine.maximumResultRows {
				overflow = true
				return true, nil
			}
			output := joinOutputRow{values: values}
			if ordered {
				output.order = joinOrderValues(environment, values, orders)
			}
			if working != nil {
				retainedBytes := materializedRowBytes(output.values)
				if len(output.order) != 0 {
					retainedBytes += materializedReferenceRowBytes(output.order)
				}
				if err := working.reserve(retainedBytes, "JOIN result rows"); err != nil {
					return false, err
				}
			}
			outputs = append(outputs, output)
			if !ordered && len(outputs) >= limit {
				if plan.HasLimit {
					return true, nil
				}
			}
		}
		return false, nil
	})
	if err != nil {
		return Result{}, err
	}
	if aggregate != nil {
		result, err := stream.finish(ctx, columns, aggregate.plan, parameters, stats)
		if stats != nil {
			stats.MaterializationPeakBytes = uint64(working.budget.peakBytes)
		}
		return result, err
	}
	if overflow {
		return Result{}, fmt.Errorf(
			"kitdb SQL: JOIN result exceeds %d rows; add a narrower WHERE or LIMIT",
			transaction.engine.maximumResultRows,
		)
	}
	if ordered {
		sort.SliceStable(outputs, func(left, right int) bool {
			for index, order := range orders {
				comparison := compareJoinOrderValues(order, columns, outputs[left].order[index], outputs[right].order[index])
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
		if plan.Offset >= len(outputs) {
			outputs = outputs[:0]
		} else {
			outputs = outputs[plan.Offset:]
		}
		if len(outputs) > limit {
			outputs = outputs[:limit]
		}
	}
	rows := make([][]any, len(outputs))
	for index, output := range outputs {
		rows[index] = output.values
	}
	return Result{
		Columns: columns, Rows: rows, CommandTag: fmt.Sprintf("SELECT %d", len(rows)),
		Execution: stats,

		materializationWorkingAccounted: working != nil,
	}, nil
}

func bindJoinSources(catalog kitdbengine.CatalogSnapshot, plan *kitdbsql.SelectStatement) ([]joinSource, error) {
	if plan == nil || len(plan.Joins) == 0 {
		return nil, fmt.Errorf("kitdb SQL: JOIN plan is unavailable")
	}
	if len(plan.Joins) > kitdbsql.MaximumSelectJoins {
		return nil, fmt.Errorf("kitdb SQL: SELECT exceeds %d JOIN clauses", kitdbsql.MaximumSelectJoins)
	}
	all := make([]struct{ table, alias string }, 0, len(plan.Joins)+1)
	all = append(all, struct{ table, alias string }{plan.Table, plan.TableAlias})
	for _, join := range plan.Joins {
		all = append(all, struct{ table, alias string }{join.Table, join.Alias})
	}
	sources := make([]joinSource, len(all))
	aliases := make(map[string]string, len(all))
	for index, item := range all {
		schema, err := schemaFromCatalog(catalog, item.table)
		if err != nil {
			return nil, err
		}
		alias := item.alias
		if alias == "" {
			alias = schema.Name
		}
		key := strings.ToLower(alias)
		if previous := aliases[key]; previous != "" {
			return nil, fmt.Errorf("kitdb SQL: JOIN aliases %q and %q collide", previous, alias)
		}
		aliases[key] = alias
		sources[index] = joinSource{table: schema.Name, alias: alias, schema: schema}
	}
	return sources, nil
}

func bindJoins(sources []joinSource, plans []kitdbsql.Join) ([]boundJoin, error) {
	result := make([]boundJoin, len(plans))
	for index, plan := range plans {
		if len(plan.And) >= kitdbsql.MaximumJoinEqualities {
			return nil, fmt.Errorf("kitdb SQL: JOIN ON exceeds %d equalities", kitdbsql.MaximumJoinEqualities)
		}
		binding := boundJoin{kind: plan.Kind, target: index + 1}
		equalities := append([]kitdbsql.JoinEquality{{Left: plan.Left, Right: plan.Right}}, plan.And...)
		for _, equality := range equalities {
			left, err := resolveJoinColumn(sources, equality.Left)
			if err != nil {
				return nil, err
			}
			right, err := resolveJoinColumn(sources, equality.Right)
			if err != nil {
				return nil, err
			}
			target := index + 1
			pair := boundJoinEquality{}
			switch {
			case left.source == target && right.source < target:
				pair.targetField, pair.value = left.field, right
			case right.source == target && left.source < target:
				pair.targetField, pair.value = right.field, left
			default:
				return nil, fmt.Errorf(
					"kitdb SQL: JOIN %q must connect its table to an earlier source", sources[target].alias,
				)
			}
			leftType, _ := kitdbsql.LookupKind(pair.targetField.Kind)
			rightType, _ := kitdbsql.LookupKind(pair.value.field.Kind)
			if leftType.Family != rightType.Family ||
				!exactUUIDFieldsCompatible(pair.targetField, pair.value.field) {
				return nil, fmt.Errorf("kitdb SQL: JOIN fields %q and %q have incompatible types", equality.Left, equality.Right)
			}
			binding.equalities = append(binding.equalities, pair)
		}
		result[index] = binding
	}
	return result, nil
}

func bindJoinProjection(
	sources []joinSource,
	plans []kitdbsql.Projection,
) ([]Column, []joinColumn, error) {
	columns := make([]Column, 0, len(plans))
	bindings := make([]joinColumn, 0, len(plans))
	for _, plan := range plans {
		if plan.Aggregate != "" || plan.Count {
			return nil, nil, fmt.Errorf("kitdb SQL: aggregate projection is not valid in this JOIN profile")
		}
		if plan.All {
			before := len(bindings)
			for sourceIndex, source := range sources {
				if plan.Qualifier != "" && !sourceMatchesQualifier(source, plan.Qualifier) {
					continue
				}
				for _, field := range source.schema.Fields {
					columns = append(columns, columnForField(field.Name, field))
					bindings = append(bindings, joinColumn{source: sourceIndex, field: field})
				}
			}
			if plan.Qualifier != "" && len(bindings) == before {
				return nil, nil, fmt.Errorf("kitdb SQL: JOIN has no source %q", plan.Qualifier)
			}
			continue
		}
		binding, err := resolveJoinColumn(sources, plan.Name)
		if err != nil {
			return nil, nil, err
		}
		name := plan.Alias
		if name == "" {
			name = binding.field.Name
		}
		columns = append(columns, columnForField(name, binding.field))
		bindings = append(bindings, binding)
	}
	if len(columns) > maximumJoinProjections {
		return nil, nil, fmt.Errorf(
			"kitdb SQL: JOIN projection exceeds %d columns", maximumJoinProjections,
		)
	}
	return columns, bindings, nil
}

func bindJoinConditions(
	sources []joinSource,
	plans []kitdbsql.Condition,
	parameters []any,
) ([]boundJoinCondition, error) {
	result := make([]boundJoinCondition, len(plans))
	for index, plan := range plans {
		column, err := resolveJoinColumn(sources, plan.Column)
		if err != nil {
			return nil, err
		}
		value, err := resolveLiteral(plan.Value, parameters)
		if err != nil {
			return nil, err
		}
		if plan.Operator != "is" && plan.Operator != "is not" {
			value, err = coerceField(column.field, value)
			if err != nil {
				return nil, err
			}
		}
		result[index] = boundJoinCondition{column: column, operator: plan.Operator, value: value}
	}
	return result, nil
}

func bindJoinPredicatePlan(
	sources []joinSource,
	plan *kitdbsql.CheckPlan,
	parameters []any,
) (*boundJoinPredicate, error) {
	if plan == nil {
		return nil, nil
	}
	result := &boundJoinPredicate{kind: plan.Kind, operator: strings.ToLower(plan.Operator)}
	switch plan.Kind {
	case "field":
		column, err := resolveJoinColumn(sources, plan.Field)
		if err != nil {
			return nil, err
		}
		result.column = &column
	case "literal":
		value, err := resolveLiteral(plan.Literal, parameters)
		if err != nil {
			return nil, err
		}
		result.literal = value
	case "unary", "binary", "function":
	default:
		return nil, fmt.Errorf("kitdb SQL: unsupported JOIN predicate node %q", plan.Kind)
	}
	result.arguments = make([]*boundJoinPredicate, len(plan.Arguments))
	for index := range plan.Arguments {
		argument, err := bindJoinPredicatePlan(sources, &plan.Arguments[index], parameters)
		if err != nil {
			return nil, err
		}
		result.arguments[index] = argument
	}
	if result.kind == "binary" && len(result.arguments) == 2 {
		if err := coerceJoinPredicatePair(result.operator, result.arguments[0], result.arguments[1]); err != nil {
			return nil, err
		}
	}
	if result.kind == "function" && (result.operator == "in" || result.operator == "not in") {
		if len(result.arguments) < 2 || result.arguments[0].column == nil {
			return nil, fmt.Errorf("kitdb SQL: JOIN IN requires a field and values")
		}
		for _, argument := range result.arguments[1:] {
			if argument.kind != "literal" {
				return nil, fmt.Errorf("kitdb SQL: JOIN IN accepts literal or bound values only")
			}
			value, err := coerceField(result.arguments[0].column.field, argument.literal)
			if err != nil {
				return nil, err
			}
			argument.literal = readField(result.arguments[0].column.field, value)
		}
	}
	if result.kind == "function" && (result.operator == "between" || result.operator == "not between") {
		if len(result.arguments) != 3 || result.arguments[0].column == nil {
			return nil, fmt.Errorf("kitdb SQL: JOIN BETWEEN requires a field and two bounds")
		}
		for _, argument := range result.arguments[1:] {
			if argument.kind != "literal" {
				continue
			}
			value, err := coerceField(result.arguments[0].column.field, argument.literal)
			if err != nil {
				return nil, err
			}
			argument.literal = readField(result.arguments[0].column.field, value)
		}
	}
	return result, nil
}

func coerceJoinPredicatePair(operator string, left, right *boundJoinPredicate) error {
	if !isPredicateComparison(operator) && !strings.Contains(operator, "like") {
		return nil
	}
	if left != nil && right != nil && left.column != nil && right.column != nil &&
		!exactUUIDFieldsCompatible(left.column.field, right.column.field) {
		return fmt.Errorf("kitdb SQL: JOIN UUID comparison requires matching exact UUID fields or an explicit cast")
	}
	var column *joinColumn
	var literal *boundJoinPredicate
	if left != nil && right != nil {
		if left.column != nil && right.kind == "literal" {
			column, literal = left.column, right
		} else if right.column != nil && left.kind == "literal" {
			column, literal = right.column, left
		}
	}
	if column == nil {
		return nil
	}
	if strings.Contains(operator, "like") {
		if column.field.Kind == "uuid" && column.field.ExactUUID {
			return fmt.Errorf("kitdb SQL: LIKE on UUID requires an explicit CAST AS TEXT")
		}
		if _, ok := literal.literal.(string); !ok && literal.literal != nil {
			return fmt.Errorf("kitdb SQL: LIKE pattern must be text")
		}
		return nil
	}
	value, err := coerceField(column.field, literal.literal)
	if err != nil {
		return err
	}
	literal.literal = readField(column.field, value)
	return nil
}

func joinPredicateMatches(environment joinEnvironment, predicate *boundJoinPredicate) (bool, error) {
	if predicate == nil {
		return true, nil
	}
	value, err := evaluateJoinPredicate(environment, predicate)
	if err != nil {
		return false, err
	}
	truth, err := checkTruthOf(value)
	return truth == checkTrue, err
}

func evaluateJoinPredicate(environment joinEnvironment, predicate *boundJoinPredicate) (any, error) {
	switch predicate.kind {
	case "field":
		return readField(predicate.column.field, joinEnvironmentValue(environment, *predicate.column)), nil
	case "literal":
		return predicate.literal, nil
	case "unary":
		if len(predicate.arguments) != 1 {
			return nil, fmt.Errorf("kitdb SQL: unary JOIN predicate needs one argument")
		}
		child, err := evaluateJoinPredicate(environment, predicate.arguments[0])
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
		}
	case "binary":
		if len(predicate.arguments) != 2 {
			return nil, fmt.Errorf("kitdb SQL: binary JOIN predicate needs two arguments")
		}
		left, err := evaluateJoinPredicate(environment, predicate.arguments[0])
		if err != nil {
			return nil, err
		}
		right, err := evaluateJoinPredicate(environment, predicate.arguments[1])
		if err != nil {
			return nil, err
		}
		if predicate.operator == "and" || predicate.operator == "or" {
			return evaluateCheckBoolean(predicate.operator, left, right)
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
			matched := sqlLike(text, pattern, strings.Contains(predicate.operator, "ilike"))
			if strings.HasPrefix(predicate.operator, "not ") {
				matched = !matched
			}
			return matched, nil
		}
		if left == nil || right == nil {
			return nil, nil
		}
		comparison := compareJoinPredicateValues(predicate.arguments[0], predicate.arguments[1], left, right)
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
		}
	case "function":
		switch predicate.operator {
		case "in", "not in":
			if len(predicate.arguments) < 2 {
				return nil, fmt.Errorf("kitdb SQL: JOIN IN needs values")
			}
			left, err := evaluateJoinPredicate(environment, predicate.arguments[0])
			if err != nil || left == nil {
				return nil, err
			}
			unknown := false
			for _, argument := range predicate.arguments[1:] {
				value, err := evaluateJoinPredicate(environment, argument)
				if err != nil {
					return nil, err
				}
				if value == nil {
					unknown = true
				} else if compareJoinPredicateValues(predicate.arguments[0], argument, left, value) == 0 {
					return predicate.operator == "in", nil
				}
			}
			if unknown {
				return nil, nil
			}
			return predicate.operator == "not in", nil
		case "between", "not between":
			if len(predicate.arguments) != 3 {
				return nil, fmt.Errorf("kitdb SQL: JOIN BETWEEN needs two bounds")
			}
			item, err := evaluateJoinPredicate(environment, predicate.arguments[0])
			if err != nil {
				return nil, err
			}
			lower, err := evaluateJoinPredicate(environment, predicate.arguments[1])
			if err != nil {
				return nil, err
			}
			upper, err := evaluateJoinPredicate(environment, predicate.arguments[2])
			if err != nil {
				return nil, err
			}
			if item == nil || lower == nil || upper == nil {
				return nil, nil
			}
			matched := compareJoinPredicateValues(predicate.arguments[0], predicate.arguments[1], item, lower) >= 0 &&
				compareJoinPredicateValues(predicate.arguments[0], predicate.arguments[2], item, upper) <= 0
			if predicate.operator == "not between" {
				matched = !matched
			}
			return matched, nil
		default:
			return nil, fmt.Errorf("kitdb SQL: unsupported JOIN predicate function %q", predicate.operator)
		}
	}
	return nil, fmt.Errorf("kitdb SQL: unsupported JOIN predicate %q", predicate.operator)
}

func bindJoinOrders(sources []joinSource, columns []Column, plans []kitdbsql.Order) ([]joinOrder, error) {
	if len(plans) > maximumJoinOrders {
		return nil, fmt.Errorf("kitdb SQL: JOIN ORDER BY exceeds %d fields", maximumJoinOrders)
	}
	result := make([]joinOrder, len(plans))
	for index, plan := range plans {
		projection := -1
		for columnIndex, column := range columns {
			if column.Name == plan.Column || strings.EqualFold(column.Name, plan.Column) {
				if projection != -1 {
					projection = -2
					break
				}
				projection = columnIndex
			}
		}
		result[index] = joinOrder{projection: projection, descending: plan.Descending}
		if projection >= 0 {
			continue
		}
		column, err := resolveJoinColumn(sources, plan.Column)
		if err != nil {
			return nil, err
		}
		result[index].column = &column
	}
	return result, nil
}

func resolveJoinColumn(sources []joinSource, requested string) (joinColumn, error) {
	qualifier, fieldName := "", requested
	if marker := strings.LastIndex(requested, "."); marker >= 0 {
		qualifier, fieldName = requested[:marker], requested[marker+1:]
	}
	result := joinColumn{source: -1}
	for index, source := range sources {
		if qualifier != "" && !sourceMatchesQualifier(source, qualifier) {
			continue
		}
		_, field, found := source.schema.FieldByName(fieldName)
		if !found {
			continue
		}
		if result.source >= 0 {
			return joinColumn{}, fmt.Errorf("kitdb SQL: JOIN field %q is ambiguous", requested)
		}
		result = joinColumn{source: index, field: field}
	}
	if result.source < 0 {
		return joinColumn{}, fmt.Errorf("kitdb SQL: JOIN has no field %q", requested)
	}
	return result, nil
}

func sourceMatchesQualifier(source joinSource, qualifier string) bool {
	return strings.EqualFold(source.alias, qualifier) || strings.EqualFold(source.table, qualifier)
}

func (transaction *Transaction) expandJoin(
	ctx context.Context,
	sources []joinSource,
	join boundJoin,
	environment joinEnvironment,
	stats *ExecutionStats,
	candidatePairs *int,
	working *materializationWorkingSet,
) ([]joinEnvironment, error) {
	matches := make([]map[string]any, 0, 1)
	conditions := make([]boundCondition, 0, len(join.equalities))
	for _, equality := range join.equalities {
		value := joinEnvironmentValue(environment, equality.value)
		if value == nil {
			conditions = nil
			break
		}
		coerced, err := coerceField(equality.targetField, value)
		if err != nil {
			return nil, err
		}
		// Lookup encoding must not turn a rounded/truncated value into an
		// equality match. Compare temporal values at full stored precision.
		comparisonField := equality.targetField
		comparisonField.TimePrecision = nil
		if compareFieldValues(comparisonField, value, coerced) != 0 {
			conditions = nil
			break
		}
		conditions = append(conditions, boundCondition{field: equality.targetField, operator: "=", value: coerced})
	}
	if len(conditions) != 0 {
		generation, err := activeRowGeneration(transaction, sources[join.target].schema)
		if err != nil {
			return nil, err
		}
		access, err := transaction.planRowAccess(sources[join.target].schema, generation, conditions, nil)
		if err != nil {
			return nil, err
		}
		if access.kind == rowAccessScan {
			return nil, fmt.Errorf(
				"kitdb SQL: JOIN target %q field %q requires a primary, unique, or leading index",
				sources[join.target].alias, conditions[0].field.Name,
			)
		}
		visited := 0
		err = transaction.walkAccessRowsObserved(access, stats, func(_, encoded []byte) (bool, error) {
			if candidatePairs != nil {
				if *candidatePairs >= maximumJoinCandidatePairs {
					return false, fmt.Errorf(
						"kitdb SQL: JOIN exceeds the bounded %d-row candidate limit",
						maximumJoinCandidatePairs,
					)
				}
				(*candidatePairs)++
			}
			visited++
			if visited == 1 || visited&255 == 0 {
				if err := ctx.Err(); err != nil {
					return false, err
				}
			}
			decoded, err := decodeRow(sources[join.target].schema, encoded)
			if err != nil {
				return false, err
			}
			if matchesAll(decoded.values, conditions) {
				if working != nil {
					if _, err := working.reserveMap(decoded.values, "JOIN target rows"); err != nil {
						return false, err
					}
					if err := working.reserve(materializedMapReferenceBytes(), "JOIN target row references"); err != nil {
						return false, err
					}
				}
				matches = append(matches, decoded.values)
			}
			return false, nil
		})
		if err != nil {
			return nil, err
		}
	}
	if len(matches) == 0 {
		if join.kind != "left" {
			return nil, nil
		}
		if working != nil {
			if err := working.reserve(
				joinEnvironmentWorkingBytes(len(environment.rows)+1), "JOIN intermediate environments",
			); err != nil {
				return nil, err
			}
		}
		rows := append([]map[string]any(nil), environment.rows...)
		rows = append(rows, nil)
		return []joinEnvironment{{rows: rows}}, nil
	}
	result := make([]joinEnvironment, len(matches))
	for index, row := range matches {
		if working != nil {
			if err := working.reserve(
				joinEnvironmentWorkingBytes(len(environment.rows)+1), "JOIN intermediate environments",
			); err != nil {
				return nil, err
			}
		}
		rows := append([]map[string]any(nil), environment.rows...)
		rows = append(rows, row)
		result[index] = joinEnvironment{rows: rows}
	}
	return result, nil
}

func matchesJoinConditions(environment joinEnvironment, conditions []boundJoinCondition) bool {
	for _, condition := range conditions {
		value := joinEnvironmentValue(environment, condition.column)
		comparison := compareFieldValues(condition.column.field, value, condition.value)
		switch condition.operator {
		case "=":
			if value == nil || condition.value == nil || comparison != 0 {
				return false
			}
		case "!=", "<>":
			if value == nil || condition.value == nil || comparison == 0 {
				return false
			}
		case "<":
			if value == nil || condition.value == nil || comparison >= 0 {
				return false
			}
		case "<=":
			if value == nil || condition.value == nil || comparison > 0 {
				return false
			}
		case ">":
			if value == nil || condition.value == nil || comparison <= 0 {
				return false
			}
		case ">=":
			if value == nil || condition.value == nil || comparison < 0 {
				return false
			}
		case "is":
			if value != nil {
				return false
			}
		case "is not":
			if value == nil {
				return false
			}
		}
	}
	return true
}

func compareJoinPredicateValues(leftPlan, rightPlan *boundJoinPredicate, left, right any) int {
	for _, plan := range []*boundJoinPredicate{leftPlan, rightPlan} {
		if plan != nil && plan.column != nil {
			return compareFieldValues(plan.column.field, left, right)
		}
	}
	return compareValues(left, right)
}

func compareJoinOrderValues(order joinOrder, columns []Column, left, right any) int {
	if order.column != nil {
		return compareFieldValues(order.column.field, left, right)
	}
	if order.projection >= 0 && order.projection < len(columns) {
		return compareColumnValues(columns[order.projection], left, right)
	}
	return compareValues(left, right)
}

func joinEnvironmentValue(environment joinEnvironment, column joinColumn) any {
	if column.source < 0 || column.source >= len(environment.rows) || environment.rows[column.source] == nil {
		return nil
	}
	return environment.rows[column.source][column.field.Name]
}

func projectJoinEnvironment(environment joinEnvironment, projection []joinColumn) []any {
	result := make([]any, len(projection))
	for index, column := range projection {
		result[index] = readField(column.field, joinEnvironmentValue(environment, column))
	}
	return result
}

func joinOrderValues(environment joinEnvironment, projected []any, orders []joinOrder) []any {
	result := make([]any, len(orders))
	for index, order := range orders {
		if order.projection >= 0 {
			result[index] = projected[order.projection]
		} else if order.column != nil {
			result[index] = readField(order.column.field, joinEnvironmentValue(environment, *order.column))
		}
	}
	return result
}
