package relational

import (
	"context"
	"fmt"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

// aggregateStream retains group states, not input rows. Row scans and search
// matches share the same numeric, NULL, grouping and output semantics.
type aggregateStream struct {
	fields        []kitdbsql.Field
	bindings      []aggregateBinding
	groups        map[string]*aggregateGroup
	order         []string
	maximumGroups int
	decimalStates int
	working       *materializationWorkingSet
}

func newAggregateStream(fields []kitdbsql.Field, bindings []aggregateBinding, maximumGroups int, working *materializationWorkingSet) (*aggregateStream, error) {
	stream := &aggregateStream{fields: fields, bindings: bindings, maximumGroups: maximumGroups,
		working: working, groups: make(map[string]*aggregateGroup)}
	for _, binding := range bindings {
		if exactDecimalAggregate(binding.field, binding.function) {
			stream.decimalStates++
		}
	}
	if len(fields) == 0 {
		if _, err := stream.group("", nil); err != nil {
			return nil, err
		}
	}
	return stream, nil
}

func (stream *aggregateStream) group(key string, row map[string]any) (*aggregateGroup, error) {
	if group := stream.groups[key]; group != nil {
		return group, nil
	}
	if len(stream.groups) >= stream.maximumGroups {
		return nil, fmt.Errorf("kitdb SQL: GROUP BY exceeds this server's %d-group limit", stream.maximumGroups)
	}
	if stream.decimalStates != 0 && len(stream.groups) >= maximumDecimalGroupStateBytes/(stream.decimalStates*(maximumDecimalText+128)) {
		return nil, fmt.Errorf("kitdb SQL: decimal GROUP BY exceeds the %d-byte exact aggregate state budget", maximumDecimalGroupStateBytes)
	}
	if err := stream.working.reserve(aggregateGroupWorkingBytes(key, row, stream.fields, stream.bindings, false), "GROUP BY state"); err != nil {
		return nil, err
	}
	group := &aggregateGroup{values: aggregateGroupValues(row, stream.fields), states: make([]aggregateState, len(stream.bindings))}
	stream.groups[key] = group
	stream.order = append(stream.order, key)
	return group, nil
}

func (stream *aggregateStream) add(row map[string]any) error {
	key, err := aggregateGroupKey(row, stream.fields)
	if err != nil {
		return err
	}
	group, err := stream.group(key, row)
	if err != nil {
		return err
	}
	for i, binding := range stream.bindings {
		if binding.function == "" {
			continue
		}
		var value any
		if binding.field != nil {
			value = row[binding.field.Name]
		}
		if err := updateAggregateStateAccounted(stream.working, &group.states[i], binding.function, value, binding.field, binding.field == nil); err != nil {
			return err
		}
	}
	return nil
}

func (stream *aggregateStream) finish(ctx context.Context, columns []Column, plan *kitdbsql.SelectStatement, parameters []any, stats *ExecutionStats) (Result, error) {
	rows := make([][]any, 0, len(stream.order))
	for position, key := range stream.order {
		if position&255 == 0 {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
		}
		group := stream.groups[key]
		row := make([]any, len(stream.bindings))
		for i, binding := range stream.bindings {
			if binding.function == "" {
				row[i] = readField(*binding.field, group.values[binding.field.Name])
				continue
			}
			value, err := aggregateStateValue(group.states[i], binding.function, binding.field)
			if err != nil {
				return Result{}, err
			}
			row[i] = value
		}
		if err := stream.working.reserveRow(row, "GROUP BY result rows"); err != nil {
			return Result{}, err
		}
		rows = append(rows, row)
	}
	if stats != nil {
		stats.Groups = uint64(len(stream.groups))
	}
	return finishAggregateRows(ctx, rows, columns, plan, parameters, stats, stream.working)
}
