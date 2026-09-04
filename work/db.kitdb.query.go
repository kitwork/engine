package work

import (
	"fmt"
	"math"
	"strings"

	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

type kitDBAggregateRequest struct {
	operation string
	field     string
	label     string
}

type kitDBAggregateState struct {
	request kitDBAggregateRequest
	spec    *ColumnSpec
	count   uint64
	number  float64
	value   value.Value
	has     bool
}

// Explain reports the exact access path consumed by KitDB reads. It deliberately
// reports no guessed cost or row count: those would be fiction without statistics.
func (t *SchemaTable) Explain(_ ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	if t.engine != "kitdb" {
		return value.Value{K: value.Invalid, V: "db.explain: currently available only for KitDB"}
	}
	plan := t.builder().ExecutionPlan()
	access, err := t.planKitDBAccess(plan)
	if err != nil {
		return kitDBError(err)
	}
	fields := make([]value.Value, len(access.fields))
	for index, field := range access.fields {
		fields[index] = value.New(field)
	}
	index := value.NewNil()
	if access.name != "" {
		index = value.New(access.name)
	}
	offset := plan.Offset
	if offset < 0 {
		offset = 0
	}
	sortMode := "none"
	if len(plan.Orders) != 0 {
		if access.orderCovered {
			sortMode = "index"
		} else {
			sortMode = "memory"
		}
	}
	rangeField := value.NewNil()
	if access.rangeField != "" {
		rangeField = value.New(access.rangeField)
	}
	source := "snapshot"
	if t.transaction != nil {
		source = "transaction"
	}
	direction := "forward"
	if access.kind == kitDBAccessPrimary || access.kind == kitDBAccessUnique {
		direction = "point"
	} else if access.options.Reverse {
		direction = "reverse"
	}
	estimatedRows := value.NewNil()
	estimateKind := "unavailable"
	if access.hasEstimate {
		estimatedRows = value.New(access.estimatedRows)
		estimateKind = access.estimateKind
	}
	statisticsTransaction := value.NewNil()
	if access.statisticsTransaction != 0 {
		statisticsTransaction = value.New(fmt.Sprintf("%d", access.statisticsTransaction))
	}
	return value.New(map[string]value.Value{
		"engine":                value.New("kitdb"),
		"struct":                value.New(t.table),
		"access":                value.New(string(access.kind)),
		"index":                 index,
		"fields":                value.New(fields),
		"equalityPrefix":        value.New(access.equalityPrefix),
		"rangeField":            rangeField,
		"residualFilter":        value.New(kitDBPlanHasFilter(plan)),
		"sort":                  value.New(sortMode),
		"orderCovered":          value.New(access.orderCovered),
		"earlyStop":             value.New(len(plan.Orders) == 0 || access.orderCovered),
		"limit":                 value.New(kitDBEffectiveLimit(plan, 0)),
		"offset":                value.New(offset),
		"source":                value.New(source),
		"direction":             value.New(direction),
		"statistics":            value.New(access.statisticsState),
		"statisticsTransaction": statisticsTransaction,
		"estimatedRows":         estimatedRows,
		"estimateKind":          value.New(estimateKind),
	})
}

func (t *SchemaTable) Sum(field string) value.Value {
	return t.schemaAggregate("sum", field)
}

func (t *SchemaTable) Avg(field string) value.Value {
	return t.schemaAggregate("avg", field)
}

func (t *SchemaTable) Min(field string) value.Value {
	return t.schemaAggregate("min", field)
}

func (t *SchemaTable) Max(field string) value.Value {
	return t.schemaAggregate("max", field)
}

func (t *SchemaTable) schemaAggregate(operation, field string) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	spec := t.columns[field]
	if spec == nil {
		return value.Value{
			K: value.Invalid,
			V: fmt.Sprintf("db.%s: table %q has no column %q", operation, t.table, field),
		}
	}
	if t.engine == "kitdb" {
		results, err := t.kitDBAggregates([]kitDBAggregateRequest{{operation: operation, field: field}})
		if err != nil {
			return kitDBError(err)
		}
		return results[0]
	}
	var result value.Value
	switch operation {
	case "sum":
		result = t.builder().Sum(field)
	case "avg":
		result = t.builder().Avg(field)
	case "min":
		result = t.builder().Min(field)
	case "max":
		result = t.builder().Max(field)
	default:
		return value.Value{K: value.Invalid, V: "db.aggregate: unsupported operation"}
	}
	if operation == "min" || operation == "max" {
		return coerceRead(spec.kind, result)
	}
	return result
}

// kitDBAggregates evaluates every scalar aggregate in one streaming pass. It
// does not inherit list()'s 60/120-row presentation bounds and retains O(1)
// memory per requested aggregate.
func (t *SchemaTable) kitDBAggregates(requests []kitDBAggregateRequest) ([]value.Value, error) {
	states, err := t.prepareKitDBAggregateStates(requests)
	if err != nil {
		return nil, err
	}

	plan := kitDBAggregateExecutionPlan(t.builder().ExecutionPlan())
	err = t.scanKitDBRows(plan, func(row kitDBStoredRow) (bool, error) {
		return false, applyKitDBAggregateStates(states, row.values)
	})
	if err != nil {
		return nil, err
	}
	return finishKitDBAggregateStates(states), nil
}

func (t *SchemaTable) prepareKitDBAggregateStates(requests []kitDBAggregateRequest) ([]kitDBAggregateState, error) {
	return prepareKitDBAggregateStates(requests, func(field string) (*ColumnSpec, bool) {
		spec := t.columns[field]
		return spec, spec != nil
	}, fmt.Sprintf("struct %q", t.table))
}

func prepareKitDBAggregateStates(
	requests []kitDBAggregateRequest,
	resolve func(string) (*ColumnSpec, bool),
	source string,
) ([]kitDBAggregateState, error) {
	if len(requests) == 0 {
		return nil, fmt.Errorf("kitdb: aggregate list is empty")
	}
	states := make([]kitDBAggregateState, len(requests))
	for index, request := range requests {
		request.operation = strings.ToLower(strings.TrimSpace(request.operation))
		state := kitDBAggregateState{request: request}
		if request.operation == "count" && request.field == "*" {
			states[index] = state
			continue
		}
		spec, found := resolve(request.field)
		if !found || spec == nil {
			return nil, fmt.Errorf("kitdb: %s has no field %q", source, kitDBAggregateLabel(request))
		}
		state.spec = spec
		switch request.operation {
		case "count":
		case "sum", "avg":
			if !kitDBNumericAggregateKind(spec.kind) {
				return nil, fmt.Errorf(
					"kitdb: %s(%s) requires a numeric field, got %s",
					strings.ToUpper(request.operation), kitDBAggregateLabel(request), spec.kind,
				)
			}
		case "min", "max":
			if !kitDBComparableAggregateKind(spec.kind) {
				return nil, fmt.Errorf(
					"kitdb: %s(%s) does not support field kind %s",
					strings.ToUpper(request.operation), kitDBAggregateLabel(request), spec.kind,
				)
			}
		default:
			return nil, fmt.Errorf("kitdb: unsupported aggregate %q", request.operation)
		}
		states[index] = state
	}
	return states, nil
}

func kitDBAggregateLabel(request kitDBAggregateRequest) string {
	if request.label != "" {
		return request.label
	}
	return request.field
}

func applyKitDBAggregateStates(states []kitDBAggregateState, row map[string]value.Value) error {
	for index := range states {
		state := &states[index]
		if state.request.operation == "count" && state.request.field == "*" {
			state.count++
			continue
		}
		item, found := row[state.request.field]
		if !found || item.IsNil() {
			continue
		}
		switch state.request.operation {
		case "count":
			state.count++
		case "sum":
			number, err := kitDBAggregateNumber(state.spec.kind, item)
			if err != nil {
				return fmt.Errorf("kitdb: SUM(%s): %w", kitDBAggregateLabel(state.request), err)
			}
			state.count++
			state.number += number
			if math.IsInf(state.number, 0) || math.IsNaN(state.number) {
				return fmt.Errorf("kitdb: SUM(%s) overflowed", kitDBAggregateLabel(state.request))
			}
		case "avg":
			number, err := kitDBAggregateNumber(state.spec.kind, item)
			if err != nil {
				return fmt.Errorf("kitdb: AVG(%s): %w", kitDBAggregateLabel(state.request), err)
			}
			state.count++
			state.number = kitDBIncrementalMean(state.number, number, state.count)
			if math.IsInf(state.number, 0) || math.IsNaN(state.number) {
				return fmt.Errorf("kitdb: AVG(%s) overflowed", kitDBAggregateLabel(state.request))
			}
		case "min", "max":
			logical := coerceRead(state.spec.kind, item)
			if !state.has ||
				(state.request.operation == "min" && kitDBCompareValues(logical, state.value) < 0) ||
				(state.request.operation == "max" && kitDBCompareValues(logical, state.value) > 0) {
				state.value = logical
				state.has = true
			}
		}
	}
	return nil
}

func finishKitDBAggregateStates(states []kitDBAggregateState) []value.Value {
	results := make([]value.Value, len(states))
	for index, state := range states {
		switch state.request.operation {
		case "count":
			results[index] = value.New(float64(state.count))
		case "sum", "avg":
			if state.count == 0 {
				results[index] = value.NewNil()
			} else {
				results[index] = value.New(state.number)
			}
		case "min", "max":
			if state.has {
				results[index] = state.value
			} else {
				results[index] = value.NewNil()
			}
		}
	}
	return results
}

func kitDBNumericAggregateKind(kind string) bool {
	switch kind {
	case "integer", "smallint", "int32", "float", "decimal", "serial", "year", "month", "day":
		return true
	default:
		return false
	}
}

func kitDBComparableAggregateKind(kind string) bool {
	switch kind {
	case "json", "jsonb", "array", "vector", "blob":
		return false
	default:
		return true
	}
}

func kitDBAggregateNumber(kind string, item value.Value) (float64, error) {
	logical := coerceRead(kind, item)
	if logical.K != value.Number || math.IsNaN(logical.N) || math.IsInf(logical.N, 0) {
		return 0, fmt.Errorf("stored value is not a finite number")
	}
	return logical.N, nil
}

func kitDBIncrementalMean(previous, next float64, count uint64) float64 {
	n := float64(count)
	return previous*((n-1)/n) + next/n
}

func kitDBExplainDetail(table string, access kitDBAccessPlan, plan query.ExecutionPlan) string {
	detail := kitDBExplainAccessDetail(table, access, plan)
	if len(plan.Orders) != 0 {
		if access.orderCovered {
			if access.options.Reverse {
				detail += "; REVERSE INDEX ORDER"
			} else {
				detail += "; INDEX ORDER"
			}
		} else {
			detail += "; TEMP SORT"
		}
	}
	detail += fmt.Sprintf("; LIMIT %d", kitDBEffectiveLimit(plan, 0))
	if plan.Offset > 0 {
		detail += fmt.Sprintf(" OFFSET %d", plan.Offset)
	}
	if len(plan.Orders) == 0 || access.orderCovered {
		detail += "; EARLY STOP"
	}
	return detail
}

func kitDBExplainAggregateDetail(table string, access kitDBAccessPlan, plan query.ExecutionPlan) string {
	return kitDBExplainAccessDetail(table, access, plan) + "; STREAM AGGREGATE"
}

func kitDBExplainAccessDetail(table string, access kitDBAccessPlan, plan query.ExecutionPlan) string {
	var detail string
	switch access.kind {
	case kitDBAccessPrimary:
		detail = fmt.Sprintf("KITDB PRIMARY KEY %s.%s", table, strings.Join(access.fields, ","))
	case kitDBAccessUnique:
		detail = fmt.Sprintf("KITDB UNIQUE %s (%s)", access.name, strings.Join(access.fields, ","))
	case kitDBAccessIndex:
		detail = fmt.Sprintf("KITDB INDEX %s (%s)", access.name, strings.Join(access.fields, ","))
	case kitDBAccessIndexPrefix:
		detail = fmt.Sprintf("KITDB INDEX PREFIX %s (%s)", access.name, strings.Join(access.fields, ","))
	case kitDBAccessIndexRange:
		detail = fmt.Sprintf("KITDB INDEX RANGE %s (%s) ON %s", access.name, strings.Join(access.fields, ","), access.rangeField)
	case kitDBAccessIndexOrder:
		detail = fmt.Sprintf("KITDB INDEX ORDER %s (%s)", access.name, strings.Join(access.fields, ","))
	default:
		detail = "KITDB SCAN " + table
	}
	if kitDBPlanHasFilter(plan) {
		detail += "; FILTER"
	}
	return detail + kitDBExplainStatisticsDetail(access)
}

func kitDBExplainStatisticsDetail(access kitDBAccessPlan) string {
	state := access.statisticsState
	if state == "" {
		state = "missing"
	}
	detail := "; STATS " + strings.ToUpper(state)
	if access.statisticsTransaction != 0 {
		detail += fmt.Sprintf("@%d", access.statisticsTransaction)
	}
	if access.hasEstimate {
		detail += fmt.Sprintf(
			"; ESTIMATE %d ROWS (%s)",
			access.estimatedRows, strings.ToUpper(access.estimateKind),
		)
	}
	return detail
}

func kitDBPlanHasFilter(plan query.ExecutionPlan) bool {
	return len(plan.Conditions) != 0 || plan.Predicate != nil
}

func kitDBAggregateExecutionPlan(plan query.ExecutionPlan) query.ExecutionPlan {
	plan.Orders = nil
	plan.Limit = 0
	plan.Offset = 0
	return plan
}
