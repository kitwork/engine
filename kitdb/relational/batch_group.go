package relational

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/kitwork/engine/kitdb/columnar"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const maximumBatchGroupBytes = 16 << 20

type batchGroup struct {
	values []any
	states []batchAggregateState
}

type batchGroups struct {
	keys      map[string]int
	groups    []batchGroup
	scratch   []byte
	positions []int
	bindings  []aggregateBinding
	maximum   int
	entryCost int
	working   *materializationWorkingSet
	accounted int
}

func newBatchGroups(positions []int, bindings []aggregateBinding, maximum int, working *materializationWorkingSet) *batchGroups {
	entryCost := 128 + len(positions)*16 + len(bindings)*96
	for _, binding := range bindings {
		if exactIntegerAggregate(binding.field, binding.function) {
			// Include the lazy accumulator, big.Int and widened limb capacity.
			entryCost += 128
		}
	}
	return &batchGroups{
		keys: make(map[string]int), scratch: make([]byte, 0, 9*len(positions)),
		positions: positions, bindings: bindings, maximum: maximum,
		// Conservative accounted state budget: key, map/slice overhead, boxed
		// projected values and accumulators. This is not a process RSS quota.
		entryCost: entryCost,
		working:   working,
	}
}

func (groups *batchGroups) reset() {
	groups.working.release(groups.accounted)
	groups.accounted = 0
	clear(groups.keys)
	clear(groups.groups)
	groups.groups = groups.groups[:0]
}

func (groups *batchGroups) resolve(batch *columnar.Batch, row int, projectionPositions []int) (int, error) {
	groups.scratch = groups.scratch[:0]
	for _, position := range groups.positions {
		v := &batch.Columns[position]
		groups.scratch = append(groups.scratch, v.Valid[row])
		if v.Valid[row] == 0 {
			continue
		}
		if v.Field.Kind == columnar.Text {
			text := v.Texts[row]
			var encodedLength [binary.MaxVarintLen64]byte
			lengthBytes := binary.PutUvarint(encodedLength[:], uint64(len(text)))
			maximumKeyBytes := maximumBatchGroupBytes / 2
			if len(groups.scratch) > maximumKeyBytes-lengthBytes || len(text) > maximumKeyBytes-lengthBytes-len(groups.scratch) {
				return 0, fmt.Errorf("kitdb SQL: GROUP BY exceeds the %d-byte batch group state budget", maximumBatchGroupBytes)
			}
			groups.scratch = append(groups.scratch, encodedLength[:lengthBytes]...)
			groups.scratch = append(groups.scratch, text...)
			continue
		}
		groups.scratch = binary.LittleEndian.AppendUint64(groups.scratch, uint64(v.Integers[row]))
	}
	// The conversion for map lookup does not retain/copy scratch. New keys do.
	if position, exists := groups.keys[string(groups.scratch)]; exists {
		return position, nil
	}
	if len(groups.groups) >= groups.maximum {
		return 0, fmt.Errorf("kitdb SQL: GROUP BY exceeds this server's %d-group limit", groups.maximum)
	}
	entryCost := groups.entryCost + 2*len(groups.scratch)
	if entryCost > maximumBatchGroupBytes-groups.accounted {
		return 0, fmt.Errorf("kitdb SQL: GROUP BY exceeds the %d-byte batch group state budget", maximumBatchGroupBytes)
	}
	if err := groups.working.reserve(entryCost, "GROUP BY batch state"); err != nil {
		return 0, err
	}
	groups.accounted += entryCost
	group := batchGroup{values: make([]any, len(groups.bindings)), states: make([]batchAggregateState, len(groups.bindings))}
	for i, binding := range groups.bindings {
		if binding.function != "" {
			continue
		}
		v := &batch.Columns[projectionPositions[i]]
		if v.Valid[row] != 0 {
			switch v.Field.Kind {
			case columnar.Boolean:
				group.values[i] = v.Integers[row] != 0
			case columnar.Text:
				group.values[i] = strings.Clone(v.Texts[row])
			default:
				group.values[i] = v.Integers[row]
			}
		}
	}
	position := len(groups.groups)
	groups.groups = append(groups.groups, group)
	groups.keys[string(groups.scratch)] = position
	return position, nil
}

func (transaction *Transaction) executeBatchGroup(ctx context.Context, schema kitdbsql.Schema, plan *kitdbsql.SelectStatement, columns []Column, bindings []aggregateBinding, groupFields []kitdbsql.Field, predicate *boundPredicate, generation uint64, parameters []any, observe bool, working *materializationWorkingSet) (Result, bool, error) {
	if len(transaction.operations) != 0 || predicate == nil && len(plan.Conditions) != 0 {
		return Result{}, false, nil
	}
	var fields []columnar.Field
	groupPositions := make([]int, len(groupFields))
	for i, field := range groupFields {
		position, ok := batchFieldPosition(&fields, field)
		// Float grouping needs a shared canonical identity contract, including
		// signed zero. Keep scalar semantics for it for now.
		if !ok || fields[position].Kind == columnar.Float {
			return Result{}, false, nil
		}
		groupPositions[i] = position
	}
	positions := make([]int, len(bindings))
	for i, binding := range bindings {
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
	groups := newBatchGroups(groupPositions, bindings, transaction.engine.maximumResultRows, working)
	selected, groupIDs := make([]int, columnar.BatchRows), make([]int, columnar.BatchRows)
	decide := func(block *columnar.BlockStatistics) (columnar.BlockAction, error) {
		if batchBlockCoverage(block, filters) == batchCoverageNone {
			return columnar.SkipBlock, nil
		}
		return columnar.ScanBlock, nil
	}
	consume := func(batch *columnar.Batch) error {
		n := 0
		for row := range batch.Rows {
			if row&255 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if !batchMatches(batch, row, filters) {
				continue
			}
			group, err := groups.resolve(batch, row, positions)
			if err != nil {
				return err
			}
			selected[n], groupIDs[n] = row, group
			n++
		}
		for i, binding := range bindings {
			if binding.function == "" {
				continue
			}
			for index, row := range selected[:n] {
				state := &groups.groups[groupIDs[index]].states[i]
				if positions[i] < 0 {
					state.count++
				} else {
					state.add(&batch.Columns[positions[i]], row, binding.function, exactIntegerAggregate(binding.field, binding.function), binding.field)
				}
			}
		}
		return nil
	}
	stats, err := transaction.scanAggregateBatches(
		ctx, schema, generation, fields,
		batchPartitionChunkSelector(schema, fields, filters), decide, consume, groups.reset, observe,
	)
	if err != nil {
		return Result{}, true, err
	}
	stats.Groups = uint64(len(groups.groups))
	rows := make([][]any, len(groups.groups))
	for g, group := range groups.groups {
		if g&255 == 0 {
			if err := ctx.Err(); err != nil {
				return Result{}, true, err
			}
		}
		for i, binding := range bindings {
			if binding.function == "" {
				continue
			}
			kind := columnar.Integer
			if positions[i] >= 0 {
				kind = fields[positions[i]].Kind
			}
			value, err := group.states[i].value(binding.function, kind, binding.field)
			if err != nil {
				return Result{}, true, err
			}
			group.values[i] = value
		}
		if err := working.reserve(materializedReferenceRowBytes(group.values), "GROUP BY batch result rows"); err != nil {
			return Result{}, true, err
		}
		rows[g] = group.values
	}
	result, err := finishAggregateRows(ctx, rows, columns, plan, parameters, stats, working)
	return result, true, err
}
