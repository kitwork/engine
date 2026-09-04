package relational

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"sort"

	"github.com/kitwork/engine/internal/snapshotfile"
	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/columnar"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const (
	columnarFormatVersion       = 3
	columnarChunkVersion        = 5
	columnarRangeChunkVersion   = 6
	columnarChunkRows           = 8 * columnar.BatchRows
	columnarMaximumChunks       = 16384
	columnarChunkMetadataBytes  = 4 << 20
	columnarBlockDirectoryBytes = 1 << 20
	columnarHistoryOperations   = 100000
	columnarHistoryBytes        = 64 << 20
	chunkPartitionVersion       = 1
	blockPartitionVersion       = 2
	blockPartitionEntryBytes    = 30
)

var errColumnarReuse = errors.New("kitdb: columnar chunk reuse failed")
var errColumnarHistoryBudget = errors.New("kitdb: columnar history budget exceeded")

func expectedColumnarChunkVersion(schema kitdbsql.Schema) int {
	if schema.Partition != nil && schema.Partition.Strategy == "range" {
		return columnarRangeChunkVersion
	}
	return columnarChunkVersion
}

// Bounds cover the complete physical row-key domain, including empty gaps.
// Offsets refer to complete KCOL groups within one table's snapshot section.
type projectionChunk struct {
	Start     []byte
	End       []byte
	Offset    int64
	Length    int64
	Rows      uint64
	Partition projectionChunkPartition `json:",omitzero"`
	blocks    projectionBlockDirectory
}

// projectionChunkPartition is a conservative routing proof. Min/max support
// RANGE predicates; HashMask can reject HASH equality predicates without
// exposing row identities. It never serves as canonical data.
type projectionChunkPartition struct {
	Version  int
	Nulls    uint64
	HasValue bool
	Minimum  int64
	Maximum  int64
	HashMask uint64 `json:",omitempty"`
}

// projectionBlockDirectory is a compact, optional routing index over the
// physical KCOL blocks in one chunk. Entries are fixed-width and covered by
// the snapshot manifest checksum; canonical values remain in KROW/KCOL.
type projectionBlockDirectory struct {
	Version int
	Entries []byte
}

func (directory projectionBlockDirectory) IsZero() bool {
	return directory.Version == 0 && len(directory.Entries) == 0
}

type columnarReuseTable struct {
	prefix    []byte
	meta      projectionTable
	reader    *columnar.Reader
	dirty     []bool
	partition *kitdbsql.Partition
}

type columnarReusePlan struct {
	file                    *snapshotfile.Reader
	tables                  map[string]*columnarReuseTable
	unchanged               bool
	upgradeBlockDirectories bool
}

func (plan *columnarReusePlan) close() error {
	if plan == nil || plan.file == nil {
		return nil
	}
	err := plan.file.Close()
	plan.file = nil
	return err
}

type columnarRefreshStats struct {
	sourceRows      uint64
	reusedRows      uint64
	builtChunks     int
	reusedChunks    int
	copiedBytes     int64
	referencedBytes int64
	writtenBytes    int64
	fileBytes       int64
	obsoleteBytes   int64
	publication     string
	metadata        int
	blockDirectory  int
	tables          int
}

func (stats *columnarRefreshStats) addChunk(table *projectionTable, chunk projectionChunk, reused bool) error {
	if stats.builtChunks+stats.reusedChunks >= columnarMaximumChunks {
		return fmt.Errorf("kitdb: columnar chunk count exceeds %d", columnarMaximumChunks)
	}
	size := columnarChunkMetadataSize(chunk)
	if size > columnarChunkMetadataBytes-stats.metadata {
		return fmt.Errorf("kitdb: columnar chunk metadata budget exceeded")
	}
	stats.metadata += size
	if chunk.Partition.Version != 0 && chunk.Rows != 0 && chunk.blocks.IsZero() {
		stats.blockDirectory -= table.blockDirectoryBytes
		table.BlockDirectoryVersion, table.BlockDirectory = 0, nil
		table.blockDirectoryBytes, table.blockDirectoryDisabled = 0, true
	} else if !chunk.blocks.IsZero() && !table.blockDirectoryDisabled {
		directorySize := len(chunk.blocks.Entries)
		if table.BlockDirectoryVersion == 0 {
			directorySize += 48
		}
		if chunk.blocks.Version != blockPartitionVersion || directorySize > columnarBlockDirectoryBytes-stats.blockDirectory {
			stats.blockDirectory -= table.blockDirectoryBytes
			table.BlockDirectoryVersion, table.BlockDirectory = 0, nil
			table.blockDirectoryBytes, table.blockDirectoryDisabled = 0, true
		} else {
			table.BlockDirectoryVersion = blockPartitionVersion
			table.BlockDirectory = append(table.BlockDirectory, chunk.blocks.Entries...)
			table.blockDirectoryBytes += directorySize
			stats.blockDirectory += directorySize
		}
	}
	chunk.blocks = projectionBlockDirectory{}
	chunk.Start, chunk.End = bytes.Clone(chunk.Start), bytes.Clone(chunk.End)
	table.Chunks = append(table.Chunks, chunk)
	table.Rows += chunk.Rows
	if reused {
		stats.reusedChunks++
		stats.reusedRows += chunk.Rows
	} else {
		stats.builtChunks++
	}
	return nil
}

func columnarChunkMetadataSize(chunk projectionChunk) int {
	size := len(chunk.Start) + len(chunk.End) + 128
	if chunk.Partition.Version != 0 {
		size += 96
	}
	return size
}

func columnarBlockDirectoryMetadataSize(table projectionTable) int {
	if tableBlockDirectoryIsZero(table) {
		return 0
	}
	return len(table.BlockDirectory) + 48
}

func (engine *Engine) refreshColumnarSnapshot(ctx context.Context, tx *Transaction, report *ProjectionReport) error {
	plan, reason, err := engine.prepareColumnarReuse(ctx, tx)
	if err != nil {
		return err
	}
	defer plan.close()
	stats, err := engine.writeColumnarGeneration(ctx, tx, plan)
	if errors.Is(err, errColumnarReuse) && ctx.Err() == nil {
		// Failed reuse can leave an unreachable append tail or temporary bytes.
		// Close that attempt and build one full replacement from the same snapshot.
		if closeErr := plan.close(); closeErr != nil {
			return errors.Join(err, closeErr)
		}
		previousSourceRows := stats.sourceRows
		previousWrittenBytes := stats.writtenBytes
		stats, err = engine.writeColumnarGeneration(ctx, tx, nil)
		stats.sourceRows += previousSourceRows
		stats.writtenBytes += previousWrittenBytes
		reason = "chunk verification failed; rebuilt from source snapshot"
	}
	if err != nil {
		return err
	}
	report.AnalyticsTables = stats.tables
	report.AnalyticsSourceRows = stats.sourceRows
	report.AnalyticsReusedRows = stats.reusedRows
	report.AnalyticsBuiltChunks = stats.builtChunks
	report.AnalyticsReusedChunks = stats.reusedChunks
	report.AnalyticsCopiedBytes = stats.copiedBytes
	report.AnalyticsReferencedBytes = stats.referencedBytes
	report.AnalyticsWrittenBytes = stats.writtenBytes
	report.AnalyticsFileBytes = stats.fileBytes
	report.AnalyticsObsoleteBytes = stats.obsoleteBytes
	report.AnalyticsPublication = stats.publication
	report.AnalyticsFallback = reason
	return nil
}

func (engine *Engine) prepareColumnarReuse(ctx context.Context, tx *Transaction) (*columnarReusePlan, string, error) {
	file, err := snapshotfile.Open(engine.database.Path() + ".analytics")
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "previous columnar container is unreadable", nil
	}
	plan := &columnarReusePlan{file: file, tables: make(map[string]*columnarReuseTable)}
	keep := false
	defer func() {
		if !keep {
			_ = plan.close()
		}
	}()
	var previous projectionManifest
	current := tx.snapshot.HistoryCursor()
	if err := file.Metadata(&previous); err != nil || previous.Version != 1 || previous.Kind != "columnar" || previous.Cursor.DatabaseID != current.DatabaseID || previous.Cursor.Transaction > current.Transaction {
		return nil, "previous columnar manifest is incompatible", nil
	}
	var ordered []*columnarReuseTable
	chunkCount, metadataBytes, blockDirectoryBytes, expectedBlockDirectoryBytes, supportedTables := 0, 0, 0, 0, 0
	missingBlockDirectory := false
	for _, item := range tx.catalog.Structs {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		schema, err := kitdbsql.DecodeSchema(item.Definition)
		if err != nil {
			return nil, "", err
		}
		fields := columnarFields(schema)
		if len(fields) > 0 {
			supportedTables++
		}
		old, found := previous.Tables[schema.ID]
		if !found || len(fields) == 0 || old.ChunkVersion != expectedColumnarChunkVersion(schema) || old.Hash != schema.Hash {
			continue
		}
		generation, epoch, err := activeRowLayout(tx.snapshot, schema)
		if err != nil {
			return nil, "", err
		}
		if old.Generation != generation || old.Epoch != epoch {
			continue
		}
		prefix, err := rowPrefix(schema, generation)
		if err != nil {
			return nil, "", err
		}
		section, err := file.Section(schema.ID)
		if err != nil {
			continue
		}
		reader, err := columnar.Open(section)
		if err != nil || reader.FormatVersion() != columnarFormatVersion || !slices.Equal(reader.Fields, fields) || !validColumnarChunks(old, prefix, reader.DataOffset(), section.Size(), schema.Partition) {
			continue
		}
		chunkCount += len(old.Chunks)
		for _, chunk := range old.Chunks {
			metadataBytes += columnarChunkMetadataSize(chunk)
		}
		blockDirectoryBytes += columnarBlockDirectoryMetadataSize(old)
		if schema.Partition != nil && old.Rows != 0 {
			expectedBlockDirectoryBytes += 48
			for _, chunk := range old.Chunks {
				expectedBlockDirectoryBytes += projectionBlockCount(chunk.Rows) * blockPartitionEntryBytes
			}
			missingBlockDirectory = missingBlockDirectory || tableBlockDirectoryIsZero(old)
		}
		if chunkCount > columnarMaximumChunks || metadataBytes > columnarChunkMetadataBytes || blockDirectoryBytes > columnarBlockDirectoryBytes {
			return nil, "previous chunk metadata exceeds reuse budget", nil
		}
		attachProjectionBlockDirectory(&old)
		table := &columnarReuseTable{
			prefix: prefix, meta: old, reader: reader, dirty: make([]bool, len(old.Chunks)),
			partition: schema.Partition,
		}
		plan.tables[schema.ID] = table
		ordered = append(ordered, table)
	}
	if len(ordered) == 0 {
		return nil, "no compatible chunk metadata; rebuilt from source snapshot", nil
	}
	plan.upgradeBlockDirectories = missingBlockDirectory && expectedBlockDirectoryBytes <= columnarBlockDirectoryBytes
	sort.Slice(ordered, func(i, j int) bool { return bytes.Compare(ordered[i].prefix, ordered[j].prefix) < 0 })
	if err := engine.markColumnarChanges(ctx, previous.Cursor, current, ordered); err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		reason := "history proof unavailable; rebuilt from source snapshot"
		switch {
		case errors.Is(err, kitdbengine.ErrHistoryDisabled):
			reason = "history disabled; rebuilt from source snapshot"
		case errors.Is(err, kitdbengine.ErrHistoryGap):
			reason = "history gap; rebuilt from source snapshot"
		case errors.Is(err, errColumnarHistoryBudget):
			reason = "history budget exceeded; rebuilt from source snapshot"
		}
		return nil, reason, nil
	}
	keep = true
	plan.unchanged = previous.Cursor == current && previous.Catalog == tx.catalog.Revision && len(plan.tables) == supportedTables && len(previous.Tables) == supportedTables && file.GenerationInfo().Number != 0 && !plan.upgradeBlockDirectories
	return plan, "", nil
}

func validColumnarChunks(table projectionTable, prefix []byte, offset, size int64, partition *kitdbsql.Partition) bool {
	if len(table.Chunks) == 0 || len(table.Chunks) > columnarMaximumChunks {
		return false
	}
	start, end := prefix, prefixEnd(prefix)
	var rows uint64
	for _, chunk := range table.Chunks {
		if !bytes.Equal(chunk.Start, start) || bytes.Compare(chunk.End, chunk.Start) <= 0 || bytes.Compare(chunk.End, end) > 0 || chunk.Offset != offset || chunk.Length < 0 || chunk.Length > size-offset || chunk.Rows > columnarChunkRows || (chunk.Rows == 0) != (chunk.Length == 0) || !validProjectionChunkPartition(chunk, partition) {
			return false
		}
		start, offset = chunk.End, offset+chunk.Length
		rows += chunk.Rows
	}
	return bytes.Equal(start, end) && offset == size && rows == table.Rows && validProjectionTableBlockDirectory(table, partition)
}

func validProjectionChunkPartition(chunk projectionChunk, partition *kitdbsql.Partition) bool {
	if partition == nil {
		return chunk.Partition.Version == 0
	}
	return validProjectionPartitionSummary(chunk.Partition, chunk.Rows, partition)
}

func validProjectionPartitionSummary(summary projectionChunkPartition, rows uint64, partition *kitdbsql.Partition) bool {
	if partition == nil {
		return summary.Version == 0
	}
	if summary.Version != chunkPartitionVersion || summary.Nulls > rows ||
		summary.HasValue != (summary.Nulls < rows) || summary.HasValue && summary.Minimum > summary.Maximum {
		return false
	}
	if partition.Strategy == "range" && summary.HashMask != 0 {
		return false
	}
	if !summary.HasValue && (summary.Minimum != 0 || summary.Maximum != 0 || summary.HashMask != 0) {
		return false
	}
	return partition.Strategy == "range" || partition.Strategy == "hash"
}

func tableBlockDirectoryIsZero(table projectionTable) bool {
	return table.BlockDirectoryVersion == 0 && len(table.BlockDirectory) == 0
}

func validProjectionTableBlockDirectory(table projectionTable, partition *kitdbsql.Partition) bool {
	if tableBlockDirectoryIsZero(table) {
		return true // Legacy and budget-limited manifests use chunk routing only.
	}
	if len(table.BlockDirectory)+48 > columnarBlockDirectoryBytes {
		return false
	}
	blocks := 0
	for _, chunk := range table.Chunks {
		if chunk.Rows > columnarChunkRows {
			return false
		}
		blocks += projectionBlockCount(chunk.Rows)
	}
	if partition == nil || table.Rows == 0 || table.BlockDirectoryVersion != blockPartitionVersion ||
		len(table.BlockDirectory) != blocks*blockPartitionEntryBytes {
		return false
	}
	block := 0
	for _, chunk := range table.Chunks {
		actual := projectionChunkPartition{Version: chunkPartitionVersion}
		var encodedBytes int64
		var previousMaximum int64
		var hasPrevious, reachedNulls bool
		for local := range projectionBlockCount(chunk.Rows) {
			rows, ok := projectionBlockRows(chunk.Rows, local)
			if !ok {
				return false
			}
			summary, length, ok := decodeProjectionBlockPartition(table.BlockDirectory, block, rows)
			if !ok || !validProjectionPartitionSummary(summary, rows, partition) {
				return false
			}
			if length > chunk.Length-encodedBytes {
				return false
			}
			encodedBytes += length
			if partition.Strategy == "range" {
				if reachedNulls && summary.HasValue || hasPrevious && summary.HasValue && summary.Minimum < previousMaximum {
					return false
				}
				if summary.HasValue {
					previousMaximum, hasPrevious = summary.Maximum, true
				}
				reachedNulls = reachedNulls || summary.Nulls != 0
			}
			mergeProjectionPartition(&actual, summary)
			block++
		}
		if actual != chunk.Partition || encodedBytes != chunk.Length {
			return false
		}
	}
	return block == blocks
}

func attachProjectionBlockDirectory(table *projectionTable) {
	if table == nil || tableBlockDirectoryIsZero(*table) {
		return
	}
	block := 0
	for index := range table.Chunks {
		blocks := projectionBlockCount(table.Chunks[index].Rows)
		start, end := block*blockPartitionEntryBytes, (block+blocks)*blockPartitionEntryBytes
		table.Chunks[index].blocks = projectionBlockDirectory{
			Version: table.BlockDirectoryVersion,
			Entries: table.BlockDirectory[start:end],
		}
		block += blocks
	}
}

func projectionBlockCount(rows uint64) int {
	if rows == 0 {
		return 0
	}
	return int((rows-1)/columnar.BatchRows) + 1
}

func projectionBlockRows(rows uint64, block int) (uint64, bool) {
	if block < 0 || block >= projectionBlockCount(rows) {
		return 0, false
	}
	start := uint64(block * columnar.BatchRows)
	return min(uint64(columnar.BatchRows), rows-start), true
}

func appendProjectionBlockPartition(directory *projectionBlockDirectory, summary projectionChunkPartition, length int64) error {
	if length < 1 || length > math.MaxUint32 {
		return fmt.Errorf("kitdb: invalid analytics block length")
	}
	if directory.Version == 0 {
		directory.Version = blockPartitionVersion
	} else if directory.Version != blockPartitionVersion {
		return fmt.Errorf("kitdb: invalid analytics block directory version")
	}
	offset := len(directory.Entries)
	directory.Entries = append(directory.Entries, make([]byte, blockPartitionEntryBytes)...)
	entry := directory.Entries[offset:]
	binary.LittleEndian.PutUint16(entry, uint16(summary.Nulls))
	binary.LittleEndian.PutUint64(entry[2:], uint64(summary.Minimum))
	binary.LittleEndian.PutUint64(entry[10:], uint64(summary.Maximum))
	binary.LittleEndian.PutUint64(entry[18:], summary.HashMask)
	binary.LittleEndian.PutUint32(entry[26:], uint32(length))
	return nil
}

func decodeProjectionBlockPartition(entries []byte, block int, rows uint64) (projectionChunkPartition, int64, bool) {
	offset := block * blockPartitionEntryBytes
	if block < 0 || offset < 0 || offset+blockPartitionEntryBytes > len(entries) {
		return projectionChunkPartition{}, 0, false
	}
	entry := entries[offset : offset+blockPartitionEntryBytes]
	nulls := uint64(binary.LittleEndian.Uint16(entry))
	length := int64(binary.LittleEndian.Uint32(entry[26:]))
	if length == 0 {
		return projectionChunkPartition{}, 0, false
	}
	return projectionChunkPartition{
		Version:  chunkPartitionVersion,
		Nulls:    nulls,
		HasValue: nulls < rows,
		Minimum:  int64(binary.LittleEndian.Uint64(entry[2:])),
		Maximum:  int64(binary.LittleEndian.Uint64(entry[10:])),
		HashMask: binary.LittleEndian.Uint64(entry[18:]),
	}, length, true
}

func mergeProjectionPartition(target *projectionChunkPartition, source projectionChunkPartition) {
	target.Nulls += source.Nulls
	if source.HasValue {
		if !target.HasValue {
			target.HasValue, target.Minimum, target.Maximum = true, source.Minimum, source.Maximum
		} else {
			target.Minimum = min(target.Minimum, source.Minimum)
			target.Maximum = max(target.Maximum, source.Maximum)
		}
	}
	target.HashMask |= source.HashMask
}

func (engine *Engine) markColumnarChanges(ctx context.Context, from, through kitdbengine.HistoryCursor, tables []*columnarReuseTable) error {
	if from == through {
		return ctx.Err()
	}
	if from.DatabaseID != through.DatabaseID || from.Transaction >= through.Transaction {
		return kitdbengine.ErrHistoryCursor
	}
	seenFrom, seenThrough := false, false
	operations, size := 0, 0
	visit := func(event kitdbengine.CommitEvent) error {
		if event.Transaction == from.Transaction {
			if event.Checksum != from.Checksum {
				return kitdbengine.ErrHistoryCursor
			}
			seenFrom = true
			return nil
		}
		if event.Transaction <= from.Transaction || event.Transaction > through.Transaction {
			return nil
		}
		if event.Transaction == through.Transaction {
			if event.Checksum != through.Checksum {
				return kitdbengine.ErrHistoryCursor
			}
			seenThrough = true
		}
		for _, operation := range event.Operations {
			if operations&255 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			operations++
			size += len(operation.Key) + len(operation.Value)
			if operations > columnarHistoryOperations || size > columnarHistoryBytes {
				return errColumnarHistoryBudget
			}
			index := sort.Search(len(tables), func(i int) bool { return bytes.Compare(tables[i].prefix, operation.Key) > 0 }) - 1
			if index < 0 || !bytes.HasPrefix(operation.Key, tables[index].prefix) {
				continue
			}
			if operation.Kind != kitdbengine.CommitOperationPut && operation.Kind != kitdbengine.CommitOperationDelete {
				return kitdbengine.ErrHistoryCursor
			}
			table := tables[index]
			chunk := sort.Search(len(table.meta.Chunks), func(i int) bool { return bytes.Compare(table.meta.Chunks[i].End, operation.Key) > 0 })
			if chunk >= len(table.dirty) || bytes.Compare(operation.Key, table.meta.Chunks[chunk].Start) < 0 {
				return kitdbengine.ErrHistoryCursor
			}
			table.dirty[chunk] = true
		}
		return nil
	}
	after := from.Transaction
	if after > 0 {
		after-- // Include the old cursor frame to verify its checksum as well.
	}
	history, err := engine.database.WalkHistory(ctx, after, visit)
	if errors.Is(err, kitdbengine.ErrHistoryGap) && history.BaseTransaction == from.Transaction && history.BaseChecksum == from.Checksum {
		operations, size = 0, 0
		seenFrom = true
		history, err = engine.database.WalkHistory(ctx, from.Transaction, visit)
	}
	if err != nil {
		return err
	}
	if history.BaseTransaction == from.Transaction && history.BaseChecksum == from.Checksum {
		seenFrom = true
	}
	if history.DatabaseID != through.DatabaseID || !seenFrom || !seenThrough {
		return kitdbengine.ErrHistoryCursor
	}
	return nil
}

func (engine *Engine) writeColumnarGeneration(ctx context.Context, tx *Transaction, plan *columnarReusePlan) (stats columnarRefreshStats, returnErr error) {
	if plan != nil && plan.unchanged && !plan.file.NeedsCompaction() {
		for _, old := range plan.tables {
			var table projectionTable
			for _, chunk := range old.meta.Chunks {
				if err := verifyColumnarChunk(ctx, old.reader, chunk, old.partition); err != nil {
					return stats, err
				}
				if err := stats.addChunk(&table, chunk, true); err != nil {
					return stats, err
				}
				stats.referencedBytes += chunk.Length
			}
			stats.tables++
		}
		info := plan.file.GenerationInfo()
		stats.fileBytes, stats.obsoleteBytes = info.FileBytes, info.ObsoleteBytes
		stats.publication = "unchanged"
		return stats, nil
	}
	var writer *snapshotfile.GenerationWriter
	var err error
	path := engine.database.Path() + ".analytics"
	if plan != nil && plan.file.GenerationInfo().Number != 0 && !plan.file.NeedsCompaction() {
		writer, err = snapshotfile.AppendGeneration(path, plan.file)
		stats.publication = "append"
	} else {
		writer, err = snapshotfile.CreateGeneration(path)
		stats.publication = "rewrite"
		if plan != nil && plan.file.GenerationInfo().Number != 0 {
			stats.publication = "compact"
		}
	}
	if err != nil {
		return stats, err
	}
	defer writer.Close()
	defer func() {
		stats.writtenBytes = writer.BytesWritten()
		info := writer.Info()
		stats.fileBytes, stats.obsoleteBytes = info.FileBytes, info.ObsoleteBytes
	}()
	manifest := projectionManifest{Version: 1, Kind: "columnar", Cursor: tx.snapshot.HistoryCursor(), Catalog: tx.catalog.Revision, Tables: make(map[string]projectionTable)}
	for _, item := range tx.catalog.Structs {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		schema, err := kitdbsql.DecodeSchema(item.Definition)
		if err != nil {
			return stats, err
		}
		fields := columnarFields(schema)
		if len(fields) == 0 {
			continue
		}
		generation, epoch, err := activeRowLayout(tx.snapshot, schema)
		if err != nil {
			return stats, err
		}
		prefix, err := rowPrefix(schema, generation)
		if err != nil {
			return stats, err
		}
		table := projectionTable{Hash: schema.Hash, Generation: generation, Epoch: epoch, ChunkVersion: expectedColumnarChunkVersion(schema)}
		err = writer.Add(schema.ID, func(output io.Writer) error {
			counted := &columnarCountingWriter{output: output}
			columnWriter, err := columnar.NewWriter(counted, fields)
			if err != nil {
				return err
			}
			var old *columnarReuseTable
			if plan != nil {
				old = plan.tables[schema.ID]
			}
			if old == nil {
				return buildColumnarRange(ctx, tx.snapshot, schema, fields, prefix, prefix, prefixEnd(prefix), columnWriter, counted, &table, &stats)
			}
			for i, chunk := range old.meta.Chunks {
				if err := ctx.Err(); err != nil {
					return err
				}
				if old.dirty[i] {
					if err := buildColumnarRange(ctx, tx.snapshot, schema, fields, prefix, chunk.Start, chunk.End, columnWriter, counted, &table, &stats); err != nil {
						return err
					}
					continue
				}
				if writer.Appending() {
					chunk, err = verifiedColumnarChunk(ctx, old.reader, chunk, schema.Partition)
					if err == nil {
						err = snapshotfile.Reference(output, plan.file, schema.ID, chunk.Offset, chunk.Length)
					}
					if err == nil {
						chunk.Offset = counted.bytes
						counted.bytes += chunk.Length
						stats.referencedBytes += chunk.Length
					}
				} else {
					chunk, err = copyColumnarChunk(ctx, old.reader, counted, chunk, schema.Partition)
					if err == nil {
						stats.copiedBytes += chunk.Length
					}
				}
				if err != nil {
					return err
				}
				if err := stats.addChunk(&table, chunk, true); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return stats, err
		}
		count, found, err := readTableCount(tx.snapshot, schema)
		if err != nil {
			return stats, err
		}
		if found && count != table.Rows {
			err := fmt.Errorf("kitdb: columnar rows do not match source table count")
			if plan != nil {
				err = errors.Join(errColumnarReuse, err)
			}
			return stats, err
		}
		manifest.Tables[schema.ID] = table
		stats.tables++
	}
	// Windows cannot replace a file held by our own reuse reader. Close it
	// before publication, then drain ordinary query readers under projectionMu.
	if err := plan.close(); err != nil {
		return stats, err
	}
	err = engine.publishProjection(ctx, "columnar", writer, manifest)
	return stats, err
}

type columnarCountingWriter struct {
	output io.Writer
	bytes  int64
	err    error
}

func (writer *columnarCountingWriter) Write(data []byte) (int, error) {
	if writer.err != nil {
		return 0, writer.err
	}
	n, err := writer.output.Write(data)
	writer.bytes += int64(n)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	writer.err = err
	return n, err
}

func copyColumnarChunk(ctx context.Context, reader *columnar.Reader, output *columnarCountingWriter, chunk projectionChunk, partitions ...*kitdbsql.Partition) (projectionChunk, error) {
	offset := output.bytes
	rows, err := reader.CopyGroups(ctx, output, chunk.Offset, chunk.Length)
	if err != nil {
		// Destination failures (e.g. a full disk) must not trigger an expensive
		// source rebuild. Only unverifiable old bytes are eligible for retry.
		if output.err != nil {
			return projectionChunk{}, output.err
		}
		if ctx.Err() != nil {
			return projectionChunk{}, ctx.Err()
		}
		return projectionChunk{}, errors.Join(errColumnarReuse, err)
	}
	if rows != chunk.Rows || output.bytes-offset != chunk.Length {
		return projectionChunk{}, errors.Join(errColumnarReuse, fmt.Errorf("copied chunk row/byte count mismatch"))
	}
	chunk, err = verifiedColumnarPartitionSummary(ctx, reader, chunk, firstPartition(partitions))
	if err != nil {
		return projectionChunk{}, err
	}
	chunk.Offset = offset
	return chunk, nil
}

func verifyColumnarChunk(ctx context.Context, reader *columnar.Reader, chunk projectionChunk, partitions ...*kitdbsql.Partition) error {
	_, err := verifiedColumnarChunk(ctx, reader, chunk, partitions...)
	return err
}

func verifiedColumnarChunk(ctx context.Context, reader *columnar.Reader, chunk projectionChunk, partitions ...*kitdbsql.Partition) (projectionChunk, error) {
	rows, err := reader.CopyGroups(ctx, io.Discard, chunk.Offset, chunk.Length)
	if ctx.Err() != nil {
		return projectionChunk{}, ctx.Err()
	}
	if err != nil {
		return projectionChunk{}, errors.Join(errColumnarReuse, err)
	}
	if rows != chunk.Rows {
		return projectionChunk{}, errors.Join(errColumnarReuse, fmt.Errorf("referenced chunk row count mismatch"))
	}
	return verifiedColumnarPartitionSummary(ctx, reader, chunk, firstPartition(partitions))
}

func firstPartition(partitions []*kitdbsql.Partition) *kitdbsql.Partition {
	if len(partitions) == 0 {
		return nil
	}
	return partitions[0]
}

func verifyColumnarPartitionSummary(ctx context.Context, reader *columnar.Reader, chunk projectionChunk, partition *kitdbsql.Partition) error {
	_, err := verifiedColumnarPartitionSummary(ctx, reader, chunk, partition)
	return err
}

func verifiedColumnarPartitionSummary(ctx context.Context, reader *columnar.Reader, chunk projectionChunk, partition *kitdbsql.Partition) (projectionChunk, error) {
	if partition == nil {
		if !chunk.blocks.IsZero() {
			return projectionChunk{}, errors.Join(errColumnarReuse, fmt.Errorf("unexpected partition block directory"))
		}
		return chunk, nil
	}
	actual := projectionChunkPartition{Version: chunkPartitionVersion}
	var blocks projectionBlockDirectory
	field := columnar.Field{Tag: partition.Field, Kind: columnar.Integer}
	var previous int64
	var hasPrevious, reachedNulls bool
	var blockLength int64
	report, err := reader.ScanBlockRanges(ctx, []columnar.Field{field}, []columnar.BlockRange{{
		Offset: chunk.Offset,
		Length: chunk.Length,
	}}, func(block *columnar.BlockStatistics) (columnar.BlockAction, error) {
		blockLength = block.EncodedBytes
		return columnar.ScanBlock, nil
	}, func(batch *columnar.Batch) error {
		for row := range batch.Rows {
			if partition.Strategy == "range" {
				vector := &batch.Columns[0]
				if vector.Valid[row] == 0 {
					reachedNulls = true
				} else {
					value := vector.Integers[row]
					if reachedNulls || hasPrevious && value < previous {
						return fmt.Errorf("range-clustered chunk is not ordered")
					}
					previous, hasPrevious = value, true
				}
			}
		}
		summary, err := summarizePartitionBlock(partition, batch, 0)
		if err != nil {
			return err
		}
		mergeProjectionPartition(&actual, summary)
		return appendProjectionBlockPartition(&blocks, summary, blockLength)
	})
	if ctx.Err() != nil {
		return projectionChunk{}, ctx.Err()
	}
	if err != nil {
		return projectionChunk{}, errors.Join(errColumnarReuse, err)
	}
	if report.Rows != chunk.Rows || chunk.Partition.Version == 0 || actual != chunk.Partition {
		return projectionChunk{}, errors.Join(errColumnarReuse, fmt.Errorf("partition chunk summary mismatch"))
	}
	if !chunk.blocks.IsZero() && (chunk.blocks.Version != blocks.Version || !bytes.Equal(chunk.blocks.Entries, blocks.Entries)) {
		return projectionChunk{}, errors.Join(errColumnarReuse, fmt.Errorf("partition block directory mismatch"))
	}
	chunk.blocks = blocks
	table := projectionTable{
		Rows: chunk.Rows, Chunks: []projectionChunk{chunk},
		BlockDirectoryVersion: blocks.Version, BlockDirectory: blocks.Entries,
	}
	if !validProjectionTableBlockDirectory(table, partition) {
		return projectionChunk{}, errors.Join(errColumnarReuse, fmt.Errorf("invalid partition block directory"))
	}
	return chunk, nil
}

func observeChunkPartition(summary *projectionChunkPartition, partition *kitdbsql.Partition, vector *columnar.Vector, row int) error {
	if summary == nil || partition == nil || vector == nil || row < 0 || row >= len(vector.Valid) {
		return fmt.Errorf("kitdb: invalid partition summary input")
	}
	if vector.Valid[row] == 0 {
		summary.Nulls++
		return nil
	}
	value := vector.Integers[row]
	if !summary.HasValue {
		summary.HasValue, summary.Minimum, summary.Maximum = true, value, value
	} else {
		if value < summary.Minimum {
			summary.Minimum = value
		}
		if value > summary.Maximum {
			summary.Maximum = value
		}
	}
	if partition.Strategy == "hash" {
		bucket, err := kitdbsql.IntegerPartitionBucket(value, partition.Buckets)
		if err != nil {
			return err
		}
		summary.HashMask |= uint64(1) << bucket
	}
	return nil
}

func summarizePartitionBlock(partition *kitdbsql.Partition, batch *columnar.Batch, position int) (projectionChunkPartition, error) {
	summary := projectionChunkPartition{Version: chunkPartitionVersion}
	if partition == nil || batch == nil || batch.Rows < 1 || batch.Rows > columnar.BatchRows || position < 0 || position >= len(batch.Columns) {
		return projectionChunkPartition{}, fmt.Errorf("kitdb: invalid partition block")
	}
	vector := &batch.Columns[position]
	for row := range batch.Rows {
		if err := observeChunkPartition(&summary, partition, vector, row); err != nil {
			return projectionChunkPartition{}, err
		}
	}
	return summary, nil
}

func observeColumnarPartitionBlock(chunk *projectionChunk, partition *kitdbsql.Partition, batch *columnar.Batch, position int, length int64) error {
	if partition == nil {
		return nil
	}
	summary, err := summarizePartitionBlock(partition, batch, position)
	if err != nil {
		return err
	}
	mergeProjectionPartition(&chunk.Partition, summary)
	return appendProjectionBlockPartition(&chunk.blocks, summary, length)
}

func addBuiltColumnarChunk(stats *columnarRefreshStats, table *projectionTable, chunk projectionChunk, partition *kitdbsql.Partition) error {
	if (partition != nil && chunk.Rows != 0 && chunk.blocks.IsZero()) || !validProjectionChunkPartition(chunk, partition) {
		return fmt.Errorf("kitdb: invalid built columnar partition metadata")
	}
	return stats.addChunk(table, chunk, false)
}

func newProjectionChunk(start []byte, offset int64, partition *kitdbsql.Partition) projectionChunk {
	chunk := projectionChunk{Start: start, Offset: offset}
	if partition != nil {
		chunk.Partition.Version = chunkPartitionVersion
	}
	return chunk
}

func buildColumnarRange(ctx context.Context, snapshot *kitdbengine.Snapshot, schema kitdbsql.Schema, fields []columnar.Field, prefix, start, end []byte, writer *columnar.Writer, output *columnarCountingWriter, table *projectionTable, stats *columnarRefreshStats) error {
	cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: prefix, Start: start, End: end})
	if err != nil {
		return err
	}
	defer cursor.Close()
	clusterRange := schema.Partition != nil && schema.Partition.Strategy == "range"
	decoderRows := columnar.BatchRows
	if clusterRange {
		decoderRows = columnarChunkRows
	}
	decoder, err := newBatchDecoderCapacity(schema, fields, decoderRows)
	if err != nil {
		return err
	}
	partitionPosition := -1
	if schema.Partition != nil {
		for index, field := range fields {
			if field.Tag == schema.Partition.Field && field.Kind == columnar.Integer {
				partitionPosition = index
				break
			}
		}
		if partitionPosition < 0 {
			return fmt.Errorf("kitdb: partition field is unavailable in analytics projection")
		}
	}
	var clusterer *rangeChunkClusterer
	if clusterRange {
		clusterer, err = newRangeChunkClusterer(fields, partitionPosition)
		if err != nil {
			return err
		}
	}
	chunk := newProjectionChunk(start, output.bytes, schema.Partition)
	for cursor.Next() {
		if chunk.Rows&255 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if chunk.Rows == columnarChunkRows {
			key := cursor.Key()
			if clusterer != nil {
				if err := clusterer.write(writer, decoder.batch, func(batch *columnar.Batch, length int64) error {
					return observeColumnarPartitionBlock(&chunk, schema.Partition, batch, partitionPosition, length)
				}); err != nil {
					return err
				}
				decoder.batch.Reset()
			}
			chunk.End, chunk.Length = key, output.bytes-chunk.Offset
			if err := addBuiltColumnarChunk(stats, table, chunk, schema.Partition); err != nil {
				return err
			}
			chunk = newProjectionChunk(key, output.bytes, schema.Partition)
		}
		if err := decoder.append(cursor.Value()); err != nil {
			return err
		}
		chunk.Rows++
		stats.sourceRows++
		if clusterer == nil && decoder.batch.Rows == columnar.BatchRows {
			if err := writer.WriteBatch(decoder.batch); err != nil {
				return err
			}
			if err := observeColumnarPartitionBlock(&chunk, schema.Partition, decoder.batch, partitionPosition, writer.LastBlockLength()); err != nil {
				return err
			}
			decoder.batch.Reset()
		}
	}
	if err := cursor.Err(); err != nil {
		return err
	}
	if clusterer != nil && decoder.batch.Rows != 0 {
		if err := clusterer.write(writer, decoder.batch, func(batch *columnar.Batch, length int64) error {
			return observeColumnarPartitionBlock(&chunk, schema.Partition, batch, partitionPosition, length)
		}); err != nil {
			return err
		}
		decoder.batch.Reset()
	} else if decoder.batch.Rows != 0 {
		if err := writer.WriteBatch(decoder.batch); err != nil {
			return err
		}
		if err := observeColumnarPartitionBlock(&chunk, schema.Partition, decoder.batch, partitionPosition, writer.LastBlockLength()); err != nil {
			return err
		}
	}
	chunk.End, chunk.Length = end, output.bytes-chunk.Offset
	return addBuiltColumnarChunk(stats, table, chunk, schema.Partition)
}
