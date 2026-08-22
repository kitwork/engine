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

	"github.com/kitwork/engine/search"
	"github.com/kitwork/engine/value"
)

const (
	searchSignatureLimit = 512
	searchSnippetTokens  = 18
	searchSnippetContext = 4
	searchMaximumLimit   = 200
)

type searchIndexState struct {
	setupMu       sync.Mutex
	revisionReady bool
	rebuild       chan struct{}
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
	source := t.source().db()
	if source == nil {
		return value.Value{K: value.Invalid, V: "db: search: database unavailable"}
	}
	manager, err := t.tenant.searchManagerFor()
	if err != nil || manager == nil {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db: search manager: %v", err)}
	}
	schema, fields, err := newSearchSchema(columns)
	if err != nil {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db: search schema: %v", err)}
	}
	indexKey := t.searchIndexKey(schema)
	state := t.tenant.searchIndexState(indexKey)
	if err := t.ensureSearchRevision(ctx, source, state); err != nil {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db: search revision: %v", err)}
	}
	hits, err := t.searchFreshSnapshot(ctx, source, manager, state, indexKey, schema, fields, primaryKey, columns, limit)
	if err != nil {
		return value.Value{K: value.Invalid, V: fmt.Sprintf("db: search: %v", err)}
	}
	return t.hydrateSearchHits(ctx, hits, primaryKey, columns)
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
	digest := sha256.Sum256([]byte(t.searchScopeKey() + "|" + hex.EncodeToString(fingerprint[:])))
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
	if len(data) > searchSignatureLimit {
		return "", fmt.Errorf("search signature exceeds %d bytes", searchSignatureLimit)
	}
	return string(data), nil
}

func (t *SchemaTable) writeStoredSearchSignature(indexKey string, signature string) error {
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
