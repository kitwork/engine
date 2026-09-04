package relational

import (
	"bytes"
	"context"
	"fmt"
	"math"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbrecord "github.com/kitwork/engine/kitdb/record"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

// indexOnlyAggregateAccess describes a secondary-index scan that contains
// every field needed to filter, group, and aggregate without fetching KROW.
type indexOnlyAggregateAccess struct {
	index           secondaryIndex
	options         kitdbengine.RangeOptions
	prefix          []byte
	components      []kitdbsql.Field
	required        []bool
	constants       map[string]any
	conditionsExact bool
	equalityPrefix  int
	rangeField      string
}

func (transaction *Transaction) planIndexOnlyAggregate(
	schema kitdbsql.Schema,
	plan *kitdbsql.SelectStatement,
	groupFields []kitdbsql.Field,
	bindings []aggregateBinding,
	conditions []boundCondition,
	predicate *boundPredicate,
) (indexOnlyAggregateAccess, bool, error) {
	if plan == nil || plan.Distinct || plan.Search != nil {
		return indexOnlyAggregateAccess{}, false, nil
	}
	indexes, err := collectSecondaryIndexes(schema)
	if err != nil {
		return indexOnlyAggregateAccess{}, false, err
	}
	equality := make(map[string]any)
	for _, condition := range conditions {
		if condition.operator == "=" && condition.value != nil ||
			condition.operator == "is" && condition.value == nil {
			equality[condition.field.Name] = condition.value
		}
	}
	bestScore, bestWidth := -1, math.MaxInt
	var best indexOnlyAggregateAccess
	for _, index := range indexes {
		if len(index.filter) != 0 {
			continue
		}
		indexGeneration, ready, err := secondaryIndexPhysicalLayout(transaction, schema, index)
		if err != nil {
			return indexOnlyAggregateAccess{}, false, err
		}
		if !ready {
			continue
		}
		components := append([]kitdbsql.Field(nil), index.fields...)
		components = append(components, schema.PrimaryFields()...)
		positions := make(map[string]int, len(components))
		valid := true
		for position, field := range components {
			if field.ID == "" {
				valid = false
				break
			}
			if _, exists := positions[field.ID]; !exists {
				positions[field.ID] = position
			}
		}
		if !valid {
			continue
		}
		required := make([]bool, len(components))
		require := func(field kitdbsql.Field) bool {
			position, covered := positions[field.ID]
			if covered {
				required[position] = true
			}
			return covered
		}
		for _, field := range groupFields {
			valid = valid && require(field)
		}
		for _, binding := range bindings {
			if binding.field != nil {
				valid = valid && require(*binding.field)
			}
		}
		for _, condition := range conditions {
			valid = valid && require(condition.field)
		}
		valid = valid && requirePredicateIndexFields(predicate, require)
		if !valid {
			continue
		}
		constants := make(map[string]any)
		for fieldID, position := range positions {
			if !required[position] {
				continue
			}
			field := components[position]
			item, fixed := equality[field.Name]
			if !fixed || field.ID != fieldID {
				continue
			}
			// Keep the same internal representation as decodeRow. Predicate and
			// result readers apply readField at their normal SQL boundary.
			constants[field.Name] = item
			required[position] = false
		}

		basePrefix, err := secondaryIndexBasePrefixForGeneration(schema, index, indexGeneration)
		if err != nil {
			return indexOnlyAggregateAccess{}, false, err
		}
		prefix := bytes.Clone(basePrefix)
		equalityPrefix := 0
		for _, field := range index.fields {
			item, fixed := equality[field.Name]
			if !fixed {
				break
			}
			component, err := orderedFieldScalarComponent(field, item)
			if err != nil {
				return indexOnlyAggregateAccess{}, false, err
			}
			prefix = append(prefix, component...)
			equalityPrefix++
		}
		options := kitdbengine.RangeOptions{Prefix: bytes.Clone(prefix)}
		rangeField := ""
		if equalityPrefix < len(index.fields) {
			field := index.fields[equalityPrefix]
			bounded, err := boundSecondaryIndexRange(&options, field, conditions)
			if err != nil {
				return indexOnlyAggregateAccess{}, false, err
			}
			if bounded {
				rangeField = field.Name
			}
		}
		score := equalityPrefix * 100
		if rangeField != "" {
			score += 30
		}
		width := len(components)
		if score < bestScore || score == bestScore && width >= bestWidth {
			continue
		}
		best = indexOnlyAggregateAccess{
			index: index, options: options, prefix: basePrefix,
			components: components, required: required, constants: constants,
			conditionsExact: plannerConditionsCoverPredicate(plan.Predicate, len(conditions)),
			equalityPrefix:  equalityPrefix, rangeField: rangeField,
		}
		bestScore, bestWidth = score, width
	}
	return best, bestScore >= 0, nil
}

func plannerConditionsCoverPredicate(predicate *kitdbsql.CheckPlan, conditions int) bool {
	if predicate == nil {
		return conditions == 0
	}
	leaves, exact := countPlannerConditionLeaves(predicate)
	return exact && leaves == conditions
}

func countPlannerConditionLeaves(predicate *kitdbsql.CheckPlan) (int, bool) {
	if predicate == nil {
		return 0, false
	}
	if predicate.Kind == "binary" && predicate.Operator == "and" && len(predicate.Arguments) == 2 {
		left, leftOK := countPlannerConditionLeaves(&predicate.Arguments[0])
		right, rightOK := countPlannerConditionLeaves(&predicate.Arguments[1])
		return left + right, leftOK && rightOK
	}
	if predicate.Kind == "binary" && isPredicateComparison(predicate.Operator) && len(predicate.Arguments) == 2 &&
		predicate.Arguments[0].Kind == "field" && plannerConditionLiteral(predicate.Arguments[1]) {
		return 1, true
	}
	if predicate.Kind == "unary" && (predicate.Operator == "is null" || predicate.Operator == "is not null") &&
		len(predicate.Arguments) == 1 && predicate.Arguments[0].Kind == "field" {
		return 1, true
	}
	return 0, false
}

func plannerConditionLiteral(predicate kitdbsql.CheckPlan) bool {
	if predicate.Kind == "literal" {
		return true
	}
	return predicate.Kind == "cast" && len(predicate.Arguments) == 1 && predicate.Arguments[0].Kind == "literal"
}

func requirePredicateIndexFields(predicate *boundPredicate, require func(kitdbsql.Field) bool) bool {
	if predicate == nil {
		return true
	}
	if predicate.field != nil && !require(*predicate.field) {
		return false
	}
	for _, argument := range predicate.arguments {
		if !requirePredicateIndexFields(argument, require) {
			return false
		}
	}
	return true
}

func (transaction *Transaction) executeIndexOnlyAggregate(
	ctx context.Context,
	access indexOnlyAggregateAccess,
	plan *kitdbsql.SelectStatement,
	columns []Column,
	bindings []aggregateBinding,
	groupFields []kitdbsql.Field,
	conditions []boundCondition,
	predicate *boundPredicate,
	parameters []any,
	observe bool,
	working *materializationWorkingSet,
) (Result, error) {
	stats := &ExecutionStats{Path: "index-only-aggregate"}
	groups := make(map[string]*aggregateGroup)
	order := make([]string, 0)
	decimalStates := 0
	for _, binding := range bindings {
		if exactDecimalAggregate(binding.field, binding.function) {
			decimalStates++
		}
	}
	if len(groupFields) == 0 {
		if working != nil {
			if err := working.reserve(
				aggregateGroupWorkingBytes("", nil, nil, bindings, false), "GROUP BY state",
			); err != nil {
				return Result{}, err
			}
		}
		groups[""] = &aggregateGroup{values: make(map[string]any), states: make([]aggregateState, len(bindings))}
		order = append(order, "")
	}

	row := make(map[string]any, len(access.components))
	for field, value := range access.constants {
		row[field] = value
	}
	var cursorStats kitdbengine.CursorStats
	var observed *kitdbengine.CursorStats
	if observe {
		observed = &cursorStats
	}
	err := transaction.scanKeys(access.options, observed, func(key []byte) (bool, error) {
		if stats.IndexEntriesScanned&255 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		stats.IndexEntriesScanned++
		if err := decodeCoveringIndexRow(key, access, row); err != nil {
			return false, fmt.Errorf("kitdb: index %q: %w", access.index.name, err)
		}
		if !matchesAll(row, conditions) {
			return false, nil
		}
		if !access.conditionsExact {
			matched, err := predicateMatches(row, predicate)
			if err != nil || !matched {
				return false, err
			}
		}
		stats.RowsMatched++
		groupKey, err := aggregateGroupKey(row, groupFields)
		if err != nil {
			return false, err
		}
		group := groups[groupKey]
		if group == nil {
			if len(groups) >= transaction.engine.maximumResultRows {
				return false, fmt.Errorf(
					"kitdb SQL: GROUP BY exceeds this server's %d-group limit",
					transaction.engine.maximumResultRows,
				)
			}
			if decimalStates != 0 && len(groups) >= maximumDecimalGroupStateBytes/(decimalStates*(maximumDecimalText+128)) {
				return false, fmt.Errorf(
					"kitdb SQL: decimal GROUP BY exceeds the %d-byte exact aggregate state budget",
					maximumDecimalGroupStateBytes,
				)
			}
			if working != nil {
				if err := working.reserve(
					aggregateGroupWorkingBytes(groupKey, row, groupFields, bindings, false), "GROUP BY state",
				); err != nil {
					return false, err
				}
			}
			group = &aggregateGroup{
				values: aggregateGroupValues(row, groupFields),
				states: make([]aggregateState, len(bindings)),
			}
			groups[groupKey] = group
			order = append(order, groupKey)
		}
		for position, binding := range bindings {
			if binding.function == "" {
				continue
			}
			var value any
			if binding.field != nil {
				value = row[binding.field.Name]
			}
			if err := updateAggregateStateAccounted(
				working, &group.states[position], binding.function, value, binding.field, binding.field == nil,
			); err != nil {
				return false, err
			}
		}
		return false, nil
	})
	if err != nil {
		return Result{}, err
	}
	stats.addCursorStats(cursorStats)
	rows := make([][]any, 0, len(order))
	for groupPosition, key := range order {
		if groupPosition&255 == 0 {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
		}
		group := groups[key]
		resultRow := make([]any, len(plan.Projection))
		for position := range plan.Projection {
			binding := bindings[position]
			if binding.function == "" {
				field := *binding.field
				resultRow[position] = readField(field, group.values[field.Name])
				continue
			}
			value, err := aggregateStateValue(group.states[position], binding.function, binding.field)
			if err != nil {
				return Result{}, err
			}
			resultRow[position] = value
		}
		if err := working.reserveRow(resultRow, "GROUP BY index-only result rows"); err != nil {
			return Result{}, err
		}
		rows = append(rows, resultRow)
	}
	stats.Groups = uint64(len(groups))
	return finishAggregateRows(ctx, rows, columns, plan, parameters, stats, working)
}

func decodeCoveringIndexRow(key []byte, access indexOnlyAggregateAccess, row map[string]any) error {
	if !bytes.HasPrefix(key, access.prefix) || len(key) == len(access.prefix) {
		return fmt.Errorf("invalid covering-index key envelope")
	}
	encoded := key[len(access.prefix):]
	position := 0
	for component, field := range access.components {
		if position >= len(encoded) {
			return fmt.Errorf("invalid covering-index key component count")
		}
		size, err := kitdbrecord.OrderedScalarComponentSize(encoded[position:])
		if err != nil {
			return err
		}
		if access.required[component] {
			scalar, decodedSize, err := kitdbrecord.DecodeOrderedScalarComponent(encoded[position:])
			if err != nil {
				return err
			}
			if decodedSize != size {
				return fmt.Errorf("inconsistent ordered scalar size")
			}
			value, err := indexScalarValue(scalar)
			if err != nil {
				return err
			}
			value, err = coerceField(field, value)
			if err != nil {
				return err
			}
			row[field.Name] = value
		}
		position += size
	}
	if position != len(encoded) {
		return fmt.Errorf("invalid covering-index key component count")
	}
	return nil
}
