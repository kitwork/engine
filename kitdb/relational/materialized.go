package relational

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const (
	maximumMaterializedColumns = 128
	maximumMaterializedBytes   = 32 << 20
)

type materializedRelation struct {
	schema kitdbsql.Schema
	rows   []map[string]any
}

type queryScope struct {
	parent    *queryScope
	relations map[string]*materializedRelation
	reserved  map[string]string
}

type materializationBudget struct {
	maximumRows  int
	maximumBytes int
	rows         int
	bytes        int
	workingBytes int
	peakBytes    int
	directRows   int
}

type materializationWorkingSet struct {
	budget *materializationBudget
	bytes  int
}

func newMaterializationBudget(maximumRows int) *materializationBudget {
	return &materializationBudget{maximumRows: maximumRows, maximumBytes: maximumMaterializedBytes}
}

func newQueryScope(parent *queryScope, commonTables []kitdbsql.CommonTableExpression) (*queryScope, error) {
	scope := &queryScope{
		parent: parent, relations: make(map[string]*materializedRelation, len(commonTables)),
		reserved: make(map[string]string, len(commonTables)),
	}
	for _, commonTable := range commonTables {
		key := strings.ToLower(commonTable.Name)
		if _, duplicate := scope.reserved[key]; duplicate {
			return nil, fmt.Errorf("kitdb SQL: WITH repeats common table %q", commonTable.Name)
		}
		scope.reserved[key] = commonTable.Name
	}
	return scope, nil
}

func (scope *queryScope) publish(name string, relation *materializedRelation) {
	key := strings.ToLower(name)
	delete(scope.reserved, key)
	scope.relations[key] = relation
}

func (scope *queryScope) resolve(name string) (*materializedRelation, bool, error) {
	key := strings.ToLower(name)
	for current := scope; current != nil; current = current.parent {
		if relation := current.relations[key]; relation != nil {
			return relation, true, nil
		}
		if reserved, blocked := current.reserved[key]; blocked {
			return nil, false, fmt.Errorf(
				"kitdb SQL: common table %q is a recursive or forward reference", reserved,
			)
		}
	}
	return nil, false, nil
}

func (budget *materializationBudget) consumeColumns(columns []Column) error {
	if budget == nil {
		return fmt.Errorf("kitdb SQL: materialization budget is unavailable")
	}
	bytes := 0
	for _, column := range columns {
		bytes += 64 + len(column.Name) + len(column.Kind)
	}
	if err := budget.admitRetained(bytes); err != nil {
		return err
	}
	return nil
}

func (budget *materializationBudget) admitRetained(bytes int) error {
	if budget == nil {
		return fmt.Errorf("kitdb SQL: materialization budget is unavailable")
	}
	if bytes < 0 || budget.bytes+budget.workingBytes > budget.maximumBytes ||
		bytes > budget.maximumBytes-budget.bytes-budget.workingBytes {
		return fmt.Errorf(
			"kitdb SQL: WITH and derived tables exceed the %d-byte materialization budget",
			budget.maximumBytes,
		)
	}
	budget.bytes += bytes
	budget.observePeak()
	return nil
}

func (budget *materializationBudget) consumeRow(row []any, direct bool) error {
	if budget == nil {
		return fmt.Errorf("kitdb SQL: materialization budget is unavailable")
	}
	if budget.rows >= budget.maximumRows {
		return fmt.Errorf(
			"kitdb SQL: WITH and derived tables exceed the bounded %d-row materialization budget",
			budget.maximumRows,
		)
	}
	// Admission includes the active buffered executor, if any, and happens
	// before the retained row map is allocated.
	bytes := materializedRowBytes(row)
	if err := budget.admitRetained(bytes); err != nil {
		return err
	}
	budget.rows++
	if direct {
		budget.directRows++
	}
	return nil
}

func (budget *materializationBudget) observePeak() {
	if budget == nil {
		return
	}
	if current := budget.bytes + budget.workingBytes; current > budget.peakBytes {
		budget.peakBytes = current
	}
}

func newMaterializationWorkingSet(budget *materializationBudget) *materializationWorkingSet {
	return &materializationWorkingSet{budget: budget}
}

func (working *materializationWorkingSet) child() *materializationWorkingSet {
	if working == nil {
		return nil
	}
	return newMaterializationWorkingSet(working.budget)
}

func (working *materializationWorkingSet) reserve(bytes int, operation string) error {
	if working == nil {
		return nil
	}
	if working.budget == nil {
		return fmt.Errorf("kitdb SQL: materialization budget is unavailable")
	}
	budget := working.budget
	if bytes < 0 || budget.bytes+budget.workingBytes > budget.maximumBytes ||
		bytes > budget.maximumBytes-budget.bytes-budget.workingBytes {
		return fmt.Errorf(
			"kitdb SQL: WITH and derived tables exceed the %d-byte query memory budget while buffering %s",
			budget.maximumBytes, operation,
		)
	}
	working.bytes += bytes
	budget.workingBytes += bytes
	budget.observePeak()
	return nil
}

func (working *materializationWorkingSet) release(bytes int) {
	if working == nil || working.budget == nil || bytes <= 0 {
		return
	}
	if bytes > working.bytes {
		bytes = working.bytes
	}
	working.bytes -= bytes
	working.budget.workingBytes -= bytes
}

func (working *materializationWorkingSet) replace(previous, next int, operation string) error {
	if next > previous {
		return working.reserve(next-previous, operation)
	}
	working.release(previous - next)
	return nil
}

func (working *materializationWorkingSet) close() {
	if working == nil {
		return
	}
	working.release(working.bytes)
}

func (working *materializationWorkingSet) reserveRow(row []any, operation string) error {
	if working == nil {
		return nil
	}
	return working.reserve(materializedRowBytes(row), operation)
}

func (working *materializationWorkingSet) reserveMap(row map[string]any, operation string) (int, error) {
	if working == nil {
		return 0, nil
	}
	bytes := materializedMapBytes(row)
	return bytes, working.reserve(bytes, operation)
}

func materializedRowBytes(row []any) int {
	bytes := 64 + len(row)*32
	nodes := 0
	for _, value := range row {
		bytes += materializedValueBytesBounded(value, 0, &nodes)
		if bytes > maximumMaterializedBytes {
			return maximumMaterializedBytes + 1
		}
	}
	return bytes
}

// A projected row normally shares its values with an already-accounted source
// row. Count only the slice and interface references in that case.
func materializedReferenceRowBytes(row []any) int {
	return 32 + len(row)*16
}

func materializedMapReferenceBytes() int {
	return 16
}

func materializedMapBytes(row map[string]any) int {
	bytes := 64 + len(row)*48
	nodes := 0
	for key, value := range row {
		bytes += len(key) + materializedValueBytesBounded(value, 0, &nodes)
		if bytes > maximumMaterializedBytes {
			return maximumMaterializedBytes + 1
		}
	}
	return bytes
}

func (budget *materializationBudget) consume(result Result) error {
	if err := budget.consumeColumns(result.Columns); err != nil {
		return err
	}
	for _, row := range result.Rows {
		if err := budget.consumeRow(row, false); err != nil {
			return err
		}
	}
	return nil
}

func materializedValueBytesBounded(value any, depth int, nodes *int) int {
	if value == nil {
		return 1
	}
	*nodes = *nodes + 1
	if depth >= valueDepthLimit || *nodes > valueNodeLimit {
		return maximumMaterializedBytes + 1
	}
	switch typed := value.(type) {
	case string:
		return len(typed)
	case []byte:
		return len(typed)
	case time.Time:
		return 24
	case json.Number:
		return len(typed)
	case []any:
		total := 24 + len(typed)*16
		for _, item := range typed {
			total += materializedValueBytesBounded(item, depth+1, nodes)
			if total > maximumMaterializedBytes {
				return maximumMaterializedBytes + 1
			}
		}
		return total
	case map[string]any:
		total := 48
		for key, item := range typed {
			total += len(key) + 16 + materializedValueBytesBounded(item, depth+1, nodes)
			if total > maximumMaterializedBytes {
				return maximumMaterializedBytes + 1
			}
		}
		return total
	default:
		return 16
	}
}

func relationFromColumns(name string, columns []Column, aliases []string) (*materializedRelation, error) {
	if len(columns) == 0 {
		return nil, fmt.Errorf("kitdb SQL: materialized relation %q has no columns", name)
	}
	if len(columns) > maximumMaterializedColumns {
		return nil, fmt.Errorf(
			"kitdb SQL: materialized relation %q exceeds %d columns", name, maximumMaterializedColumns,
		)
	}
	if len(aliases) != 0 && len(aliases) != len(columns) {
		return nil, fmt.Errorf(
			"kitdb SQL: common table %q declares %d columns but returns %d",
			name, len(aliases), len(columns),
		)
	}
	fields := make([]kitdbsql.Field, len(columns))
	seen := make(map[string]struct{}, len(columns))
	for index, column := range columns {
		columnName := column.Name
		if len(aliases) != 0 {
			columnName = aliases[index]
		}
		if columnName == "" {
			return nil, fmt.Errorf("kitdb SQL: materialized relation %q has an unnamed column", name)
		}
		key := strings.ToLower(columnName)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf(
				"kitdb SQL: materialized relation %q has ambiguous output field %q; provide unique aliases",
				name, columnName,
			)
		}
		seen[key] = struct{}{}
		fields[index] = kitdbsql.Field{
			ID: fmt.Sprintf("materialized-field-%d", index+1), Tag: uint32(index + 1),
			Name: columnName, Position: index + 1, Kind: column.Kind,
			Precision: column.Precision, Scale: column.Scale,
			TimePrecision: column.TimePrecision, TextLength: column.TextLength, ExactUUID: column.ExactUUID,
		}
	}
	return &materializedRelation{schema: kitdbsql.Schema{
		Version: kitdbsql.CurrentSchemaVersion, ID: "materialized:" + strings.ToLower(name),
		Name: name, NextFieldTag: uint32(len(fields) + 1), Fields: fields,
	}}, nil
}

func relationFromResult(
	name string,
	result Result,
	aliases []string,
	budget *materializationBudget,
) (*materializedRelation, error) {
	builder, err := newMaterializedRelationBuilder(name, result.Columns, aliases, budget, false)
	if err != nil {
		return nil, err
	}
	for _, row := range result.Rows {
		if err := builder.append(row); err != nil {
			return nil, err
		}
	}
	return builder.relation, nil
}

type materializedRelationBuilder struct {
	relation *materializedRelation
	budget   *materializationBudget
	direct   bool
}

func newMaterializedRelationBuilder(
	name string,
	columns []Column,
	aliases []string,
	budget *materializationBudget,
	direct bool,
) (*materializedRelationBuilder, error) {
	relation, err := relationFromColumns(name, columns, aliases)
	if err != nil {
		return nil, err
	}
	if err := budget.consumeColumns(columns); err != nil {
		return nil, err
	}
	return &materializedRelationBuilder{relation: relation, budget: budget, direct: direct}, nil
}

func (builder *materializedRelationBuilder) append(row []any) error {
	if builder == nil || builder.relation == nil {
		return fmt.Errorf("kitdb SQL: materialized relation builder is unavailable")
	}
	if len(row) != len(builder.relation.schema.Fields) {
		return fmt.Errorf(
			"kitdb SQL: materialized relation %q row %d has %d values; expected %d",
			builder.relation.schema.Name, len(builder.relation.rows)+1, len(row), len(builder.relation.schema.Fields),
		)
	}
	// Admission happens before the row map is allocated and retained.
	if err := builder.budget.consumeRow(row, builder.direct); err != nil {
		return err
	}
	values := make(map[string]any, len(row))
	for columnIndex, field := range builder.relation.schema.Fields {
		values[field.Name] = row[columnIndex]
	}
	builder.relation.rows = append(builder.relation.rows, values)
	return nil
}

func describeSelectFromCatalog(
	catalog kitdbengine.CatalogSnapshot,
	plan *kitdbsql.SelectStatement,
	scope *queryScope,
) ([]Column, error) {
	if plan == nil {
		return nil, fmt.Errorf("kitdb SQL: invalid SELECT plan")
	}
	if len(plan.CommonTables) != 0 {
		child, err := newQueryScope(scope, plan.CommonTables)
		if err != nil {
			return nil, err
		}
		for _, commonTable := range plan.CommonTables {
			columns, err := describeSelectFromCatalog(catalog, commonTable.Select, child)
			if err != nil {
				return nil, fmt.Errorf("kitdb SQL: WITH %q: %w", commonTable.Name, err)
			}
			relation, err := relationFromColumns(commonTable.Name, columns, commonTable.Columns)
			if err != nil {
				return nil, err
			}
			child.publish(commonTable.Name, relation)
		}
		main := *plan
		main.CommonTables = nil
		return describeSelectFromCatalog(catalog, &main, child)
	}
	if len(plan.SetOperations) != 0 {
		return describeCompoundSelect(plan, func(branch *kitdbsql.SelectStatement) ([]Column, error) {
			return describeSelectFromCatalog(catalog, branch, scope)
		})
	}
	if plan.Source != nil {
		if len(plan.Joins) != 0 {
			return nil, fmt.Errorf("kitdb SQL: JOIN from a derived table is not supported yet")
		}
		columns, err := describeSelectFromCatalog(catalog, plan.Source, scope)
		if err != nil {
			return nil, fmt.Errorf("kitdb SQL: derived table %q: %w", plan.TableAlias, err)
		}
		relation, err := relationFromColumns(plan.TableAlias, columns, nil)
		if err != nil {
			return nil, err
		}
		return describeMaterializedSelect(relation.schema, plan)
	}
	if plan.Table == "" {
		return describeScalarExpressionSelect(kitdbsql.Schema{}, plan)
	}
	if relation, found, err := scope.resolve(plan.Table); err != nil {
		return nil, err
	} else if found {
		if len(plan.Joins) != 0 {
			return nil, fmt.Errorf("kitdb SQL: JOIN from common table %q is not supported yet", plan.Table)
		}
		return describeMaterializedSelect(relation.schema, plan)
	}
	if len(plan.Joins) != 0 {
		for _, join := range plan.Joins {
			if _, found, err := scope.resolve(join.Table); err != nil {
				return nil, err
			} else if found {
				return nil, fmt.Errorf("kitdb SQL: JOIN to common table %q is not supported yet", join.Table)
			}
		}
		return describeJoinSelect(catalog, plan)
	}
	schema, err := schemaFromCatalog(catalog, plan.Table)
	if err != nil {
		return nil, err
	}
	return describeSelectAgainstSchema(schema, plan)
}

func describeMaterializedSelect(schema kitdbsql.Schema, plan *kitdbsql.SelectStatement) ([]Column, error) {
	if plan.Search != nil {
		return nil, fmt.Errorf("kitdb SQL: SEARCH cannot read a materialized relation")
	}
	return describeSelectAgainstSchema(schema, plan)
}

func describeSelectAgainstSchema(schema kitdbsql.Schema, plan *kitdbsql.SelectStatement) ([]Column, error) {
	if plan.Search != nil {
		return describeSearchSelect(schema, plan)
	}
	if selectHasScalarExpressions(plan) {
		return describeScalarExpressionSelect(schema, plan)
	}
	for _, condition := range plan.Conditions {
		if _, _, found := schema.FieldByName(unqualifiedColumn(condition.Column)); !found {
			return nil, fmt.Errorf("kitdb SQL: table %q has no field %q", schema.Name, condition.Column)
		}
	}
	if _, err := bindOrders(schema, plan.Order); err != nil && !selectHasAggregates(plan) {
		return nil, err
	}
	if selectHasAggregates(plan) {
		return describeAggregateSelect(schema, plan)
	}
	columns, _, err := bindProjection(schema, plan.Projection)
	return columns, err
}

func (transaction *Transaction) materializeSelectInScope(
	ctx context.Context,
	name string,
	aliases []string,
	plan *kitdbsql.SelectStatement,
	parameters []any,
	observe bool,
	scope *queryScope,
	budget *materializationBudget,
) (*materializedRelation, error) {
	if plan == nil {
		return nil, fmt.Errorf("kitdb SQL: invalid SELECT plan")
	}
	if plan.HasLimit && plan.Limit > transaction.engine.maximumResultRows {
		return nil, fmt.Errorf(
			"kitdb SQL: LIMIT %d exceeds this server's result limit of %d",
			plan.Limit, transaction.engine.maximumResultRows,
		)
	}
	columns, err := describeSelectFromCatalog(transaction.catalog, plan, scope)
	if err != nil {
		return nil, err
	}
	if selectOutputCanMaterializeDirectly(plan) {
		builder, err := newMaterializedRelationBuilder(name, columns, aliases, budget, true)
		if err != nil {
			return nil, err
		}
		if err := transaction.materializeDirectSelect(
			ctx, plan, parameters, observe, scope, budget, builder,
		); err != nil {
			return nil, err
		}
		return builder.relation, nil
	}

	// Validate relation names and aliases before running a buffered shape. Its
	// rows still pass through the same incremental admission gate when copied
	// into the retained relation.
	if _, err := relationFromColumns(name, columns, aliases); err != nil {
		return nil, err
	}
	working := newMaterializationWorkingSet(budget)
	defer working.close()
	if err := working.reserve(materializedColumnsBytes(columns), "result columns"); err != nil {
		return nil, err
	}
	result, err := transaction.executeSelectInScope(ctx, plan, parameters, observe, scope, budget, working)
	if err != nil {
		return nil, err
	}
	return relationFromResult(name, result, aliases, budget)
}

func selectOutputCanMaterializeDirectly(plan *kitdbsql.SelectStatement) bool {
	if plan == nil || len(plan.SetOperations) != 0 || len(plan.Joins) != 0 || plan.Search != nil ||
		selectHasAggregates(plan) || len(plan.GroupBy) != 0 || plan.Having != nil ||
		len(plan.Order) != 0 || plan.Distinct || plan.HasAfter {
		return false
	}
	return true
}

func materializedColumnsBytes(columns []Column) int {
	bytes := 0
	for _, column := range columns {
		bytes += 64 + len(column.Name) + len(column.Kind)
	}
	return bytes
}

func (transaction *Transaction) materializeDirectSelect(
	ctx context.Context,
	plan *kitdbsql.SelectStatement,
	parameters []any,
	observe bool,
	scope *queryScope,
	budget *materializationBudget,
	builder *materializedRelationBuilder,
) error {
	if len(plan.CommonTables) != 0 {
		child, err := newQueryScope(scope, plan.CommonTables)
		if err != nil {
			return err
		}
		for _, commonTable := range plan.CommonTables {
			relation, err := transaction.materializeSelectInScope(
				ctx, commonTable.Name, commonTable.Columns, commonTable.Select,
				parameters, observe, child, budget,
			)
			if err != nil {
				return fmt.Errorf("kitdb SQL: WITH %q: %w", commonTable.Name, err)
			}
			child.publish(commonTable.Name, relation)
		}
		main := *plan
		main.CommonTables = nil
		return transaction.materializeDirectSelect(
			ctx, &main, parameters, observe, child, budget, builder,
		)
	}
	if plan.Source != nil {
		relation, err := transaction.materializeSelectInScope(
			ctx, plan.TableAlias, nil, plan.Source, parameters, observe, scope, budget,
		)
		if err != nil {
			return fmt.Errorf("kitdb SQL: derived table %q: %w", plan.TableAlias, err)
		}
		return transaction.materializeRelationRows(ctx, relation, plan, parameters, builder)
	}
	if plan.Table == "" {
		result, err := executeConstantSelect(plan, parameters)
		if err != nil {
			return err
		}
		for _, row := range result.Rows {
			if err := builder.append(row); err != nil {
				return err
			}
		}
		return nil
	}
	if relation, found, err := scope.resolve(plan.Table); err != nil {
		return err
	} else if found {
		return transaction.materializeRelationRows(ctx, relation, plan, parameters, builder)
	}
	return transaction.materializePhysicalRows(ctx, plan, parameters, builder)
}

type directMaterialization struct {
	ctx        context.Context
	plan       *kitdbsql.SelectStatement
	conditions []boundCondition
	predicate  *boundPredicate
	project    func(map[string]any) ([]any, error)
	builder    *materializedRelationBuilder
	matched    int
	emitted    int
	scanned    int
}

func (stream *directMaterialization) accept(row map[string]any) (bool, error) {
	stream.scanned++
	if stream.scanned&255 == 0 {
		if err := stream.ctx.Err(); err != nil {
			return false, err
		}
	}
	if !matchesAll(row, stream.conditions) {
		return false, nil
	}
	accepted, err := predicateMatches(row, stream.predicate)
	if err != nil || !accepted {
		return false, err
	}
	stream.matched++
	if stream.matched <= stream.plan.Offset {
		return false, nil
	}
	if stream.plan.HasLimit && stream.emitted >= stream.plan.Limit {
		return true, nil
	}
	projected, err := stream.project(row)
	if err != nil {
		return false, err
	}
	if err := stream.builder.append(projected); err != nil {
		return false, err
	}
	stream.emitted++
	return stream.plan.HasLimit && stream.emitted >= stream.plan.Limit, nil
}

func newDirectMaterialization(
	ctx context.Context,
	schema kitdbsql.Schema,
	plan *kitdbsql.SelectStatement,
	parameters []any,
	builder *materializedRelationBuilder,
) (*directMaterialization, error) {
	conditions, err := bindConditions(schema, plan.Conditions, parameters)
	if err != nil {
		return nil, err
	}
	predicate, err := bindPredicate(schema, plan.Predicate, parameters)
	if err != nil {
		return nil, err
	}
	if err := builder.validateRowLimit(plan); err != nil {
		return nil, err
	}
	stream := &directMaterialization{
		ctx: ctx, plan: plan, conditions: conditions, predicate: predicate, builder: builder,
	}
	if selectHasScalarExpressions(plan) {
		projections, err := bindScalarProjections(schema, plan.Projection, parameters)
		if err != nil {
			return nil, err
		}
		stream.project = func(row map[string]any) ([]any, error) {
			projected := make([]any, len(projections))
			for index, projection := range projections {
				value, err := evaluateBoundPredicate(row, projection.expression)
				if err != nil {
					return nil, fmt.Errorf("kitdb SQL: projection %d: %w", index+1, err)
				}
				value, err = coerceScalarExpressionValue(projection.column.Kind, value)
				if err != nil {
					return nil, fmt.Errorf("kitdb SQL: projection %d: %w", index+1, err)
				}
				projected[index] = value
			}
			return projected, nil
		}
		return stream, nil
	}
	_, names, err := bindProjection(schema, plan.Projection)
	if err != nil {
		return nil, err
	}
	stream.project = func(row map[string]any) ([]any, error) {
		return projectRow(schema, row, names), nil
	}
	return stream, nil
}

func (builder *materializedRelationBuilder) validateRowLimit(plan *kitdbsql.SelectStatement) error {
	if builder == nil || builder.budget == nil {
		return fmt.Errorf("kitdb SQL: materialization budget is unavailable")
	}
	if plan.HasLimit && plan.Limit > builder.budget.maximumRows {
		return fmt.Errorf(
			"kitdb SQL: LIMIT %d exceeds this server's result limit of %d",
			plan.Limit, builder.budget.maximumRows,
		)
	}
	return nil
}

func (transaction *Transaction) materializePhysicalRows(
	ctx context.Context,
	plan *kitdbsql.SelectStatement,
	parameters []any,
	builder *materializedRelationBuilder,
) error {
	schema, err := transaction.schema(plan.Table)
	if err != nil {
		return err
	}
	stream, err := newDirectMaterialization(ctx, schema, plan, parameters, builder)
	if err != nil {
		return err
	}
	if plan.HasLimit && plan.Limit == 0 {
		return nil
	}
	generation, err := activeRowGeneration(transaction, schema)
	if err != nil {
		return err
	}
	access, err := transaction.planRowAccess(schema, generation, stream.conditions, nil)
	if err != nil {
		return err
	}
	return transaction.walkAccessRowsObserved(access, nil, func(_, encoded []byte) (bool, error) {
		decoded, err := decodeRow(schema, encoded)
		if err != nil {
			return false, err
		}
		return stream.accept(decoded.values)
	})
}

func (transaction *Transaction) materializeRelationRows(
	ctx context.Context,
	relation *materializedRelation,
	plan *kitdbsql.SelectStatement,
	parameters []any,
	builder *materializedRelationBuilder,
) error {
	if relation == nil {
		return fmt.Errorf("kitdb SQL: materialized relation is unavailable")
	}
	stream, err := newDirectMaterialization(ctx, relation.schema, plan, parameters, builder)
	if err != nil {
		return err
	}
	if plan.HasLimit && plan.Limit == 0 {
		return nil
	}
	for _, row := range relation.rows {
		stop, err := stream.accept(row)
		if err != nil {
			return err
		}
		if stop {
			break
		}
	}
	return ctx.Err()
}

func (transaction *Transaction) executeSelectInScope(
	ctx context.Context,
	plan *kitdbsql.SelectStatement,
	parameters []any,
	observe bool,
	scope *queryScope,
	budget *materializationBudget,
	working *materializationWorkingSet,
) (Result, error) {
	if plan == nil {
		return Result{}, fmt.Errorf("kitdb SQL: invalid SELECT plan")
	}
	if plan.HasLimit && plan.Limit > transaction.engine.maximumResultRows {
		return Result{}, fmt.Errorf(
			"kitdb SQL: LIMIT %d exceeds this server's result limit of %d",
			plan.Limit, transaction.engine.maximumResultRows,
		)
	}
	if len(plan.CommonTables) != 0 {
		child, err := newQueryScope(scope, plan.CommonTables)
		if err != nil {
			return Result{}, err
		}
		for _, commonTable := range plan.CommonTables {
			relation, err := transaction.materializeSelectInScope(
				ctx, commonTable.Name, commonTable.Columns, commonTable.Select,
				parameters, observe, child, budget,
			)
			if err != nil {
				return Result{}, fmt.Errorf("kitdb SQL: WITH %q: %w", commonTable.Name, err)
			}
			child.publish(commonTable.Name, relation)
		}
		main := *plan
		main.CommonTables = nil
		return transaction.executeSelectInScope(ctx, &main, parameters, observe, child, budget, working)
	}
	if len(plan.SetOperations) != 0 {
		result, err := transaction.executeCompoundSelectInScope(ctx, plan, parameters, observe, scope, budget, working)
		return accountMaterializationResult(result, working, "set-operation result", err)
	}
	if plan.Source != nil {
		if len(plan.Joins) != 0 {
			return Result{}, fmt.Errorf("kitdb SQL: JOIN from a derived table is not supported yet")
		}
		relation, err := transaction.materializeSelectInScope(
			ctx, plan.TableAlias, nil, plan.Source, parameters, observe, scope, budget,
		)
		if err != nil {
			return Result{}, fmt.Errorf("kitdb SQL: derived table %q: %w", plan.TableAlias, err)
		}
		result, err := transaction.executeMaterializedSelect(ctx, relation, plan, parameters, observe, working)
		return accountMaterializationResult(result, working, "derived-table result", err)
	}
	if plan.Table == "" {
		result, err := executeConstantSelect(plan, parameters)
		return accountMaterializationResult(result, working, "constant result", err)
	}
	if relation, found, err := scope.resolve(plan.Table); err != nil {
		return Result{}, err
	} else if found {
		if len(plan.Joins) != 0 {
			return Result{}, fmt.Errorf("kitdb SQL: JOIN from common table %q is not supported yet", plan.Table)
		}
		result, err := transaction.executeMaterializedSelect(ctx, relation, plan, parameters, observe, working)
		return accountMaterializationResult(result, working, "common-table result", err)
	}
	if len(plan.Joins) != 0 {
		for _, join := range plan.Joins {
			if _, found, err := scope.resolve(join.Table); err != nil {
				return Result{}, err
			} else if found {
				return Result{}, fmt.Errorf("kitdb SQL: JOIN to common table %q is not supported yet", join.Table)
			}
		}
	}
	if plan.Search != nil {
		return Result{}, fmt.Errorf("kitdb SQL: SEARCH is not available inside an explicit transaction; run it in autocommit")
	}
	result, err := transaction.executeSelect(ctx, plan, parameters, observe, working)
	return accountMaterializationResult(result, working, "SELECT result", err)
}

func accountMaterializationResult(
	result Result,
	working *materializationWorkingSet,
	operation string,
	err error,
) (Result, error) {
	if err != nil || working == nil || result.materializationWorkingAccounted {
		return result, err
	}
	for _, row := range result.Rows {
		if err := working.reserveRow(row, operation); err != nil {
			return Result{}, err
		}
	}
	result.materializationWorkingAccounted = true
	return result, nil
}

func (transaction *Transaction) executeMaterializedSelect(
	ctx context.Context,
	relation *materializedRelation,
	plan *kitdbsql.SelectStatement,
	parameters []any,
	observe bool,
	working *materializationWorkingSet,
) (Result, error) {
	if relation == nil {
		return Result{}, fmt.Errorf("kitdb SQL: materialized relation is unavailable")
	}
	if selectHasAggregates(plan) {
		return transaction.executeMaterializedAggregate(ctx, relation, plan, parameters, observe, working)
	}
	if selectHasScalarExpressions(plan) {
		return transaction.executeMaterializedExpressions(ctx, relation, plan, parameters, observe, working)
	}
	return transaction.executeMaterializedRows(ctx, relation, plan, parameters, observe, working)
}

func (transaction *Transaction) executeMaterializedRows(
	ctx context.Context,
	relation *materializedRelation,
	plan *kitdbsql.SelectStatement,
	parameters []any,
	observe bool,
	working *materializationWorkingSet,
) (Result, error) {
	schema := relation.schema
	conditions, err := bindConditions(schema, plan.Conditions, parameters)
	if err != nil {
		return Result{}, err
	}
	predicate, err := bindPredicate(schema, plan.Predicate, parameters)
	if err != nil {
		return Result{}, err
	}
	orders, err := bindOrders(schema, plan.Order)
	if err != nil {
		return Result{}, err
	}
	columns, names, err := bindProjection(schema, plan.Projection)
	if err != nil {
		return Result{}, err
	}
	limit, err := transaction.boundedSelectLimit(plan)
	if err != nil {
		return Result{}, err
	}
	stats := materializedExecutionStats(observe, "materialized-scan")
	matched := make([]map[string]any, 0, min(len(relation.rows), 256))
	for index, row := range relation.rows {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
		}
		if stats != nil {
			stats.RowsScanned++
		}
		if !matchesAll(row, conditions) {
			continue
		}
		accepted, err := predicateMatches(row, predicate)
		if err != nil {
			return Result{}, err
		}
		if !accepted {
			continue
		}
		if stats != nil {
			stats.RowsMatched++
		}
		if err := working.reserve(materializedMapReferenceBytes(), "materialized source row references"); err != nil {
			return Result{}, err
		}
		matched = append(matched, row)
	}
	if len(orders) != 0 {
		sort.SliceStable(matched, func(left, right int) bool {
			for _, order := range orders {
				comparison := compareFieldValues(order.field, matched[left][order.field.Name], matched[right][order.field.Name])
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
	rows := make([][]any, 0, min(len(matched), limit))
	seen := make(map[string]struct{})
	for _, row := range matched {
		projected := projectRow(schema, row, names)
		if plan.Distinct {
			identity, err := json.Marshal(projected)
			if err != nil {
				return Result{}, fmt.Errorf("kitdb SQL: encode DISTINCT row: %w", err)
			}
			key := string(identity)
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			if err := working.reserve(len(key)+64, "DISTINCT identities"); err != nil {
				return Result{}, err
			}
			seen[key] = struct{}{}
		}
		if err := working.reserve(materializedReferenceRowBytes(projected), "materialized result rows"); err != nil {
			return Result{}, err
		}
		rows = append(rows, projected)
	}
	rows = sliceMaterializedRows(rows, plan.Offset, limit)
	return Result{
		Columns: columns, Rows: rows, CommandTag: fmt.Sprintf("SELECT %d", len(rows)), Execution: stats,

		materializationWorkingAccounted: working != nil,
	}, nil
}

func (transaction *Transaction) executeMaterializedExpressions(
	ctx context.Context,
	relation *materializedRelation,
	plan *kitdbsql.SelectStatement,
	parameters []any,
	observe bool,
	working *materializationWorkingSet,
) (Result, error) {
	schema := relation.schema
	conditions, err := bindConditions(schema, plan.Conditions, parameters)
	if err != nil {
		return Result{}, err
	}
	predicate, err := bindPredicate(schema, plan.Predicate, parameters)
	if err != nil {
		return Result{}, err
	}
	projections, err := bindScalarProjections(schema, plan.Projection, parameters)
	if err != nil {
		return Result{}, err
	}
	orders, _, _, err := bindScalarOrders(schema, plan.Order, projections, parameters)
	if err != nil {
		return Result{}, err
	}
	limit, err := transaction.boundedSelectLimit(plan)
	if err != nil {
		return Result{}, err
	}
	columns := make([]Column, len(projections))
	for index := range projections {
		columns[index] = projections[index].column
	}
	stats := materializedExecutionStats(observe, "materialized-scan")
	rows := make([]scalarExpressionRow, 0, min(len(relation.rows), 256))
	distinct := make(map[string]struct{})
	for rowIndex, source := range relation.rows {
		if rowIndex&255 == 0 {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
		}
		if stats != nil {
			stats.RowsScanned++
		}
		if !matchesAll(source, conditions) {
			continue
		}
		accepted, err := predicateMatches(source, predicate)
		if err != nil {
			return Result{}, err
		}
		if !accepted {
			continue
		}
		if stats != nil {
			stats.RowsMatched++
		}
		projected := make([]any, len(projections))
		for index, projection := range projections {
			value, err := evaluateBoundPredicate(source, projection.expression)
			if err != nil {
				return Result{}, fmt.Errorf("kitdb SQL: projection %d: %w", index+1, err)
			}
			value, err = coerceScalarExpressionValue(projection.column.Kind, value)
			if err != nil {
				return Result{}, fmt.Errorf("kitdb SQL: projection %d: %w", index+1, err)
			}
			projected[index] = value
		}
		if plan.Distinct {
			identity, err := json.Marshal(projected)
			if err != nil {
				return Result{}, fmt.Errorf("kitdb SQL: encode DISTINCT expression row: %w", err)
			}
			key := string(identity)
			if _, duplicate := distinct[key]; duplicate {
				continue
			}
			if err := working.reserve(len(key)+64, "DISTINCT expression identities"); err != nil {
				return Result{}, err
			}
			distinct[key] = struct{}{}
		}
		row := scalarExpressionRow{values: projected}
		if len(orders) != 0 {
			row.ordered = make([]any, len(orders))
			for index, order := range orders {
				if order.projection >= 0 {
					row.ordered[index] = projected[order.projection]
					continue
				}
				value, err := evaluateBoundPredicate(source, order.expression)
				if err != nil {
					return Result{}, fmt.Errorf("kitdb SQL: ORDER BY expression %d: %w", index+1, err)
				}
				value, err = coerceScalarExpressionValue(order.kind, value)
				if err != nil {
					return Result{}, fmt.Errorf("kitdb SQL: ORDER BY expression %d: %w", index+1, err)
				}
				row.ordered[index] = value
			}
		}
		if working != nil {
			retainedBytes := materializedRowBytes(row.values)
			if len(row.ordered) != 0 {
				retainedBytes += materializedReferenceRowBytes(row.ordered)
			}
			if err := working.reserve(retainedBytes, "materialized ORDER BY/DISTINCT expression rows"); err != nil {
				return Result{}, err
			}
		}
		rows = append(rows, row)
	}
	if len(orders) != 0 {
		sort.SliceStable(rows, func(left, right int) bool {
			for index, order := range orders {
				comparison := compareScalarOrderValues(order, rows[left].ordered[index], rows[right].ordered[index])
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
	end := min(start+limit, len(rows))
	result := Result{Columns: columns, Rows: make([][]any, end-start), Execution: stats}
	for index, row := range rows[start:end] {
		result.Rows[index] = row.values
	}
	result.CommandTag = fmt.Sprintf("SELECT %d", len(result.Rows))
	result.materializationWorkingAccounted = working != nil
	return result, nil
}

func (transaction *Transaction) executeMaterializedAggregate(
	ctx context.Context,
	relation *materializedRelation,
	plan *kitdbsql.SelectStatement,
	parameters []any,
	observe bool,
	working *materializationWorkingSet,
) (Result, error) {
	schema := relation.schema
	columns, err := describeAggregateSelect(schema, plan)
	if err != nil {
		return Result{}, err
	}
	if _, err := transaction.boundedSelectLimit(plan); err != nil {
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
	groupFields := make([]kitdbsql.Field, len(plan.GroupBy))
	for index, requested := range plan.GroupBy {
		_, field, _ := schema.FieldByName(unqualifiedColumn(requested))
		groupFields[index] = field
	}
	bindings := make([]aggregateBinding, len(plan.Projection))
	decimalStates := 0
	for index, projection := range plan.Projection {
		function := strings.ToLower(projection.Aggregate)
		if function == "" && projection.Count {
			function = "count"
		}
		bindings[index].function = function
		if projection.Name != "" {
			_, field, _ := schema.FieldByName(unqualifiedColumn(projection.Name))
			bindings[index].field = &field
		}
		if exactDecimalAggregate(bindings[index].field, function) {
			decimalStates++
		}
	}
	groups := make(map[string]*aggregateGroup)
	order := make([]string, 0)
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
	stats := materializedExecutionStats(observe, "materialized-aggregate")
	for rowIndex, row := range relation.rows {
		if rowIndex&255 == 0 {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
		}
		if stats != nil {
			stats.RowsScanned++
		}
		if !matchesAll(row, conditions) {
			continue
		}
		accepted, err := predicateMatches(row, predicate)
		if err != nil {
			return Result{}, err
		}
		if !accepted {
			continue
		}
		if stats != nil {
			stats.RowsMatched++
		}
		key, err := aggregateGroupKey(row, groupFields)
		if err != nil {
			return Result{}, err
		}
		group := groups[key]
		if group == nil {
			if len(groups) >= transaction.engine.maximumResultRows {
				return Result{}, fmt.Errorf(
					"kitdb SQL: GROUP BY exceeds this server's %d-group limit",
					transaction.engine.maximumResultRows,
				)
			}
			if decimalStates != 0 && len(groups) >= maximumDecimalGroupStateBytes/(decimalStates*(maximumDecimalText+128)) {
				return Result{}, fmt.Errorf(
					"kitdb SQL: decimal GROUP BY exceeds the %d-byte exact aggregate state budget",
					maximumDecimalGroupStateBytes,
				)
			}
			if working != nil {
				if err := working.reserve(
					aggregateGroupWorkingBytes(key, row, groupFields, bindings, true), "GROUP BY state",
				); err != nil {
					return Result{}, err
				}
			}
			group = &aggregateGroup{
				values: aggregateGroupValues(row, groupFields),
				states: make([]aggregateState, len(bindings)),
			}
			groups[key] = group
			order = append(order, key)
		}
		for index, binding := range bindings {
			if binding.function == "" {
				continue
			}
			var value any
			if binding.field != nil {
				value = row[binding.field.Name]
			}
			if err := updateAggregateStateAccounted(
				working, &group.states[index], binding.function, value, binding.field, binding.field == nil,
			); err != nil {
				return Result{}, err
			}
		}
	}
	rows := make([][]any, 0, len(order))
	for _, key := range order {
		group := groups[key]
		row := make([]any, len(plan.Projection))
		for index, projection := range plan.Projection {
			binding := bindings[index]
			if binding.function == "" {
				_, field, _ := schema.FieldByName(unqualifiedColumn(projection.Name))
				row[index] = readField(field, group.values[field.Name])
				continue
			}
			value, err := aggregateStateValue(group.states[index], binding.function, binding.field)
			if err != nil {
				return Result{}, err
			}
			row[index] = value
		}
		if err := working.reserveRow(row, "GROUP BY result rows"); err != nil {
			return Result{}, err
		}
		rows = append(rows, row)
	}
	if stats != nil {
		stats.Groups = uint64(len(groups))
	}
	return finishAggregateRows(ctx, rows, columns, plan, parameters, stats, working)
}

func (transaction *Transaction) boundedSelectLimit(plan *kitdbsql.SelectStatement) (int, error) {
	limit := transaction.engine.maximumResultRows
	if plan.HasLimit {
		if plan.Limit > transaction.engine.maximumResultRows {
			return 0, fmt.Errorf(
				"kitdb SQL: LIMIT %d exceeds this server's result limit of %d",
				plan.Limit, transaction.engine.maximumResultRows,
			)
		}
		limit = plan.Limit
	}
	return limit, nil
}

func sliceMaterializedRows(rows [][]any, offset, limit int) [][]any {
	start := min(offset, len(rows))
	end := min(start+limit, len(rows))
	return rows[start:end]
}

func materializedExecutionStats(observe bool, path string) *ExecutionStats {
	if !observe {
		return nil
	}
	return &ExecutionStats{Path: path}
}

func selectUsesMaterialization(plan *kitdbsql.SelectStatement) bool {
	return plan != nil && (len(plan.CommonTables) != 0 || plan.Source != nil)
}

func (transaction *Transaction) executeMaterializedExplain(plan *kitdbsql.SelectStatement) (Result, error) {
	if _, err := describeSelectFromCatalog(transaction.catalog, plan, nil); err != nil {
		return Result{}, err
	}
	rows := make([][]any, 0, len(plan.CommonTables)+2)
	rows = append(rows, []any{
		int64(0), "materialization",
		fmt.Sprintf(
			"snapshot_tx=%d row_budget=%d byte_budget=%d",
			transaction.base, transaction.engine.maximumResultRows, maximumMaterializedBytes,
		),
	})
	for _, commonTable := range plan.CommonTables {
		rows = append(rows, []any{
			int64(len(rows)), "common table expression", "name=" + commonTable.Name + " mode=materialized",
		})
	}
	if plan.Source != nil {
		rows = append(rows, []any{
			int64(len(rows)), "derived table", "alias=" + plan.TableAlias + " mode=materialized",
		})
	}
	rows = append(rows, []any{int64(len(rows)), "materialized query", "execution=bounded"})
	return Result{Columns: explainColumns(), Rows: rows, CommandTag: "EXPLAIN"}, nil
}
