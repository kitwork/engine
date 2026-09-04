package relational

import (
	"context"
	"fmt"

	"github.com/kitwork/engine/kitdb/columnar"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

// Every aggregate uses the same snapshot proof and all-or-nothing fallback.
func (transaction *Transaction) scanAggregateBatches(
	ctx context.Context,
	schema kitdbsql.Schema,
	generation uint64,
	fields []columnar.Field,
	selectChunk func(projectionChunk) (bool, error),
	decide func(*columnar.BlockStatistics) (columnar.BlockAction, error),
	consume func(*columnar.Batch) error,
	reset func(),
	observe bool,
) (*ExecutionStats, error) {
	select {
	case transaction.engine.projectionQueries <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-transaction.engine.projectionQueries }()
	stats := &ExecutionStats{Path: "krow-batch"}
	var consumeErr, decideErr, chunkErr error
	visit := func(batch *columnar.Batch) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		stats.RowsScanned += uint64(batch.Rows)
		stats.Batches++
		consumeErr = consume(batch)
		return consumeErr
	}
	decideBlock := func(block *columnar.BlockStatistics) (columnar.BlockAction, error) {
		if decide == nil {
			return columnar.ScanBlock, nil
		}
		action, err := decide(block)
		decideErr = err
		return action, err
	}
	if transaction.engine.experimentalProjections {
		err := func() error {
			transaction.engine.projectionMu.RLock()
			defer transaction.engine.projectionMu.RUnlock()
			_, epoch, err := activeRowLayout(transaction, schema)
			if err != nil {
				return err
			}
			lease, err := transaction.engine.acquireProjection(
				transaction.engine.database.Path()+".analytics", "columnar",
				transaction.snapshot.HistoryCursor(), transaction.catalog.Revision,
				schema, generation, epoch,
			)
			if err != nil {
				stats.ProjectionCacheMisses++
				return err
			}
			defer lease.close()
			stats.observeProjectionCache(lease.access)
			section, err := lease.file.Section(schema.ID)
			if err != nil {
				return err
			}
			table := lease.table
			reader, err := columnar.Open(section)
			if err != nil {
				return err
			}
			prefix, err := rowPrefix(schema, generation)
			if err != nil {
				return err
			}
			if reader.FormatVersion() != columnarFormatVersion || table.ChunkVersion != expectedColumnarChunkVersion(schema) ||
				!validColumnarChunks(table, prefix, reader.DataOffset(), section.Size(), schema.Partition) {
				return fmt.Errorf("kitdb: invalid analytics chunk manifest")
			}
			ranges := make([]columnar.BlockRange, 0, len(table.Chunks))
			var chunkRowsSkipped, directoryRowsSkipped uint64
			directoryBlock := 0
			for _, chunk := range table.Chunks {
				chunkBlocks := projectionBlockCount(chunk.Rows)
				chunkDirectoryBlock := directoryBlock
				directoryBlock += chunkBlocks
				selected := true
				if selectChunk != nil {
					selected, chunkErr = selectChunk(chunk)
					if chunkErr != nil {
						return chunkErr
					}
				}
				if !selected {
					stats.ChunksSkipped++
					chunkRowsSkipped += chunk.Rows
					stats.BatchesSkipped += (chunk.Rows + columnar.BatchRows - 1) / columnar.BatchRows
					continue
				}
				stats.ChunksScanned++
				if chunk.Length == 0 {
					continue
				}
				if selectChunk == nil || tableBlockDirectoryIsZero(table) {
					ranges = append(ranges, columnar.BlockRange{Offset: chunk.Offset, Length: chunk.Length})
					continue
				}
				offset := chunk.Offset
				for block := range chunkBlocks {
					rows, ok := projectionBlockRows(chunk.Rows, block)
					if !ok {
						return fmt.Errorf("kitdb: invalid analytics block directory rows")
					}
					summary, length, ok := decodeProjectionBlockPartition(table.BlockDirectory, chunkDirectoryBlock+block, rows)
					if !ok {
						return fmt.Errorf("kitdb: invalid analytics block directory entry")
					}
					if length > chunk.Offset+chunk.Length-offset {
						return fmt.Errorf("kitdb: invalid analytics block directory bounds")
					}
					selected, chunkErr = selectChunk(projectionChunk{Rows: rows, Partition: summary})
					if chunkErr != nil {
						return chunkErr
					}
					if selected {
						ranges = append(ranges, columnar.BlockRange{Offset: offset, Length: length})
					} else {
						directoryRowsSkipped += rows
						stats.BatchesSkipped++
						stats.ColumnarBlocksPruned++
						stats.ColumnarRowsPruned += rows
					}
					offset += length
				}
				if offset != chunk.Offset+chunk.Length {
					return fmt.Errorf("kitdb: analytics block directory length mismatch")
				}
			}
			scan, err := reader.ScanBlockRanges(ctx, fields, ranges, decideBlock, visit)
			if err != nil {
				return err
			}
			stats.RowsSkipped = chunkRowsSkipped + directoryRowsSkipped + scan.RowsSkipped
			stats.BatchesSkipped += scan.BlocksSkipped
			stats.RowsFromMetadata = scan.RowsFromStatistics
			stats.BatchesFromMetadata = scan.BlocksFromStatistics
			if scan.Rows+chunkRowsSkipped+directoryRowsSkipped != table.Rows {
				return fmt.Errorf("kitdb: columnar row count mismatch")
			}
			stats.ColumnarBlockHeadersRead = scan.BlockHeadersRead
			stats.ColumnarHeaderBytesRead = scan.BlockHeaderBytesRead
			stats.ColumnarPayloadsRead = scan.ColumnPayloadsRead
			stats.ColumnarPayloadBytesRead = scan.ColumnPayloadBytesRead
			return nil
		}()
		if err == nil {
			stats.Path = "kcol-batch"
			return stats, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Query errors (including group budgets) are not corrupt/stale storage.
		// Retrying them would hide the failure and repeat the entire scan.
		if consumeErr != nil {
			return nil, consumeErr
		}
		if decideErr != nil {
			return nil, decideErr
		}
		if chunkErr != nil {
			return nil, chunkErr
		}
		reset()
		stats.ChunksScanned, stats.ChunksSkipped = 0, 0
		stats.RowsScanned, stats.Batches = 0, 0
		stats.RowsSkipped, stats.BatchesSkipped = 0, 0
		stats.RowsFromMetadata, stats.BatchesFromMetadata = 0, 0
		stats.ColumnarBlocksPruned, stats.ColumnarRowsPruned = 0, 0
		stats.Fallback = err.Error()
	}
	_, cursorStats, err := scanSnapshotBatches(ctx, transaction.snapshot, schema, generation, fields, observe, visit)
	if err != nil {
		return nil, err
	}
	stats.addCursorStats(cursorStats)
	return stats, nil
}
