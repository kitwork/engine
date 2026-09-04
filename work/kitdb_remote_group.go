package work

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

const kitDBRemoteGroupReferencePrefix = "\x00kitdb-group\x00"
const kitDBRemoteGroupComputedPrefix = "\x00kitdb-computed\x00"

type kitDBRemoteGroupField struct {
	name   string
	kind   string
	source string
	ref    string
}

type kitDBRemoteResolvedGroupField struct {
	name   string
	kind   string
	source string
	spec   *ColumnSpec
}

type kitDBRemoteGroupResolver func(string) (kitDBRemoteResolvedGroupField, error)

type kitDBRemoteGroupProjection struct {
	ref string
}

type kitDBRemoteGroupOrder struct {
	ref       string
	direction string
}

type kitDBRemoteGroupComputed struct {
	ref        string
	expression *kitSQLExpression
}

type kitDBRemoteGroupPlan struct {
	fields              []kitDBRemoteGroupField
	aggregates          []kitDBAggregateRequest
	aggregateRefs       []string
	aggregateIndex      map[string]int
	aggregateSpecs      map[string]*ColumnSpec
	aggregateTemplate   []kitDBAggregateState
	projections         []kitDBRemoteGroupProjection
	columns             []kitDBRemoteColumn
	aliases             map[string]string
	ambiguousAliases    map[string]struct{}
	groupRefs           map[string]string
	refKinds            map[string]string
	computed            []kitDBRemoteGroupComputed
	having              *query.Predicate
	havingExpressionRef string
	orders              []kitDBRemoteGroupOrder
}

type kitDBRemoteGroupState struct {
	key        string
	fields     []value.Value
	aggregates []kitDBAggregateState
}

type kitDBRemoteGroupedRow struct {
	key    string
	values map[string]value.Value
}

func kitDBRemoteGrouped(statement kitSQLStatement) bool {
	if len(statement.groups) != 0 || statement.having != nil || statement.havingExpression != nil {
		return true
	}
	for _, projection := range statement.projections {
		if kitSQLExpressionContainsAggregate(projection.expression) {
			return true
		}
	}
	for _, order := range statement.orders {
		if kitSQLExpressionContainsAggregate(order.expression) {
			return true
		}
	}
	return false
}

func lowerKitDBRemoteDistinct(
	statement kitSQLStatement,
	expandStar func(string) ([]kitSQLProjection, error),
) (kitSQLStatement, error) {
	if !statement.distinct {
		return statement, nil
	}
	if kitDBRemoteGrouped(statement) {
		return kitSQLStatement{}, fmt.Errorf("kitdb SQL: DISTINCT cannot be combined with GROUP BY or HAVING")
	}
	for _, projection := range statement.projections {
		if projection.expression != nil {
			// Expression DISTINCT is evaluated after projection by the bounded
			// expression operator; it cannot be lowered to source GROUP BY fields.
			return statement, nil
		}
	}
	aggregate, err := kitDBRemoteAggregateMode(statement.projections)
	if err != nil {
		return kitSQLStatement{}, err
	}
	if aggregate {
		// A scalar aggregate already produces at most one row.
		return statement, nil
	}
	expanded := make([]kitSQLProjection, 0, len(statement.projections))
	for _, projection := range statement.projections {
		if !projection.all {
			expanded = append(expanded, projection)
			continue
		}
		if projection.alias != "" {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: a star projection cannot have an alias")
		}
		fields, err := expandStar(projection.field)
		if err != nil {
			return kitSQLStatement{}, err
		}
		expanded = append(expanded, fields...)
	}
	statement.projections = expanded
	statement.groups = make([]string, len(expanded))
	for index, projection := range expanded {
		statement.groups[index] = projection.field
	}
	return statement, nil
}

func lowerKitDBRemoteTableDistinct(
	table *SchemaTable,
	statement kitSQLStatement,
) (kitSQLStatement, error) {
	return lowerKitDBRemoteDistinct(statement, func(requested string) ([]kitSQLProjection, error) {
		qualifier, field := kitSQLReferenceParts(requested)
		if field != "*" {
			return nil, fmt.Errorf("kitdb SQL: invalid star projection %q", requested)
		}
		if qualifier != "" && !strings.EqualFold(qualifier, statement.tableAlias) &&
			!strings.EqualFold(qualifier, table.table) {
			return nil, fmt.Errorf("kitdb SQL: unknown table qualifier %q", qualifier)
		}
		fields := make([]kitSQLProjection, len(table.definition.Fields))
		for index, definition := range table.definition.Fields {
			fields[index] = kitSQLProjection{field: definition.Name}
		}
		return fields, nil
	})
}

func kitDBRemoteGroupReference(field string) string {
	return kitDBRemoteGroupReferencePrefix + field
}

func prepareKitDBRemoteGroupPlan(table *SchemaTable, statement kitSQLStatement) (*kitDBRemoteGroupPlan, error) {
	return prepareKitDBRemoteGroupPlanWithResolver(statement, func(requested string) (kitDBRemoteResolvedGroupField, error) {
		name, field, err := kitDBRemoteField(table.definition, requested)
		if err != nil {
			return kitDBRemoteResolvedGroupField{}, err
		}
		spec := table.columns[name]
		if spec == nil {
			return kitDBRemoteResolvedGroupField{}, fmt.Errorf("kitdb SQL: struct %q has no field %q", table.table, name)
		}
		return kitDBRemoteResolvedGroupField{name: name, kind: field.Kind, source: name, spec: spec}, nil
	})
}

func prepareKitDBRemoteGroupPlanWithResolver(
	statement kitSQLStatement,
	resolve kitDBRemoteGroupResolver,
) (*kitDBRemoteGroupPlan, error) {
	if len(statement.groups) > kitDBRemoteGroupFieldLimit {
		return nil, fmt.Errorf("kitdb SQL: GROUP BY exceeds %d fields", kitDBRemoteGroupFieldLimit)
	}
	if len(statement.projections) > kitDBRemoteGroupProjectionLimit {
		return nil, fmt.Errorf("kitdb SQL: grouped SELECT exceeds %d projections", kitDBRemoteGroupProjectionLimit)
	}
	plan := &kitDBRemoteGroupPlan{
		aggregateIndex:   make(map[string]int),
		aggregateSpecs:   make(map[string]*ColumnSpec),
		aliases:          make(map[string]string),
		ambiguousAliases: make(map[string]struct{}),
		groupRefs:        make(map[string]string),
		refKinds:         make(map[string]string),
	}
	grouped := make(map[string]struct{}, len(statement.groups))
	for _, requested := range statement.groups {
		field, err := resolve(requested)
		if err != nil {
			return nil, err
		}
		if !kitDBRemoteGroupableKind(field.kind) {
			return nil, fmt.Errorf("kitdb SQL: GROUP BY does not support field %q of kind %s", field.name, field.kind)
		}
		if _, duplicate := grouped[field.source]; duplicate {
			if statement.distinct {
				continue
			}
			return nil, fmt.Errorf("kitdb SQL: duplicate GROUP BY field %q", requested)
		}
		grouped[field.source] = struct{}{}
		ref := kitDBRemoteGroupReference(field.source)
		plan.fields = append(plan.fields, kitDBRemoteGroupField{
			name: field.name, kind: field.kind, source: field.source, ref: ref,
		})
		plan.groupRefs[field.source] = ref
		plan.refKinds[ref] = field.kind
		plan.addGroupAlias(field.name, ref)
	}

	for _, projection := range statement.projections {
		if projection.all {
			return nil, fmt.Errorf("kitdb SQL: * is not supported with GROUP BY or HAVING")
		}
		ref := ""
		kind := ""
		name := projection.alias
		if projection.expression != nil {
			expression := projection.expression
			var err error
			kind, err = plan.compileExpression(resolve, expression, grouped)
			if err != nil {
				return nil, err
			}
			ref = plan.addComputed(expression, kind)
			if name == "" {
				name = projection.label
			}
		} else if projection.aggregate == "" {
			field, err := resolve(projection.field)
			if err != nil {
				return nil, err
			}
			if _, found := grouped[field.source]; !found {
				return nil, fmt.Errorf("kitdb SQL: field %q must appear in GROUP BY or be aggregated", field.name)
			}
			ref, kind = plan.groupRefs[field.source], field.kind
			if name == "" {
				name = field.name
			}
		} else {
			var err error
			ref, kind, err = plan.addAggregate(resolve, projection.aggregate, projection.field)
			if err != nil {
				return nil, err
			}
			if name == "" {
				name = kitDBRemoteAggregateName(
					projection.aggregate,
					kitDBAggregateLabel(plan.aggregates[plan.aggregateIndex[ref]]),
				)
			}
		}
		plan.projections = append(plan.projections, kitDBRemoteGroupProjection{ref: ref})
		if kind == "null" || kind == "any" {
			kind = "text"
		}
		plan.columns = append(plan.columns, kitDBRemoteColumn{name: name, kind: kind})
		if projection.alias != "" || projection.aggregate != "" {
			if err := plan.addAlias(name, ref); err != nil {
				return nil, err
			}
		}
	}

	if statement.having != nil {
		having, err := plan.canonicalHaving(resolve, *statement.having)
		if err != nil {
			return nil, err
		}
		plan.having = &having
	}
	if statement.havingExpression != nil {
		expression := statement.havingExpression
		kind, err := plan.compileExpression(resolve, expression, grouped)
		if err != nil {
			return nil, err
		}
		plan.havingExpressionRef = plan.addComputed(expression, kind)
	}
	if len(plan.fields) == 0 && len(plan.aggregates) == 0 {
		return nil, fmt.Errorf("kitdb SQL: HAVING requires GROUP BY or an aggregate")
	}
	for _, order := range statement.orders {
		if order.expression != nil {
			expression := order.expression
			kind, err := plan.compileExpression(resolve, expression, grouped)
			if err != nil {
				return nil, err
			}
			plan.orders = append(plan.orders, kitDBRemoteGroupOrder{
				ref: plan.addComputed(expression, kind), direction: order.direction,
			})
			continue
		}
		ref, found := plan.aliases[strings.ToLower(order.column)]
		if !found {
			field, err := resolve(order.column)
			if err == nil {
				ref, found = plan.groupRefs[field.source]
			}
			if !found {
				return nil, fmt.Errorf("kitdb SQL: ORDER BY field %q must be grouped or be a SELECT alias", order.column)
			}
		}
		plan.orders = append(plan.orders, kitDBRemoteGroupOrder{ref: ref, direction: order.direction})
	}
	if len(plan.aggregates) != 0 {
		states, err := prepareKitDBAggregateStates(plan.aggregates, func(field string) (*ColumnSpec, bool) {
			spec := plan.aggregateSpecs[field]
			return spec, spec != nil
		}, "grouped input")
		if err != nil {
			return nil, err
		}
		plan.aggregateTemplate = states
	}
	return plan, nil
}

func (plan *kitDBRemoteGroupPlan) addComputed(expression *kitSQLExpression, kind string) string {
	ref := fmt.Sprintf("%s%d", kitDBRemoteGroupComputedPrefix, len(plan.computed))
	plan.computed = append(plan.computed, kitDBRemoteGroupComputed{ref: ref, expression: expression})
	plan.refKinds[ref] = kind
	return ref
}

func (plan *kitDBRemoteGroupPlan) compileExpression(
	resolve kitDBRemoteGroupResolver,
	expression *kitSQLExpression,
	grouped map[string]struct{},
) (string, error) {
	var lowerAggregates func(*kitSQLExpression) error
	lowerAggregates = func(current *kitSQLExpression) error {
		if current == nil {
			return nil
		}
		if current.kind == kitSQLExpressionFunction && kitSQLAggregateName(current.operator) {
			if len(current.arguments) != 1 {
				return fmt.Errorf("kitdb SQL: %s expects one field", strings.ToUpper(current.operator))
			}
			requested := ""
			switch argument := current.arguments[0]; argument.kind {
			case kitSQLExpressionStar:
				requested = "*"
			case kitSQLExpressionReference:
				requested = argument.reference
			default:
				return fmt.Errorf("kitdb SQL: %s expects one field", strings.ToUpper(current.operator))
			}
			ref, kind, err := plan.addAggregate(resolve, current.operator, requested)
			if err != nil {
				return err
			}
			*current = kitSQLExpression{
				kind: kitSQLExpressionReference, reference: ref,
				resultKind: kind, resolved: true,
			}
			return nil
		}
		for _, argument := range current.arguments {
			if err := lowerAggregates(argument); err != nil {
				return err
			}
		}
		if err := lowerAggregates(current.caseBase); err != nil {
			return err
		}
		for _, branch := range current.branches {
			if err := lowerAggregates(branch.when); err != nil {
				return err
			}
			if err := lowerAggregates(branch.then); err != nil {
				return err
			}
		}
		return lowerAggregates(current.fallback)
	}
	if err := lowerAggregates(expression); err != nil {
		return "", err
	}
	return resolveKitSQLExpression(expression, func(requested string) (string, string, error) {
		key := strings.ToLower(strings.TrimSpace(requested))
		if _, ambiguous := plan.ambiguousAliases[key]; ambiguous {
			return "", "", fmt.Errorf("kitdb SQL: grouped reference %q is ambiguous", requested)
		}
		if ref, found := plan.aliases[key]; found {
			return ref, plan.refKinds[ref], nil
		}
		field, err := resolve(requested)
		if err != nil {
			return "", "", err
		}
		if _, found := grouped[field.source]; !found {
			return "", "", fmt.Errorf(
				"kitdb SQL: field %q must appear in GROUP BY or be aggregated",
				field.name,
			)
		}
		ref := plan.groupRefs[field.source]
		return ref, field.kind, nil
	})
}

func (plan *kitDBRemoteGroupPlan) addAlias(name, ref string) error {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return fmt.Errorf("kitdb SQL: grouped result name cannot be empty")
	}
	if _, ambiguous := plan.ambiguousAliases[key]; ambiguous {
		return fmt.Errorf("kitdb SQL: grouped result name %q is ambiguous", name)
	}
	if existing, found := plan.aliases[key]; found && existing != ref {
		return fmt.Errorf("kitdb SQL: grouped result name %q is ambiguous", name)
	}
	plan.aliases[key] = ref
	return nil
}

func (plan *kitDBRemoteGroupPlan) addGroupAlias(name, ref string) {
	key := strings.ToLower(strings.TrimSpace(name))
	if existing, found := plan.aliases[key]; found && existing != ref {
		delete(plan.aliases, key)
		plan.ambiguousAliases[key] = struct{}{}
		return
	}
	if _, ambiguous := plan.ambiguousAliases[key]; !ambiguous {
		plan.aliases[key] = ref
	}
}

func (plan *kitDBRemoteGroupPlan) addAggregate(
	resolve kitDBRemoteGroupResolver,
	operation string,
	requested string,
) (string, string, error) {
	operation = strings.ToLower(strings.TrimSpace(operation))
	fieldName := requested
	fieldSource := requested
	field := StructFieldDef{}
	var spec *ColumnSpec
	if requested == "*" {
		if operation != "count" {
			return "", "", fmt.Errorf("kitdb SQL: %s(*) is not supported", strings.ToUpper(operation))
		}
	} else {
		resolved, err := resolve(requested)
		if err != nil {
			return "", "", err
		}
		fieldName, fieldSource, spec = resolved.name, resolved.source, resolved.spec
		field = StructFieldDef{Name: resolved.name, Kind: resolved.kind}
	}
	ref := kitSQLAggregateReference(operation, fieldSource)
	if index, found := plan.aggregateIndex[ref]; found {
		return ref, plan.refKinds[ref], plan.validateAggregate(index, operation, fieldName)
	}
	if len(plan.aggregates) >= kitDBRemoteGroupAggregateLimit {
		return "", "", fmt.Errorf("kitdb SQL: grouped SELECT exceeds %d aggregate states", kitDBRemoteGroupAggregateLimit)
	}
	index := len(plan.aggregates)
	plan.aggregateIndex[ref] = index
	plan.aggregates = append(plan.aggregates, kitDBAggregateRequest{
		operation: operation, field: fieldSource, label: fieldName,
	})
	if spec != nil {
		plan.aggregateSpecs[fieldSource] = spec
	}
	plan.aggregateRefs = append(plan.aggregateRefs, ref)
	kind := kitDBRemoteAggregateKind(operation, field)
	plan.refKinds[ref] = kind
	return ref, kind, plan.validateAggregate(index, operation, fieldName)
}

func (plan *kitDBRemoteGroupPlan) validateAggregate(index int, operation, field string) error {
	if index < 0 || index >= len(plan.aggregates) {
		return fmt.Errorf("kitdb SQL: invalid aggregate state")
	}
	switch operation {
	case "count", "sum", "avg", "min", "max":
		return nil
	default:
		return fmt.Errorf("kitdb SQL: unsupported aggregate %s(%s)", strings.ToUpper(operation), field)
	}
}

func (plan *kitDBRemoteGroupPlan) canonicalHaving(
	resolve kitDBRemoteGroupResolver,
	predicate query.Predicate,
) (query.Predicate, error) {
	canonical := predicate
	canonical.Children = make([]query.Predicate, len(predicate.Children))
	if predicate.Kind == query.PredicateCondition {
		operation, field, aggregate := parseKitSQLAggregateReference(predicate.Condition.Column)
		if aggregate {
			ref, _, err := plan.addAggregate(resolve, operation, field)
			if err != nil {
				return query.Predicate{}, err
			}
			canonical.Condition.Column = ref
		} else {
			ref, found := plan.aliases[strings.ToLower(predicate.Condition.Column)]
			if !found {
				field, err := resolve(predicate.Condition.Column)
				if err == nil {
					ref, found = plan.groupRefs[field.source]
				}
				if !found {
					return query.Predicate{}, fmt.Errorf("kitdb SQL: HAVING reference %q is not grouped or aggregated", predicate.Condition.Column)
				}
			}
			canonical.Condition.Column = ref
		}
	}
	for index, child := range predicate.Children {
		resolved, err := plan.canonicalHaving(resolve, child)
		if err != nil {
			return query.Predicate{}, err
		}
		canonical.Children[index] = resolved
	}
	return canonical, nil
}

func executeKitDBRemoteGroups(
	ctx context.Context,
	table *SchemaTable,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	if err := ensureKitDBRemoteTableReady(table); err != nil {
		return kitDBRemoteResult{}, err
	}
	plan, err := prepareKitDBRemoteGroupPlan(table, statement)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	var whereExpression *kitSQLExpression
	if statement.whereExpression != nil {
		whereExpression = statement.whereExpression
		if _, err := resolveKitSQLExpression(whereExpression, func(requested string) (string, string, error) {
			name, field, err := kitDBRemoteField(table.definition, requested)
			return name, field.Kind, err
		}); err != nil {
			return kitDBRemoteResult{}, err
		}
	}
	sourcePlan := kitDBAggregateExecutionPlan(table.builder().ExecutionPlan())
	return executeKitDBRemoteGroupRows(ctx, statement, plan, func(
		visit func(map[string]value.Value) (bool, error),
	) error {
		return table.scanKitDBRows(sourcePlan, func(row kitDBStoredRow) (bool, error) {
			if whereExpression != nil {
				item, err := evaluateKitSQLExpression(whereExpression, func(reference string) (value.Value, error) {
					spec := table.columns[reference]
					if spec == nil {
						return value.Value{}, fmt.Errorf("kitdb SQL: unresolved WHERE field %q", reference)
					}
					stored, found := row.values[reference]
					if !found {
						stored = value.NewNil()
					}
					return coerceRead(spec.kind, stored), nil
				})
				if err != nil {
					return false, fmt.Errorf("kitdb SQL: WHERE expression: %w", err)
				}
				truth, err := kitSQLExpressionTruth(item)
				if err != nil || truth != kitDBTrue {
					return false, err
				}
			}
			return visit(row.values)
		})
	})
}

func executeKitDBRemoteGroupRows(
	ctx context.Context,
	statement kitSQLStatement,
	plan *kitDBRemoteGroupPlan,
	scan func(func(map[string]value.Value) (bool, error)) error,
) (kitDBRemoteResult, error) {
	result := kitDBRemoteResult{columns: plan.columns, rows: [][]value.Value{}}
	if statement.hasLimit && statement.limit == 0 {
		return result, nil
	}

	groups := make(map[string]*kitDBRemoteGroupState)
	ordered := make([]*kitDBRemoteGroupState, 0)
	newGroup := func(key string, fields []value.Value) (*kitDBRemoteGroupState, error) {
		if err := ensureKitDBRemoteGroupCapacity(len(groups)); err != nil {
			return nil, err
		}
		state := &kitDBRemoteGroupState{
			key:        key,
			fields:     append([]value.Value(nil), fields...),
			aggregates: append([]kitDBAggregateState(nil), plan.aggregateTemplate...),
		}
		groups[key] = state
		ordered = append(ordered, state)
		return state, nil
	}
	if len(plan.fields) == 0 {
		if _, err := newGroup("", nil); err != nil {
			return kitDBRemoteResult{}, err
		}
	}

	seen := 0
	err := scan(func(row map[string]value.Value) (bool, error) {
		seen++
		if seen&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		state := groups[""]
		if len(plan.fields) != 0 {
			key := make([]byte, 0, len(plan.fields)*12)
			fields := make([]value.Value, len(plan.fields))
			for index, field := range plan.fields {
				stored, found := row[field.source]
				if !found {
					stored = value.NewNil()
				}
				component, err := kitDBScalarComponent(stored)
				if err != nil {
					return false, fmt.Errorf("kitdb SQL: GROUP BY %s: %w", field.name, err)
				}
				key = append(key, component...)
				fields[index] = coerceRead(field.kind, stored)
			}
			encoded := string(key)
			state = groups[encoded]
			if state == nil {
				var err error
				state, err = newGroup(encoded, fields)
				if err != nil {
					return false, err
				}
			}
		}
		return false, applyKitDBAggregateStates(state.aggregates, row)
	})
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}

	rows := make([]kitDBRemoteGroupedRow, 0, len(ordered))
	for _, group := range ordered {
		values := make(map[string]value.Value, len(plan.fields)+len(plan.aggregates))
		for index, field := range plan.fields {
			values[field.ref] = group.fields[index]
		}
		aggregates := finishKitDBAggregateStates(group.aggregates)
		for index, item := range aggregates {
			values[plan.aggregateRefs[index]] = item
		}
		for index, computed := range plan.computed {
			item, err := evaluateKitSQLExpression(computed.expression, func(reference string) (value.Value, error) {
				item, found := values[reference]
				if !found {
					return value.Value{}, fmt.Errorf("kitdb SQL: unresolved grouped expression reference")
				}
				return item, nil
			})
			if err != nil {
				return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: grouped expression %d: %w", index+1, err)
			}
			values[computed.ref] = item
		}
		truth, err := kitDBEvaluatePredicate(plan.having, func(reference string) (value.Value, string, error) {
			item, found := values[reference]
			if !found {
				return value.Value{}, "", fmt.Errorf("kitdb SQL: unresolved grouped reference")
			}
			item, kind := kitDBRemoteLogicalPredicateValue(item, plan.refKinds[reference])
			return item, kind, nil
		})
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		if truth == kitDBTrue && plan.havingExpressionRef != "" {
			item, found := values[plan.havingExpressionRef]
			if !found {
				return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: unresolved HAVING expression")
			}
			truth, err = kitSQLExpressionTruth(item)
			if err != nil {
				return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: HAVING expression: %w", err)
			}
		}
		if truth == kitDBTrue {
			rows = append(rows, kitDBRemoteGroupedRow{key: group.key, values: values})
		}
	}
	if len(plan.orders) != 0 {
		sort.SliceStable(rows, func(left, right int) bool {
			for _, order := range plan.orders {
				comparison := kitDBCompareValues(rows[left].values[order.ref], rows[right].values[order.ref])
				if comparison == 0 {
					continue
				}
				if strings.EqualFold(order.direction, "desc") {
					return comparison > 0
				}
				return comparison < 0
			}
			return rows[left].key < rows[right].key
		})
	}
	rows = boundKitDBRemoteGroupedRows(rows, statement)
	result.rows = make([][]value.Value, 0, len(rows))
	for _, row := range rows {
		projected := make([]value.Value, len(plan.projections))
		for index, projection := range plan.projections {
			projected[index] = row.values[projection.ref]
		}
		result.rows = append(result.rows, projected)
	}
	return result, nil
}

func kitDBRemoteLogicalPredicateValue(item value.Value, kind string) (value.Value, string) {
	switch kind {
	case "bool":
		return coerceWrite(kind, item), kind
	case "decimal":
		// Aggregates and grouped output expose decimals as logical numbers.
		return item, "float"
	default:
		return item, kind
	}
}

func ensureKitDBRemoteGroupCapacity(current int) error {
	if current >= kitDBRemoteGroupLimit {
		return fmt.Errorf("kitdb SQL: GROUP BY exceeds %d intermediate groups", kitDBRemoteGroupLimit)
	}
	return nil
}

func boundKitDBRemoteGroupedRows(rows []kitDBRemoteGroupedRow, statement kitSQLStatement) []kitDBRemoteGroupedRow {
	offset := statement.offset
	if offset > len(rows) {
		offset = len(rows)
	}
	rows = rows[offset:]
	limit := query.DefaultDBLimit
	if statement.hasLimit {
		limit = statement.limit
	}
	if limit > kitDBRemoteSelectRowLimit {
		limit = kitDBRemoteSelectRowLimit
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func kitDBRemoteGroupableKind(kind string) bool {
	switch kind {
	case "json", "jsonb", "array", "vector", "blob":
		return false
	default:
		return true
	}
}

func kitDBRemoteAggregateName(operation, field string) string {
	return strings.ToLower(operation) + "(" + field + ")"
}

func kitDBExplainGroupDetail(
	table string,
	access kitDBAccessPlan,
	source query.ExecutionPlan,
	statement kitSQLStatement,
	plan *kitDBRemoteGroupPlan,
) string {
	detail := kitDBExplainAccessDetail(table, access, source)
	return kitDBExplainGroupStages(detail, statement, plan)
}

func kitDBExplainGroupStages(
	detail string,
	statement kitSQLStatement,
	plan *kitDBRemoteGroupPlan,
) string {
	if statement.distinct {
		fields := make([]string, len(plan.fields))
		for index, field := range plan.fields {
			fields[index] = field.name
		}
		detail += "; HASH DISTINCT " + strings.Join(fields, ",")
	} else if len(plan.fields) == 0 {
		detail += "; STREAM AGGREGATE"
	} else {
		fields := make([]string, len(plan.fields))
		for index, field := range plan.fields {
			fields[index] = field.name
		}
		detail += "; HASH GROUP BY " + strings.Join(fields, ",")
	}
	if plan.having != nil || plan.havingExpressionRef != "" {
		detail += "; HAVING"
	}
	if len(plan.computed) != 0 {
		detail += "; PROJECT EXPRESSIONS"
	}
	if len(plan.orders) != 0 {
		detail += "; TEMP SORT"
	}
	limit := query.DefaultDBLimit
	if statement.hasLimit {
		limit = statement.limit
	}
	if limit > kitDBRemoteSelectRowLimit {
		limit = kitDBRemoteSelectRowLimit
	}
	detail += fmt.Sprintf("; LIMIT %d", limit)
	if statement.offset > 0 {
		detail += fmt.Sprintf(" OFFSET %d", statement.offset)
	}
	return detail
}
