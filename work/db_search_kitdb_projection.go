package work

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/search"
)

const kitDBSearchCatchUpBatchSize = 128

func (t *SchemaTable) readCurrentKitDBSearchCursor() (kitdbengine.HistoryCursor, error) {
	managed, err := t.kitDBManaged()
	if err != nil {
		return kitdbengine.HistoryCursor{}, err
	}
	defer managed.Release()
	return managed.database.CurrentCursor()
}

func (t *SchemaTable) kitDBSearchProjectionFresh(
	ctx context.Context,
	manager *search.Manager,
	indexKey string,
	schema search.Schema,
	current kitdbengine.HistoryCursor,
) (bool, error) {
	watermark, found, err := t.readKitDBSearchWatermark(ctx, manager, indexKey, schema)
	if errors.Is(err, errKitDBSearchWatermark) {
		return false, nil
	}
	if err != nil || !found || !t.kitDBSearchWatermarkMatches(watermark, current.DatabaseID) ||
		watermark.Cursor != current {
		return false, err
	}
	info, err := manager.Info(ctx, indexKey, schema)
	return err == nil && info.Generation != 0, err
}

func (t *SchemaTable) synchronizeKitDBSearchProjection(
	ctx context.Context,
	manager *search.Manager,
	indexKey string,
	schema search.Schema,
	primaryKey string,
	columns []searchColumn,
) error {
	managed, err := t.kitDBManaged()
	if err != nil {
		return err
	}
	defer managed.Release()

	info, err := manager.Info(ctx, indexKey, schema)
	if err != nil {
		return err
	}
	watermark, found, watermarkErr := t.readKitDBSearchWatermark(ctx, manager, indexKey, schema)
	if watermarkErr != nil && !errors.Is(watermarkErr, errKitDBSearchWatermark) {
		return watermarkErr
	}
	if watermarkErr != nil {
		found = false
	}
	if found && info.Generation != 0 &&
		t.kitDBSearchWatermarkMatches(watermark, managed.database.ID()) {
		err = t.catchUpKitDBSearchProjection(ctx, managed, manager, indexKey, schema, columns, watermark)
		if err == nil {
			return nil
		}
		if !kitDBSearchNeedsFullRebuild(err) {
			return err
		}
	}

	if !found && info.Generation != 0 {
		adopted, err := t.adoptLegacyKitDBSearchProjection(ctx, managed, manager, indexKey, schema)
		if err != nil {
			return err
		}
		if adopted {
			return nil
		}
	}
	return t.bootstrapKitDBSearchProjection(
		ctx, managed, manager, indexKey, schema, primaryKey, columns,
	)
}

func kitDBSearchNeedsFullRebuild(err error) bool {
	return errors.Is(err, kitdbengine.ErrHistoryDisabled) ||
		errors.Is(err, kitdbengine.ErrHistoryGap) ||
		errors.Is(err, kitdbengine.ErrHistoryCursor) ||
		errors.Is(err, errKitDBLayoutAdvanced)
}

func (t *SchemaTable) adoptLegacyKitDBSearchProjection(
	ctx context.Context,
	managed *managedKitDB,
	manager *search.Manager,
	indexKey string,
	schema search.Schema,
) (bool, error) {
	stored, err := t.readStoredSearchSignature(indexKey)
	if err != nil || stored == "" {
		return false, err
	}
	stored, proved := t.verifyKitDBSearchSignatureProof(stored)
	if !proved {
		return false, nil
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()
	if err := t.validateKitDBSearchLayout(managed.database); err != nil {
		return false, err
	}
	current, err := t.currentKitDBSearchSignature(managed.database)
	if err != nil || !kitDBSearchSignaturesMatch(stored, current) {
		return false, err
	}
	if _, err := managed.database.Checkpoint(); err != nil {
		return false, err
	}
	current, err = t.currentKitDBSearchSignature(managed.database)
	if err != nil || !kitDBSearchSignaturesMatch(stored, current) {
		return false, err
	}
	cursor, err := managed.database.CurrentCursor()
	if err != nil {
		return false, err
	}
	if err := t.ensureKitDBSearchHistoryPin(ctx, managed.database, cursor); err != nil &&
		!errors.Is(err, kitdbengine.ErrHistoryDisabled) {
		return false, err
	}
	watermark := t.kitDBSearchWatermark(cursor)
	if err := t.writeKitDBSearchWatermark(ctx, manager, indexKey, schema, watermark); err != nil {
		return false, err
	}
	return true, nil
}

func (t *SchemaTable) bootstrapKitDBSearchProjection(
	ctx context.Context,
	managed *managedKitDB,
	manager *search.Manager,
	indexKey string,
	schema search.Schema,
	primaryKey string,
	columns []searchColumn,
) (returnErr error) {
	if t.columns[primaryKey] == nil || len(t.definition.primaryFields()) == 0 {
		return fmt.Errorf("search source has no primary field %q", primaryKey)
	}
	projectedTags, err := t.kitDBSearchProjectionTags(columns)
	if err != nil {
		return err
	}

	managed.writeMu.Lock()
	if err := t.validateKitDBSearchLayout(managed.database); err != nil {
		managed.writeMu.Unlock()
		return err
	}
	if _, err := managed.database.Checkpoint(); err != nil {
		managed.writeMu.Unlock()
		return err
	}
	snapshot, err := managed.database.Snapshot()
	if err != nil {
		managed.writeMu.Unlock()
		return err
	}
	boundary := snapshot.HistoryCursor()
	pinCreated, historyEnabled, err := t.pinInitialKitDBSearchBoundary(ctx, managed.database, boundary)
	managed.writeMu.Unlock()
	if err != nil {
		_ = snapshot.Close()
		return err
	}
	keepPin := false
	defer func() {
		returnErr = errors.Join(returnErr, snapshot.Close())
		if pinCreated && !keepPin {
			returnErr = errors.Join(returnErr, managed.database.ReleaseHistoryPin(
				context.Background(), t.kitDBSearchHistoryPinName(),
			))
		}
	}()

	replacement, err := manager.BeginReplacement(ctx, indexKey, schema)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, replacement.Abort()) }()
	if err := t.scanKitDBSearchSnapshot(
		ctx, snapshot, projectedTags,
		func(row kitDBStoredRow) (bool, error) {
			document, err := t.kitDBSearchDocument(row, columns)
			if err != nil {
				return false, err
			}
			return false, replacement.Add(ctx, document)
		},
	); err != nil {
		return err
	}
	if _, err := replacement.Commit(ctx); err != nil {
		return err
	}
	// The source snapshot is no longer needed after the immutable replacement
	// is visible. Release it before WalkHistory checkpoints again; otherwise a
	// rare full checkpoint at the segment ceiling would correctly refuse while
	// this snapshot still pins the old main generation.
	if err := snapshot.Close(); err != nil {
		return err
	}
	watermark := t.kitDBSearchWatermark(boundary)
	if err := t.writeKitDBSearchWatermark(ctx, manager, indexKey, schema, watermark); err != nil {
		return err
	}
	keepPin = pinCreated
	if !historyEnabled {
		current, err := managed.database.CurrentCursor()
		if err != nil {
			return err
		}
		if current != boundary {
			return fmt.Errorf("search source changed during a recovery-disabled projection build")
		}
		return nil
	}
	if _, err := managed.database.SetHistoryPin(ctx, t.kitDBSearchHistoryPinName(), boundary); err != nil {
		return err
	}
	return t.catchUpKitDBSearchProjection(
		ctx, managed, manager, indexKey, schema, columns, watermark,
	)
}

func (t *SchemaTable) catchUpKitDBSearchProjection(
	ctx context.Context,
	managed *managedKitDB,
	manager *search.Manager,
	indexKey string,
	schema search.Schema,
	columns []searchColumn,
	watermark kitDBSearchWatermark,
) error {
	if !t.kitDBSearchWatermarkMatches(watermark, managed.database.ID()) {
		return errors.Join(kitdbengine.ErrHistoryCursor, fmt.Errorf("search watermark layout changed"))
	}
	if _, err := managed.database.SetHistoryPin(
		ctx, t.kitDBSearchHistoryPinName(), watermark.Cursor,
	); err != nil {
		return err
	}
	projectedTags, err := t.kitDBSearchProjectionTags(columns)
	if err != nil {
		return err
	}
	physicalPrefix, err := kitDBPhysicalRowPrefix(t.definition, t.rowGeneration)
	if err != nil {
		return err
	}
	buffer := newKitDBSearchMutationBuffer(manager, indexKey, schema)
	rangeInfo, err := managed.database.WalkHistory(
		ctx,
		watermark.Cursor.Transaction,
		func(event kitdbengine.CommitEvent) error {
			for _, operation := range event.Operations {
				if !bytes.HasPrefix(operation.Key, physicalPrefix) {
					continue
				}
				logicalKey, err := kitDBLogicalRowKey(t.definition, operation.Key, t.rowGeneration)
				if err != nil {
					return err
				}
				switch operation.Kind {
				case kitdbengine.CommitOperationPut:
					decoded, err := decodeProjectedKitDBRow(t.definition, operation.Value, projectedTags)
					if err != nil {
						return err
					}
					row := kitDBStoredRow{key: logicalKey, values: decoded.values}
					document, err := t.kitDBSearchDocument(row, columns)
					if err != nil {
						return err
					}
					buffer.Upsert(document)
				case kitdbengine.CommitOperationDelete:
					identifier, err := t.kitDBSearchDocumentID(kitDBStoredRow{key: logicalKey})
					if err != nil {
						return err
					}
					buffer.Delete(identifier)
				default:
					return fmt.Errorf("search history contains an unknown operation kind %d", operation.Kind)
				}
				if buffer.Len() >= kitDBSearchCatchUpBatchSize {
					if err := buffer.Flush(ctx); err != nil {
						return err
					}
				}
			}
			return nil
		},
	)
	if err != nil {
		return err
	}
	if err := buffer.Flush(ctx); err != nil {
		return err
	}
	managed.writeMu.RLock()
	err = t.validateKitDBSearchLayout(managed.database)
	managed.writeMu.RUnlock()
	if err != nil {
		return err
	}
	next := t.kitDBSearchWatermark(rangeInfo.Cursor())
	if err := t.writeKitDBSearchWatermark(ctx, manager, indexKey, schema, next); err != nil {
		return err
	}
	_, err = managed.database.SetHistoryPin(ctx, t.kitDBSearchHistoryPinName(), next.Cursor)
	return err
}

func (t *SchemaTable) validateKitDBSearchLayout(database *kitdbengine.DB) error {
	if err := validateKitDBIndexLayoutEpoch(database, t.definition, t.indexEpoch); err != nil {
		return err
	}
	return validateKitDBRowLayoutEpoch(database, t.definition, t.rowEpoch)
}

func (t *SchemaTable) pinInitialKitDBSearchBoundary(
	ctx context.Context,
	database *kitdbengine.DB,
	cursor kitdbengine.HistoryCursor,
) (created bool, enabled bool, err error) {
	pins, err := database.HistoryPins()
	if errors.Is(err, kitdbengine.ErrHistoryDisabled) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	name := t.kitDBSearchHistoryPinName()
	for _, pin := range pins {
		if pin.Name == name {
			return false, true, nil
		}
	}
	if _, err := database.SetHistoryPin(ctx, name, cursor); err != nil {
		return false, true, err
	}
	return true, true, nil
}

func (t *SchemaTable) ensureKitDBSearchHistoryPin(
	ctx context.Context,
	database *kitdbengine.DB,
	cursor kitdbengine.HistoryCursor,
) error {
	_, err := database.SetHistoryPin(ctx, t.kitDBSearchHistoryPinName(), cursor)
	return err
}

func (t *SchemaTable) scanKitDBSearchSnapshot(
	ctx context.Context,
	snapshot *kitdbengine.Snapshot,
	projectedTags map[uint32]struct{},
	visit func(kitDBStoredRow) (bool, error),
) error {
	// Validate every KROW envelope but materialize only searchable field tags.
	// Unrelated JSON/array columns remain byte slices instead of Go object
	// graphs during a full replacement scan.
	physicalPrefix, err := kitDBPhysicalRowPrefix(t.definition, t.rowGeneration)
	if err != nil {
		return err
	}
	reader := kitDBSnapshotReader{snapshot: snapshot}
	return reader.Scan(kitdbengine.RangeOptions{Prefix: physicalPrefix}, func(physicalKey, encoded []byte) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		logicalKey, err := kitDBLogicalRowKey(t.definition, physicalKey, t.rowGeneration)
		if err != nil {
			return false, err
		}
		decoded, err := decodeProjectedKitDBRow(t.definition, encoded, projectedTags)
		if err != nil {
			return false, err
		}
		return visit(kitDBStoredRow{key: logicalKey, values: decoded.values})
	})
}

func (t *SchemaTable) kitDBSearchDocument(
	row kitDBStoredRow,
	columns []searchColumn,
) (search.Document, error) {
	identifier, err := t.kitDBSearchDocumentID(row)
	if err != nil {
		return search.Document{}, err
	}
	fields := make(map[string]string, len(columns))
	for _, column := range columns {
		if field, exists := row.values[column.name]; exists {
			fields[column.name] = searchFieldText(coerceRead(t.columns[column.name].kind, field))
		}
	}
	return search.Document{ID: identifier, Fields: fields}, nil
}

type kitDBSearchBufferedMutation struct {
	document search.Document
	deleted  bool
}

type kitDBSearchMutationBuffer struct {
	manager *search.Manager
	key     string
	schema  search.Schema
	order   []string
	items   map[string]kitDBSearchBufferedMutation
}

func newKitDBSearchMutationBuffer(
	manager *search.Manager,
	key string,
	schema search.Schema,
) *kitDBSearchMutationBuffer {
	return &kitDBSearchMutationBuffer{
		manager: manager, key: key, schema: schema,
		items: make(map[string]kitDBSearchBufferedMutation, kitDBSearchCatchUpBatchSize),
	}
}

func (buffer *kitDBSearchMutationBuffer) Len() int { return len(buffer.items) }

func (buffer *kitDBSearchMutationBuffer) Upsert(document search.Document) {
	buffer.remember(document.ID, kitDBSearchBufferedMutation{document: document})
}

func (buffer *kitDBSearchMutationBuffer) Delete(identifier string) {
	buffer.remember(identifier, kitDBSearchBufferedMutation{deleted: true})
}

func (buffer *kitDBSearchMutationBuffer) remember(
	identifier string,
	mutation kitDBSearchBufferedMutation,
) {
	if _, exists := buffer.items[identifier]; !exists {
		buffer.order = append(buffer.order, identifier)
	}
	buffer.items[identifier] = mutation
}

func (buffer *kitDBSearchMutationBuffer) Flush(ctx context.Context) error {
	if len(buffer.items) == 0 {
		return nil
	}
	batch := search.MutationBatch{}
	for _, identifier := range buffer.order {
		mutation := buffer.items[identifier]
		if mutation.deleted {
			batch.Deletes = append(batch.Deletes, identifier)
		} else {
			batch.Upserts = append(batch.Upserts, mutation.document)
		}
	}
	if _, err := buffer.manager.Mutate(ctx, buffer.key, buffer.schema, batch); err != nil {
		return err
	}
	buffer.order = buffer.order[:0]
	clear(buffer.items)
	return nil
}
