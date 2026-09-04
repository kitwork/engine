package relational

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type boundScalarProjection struct {
	column      Column
	expression  *boundPredicate
	sourceField *kitdbsql.Field
}

type boundScalarOrder struct {
	expression *boundPredicate
	projection int
	field      *kitdbsql.Field
	kind       string
	descending bool
}

type scalarExpressionRow struct {
	key     []byte
	values  []any
	ordered []any
}

func selectHasScalarExpressions(plan *kitdbsql.SelectStatement) bool {
	if plan == nil {
		return false
	}
	for _, projection := range plan.Projection {
		if projection.Expression != nil {
			return true
		}
	}
	for _, order := range plan.Order {
		if order.Expression != nil {
			return true
		}
	}
	return false
}

func describeScalarExpressionSelect(schema kitdbsql.Schema, plan *kitdbsql.SelectStatement) ([]Column, error) {
	projections, err := describeScalarProjections(schema, plan.Projection)
	if err != nil {
		return nil, err
	}
	columns := make([]Column, len(projections))
	for index := range projections {
		columns[index] = projections[index].column
	}
	return columns, nil
}

func describeScalarProjections(
	schema kitdbsql.Schema,
	plans []kitdbsql.Projection,
) ([]boundScalarProjection, error) {
	result := make([]boundScalarProjection, 0, len(plans))
	for _, plan := range plans {
		if plan.Aggregate != "" || plan.Count {
			return nil, fmt.Errorf("kitdb SQL: aggregate and scalar expression projections cannot be mixed")
		}
		if plan.All {
			if plan.Qualifier != "" && !strings.EqualFold(plan.Qualifier, schema.Name) {
				return nil, fmt.Errorf("kitdb SQL: unknown table qualifier %q", plan.Qualifier)
			}
			for index := range schema.Fields {
				field := schema.Fields[index]
				copy := field
				result = append(result, boundScalarProjection{
					column: columnForField(field.Name, field), sourceField: &copy,
				})
			}
			continue
		}
		expression := plan.Expression
		if expression == nil {
			expression = &kitdbsql.CheckPlan{Kind: "field", Field: plan.Name}
		}
		kind, field, err := scalarExpressionKind(schema, expression)
		if err != nil {
			return nil, err
		}
		label := plan.Alias
		if label == "" && field != nil {
			label = field.Name
		}
		if label == "" {
			label = fmt.Sprintf("column%d", len(result)+1)
		}
		column := Column{Name: label, Kind: kind, ExactUUID: kind == "uuid"}
		if field != nil && kind == field.Kind {
			column = columnForField(label, *field)
		} else if expression.Kind == "cast" {
			if kind == "decimal" {
				column.Precision, column.Scale = expression.Precision, expression.Scale
			}
			if exactTemporalFieldKind(kind) {
				column.TimePrecision = expression.TimePrecision
			}
			if kind == "varchar" || kind == "char" {
				column.TextLength = expression.TextLength
			}
			column.ExactUUID = kind == "uuid"
		}
		result = append(result, boundScalarProjection{column: column, sourceField: field})
	}
	return result, nil
}

func bindScalarProjections(
	schema kitdbsql.Schema,
	plans []kitdbsql.Projection,
	parameters []any,
) ([]boundScalarProjection, error) {
	result, err := describeScalarProjections(schema, plans)
	if err != nil {
		return nil, err
	}
	position := 0
	now := time.Now().UTC()
	for _, plan := range plans {
		if plan.All {
			for range schema.Fields {
				field := result[position].sourceField
				result[position].expression = &boundPredicate{kind: "field", field: field}
				position++
			}
			continue
		}
		expression := plan.Expression
		if expression == nil {
			expression = &kitdbsql.CheckPlan{Kind: "field", Field: plan.Name}
		}
		bound, err := bindPredicateAt(schema, expression, parameters, now)
		if err != nil {
			return nil, err
		}
		result[position].expression = bound
		position++
	}
	return result, nil
}

func scalarExpressionKind(
	schema kitdbsql.Schema,
	expression *kitdbsql.CheckPlan,
) (string, *kitdbsql.Field, error) {
	if expression == nil {
		return "", nil, fmt.Errorf("kitdb SQL: empty scalar expression")
	}
	switch expression.Kind {
	case "field":
		_, field, found := schema.FieldByName(unqualifiedColumn(expression.Field))
		if !found {
			return "", nil, fmt.Errorf("kitdb SQL: table %q has no field %q", schema.Name, expression.Field)
		}
		copy := field
		return field.Kind, &copy, nil
	case "literal":
		switch expression.Literal.Kind {
		case kitdbsql.LiteralBoolean:
			return "bool", nil, nil
		case kitdbsql.LiteralNumber:
			value, err := resolveLiteral(expression.Literal, nil)
			if err != nil {
				return "", nil, err
			}
			integer, exact := value.(int64)
			if !exact {
				return "decimal", nil, nil
			}
			return integerLiteralExpressionKind(integer), nil, nil
		case kitdbsql.LiteralCurrentTimestamp:
			return "timestamptz", nil, nil
		case kitdbsql.LiteralCurrentDate:
			return "date", nil, nil
		case kitdbsql.LiteralCurrentTime:
			return "time", nil, nil
		case kitdbsql.LiteralLocalTimestamp:
			return "timestamp", nil, nil
		default:
			return "text", nil, nil
		}
	case "unary":
		if expression.Operator == "not" || expression.Operator == "is null" || expression.Operator == "is not null" {
			return "bool", nil, nil
		}
		if len(expression.Arguments) != 1 {
			return "", nil, fmt.Errorf("kitdb SQL: unary expression needs one argument")
		}
		kind, _, err := scalarExpressionKind(schema, &expression.Arguments[0])
		if err == nil && expression.Operator != "not" && !isNumericExpressionKind(kind) && kind != "interval" {
			return "", nil, fmt.Errorf("kitdb SQL: unary %s requires a numeric value", expression.Operator)
		}
		return kind, nil, err
	case "binary":
		switch expression.Operator {
		case "and", "or", "=", "!=", "<>", "<", "<=", ">", ">=", "like", "not like", "ilike", "not ilike":
			return "bool", nil, nil
		case "||":
			return "text", nil, nil
		case "+", "-", "*", "/", "%":
			if len(expression.Arguments) != 2 {
				return "", nil, fmt.Errorf("kitdb SQL: binary expression needs two arguments")
			}
			left, _, err := scalarExpressionKind(schema, &expression.Arguments[0])
			if err != nil {
				return "", nil, err
			}
			right, _, err := scalarExpressionKind(schema, &expression.Arguments[1])
			if err != nil {
				return "", nil, err
			}
			if result, temporal := temporalArithmeticResultKind(expression.Operator, left, right); temporal {
				if result == "" {
					return "", nil, fmt.Errorf(
						"kitdb SQL: incompatible %s and %s operands for %s", left, right, expression.Operator,
					)
				}
				return result, nil, nil
			}
			if !isNumericExpressionKind(left) || !isNumericExpressionKind(right) {
				return "", nil, fmt.Errorf("kitdb SQL: %s requires numeric operands", expression.Operator)
			}
			if isDecimalExpressionKind(left) || isDecimalExpressionKind(right) {
				if isFloatExpressionKind(left) || isFloatExpressionKind(right) {
					return "float", nil, nil
				}
				return "decimal", nil, nil
			}
			if expression.Operator == "/" || isFloatExpressionKind(left) || isFloatExpressionKind(right) {
				return "float", nil, nil
			}
			if isIntegerExpressionKind(left) && isIntegerExpressionKind(right) {
				return integerExpressionResultKind(left, right), nil, nil
			}
			return "float", nil, nil
		}
	case "cast":
		if len(expression.Arguments) != 1 || expression.CastKind == "" {
			return "", nil, fmt.Errorf("kitdb SQL: invalid CAST expression")
		}
		return expression.CastKind, nil, nil
	case "function":
		if expression.Function != nil {
			return expression.Function.ReturnKind, nil, nil
		}
		switch expression.Operator {
		case "in", "not in", "between", "not between":
			return "bool", nil, nil
		case "length":
			return "int32", nil, nil
		case "lower", "upper", "trim", "ltrim", "rtrim":
			return "text", nil, nil
		case "round":
			if len(expression.Arguments) == 0 {
				return "", nil, fmt.Errorf("kitdb SQL: ROUND needs an argument")
			}
			kind, _, err := scalarExpressionKind(schema, &expression.Arguments[0])
			if err != nil {
				return "", nil, err
			}
			if isDecimalExpressionKind(kind) {
				return "decimal", nil, nil
			}
			return "float", nil, nil
		case "date_trunc":
			if len(expression.Arguments) != 2 {
				return "", nil, fmt.Errorf("kitdb SQL: DATE_TRUNC needs two arguments")
			}
			unitKind, _, err := scalarExpressionKind(schema, &expression.Arguments[0])
			if err != nil || (unitKind != "text" && unitKind != "varchar" && unitKind != "char") {
				return "", nil, fmt.Errorf("kitdb SQL: DATE_TRUNC unit must be text")
			}
			kind, _, err := scalarExpressionKind(schema, &expression.Arguments[1])
			if err != nil {
				return "", nil, err
			}
			if !exactTemporalFieldKind(kind) {
				return "", nil, fmt.Errorf("kitdb SQL: DATE_TRUNC value must be temporal")
			}
			return kind, nil, nil
		case "date_part":
			if len(expression.Arguments) != 2 {
				return "", nil, fmt.Errorf("kitdb SQL: DATE_PART needs two arguments")
			}
			kind, _, err := scalarExpressionKind(schema, &expression.Arguments[1])
			if err != nil {
				return "", nil, err
			}
			if !exactTemporalFieldKind(kind) {
				return "", nil, fmt.Errorf("kitdb SQL: DATE_PART value must be temporal")
			}
			return "float", nil, nil
		case "coalesce", "nullif", "abs":
			if len(expression.Arguments) == 0 {
				return "", nil, fmt.Errorf("kitdb SQL: %s needs an argument", strings.ToUpper(expression.Operator))
			}
			kind, _, err := scalarExpressionKind(schema, &expression.Arguments[0])
			return kind, nil, err
		default:
			return "", nil, fmt.Errorf("kitdb SQL: unsupported function %s", strings.ToUpper(expression.Operator))
		}
	}
	return "", nil, fmt.Errorf("kitdb SQL: unsupported scalar expression %q", expression.Operator)
}

func isIntegerExpressionKind(kind string) bool {
	typeInfo, found := kitdbsql.LookupKind(kind)
	return found && (typeInfo.Family == kitdbsql.FamilyInteger || typeInfo.Family == kitdbsql.FamilySystem)
}

func isDecimalExpressionKind(kind string) bool {
	typeInfo, found := kitdbsql.LookupKind(kind)
	return found && typeInfo.Family == kitdbsql.FamilyDecimal
}

func isFloatExpressionKind(kind string) bool {
	typeInfo, found := kitdbsql.LookupKind(kind)
	return found && typeInfo.Family == kitdbsql.FamilyFloat
}

func isNumericExpressionKind(kind string) bool {
	return isIntegerExpressionKind(kind) || isDecimalExpressionKind(kind) || isFloatExpressionKind(kind)
}

func isTemporalExpressionKind(kind string) bool {
	return exactTemporalFieldKind(kind)
}

func temporalArithmeticResultKind(operator, left, right string) (string, bool) {
	if !isTemporalExpressionKind(left) && !isTemporalExpressionKind(right) {
		return "", false
	}
	if operator != "+" && operator != "-" {
		return "", true
	}
	if left == "date" && isIntegerExpressionKind(right) || operator == "+" && right == "date" && isIntegerExpressionKind(left) {
		return "date", true
	}
	if operator == "-" && left == "date" && right == "date" {
		return "int32", true
	}
	if right == "interval" {
		switch left {
		case "date":
			return "timestamp", true
		case "time", "timestamp", "timestamptz", "interval":
			return left, true
		}
	}
	if operator == "+" && left == "interval" {
		switch right {
		case "date":
			return "timestamp", true
		case "time", "timestamp", "timestamptz":
			return right, true
		}
	}
	if operator == "-" && (left == "timestamp" || left == "timestamptz") && left == right {
		return "interval", true
	}
	return "", true
}

func integerLiteralExpressionKind(value int64) string {
	if value >= int64(-1<<31) && value <= int64(1<<31-1) {
		return "int32"
	}
	return "bigint"
}

func integerExpressionResultKind(left, right string) string {
	if left == right {
		return left
	}
	// The original integer kind has a frozen 53-bit contract and key encoding.
	// Keep expressions involving it in that compatibility domain.
	if left == "integer" || right == "integer" {
		return "integer"
	}
	rank := func(kind string) int {
		switch kind {
		case "smallint":
			return 1
		case "int32":
			return 2
		case "bigint":
			return 3
		default:
			return 4
		}
	}
	if rank(left) >= rank(right) {
		return left
	}
	return right
}

func coerceScalarExpressionValue(kind string, value any) (any, error) {
	if temporal, ok := value.(exactTemporal); ok {
		if temporal.kind != kind {
			converted, err := convertTemporal(temporal, kind, nil)
			if err != nil {
				return nil, err
			}
			return converted.text, nil
		}
		return temporal.text, nil
	}
	if value == nil || !exactIntegerFieldKind(kind) && !isDecimalExpressionKind(kind) &&
		!exactTemporalFieldKind(kind) && kind != "uuid" {
		return value, nil
	}
	field := kitdbsql.Field{Name: "expression", Kind: kind, ExactUUID: kind == "uuid"}
	coerced, err := coerceField(field, value)
	if err != nil {
		return nil, err
	}
	return readField(field, coerced), nil
}

func bindScalarOrders(
	schema kitdbsql.Schema,
	plans []kitdbsql.Order,
	projections []boundScalarProjection,
	parameters []any,
) ([]boundScalarOrder, []boundOrder, bool, error) {
	orders := make([]boundScalarOrder, len(plans))
	source := make([]boundOrder, 0, len(plans))
	sourceOnly := true
	for index, plan := range plans {
		orders[index].projection = -1
		orders[index].descending = plan.Descending
		if plan.Expression == nil {
			matches := -1
			for projectionIndex, projection := range projections {
				if strings.EqualFold(projection.column.Name, plan.Column) {
					if matches != -1 {
						return nil, nil, false, fmt.Errorf("kitdb SQL: ORDER BY alias %q is ambiguous", plan.Column)
					}
					matches = projectionIndex
				}
			}
			if matches >= 0 {
				orders[index].projection = matches
				orders[index].kind = projections[matches].column.Kind
				if projections[matches].sourceField != nil {
					orders[index].field = projections[matches].sourceField
					source = append(source, boundOrder{
						field: *projections[matches].sourceField, descending: plan.Descending,
					})
					continue
				}
				sourceOnly = false
				continue
			}
		}
		expression := plan.Expression
		if expression == nil {
			expression = &kitdbsql.CheckPlan{Kind: "field", Field: plan.Column}
		}
		bound, err := bindPredicate(schema, expression, parameters)
		if err != nil {
			return nil, nil, false, err
		}
		orders[index].expression = bound
		kind, _, err := scalarExpressionKind(schema, expression)
		if err != nil {
			return nil, nil, false, err
		}
		orders[index].kind = kind
		if bound.field != nil {
			orders[index].field = bound.field
			source = append(source, boundOrder{field: *bound.field, descending: plan.Descending})
		} else {
			sourceOnly = false
		}
	}
	if !sourceOnly {
		source = nil
	}
	return orders, source, sourceOnly, nil
}

func (transaction *Transaction) executeScalarExpressionSelect(
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
	orders, sourceOrders, sourceOnly, err := bindScalarOrders(schema, plan.Order, projections, parameters)
	if err != nil {
		return Result{}, err
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
	columns := make([]Column, len(projections))
	for index := range projections {
		columns[index] = projections[index].column
	}
	result := Result{Columns: columns}
	if limit == 0 {
		result.CommandTag = "SELECT 0"
		return result, nil
	}
	generation, err := activeRowGeneration(transaction, schema)
	if err != nil {
		return Result{}, err
	}
	access, err := transaction.planRowAccess(schema, generation, conditions, sourceOrders)
	if err != nil {
		return Result{}, err
	}
	stats := rowAccessExecutionStats(access, observe)
	needsSort := len(orders) != 0 && (!sourceOnly || !access.orderCovered)
	rows := make([]scalarExpressionRow, 0, min(limit+plan.Offset, 256))
	distinct := make(map[string]struct{})
	qualified := 0
	scanned := 0
	err = transaction.walkAccessRowsObserved(access, stats, func(key, encoded []byte) (bool, error) {
		scanned++
		if scanned&255 == 0 {
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
		if err != nil || !matched {
			return false, err
		}
		if stats != nil {
			stats.RowsMatched++
		}
		projected := make([]any, len(projections))
		for index, projection := range projections {
			item, err := evaluateBoundPredicate(decoded.values, projection.expression)
			if err != nil {
				return false, fmt.Errorf("kitdb SQL: projection %d: %w", index+1, err)
			}
			item, err = coerceScalarExpressionValue(projection.column.Kind, item)
			if err != nil {
				return false, fmt.Errorf("kitdb SQL: projection %d: %w", index+1, err)
			}
			projected[index] = item
		}
		if plan.Distinct {
			identity, err := json.Marshal(projected)
			if err != nil {
				return false, fmt.Errorf("kitdb SQL: encode DISTINCT expression row: %w", err)
			}
			key := string(identity)
			if _, duplicate := distinct[key]; duplicate {
				return false, nil
			}
			if len(distinct) >= transaction.engine.maximumResultRows {
				return false, fmt.Errorf(
					"kitdb SQL: DISTINCT expression exceeds %d rows", transaction.engine.maximumResultRows,
				)
			}
			if err := working.reserve(len(key)+64, "DISTINCT expression identities"); err != nil {
				return false, err
			}
			distinct[key] = struct{}{}
		}
		qualified++
		if !needsSort && qualified <= plan.Offset {
			return false, nil
		}
		if len(rows) >= transaction.engine.maximumResultRows {
			if !needsSort && plan.HasLimit {
				return true, nil
			}
			return false, fmt.Errorf(
				"kitdb SQL: expression result exceeds %d rows", transaction.engine.maximumResultRows,
			)
		}
		row := scalarExpressionRow{key: bytes.Clone(key), values: projected}
		if needsSort {
			row.ordered = make([]any, len(orders))
			for index, order := range orders {
				if order.projection >= 0 {
					row.ordered[index] = projected[order.projection]
					continue
				}
				item, err := evaluateBoundPredicate(decoded.values, order.expression)
				if err != nil {
					return false, fmt.Errorf("kitdb SQL: ORDER BY expression %d: %w", index+1, err)
				}
				item, err = coerceScalarExpressionValue(order.kind, item)
				if err != nil {
					return false, fmt.Errorf("kitdb SQL: ORDER BY expression %d: %w", index+1, err)
				}
				row.ordered[index] = item
			}
		}
		if working != nil {
			retainedBytes := materializedRowBytes(row.values) + len(row.key)
			if len(row.ordered) != 0 {
				retainedBytes += materializedReferenceRowBytes(row.ordered)
			}
			if err := working.reserve(retainedBytes, "ORDER BY/DISTINCT expression rows"); err != nil {
				return false, err
			}
		}
		rows = append(rows, row)
		return !needsSort && plan.HasLimit && len(rows) >= limit, nil
	})
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if needsSort {
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
			return bytes.Compare(rows[left].key, rows[right].key) < 0
		})
		offset := min(plan.Offset, len(rows))
		rows = rows[offset:]
		if len(rows) > limit {
			rows = rows[:limit]
		}
	}
	result.Rows = make([][]any, len(rows))
	for index := range rows {
		result.Rows[index] = rows[index].values
	}
	result.CommandTag = fmt.Sprintf("SELECT %d", len(result.Rows))
	result.Execution = stats
	result.materializationWorkingAccounted = working != nil
	return result, nil
}

func compareScalarOrderValues(order boundScalarOrder, left, right any) int {
	if order.field != nil {
		return compareFieldValues(*order.field, left, right)
	}
	return compareValues(left, right)
}
