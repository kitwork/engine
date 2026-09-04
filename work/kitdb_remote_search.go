package work

import (
	"context"
	"fmt"
	"strings"

	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

const (
	kitSQLSearchExpressionOperator = "__kitdb_search__"
	kitDBRemoteSearchFieldLimit    = 32
)

type kitSQLSearch struct {
	fields []string
	text   string
}

type kitDBRemoteSearchPlan struct {
	fields             []string
	columns            []kitDBRemoteColumn
	projectionFields   []string
	residualPredicate  *query.Predicate
	residualExpression *kitSQLExpression
	identifierPrefix   string
	limit              int
	offset             int
	candidateLimit     int
}

func newKitSQLSearchExpression(fields []*kitSQLExpression, text *kitSQLExpression) *kitSQLExpression {
	arguments := make([]*kitSQLExpression, 0, len(fields)+1)
	arguments = append(arguments, fields...)
	arguments = append(arguments, text)
	return &kitSQLExpression{
		kind: kitSQLExpressionFunction, operator: kitSQLSearchExpressionOperator, arguments: arguments,
	}
}

// expressionSearchTuple recognizes only the deliberate multi-field SEARCH
// left operand. Ordinary parenthesized expressions remain on the normal path.
func (parser *kitSQLParser) expressionSearchTuple() (*kitSQLExpression, bool, error) {
	start := parser.tokenPosition()
	if !parser.acceptSymbol("(") {
		return nil, false, nil
	}
	if parser.peek().kind != kitSQLTokenIdentifier {
		parser.restoreTokens(start)
		return nil, false, nil
	}
	first, err := parser.columnReference(false)
	if err != nil || !parser.acceptSymbol(",") {
		parser.restoreTokens(start)
		return nil, false, nil
	}
	fields := []*kitSQLExpression{{kind: kitSQLExpressionReference, reference: first}}
	for {
		field, err := parser.columnReference(false)
		if err != nil {
			return nil, true, err
		}
		fields = append(fields, &kitSQLExpression{kind: kitSQLExpressionReference, reference: field})
		if len(fields) > kitDBRemoteSearchFieldLimit {
			return nil, true, fmt.Errorf("kitdb SQL: SEARCH supports at most %d fields", kitDBRemoteSearchFieldLimit)
		}
		if parser.acceptSymbol(")") {
			break
		}
		if err := parser.expectSymbol(","); err != nil {
			return nil, true, err
		}
	}
	if !parser.acceptKeyword("search") {
		parser.restoreTokens(start)
		return nil, false, nil
	}
	text, err := parser.expressionConcatenation()
	if err != nil {
		return nil, true, err
	}
	return newKitSQLSearchExpression(fields, text), true, nil
}

func extractKitSQLSearch(expression *kitSQLExpression) (*kitSQLSearch, *kitSQLExpression, error) {
	if expression == nil {
		return nil, nil, nil
	}
	if expression.kind == kitSQLExpressionFunction && expression.operator == kitSQLSearchExpressionOperator {
		search, err := decodeKitSQLSearch(expression)
		return search, nil, err
	}
	if expression.kind == kitSQLExpressionBinary && expression.operator == "and" && len(expression.arguments) == 2 {
		leftSearch, leftResidual, err := extractKitSQLSearch(expression.arguments[0])
		if err != nil {
			return nil, nil, err
		}
		rightSearch, rightResidual, err := extractKitSQLSearch(expression.arguments[1])
		if err != nil {
			return nil, nil, err
		}
		if leftSearch != nil && rightSearch != nil {
			return nil, nil, fmt.Errorf("kitdb SQL: SELECT accepts one SEARCH predicate")
		}
		search := leftSearch
		if search == nil {
			search = rightSearch
		}
		return search, joinKitSQLResidualAnd(leftResidual, rightResidual), nil
	}
	if kitSQLExpressionContainsSearch(expression) {
		return nil, nil, fmt.Errorf("kitdb SQL: SEARCH may be combined with row filters only through AND")
	}
	return nil, expression, nil
}

func decodeKitSQLSearch(expression *kitSQLExpression) (*kitSQLSearch, error) {
	if expression == nil || len(expression.arguments) == 0 {
		return nil, fmt.Errorf("kitdb SQL: SEARCH needs a query string")
	}
	text := expression.arguments[len(expression.arguments)-1]
	if text == nil || text.kind != kitSQLExpressionLiteral ||
		(text.literal.K != value.String && !text.literal.IsNil()) {
		return nil, fmt.Errorf("kitdb SQL: SEARCH query must be a string literal, NULL, or bound string")
	}
	fieldExpressions := expression.arguments[:len(expression.arguments)-1]
	if len(fieldExpressions) > kitDBRemoteSearchFieldLimit {
		return nil, fmt.Errorf("kitdb SQL: SEARCH supports at most %d fields", kitDBRemoteSearchFieldLimit)
	}
	search := &kitSQLSearch{fields: make([]string, len(fieldExpressions))}
	if !text.literal.IsNil() {
		search.text = text.literal.String()
	}
	for index, field := range fieldExpressions {
		if field == nil || field.kind != kitSQLExpressionReference || field.reference == "" {
			return nil, fmt.Errorf("kitdb SQL: SEARCH fields must be column references")
		}
		search.fields[index] = field.reference
	}
	return search, nil
}

func joinKitSQLResidualAnd(left, right *kitSQLExpression) *kitSQLExpression {
	switch {
	case left == nil:
		return right
	case right == nil:
		return left
	default:
		return kitSQLBinaryExpression("and", left, right)
	}
}

func kitSQLExpressionContainsSearch(expression *kitSQLExpression) bool {
	if expression == nil {
		return false
	}
	if expression.kind == kitSQLExpressionFunction && expression.operator == kitSQLSearchExpressionOperator {
		return true
	}
	for _, argument := range expression.arguments {
		if kitSQLExpressionContainsSearch(argument) {
			return true
		}
	}
	if kitSQLExpressionContainsSearch(expression.caseBase) || kitSQLExpressionContainsSearch(expression.fallback) {
		return true
	}
	for _, branch := range expression.branches {
		if kitSQLExpressionContainsSearch(branch.when) || kitSQLExpressionContainsSearch(branch.then) {
			return true
		}
	}
	return false
}

func executeKitDBRemoteSearchSelect(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	table, plan, err := prepareKitDBRemoteSearch(ctx, scope, database, statement)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	result := kitDBRemoteResult{columns: plan.columns, rows: [][]value.Value{}}
	if plan.limit == 0 || strings.TrimSpace(statement.search.text) == "" {
		return result, nil
	}

	table.searchActive = true
	table.searchText = statement.search.text
	table.searchFields = append([]string(nil), plan.fields...)
	table.searchIdentifierPrefix = plan.identifierPrefix
	table.limitN = plan.candidateLimit
	rowsValue := table.List()
	if err := kitDBRemoteValueError(rowsValue); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	candidates := rowsValue.Array()
	needed := plan.offset + plan.limit
	qualified := make([]value.Value, 0, min(needed, len(candidates)))
	for position, row := range candidates {
		if position&31 == 0 {
			if err := ctx.Err(); err != nil {
				return kitDBRemoteResult{}, err
			}
		}
		matched, err := matchKitDBRemoteSearchRow(table, plan, row)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		if !matched {
			continue
		}
		qualified = append(qualified, row)
		if len(qualified) >= needed {
			break
		}
	}
	if (plan.residualPredicate != nil || plan.residualExpression != nil) &&
		len(qualified) < needed && table.searchHitCount == searchMaximumLimit {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: SEARCH row filter exceeds the bounded %d-candidate window; make the search more selective",
			searchMaximumLimit,
		)
	}
	start := min(plan.offset, len(qualified))
	end := min(start+plan.limit, len(qualified))
	return projectKitDBRemoteSearchRows(plan, qualified[start:end])
}

func executeKitDBRemoteSearchExplain(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	table, plan, err := prepareKitDBRemoteSearch(ctx, scope, database, statement)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ensureKitDBRemoteTableReady(table); err != nil {
		return kitDBRemoteResult{}, err
	}
	detail := fmt.Sprintf(
		"KITDB SEARCH %s USING BM25 FIELDS (%s); RANK _score DESC; CANDIDATES %d; LIMIT %d",
		table.table, strings.Join(plan.fields, ", "), plan.candidateLimit, plan.limit,
	)
	if plan.residualPredicate != nil || plan.residualExpression != nil {
		detail += "; BOUNDED ROW FILTER"
	}
	if plan.identifierPrefix != "" {
		detail += "; IDENTIFIER PREFIX FILTER"
	}
	if plan.offset > 0 {
		detail += fmt.Sprintf(" OFFSET %d", plan.offset)
	}
	return kitDBRemoteResult{
		columns: []kitDBRemoteColumn{
			{name: "id", kind: "integer"},
			{name: "parent", kind: "integer"},
			{name: "notused", kind: "integer"},
			{name: "detail", kind: "text"},
		},
		rows: [][]value.Value{{value.New(0), value.New(0), value.New(0), value.New(detail)}},
	}, nil
}

func prepareKitDBRemoteSearch(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (*SchemaTable, *kitDBRemoteSearchPlan, error) {
	if statement.search == nil {
		return nil, nil, fmt.Errorf("kitdb SQL: SEARCH plan is unavailable")
	}
	if len(statement.joins) != 0 {
		return nil, nil, fmt.Errorf("kitdb SQL: SEARCH does not support JOIN yet")
	}
	if strings.EqualFold(statement.table, "sqlite_master") || strings.EqualFold(statement.table, "sqlite_schema") {
		return nil, nil, fmt.Errorf("kitdb SQL: SEARCH is not supported on catalog metadata")
	}
	if statement.distinct {
		return nil, nil, fmt.Errorf("kitdb SQL: SEARCH does not support DISTINCT yet")
	}
	if kitDBRemoteGrouped(statement) {
		return nil, nil, fmt.Errorf("kitdb SQL: SEARCH does not support GROUP BY or HAVING yet")
	}
	if len(statement.orders) > 1 {
		return nil, nil, fmt.Errorf("kitdb SQL: SEARCH accepts only ORDER BY _score DESC")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	table, err := kitDBRemoteTable(database, scope, statement.table)
	if err != nil {
		return nil, nil, err
	}
	if err := validateKitDBRemoteSearchVirtualFields(table.definition); err != nil {
		return nil, nil, err
	}
	fields, err := resolveKitDBRemoteSearchFields(table, statement)
	if err != nil {
		return nil, nil, err
	}
	columns, projectionFields, err := prepareKitDBRemoteSearchProjections(table, statement)
	if err != nil {
		return nil, nil, err
	}
	if err := validateKitDBRemoteSearchOrder(statement); err != nil {
		return nil, nil, err
	}

	limit := 20
	if statement.hasLimit {
		limit = statement.limit
	}
	if limit > searchMaximumLimit {
		return nil, nil, fmt.Errorf("kitdb SQL: SEARCH limit exceeds %d", searchMaximumLimit)
	}
	if statement.offset > searchMaximumLimit-limit {
		return nil, nil, fmt.Errorf(
			"kitdb SQL: SEARCH LIMIT plus OFFSET exceeds %d", searchMaximumLimit,
		)
	}
	plan := &kitDBRemoteSearchPlan{
		fields: fields, columns: columns,
		projectionFields: projectionFields, limit: limit, offset: statement.offset,
	}
	if statement.whereExpression != nil {
		if kitSQLExpressionContainsAggregate(statement.whereExpression) {
			return nil, nil, fmt.Errorf("kitdb SQL: aggregate expressions are not valid in SEARCH row filters")
		}
		resolve := kitDBRemoteSearchReferenceResolver(table, statement)
		if _, err := resolveKitSQLExpression(statement.whereExpression, resolve); err != nil {
			return nil, nil, err
		}
		plan.residualExpression = statement.whereExpression
	} else if statement.predicate != nil {
		predicate, err := canonicalKitDBRemoteSearchPredicate(table, statement, *statement.predicate)
		if err != nil {
			return nil, nil, err
		}
		plan.residualPredicate = &predicate
	}
	plan.identifierPrefix, _ = kitDBRemoteSearchIdentifierPrefix(
		table, plan.residualExpression, plan.residualPredicate,
	)
	plan.candidateLimit = plan.offset + plan.limit
	if plan.residualPredicate != nil || plan.residualExpression != nil {
		plan.candidateLimit = searchMaximumLimit
	}
	return table, plan, nil
}

func kitDBRemoteSearchIdentifierPrefix(
	table *SchemaTable,
	expression *kitSQLExpression,
	predicate *query.Predicate,
) (string, bool) {
	if field, text, found := kitDBRemoteSearchTextEqualityExpression(expression); found {
		if prefix, supported := table.kitDBLeadingTextSearchIdentifierPrefix(field, text); supported {
			return prefix, true
		}
	}
	if field, text, found := kitDBRemoteSearchTextEqualityPredicate(predicate); found {
		return table.kitDBLeadingTextSearchIdentifierPrefix(field, text)
	}
	return "", false
}

func kitDBRemoteSearchTextEqualityExpression(
	expression *kitSQLExpression,
) (field, text string, found bool) {
	if expression == nil || expression.kind != kitSQLExpressionBinary || len(expression.arguments) != 2 {
		return "", "", false
	}
	if expression.operator == "and" {
		if field, text, found := kitDBRemoteSearchTextEqualityExpression(expression.arguments[0]); found {
			return field, text, true
		}
		return kitDBRemoteSearchTextEqualityExpression(expression.arguments[1])
	}
	if expression.operator != "=" && expression.operator != "==" {
		return "", "", false
	}
	if field, text, found := kitDBRemoteSearchTextEqualityOperands(
		expression.arguments[0], expression.arguments[1],
	); found {
		return field, text, true
	}
	return kitDBRemoteSearchTextEqualityOperands(expression.arguments[1], expression.arguments[0])
}

func kitDBRemoteSearchTextEqualityOperands(
	reference *kitSQLExpression,
	literal *kitSQLExpression,
) (field, text string, found bool) {
	if reference == nil || reference.kind != kitSQLExpressionReference || reference.reference == "" ||
		literal == nil || literal.kind != kitSQLExpressionLiteral || literal.literal.K != value.String {
		return "", "", false
	}
	return reference.reference, literal.literal.String(), true
}

func kitDBRemoteSearchTextEqualityPredicate(
	predicate *query.Predicate,
) (field, text string, found bool) {
	if predicate == nil {
		return "", "", false
	}
	switch predicate.Kind {
	case query.PredicateCondition:
		condition := predicate.Condition
		if condition.IsColumn || (condition.Operator != "=" && condition.Operator != "==") {
			return "", "", false
		}
		switch item := condition.Value.(type) {
		case value.Value:
			if item.K == value.String {
				return condition.Column, item.String(), true
			}
		case string:
			return condition.Column, item, true
		}
	case query.PredicateAnd:
		for index := range predicate.Children {
			if field, text, found := kitDBRemoteSearchTextEqualityPredicate(&predicate.Children[index]); found {
				return field, text, true
			}
		}
	}
	return "", "", false
}

func resolveKitDBRemoteSearchFields(
	table *SchemaTable,
	statement kitSQLStatement,
) ([]string, error) {
	available := table.searchableColumns()
	if len(available) == 0 {
		return nil, fmt.Errorf("kitdb SQL: struct %q has no searchable field", table.table)
	}
	byName := make(map[string]searchColumn, len(available))
	for _, column := range available {
		byName[strings.ToLower(column.name)] = column
	}
	if len(statement.search.fields) == 0 {
		fields := make([]string, len(available))
		for index, column := range available {
			fields[index] = column.name
		}
		return fields, nil
	}
	fields := make([]string, 0, len(statement.search.fields))
	seen := make(map[string]struct{}, len(statement.search.fields))
	for _, requested := range statement.search.fields {
		canonical, _, err := resolveKitDBRemoteSearchReference(table, statement, requested)
		if err != nil {
			return nil, err
		}
		column, searchable := byName[strings.ToLower(canonical)]
		if !searchable {
			return nil, fmt.Errorf("kitdb SQL: SEARCH field %q is not searchable", canonical)
		}
		key := strings.ToLower(column.name)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("kitdb SQL: duplicate SEARCH field %q", requested)
		}
		seen[key] = struct{}{}
		fields = append(fields, column.name)
	}
	return fields, nil
}

func validateKitDBRemoteSearchVirtualFields(definition *StructDef) error {
	for _, field := range definition.Fields {
		if strings.EqualFold(field.Name, "_score") || strings.EqualFold(field.Name, "_snippet") {
			return fmt.Errorf("kitdb SQL: SEARCH reserves field name %q", field.Name)
		}
	}
	return nil
}

func prepareKitDBRemoteSearchProjections(
	table *SchemaTable,
	statement kitSQLStatement,
) ([]kitDBRemoteColumn, []string, error) {
	if len(statement.projections) == 1 && statement.projections[0].all {
		qualifier, field := kitSQLReferenceParts(statement.projections[0].field)
		if field != "*" {
			return nil, nil, fmt.Errorf("kitdb SQL: invalid star projection %q", statement.projections[0].field)
		}
		if err := validateKitDBRemoteSearchQualifier(table, statement, qualifier); err != nil {
			return nil, nil, err
		}
		columns := make([]kitDBRemoteColumn, len(table.definition.Fields))
		fields := make([]string, len(table.definition.Fields))
		for index, field := range table.definition.Fields {
			columns[index] = kitDBRemoteColumn{name: field.Name, kind: field.Kind}
			fields[index] = field.Name
		}
		return columns, fields, nil
	}
	columns := make([]kitDBRemoteColumn, 0, len(statement.projections))
	fields := make([]string, 0, len(statement.projections))
	for _, projection := range statement.projections {
		if projection.all {
			return nil, nil, fmt.Errorf("kitdb SQL: * cannot be mixed with named SEARCH fields")
		}
		if projection.aggregate != "" || projection.expression != nil {
			return nil, nil, fmt.Errorf("kitdb SQL: SEARCH supports field projections only")
		}
		qualifier, requested := kitSQLReferenceParts(projection.field)
		if err := validateKitDBRemoteSearchQualifier(table, statement, qualifier); err != nil {
			return nil, nil, err
		}
		field, kind := "", ""
		switch {
		case strings.EqualFold(requested, "_score"):
			field, kind = "_score", "float"
		case strings.EqualFold(requested, "_snippet"):
			field, kind = "_snippet", "text"
		default:
			canonical, definition, err := kitDBRemoteField(table.definition, requested)
			if err != nil {
				return nil, nil, err
			}
			field, kind = canonical, definition.Kind
		}
		name := projection.alias
		if name == "" {
			name = field
		}
		columns = append(columns, kitDBRemoteColumn{name: name, kind: kind})
		fields = append(fields, field)
	}
	return columns, fields, nil
}

func validateKitDBRemoteSearchOrder(statement kitSQLStatement) error {
	if len(statement.orders) == 0 {
		return nil
	}
	order := statement.orders[0]
	if order.expression != nil {
		return fmt.Errorf("kitdb SQL: SEARCH accepts only ORDER BY _score DESC")
	}
	requested := order.column
	projection, found, err := kitDBRemoteProjectionAlias(statement.projections, requested)
	if err != nil {
		return err
	}
	if found {
		requested = projection.field
	}
	_, requested = kitSQLReferenceParts(requested)
	if !strings.EqualFold(requested, "_score") || !strings.EqualFold(order.direction, "desc") {
		return fmt.Errorf("kitdb SQL: SEARCH accepts only ORDER BY _score DESC")
	}
	return nil
}

func kitDBRemoteSearchReferenceResolver(
	table *SchemaTable,
	statement kitSQLStatement,
) kitSQLExpressionReferenceResolver {
	return func(requested string) (string, string, error) {
		return resolveKitDBRemoteSearchReference(table, statement, requested)
	}
}

func resolveKitDBRemoteSearchReference(
	table *SchemaTable,
	statement kitSQLStatement,
	requested string,
) (string, string, error) {
	qualifier, field := kitSQLReferenceParts(requested)
	if err := validateKitDBRemoteSearchQualifier(table, statement, qualifier); err != nil {
		return "", "", err
	}
	canonical, definition, err := kitDBRemoteField(table.definition, field)
	if err != nil {
		return "", "", err
	}
	return canonical, definition.Kind, nil
}

func validateKitDBRemoteSearchQualifier(
	table *SchemaTable,
	statement kitSQLStatement,
	qualifier string,
) error {
	if qualifier == "" || strings.EqualFold(qualifier, statement.tableAlias) ||
		strings.EqualFold(qualifier, statement.table) || strings.EqualFold(qualifier, table.table) {
		return nil
	}
	return fmt.Errorf("kitdb SQL: unknown table qualifier %q", qualifier)
}

func canonicalKitDBRemoteSearchPredicate(
	table *SchemaTable,
	statement kitSQLStatement,
	predicate query.Predicate,
) (query.Predicate, error) {
	canonical := predicate
	canonical.Children = make([]query.Predicate, len(predicate.Children))
	if predicate.Kind == query.PredicateCondition {
		field, _, err := resolveKitDBRemoteSearchReference(table, statement, predicate.Condition.Column)
		if err != nil {
			return query.Predicate{}, err
		}
		canonical.Condition.Column = field
		if predicate.Condition.IsColumn {
			right, _, err := resolveKitDBRemoteSearchReference(table, statement, fmt.Sprint(predicate.Condition.Value))
			if err != nil {
				return query.Predicate{}, err
			}
			canonical.Condition.Value = right
		}
	}
	for index, child := range predicate.Children {
		resolved, err := canonicalKitDBRemoteSearchPredicate(table, statement, child)
		if err != nil {
			return query.Predicate{}, err
		}
		canonical.Children[index] = resolved
	}
	return canonical, nil
}

func matchKitDBRemoteSearchRow(
	table *SchemaTable,
	plan *kitDBRemoteSearchPlan,
	row value.Value,
) (bool, error) {
	if row.K != value.Map {
		return false, fmt.Errorf("kitdb remote: search row is not an object")
	}
	fields := row.Map()
	if plan.residualExpression != nil {
		item, err := evaluateKitSQLExpression(plan.residualExpression, func(reference string) (value.Value, error) {
			spec := table.columns[reference]
			if spec == nil {
				return value.Value{}, fmt.Errorf("kitdb SQL: unresolved SEARCH filter field %q", reference)
			}
			item, found := fields[reference]
			if !found {
				item = value.NewNil()
			}
			return item, nil
		})
		if err != nil {
			return false, fmt.Errorf("kitdb SQL: SEARCH row filter: %w", err)
		}
		truth, err := kitSQLExpressionTruth(item)
		if err != nil {
			return false, fmt.Errorf("kitdb SQL: SEARCH row filter: %w", err)
		}
		return truth == kitDBTrue, nil
	}
	if plan.residualPredicate != nil {
		truth, err := kitDBEvaluatePredicate(plan.residualPredicate, func(reference string) (value.Value, string, error) {
			spec := table.columns[reference]
			if spec == nil {
				return value.Value{}, "", fmt.Errorf("kitdb SQL: unresolved SEARCH filter field %q", reference)
			}
			item, found := fields[reference]
			if !found {
				item = value.NewNil()
			}
			return item, spec.kind, nil
		})
		return truth == kitDBTrue, err
	}
	return true, nil
}

func projectKitDBRemoteSearchRows(
	plan *kitDBRemoteSearchPlan,
	rows []value.Value,
) (kitDBRemoteResult, error) {
	result := kitDBRemoteResult{columns: plan.columns, rows: make([][]value.Value, 0, len(rows))}
	for _, encoded := range rows {
		if encoded.K != value.Map {
			return kitDBRemoteResult{}, fmt.Errorf("kitdb remote: search row is not an object")
		}
		row := encoded.Map()
		projected := make([]value.Value, len(plan.projectionFields))
		for index, field := range plan.projectionFields {
			item, found := row[field]
			if !found {
				item = value.NewNil()
			}
			projected[index] = item
		}
		result.rows = append(result.rows, projected)
	}
	return result, nil
}
