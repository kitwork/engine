package relational

import (
	"context"
	"fmt"
	"math"

	"github.com/kitwork/engine/kitdb/columnar"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type batchFilter struct {
	column   int
	operator string
	integer  int64
	number   float64
	text     string
	field    kitdbsql.Field
	null     bool
}

type batchAggregateState struct {
	count   int64
	sum     float64
	integer int64
	number  float64
	text    string
	has     bool
	exact   *integerSum
}

type batchCoverage byte

const (
	batchCoverageNone batchCoverage = iota
	batchCoveragePartial
	batchCoverageAll
)

func batchBlockCoverage(block *columnar.BlockStatistics, filters []batchFilter) batchCoverage {
	coverage := batchCoverageAll
	for _, filter := range filters {
		if filter.column < 0 || filter.column >= len(block.Columns) {
			return batchCoveragePartial
		}
		current := batchFilterCoverage(block.Columns[filter.column], block.Rows, filter)
		if current == batchCoverageNone {
			return batchCoverageNone
		}
		if current == batchCoveragePartial {
			coverage = batchCoveragePartial
		}
	}
	return coverage
}

func batchFilterCoverage(statistics columnar.ColumnStatistics, rows int, filter batchFilter) batchCoverage {
	switch filter.operator {
	case "is null":
		switch statistics.Nulls {
		case 0:
			return batchCoverageNone
		case uint32(rows):
			return batchCoverageAll
		default:
			return batchCoveragePartial
		}
	case "is not null":
		switch statistics.Nulls {
		case 0:
			return batchCoverageAll
		case uint32(rows):
			return batchCoverageNone
		default:
			return batchCoveragePartial
		}
	}
	if filter.null || !statistics.HasValue {
		return batchCoverageNone
	}
	if statistics.Field.Kind == columnar.Text {
		return batchCoveragePartial
	}
	minimum, maximum := 0, 0
	if statistics.Field.Kind == columnar.Float {
		if math.IsNaN(filter.number) {
			return batchCoveragePartial
		}
		minimum = compareFloatStatistic(statistics.FloatMin, filter.number)
		maximum = compareFloatStatistic(statistics.FloatMax, filter.number)
	} else {
		minimum = compareIntegerStatistic(statistics.IntegerMin, filter.integer)
		maximum = compareIntegerStatistic(statistics.IntegerMax, filter.integer)
	}
	allValues := statistics.Nulls == 0
	switch filter.operator {
	case "=":
		if minimum > 0 || maximum < 0 {
			return batchCoverageNone
		}
		if allValues && minimum == 0 && maximum == 0 {
			return batchCoverageAll
		}
	case "!=", "<>":
		if minimum == 0 && maximum == 0 {
			return batchCoverageNone
		}
		if allValues && (minimum > 0 || maximum < 0) {
			return batchCoverageAll
		}
	case "<":
		if minimum >= 0 {
			return batchCoverageNone
		}
		if allValues && maximum < 0 {
			return batchCoverageAll
		}
	case "<=":
		if minimum > 0 {
			return batchCoverageNone
		}
		if allValues && maximum <= 0 {
			return batchCoverageAll
		}
	case ">":
		if maximum <= 0 {
			return batchCoverageNone
		}
		if allValues && minimum > 0 {
			return batchCoverageAll
		}
	case ">=":
		if maximum < 0 {
			return batchCoverageNone
		}
		if allValues && minimum >= 0 {
			return batchCoverageAll
		}
	}
	return batchCoveragePartial
}

func compareIntegerStatistic(left, right int64) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func compareFloatStatistic(left, right float64) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func batchStatisticsCanAnswer(bindings []aggregateBinding, positions []int, fields []columnar.Field) bool {
	for index, binding := range bindings {
		position := positions[index]
		switch binding.function {
		case "count":
			continue
		case "min", "max":
			if position < 0 || fields[position].Kind == columnar.Text {
				return false
			}
		case "sum", "avg":
			if position < 0 || fields[position].Kind != columnar.Integer || !exactIntegerAggregate(binding.field, binding.function) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func applyBatchStatistics(states []batchAggregateState, bindings []aggregateBinding, positions []int, block *columnar.BlockStatistics) {
	for index, binding := range bindings {
		state := &states[index]
		position := positions[index]
		if binding.function == "count" {
			if position < 0 {
				state.count += int64(block.Rows)
			} else {
				state.count += int64(block.Rows) - int64(block.Columns[position].Nulls)
			}
			continue
		}
		statistics := block.Columns[position]
		valid := int64(block.Rows) - int64(statistics.Nulls)
		if valid == 0 {
			continue
		}
		switch binding.function {
		case "sum", "avg":
			if state.exact == nil {
				state.exact = new(integerSum)
			}
			state.exact.addSigned128(statistics.IntegerSumHigh, statistics.IntegerSumLow)
		case "min", "max":
			integer, number := statistics.IntegerMin, statistics.FloatMin
			if binding.function == "max" {
				integer, number = statistics.IntegerMax, statistics.FloatMax
			}
			less, greater := integer < state.integer, integer > state.integer
			if statistics.Field.Kind == columnar.Float {
				less, greater = number < state.number, number > state.number
			}
			if !state.has || binding.function == "min" && less || binding.function == "max" && greater {
				state.integer, state.number = integer, number
			}
		}
		state.count += valid
		state.has = true
	}
}

func batchFieldPosition(fields *[]columnar.Field, field kitdbsql.Field) (int, bool) {
	projected, ok := columnarField(field)
	if !ok {
		return 0, false
	}
	for i, existing := range *fields {
		if existing == projected {
			return i, true
		}
	}
	if len(*fields) >= columnar.MaximumColumns {
		return 0, false
	}
	*fields = append(*fields, projected)
	return len(*fields) - 1, true
}

func compileBatchFilters(predicate *boundPredicate, fields *[]columnar.Field, filters *[]batchFilter) bool {
	if predicate == nil {
		return true
	}
	args := predicate.arguments
	if predicate.kind == "binary" && predicate.operator == "and" && len(args) == 2 {
		return compileBatchFilters(args[0], fields, filters) && compileBatchFilters(args[1], fields, filters)
	}
	if predicate.kind == "unary" && len(args) == 1 && args[0].field != nil && (predicate.operator == "is null" || predicate.operator == "is not null") {
		position, ok := batchFieldPosition(fields, *args[0].field)
		if !ok {
			return false
		}
		*filters = append(*filters, batchFilter{column: position, operator: predicate.operator})
		return true
	}
	if predicate.kind != "binary" || len(args) != 2 || args[0].field == nil || args[1].kind != "literal" {
		return false
	}
	switch predicate.operator {
	case "=", "!=", "<>", "<", "<=", ">", ">=":
	default:
		return false
	}
	position, ok := batchFieldPosition(fields, *args[0].field)
	if !ok {
		return false
	}
	filter := batchFilter{column: position, operator: predicate.operator, field: *args[0].field, null: args[1].literal == nil}
	if !filter.null {
		var err error
		switch (*fields)[position].Kind {
		case columnar.Integer:
			filter.integer, err = integerValue(args[1].literal)
		case columnar.Float:
			filter.number, err = floatValue(args[1].literal)
		case columnar.Boolean:
			value, err := booleanValue(args[1].literal)
			if err != nil {
				return false
			}
			if value {
				filter.integer = 1
			}
		case columnar.Text:
			filter.text, err = stringValue(args[1].literal)
		}
		if err != nil {
			return false
		}
	}
	*filters = append(*filters, filter)
	return true
}

func batchPartitionChunkSelector(schema kitdbsql.Schema, fields []columnar.Field, filters []batchFilter) func(projectionChunk) (bool, error) {
	if schema.Partition == nil {
		return nil
	}
	position := -1
	for index, field := range fields {
		if field.Tag == schema.Partition.Field && field.Kind == columnar.Integer {
			position = index
			break
		}
	}
	if position < 0 {
		return nil
	}
	relevant := make([]batchFilter, 0, len(filters))
	for _, filter := range filters {
		if filter.column == position {
			relevant = append(relevant, filter)
		}
	}
	if len(relevant) == 0 {
		return nil
	}
	field := fields[position]
	partition := *schema.Partition
	return func(chunk projectionChunk) (bool, error) {
		if chunk.Partition.Version == 0 {
			return false, fmt.Errorf("kitdb: analytics partition summary is missing")
		}
		summary := chunk.Partition
		statistics := columnar.ColumnStatistics{
			Field: field, Nulls: uint32(summary.Nulls), HasValue: summary.HasValue,
			IntegerMin: summary.Minimum, IntegerMax: summary.Maximum,
		}
		for _, filter := range relevant {
			if batchFilterCoverage(statistics, int(chunk.Rows), filter) == batchCoverageNone {
				return false, nil
			}
			if partition.Strategy == "hash" && filter.operator == "=" && !filter.null {
				bucket, err := kitdbsql.IntegerPartitionBucket(filter.integer, partition.Buckets)
				if err != nil {
					return false, err
				}
				if summary.HashMask&(uint64(1)<<bucket) == 0 {
					return false, nil
				}
			}
		}
		return true, nil
	}
}

func batchMatches(batch *columnar.Batch, row int, filters []batchFilter) bool {
	for _, filter := range filters {
		v := &batch.Columns[filter.column]
		null := v.Valid[row] == 0
		if filter.operator == "is null" {
			if !null {
				return false
			}
			continue
		}
		if filter.operator == "is not null" {
			if null {
				return false
			}
			continue
		}
		if null || filter.null {
			return false
		}
		comparison := 0
		switch v.Field.Kind {
		case columnar.Float:
			if v.Floats[row] < filter.number {
				comparison = -1
			} else if v.Floats[row] > filter.number {
				comparison = 1
			}
		case columnar.Text:
			comparison = compareFieldValues(filter.field, v.Texts[row], filter.text)
		default:
			if v.Integers[row] < filter.integer {
				comparison = -1
			} else if v.Integers[row] > filter.integer {
				comparison = 1
			}
		}
		switch filter.operator {
		case "=":
			if comparison != 0 {
				return false
			}
		case "!=", "<>":
			if comparison == 0 {
				return false
			}
		case "<":
			if comparison >= 0 {
				return false
			}
		case "<=":
			if comparison > 0 {
				return false
			}
		case ">":
			if comparison <= 0 {
				return false
			}
		case ">=":
			if comparison < 0 {
				return false
			}
		}
	}
	return true
}

func (transaction *Transaction) executeBatchAggregate(ctx context.Context, schema kitdbsql.Schema, plan *kitdbsql.SelectStatement, columns []Column, bindings []aggregateBinding, predicate *boundPredicate, generation uint64, observe bool, working *materializationWorkingSet) (Result, bool, error) {
	if len(plan.GroupBy) != 0 || plan.Having != nil || len(plan.Order) != 0 || len(transaction.operations) != 0 {
		return Result{}, false, nil
	}
	// A direct parser plan with Conditions but no predicate keeps the scalar
	// path. Parsed SQL carries the complete predicate, including residuals.
	if predicate == nil && len(plan.Conditions) != 0 {
		return Result{}, false, nil
	}
	fields := []columnar.Field{}
	positions := make([]int, len(bindings))
	for i, binding := range bindings {
		if binding.function == "" {
			return Result{}, false, nil
		}
		positions[i] = -1
		if binding.field != nil {
			position, ok := batchFieldPosition(&fields, *binding.field)
			if !ok {
				return Result{}, false, nil
			}
			if binding.function != "" && fields[position].Kind == columnar.Text && binding.function != "count" && binding.function != "min" && binding.function != "max" {
				return Result{}, false, nil
			}
			positions[i] = position
		}
	}
	var filters []batchFilter
	if !compileBatchFilters(predicate, &fields, &filters) {
		return Result{}, false, nil
	}
	if len(fields) == 0 {
		available := columnarFields(schema)
		if len(available) == 0 {
			return Result{}, false, nil
		}
		fields = available[:1]
	}
	states := make([]batchAggregateState, len(bindings))
	statisticsCanAnswer := batchStatisticsCanAnswer(bindings, positions, fields)
	selected := make([]int, columnar.BatchRows)
	decide := func(block *columnar.BlockStatistics) (columnar.BlockAction, error) {
		switch batchBlockCoverage(block, filters) {
		case batchCoverageNone:
			return columnar.SkipBlock, nil
		case batchCoverageAll:
			if statisticsCanAnswer {
				applyBatchStatistics(states, bindings, positions, block)
				return columnar.UseBlockStatistics, nil
			}
		}
		return columnar.ScanBlock, nil
	}
	consume := func(batch *columnar.Batch) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := 0
		for row := range batch.Rows {
			if batchMatches(batch, row, filters) {
				selected[n] = row
				n++
			}
		}
		for i, binding := range bindings {
			state := &states[i]
			if positions[i] < 0 {
				state.count += int64(n)
				continue
			}
			v := &batch.Columns[positions[i]]
			exact := exactIntegerAggregate(binding.field, binding.function)
			for _, row := range selected[:n] {
				state.add(v, row, binding.function, exact, binding.field)
			}
		}
		return nil
	}
	stats, err := transaction.scanAggregateBatches(
		ctx, schema, generation, fields,
		batchPartitionChunkSelector(schema, fields, filters), decide, consume,
		func() { clear(states) }, observe,
	)
	if err != nil {
		return Result{}, true, err
	}
	row := make([]any, len(bindings))
	for i, binding := range bindings {
		kind := columnar.Integer
		if positions[i] >= 0 {
			kind = fields[positions[i]].Kind
		}
		row[i], err = states[i].value(binding.function, kind, binding.field)
		if err != nil {
			return Result{}, true, err
		}
	}
	if err := working.reserveRow(row, "aggregate result row"); err != nil {
		return Result{}, true, err
	}
	result, err := finishAggregateRows(ctx, [][]any{row}, columns, plan, nil, stats, working)
	return result, true, err
}

func (state *batchAggregateState) add(v *columnar.Vector, row int, function string, exact bool, field *kitdbsql.Field) {
	if v.Valid[row] == 0 {
		return
	}
	integer, number := int64(0), float64(0)
	switch v.Field.Kind {
	case columnar.Float:
		number = v.Floats[row]
	case columnar.Text:
		value := v.Texts[row]
		if function == "count" {
			state.count++
			state.has = true
			return
		}
		if function != "min" && function != "max" {
			return
		}
		comparison := 0
		if state.has {
			if field != nil {
				comparison = compareFieldValues(*field, value, state.text)
			} else {
				comparison = compareValues(value, state.text)
			}
		}
		if !state.has || function == "min" && comparison < 0 || function == "max" && comparison > 0 {
			state.text = value
		}
		state.count++
		state.has = true
		return
	default:
		integer = v.Integers[row]
		number = float64(integer)
	}
	switch function {
	case "sum", "avg":
		if exact {
			if state.exact == nil {
				state.exact = new(integerSum)
			}
			state.exact.add(integer)
		} else {
			state.sum += number
		}
	case "min", "max":
		less, greater := integer < state.integer, integer > state.integer
		if v.Field.Kind == columnar.Float {
			less, greater = number < state.number, number > state.number
		}
		if !state.has || function == "min" && less || function == "max" && greater {
			state.integer, state.number = integer, number
		}
	}
	state.count++
	state.has = true
}

func (state batchAggregateState) value(function string, kind columnar.Kind, field *kitdbsql.Field) (any, error) {
	if function == "count" {
		return state.count, nil
	}
	if !state.has {
		return nil, nil
	}
	if state.exact != nil {
		return state.exact.aggregateResult(state.count, function, field)
	}
	switch function {
	case "sum":
		return state.sum, nil
	case "avg":
		return state.sum / float64(state.count), nil
	case "min", "max":
		switch kind {
		case columnar.Float:
			return state.number, nil
		case columnar.Boolean:
			return state.integer != 0, nil
		case columnar.Text:
			return state.text, nil
		default:
			return state.integer, nil
		}
	}
	return nil, nil
}
