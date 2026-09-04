package work

import (
	"context"
	"fmt"
	"sort"
	"strings"

	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

const kitDBRemoteJoinReferencePrefix = "\x00kitdb-join\x00"

type kitDBRemoteJoinSource struct {
	table *SchemaTable
	alias string
}

type kitDBRemoteJoinReference struct {
	source int
	field  string
	kind   string
	ref    string
}

type kitDBRemoteJoinProjection struct {
	reference  kitDBRemoteJoinReference
	expression *kitSQLExpression
}

type kitDBRemoteJoinOrder struct {
	reference  kitDBRemoteJoinReference
	expression *kitSQLExpression
	direction  string
}

type kitDBRemoteJoinPlan struct {
	kind              string
	sources           [2]kitDBRemoteJoinSource
	onLeft            kitDBRemoteJoinReference
	onRight           kitDBRemoteJoinReference
	lookupName        string
	references        map[string]kitDBRemoteJoinReference
	projections       []kitDBRemoteJoinProjection
	columns           []kitDBRemoteColumn
	projectionAliases map[string]kitDBRemoteJoinProjection
	predicate         *query.Predicate
	basePredicate     *query.Predicate
	filterExpression  *kitSQLExpression
	orders            []kitDBRemoteJoinOrder
}

type kitDBRemoteJoinedRow struct {
	projected []value.Value
	ordered   []value.Value
	key       string
}

func prepareKitDBRemoteJoinBasePlan(
	left *SchemaTable,
	right *SchemaTable,
	statement kitSQLStatement,
) (*kitDBRemoteJoinPlan, error) {
	if len(statement.joins) != 1 {
		return nil, fmt.Errorf("kitdb SQL: SELECT requires exactly one JOIN")
	}
	if len(statement.orders) > kitDBRemoteJoinOrderLimit {
		return nil, fmt.Errorf("kitdb SQL: joined SELECT exceeds %d ORDER BY fields", kitDBRemoteJoinOrderLimit)
	}
	join := statement.joins[0]
	plan := &kitDBRemoteJoinPlan{
		kind: join.kind,
		sources: [2]kitDBRemoteJoinSource{
			{table: left, alias: statement.tableAlias},
			{table: right, alias: join.alias},
		},
		references:        make(map[string]kitDBRemoteJoinReference),
		projectionAliases: make(map[string]kitDBRemoteJoinProjection),
	}
	if strings.EqualFold(plan.sources[0].alias, plan.sources[1].alias) {
		return nil, fmt.Errorf("kitdb SQL: duplicate table alias %q", plan.sources[0].alias)
	}

	first, err := plan.resolve(join.left)
	if err != nil {
		return nil, fmt.Errorf("kitdb SQL: JOIN ON: %w", err)
	}
	second, err := plan.resolve(join.right)
	if err != nil {
		return nil, fmt.Errorf("kitdb SQL: JOIN ON: %w", err)
	}
	if first.source == 1 && second.source == 0 {
		first, second = second, first
	}
	if first.source != 0 || second.source != 1 {
		return nil, fmt.Errorf("kitdb SQL: JOIN ON must compare the source struct with the joined struct")
	}
	if storageClass(first.kind) != storageClass(second.kind) {
		return nil, fmt.Errorf(
			"kitdb SQL: JOIN ON fields %s and %s have incompatible storage types",
			join.left, join.right,
		)
	}
	lookupName, indexed := kitDBRemoteJoinLookup(right, second.field)
	if !indexed {
		return nil, fmt.Errorf(
			"kitdb SQL: JOIN field %q on struct %q must be primary, unique, or the first field of an active index",
			second.field, right.table,
		)
	}
	plan.onLeft, plan.onRight, plan.lookupName = first, second, lookupName
	if statement.predicate != nil {
		predicate, err := plan.canonicalPredicate(*statement.predicate)
		if err != nil {
			return nil, err
		}
		plan.predicate = &predicate
		if base, ok := plan.predicateForSource(predicate, 0); ok {
			plan.basePredicate = &base
		}
	}
	if statement.whereExpression != nil {
		plan.filterExpression = statement.whereExpression
		if _, err := resolveKitSQLExpression(plan.filterExpression, func(requested string) (string, string, error) {
			reference, err := plan.resolve(requested)
			if err != nil {
				return "", "", err
			}
			return reference.ref, reference.kind, nil
		}); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func prepareKitDBRemoteJoinPlan(
	left *SchemaTable,
	right *SchemaTable,
	statement kitSQLStatement,
) (*kitDBRemoteJoinPlan, error) {
	plan, err := prepareKitDBRemoteJoinBasePlan(left, right, statement)
	if err != nil {
		return nil, err
	}
	if len(statement.groups) != 0 || statement.having != nil {
		return nil, fmt.Errorf("kitdb SQL: grouped JOIN requires the grouped execution plan")
	}

	for _, projection := range statement.projections {
		if projection.aggregate != "" {
			return nil, fmt.Errorf("kitdb SQL: joined aggregates require the grouped execution plan")
		}
		if projection.all {
			if projection.alias != "" {
				return nil, fmt.Errorf("kitdb SQL: a star projection cannot have an alias")
			}
			qualifier, field := kitSQLReferenceParts(projection.field)
			if field != "*" {
				return nil, fmt.Errorf("kitdb SQL: invalid star projection %q", projection.field)
			}
			sources := []int{0, 1}
			if qualifier != "" {
				source, err := plan.sourceForQualifier(qualifier)
				if err != nil {
					return nil, err
				}
				sources = []int{source}
			}
			for _, source := range sources {
				for _, definition := range plan.sources[source].table.definition.Fields {
					reference := plan.reference(source, definition.Name, definition.Kind)
					if err := plan.addProjection(reference, definition.Name); err != nil {
						return nil, err
					}
				}
			}
			continue
		}
		if projection.expression != nil {
			expression := projection.expression
			kind, err := resolveKitSQLExpression(expression, func(requested string) (string, string, error) {
				reference, err := plan.resolve(requested)
				if err != nil {
					return "", "", err
				}
				return reference.ref, reference.kind, nil
			})
			if err != nil {
				return nil, err
			}
			name := projection.alias
			if name == "" {
				name = projection.label
			}
			if kind == "null" || kind == "any" {
				kind = "text"
			}
			planned := kitDBRemoteJoinProjection{expression: expression}
			if err := plan.addExpressionProjection(planned, name, kind); err != nil {
				return nil, err
			}
			if projection.alias != "" {
				if err := plan.addProjectionAlias(projection.alias, planned); err != nil {
					return nil, err
				}
			}
			continue
		}
		reference, err := plan.resolve(projection.field)
		if err != nil {
			return nil, err
		}
		name := projection.alias
		if name == "" {
			name = reference.field
		}
		if err := plan.addProjection(reference, name); err != nil {
			return nil, err
		}
		if projection.alias != "" {
			if err := plan.addProjectionAlias(
				projection.alias,
				kitDBRemoteJoinProjection{reference: reference},
			); err != nil {
				return nil, err
			}
		}
	}

	for _, order := range statement.orders {
		planned := kitDBRemoteJoinOrder{direction: order.direction}
		if order.expression != nil {
			planned.expression = order.expression
			if _, err := resolveKitSQLExpression(planned.expression, func(requested string) (string, string, error) {
				reference, err := plan.resolve(requested)
				if err != nil {
					return "", "", err
				}
				return reference.ref, reference.kind, nil
			}); err != nil {
				return nil, err
			}
		} else if projection, found := plan.projectionAliases[strings.ToLower(order.column)]; found {
			planned.reference, planned.expression = projection.reference, projection.expression
		} else {
			reference, err := plan.resolve(order.column)
			if err != nil {
				return nil, err
			}
			planned.reference = reference
		}
		plan.orders = append(plan.orders, planned)
	}
	return plan, nil
}

func (plan *kitDBRemoteJoinPlan) addProjection(reference kitDBRemoteJoinReference, name string) error {
	if len(plan.projections) >= kitDBRemoteJoinProjectionLimit {
		return fmt.Errorf("kitdb SQL: joined SELECT exceeds %d projected fields", kitDBRemoteJoinProjectionLimit)
	}
	plan.projections = append(plan.projections, kitDBRemoteJoinProjection{reference: reference})
	plan.columns = append(plan.columns, kitDBRemoteColumn{name: name, kind: reference.kind})
	return nil
}

func (plan *kitDBRemoteJoinPlan) addExpressionProjection(
	projection kitDBRemoteJoinProjection,
	name string,
	kind string,
) error {
	if len(plan.projections) >= kitDBRemoteJoinProjectionLimit {
		return fmt.Errorf("kitdb SQL: joined SELECT exceeds %d projected fields", kitDBRemoteJoinProjectionLimit)
	}
	plan.projections = append(plan.projections, projection)
	plan.columns = append(plan.columns, kitDBRemoteColumn{name: name, kind: kind})
	return nil
}

func (plan *kitDBRemoteJoinPlan) addProjectionAlias(
	name string,
	projection kitDBRemoteJoinProjection,
) error {
	key := strings.ToLower(name)
	if existing, duplicate := plan.projectionAliases[key]; duplicate &&
		(existing.reference.ref != projection.reference.ref || existing.expression != projection.expression) {
		return fmt.Errorf("kitdb SQL: duplicate joined projection alias %q", name)
	}
	plan.projectionAliases[key] = projection
	return nil
}

func (plan *kitDBRemoteJoinPlan) sourceForQualifier(qualifier string) (int, error) {
	match := -1
	for index, source := range plan.sources {
		if strings.EqualFold(qualifier, source.alias) || strings.EqualFold(qualifier, source.table.table) {
			if match >= 0 && match != index {
				return -1, fmt.Errorf("kitdb SQL: ambiguous table qualifier %q", qualifier)
			}
			match = index
		}
	}
	if match < 0 {
		return -1, fmt.Errorf("kitdb SQL: unknown table qualifier %q", qualifier)
	}
	return match, nil
}

func (plan *kitDBRemoteJoinPlan) resolve(requested string) (kitDBRemoteJoinReference, error) {
	qualifier, fieldName := kitSQLReferenceParts(requested)
	if fieldName == "" || fieldName == "*" {
		return kitDBRemoteJoinReference{}, fmt.Errorf("kitdb SQL: invalid joined column %q", requested)
	}
	if qualifier != "" {
		source, err := plan.sourceForQualifier(qualifier)
		if err != nil {
			return kitDBRemoteJoinReference{}, err
		}
		name, field, err := kitDBRemoteField(plan.sources[source].table.definition, fieldName)
		if err != nil {
			return kitDBRemoteJoinReference{}, err
		}
		return plan.reference(source, name, field.Kind), nil
	}

	match := kitDBRemoteJoinReference{}
	found := false
	for source := range plan.sources {
		name, field, ok := kitDBRemoteOptionalField(plan.sources[source].table.definition, fieldName)
		if !ok {
			continue
		}
		if found {
			return kitDBRemoteJoinReference{}, fmt.Errorf("kitdb SQL: ambiguous column %q", requested)
		}
		match = plan.reference(source, name, field.Kind)
		found = true
	}
	if !found {
		return kitDBRemoteJoinReference{}, fmt.Errorf("kitdb SQL: no such joined column: %s", requested)
	}
	return match, nil
}

func (plan *kitDBRemoteJoinPlan) reference(source int, field, kind string) kitDBRemoteJoinReference {
	ref := fmt.Sprintf("%s%d\x00%s", kitDBRemoteJoinReferencePrefix, source, field)
	reference := kitDBRemoteJoinReference{source: source, field: field, kind: kind, ref: ref}
	plan.references[ref] = reference
	return reference
}

func kitDBRemoteOptionalField(
	definition *StructDef,
	requested string,
) (string, StructFieldDef, bool) {
	if definition == nil {
		return "", StructFieldDef{}, false
	}
	for _, field := range definition.Fields {
		if field.Name == requested {
			return field.Name, field, true
		}
	}
	for _, field := range definition.Fields {
		if strings.EqualFold(field.Name, requested) {
			return field.Name, field, true
		}
	}
	return "", StructFieldDef{}, false
}

func kitDBRemoteJoinLookup(table *SchemaTable, fieldName string) (string, bool) {
	_, field, ok := kitDBRemoteOptionalField(table.definition, fieldName)
	if !ok {
		return "", false
	}
	if field.Primary {
		return "PRIMARY", true
	}
	if field.Unique {
		return "unique_" + table.table + "_" + field.Name, true
	}
	for _, index := range collectIndexes(table.table, table.definition.columns) {
		if len(index.columns) == 0 || index.columns[0] != field.Name || len(index.filter) != 0 {
			continue
		}
		if _, inactive := table.inactiveIndexes[kitDBIndexSignature(index)]; inactive {
			continue
		}
		return index.name, true
	}
	return "", false
}

func (plan *kitDBRemoteJoinPlan) canonicalPredicate(
	predicate query.Predicate,
) (query.Predicate, error) {
	canonical := predicate
	canonical.Children = make([]query.Predicate, len(predicate.Children))
	if predicate.Kind == query.PredicateCondition {
		reference, err := plan.resolve(predicate.Condition.Column)
		if err != nil {
			return query.Predicate{}, err
		}
		canonical.Condition.Column = reference.ref
		if predicate.Condition.IsColumn {
			right, err := plan.resolve(fmt.Sprint(predicate.Condition.Value))
			if err != nil {
				return query.Predicate{}, err
			}
			canonical.Condition.Value = right.ref
		}
	}
	for index, child := range predicate.Children {
		resolved, err := plan.canonicalPredicate(child)
		if err != nil {
			return query.Predicate{}, err
		}
		canonical.Children[index] = resolved
	}
	return canonical, nil
}

func (plan *kitDBRemoteJoinPlan) predicateForSource(
	predicate query.Predicate,
	source int,
) (query.Predicate, bool) {
	canonical := predicate
	canonical.Children = make([]query.Predicate, len(predicate.Children))
	if predicate.Kind == query.PredicateCondition {
		reference, found := plan.references[predicate.Condition.Column]
		if !found || reference.source != source {
			return query.Predicate{}, false
		}
		canonical.Condition.Column = reference.field
		if predicate.Condition.IsColumn {
			right, found := plan.references[fmt.Sprint(predicate.Condition.Value)]
			if !found || right.source != source {
				return query.Predicate{}, false
			}
			canonical.Condition.Value = right.field
		}
	}
	for index, child := range predicate.Children {
		resolved, ok := plan.predicateForSource(child, source)
		if !ok {
			return query.Predicate{}, false
		}
		canonical.Children[index] = resolved
	}
	return canonical, true
}

func (plan *kitDBRemoteJoinPlan) sourcePlan() query.ExecutionPlan {
	source := query.ExecutionPlan{}
	if plan.basePredicate == nil {
		return source
	}
	predicate := *plan.basePredicate
	source.Predicate = &predicate
	if conditions, ok := query.ConjunctiveConditions(&predicate); ok {
		source.Conditions = conditions
	}
	return source
}

func executeKitDBRemoteJoin(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	left, err := kitDBRemoteTable(database, scope, statement.table)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	right, err := kitDBRemoteTable(database, scope, statement.joins[0].table)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ensureKitDBRemoteTableReady(left); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ensureKitDBRemoteTableReady(right); err != nil {
		return kitDBRemoteResult{}, err
	}
	statement, err = lowerKitDBRemoteJoinDistinct(left, right, statement)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if kitDBRemoteGrouped(statement) {
		return executeKitDBRemoteJoinGroups(ctx, left, right, statement)
	}
	aggregate, err := kitDBRemoteAggregateMode(statement.projections)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if aggregate {
		return executeKitDBRemoteJoinGroups(ctx, left, right, statement)
	}
	plan, err := prepareKitDBRemoteJoinPlan(left, right, statement)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	result := kitDBRemoteResult{columns: plan.columns, rows: [][]value.Value{}}
	limit := kitDBRemoteJoinResultLimit(statement)
	if limit == 0 {
		return result, nil
	}

	sourcePlan := plan.sourcePlan()
	leftAccess, err := left.planKitDBAccess(sourcePlan)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	rows := make([]kitDBRemoteJoinedRow, 0, limit)
	qualified := 0
	distinct := make(map[string]struct{})

	visit := func(leftRow kitDBStoredRow, rightRow *kitDBStoredRow) (bool, error) {
		joined := kitDBRemoteJoinedRow{
			projected: make([]value.Value, len(plan.projections)),
			key:       string(leftRow.key),
		}
		if rightRow != nil {
			joined.key += "\x00" + string(rightRow.key)
		}
		for index, projection := range plan.projections {
			item, err := plan.projectedValue(projection, leftRow, rightRow)
			if err != nil {
				return false, fmt.Errorf("kitdb SQL: joined projection %d: %w", index+1, err)
			}
			joined.projected[index] = item
		}
		if statement.distinct {
			key, err := kitSQLExpressionRowKey(joined.projected)
			if err != nil {
				return false, err
			}
			if _, duplicate := distinct[key]; duplicate {
				return false, nil
			}
			if len(distinct) >= kitDBRemoteExpressionInputLimit {
				return false, fmt.Errorf("kitdb SQL: joined DISTINCT exceeds %d rows", kitDBRemoteExpressionInputLimit)
			}
			distinct[key] = struct{}{}
		}
		qualified++
		if len(plan.orders) == 0 && qualified <= statement.offset {
			return false, nil
		}
		if len(plan.orders) != 0 {
			joined.ordered = make([]value.Value, len(plan.orders))
			for index, order := range plan.orders {
				item, err := plan.orderedValue(order, leftRow, rightRow)
				if err != nil {
					return false, fmt.Errorf("kitdb SQL: joined ORDER BY expression %d: %w", index+1, err)
				}
				joined.ordered[index] = item
			}
		}
		rows = append(rows, joined)
		return len(plan.orders) == 0 && len(rows) >= limit, nil
	}

	err = scanKitDBRemoteJoinRows(ctx, left, right, plan, sourcePlan, leftAccess, visit)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}

	if len(plan.orders) != 0 {
		sort.SliceStable(rows, func(leftIndex, rightIndex int) bool {
			leftRow, rightRow := rows[leftIndex], rows[rightIndex]
			for index, order := range plan.orders {
				comparison := kitDBCompareValues(leftRow.ordered[index], rightRow.ordered[index])
				if comparison == 0 {
					continue
				}
				if strings.EqualFold(order.direction, "desc") {
					return comparison > 0
				}
				return comparison < 0
			}
			return leftRow.key < rightRow.key
		})
		offset := statement.offset
		if offset > len(rows) {
			offset = len(rows)
		}
		rows = rows[offset:]
		if len(rows) > limit {
			rows = rows[:limit]
		}
	}
	result.rows = make([][]value.Value, len(rows))
	for index, row := range rows {
		result.rows[index] = row.projected
	}
	return result, nil
}

func scanKitDBRemoteJoinRows(
	ctx context.Context,
	left *SchemaTable,
	right *SchemaTable,
	plan *kitDBRemoteJoinPlan,
	sourcePlan query.ExecutionPlan,
	leftAccess kitDBAccessPlan,
	visit func(kitDBStoredRow, *kitDBStoredRow) (bool, error),
) error {
	leftRows := 0
	pairs := 0
	stopped := false
	emit := func(leftRow kitDBStoredRow, rightRow *kitDBStoredRow) error {
		pairs++
		if err := ensureKitDBRemoteJoinPairs(pairs); err != nil {
			return err
		}
		if pairs&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		truth, err := kitDBEvaluatePredicate(plan.predicate, func(encoded string) (value.Value, string, error) {
			reference, found := plan.references[encoded]
			if !found {
				return value.Value{}, "", fmt.Errorf("kitdb SQL: unresolved joined reference")
			}
			item := plan.rowValue(reference, leftRow, rightRow)
			item, kind := kitDBRemoteLogicalPredicateValue(item, reference.kind)
			return item, kind, nil
		})
		if err != nil || truth != kitDBTrue {
			return err
		}
		if plan.filterExpression != nil {
			item, err := plan.expressionValue(plan.filterExpression, leftRow, rightRow)
			if err != nil {
				return fmt.Errorf("kitdb SQL: joined WHERE expression: %w", err)
			}
			truth, err := kitSQLExpressionTruth(item)
			if err != nil {
				return fmt.Errorf("kitdb SQL: joined WHERE expression: %w", err)
			}
			if truth != kitDBTrue {
				return nil
			}
		}
		stopped, err = visit(leftRow, rightRow)
		return err
	}

	return withKitDBRemoteJoinReader(left, right, func(reader kitDBRecordReader) error {
		return left.scanKitDBRowsFrom(reader, sourcePlan, leftAccess, func(leftRow kitDBStoredRow) (bool, error) {
			leftRows++
			if err := ensureKitDBRemoteJoinInput(leftRows); err != nil {
				return false, err
			}
			if leftRows&1023 == 0 {
				if err := ctx.Err(); err != nil {
					return false, err
				}
			}
			onValue, found := leftRow.values[plan.onLeft.field]
			if !found {
				onValue = value.NewNil()
			}
			matched := false
			if !onValue.IsNil() {
				rightPlan := query.ExecutionPlan{Conditions: []query.Condition{{
					Column: plan.onRight.field, Operator: "=", Value: onValue, Logic: "AND",
				}}}
				rightAccess, err := right.planKitDBAccess(rightPlan)
				if err != nil {
					return false, err
				}
				if rightAccess.kind == kitDBAccessScan {
					return false, fmt.Errorf(
						"kitdb SQL: JOIN lookup on %s.%s could not use its declared index",
						right.table, plan.onRight.field,
					)
				}
				err = right.scanKitDBRowsFrom(reader, rightPlan, rightAccess, func(candidate kitDBStoredRow) (bool, error) {
					matched = true
					if err := emit(leftRow, &candidate); err != nil {
						return false, err
					}
					return stopped, nil
				})
				if err != nil {
					return false, err
				}
			}
			if !matched && plan.kind == "left" {
				if err := emit(leftRow, nil); err != nil {
					return false, err
				}
			}
			return stopped, nil
		})
	})
}

func ensureKitDBRemoteJoinInput(current int) error {
	if current > kitDBRemoteJoinInputLimit {
		return fmt.Errorf("kitdb SQL: JOIN exceeds %d source rows", kitDBRemoteJoinInputLimit)
	}
	return nil
}

func ensureKitDBRemoteJoinPairs(current int) error {
	if current > kitDBRemoteJoinPairLimit {
		return fmt.Errorf("kitdb SQL: JOIN exceeds %d candidate pairs", kitDBRemoteJoinPairLimit)
	}
	return nil
}

func (plan *kitDBRemoteJoinPlan) rowValue(
	reference kitDBRemoteJoinReference,
	left kitDBStoredRow,
	right *kitDBStoredRow,
) value.Value {
	return coerceRead(reference.kind, plan.storedRowValue(reference, left, right))
}

func (plan *kitDBRemoteJoinPlan) projectedValue(
	projection kitDBRemoteJoinProjection,
	left kitDBStoredRow,
	right *kitDBStoredRow,
) (value.Value, error) {
	if projection.expression == nil {
		return plan.rowValue(projection.reference, left, right), nil
	}
	return plan.expressionValue(projection.expression, left, right)
}

func (plan *kitDBRemoteJoinPlan) orderedValue(
	order kitDBRemoteJoinOrder,
	left kitDBStoredRow,
	right *kitDBStoredRow,
) (value.Value, error) {
	if order.expression == nil {
		return plan.rowValue(order.reference, left, right), nil
	}
	return plan.expressionValue(order.expression, left, right)
}

func (plan *kitDBRemoteJoinPlan) expressionValue(
	expression *kitSQLExpression,
	left kitDBStoredRow,
	right *kitDBStoredRow,
) (value.Value, error) {
	return evaluateKitSQLExpression(expression, func(encoded string) (value.Value, error) {
		reference, found := plan.references[encoded]
		if !found {
			return value.Value{}, fmt.Errorf("kitdb SQL: unresolved joined expression reference")
		}
		return plan.rowValue(reference, left, right), nil
	})
}

func (plan *kitDBRemoteJoinPlan) storedRowValue(
	reference kitDBRemoteJoinReference,
	left kitDBStoredRow,
	right *kitDBStoredRow,
) value.Value {
	if reference.source == 1 && right == nil {
		return value.NewNil()
	}
	row := left
	if reference.source == 1 {
		row = *right
	}
	item, found := row.values[reference.field]
	if !found {
		return value.NewNil()
	}
	return item
}

func kitDBRemoteJoinResultLimit(statement kitSQLStatement) int {
	limit := query.DefaultDBLimit
	if statement.hasLimit {
		limit = statement.limit
	}
	if limit > kitDBRemoteSelectRowLimit {
		limit = kitDBRemoteSelectRowLimit
	}
	return limit
}

func withKitDBRemoteJoinReader(
	left *SchemaTable,
	right *SchemaTable,
	visit func(kitDBRecordReader) error,
) error {
	if left.transaction != nil {
		if right.transaction != left.transaction {
			return fmt.Errorf("kitdb SQL: JOIN structs do not share one record transaction")
		}
		return visit(left.transaction)
	}
	if left.tenant != right.tenant || left.dbName != right.dbName {
		return fmt.Errorf("kitdb SQL: JOIN structs must belong to one KitDB file")
	}
	managed, err := left.kitDBManaged()
	if err != nil {
		return err
	}
	defer managed.Release()
	managed.writeMu.RLock()
	for _, table := range []*SchemaTable{left, right} {
		if err := validateKitDBIndexLayoutEpoch(managed.database, table.definition, table.indexEpoch); err != nil {
			managed.writeMu.RUnlock()
			return err
		}
	}
	snapshot, err := managed.database.Snapshot()
	managed.writeMu.RUnlock()
	if err != nil {
		return err
	}
	defer snapshot.Close()
	return visit(kitDBSnapshotReader{snapshot: snapshot})
}

func kitDBExplainJoinDetail(
	left *SchemaTable,
	access kitDBAccessPlan,
	source query.ExecutionPlan,
	statement kitSQLStatement,
	plan *kitDBRemoteJoinPlan,
) string {
	detail := kitDBExplainAccessDetail(left.table, access, source)
	detail += "; INDEX NESTED LOOP " + strings.ToUpper(plan.kind) + " JOIN "
	detail += plan.sources[1].table.table + " USING " + plan.lookupName
	if plan.predicate != nil || plan.filterExpression != nil {
		detail += "; JOIN FILTER"
	}
	if kitDBRemoteUsesExpressionEngine(statement) {
		detail += "; PROJECT EXPRESSIONS"
	}
	if len(plan.orders) != 0 {
		detail += "; TEMP SORT"
	} else {
		detail += "; EARLY STOP"
	}
	detail += fmt.Sprintf(
		"; LIMIT %d; INPUT CAP %d; PAIR CAP %d",
		kitDBRemoteJoinResultLimit(statement), kitDBRemoteJoinInputLimit, kitDBRemoteJoinPairLimit,
	)
	if statement.offset > 0 {
		detail += fmt.Sprintf(" OFFSET %d", statement.offset)
	}
	return detail
}

func executeKitDBRemoteJoinExplain(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	left, err := kitDBRemoteTable(database, scope, statement.table)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	right, err := kitDBRemoteTable(database, scope, statement.joins[0].table)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ensureKitDBRemoteTableReady(left); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ensureKitDBRemoteTableReady(right); err != nil {
		return kitDBRemoteResult{}, err
	}
	statement, err = lowerKitDBRemoteJoinDistinct(left, right, statement)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if kitDBRemoteGrouped(statement) {
		return executeKitDBRemoteJoinGroupExplain(ctx, left, right, statement)
	}
	aggregate, err := kitDBRemoteAggregateMode(statement.projections)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if aggregate {
		return executeKitDBRemoteJoinGroupExplain(ctx, left, right, statement)
	}
	plan, err := prepareKitDBRemoteJoinPlan(left, right, statement)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	source := plan.sourcePlan()
	access, err := left.planKitDBAccess(source)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	return kitDBRemoteResult{
		columns: []kitDBRemoteColumn{
			{name: "id", kind: "integer"},
			{name: "parent", kind: "integer"},
			{name: "notused", kind: "integer"},
			{name: "detail", kind: "text"},
		},
		rows: [][]value.Value{{
			value.New(0), value.New(0), value.New(0),
			value.New(kitDBExplainJoinDetail(left, access, source, statement, plan)),
		}},
	}, nil
}
