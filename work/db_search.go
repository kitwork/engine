package work

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/search"
	"github.com/kitwork/engine/value"
)

const (
	searchSignatureLimit = 512
	searchSnippetTokens  = 18
	searchSnippetContext = 4
	searchMaximumLimit   = 200
	searchForegroundWait = 2 * time.Second
)

var errKitDBSearchProjectionBuilding = errors.New("search projection is building in the background")

type searchIndexState struct {
	setupMu       sync.Mutex
	revisionReady bool
	rebuild       chan struct{}

	kitDBBuildMu sync.Mutex
	kitDBBuild   *kitDBSearchBuild
}

type kitDBSearchBuild struct {
	signature string
	done      chan struct{}
	err       error
}

type searchColumn struct {
	name   string
	weight int
	seq    uint64
}

func (t *SchemaTable) searchScopeKey() string {
	return tenantScopeKey(t.tenant) + "|" + t.engine + "|" + t.dbName + "|" + t.table
}

// searchableColumns returns fields in declaration order. That order is part
// of the persisted search schema fingerprint.
func (t *SchemaTable) searchableColumns() []searchColumn {
	var columns []searchColumn
	for name, spec := range t.columns {
		if !spec.searchable {
			continue
		}
		weight := spec.searchWt
		if weight <= 0 {
			weight = 1
		}
		columns = append(columns, searchColumn{name: name, weight: weight, seq: spec.seq})
	}
	sort.Slice(columns, func(left, right int) bool { return columns[left].seq < columns[right].seq })
	return columns
}

func (t *SchemaTable) primaryKeyColumn() string {
	best := ""
	bestSequence := ^uint64(0)
	for name, spec := range t.columns {
		if spec.primary && spec.seq < bestSequence {
			best = name
			bestSequence = spec.seq
		}
	}
	return best
}

// runSearch keeps the tenant database as source of truth and stores only a
// disposable immutable projection in the pure-Go search manager.
func (t *SchemaTable) runSearch() value.Value {
	columns := t.searchableColumns()
	if len(columns) == 0 {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db: table %q has no .searchable() column", t.table)}
	}
	primaryKey := t.primaryKeyColumn()
	if primaryKey == "" {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db: search on %q needs a primary key column; mark one .key()", t.table)}
	}
	if strings.TrimSpace(t.searchText) == "" {
		return value.New([]any{})
	}
	limit := t.limitN
	if limit <= 0 {
		limit = 20
	}
	if limit > searchMaximumLimit {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db: search limit exceeds %d", searchMaximumLimit)}
	}
	ctx := context.Background()
	if t.scope != nil {
		ctx = t.scope.Context()
	}
	manager, err := t.tenant.searchManagerFor()
	if err != nil || manager == nil {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db: search manager: %v", err)}
	}
	schema, allFields, err := newSearchSchema(columns)
	if err != nil {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db: search schema: %v", err)}
	}
	fields, snippetColumns, err := t.selectedSearchColumns(columns, allFields)
	if err != nil {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db: search: %v", err)}
	}
	indexKey := t.searchIndexKey(schema)
	state := t.tenant.searchIndexState(indexKey)
	if t.engine == "kitdb" {
		if t.transaction != nil {
			return value.Value{K: value.Invalid, V: "db: KitDB search is unavailable inside a record transaction"}
		}
		hits, err := t.searchFreshKitDBSnapshot(
			ctx, manager, state, indexKey, schema, fields, primaryKey, columns, limit,
		)
		if err != nil {
			return value.Value{K: value.Invalid, V: fmt.Sprintf("db: search: %v", err)}
		}
		t.searchHitCount = len(hits)
		return t.hydrateKitDBSearchHits(ctx, hits, primaryKey, snippetColumns)
	}
	source := t.source().db()
	if source == nil {
		return value.Value{K: value.Invalid, V: "db: search: database unavailable"}
	}
	if err := t.ensureSearchRevision(ctx, source, state); err != nil {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db: search revision: %v", err)}
	}
	hits, err := t.searchFreshSnapshot(ctx, source, manager, state, indexKey, schema, fields, primaryKey, columns, limit)
	if err != nil {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db: search: %v", err)}
	}
	t.searchHitCount = len(hits)
	return t.hydrateSearchHits(ctx, hits, primaryKey, snippetColumns)
}

func (t *SchemaTable) selectedSearchColumns(
	columns []searchColumn,
	allFields []string,
) ([]string, []searchColumn, error) {
	if len(t.searchFields) == 0 {
		return allFields, columns, nil
	}
	byName := make(map[string]searchColumn, len(columns))
	for _, column := range columns {
		byName[column.name] = column
	}
	fields := make([]string, 0, len(t.searchFields))
	selected := make([]searchColumn, 0, len(t.searchFields))
	seen := make(map[string]struct{}, len(t.searchFields))
	for _, field := range t.searchFields {
		column, exists := byName[field]
		if !exists {
			return nil, nil, fmt.Errorf("field %q is not searchable", field)
		}
		if _, duplicate := seen[field]; duplicate {
			return nil, nil, fmt.Errorf("duplicate search field %q", field)
		}
		seen[field] = struct{}{}
		fields = append(fields, field)
		selected = append(selected, column)
	}
	return fields, selected, nil
}

func (t *SchemaTable) searchFreshKitDBSnapshot(
	ctx context.Context,
	manager *search.Manager,
	state *searchIndexState,
	indexKey string,
	schema search.Schema,
	fields []string,
	primaryKey string,
	columns []searchColumn,
	limit int,
) ([]search.Hit, error) {
	query := search.MatchQuery{
		Fields: fields, Text: t.searchText, IdentifierPrefix: t.searchIdentifierPrefix,
	}
	options := search.SearchOptions{Limit: limit}
	background := *t
	background.scope = nil
	background.transaction = nil
	background.referential = nil
	background.q = nil
	background.searchText = ""
	background.searchFields = nil
	background.searchIdentifierPrefix = ""

	for attempt := 0; attempt < 3; attempt++ {
		current, err := t.readCurrentKitDBSearchCursor()
		if err != nil {
			return nil, err
		}
		fresh, err := t.kitDBSearchProjectionFresh(ctx, manager, indexKey, schema, current)
		if err != nil {
			return nil, err
		}
		if fresh {
			return manager.Search(ctx, indexKey, schema, query, options)
		}

		// A KitDB projection belongs to the node, not to the SQL statement
		// that first discovers it is stale. Forget only a completed worker;
		// concurrent callers continue sharing the active one.
		state.forgetCompletedKitDBSearchBuild()
		signature := kitDBSearchCursorSignature(current) + fmt.Sprintf(
			":%d:%d", t.rowGeneration, t.rowEpoch,
		)
		build := state.ensureKitDBSearchBuild(signature, func() error {
			return background.synchronizeKitDBSearchProjection(
				context.Background(), manager, indexKey, schema, primaryKey, columns,
			)
		})
		if err := waitKitDBSearchBuild(ctx, build, searchForegroundWait); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("search projection could not reach a stable source boundary; retry")
}

// ensureKitDBSearchBuild coalesces every request for one index behind a
// process-owned replacement. A failed or superseded completed build can be
// replaced by the next request; a successful build remains reusable until the
// source signature changes.
func (state *searchIndexState) ensureKitDBSearchBuild(
	signature string,
	run func() error,
) *kitDBSearchBuild {
	state.kitDBBuildMu.Lock()
	if existing := state.kitDBBuild; existing != nil {
		select {
		case <-existing.done:
			if existing.err == nil && existing.signature == signature {
				state.kitDBBuildMu.Unlock()
				return existing
			}
		default:
			state.kitDBBuildMu.Unlock()
			return existing
		}
	}
	build := &kitDBSearchBuild{signature: signature, done: make(chan struct{})}
	state.kitDBBuild = build
	state.kitDBBuildMu.Unlock()

	go func() {
		err := run()
		state.kitDBBuildMu.Lock()
		build.err = err
		close(build.done)
		state.kitDBBuildMu.Unlock()
	}()
	return build
}

func (state *searchIndexState) forgetCompletedKitDBSearchBuild() {
	state.kitDBBuildMu.Lock()
	defer state.kitDBBuildMu.Unlock()
	if state.kitDBBuild == nil {
		return
	}
	select {
	case <-state.kitDBBuild.done:
		state.kitDBBuild = nil
	default:
	}
}

func waitKitDBSearchBuild(
	ctx context.Context,
	build *kitDBSearchBuild,
	foregroundWait time.Duration,
) error {
	if foregroundWait <= 0 {
		select {
		case <-build.done:
			return build.err
		case <-ctx.Done():
			return fmt.Errorf("search projection continues building in the background: %w", ctx.Err())
		}
	}
	timer := time.NewTimer(foregroundWait)
	defer timer.Stop()
	select {
	case <-build.done:
		return build.err
	case <-ctx.Done():
		return fmt.Errorf("search projection continues building in the background: %w", ctx.Err())
	case <-timer.C:
		return fmt.Errorf("%w for source %s; retry later", errKitDBSearchProjectionBuilding, build.signature)
	}
}

func (t *SchemaTable) currentKitDBSearchSignature(database *kitdbengine.DB) (string, error) {
	transaction, err := database.LastTransaction()
	if err != nil {
		return "", err
	}
	contentRevision, revisionFound, err := loadKitDBContentRevision(
		database, t.definition,
	)
	if err != nil {
		return "", err
	}
	if revisionFound {
		return "r:" + strconv.FormatUint(contentRevision, 10), nil
	}
	statistics, found, dirty, err := loadKitDBStatistics(database, t.definition)
	if err != nil {
		return "", err
	}
	if found && !dirty && statistics != nil && statistics.StructID == t.definition.ID {
		// ANALYZE records the exact source snapshot it inspected. Its own
		// metadata commit and catalog-only changes cannot make the table's
		// search projection stale when this boundary is still current.
		transaction = statistics.AnalyzedTransaction
	}
	return "t:" + strconv.FormatUint(transaction, 10), nil
}

func kitDBSearchSignaturesMatch(stored, current string) bool {
	if stored == current {
		return true
	}
	if !strings.HasPrefix(current, "t:") {
		return false
	}
	legacyTransaction, _, legacy := strings.Cut(stored, ":")
	return legacy && legacyTransaction == strings.TrimPrefix(current, "t:")
}

func (t *SchemaTable) kitDBSearchProjectionTags(
	columns []searchColumn,
) (map[uint32]struct{}, error) {
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		names = append(names, column.name)
	}
	tags := make(map[uint32]struct{}, len(names))
	for _, name := range names {
		fieldIndex, found := t.definition.byName[name]
		if !found {
			return nil, fmt.Errorf("search projection has no field %q", name)
		}
		tags[t.definition.Fields[fieldIndex].Tag] = struct{}{}
	}
	return tags, nil
}

func newSearchSchema(columns []searchColumn) (search.Schema, []string, error) {
	analyzer := search.VietnameseAnalyzer()
	fields := make([]search.Field, len(columns))
	names := make([]string, len(columns))
	for position, column := range columns {
		fields[position] = search.Text(column.name, analyzer, search.Boost(float64(column.weight)))
		names[position] = column.name
	}
	schema, err := search.NewSchema(fields...)
	return schema, names, err
}

func (t *SchemaTable) searchIndexKey(schema search.Schema) string {
	fingerprint := schema.Fingerprint()
	scope := t.searchScopeKey() + "|" + hex.EncodeToString(fingerprint[:])
	if primary := t.definition.primaryFields(); len(primary) > 1 {
		identities := make([]string, len(primary))
		for index, field := range primary {
			identities[index] = field.ID
		}
		scope += "|composite-primary:" + strings.Join(identities, ",")
	} else if t.engine == "kitdb" {
		// Scalar KitDB indexes created before v2 used a textual primary-key
		// identifier. The logical row-key encoding is stable across key kinds and
		// makes history deletes directly replayable, so isolate the new layout.
		scope += "|kitdb-row-key-identifiers:v2"
	}
	digest := sha256.Sum256([]byte(scope))
	return "db-" + hex.EncodeToString(digest[:])
}

func (t *Tenant) searchIndexState(key string) *searchIndexState {
	t.searchMu.Lock()
	defer t.searchMu.Unlock()
	if t.searchStates == nil {
		t.searchStates = make(map[string]*searchIndexState)
	}
	state := t.searchStates[key]
	if state == nil {
		state = &searchIndexState{rebuild: make(chan struct{}, 1)}
		state.rebuild <- struct{}{}
		t.searchStates[key] = state
	}
	return state
}

func (t *SchemaTable) ensureSearchRevision(ctx context.Context, db *sql.DB, state *searchIndexState) error {
	state.setupMu.Lock()
	defer state.setupMu.Unlock()
	if state.revisionReady {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS "_kitwork_search_revision" (`+
		`"table_name" TEXT PRIMARY KEY, "revision" INTEGER NOT NULL) WITHOUT ROWID`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO "_kitwork_search_revision" ("table_name", "revision") VALUES (?, 0)`,
		t.table,
	); err != nil {
		return err
	}
	triggerHash := sha256.Sum256([]byte(t.table))
	triggerStem := "_kitwork_search_" + hex.EncodeToString(triggerHash[:8])
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		triggerName := triggerStem + "_" + strings.ToLower(event)
		statement := fmt.Sprintf(
			`CREATE TRIGGER IF NOT EXISTS %s AFTER %s ON %s BEGIN `+
				`UPDATE "_kitwork_search_revision" SET "revision" = "revision" + 1 WHERE "table_name" = %s; END`,
			quoteSearchIdentifier(triggerName), event, quoteSearchIdentifier(t.table), quoteSearchLiteral(t.table),
		)
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	state.revisionReady = true
	return nil
}

func (t *SchemaTable) searchFreshSnapshot(
	ctx context.Context,
	db *sql.DB,
	manager *search.Manager,
	state *searchIndexState,
	indexKey string,
	schema search.Schema,
	fields []string,
	primaryKey string,
	columns []searchColumn,
	limit int,
) ([]search.Hit, error) {
	signature, err := t.readCurrentSearchSignature(ctx, db)
	if err != nil {
		return nil, err
	}
	stored, err := t.readStoredSearchSignature(indexKey)
	if err != nil {
		return nil, err
	}
	query := search.MatchQuery{Fields: fields, Text: t.searchText}
	options := search.SearchOptions{Limit: limit}
	if stored == signature {
		hits, searchErr := manager.Search(ctx, indexKey, schema, query, options)
		if searchErr != nil {
			return nil, searchErr
		}
		if stats, exists := manager.IndexStats(indexKey); exists && stats.Generation != 0 {
			return hits, nil
		}
	}

	if err := acquireSearchRebuild(ctx, state.rebuild); err != nil {
		return nil, err
	}
	defer func() { state.rebuild <- struct{}{} }()

	// Another request may have completed the replacement while this request
	// waited for the per-index rebuild gate.
	signature, err = t.readCurrentSearchSignature(ctx, db)
	if err != nil {
		return nil, err
	}
	stored, err = t.readStoredSearchSignature(indexKey)
	if err != nil {
		return nil, err
	}
	if stored == signature {
		hits, searchErr := manager.Search(ctx, indexKey, schema, query, options)
		if searchErr != nil {
			return nil, searchErr
		}
		if stats, exists := manager.IndexStats(indexKey); exists && stats.Generation != 0 {
			return hits, nil
		}
	}

	if err := t.rebuildSearchIndex(ctx, db, manager, indexKey, schema, primaryKey, columns); err != nil {
		return nil, err
	}
	current, err := t.readCurrentSearchSignature(ctx, db)
	if err != nil {
		return nil, err
	}
	if current == signature {
		if err := t.writeStoredSearchSignature(indexKey, signature); err != nil {
			return nil, err
		}
	}
	return manager.Search(ctx, indexKey, schema, query, options)
}

func acquireSearchRebuild(ctx context.Context, gate chan struct{}) error {
	select {
	case <-gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *SchemaTable) rebuildSearchIndex(
	ctx context.Context,
	db *sql.DB,
	manager *search.Manager,
	indexKey string,
	schema search.Schema,
	primaryKey string,
	columns []searchColumn,
) (returnErr error) {
	replacement, err := manager.BeginReplacement(ctx, indexKey, schema)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, replacement.Abort())
	}()

	names := make([]string, 0, len(columns)+1)
	names = append(names, quoteSearchIdentifier(primaryKey))
	for _, column := range columns {
		names = append(names, quoteSearchIdentifier(column.name))
	}
	statement := fmt.Sprintf(
		"SELECT %s FROM %s",
		strings.Join(names, ", "), quoteSearchIdentifier(t.table),
	)
	rows, err := db.QueryContext(ctx, statement)
	if err != nil {
		return err
	}
	for rows.Next() {
		key := sql.NullString{}
		values := make([]sql.NullString, len(columns))
		destinations := make([]any, 0, len(columns)+1)
		destinations = append(destinations, &key)
		for position := range values {
			destinations = append(destinations, &values[position])
		}
		if err := rows.Scan(destinations...); err != nil {
			_ = rows.Close()
			return err
		}
		if !key.Valid || key.String == "" {
			_ = rows.Close()
			return fmt.Errorf("search source row has an empty primary key")
		}
		fields := make(map[string]string, len(columns))
		for position, column := range columns {
			if values[position].Valid {
				fields[column.name] = values[position].String
			}
		}
		if err := replacement.Add(ctx, search.Document{ID: key.String, Fields: fields}); err != nil {
			_ = rows.Close()
			return err
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	_, err = replacement.Commit(ctx)
	return err
}

func (t *SchemaTable) readCurrentSearchSignature(ctx context.Context, db *sql.DB) (string, error) {
	var revision uint64
	if err := db.QueryRowContext(
		ctx,
		`SELECT "revision" FROM "_kitwork_search_revision" WHERE "table_name" = ?`,
		t.table,
	).Scan(&revision); err != nil {
		return "", err
	}
	var schemaVersion uint64
	if err := db.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&schemaVersion); err != nil {
		return "", err
	}
	return strconv.FormatUint(revision, 10) + ":" + strconv.FormatUint(schemaVersion, 10), nil
}

func (t *SchemaTable) searchSignaturePath(indexKey string) string {
	return t.tenant.resolve(".data", "search-state", indexKey+".signature")
}

func (t *SchemaTable) readStoredSearchSignature(indexKey string) (string, error) {
	data, err := os.ReadFile(t.searchSignaturePath(indexKey))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return decodeStoredSearchSignature(data)
}

func decodeStoredSearchSignature(data []byte) (string, error) {
	if len(data) > searchSignatureLimit {
		return "", fmt.Errorf("search signature exceeds %d bytes", searchSignatureLimit)
	}
	signature := strings.TrimSuffix(string(data), "\n")
	signature = strings.TrimSuffix(signature, "\r")
	if signature == "" || strings.TrimSpace(signature) != signature {
		return "", fmt.Errorf("search signature is invalid")
	}
	return signature, nil
}

func (t *SchemaTable) writeStoredSearchSignature(indexKey string, signature string) error {
	if t.engine == "kitdb" {
		signature = t.proveKitDBSearchSignature(signature)
	}
	if len(signature) == 0 || len(signature) > searchSignatureLimit {
		return fmt.Errorf("search signature is invalid")
	}
	path := t.searchSignaturePath(indexKey)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(signature)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	returnErr := writeErr
	if closeErr := file.Close(); returnErr == nil {
		returnErr = closeErr
	}
	return returnErr
}

func (t *SchemaTable) hydrateSearchHits(
	ctx context.Context,
	hits []search.Hit,
	primaryKey string,
	columns []searchColumn,
) value.Value {
	if len(hits) == 0 {
		return value.New([]any{})
	}
	identifiers := make([]string, len(hits))
	for position, hit := range hits {
		identifiers[position] = hit.ID
	}
	rows := coerceResult(
		t.columns,
		t.source().Table(t.table).In(primaryKey, identifiers).Limit(len(hits)).Limited(len(hits)).List(),
	)
	if rows.K == value.Invalid {
		return rows
	}
	byIdentifier := make(map[string]value.Value, len(hits))
	for _, row := range rows.Array() {
		fields := row.Map()
		identifier, exists := fields[primaryKey]
		if row.K == value.Map && exists {
			byIdentifier[identifier.String()] = row
		}
	}
	result := make([]value.Value, 0, len(hits))
	for _, hit := range hits {
		row, exists := byIdentifier[hit.ID]
		if !exists {
			continue
		}
		fields := row.Map()
		snippet, err := bestSearchSnippet(ctx, fields, columns, t.searchText)
		if err != nil {
			return value.Value{K: value.Invalid, V: fmt.Sprintf("db: search snippet: %v", err)}
		}
		fields["_score"] = value.New(hit.Score)
		fields["_snippet"] = value.New(snippet)
		result = append(result, row)
	}
	return value.New(result)
}

func (t *SchemaTable) hydrateKitDBSearchHits(
	ctx context.Context,
	hits []search.Hit,
	primaryKey string,
	columns []searchColumn,
) value.Value {
	if len(hits) == 0 {
		return value.New([]any{})
	}
	if t.columns[primaryKey] == nil || len(t.definition.primaryFields()) == 0 {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db: search: no primary field %q", primaryKey)}
	}
	managed, err := t.kitDBManaged()
	if err != nil {
		return kitDBError(err)
	}
	defer managed.Release()
	managed.writeMu.RLock()
	if err := validateKitDBIndexLayoutEpoch(managed.database, t.definition, t.indexEpoch); err != nil {
		managed.writeMu.RUnlock()
		return kitDBError(err)
	}
	if err := validateKitDBRowLayoutEpoch(managed.database, t.definition, t.rowEpoch); err != nil {
		managed.writeMu.RUnlock()
		return kitDBError(err)
	}
	snapshot, err := managed.database.Snapshot()
	managed.writeMu.RUnlock()
	if err != nil {
		return kitDBError(err)
	}
	defer snapshot.Close()

	result := make([]value.Value, 0, len(hits))
	for position, hit := range hits {
		if position&31 == 0 {
			if err := ctx.Err(); err != nil {
				return kitDBError(err)
			}
		}
		rowKey, err := t.kitDBSearchRowKey(hit.ID)
		if err != nil {
			return kitDBError(err)
		}
		encoded, found, err := t.getKitDBRow(snapshot, rowKey)
		if err != nil {
			return kitDBError(err)
		}
		if !found {
			continue
		}
		decoded, err := decodeKitDBRow(t.definition, encoded)
		if err != nil {
			return kitDBError(err)
		}
		row := value.New(cloneKitDBRow(decoded.values))
		coerceResult(t.columns, row)
		fields := row.Map()
		snippet, err := bestSearchSnippet(ctx, fields, columns, t.searchText)
		if err != nil {
			return value.Value{K: value.Invalid, V: fmt.Sprintf("db: search snippet: %v", err)}
		}
		fields["_score"] = value.New(hit.Score)
		fields["_snippet"] = value.New(snippet)
		result = append(result, row)
	}
	return value.New(result)
}

func parseKitDBSearchIdentifier(kind, identifier string) (value.Value, error) {
	switch kind {
	case "integer", "smallint", "int32", "serial", "year", "month", "day":
		number, err := strconv.ParseInt(identifier, 10, 64)
		if err != nil {
			return value.Value{}, err
		}
		return value.New(number), nil
	case "float":
		number, err := strconv.ParseFloat(identifier, 64)
		if err != nil {
			return value.Value{}, err
		}
		return value.New(number), nil
	case "bool":
		flag, err := strconv.ParseBool(identifier)
		if err != nil {
			return value.Value{}, err
		}
		return coerceWrite(kind, value.New(flag)), nil
	default:
		return value.New(identifier), nil
	}
}

func bestSearchSnippet(
	ctx context.Context,
	row map[string]value.Value,
	columns []searchColumn,
	query string,
) (string, error) {
	var best search.Fragment
	bestQuality := -1
	for _, column := range columns {
		text := searchFieldText(row[column.name])
		if text == "" {
			continue
		}
		fragment, err := search.Highlight(
			ctx,
			search.VietnameseAnalyzer(),
			text,
			query,
			search.FragmentOptions{MaxTokens: searchSnippetTokens, ContextTokens: searchSnippetContext},
		)
		if err != nil {
			return "", err
		}
		quality := len(fragment.Matches) * column.weight
		if bestQuality < 0 || quality > bestQuality {
			best = fragment
			bestQuality = quality
		}
	}
	return renderSearchFragment(best), nil
}

func searchFieldText(field value.Value) string {
	switch field.K {
	case value.String:
		return field.String()
	case value.Bytes:
		return string(field.Bytes())
	default:
		return ""
	}
}

func renderSearchFragment(fragment search.Fragment) string {
	if fragment.Text == "" {
		return ""
	}
	var output strings.Builder
	output.Grow(len(fragment.Text) + len(fragment.Matches)*7 + 8)
	if fragment.PrefixElided {
		output.WriteString("... ")
	}
	cursor := 0
	for _, match := range fragment.Matches {
		if match.Start < cursor || match.End < match.Start || match.End > len(fragment.Text) {
			continue
		}
		output.WriteString(html.EscapeString(fragment.Text[cursor:match.Start]))
		output.WriteString("<b>")
		output.WriteString(html.EscapeString(fragment.Text[match.Start:match.End]))
		output.WriteString("</b>")
		cursor = match.End
	}
	output.WriteString(html.EscapeString(fragment.Text[cursor:]))
	if fragment.SuffixElided {
		output.WriteString(" ...")
	}
	return output.String()
}

func quoteSearchIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func quoteSearchLiteral(literal string) string {
	return `'` + strings.ReplaceAll(literal, `'`, `''`) + `'`
}
