package relational

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	searchprojection "github.com/kitwork/engine/kitdb/searchprojection"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	"github.com/kitwork/engine/search"
)

const (
	DefaultMaximumSearchResults    = 10_000
	MaximumSearchResults           = 100_000
	DefaultMaximumSearchCandidates = 50_000
	MaximumSearchCandidates        = 1_000_000
	DefaultSearchForegroundWait    = 2 * time.Second
	searchCatchUpBatchSize         = 128
)

var errRelationalSearchProjectionBuilding = errors.New("kitdb: search projection is building")

type relationalSearchConfiguration struct {
	root              string
	namespace         string
	manager           search.ManagerOptions
	maximumResults    int
	maximumCandidates int
	foregroundWait    time.Duration
}

type relationalSearchColumn struct {
	field  kitdbsql.Field
	weight int
}

type relationalSearchSource struct {
	cursor     kitdbengine.HistoryCursor
	generation uint64
	epoch      uint64
}

type relationalSearchBuild struct {
	signature string
	done      chan struct{}
	err       error
}

type relationalSearchState struct {
	projectionMu sync.RWMutex
	buildMu      sync.Mutex
	build        *relationalSearchBuild
}

func normalizeRelationalSearchOptions(
	databasePath string,
	databaseID string,
	maximumResultRows int,
	options Options,
) (relationalSearchConfiguration, error) {
	maximumResults := options.MaximumSearchResults
	if maximumResults == 0 {
		maximumResults = min(DefaultMaximumSearchResults, maximumResultRows)
	}
	if maximumResults < 1 || maximumResults > MaximumSearchResults || maximumResults > maximumResultRows {
		return relationalSearchConfiguration{}, fmt.Errorf(
			"kitdb: maximum search results must be between 1 and %d and not exceed maximum result rows",
			min(MaximumSearchResults, maximumResultRows),
		)
	}
	maximumCandidates := options.MaximumSearchCandidates
	if maximumCandidates == 0 {
		maximumCandidates = max(DefaultMaximumSearchCandidates, maximumResults)
	}
	if maximumCandidates < maximumResults || maximumCandidates > MaximumSearchCandidates {
		return relationalSearchConfiguration{}, fmt.Errorf(
			"kitdb: maximum search candidates must be between %d and %d",
			maximumResults, MaximumSearchCandidates,
		)
	}
	foregroundWait := options.SearchForegroundWait
	if foregroundWait == 0 {
		foregroundWait = DefaultSearchForegroundWait
	}
	if foregroundWait < 0 {
		return relationalSearchConfiguration{}, fmt.Errorf("kitdb: search foreground wait cannot be negative")
	}

	directory := filepath.Dir(databasePath)
	root := strings.TrimSpace(options.SearchRoot)
	if root == "" {
		if strings.EqualFold(filepath.Base(directory), ".data") {
			root = filepath.Join(directory, "search")
		} else {
			root = databasePath + ".search"
		}
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return relationalSearchConfiguration{}, fmt.Errorf("kitdb: resolve search root: %w", err)
	}
	absoluteRoot = filepath.Clean(absoluteRoot)
	if strings.EqualFold(absoluteRoot, filepath.Clean(databasePath)) {
		return relationalSearchConfiguration{}, fmt.Errorf("kitdb: search root cannot be the database file")
	}

	namespace := strings.TrimSpace(options.SearchNamespace)
	if namespace == "" {
		if strings.EqualFold(filepath.Base(directory), ".data") {
			// Preserve the historical Kitwork key for in-place adoption. The
			// projection itself remains owned by KitDB and contains no tenant state.
			namespace = filepath.Dir(directory) + "|kitdb|" + filepath.Base(databasePath)
		} else {
			namespace = "kitdb:" + databaseID
		}
	}
	if len(namespace) > 768 {
		return relationalSearchConfiguration{}, fmt.Errorf("kitdb: search namespace exceeds 768 bytes")
	}
	return relationalSearchConfiguration{
		root: absoluteRoot, namespace: namespace, manager: options.SearchManager,
		maximumResults: maximumResults, maximumCandidates: maximumCandidates,
		foregroundWait: foregroundWait,
	}, nil
}

func (engine *Engine) searchManagerFor() (*search.Manager, error) {
	engine.searchMu.Lock()
	defer engine.searchMu.Unlock()
	if engine.searchManager != nil {
		return engine.searchManager, nil
	}
	if engine.searchContext == nil || engine.searchContext.Err() != nil {
		return nil, fmt.Errorf("kitdb: relational engine is closed")
	}
	manager, err := search.NewManager(engine.searchRoot, engine.searchManagerOptions)
	if err != nil {
		return nil, err
	}
	engine.searchManager = manager
	return manager, nil
}

func (engine *Engine) relationalSearchState(indexKey string) *relationalSearchState {
	engine.searchMu.Lock()
	defer engine.searchMu.Unlock()
	state := engine.searchStates[indexKey]
	if state == nil {
		state = &relationalSearchState{}
		engine.searchStates[indexKey] = state
	}
	return state
}

func relationalSearchableColumns(schema kitdbsql.Schema) []relationalSearchColumn {
	columns := make([]relationalSearchColumn, 0)
	for _, field := range schema.Fields {
		if !field.Searchable {
			continue
		}
		weight := field.SearchWeight
		if weight <= 0 {
			weight = 1
		}
		columns = append(columns, relationalSearchColumn{field: field, weight: weight})
	}
	return columns
}

func newRelationalSearchSchema(
	columns []relationalSearchColumn,
) (search.Schema, []string, error) {
	fields := make([]search.Field, len(columns))
	names := make([]string, len(columns))
	analyzer := search.VietnameseAnalyzer()
	for index, column := range columns {
		fields[index] = search.Text(
			column.field.Name, analyzer, search.Boost(float64(column.weight)),
		)
		names[index] = column.field.Name
	}
	schema, err := search.NewSchema(fields...)
	return schema, names, err
}

func (engine *Engine) relationalSearchIndexKey(
	schema kitdbsql.Schema,
	projection search.Schema,
) string {
	fingerprint := projection.Fingerprint()
	scope := engine.searchNamespace + "|" + schema.Name + "|" + hex.EncodeToString(fingerprint[:])
	primary := schema.PrimaryFields()
	if len(primary) > 1 {
		identities := make([]string, len(primary))
		for index, field := range primary {
			identities[index] = field.ID
		}
		scope += "|composite-primary:" + strings.Join(identities, ",")
	} else {
		scope += "|kitdb-row-key-identifiers:v2"
	}
	digest := sha256.Sum256([]byte(scope))
	return "db-" + hex.EncodeToString(digest[:])
}

func relationalSearchIdentifierLayout(schema kitdbsql.Schema) searchprojection.IdentifierLayout {
	primary := schema.PrimaryFields()
	if len(primary) == 2 && primary[0].Kind == "text" &&
		(primary[1].Kind == "integer" || primary[1].Kind == "serial") {
		return searchprojection.IdentifierTextInteger
	}
	return searchprojection.IdentifierLogicalRowKey
}

func relationalSearchWatermark(
	databaseID string,
	schema kitdbsql.Schema,
	source relationalSearchSource,
) searchprojection.Watermark {
	return searchprojection.Watermark{
		Cursor: source.cursor, StructID: schema.ID,
		IdentifierLayout: relationalSearchIdentifierLayout(schema),
		RowGeneration:    source.generation, RowEpoch: source.epoch,
	}
}

func relationalSearchWatermarkMatches(
	watermark searchprojection.Watermark,
	databaseID string,
	schema kitdbsql.Schema,
	source relationalSearchSource,
) bool {
	return watermark.Cursor.DatabaseID == databaseID && watermark.StructID == schema.ID &&
		watermark.IdentifierLayout == relationalSearchIdentifierLayout(schema) &&
		watermark.RowGeneration == source.generation && watermark.RowEpoch == source.epoch
}

func readRelationalSearchWatermark(
	ctx context.Context,
	manager *search.Manager,
	indexKey string,
	schema search.Schema,
) (searchprojection.Watermark, bool, error) {
	encoded, err := manager.ReadCheckpoint(ctx, indexKey, schema)
	if err != nil || len(encoded) == 0 {
		return searchprojection.Watermark{}, false, err
	}
	watermark, err := searchprojection.DecodeWatermark(encoded)
	return watermark, err == nil, err
}

func writeRelationalSearchWatermark(
	ctx context.Context,
	manager *search.Manager,
	indexKey string,
	schema search.Schema,
	watermark searchprojection.Watermark,
) error {
	encoded, err := searchprojection.EncodeWatermark(watermark)
	if err != nil {
		return err
	}
	return manager.WriteCheckpoint(ctx, indexKey, schema, encoded)
}

func (engine *Engine) currentRelationalSearchSource(
	schema kitdbsql.Schema,
) (relationalSearchSource, error) {
	engine.writeMu.Lock()
	defer engine.writeMu.Unlock()
	generation, epoch, err := activeRowLayout(engine.database, schema)
	if err != nil {
		return relationalSearchSource{}, err
	}
	cursor, err := engine.database.CurrentCursor()
	return relationalSearchSource{cursor: cursor, generation: generation, epoch: epoch}, err
}

func (engine *Engine) relationalSearchProjectionFresh(
	ctx context.Context,
	manager *search.Manager,
	indexKey string,
	projection search.Schema,
	schema kitdbsql.Schema,
	source relationalSearchSource,
) (bool, error) {
	watermark, found, err := readRelationalSearchWatermark(ctx, manager, indexKey, projection)
	if errors.Is(err, searchprojection.ErrInvalidWatermark) {
		return false, nil
	}
	if err != nil || !found || !relationalSearchWatermarkMatches(
		watermark, engine.database.ID(), schema, source,
	) || watermark.Cursor != source.cursor {
		return false, err
	}
	info, err := manager.Info(ctx, indexKey, projection)
	return err == nil && info.Generation != 0, err
}

func relationalSearchBuildSignature(source relationalSearchSource) string {
	return fmt.Sprintf(
		"%s:%d:%08x:%d:%d",
		source.cursor.DatabaseID, source.cursor.Transaction, source.cursor.Checksum,
		source.generation, source.epoch,
	)
}

func (state *relationalSearchState) ensureBuild(
	signature string,
	run func() error,
) *relationalSearchBuild {
	state.buildMu.Lock()
	if current := state.build; current != nil {
		select {
		case <-current.done:
			if current.err == nil && current.signature == signature {
				state.buildMu.Unlock()
				return current
			}
		default:
			state.buildMu.Unlock()
			return current
		}
	}
	build := &relationalSearchBuild{signature: signature, done: make(chan struct{})}
	state.build = build
	state.buildMu.Unlock()
	go func() {
		err := run()
		state.buildMu.Lock()
		build.err = err
		close(build.done)
		state.buildMu.Unlock()
	}()
	return build
}

func (state *relationalSearchState) forgetCompletedBuild() {
	state.buildMu.Lock()
	defer state.buildMu.Unlock()
	if state.build == nil {
		return
	}
	select {
	case <-state.build.done:
		state.build = nil
	default:
	}
}

func waitRelationalSearchBuild(
	ctx context.Context,
	build *relationalSearchBuild,
	wait time.Duration,
) error {
	if wait <= 0 {
		select {
		case <-build.done:
			return build.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-build.done:
		return build.err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf(
			"%w for source %s; retry later", errRelationalSearchProjectionBuilding, build.signature,
		)
	}
}

func (engine *Engine) ensureRelationalSearchProjection(
	ctx context.Context,
	manager *search.Manager,
	state *relationalSearchState,
	indexKey string,
	projection search.Schema,
	schema kitdbsql.Schema,
	columns []relationalSearchColumn,
) error {
	for attempt := 0; attempt < 3; attempt++ {
		source, err := engine.currentRelationalSearchSource(schema)
		if err != nil {
			return err
		}
		fresh, err := engine.relationalSearchProjectionFresh(
			ctx, manager, indexKey, projection, schema, source,
		)
		if err != nil {
			return err
		}
		if fresh {
			return nil
		}
		state.forgetCompletedBuild()
		build := state.ensureBuild(relationalSearchBuildSignature(source), func() error {
			return engine.runRelationalSearchBuild(
				state, indexKey, projection, schema, columns,
			)
		})
		if err := waitRelationalSearchBuild(ctx, build, engine.searchForegroundWait); err != nil {
			return err
		}
	}
	return fmt.Errorf("kitdb: search projection could not reach a stable source boundary; retry")
}

func (engine *Engine) runRelationalSearchBuild(
	state *relationalSearchState,
	indexKey string,
	projection search.Schema,
	schema kitdbsql.Schema,
	columns []relationalSearchColumn,
) error {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if err := engine.readyLocked(); err != nil {
		return err
	}
	manager, err := engine.searchManagerFor()
	if err != nil {
		return err
	}
	state.projectionMu.Lock()
	defer state.projectionMu.Unlock()
	return engine.synchronizeRelationalSearchProjection(
		engine.searchContext, manager, indexKey, projection, schema, columns,
	)
}

func (engine *Engine) synchronizeRelationalSearchProjection(
	ctx context.Context,
	manager *search.Manager,
	indexKey string,
	projection search.Schema,
	schema kitdbsql.Schema,
	columns []relationalSearchColumn,
) error {
	source, err := engine.currentRelationalSearchSource(schema)
	if err != nil {
		return err
	}
	info, err := manager.Info(ctx, indexKey, projection)
	if err != nil {
		return err
	}
	watermark, found, watermarkErr := readRelationalSearchWatermark(
		ctx, manager, indexKey, projection,
	)
	if watermarkErr != nil && !errors.Is(watermarkErr, searchprojection.ErrInvalidWatermark) {
		return watermarkErr
	}
	if watermarkErr != nil {
		found = false
	}
	if found && info.Generation != 0 && relationalSearchWatermarkMatches(
		watermark, engine.database.ID(), schema, source,
	) {
		if watermark.Cursor == source.cursor {
			return nil
		}
		err = engine.catchUpRelationalSearchProjection(
			ctx, manager, indexKey, projection, schema, columns, watermark,
		)
		if err == nil {
			return nil
		}
		if !relationalSearchNeedsFullRebuild(err) {
			return err
		}
	}
	return engine.bootstrapRelationalSearchProjection(
		ctx, manager, indexKey, projection, schema, columns,
	)
}

func relationalSearchNeedsFullRebuild(err error) bool {
	return errors.Is(err, kitdbengine.ErrHistoryDisabled) ||
		errors.Is(err, kitdbengine.ErrHistoryGap) ||
		errors.Is(err, kitdbengine.ErrHistoryCursor) ||
		errors.Is(err, searchprojection.ErrInvalidWatermark)
}

func relationalSearchHistoryPin(schema kitdbsql.Schema) string {
	return "search/" + schema.ID
}

func (engine *Engine) bootstrapRelationalSearchProjection(
	ctx context.Context,
	manager *search.Manager,
	indexKey string,
	projection search.Schema,
	schema kitdbsql.Schema,
	columns []relationalSearchColumn,
) (returnErr error) {
	projectedTags := relationalSearchProjectionTags(columns)
	engine.writeMu.Lock()
	generation, epoch, err := activeRowLayout(engine.database, schema)
	if err != nil {
		engine.writeMu.Unlock()
		return err
	}
	if _, err := engine.database.Checkpoint(); err != nil {
		engine.writeMu.Unlock()
		return err
	}
	snapshot, err := engine.database.Snapshot()
	if err != nil {
		engine.writeMu.Unlock()
		return err
	}
	boundary := relationalSearchSource{
		cursor: snapshot.HistoryCursor(), generation: generation, epoch: epoch,
	}
	pinCreated, historyEnabled, err := pinInitialRelationalSearchBoundary(
		ctx, engine.database, relationalSearchHistoryPin(schema), boundary.cursor,
	)
	engine.writeMu.Unlock()
	if err != nil {
		_ = snapshot.Close()
		return err
	}
	keepPin := false
	snapshotOpen := true
	defer func() {
		if snapshotOpen {
			returnErr = errors.Join(returnErr, snapshot.Close())
		}
		if pinCreated && !keepPin {
			returnErr = errors.Join(returnErr, engine.database.ReleaseHistoryPin(
				context.Background(), relationalSearchHistoryPin(schema),
			))
		}
	}()

	replacement, err := manager.BeginReplacement(ctx, indexKey, projection)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, replacement.Abort()) }()
	if err := scanRelationalSearchSnapshot(
		ctx, snapshot, schema, generation, projectedTags,
		func(logicalKey []byte, values map[string]any) error {
			document, err := relationalSearchDocument(schema, logicalKey, values, columns)
			if err != nil {
				return err
			}
			return replacement.Add(ctx, document)
		},
	); err != nil {
		return err
	}
	if _, err := replacement.Commit(ctx); err != nil {
		return err
	}
	if err := snapshot.Close(); err != nil {
		return err
	}
	snapshotOpen = false
	watermark := relationalSearchWatermark(engine.database.ID(), schema, boundary)
	if err := writeRelationalSearchWatermark(ctx, manager, indexKey, projection, watermark); err != nil {
		return err
	}
	keepPin = pinCreated
	if !historyEnabled {
		current, err := engine.currentRelationalSearchSource(schema)
		if err != nil {
			return err
		}
		if current != boundary {
			return fmt.Errorf("kitdb: search source changed during a history-disabled projection build")
		}
		return nil
	}
	if _, err := engine.database.SetHistoryPin(
		ctx, relationalSearchHistoryPin(schema), boundary.cursor,
	); err != nil {
		return err
	}
	return engine.catchUpRelationalSearchProjection(
		ctx, manager, indexKey, projection, schema, columns, watermark,
	)
}

func pinInitialRelationalSearchBoundary(
	ctx context.Context,
	database *kitdbengine.DB,
	name string,
	cursor kitdbengine.HistoryCursor,
) (created bool, enabled bool, err error) {
	pins, err := database.HistoryPins()
	if errors.Is(err, kitdbengine.ErrHistoryDisabled) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
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

func scanRelationalSearchSnapshot(
	ctx context.Context,
	snapshot *kitdbengine.Snapshot,
	schema kitdbsql.Schema,
	generation uint64,
	projectedTags map[uint32]struct{},
	visit func(logicalKey []byte, values map[string]any) error,
) error {
	prefix, err := rowPrefix(schema, generation)
	if err != nil {
		return err
	}
	cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: prefix})
	if err != nil {
		return err
	}
	defer cursor.Close()
	for position := 0; cursor.Next(); position++ {
		if position&255 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		logicalKey, err := logicalRowKey(schema, cursor.Key(), generation)
		if err != nil {
			return err
		}
		decoded, err := decodeProjectedRow(schema, cursor.Value(), projectedTags)
		if err != nil {
			return err
		}
		if err := visit(logicalKey, decoded.values); err != nil {
			return err
		}
	}
	return cursor.Err()
}

func (engine *Engine) catchUpRelationalSearchProjection(
	ctx context.Context,
	manager *search.Manager,
	indexKey string,
	projection search.Schema,
	schema kitdbsql.Schema,
	columns []relationalSearchColumn,
	watermark searchprojection.Watermark,
) error {
	source := relationalSearchSource{
		cursor: watermark.Cursor, generation: watermark.RowGeneration, epoch: watermark.RowEpoch,
	}
	if !relationalSearchWatermarkMatches(watermark, engine.database.ID(), schema, source) {
		return errors.Join(kitdbengine.ErrHistoryCursor, fmt.Errorf("search watermark layout changed"))
	}
	if _, err := engine.database.SetHistoryPin(
		ctx, relationalSearchHistoryPin(schema), watermark.Cursor,
	); err != nil {
		return err
	}
	projectedTags := relationalSearchProjectionTags(columns)
	physicalPrefix, err := rowPrefix(schema, source.generation)
	if err != nil {
		return err
	}
	buffer := newRelationalSearchMutationBuffer(manager, indexKey, projection)
	rangeInfo, err := engine.database.WalkHistory(
		ctx,
		watermark.Cursor.Transaction,
		func(event kitdbengine.CommitEvent) error {
			for _, operation := range event.Operations {
				if !bytes.HasPrefix(operation.Key, physicalPrefix) {
					continue
				}
				logicalKey, err := logicalRowKey(schema, operation.Key, source.generation)
				if err != nil {
					return err
				}
				switch operation.Kind {
				case kitdbengine.CommitOperationPut:
					decoded, err := decodeProjectedRow(schema, operation.Value, projectedTags)
					if err != nil {
						return err
					}
					document, err := relationalSearchDocument(
						schema, logicalKey, decoded.values, columns,
					)
					if err != nil {
						return err
					}
					buffer.Upsert(document)
				case kitdbengine.CommitOperationDelete:
					identifier, err := relationalSearchDocumentID(schema, logicalKey)
					if err != nil {
						return err
					}
					buffer.Delete(identifier)
				default:
					return fmt.Errorf("kitdb: search history has unknown operation kind %d", operation.Kind)
				}
				if buffer.Len() >= searchCatchUpBatchSize {
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
	engine.writeMu.Lock()
	generation, epoch, layoutErr := activeRowLayout(engine.database, schema)
	engine.writeMu.Unlock()
	if layoutErr != nil {
		return layoutErr
	}
	if generation != source.generation || epoch != source.epoch {
		return errors.Join(kitdbengine.ErrHistoryCursor, fmt.Errorf("search row layout advanced"))
	}
	nextSource := relationalSearchSource{
		cursor: rangeInfo.Cursor(), generation: generation, epoch: epoch,
	}
	next := relationalSearchWatermark(engine.database.ID(), schema, nextSource)
	if err := writeRelationalSearchWatermark(ctx, manager, indexKey, projection, next); err != nil {
		return err
	}
	_, err = engine.database.SetHistoryPin(ctx, relationalSearchHistoryPin(schema), next.Cursor)
	return err
}

func relationalSearchProjectionTags(columns []relationalSearchColumn) map[uint32]struct{} {
	tags := make(map[uint32]struct{}, len(columns))
	for _, column := range columns {
		tags[column.field.Tag] = struct{}{}
	}
	return tags
}

func relationalSearchDocument(
	schema kitdbsql.Schema,
	logicalKey []byte,
	values map[string]any,
	columns []relationalSearchColumn,
) (search.Document, error) {
	identifier, err := relationalSearchDocumentID(schema, logicalKey)
	if err != nil {
		return search.Document{}, err
	}
	fields := make(map[string]string, len(columns))
	for _, column := range columns {
		if text := relationalSearchFieldText(values[column.field.Name]); text != "" {
			fields[column.field.Name] = text
		}
	}
	return search.Document{ID: identifier, Fields: fields}, nil
}

func relationalSearchFieldText(item any) string {
	switch current := item.(type) {
	case string:
		return current
	case []byte:
		return string(current)
	default:
		return ""
	}
}

func relationalSearchDocumentID(schema kitdbsql.Schema, logicalKey []byte) (string, error) {
	if len(logicalKey) == 0 {
		return "", fmt.Errorf("kitdb: search source row has an empty logical key")
	}
	if relationalSearchIdentifierLayout(schema) == searchprojection.IdentifierTextInteger {
		text, integer, err := decodeRelationalSearchTextIntegerKey(schema, logicalKey)
		if err != nil {
			return "", err
		}
		return encodeRelationalSearchTextIntegerIdentifier(text, integer), nil
	}
	return "k:" + hex.EncodeToString(logicalKey), nil
}

func relationalSearchRowKey(schema kitdbsql.Schema, identifier string) ([]byte, error) {
	if relationalSearchIdentifierLayout(schema) == searchprojection.IdentifierTextInteger {
		text, integer, err := decodeRelationalSearchTextIntegerIdentifier(identifier)
		if err != nil {
			return nil, err
		}
		primary := schema.PrimaryFields()
		key, err := rowKey(schema, map[string]any{
			primary[0].Name: text,
			primary[1].Name: integer,
		}, 0)
		if err != nil {
			return nil, err
		}
		if encodeRelationalSearchTextIntegerIdentifier(text, integer) != identifier {
			return nil, fmt.Errorf("kitdb: search identifier is not canonical")
		}
		return key, nil
	}
	if !strings.HasPrefix(identifier, "k:") {
		return nil, fmt.Errorf("kitdb: search identifier has an invalid prefix")
	}
	key, err := hex.DecodeString(strings.TrimPrefix(identifier, "k:"))
	if err != nil {
		return nil, fmt.Errorf("kitdb: decode search identifier: %w", err)
	}
	prefix, err := rowPrefix(schema, 0)
	if err != nil {
		return nil, err
	}
	if len(key) <= len(prefix) || !bytes.HasPrefix(key, prefix) {
		return nil, fmt.Errorf("kitdb: search identifier is outside table %q", schema.Name)
	}
	return key, nil
}

func decodeRelationalSearchTextIntegerKey(
	schema kitdbsql.Schema,
	logicalKey []byte,
) (string, int64, error) {
	prefix, err := rowPrefix(schema, 0)
	if err != nil {
		return "", 0, err
	}
	if len(logicalKey) <= len(prefix) || !bytes.HasPrefix(logicalKey, prefix) {
		return "", 0, fmt.Errorf("kitdb: search row key is outside table %q", schema.Name)
	}
	remaining := logicalKey[len(prefix):]
	textPayload, remaining, err := consumeRelationalSearchKeyComponent(remaining)
	if err != nil {
		return "", 0, err
	}
	integerPayload, remaining, err := consumeRelationalSearchKeyComponent(remaining)
	if err != nil {
		return "", 0, err
	}
	if len(remaining) != 0 || len(textPayload) == 0 || textPayload[0] != 3 ||
		len(integerPayload) != 9 || integerPayload[0] != 2 {
		return "", 0, fmt.Errorf("kitdb: search row key has an invalid text/integer identity")
	}
	ordered := binary.BigEndian.Uint64(integerPayload[1:])
	bits := ordered ^ uint64(1<<63)
	if ordered&uint64(1<<63) == 0 {
		bits = ^ordered
	}
	number := math.Float64frombits(bits)
	if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number ||
		number < -9223372036854775808 || number >= 9223372036854775808 {
		return "", 0, fmt.Errorf("kitdb: search row-key integer is outside int64")
	}
	integer := int64(number)
	encoded, err := scalarComponent(integer)
	if err != nil || !bytes.Equal(encoded, appendRelationalSearchComponent(nil, integerPayload)) {
		return "", 0, fmt.Errorf("kitdb: search row-key integer is not canonical")
	}
	return string(textPayload[1:]), integer, nil
}

func consumeRelationalSearchKeyComponent(encoded []byte) ([]byte, []byte, error) {
	length, size := binary.Uvarint(encoded)
	if size <= 0 || length == 0 || length > uint64(len(encoded)-size) {
		return nil, nil, fmt.Errorf("kitdb: search row key has an invalid scalar component")
	}
	end := size + int(length)
	return encoded[size:end], encoded[end:], nil
}

func appendRelationalSearchComponent(target, payload []byte) []byte {
	target = binary.AppendUvarint(target, uint64(len(payload)))
	return append(target, payload...)
}

func encodeRelationalSearchTextIntegerIdentifier(text string, integer int64) string {
	var ordered [8]byte
	binary.BigEndian.PutUint64(ordered[:], uint64(integer)^uint64(1<<63))
	return hex.EncodeToString([]byte(text)) + ":" + hex.EncodeToString(ordered[:])
}

func decodeRelationalSearchTextIntegerIdentifier(identifier string) (string, int64, error) {
	separator := len(identifier) - 17
	if separator < 0 || identifier[separator] != ':' || separator&1 != 0 {
		return "", 0, fmt.Errorf("kitdb: search identifier has an invalid text/integer layout")
	}
	text, err := hex.DecodeString(identifier[:separator])
	if err != nil {
		return "", 0, fmt.Errorf("kitdb: decode search identifier text: %w", err)
	}
	ordered, err := strconv.ParseUint(identifier[separator+1:], 16, 64)
	if err != nil {
		return "", 0, fmt.Errorf("kitdb: decode search identifier integer: %w", err)
	}
	return string(text), int64(ordered ^ uint64(1<<63)), nil
}

func relationalSearchIdentifierPrefix(
	schema kitdbsql.Schema,
	field string,
	text string,
) (string, bool) {
	if relationalSearchIdentifierLayout(schema) != searchprojection.IdentifierTextInteger {
		return "", false
	}
	primary := schema.PrimaryFields()
	if len(primary) != 2 || !strings.EqualFold(primary[0].Name, field) {
		return "", false
	}
	return hex.EncodeToString([]byte(text)) + ":", true
}

type relationalSearchBufferedMutation struct {
	document search.Document
	deleted  bool
}

type relationalSearchMutationBuffer struct {
	manager *search.Manager
	key     string
	schema  search.Schema
	order   []string
	items   map[string]relationalSearchBufferedMutation
}

func newRelationalSearchMutationBuffer(
	manager *search.Manager,
	key string,
	schema search.Schema,
) *relationalSearchMutationBuffer {
	return &relationalSearchMutationBuffer{
		manager: manager, key: key, schema: schema,
		items: make(map[string]relationalSearchBufferedMutation, searchCatchUpBatchSize),
	}
}

func (buffer *relationalSearchMutationBuffer) Len() int { return len(buffer.items) }

func (buffer *relationalSearchMutationBuffer) Upsert(document search.Document) {
	buffer.remember(document.ID, relationalSearchBufferedMutation{document: document})
}

func (buffer *relationalSearchMutationBuffer) Delete(identifier string) {
	buffer.remember(identifier, relationalSearchBufferedMutation{deleted: true})
}

func (buffer *relationalSearchMutationBuffer) remember(
	identifier string,
	mutation relationalSearchBufferedMutation,
) {
	if _, exists := buffer.items[identifier]; !exists {
		buffer.order = append(buffer.order, identifier)
	}
	buffer.items[identifier] = mutation
}

func (buffer *relationalSearchMutationBuffer) Flush(ctx context.Context) error {
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
