package relational

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/kitwork/engine/kitdb/columnar"
)

// rangeChunkClusterer transposes one bounded KROW extent into partition order
// before KCOL publication. Every projected column follows the same permutation.
type rangeChunkClusterer struct {
	partition int
	order     []int
	output    *columnar.Batch
}

func newRangeChunkClusterer(fields []columnar.Field, partition int) (*rangeChunkClusterer, error) {
	if partition < 0 || partition >= len(fields) || fields[partition].Kind != columnar.Integer {
		return nil, fmt.Errorf("kitdb: invalid range cluster field")
	}
	output, err := columnar.NewBatch(fields)
	if err != nil {
		return nil, err
	}
	return &rangeChunkClusterer{
		partition: partition,
		order:     make([]int, columnarChunkRows),
		output:    output,
	}, nil
}

func (clusterer *rangeChunkClusterer) write(writer *columnar.Writer, source *columnar.Batch, observe func(*columnar.Batch, int64) error) error {
	if clusterer == nil || writer == nil || source == nil || source.Rows < 1 || source.Rows > len(clusterer.order) ||
		clusterer.partition >= len(source.Columns) {
		return fmt.Errorf("kitdb: invalid range cluster batch")
	}
	partition := &source.Columns[clusterer.partition]
	order := clusterer.order[:source.Rows]
	for row := range order {
		order[row] = row
	}
	slices.SortFunc(order, func(left, right int) int {
		leftValid, rightValid := partition.Valid[left], partition.Valid[right]
		if leftValid != rightValid {
			if leftValid == 0 {
				return 1
			}
			return -1
		}
		if leftValid != 0 {
			if order := cmp.Compare(partition.Integers[left], partition.Integers[right]); order != 0 {
				return order
			}
		}
		return cmp.Compare(left, right)
	})

	for start := 0; start < len(order); start += columnar.BatchRows {
		rows := min(columnar.BatchRows, len(order)-start)
		clusterer.output.Rows = rows
		for column := range source.Columns {
			sourceVector := &source.Columns[column]
			outputVector := &clusterer.output.Columns[column]
			for row := 0; row < rows; row++ {
				sourceRow := order[start+row]
				outputVector.Valid[row] = sourceVector.Valid[sourceRow]
				switch sourceVector.Field.Kind {
				case columnar.Float:
					outputVector.Floats[row] = sourceVector.Floats[sourceRow]
				case columnar.Text:
					outputVector.Texts[row] = sourceVector.Texts[sourceRow]
				default:
					outputVector.Integers[row] = sourceVector.Integers[sourceRow]
				}
			}
		}
		if err := writer.WriteBatch(clusterer.output); err != nil {
			return err
		}
		if observe != nil {
			if err := observe(clusterer.output, writer.LastBlockLength()); err != nil {
				return err
			}
		}
	}
	return nil
}
