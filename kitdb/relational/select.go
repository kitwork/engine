package relational

import (
	"bytes"
	"container/heap"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type boundCondition struct {
	field    kitdbsql.Field
	operator string
	value    any
}

type boundOrder struct {
	field      kitdbsql.Field
	descending bool
}

type boundedOrderedRow struct {
	values        map[string]any
	sequence      int
	retainedBytes int
}

type boundedOrderedRows struct {
	orders []boundOrder
	rows   []boundedOrderedRow
}

func (rows boundedOrderedRows) Len() int { return len(rows.rows) }

// Less deliberately puts the worst retained row at the heap root.
func (rows boundedOrderedRows) Less(left, right int) bool {
	return compareBoundedOrderedRows(rows.orders, rows.rows[left], rows.rows[right]) > 0
}

func (rows boundedOrderedRows) Swap(left, right int) {
	rows.rows[left], rows.rows[right] = rows.rows[right], rows.rows[left]
}

func (rows *boundedOrderedRows) Push(item any) {
	rows.rows = append(rows.rows, item.(boundedOrderedRow))
}

func (rows *boundedOrderedRows) Pop() any {
	last := len(rows.rows) - 1
	item := rows.rows[last]
	rows.rows[last] = boundedOrderedRow{}
	rows.rows = rows.rows[:last]
	return item
}

func (transaction *Transaction) executeSelect(
	ctx context.Context,
	plan *kitdbsql.SelectStatement,
	parameters []any,
	observe bool,
	working *materializationWorkingSet,
) (Result, error) {
	if err := transaction.ready(); err != nil {
		return Result{}, err
	}
	if plan == nil {
		return Result{}, fmt.Errorf("kitdb SQL: invalid SELECT plan")
	}
	if plan.Search != nil {
		return Result{}, fmt.Errorf("kitdb SQL: SEARCH requires the engine autocommit path")
	}
	if len(plan.Joins) != 0 {
		return transaction.executeJoinSelect(ctx, plan, parameters, observe, working)
	}
	if selectHasAggregates(plan) {
		return transaction.executeAggregateSelect(ctx, plan, parameters, observe, working)
	}
	if selectHasScalarExpressions(plan) {
		return transaction.executeScalarExpressionSelect(ctx, plan, parameters, observe, working)
	}
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
	orders, err := bindOrders(schema, plan.Order)
	if err != nil {
		return Result{}, err
	}
	countMode := len(plan.Projection) == 1 && plan.Projection[0].Count
	if !countMode {
		for _, projection := range plan.Projection {
			if projection.Count {
				return Result{}, fmt.Errorf("kitdb SQL: COUNT(*) cannot be mixed with row fields")
			}
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
	generation, err := activeRowGeneration(transaction, schema)
	if err != nil {
		return Result{}, err
	}
	if countMode && len(conditions) == 0 {
		if count, found, err := readTableCount(transaction, schema); err != nil {
			return Result{}, err
		} else if found && count <= math.MaxInt64 {
			result := Result{
				Columns:    []Column{{Name: projectionLabel(plan.Projection[0], "count"), Kind: "integer"}},
				CommandTag: "SELECT 1",
			}
			if (!plan.HasLimit || plan.Limit != 0) && plan.Offset == 0 {
				result.Rows = [][]any{{int64(count)}}
			}
			return result, nil
		}
	}
	var columns []Column
	var names []string
	if !countMode {
		columns, names, err = bindProjection(schema, plan.Projection)
		if err != nil {
			return Result{}, err
		}
	}
	sourceWorking := working.child()
	if sourceWorking != nil {
		defer sourceWorking.close()
	}
	retainedFields := selectRetainedFields(names, orders)
	rows, total, overflow, stats, err := transaction.readMatchingRows(
		ctx, schema, generation, conditions, predicate, orders, plan.Offset, limit,
		plan.HasLimit, countMode, plan.Distinct, observe, sourceWorking, retainedFields,
	)
	if err != nil {
		return Result{}, err
	}
	if overflow {
		return Result{}, fmt.Errorf(
			"kitdb SQL: result exceeds %d rows; add a narrower WHERE or explicit LIMIT",
			transaction.engine.maximumResultRows,
		)
	}
	if countMode {
		result := Result{
			Columns:    []Column{{Name: projectionLabel(plan.Projection[0], "count"), Kind: "integer"}},
			CommandTag: "SELECT 1",
			Execution:  stats,
		}
		if (!plan.HasLimit || plan.Limit != 0) && plan.Offset == 0 {
			result.Rows = [][]any{{int64(total)}}
		}
		return result, nil
	}
	projected := make([][]any, 0, len(rows))
	seen := make(map[string]struct{})
	for _, row := range rows {
		values := projectRow(schema, row, names)
		if plan.Distinct {
			encoded, err := json.Marshal(values)
			if err != nil {
				return Result{}, fmt.Errorf("kitdb SQL: encode DISTINCT row: %w", err)
			}
			identity := string(encoded)
			if _, duplicate := seen[identity]; duplicate {
				continue
			}
			if err := working.reserve(len(identity)+64, "DISTINCT identities"); err != nil {
				return Result{}, err
			}
			seen[identity] = struct{}{}
		}
		// Source scratch is released when this executor returns, while result
		// values remain live. Account their complete retained payload before
		// transferring ownership to the caller.
		if err := working.reserveRow(values, "projected result rows"); err != nil {
			return Result{}, err
		}
		projected = append(projected, values)
	}
	if plan.Distinct {
		if plan.Offset >= len(projected) {
			projected = projected[:0]
		} else {
			projected = projected[plan.Offset:]
		}
		if len(projected) > limit {
			projected = projected[:limit]
		}
	}
	return Result{
		Columns: columns, Rows: projected,
		CommandTag: fmt.Sprintf("SELECT %d", len(projected)),
		Execution:  stats,

		materializationWorkingAccounted: working != nil,
	}, nil
}

func (transaction *Transaction) readMatchingRows(
	ctx context.Context,
	schema kitdbsql.Schema,
	generation uint64,
	conditions []boundCondition,
	predicate *boundPredicate,
	orders []boundOrder,
	offset, limit int,
	hasLimit, countMode, distinct bool,
	observe bool,
	working *materializationWorkingSet,
	retainedFields []string,
) ([]map[string]any, int, bool, *ExecutionStats, error) {
	access, err := transaction.planRowAccess(schema, generation, conditions, orders)
	if err != nil {
		return nil, 0, false, nil, err
	}
	stats := rowAccessExecutionStats(access, observe)
	ordered := len(orders) != 0 && !access.orderCovered
	useTopN := ordered && hasLimit && !countMode && !distinct
	if hasLimit && limit == 0 && !countMode {
		return nil, 0, false, stats, nil
	}
	var topRows *boundedOrderedRows
	if useTopN {
		if offset > transaction.engine.maximumResultRows-limit {
			return nil, 0, false, nil, fmt.Errorf(
				"kitdb SQL: ORDER BY OFFSET plus LIMIT exceeds the bounded %d-row working set",
				transaction.engine.maximumResultRows,
			)
		}
		topRows = &boundedOrderedRows{
			orders: orders,
			rows:   make([]boundedOrderedRow, 0, offset+limit),
		}
	}
	rows := make([]map[string]any, 0, min(limit, 256))
	matched := 0
	overflow := false
	decoder := newProjectedRowDecoder(
		schema, selectDecodeTags(schema, conditions, predicate, retainedFields),
	)
	err = transaction.walkAccessRowsObserved(access, stats, func(_, encoded []byte) (bool, error) {
		if matched&255 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		decoded, err := decoder.decode(encoded)
		if err != nil {
			return false, err
		}
		if !matchesAll(decoded.values, conditions) {
			return false, nil
		}
		matchedPredicate, err := predicateMatches(decoded.values, predicate)
		if err != nil {
			return false, err
		}
		if !matchedPredicate {
			return false, nil
		}
		matched++
		if stats != nil {
			stats.RowsMatched++
		}
		if countMode {
			return false, nil
		}
		if useTopN {
			candidate := boundedOrderedRow{values: decoded.values, sequence: matched - 1}
			if topRows.Len() < offset+limit {
				retained, retainedBytes, err := retainSelectRow(
					working, decoded.values, retainedFields, 0, "ORDER BY Top-N candidates",
				)
				if err != nil {
					return false, err
				}
				candidate.values = retained
				candidate.retainedBytes = retainedBytes
				heap.Push(topRows, candidate)
			} else if compareBoundedOrderedRows(orders, candidate, topRows.rows[0]) < 0 {
				retained, retainedBytes, err := retainSelectRow(
					working, decoded.values, retainedFields, topRows.rows[0].retainedBytes,
					"ORDER BY Top-N candidates",
				)
				if err != nil {
					return false, err
				}
				candidate.values = retained
				candidate.retainedBytes = retainedBytes
				topRows.rows[0] = candidate
				heap.Fix(topRows, 0)
			}
			return false, nil
		}
		if ordered || distinct {
			if len(rows) >= transaction.engine.maximumResultRows {
				overflow = true
				return true, nil
			}
			retained, _, err := retainSelectRow(
				working, decoded.values, retainedFields, 0, "ORDER BY/DISTINCT source rows",
			)
			if err != nil {
				return false, err
			}
			rows = append(rows, retained)
			return false, nil
		}
		if matched <= offset {
			return false, nil
		}
		if len(rows) < limit {
			retained, _, err := retainSelectRow(
				working, decoded.values, retainedFields, 0, "SELECT source rows",
			)
			if err != nil {
				return false, err
			}
			rows = append(rows, retained)
			return hasLimit && len(rows) == limit, nil
		}
		if hasLimit {
			return true, nil
		}
		overflow = true
		return true, nil
	})
	if err != nil {
		return nil, 0, false, nil, err
	}
	if useTopN {
		sort.Slice(topRows.rows, func(left, right int) bool {
			return compareBoundedOrderedRows(orders, topRows.rows[left], topRows.rows[right]) < 0
		})
		start := min(offset, len(topRows.rows))
		end := min(start+limit, len(topRows.rows))
		rows = make([]map[string]any, end-start)
		for index, item := range topRows.rows[start:end] {
			rows[index] = item.values
		}
		return rows, matched, false, stats, nil
	}
	if ordered {
		sort.SliceStable(rows, func(left, right int) bool {
			for _, order := range orders {
				comparison := compareFieldValues(order.field, rows[left][order.field.Name], rows[right][order.field.Name])
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
		if !distinct {
			if offset >= len(rows) {
				rows = rows[:0]
			} else {
				rows = rows[offset:]
			}
			if len(rows) > limit {
				rows = rows[:limit]
			}
		}
	}
	return rows, matched, overflow, stats, nil
}

func selectRetainedFields(projected []string, orders []boundOrder) []string {
	fields := make([]string, 0, len(projected)+len(orders))
	for _, field := range projected {
		if !containsFieldName(fields, field) {
			fields = append(fields, field)
		}
	}
	for _, order := range orders {
		field := order.field.Name
		if !containsFieldName(fields, field) {
			fields = append(fields, field)
		}
	}
	return fields
}

func containsFieldName(fields []string, name string) bool {
	for _, field := range fields {
		if field == name {
			return true
		}
	}
	return false
}

func selectDecodeTags(
	schema kitdbsql.Schema,
	conditions []boundCondition,
	predicate *boundPredicate,
	retainedFields []string,
) map[uint32]struct{} {
	tags := make(map[uint32]struct{}, len(conditions)+len(retainedFields))
	for _, condition := range conditions {
		tags[condition.field.Tag] = struct{}{}
	}
	for _, name := range retainedFields {
		_, field, found := schema.FieldByName(name)
		if found {
			tags[field.Tag] = struct{}{}
		}
	}
	collectPredicateTags(tags, predicate)
	return tags
}

func collectPredicateTags(tags map[uint32]struct{}, predicate *boundPredicate) {
	if predicate == nil {
		return
	}
	if predicate.field != nil {
		tags[predicate.field.Tag] = struct{}{}
	}
	for _, argument := range predicate.arguments {
		collectPredicateTags(tags, argument)
	}
}

// retainSelectRow admits the smallest map needed after filtering. It keeps the
// normal non-materialized path allocation-free and reserves before allocating
// a narrowed retained map.
func retainSelectRow(
	working *materializationWorkingSet,
	row map[string]any,
	fields []string,
	previous int,
	operation string,
) (map[string]any, int, error) {
	if working == nil {
		return row, 0, nil
	}
	complete := len(fields) == len(row)
	if complete {
		for _, field := range fields {
			if _, found := row[field]; !found {
				complete = false
				break
			}
		}
	}
	if complete {
		bytes := materializedMapBytes(row)
		if err := working.replace(previous, bytes, operation); err != nil {
			return nil, 0, err
		}
		return row, bytes, nil
	}
	bytes := materializedSelectedMapBytes(row, fields)
	if err := working.replace(previous, bytes, operation); err != nil {
		return nil, 0, err
	}
	retained := make(map[string]any, len(fields))
	for _, field := range fields {
		retained[field] = row[field]
	}
	return retained, bytes, nil
}

func materializedSelectedMapBytes(row map[string]any, fields []string) int {
	bytes := 64 + len(fields)*48
	nodes := 0
	for _, field := range fields {
		bytes += len(field) + materializedValueBytesBounded(row[field], 0, &nodes)
		if bytes > maximumMaterializedBytes {
			return maximumMaterializedBytes + 1
		}
	}
	return bytes
}

func compareBoundedOrderedRows(orders []boundOrder, left, right boundedOrderedRow) int {
	for _, order := range orders {
		comparison := compareFieldValues(
			order.field,
			left.values[order.field.Name],
			right.values[order.field.Name],
		)
		if comparison == 0 {
			continue
		}
		if order.descending {
			return -comparison
		}
		return comparison
	}
	if left.sequence < right.sequence {
		return -1
	}
	if left.sequence > right.sequence {
		return 1
	}
	return 0
}

func bindConditions(schema kitdbsql.Schema, plans []kitdbsql.Condition, parameters []any) ([]boundCondition, error) {
	conditions := make([]boundCondition, len(plans))
	for index, plan := range plans {
		_, field, found := schema.FieldByName(unqualifiedColumn(plan.Column))
		if !found {
			return nil, fmt.Errorf("kitdb SQL: table %q has no field %q", schema.Name, plan.Column)
		}
		item, err := resolveLiteral(plan.Value, parameters)
		if err != nil {
			return nil, err
		}
		if plan.Operator != "is" && plan.Operator != "is not" {
			item, err = coerceField(field, item)
			if err != nil {
				return nil, err
			}
		}
		conditions[index] = boundCondition{field: field, operator: plan.Operator, value: item}
	}
	return conditions, nil
}

func bindOrders(schema kitdbsql.Schema, plans []kitdbsql.Order) ([]boundOrder, error) {
	orders := make([]boundOrder, len(plans))
	for index, plan := range plans {
		_, field, found := schema.FieldByName(unqualifiedColumn(plan.Column))
		if !found {
			return nil, fmt.Errorf("kitdb SQL: table %q has no ORDER BY field %q", schema.Name, plan.Column)
		}
		orders[index] = boundOrder{field: field, descending: plan.Descending}
	}
	return orders, nil
}

func matchesAll(row map[string]any, conditions []boundCondition) bool {
	for _, condition := range conditions {
		item := row[condition.field.Name]
		comparison := compareFieldValues(condition.field, item, condition.value)
		switch condition.operator {
		case "=":
			if comparison != 0 || item == nil || condition.value == nil {
				return false
			}
		case "!=", "<>":
			if comparison == 0 || item == nil || condition.value == nil {
				return false
			}
		case "<":
			if item == nil || condition.value == nil || comparison >= 0 {
				return false
			}
		case "<=":
			if item == nil || condition.value == nil || comparison > 0 {
				return false
			}
		case ">":
			if item == nil || condition.value == nil || comparison <= 0 {
				return false
			}
		case ">=":
			if item == nil || condition.value == nil || comparison < 0 {
				return false
			}
		case "is":
			if item != nil {
				return false
			}
		case "is not":
			if item == nil {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func compareValues(left, right any) int {
	if left == nil || right == nil {
		switch {
		case left == nil && right == nil:
			return 0
		case left == nil:
			return -1
		default:
			return 1
		}
	}
	if comparison, numeric := compareRuntimeNumbers(left, right); numeric {
		return comparison
	}
	switch current := left.(type) {
	case exactTemporal:
		if other, ok := right.(exactTemporal); ok {
			return compareExactTemporals(current, other)
		}
	case bool:
		other, ok := right.(bool)
		if ok {
			if current == other {
				return 0
			}
			if !current {
				return -1
			}
			return 1
		}
	case string:
		if other, ok := right.(string); ok {
			return strings.Compare(current, other)
		}
	case []byte:
		if other, ok := right.([]byte); ok {
			return bytes.Compare(current, other)
		}
	case time.Time:
		if other, ok := right.(time.Time); ok {
			if current.Before(other) {
				return -1
			}
			if current.After(other) {
				return 1
			}
			return 0
		}
	}
	leftText, rightText := fmt.Sprint(left), fmt.Sprint(right)
	return strings.Compare(leftText, rightText)
}

func compareFieldValues(field kitdbsql.Field, left, right any) int {
	typeInfo, found := kitdbsql.LookupKind(field.Kind)
	if found && exactCharacterField(field) {
		if comparison, comparable := compareCharacterField(field, left, right); comparable {
			return comparison
		}
	}
	if found && exactTemporalFieldKind(field.Kind) {
		if comparison, comparable := compareTemporalField(field, left, right); comparable {
			return comparison
		}
	}
	if found && typeInfo.Family == kitdbsql.FamilyDecimal {
		leftText, leftOK := decimalComparableText(left)
		rightText, rightOK := decimalComparableText(right)
		if leftOK && rightOK {
			return compareCanonicalDecimals(leftText, rightText)
		}
	}
	return compareValues(left, right)
}

func decimalComparableText(item any) (string, bool) {
	var text string
	switch current := item.(type) {
	case string:
		text = current
	case exactDecimal:
		text = string(current)
	case json.Number:
		text = current.String()
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		integer, err := integerValue(item)
		if err != nil {
			return "", false
		}
		text = strconv.FormatInt(integer, 10)
	case float32:
		text = strconv.FormatFloat(float64(current), 'g', -1, 32)
	case float64:
		text = strconv.FormatFloat(current, 'g', -1, 64)
	default:
		return "", false
	}
	canonical, err := canonicalDecimal(text)
	return canonical, err == nil
}

func compareCanonicalDecimals(left, right string) int {
	leftCanonical, leftErr := canonicalDecimal(left)
	rightCanonical, rightErr := canonicalDecimal(right)
	if leftErr != nil || rightErr != nil {
		return strings.Compare(left, right)
	}
	leftNegative := strings.HasPrefix(leftCanonical, "-")
	rightNegative := strings.HasPrefix(rightCanonical, "-")
	if leftNegative != rightNegative {
		if leftNegative {
			return -1
		}
		return 1
	}
	if leftNegative {
		leftCanonical = leftCanonical[1:]
		rightCanonical = rightCanonical[1:]
	}
	comparison := compareDecimalMagnitudes(leftCanonical, rightCanonical)
	if leftNegative {
		return -comparison
	}
	return comparison
}

func compareDecimalMagnitudes(left, right string) int {
	leftInteger, leftFraction := decimalParts(left)
	rightInteger, rightFraction := decimalParts(right)
	if len(leftInteger) != len(rightInteger) {
		if len(leftInteger) < len(rightInteger) {
			return -1
		}
		return 1
	}
	if comparison := strings.Compare(leftInteger, rightInteger); comparison != 0 {
		return comparison
	}
	width := max(len(leftFraction), len(rightFraction))
	for index := 0; index < width; index++ {
		leftDigit, rightDigit := byte('0'), byte('0')
		if index < len(leftFraction) {
			leftDigit = leftFraction[index]
		}
		if index < len(rightFraction) {
			rightDigit = rightFraction[index]
		}
		if leftDigit < rightDigit {
			return -1
		}
		if leftDigit > rightDigit {
			return 1
		}
	}
	return 0
}

func decimalParts(text string) (string, string) {
	if marker := strings.IndexByte(text, '.'); marker >= 0 {
		return text[:marker], text[marker+1:]
	}
	return text, ""
}

func projectionLabel(projection kitdbsql.Projection, fallback string) string {
	if projection.Alias != "" {
		return projection.Alias
	}
	return fallback
}

func unqualifiedColumn(requested string) string {
	if marker := strings.LastIndex(requested, "."); marker >= 0 {
		return requested[marker+1:]
	}
	return requested
}
