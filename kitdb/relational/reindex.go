package relational

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbrecord "github.com/kitwork/engine/kitdb/record"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const (
	reindexStateVersion       = 1
	indexBuildProgressVersion = 1
	indexBuildCheckpointRows  = 500_000
)

type reindexState struct {
	Version   int      `json:"version"`
	Signature string   `json:"signature"`
	Remaining []string `json:"remaining"`
	Current   string   `json:"current,omitempty"`
}

type indexBuildProgress struct {
	Version    int    `json:"version"`
	Signature  string `json:"signature"`
	LastRowKey []byte `json:"last_row_key,omitempty"`
}

type indexMultisetDigest struct {
	Count uint64
	Sum   [4]uint64
	XOR   [4]uint64
}

func (engine *Engine) executeReindex(ctx context.Context, plan *kitdbsql.ReindexStatement) (Result, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if err := engine.readyLocked(); err != nil {
		return Result{}, err
	}
	if plan == nil || plan.Table == "" {
		return Result{}, fmt.Errorf("kitdb SQL: invalid REINDEX plan")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	schema, err := engine.schemaLocked(plan.Table)
	if err != nil {
		return Result{}, err
	}
	generation, err := activeRowGeneration(engine.database, schema)
	if err != nil {
		return Result{}, err
	}
	indexes, err := collectSecondaryIndexes(schema)
	if err != nil {
		return Result{}, err
	}
	if len(indexes) == 0 {
		return Result{CommandTag: "REINDEX"}, nil
	}
	stateKey, err := reindexStateKey(schema)
	if err != nil {
		return Result{}, err
	}
	signature := reindexPlanSignature(schema, indexes, generation)
	state, found, err := readReindexState(engine.database, stateKey)
	if err != nil {
		return Result{}, err
	}
	if found && state.Signature != signature {
		if err := engine.resetInterruptedReindex(ctx, schema, state, stateKey); err != nil {
			return Result{}, err
		}
		found = false
	}
	if !found {
		state = reindexState{
			Version: reindexStateVersion, Signature: signature,
			Remaining: make([]string, len(indexes)),
		}
		for position, index := range indexes {
			state.Remaining[position] = index.id
		}
		if err := putReindexState(engine.database, stateKey, state); err != nil {
			return Result{}, err
		}
	}

	for len(state.Remaining) != 0 {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		index, ok := secondaryIndexByID(indexes, state.Remaining[0])
		if !ok {
			return Result{}, fmt.Errorf("kitdb: REINDEX plan references a missing physical index")
		}
		progressKey, err := indexBuildProgressKey(schema, index)
		if err != nil {
			return Result{}, err
		}
		if state.Current == "" {
			state.Current = index.id
			if err := beginReindexStep(engine.database, schema, index, stateKey, state); err != nil {
				return Result{}, err
			}
		}
		progress, progressFound, err := readIndexBuildProgress(engine.database, progressKey)
		if err != nil {
			return Result{}, err
		}
		ready, err := indexIsReady(engine.database, schema, index)
		if err != nil {
			return Result{}, err
		}
		if ready && !progressFound {
			state = finishReindexStep(state, index.id)
			if err := putOrDeleteReindexState(engine.database, stateKey, state); err != nil {
				return Result{}, err
			}
			continue
		}
		if !progressFound {
			adopted, err := engine.adoptCompleteSecondaryIndex(
				ctx, schema, index, generation, progressKey,
			)
			if err != nil {
				return Result{}, err
			}
			if adopted {
				state = finishReindexStep(state, index.id)
				if err := putOrDeleteReindexState(engine.database, stateKey, state); err != nil {
					return Result{}, err
				}
				continue
			}
			prefix, err := secondaryIndexBasePrefix(schema, index)
			if err != nil {
				return Result{}, err
			}
			if err := engine.deletePrefixBatched(ctx, prefix); err != nil {
				return Result{}, err
			}
			progress = indexBuildProgress{
				Version:   indexBuildProgressVersion,
				Signature: reindexIndexSignature(schema, index, generation),
			}
			if err := putIndexBuildProgress(engine.database, progressKey, progress); err != nil {
				return Result{}, err
			}
		} else if progress.Signature != reindexIndexSignature(schema, index, generation) {
			return Result{}, fmt.Errorf("kitdb: REINDEX progress no longer matches index %q", index.name)
		}
		if err := engine.buildSecondaryIndexResumable(
			ctx, schema, index, generation, progressKey, progress,
		); err != nil {
			return Result{}, err
		}
		state = finishReindexStep(state, index.id)
		if err := putOrDeleteReindexState(engine.database, stateKey, state); err != nil {
			return Result{}, err
		}
	}
	return Result{CommandTag: "REINDEX"}, nil
}

func (engine *Engine) adoptCompleteSecondaryIndex(
	ctx context.Context,
	schema kitdbsql.Schema,
	index secondaryIndex,
	rowGeneration uint64,
	progressKey []byte,
) (bool, error) {
	snapshot, err := engine.database.Snapshot()
	if err != nil {
		return false, err
	}
	defer snapshot.Close()
	rowPrefix, err := rowPrefix(schema, rowGeneration)
	if err != nil {
		return false, err
	}
	rows, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: rowPrefix})
	if err != nil {
		return false, err
	}
	var expected indexMultisetDigest
	scratch := make([]byte, 0, 256)
	visited := uint64(0)
	for rows.Next() {
		if visited&255 == 0 {
			if err := ctx.Err(); err != nil {
				_ = rows.Close()
				return false, err
			}
		}
		physicalRowKey := rows.Key()
		rowKey, err := logicalRowKey(schema, physicalRowKey, rowGeneration)
		if err != nil {
			_ = rows.Close()
			return false, err
		}
		decoded, err := decodeRow(schema, rows.Value())
		if err != nil {
			_ = rows.Close()
			return false, err
		}
		entry, applicable, err := secondaryIndexEntry(schema, index, decoded.values, rowKey)
		if err != nil {
			_ = rows.Close()
			return false, err
		}
		if applicable {
			scratch = expected.add(scratch, entry.key, entry.value)
		}
		visited++
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, err
	}
	_ = rows.Close()

	indexPrefix, err := secondaryIndexBasePrefix(schema, index)
	if err != nil {
		return false, err
	}
	entries, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: indexPrefix})
	if err != nil {
		return false, err
	}
	defer entries.Close()
	var actual indexMultisetDigest
	visited = 0
	for entries.Next() {
		if visited&255 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		scratch = actual.add(scratch, entries.Key(), entries.Value())
		visited++
	}
	if err := entries.Err(); err != nil {
		return false, err
	}
	if expected != actual {
		return false, nil
	}
	readyKey, err := indexReadyKey(schema, index)
	if err != nil {
		return false, err
	}
	transaction, err := engine.database.Begin()
	if err != nil {
		return false, err
	}
	if err := transaction.Put(readyKey, indexReadyValue(schema)); err != nil {
		_ = transaction.Rollback()
		return false, err
	}
	if err := transaction.Delete(progressKey); err != nil {
		_ = transaction.Rollback()
		return false, err
	}
	if _, err := transaction.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (digest *indexMultisetDigest) add(scratch, key, value []byte) []byte {
	scratch = scratch[:0]
	scratch = binary.BigEndian.AppendUint64(scratch, uint64(len(key)))
	scratch = append(scratch, key...)
	scratch = binary.BigEndian.AppendUint64(scratch, uint64(len(value)))
	scratch = append(scratch, value...)
	hash := sha256.Sum256(scratch)
	digest.Count++
	for lane := range digest.Sum {
		value := binary.BigEndian.Uint64(hash[lane*8 : lane*8+8])
		digest.Sum[lane] += value
		digest.XOR[lane] ^= value
	}
	return scratch
}

func (engine *Engine) buildSecondaryIndexResumable(
	ctx context.Context,
	schema kitdbsql.Schema,
	index secondaryIndex,
	rowGeneration uint64,
	progressKey []byte,
	progress indexBuildProgress,
) error {
	prefix, err := rowPrefix(schema, rowGeneration)
	if err != nil {
		return err
	}
	if len(progress.LastRowKey) != 0 &&
		(len(progress.LastRowKey) <= len(prefix) || !bytes.HasPrefix(progress.LastRowKey, prefix)) {
		return fmt.Errorf("kitdb: durable index-build cursor is outside table %q", schema.Name)
	}
	for {
		complete, next, err := engine.buildSecondaryIndexChunk(
			ctx, schema, index, rowGeneration, progressKey, progress, prefix,
		)
		if err != nil {
			return err
		}
		progress = next
		if complete {
			break
		}
		if _, err := engine.database.Checkpoint(); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	readyKey, err := indexReadyKey(schema, index)
	if err != nil {
		return err
	}
	transaction, err := engine.database.Begin()
	if err != nil {
		return err
	}
	if err := transaction.Put(readyKey, indexReadyValue(schema)); err != nil {
		_ = transaction.Rollback()
		return err
	}
	if err := transaction.Delete(progressKey); err != nil {
		_ = transaction.Rollback()
		return err
	}
	if _, err := transaction.Commit(); err != nil {
		return err
	}
	_, err = engine.database.Checkpoint()
	return err
}

func (engine *Engine) buildSecondaryIndexChunk(
	ctx context.Context,
	schema kitdbsql.Schema,
	index secondaryIndex,
	rowGeneration uint64,
	progressKey []byte,
	progress indexBuildProgress,
	prefix []byte,
) (bool, indexBuildProgress, error) {
	snapshot, err := engine.database.Snapshot()
	if err != nil {
		return false, progress, err
	}
	defer snapshot.Close()
	cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{
		Prefix: prefix,
		Start:  bytes.Clone(progress.LastRowKey),
	})
	if err != nil {
		return false, progress, err
	}
	defer cursor.Close()

	var transaction *kitdbengine.Tx
	batchRows, chunkRows, bytesUsed := 0, 0, 0
	flush := func() error {
		if transaction == nil {
			return nil
		}
		encoded, err := json.Marshal(progress)
		if err != nil {
			_ = transaction.Rollback()
			transaction = nil
			return err
		}
		if err := transaction.Put(progressKey, encoded); err != nil {
			_ = transaction.Rollback()
			transaction = nil
			return err
		}
		_, err = transaction.Commit()
		transaction = nil
		batchRows, bytesUsed = 0, 0
		if err != nil {
			return err
		}
		return nil
	}
	rollback := func() {
		if transaction != nil {
			_ = transaction.Rollback()
			transaction = nil
		}
	}
	resumeKey := bytes.Clone(progress.LastRowKey)
	processed := 0
	for cursor.Next() {
		physicalRowKey := cursor.Key()
		if len(resumeKey) != 0 && bytes.Equal(physicalRowKey, resumeKey) {
			resumeKey = nil
			continue
		}
		if processed&255 == 0 {
			if err := ctx.Err(); err != nil {
				rollback()
				return false, progress, err
			}
		}
		if transaction == nil {
			transaction, err = engine.database.Begin()
			if err != nil {
				return false, progress, err
			}
		}
		decoded, err := decodeRow(schema, cursor.Value())
		if err != nil {
			rollback()
			return false, progress, err
		}
		rowKey, err := logicalRowKey(schema, physicalRowKey, rowGeneration)
		if err != nil {
			rollback()
			return false, progress, err
		}
		entry, applicable, err := secondaryIndexEntry(schema, index, decoded.values, rowKey)
		if err != nil {
			rollback()
			return false, progress, err
		}
		if applicable {
			if err := transaction.Put(entry.key, entry.value); err != nil {
				rollback()
				return false, progress, err
			}
			bytesUsed += len(entry.key) + len(entry.value)
		}
		progress.LastRowKey = bytes.Clone(physicalRowKey)
		processed++
		batchRows++
		chunkRows++
		if batchRows >= indexBuildBatchOperations || bytesUsed >= indexBuildBatchBytes {
			if err := flush(); err != nil {
				return false, progress, err
			}
		}
		if chunkRows >= indexBuildCheckpointRows {
			if err := flush(); err != nil {
				return false, progress, err
			}
			return false, progress, nil
		}
	}
	if err := cursor.Err(); err != nil {
		rollback()
		return false, progress, err
	}
	if err := flush(); err != nil {
		return false, progress, err
	}
	if err := ctx.Err(); err != nil {
		return false, progress, err
	}
	return true, progress, nil
}

func beginReindexStep(
	database *kitdbengine.DB,
	schema kitdbsql.Schema,
	index secondaryIndex,
	stateKey []byte,
	state reindexState,
) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	readyKey, err := indexReadyKey(schema, index)
	if err != nil {
		return err
	}
	transaction, err := database.Begin()
	if err != nil {
		return err
	}
	if err := transaction.Put(stateKey, encoded); err != nil {
		_ = transaction.Rollback()
		return err
	}
	if err := transaction.Delete(readyKey); err != nil {
		_ = transaction.Rollback()
		return err
	}
	_, err = transaction.Commit()
	return err
}

func (engine *Engine) resetInterruptedReindex(
	ctx context.Context,
	schema kitdbsql.Schema,
	state reindexState,
	stateKey []byte,
) error {
	if state.Current != "" {
		index := secondaryIndex{id: state.Current}
		progressKey, err := indexBuildProgressKey(schema, index)
		if err != nil {
			return err
		}
		readyKey, err := indexReadyKey(schema, index)
		if err != nil {
			return err
		}
		transaction, err := engine.database.Begin()
		if err != nil {
			return err
		}
		if err := transaction.Delete(progressKey); err != nil {
			_ = transaction.Rollback()
			return err
		}
		if err := transaction.Delete(readyKey); err != nil {
			_ = transaction.Rollback()
			return err
		}
		if err := transaction.Delete(stateKey); err != nil {
			_ = transaction.Rollback()
			return err
		}
		if _, err = transaction.Commit(); err != nil {
			return err
		}
		prefix, err := secondaryIndexBasePrefix(schema, index)
		if err != nil {
			return err
		}
		return engine.deletePrefixBatched(ctx, prefix)
	}
	transaction, err := engine.database.Begin()
	if err != nil {
		return err
	}
	if err := transaction.Delete(stateKey); err != nil {
		_ = transaction.Rollback()
		return err
	}
	_, err = transaction.Commit()
	return err
}

func (engine *Engine) checkpointIndexBuildIfNeeded() error {
	stats, err := engine.database.Stats()
	if err != nil {
		return err
	}
	if stats.OverlayMutations < indexBuildCheckpointRows || stats.ActiveSnapshots != 0 {
		return nil
	}
	_, err = engine.database.Checkpoint()
	return err
}

func reindexStateKey(schema kitdbsql.Schema) ([]byte, error) {
	identity := kitdbsql.StableSchemaID("physical", schema.ID+":standalone-reindex-state")
	return fixedKey(kitdbrecord.PhysicalNamespace, schema.ID, identity)
}

func indexBuildProgressKey(schema kitdbsql.Schema, index secondaryIndex) ([]byte, error) {
	identity := kitdbsql.StableSchemaID("physical", schema.ID+":standalone-index-build:"+index.id)
	return fixedKey(kitdbrecord.PhysicalNamespace, schema.ID, identity)
}

func reindexPlanSignature(schema kitdbsql.Schema, indexes []secondaryIndex, generation uint64) string {
	var identity strings.Builder
	identity.WriteString(schema.ID)
	fmt.Fprintf(&identity, ":rows=%d", generation)
	for _, index := range indexes {
		identity.WriteByte(':')
		identity.WriteString(reindexIndexSignature(schema, index, generation))
	}
	return kitdbsql.StableSchemaID("physical", identity.String())
}

func reindexIndexSignature(schema kitdbsql.Schema, index secondaryIndex, generation uint64) string {
	var identity strings.Builder
	identity.WriteString(schema.ID)
	fmt.Fprintf(&identity, ":rows=%d", generation)
	identity.WriteByte(':')
	identity.WriteString(index.id)
	for _, field := range index.fields {
		identity.WriteByte(':')
		identity.WriteString(field.ID)
		identity.WriteByte(':')
		identity.WriteString(field.Kind)
	}
	filter, _ := json.Marshal(index.filter)
	identity.Write(filter)
	return kitdbsql.StableSchemaID("physical", identity.String())
}

func secondaryIndexByID(indexes []secondaryIndex, identity string) (secondaryIndex, bool) {
	for _, index := range indexes {
		if index.id == identity {
			return index, true
		}
	}
	return secondaryIndex{}, false
}

func finishReindexStep(state reindexState, identity string) reindexState {
	remaining := state.Remaining[:0]
	removed := false
	for _, candidate := range state.Remaining {
		if !removed && candidate == identity {
			removed = true
			continue
		}
		remaining = append(remaining, candidate)
	}
	state.Remaining = remaining
	state.Current = ""
	return state
}

func readReindexState(reader recordReader, key []byte) (reindexState, bool, error) {
	encoded, found, err := reader.Get(key)
	if err != nil || !found {
		return reindexState{}, found, err
	}
	var state reindexState
	if err := json.Unmarshal(encoded, &state); err != nil || state.Version != reindexStateVersion {
		return reindexState{}, false, fmt.Errorf("kitdb: invalid durable REINDEX state")
	}
	seen := make(map[string]struct{}, len(state.Remaining))
	for _, identity := range state.Remaining {
		if identity == "" {
			return reindexState{}, false, fmt.Errorf("kitdb: invalid durable REINDEX state")
		}
		if _, duplicate := seen[identity]; duplicate {
			return reindexState{}, false, fmt.Errorf("kitdb: invalid durable REINDEX state")
		}
		seen[identity] = struct{}{}
	}
	if state.Signature == "" ||
		(state.Current != "" && (len(state.Remaining) == 0 || state.Current != state.Remaining[0])) {
		return reindexState{}, false, fmt.Errorf("kitdb: invalid durable REINDEX state")
	}
	return state, true, nil
}

func readIndexBuildProgress(reader recordReader, key []byte) (indexBuildProgress, bool, error) {
	encoded, found, err := reader.Get(key)
	if err != nil || !found {
		return indexBuildProgress{}, found, err
	}
	var progress indexBuildProgress
	if err := json.Unmarshal(encoded, &progress); err != nil || progress.Version != indexBuildProgressVersion {
		return indexBuildProgress{}, false, fmt.Errorf("kitdb: invalid durable index-build progress")
	}
	if progress.Signature == "" {
		return indexBuildProgress{}, false, fmt.Errorf("kitdb: invalid durable index-build progress")
	}
	return progress, true, nil
}

func putReindexState(database *kitdbengine.DB, key []byte, state reindexState) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	transaction, err := database.Begin()
	if err != nil {
		return err
	}
	if err := transaction.Put(key, encoded); err != nil {
		_ = transaction.Rollback()
		return err
	}
	_, err = transaction.Commit()
	return err
}

func putOrDeleteReindexState(database *kitdbengine.DB, key []byte, state reindexState) error {
	transaction, err := database.Begin()
	if err != nil {
		return err
	}
	if len(state.Remaining) == 0 {
		err = transaction.Delete(key)
	} else {
		var encoded []byte
		encoded, err = json.Marshal(state)
		if err == nil {
			err = transaction.Put(key, encoded)
		}
	}
	if err != nil {
		_ = transaction.Rollback()
		return err
	}
	_, err = transaction.Commit()
	return err
}

func putIndexBuildProgress(database *kitdbengine.DB, key []byte, progress indexBuildProgress) error {
	encoded, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	transaction, err := database.Begin()
	if err != nil {
		return err
	}
	if err := transaction.Put(key, encoded); err != nil {
		_ = transaction.Rollback()
		return err
	}
	_, err = transaction.Commit()
	return err
}
