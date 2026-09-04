package relational

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbrecord "github.com/kitwork/engine/kitdb/record"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type indexOnlyGroupAccess struct {
	index       secondaryIndex
	prefix      []byte
	groupFields []kitdbsql.Field
	components  int
}

type indexOnlyGroupState struct {
	values []any
	count  int64
}

func (transaction *Transaction) planIndexOnlyGroup(
	schema kitdbsql.Schema,
	plan *kitdbsql.SelectStatement,
	groupFields []kitdbsql.Field,
	bindings []aggregateBinding,
) (indexOnlyGroupAccess, bool, error) {
	if len(groupFields) == 0 || plan == nil || plan.Distinct || plan.Predicate != nil ||
		len(plan.Conditions) != 0 || plan.Search != nil {
		return indexOnlyGroupAccess{}, false, nil
	}
	grouped := make(map[string]struct{}, len(groupFields))
	for _, field := range groupFields {
		if field.ID == "" {
			return indexOnlyGroupAccess{}, false, nil
		}
		if _, duplicate := grouped[field.ID]; duplicate {
			return indexOnlyGroupAccess{}, false, nil
		}
		grouped[field.ID] = struct{}{}
	}
	for _, binding := range bindings {
		if binding.function == "" {
			if binding.field == nil {
				return indexOnlyGroupAccess{}, false, nil
			}
			if _, found := grouped[binding.field.ID]; !found {
				return indexOnlyGroupAccess{}, false, nil
			}
			continue
		}
		if binding.function != "count" || binding.field != nil {
			return indexOnlyGroupAccess{}, false, nil
		}
	}

	indexes, err := collectSecondaryIndexes(schema)
	if err != nil {
		return indexOnlyGroupAccess{}, false, err
	}
	var best indexOnlyGroupAccess
	bestWidth := math.MaxInt
	for _, index := range indexes {
		if len(index.filter) != 0 || len(index.fields) < len(groupFields) {
			continue
		}
		matches := true
		for position, field := range groupFields {
			if index.fields[position].ID != field.ID {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		generation, ready, err := secondaryIndexPhysicalLayout(transaction, schema, index)
		if err != nil {
			return indexOnlyGroupAccess{}, false, err
		}
		if !ready {
			continue
		}
		prefix, err := secondaryIndexBasePrefixForGeneration(schema, index, generation)
		if err != nil {
			return indexOnlyGroupAccess{}, false, err
		}
		if len(index.fields) >= bestWidth {
			continue
		}
		best = indexOnlyGroupAccess{
			index: index, prefix: prefix, groupFields: append([]kitdbsql.Field(nil), groupFields...),
			components: len(index.fields) + len(schema.PrimaryFields()),
		}
		bestWidth = len(index.fields)
	}
	return best, bestWidth != math.MaxInt, nil
}

func (transaction *Transaction) executeIndexOnlyGroup(
	ctx context.Context,
	access indexOnlyGroupAccess,
	plan *kitdbsql.SelectStatement,
	columns []Column,
	bindings []aggregateBinding,
	parameters []any,
	observe bool,
	working *materializationWorkingSet,
) (Result, error) {
	stats := &ExecutionStats{Path: "index-only-group"}
	groups := make([]indexOnlyGroupState, 0, min(transaction.engine.maximumResultRows, 256))
	var currentKey []byte
	var currentValues []any
	var currentCount int64
	groupStateBytes := 0

	flush := func() error {
		if currentCount == 0 {
			return nil
		}
		if len(groups) >= transaction.engine.maximumResultRows {
			return fmt.Errorf(
				"kitdb SQL: GROUP BY exceeds this server's %d-group limit",
				transaction.engine.maximumResultRows,
			)
		}
		entryBytes := indexOnlyGroupEntryBytes(currentKey, bindings)
		if entryBytes > maximumBatchGroupBytes-groupStateBytes {
			return fmt.Errorf(
				"kitdb SQL: GROUP BY exceeds the %d-byte index-only group state budget",
				maximumBatchGroupBytes,
			)
		}
		if err := working.reserve(entryBytes, "GROUP BY state"); err != nil {
			return err
		}
		groupStateBytes += entryBytes
		groups = append(groups, indexOnlyGroupState{values: currentValues, count: currentCount})
		return nil
	}

	var cursorStats kitdbengine.CursorStats
	var observed *kitdbengine.CursorStats
	if observe {
		observed = &cursorStats
	}
	err := transaction.scanKeys(
		kitdbengine.RangeOptions{Prefix: access.prefix}, observed,
		func(key []byte) (bool, error) {
			if stats.IndexEntriesScanned&255 == 0 {
				if err := ctx.Err(); err != nil {
					return false, err
				}
			}
			groupKey, err := indexOnlyGroupKey(key, access.prefix, len(access.groupFields), access.components)
			if err != nil {
				return false, fmt.Errorf("kitdb: index %q: %w", access.index.name, err)
			}
			stats.IndexEntriesScanned++
			stats.RowsMatched++
			if bytes.Equal(groupKey, currentKey) {
				if currentCount == math.MaxInt64 {
					return false, fmt.Errorf("kitdb SQL: GROUP BY count exceeds BIGINT")
				}
				currentCount++
				return false, nil
			}
			if err := flush(); err != nil {
				return false, err
			}
			values, err := decodeIndexOnlyGroupValues(groupKey, access.groupFields)
			if err != nil {
				return false, fmt.Errorf("kitdb: index %q group key: %w", access.index.name, err)
			}
			currentKey = append(currentKey[:0], groupKey...)
			currentValues = values
			currentCount = 1
			return false, nil
		},
	)
	if err != nil {
		return Result{}, err
	}
	stats.addCursorStats(cursorStats)
	if err := flush(); err != nil {
		return Result{}, err
	}
	stats.Groups = uint64(len(groups))
	rows := make([][]any, len(groups))
	for groupPosition, group := range groups {
		if groupPosition&255 == 0 {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
		}
		row := make([]any, len(bindings))
		for position, binding := range bindings {
			if binding.function == "count" {
				row[position] = group.count
				continue
			}
			for valuePosition, field := range access.groupFields {
				if binding.field != nil && binding.field.ID == field.ID {
					row[position] = group.values[valuePosition]
					break
				}
			}
		}
		if err := working.reserveRow(row, "GROUP BY index-only result rows"); err != nil {
			return Result{}, err
		}
		rows[groupPosition] = row
	}
	return finishAggregateRows(ctx, rows, columns, plan, parameters, stats, working)
}

func indexOnlyGroupEntryBytes(key []byte, bindings []aggregateBinding) int {
	bytes := 128 + 2*len(key) + len(bindings)*96
	for _, binding := range bindings {
		if exactIntegerAggregate(binding.field, binding.function) {
			bytes += 128
		}
	}
	return bytes
}

func indexOnlyGroupKey(key, prefix []byte, groupFields, components int) ([]byte, error) {
	if groupFields < 1 || components < groupFields || !bytes.HasPrefix(key, prefix) || len(key) == len(prefix) {
		return nil, fmt.Errorf("invalid index-only key envelope")
	}
	encoded := key[len(prefix):]
	position, groupEnd := 0, 0
	for component := range components {
		size, err := kitdbrecord.OrderedScalarComponentSize(encoded[position:])
		if err != nil {
			return nil, err
		}
		position += size
		if component+1 == groupFields {
			groupEnd = position
		}
	}
	if position != len(encoded) || groupEnd == 0 {
		return nil, fmt.Errorf("invalid index-only key component count")
	}
	return encoded[:groupEnd], nil
}

func decodeIndexOnlyGroupValues(encoded []byte, fields []kitdbsql.Field) ([]any, error) {
	values := make([]any, len(fields))
	position := 0
	for index, field := range fields {
		scalar, size, err := kitdbrecord.DecodeOrderedScalarComponent(encoded[position:])
		if err != nil {
			return nil, err
		}
		position += size
		value, err := indexScalarValue(scalar)
		if err != nil {
			return nil, err
		}
		value, err = coerceField(field, value)
		if err != nil {
			return nil, err
		}
		values[index] = readField(field, value)
	}
	if position != len(encoded) {
		return nil, fmt.Errorf("trailing group key bytes")
	}
	return values, nil
}

func indexScalarValue(scalar kitdbrecord.Scalar) (any, error) {
	switch scalar.Kind {
	case kitdbrecord.ScalarNil:
		return nil, nil
	case kitdbrecord.ScalarBool:
		return scalar.Bool, nil
	case kitdbrecord.ScalarNumber:
		return scalar.Number, nil
	case kitdbrecord.ScalarInteger:
		return scalar.Integer, nil
	case kitdbrecord.ScalarText:
		return scalar.Text, nil
	case kitdbrecord.ScalarBytes:
		return scalar.Bytes, nil
	case kitdbrecord.ScalarTemporal:
		if scalar.Number < math.MinInt64 || scalar.Number > math.MaxInt64 {
			return nil, fmt.Errorf("temporal scalar is outside nanosecond range")
		}
		return time.Unix(0, int64(scalar.Number)).UTC(), nil
	default:
		return nil, fmt.Errorf("unsupported scalar kind %d", scalar.Kind)
	}
}
