package relational

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash"
	"hash/crc32"
	"html"
	"math"
	"strings"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	searchprojection "github.com/kitwork/engine/kitdb/searchprojection"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	"github.com/kitwork/engine/search"
)

const (
	defaultRelationalSearchLimit   = 20
	relationalSearchBatchSize      = 256
	relationalSearchSnippetTokens  = 18
	relationalSearchSnippetContext = 4
	relationalSearchCursorSize     = 192
)

var (
	relationalSearchCursorMagic = [8]byte{'K', 'S', 'C', 'U', 'R', '0', '0', '1'}
	relationalSearchCursorCRC   = crc32.MakeTable(crc32.Castagnoli)
)

type relationalSearchProjection struct {
	column Column
	field  string
}

type boundRelationalSearchPlan struct {
	schema             kitdbsql.Schema
	columns            []relationalSearchColumn
	projection         search.Schema
	indexKey           string
	fields             []string
	snippetColumns     []relationalSearchColumn
	resultProjection   []relationalSearchProjection
	predicate          *boundPredicate
	query              string
	identifierPrefix   string
	prefixCoversFilter bool
	limit              int
	offset             int
	afterText          string
	queryFingerprint   [sha256.Size]byte
}

type relationalSearchCursor struct {
	watermark         searchprojection.Watermark
	indexGeneration   uint64
	schemaFingerprint [sha256.Size]byte
	queryFingerprint  [sha256.Size]byte
	after             search.SearchAfter
}

type relationalSearchRow struct {
	values  map[string]any
	hit     search.Hit
	snippet string
}

func describeSearchSelect(
	schema kitdbsql.Schema,
	plan *kitdbsql.SelectStatement,
) ([]Column, error) {
	_, _, projections, err := validateRelationalSearchShape(schema, plan)
	if err != nil {
		return nil, err
	}
	columns := make([]Column, len(projections))
	for index, projection := range projections {
		columns[index] = projection.column
	}
	return columns, nil
}

func validateRelationalSearchShape(
	schema kitdbsql.Schema,
	plan *kitdbsql.SelectStatement,
) ([]relationalSearchColumn, []string, []relationalSearchProjection, error) {
	if plan == nil || plan.Search == nil {
		return nil, nil, nil, fmt.Errorf("kitdb SQL: invalid SEARCH plan")
	}
	if len(plan.Joins) != 0 {
		return nil, nil, nil, fmt.Errorf("kitdb SQL: SEARCH does not support JOIN yet")
	}
	if plan.Distinct {
		return nil, nil, nil, fmt.Errorf("kitdb SQL: SEARCH does not support DISTINCT yet")
	}
	if len(plan.GroupBy) != 0 || plan.Having != nil {
		return nil, nil, nil, fmt.Errorf("kitdb SQL: SEARCH does not support GROUP BY or HAVING yet")
	}
	for _, field := range schema.Fields {
		switch strings.ToLower(field.Name) {
		case "_score", "_snippet", "_cursor":
			return nil, nil, nil, fmt.Errorf("kitdb SQL: SEARCH reserves field name %q", field.Name)
		}
	}
	available := relationalSearchableColumns(schema)
	if len(available) == 0 {
		return nil, nil, nil, fmt.Errorf("kitdb SQL: table %q has no searchable field", schema.Name)
	}
	byName := make(map[string]relationalSearchColumn, len(available))
	for _, column := range available {
		byName[strings.ToLower(column.field.Name)] = column
	}
	fields := make([]string, 0, len(plan.Search.Fields))
	if len(plan.Search.Fields) == 0 {
		fields = make([]string, len(available))
		for index, column := range available {
			fields[index] = column.field.Name
		}
	} else {
		seen := make(map[string]struct{}, len(plan.Search.Fields))
		for _, requested := range plan.Search.Fields {
			if err := validateRelationalSearchQualifier(plan, searchQualifier(requested)); err != nil {
				return nil, nil, nil, err
			}
			canonical, _, found := schema.FieldByName(unqualifiedColumn(requested))
			if !found {
				return nil, nil, nil, fmt.Errorf("kitdb SQL: table %q has no field %q", schema.Name, requested)
			}
			column, searchable := byName[strings.ToLower(canonical)]
			if !searchable {
				return nil, nil, nil, fmt.Errorf("kitdb SQL: SEARCH field %q is not searchable", canonical)
			}
			key := strings.ToLower(column.field.Name)
			if _, duplicate := seen[key]; duplicate {
				return nil, nil, nil, fmt.Errorf("kitdb SQL: duplicate SEARCH field %q", requested)
			}
			seen[key] = struct{}{}
			fields = append(fields, column.field.Name)
		}
	}
	projections, err := bindRelationalSearchProjections(schema, plan)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := validateRelationalSearchOrder(plan); err != nil {
		return nil, nil, nil, err
	}
	return available, fields, projections, nil
}

func bindRelationalSearchProjections(
	schema kitdbsql.Schema,
	plan *kitdbsql.SelectStatement,
) ([]relationalSearchProjection, error) {
	result := make([]relationalSearchProjection, 0, len(plan.Projection))
	for _, projection := range plan.Projection {
		if projection.Aggregate != "" || projection.Count || projection.Expression != nil {
			return nil, fmt.Errorf("kitdb SQL: SEARCH supports row-field projections only")
		}
		if projection.All {
			if err := validateRelationalSearchQualifier(plan, projection.Qualifier); err != nil {
				return nil, err
			}
			for _, field := range schema.Fields {
				result = append(result, relationalSearchProjection{
					column: columnForField(field.Name, field), field: field.Name,
				})
			}
			continue
		}
		if err := validateRelationalSearchQualifier(plan, projection.Qualifier); err != nil {
			return nil, err
		}
		requested := unqualifiedColumn(projection.Name)
		field, kind := "", ""
		var source *kitdbsql.Field
		switch strings.ToLower(requested) {
		case "_score":
			field, kind = "_score", "float"
		case "_snippet":
			field, kind = "_snippet", "text"
		case "_cursor":
			field, kind = "_cursor", "text"
		default:
			canonical, definition, found := schema.FieldByName(requested)
			if !found {
				return nil, fmt.Errorf("kitdb SQL: table %q has no field %q", schema.Name, projection.Name)
			}
			field, kind = canonical, definition.Kind
			source = &definition
		}
		label := projection.Alias
		if label == "" {
			label = field
		}
		column := Column{Name: label, Kind: kind}
		if source != nil {
			column = columnForField(label, *source)
		}
		result = append(result, relationalSearchProjection{column: column, field: field})
	}
	return result, nil
}

func validateRelationalSearchOrder(plan *kitdbsql.SelectStatement) error {
	if len(plan.Order) == 0 {
		return nil
	}
	if len(plan.Order) != 1 || plan.Order[0].Expression != nil || !plan.Order[0].Descending {
		return fmt.Errorf("kitdb SQL: SEARCH accepts only ORDER BY _score DESC")
	}
	requested := unqualifiedColumn(plan.Order[0].Column)
	for _, projection := range plan.Projection {
		if projection.Alias != "" && strings.EqualFold(projection.Alias, requested) {
			requested = unqualifiedColumn(projection.Name)
			break
		}
	}
	if !strings.EqualFold(requested, "_score") {
		return fmt.Errorf("kitdb SQL: SEARCH accepts only ORDER BY _score DESC")
	}
	return nil
}

func searchQualifier(reference string) string {
	if marker := strings.LastIndex(reference, "."); marker >= 0 {
		return reference[:marker]
	}
	return ""
}

func validateRelationalSearchQualifier(plan *kitdbsql.SelectStatement, qualifier string) error {
	if qualifier == "" || strings.EqualFold(qualifier, plan.Table) ||
		(plan.TableAlias != "" && strings.EqualFold(qualifier, plan.TableAlias)) {
		return nil
	}
	return fmt.Errorf("kitdb SQL: unknown table qualifier %q", qualifier)
}

func (engine *Engine) bindRelationalSearchPlan(
	schema kitdbsql.Schema,
	plan *kitdbsql.SelectStatement,
	parameters []any,
) (boundRelationalSearchPlan, error) {
	columns, fields, resultProjection, err := validateRelationalSearchShape(schema, plan)
	if err != nil {
		return boundRelationalSearchPlan{}, err
	}
	queryValue, err := resolveLiteral(plan.Search.Query, parameters)
	if err != nil {
		return boundRelationalSearchPlan{}, err
	}
	query := ""
	switch current := queryValue.(type) {
	case nil:
	case string:
		query = current
	case []byte:
		query = string(current)
	default:
		return boundRelationalSearchPlan{}, fmt.Errorf("kitdb SQL: SEARCH query must be text, got %T", queryValue)
	}
	predicate, err := bindPredicate(schema, plan.Predicate, parameters)
	if err != nil {
		return boundRelationalSearchPlan{}, err
	}
	limit := defaultRelationalSearchLimit
	if plan.HasLimit {
		limit = plan.Limit
	}
	if limit > engine.maximumSearchResults {
		return boundRelationalSearchPlan{}, fmt.Errorf(
			"kitdb SQL: SEARCH LIMIT %d exceeds this server's search result limit of %d",
			limit, engine.maximumSearchResults,
		)
	}
	if plan.Offset > engine.maximumSearchCandidates-limit {
		return boundRelationalSearchPlan{}, fmt.Errorf(
			"kitdb SQL: SEARCH LIMIT plus OFFSET exceeds this server's candidate budget of %d",
			engine.maximumSearchCandidates,
		)
	}
	afterText := ""
	if plan.HasAfter {
		item, err := resolveLiteral(plan.After, parameters)
		if err != nil {
			return boundRelationalSearchPlan{}, err
		}
		switch current := item.(type) {
		case string:
			afterText = current
		case []byte:
			afterText = string(current)
		case nil:
		default:
			return boundRelationalSearchPlan{}, fmt.Errorf("kitdb SQL: AFTER cursor must be text, got %T", item)
		}
		if strings.TrimSpace(afterText) == "" {
			return boundRelationalSearchPlan{}, fmt.Errorf("kitdb SQL: AFTER cursor cannot be empty")
		}
	}
	projection, _, err := newRelationalSearchSchema(columns)
	if err != nil {
		return boundRelationalSearchPlan{}, err
	}
	selected := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		selected[field] = struct{}{}
	}
	snippetColumns := make([]relationalSearchColumn, 0, len(fields))
	for _, column := range columns {
		if _, found := selected[column.field.Name]; found {
			snippetColumns = append(snippetColumns, column)
		}
	}
	identifierPrefix, _, prefixCoversFilter := relationalSearchPrefixFromPredicate(schema, predicate)
	bound := boundRelationalSearchPlan{
		schema: schema, columns: columns, projection: projection,
		indexKey: engine.relationalSearchIndexKey(schema, projection),
		fields:   fields, snippetColumns: snippetColumns, resultProjection: resultProjection,
		predicate: predicate, query: query, identifierPrefix: identifierPrefix,
		prefixCoversFilter: prefixCoversFilter,
		limit:              limit, offset: plan.Offset, afterText: afterText,
	}
	bound.queryFingerprint, err = relationalSearchQueryFingerprint(bound)
	return bound, err
}

func relationalSearchPrefixFromPredicate(
	schema kitdbsql.Schema,
	predicate *boundPredicate,
) (prefix string, found bool, covers bool) {
	if predicate == nil {
		return "", false, false
	}
	if predicate.kind == "binary" && predicate.operator == "and" {
		if len(predicate.arguments) != 2 {
			return "", false, false
		}
		leftPrefix, leftFound, leftCovers := relationalSearchPrefixFromPredicate(schema, predicate.arguments[0])
		rightPrefix, rightFound, rightCovers := relationalSearchPrefixFromPredicate(schema, predicate.arguments[1])
		switch {
		case leftFound && rightFound && leftPrefix == rightPrefix:
			return leftPrefix, true, leftCovers && rightCovers
		case leftFound:
			return leftPrefix, true, false
		case rightFound:
			return rightPrefix, true, false
		default:
			return "", false, false
		}
	}
	if predicate.kind != "binary" || predicate.operator != "=" || len(predicate.arguments) != 2 {
		return "", false, false
	}
	for _, pair := range [][2]*boundPredicate{
		{predicate.arguments[0], predicate.arguments[1]},
		{predicate.arguments[1], predicate.arguments[0]},
	} {
		if pair[0] == nil || pair[0].field == nil || pair[1] == nil || pair[1].kind != "literal" {
			continue
		}
		text, ok := pair[1].literal.(string)
		if !ok {
			continue
		}
		if prefix, supported := relationalSearchIdentifierPrefix(schema, pair[0].field.Name, text); supported {
			return prefix, true, true
		}
	}
	return "", false, false
}

func (engine *Engine) executeSearchSelect(
	ctx context.Context,
	plan *kitdbsql.SelectStatement,
	parameters []any,
) (Result, error) {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if err := engine.readyLocked(); err != nil {
		return Result{}, err
	}
	schema, err := engine.schemaLocked(plan.Table)
	if err != nil {
		return Result{}, err
	}
	bound, err := engine.bindRelationalSearchPlan(schema, plan, parameters)
	if err != nil {
		return Result{}, err
	}
	result := Result{Columns: make([]Column, len(bound.resultProjection))}
	for index, projection := range bound.resultProjection {
		result.Columns[index] = projection.column
	}
	if bound.limit == 0 || strings.TrimSpace(bound.query) == "" {
		result.CommandTag = "SELECT 0"
		return result, nil
	}
	if engine.experimentalProjections {
		rows, stats, err := engine.executePackedSearch(ctx, bound)
		if err != nil {
			return Result{}, err
		}
		result.Rows = rows
		result.CommandTag = fmt.Sprintf("SELECT %d", len(rows))
		result.Execution = stats
		return result, nil
	}
	manager, err := engine.searchManagerFor()
	if err != nil {
		return Result{}, err
	}
	state := engine.relationalSearchState(bound.indexKey)
	for attempt := 0; attempt < 3; attempt++ {
		if err := engine.ensureRelationalSearchProjection(
			ctx, manager, state, bound.indexKey, bound.projection, bound.schema, bound.columns,
		); err != nil {
			return Result{}, err
		}
		rows, stable, err := engine.executeStableRelationalSearch(ctx, manager, state, bound)
		if err != nil {
			return Result{}, err
		}
		if !stable {
			continue
		}
		result.Rows = rows
		result.CommandTag = fmt.Sprintf("SELECT %d", len(rows))
		return result, nil
	}
	return Result{}, fmt.Errorf("kitdb: SEARCH source or projection changed repeatedly; retry")
}

func (engine *Engine) executeStableRelationalSearch(
	ctx context.Context,
	manager *search.Manager,
	state *relationalSearchState,
	plan boundRelationalSearchPlan,
) ([][]any, bool, error) {
	state.projectionMu.RLock()
	defer state.projectionMu.RUnlock()
	watermark, found, err := readRelationalSearchWatermark(
		ctx, manager, plan.indexKey, plan.projection,
	)
	if err != nil || !found {
		return nil, false, err
	}
	before, err := manager.Info(ctx, plan.indexKey, plan.projection)
	if err != nil || before.Generation == 0 {
		return nil, false, err
	}
	engine.writeMu.Lock()
	generation, epoch, layoutErr := activeRowLayout(engine.database, plan.schema)
	var snapshot *kitdbengine.Snapshot
	if layoutErr == nil {
		snapshot, layoutErr = engine.database.Snapshot()
	}
	engine.writeMu.Unlock()
	if layoutErr != nil {
		return nil, false, layoutErr
	}
	defer snapshot.Close()
	source := relationalSearchSource{
		cursor: snapshot.HistoryCursor(), generation: generation, epoch: epoch,
	}
	if !relationalSearchWatermarkMatches(
		watermark, engine.database.ID(), plan.schema, source,
	) || watermark.Cursor != source.cursor {
		return nil, false, nil
	}
	after, err := decodeAndValidateRelationalSearchCursor(
		plan.afterText, watermark, before.Generation, plan.projection.Fingerprint(), plan.queryFingerprint,
	)
	if err != nil {
		return nil, false, err
	}
	qualified, err := engine.collectRelationalSearchRows(
		ctx, func(ctx context.Context, query search.MatchQuery, options search.SearchOptions) ([]search.Hit, error) {
			return manager.Search(ctx, plan.indexKey, plan.projection, query, options)
		}, snapshot, plan, watermark, after,
	)
	if err != nil {
		return nil, false, err
	}
	afterInfo, err := manager.Info(ctx, plan.indexKey, plan.projection)
	if err != nil {
		return nil, false, err
	}
	if afterInfo.Generation != before.Generation {
		return nil, false, nil
	}
	start := min(plan.offset, len(qualified))
	end := min(start+plan.limit, len(qualified))
	rows := make([][]any, 0, end-start)
	for _, row := range qualified[start:end] {
		projected, err := projectRelationalSearchRow(
			plan, watermark, before.Generation, row,
		)
		if err != nil {
			return nil, false, err
		}
		rows = append(rows, projected)
	}
	return rows, true, nil
}

func (engine *Engine) collectRelationalSearchRows(
	ctx context.Context,
	searcher func(context.Context, search.MatchQuery, search.SearchOptions) ([]search.Hit, error),
	snapshot *kitdbengine.Snapshot,
	plan boundRelationalSearchPlan,
	watermark searchprojection.Watermark,
	after *search.SearchAfter,
) ([]relationalSearchRow, error) {
	needed := plan.offset + plan.limit
	qualified := make([]relationalSearchRow, 0, needed)
	scanned := 0
	exhausted := false
	currentAfter := after
	for len(qualified) < needed && scanned < engine.maximumSearchCandidates {
		remaining := engine.maximumSearchCandidates - scanned
		batchTarget := needed - len(qualified)
		if plan.predicate != nil && !plan.prefixCoversFilter {
			batchTarget = max(relationalSearchBatchSize, batchTarget)
		}
		batchLimit := min(
			batchTarget,
			remaining,
			search.MaximumSearchLimit,
		)
		hits, err := searcher(
			ctx,
			search.MatchQuery{
				Fields: plan.fields, Text: plan.query, IdentifierPrefix: plan.identifierPrefix,
			},
			search.SearchOptions{Limit: batchLimit, After: currentAfter},
		)
		if err != nil {
			return nil, err
		}
		if len(hits) == 0 {
			exhausted = true
			break
		}
		scanned += len(hits)
		for position, hit := range hits {
			if position&31 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			currentAfter = &search.SearchAfter{Score: hit.Score, Ordinal: hit.Ordinal}
			logicalKey, err := relationalSearchRowKey(plan.schema, hit.ID)
			if err != nil {
				return nil, err
			}
			physicalKey, err := physicalRowKey(plan.schema, logicalKey, watermark.RowGeneration)
			if err != nil {
				return nil, err
			}
			encoded, found, err := snapshot.Get(physicalKey)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, fmt.Errorf("kitdb: search projection references a missing source row")
			}
			decoded, err := decodeRow(plan.schema, encoded)
			if err != nil {
				return nil, err
			}
			matched, err := predicateMatches(decoded.values, plan.predicate)
			if err != nil {
				return nil, fmt.Errorf("kitdb SQL: SEARCH row filter: %w", err)
			}
			if !matched {
				continue
			}
			snippet := ""
			if relationalSearchProjectionNeeds(plan.resultProjection, "_snippet") {
				snippet, err = bestRelationalSearchSnippet(
					ctx, decoded.values, plan.snippetColumns, plan.query,
				)
				if err != nil {
					return nil, err
				}
			}
			qualified = append(qualified, relationalSearchRow{
				values: decoded.values, hit: hit, snippet: snippet,
			})
			if len(qualified) >= needed {
				break
			}
		}
		if len(qualified) >= needed {
			break
		}
		if len(hits) < batchLimit {
			exhausted = true
			break
		}
	}
	if len(qualified) < needed && !exhausted && scanned >= engine.maximumSearchCandidates {
		return nil, fmt.Errorf(
			"kitdb SQL: SEARCH row filter exhausted this server's %d-candidate budget; narrow the filter or raise max-search-candidates",
			engine.maximumSearchCandidates,
		)
	}
	return qualified, nil
}

func relationalSearchProjectionNeeds(
	projections []relationalSearchProjection,
	field string,
) bool {
	for _, projection := range projections {
		if projection.field == field {
			return true
		}
	}
	return false
}

func projectRelationalSearchRow(
	plan boundRelationalSearchPlan,
	watermark searchprojection.Watermark,
	indexGeneration uint64,
	row relationalSearchRow,
) ([]any, error) {
	result := make([]any, len(plan.resultProjection))
	for index, projection := range plan.resultProjection {
		switch projection.field {
		case "_score":
			result[index] = row.hit.Score
		case "_snippet":
			result[index] = row.snippet
		case "_cursor":
			cursor, err := encodeRelationalSearchCursor(relationalSearchCursor{
				watermark: watermark, indexGeneration: indexGeneration,
				schemaFingerprint: plan.projection.Fingerprint(),
				queryFingerprint:  plan.queryFingerprint,
				after:             search.SearchAfter{Score: row.hit.Score, Ordinal: row.hit.Ordinal},
			})
			if err != nil {
				return nil, err
			}
			result[index] = cursor
		default:
			_, field, _ := plan.schema.FieldByName(projection.field)
			result[index] = readField(field, row.values[projection.field])
		}
	}
	return result, nil
}

func bestRelationalSearchSnippet(
	ctx context.Context,
	row map[string]any,
	columns []relationalSearchColumn,
	query string,
) (string, error) {
	var best search.Fragment
	bestQuality := -1
	for _, column := range columns {
		text := relationalSearchFieldText(row[column.field.Name])
		if text == "" {
			continue
		}
		fragment, err := search.Highlight(
			ctx,
			search.VietnameseAnalyzer(),
			text,
			query,
			search.FragmentOptions{
				MaxTokens: relationalSearchSnippetTokens, ContextTokens: relationalSearchSnippetContext,
			},
		)
		if err != nil {
			return "", fmt.Errorf("kitdb: search snippet: %w", err)
		}
		quality := len(fragment.Matches) * column.weight
		if bestQuality < 0 || quality > bestQuality {
			best, bestQuality = fragment, quality
		}
	}
	return renderRelationalSearchFragment(best), nil
}

func renderRelationalSearchFragment(fragment search.Fragment) string {
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

func (engine *Engine) executeSearchExplain(
	ctx context.Context,
	plan *kitdbsql.SelectStatement,
	parameters []any,
) (Result, error) {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if err := engine.readyLocked(); err != nil {
		return Result{}, err
	}
	schema, err := engine.schemaLocked(plan.Table)
	if err != nil {
		return Result{}, err
	}
	bound, err := engine.bindRelationalSearchPlan(schema, plan, parameters)
	if err != nil {
		return Result{}, err
	}
	detail := fmt.Sprintf(
		"table=%s projection=BM25 index=%s fields=(%s) order=_score_desc result_limit=%d candidate_budget=%d",
		schema.Name, bound.indexKey, strings.Join(bound.fields, ","), bound.limit,
		engine.maximumSearchCandidates,
	)
	if engine.experimentalProjections {
		detail += " storage=single-file-snapshot refresh=explicit"
	}
	if bound.identifierPrefix != "" {
		detail += " identifier_prefix=true"
	}
	if bound.predicate != nil {
		detail += " row_filter=true"
	}
	if bound.afterText != "" {
		detail += " search_after=true"
	}
	return Result{
		Columns: explainColumns(),
		Rows:    [][]any{{int64(0), "ranked search", detail}}, CommandTag: "EXPLAIN",
	}, nil
}

func (engine *Engine) executeSearchExplainAnalyze(
	ctx context.Context,
	plan *kitdbsql.SelectStatement,
	parameters []any,
) (Result, error) {
	planningStarted := time.Now()
	explained, err := engine.executeSearchExplain(ctx, plan, parameters)
	planningElapsed := time.Since(planningStarted)
	if err != nil {
		return Result{}, err
	}
	executionStarted := time.Now()
	actual, err := engine.executeSearchSelect(ctx, plan, parameters)
	executionElapsed := time.Since(executionStarted)
	if err != nil {
		return Result{}, err
	}
	return finishExplainAnalyze(explained, actual, planningElapsed, executionElapsed), nil
}

func relationalSearchQueryFingerprint(
	plan boundRelationalSearchPlan,
) ([sha256.Size]byte, error) {
	digest := sha256.New()
	writeSearchHashString(digest, "kitdb-search-query-v1")
	writeSearchHashString(digest, plan.query)
	writeSearchHashString(digest, plan.identifierPrefix)
	for _, field := range plan.fields {
		writeSearchHashString(digest, field)
	}
	if err := writeSearchPredicateHash(digest, plan.predicate); err != nil {
		return [sha256.Size]byte{}, err
	}
	var result [sha256.Size]byte
	copy(result[:], digest.Sum(nil))
	return result, nil
}

func writeSearchPredicateHash(digest hash.Hash, predicate *boundPredicate) error {
	if predicate == nil {
		_, _ = digest.Write([]byte{0})
		return nil
	}
	_, _ = digest.Write([]byte{1})
	writeSearchHashString(digest, predicate.kind)
	writeSearchHashString(digest, predicate.operator)
	if predicate.field != nil {
		writeSearchHashString(digest, predicate.field.ID)
	} else {
		writeSearchHashString(digest, "")
	}
	if predicate.kind == "literal" {
		writeSearchHashString(digest, fmt.Sprintf("%T", predicate.literal))
		encoded, err := json.Marshal(predicate.literal)
		if err != nil {
			return fmt.Errorf("kitdb: encode SEARCH cursor predicate: %w", err)
		}
		writeSearchHashBytes(digest, encoded)
	}
	for _, argument := range predicate.arguments {
		if err := writeSearchPredicateHash(digest, argument); err != nil {
			return err
		}
	}
	return nil
}

func writeSearchHashString(digest hash.Hash, text string) {
	writeSearchHashBytes(digest, []byte(text))
}

func writeSearchHashBytes(digest hash.Hash, value []byte) {
	var size [8]byte
	binary.LittleEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = digest.Write(size[:])
	_, _ = digest.Write(value)
}

func encodeRelationalSearchCursor(cursor relationalSearchCursor) (string, error) {
	watermark, err := searchprojection.EncodeWatermark(cursor.watermark)
	if err != nil {
		return "", err
	}
	if math.IsNaN(cursor.after.Score) || math.IsInf(cursor.after.Score, 0) || cursor.after.Score < 0 {
		return "", fmt.Errorf("kitdb: SEARCH cursor score is invalid")
	}
	encoded := make([]byte, relationalSearchCursorSize)
	copy(encoded[:8], relationalSearchCursorMagic[:])
	binary.LittleEndian.PutUint32(encoded[8:12], 1)
	binary.LittleEndian.PutUint32(encoded[12:16], relationalSearchCursorSize)
	copy(encoded[16:100], watermark)
	binary.LittleEndian.PutUint64(encoded[100:108], cursor.indexGeneration)
	copy(encoded[108:140], cursor.schemaFingerprint[:])
	copy(encoded[140:172], cursor.queryFingerprint[:])
	binary.LittleEndian.PutUint64(encoded[172:180], math.Float64bits(cursor.after.Score))
	binary.LittleEndian.PutUint64(encoded[180:188], cursor.after.Ordinal)
	binary.LittleEndian.PutUint32(
		encoded[188:192], crc32.Checksum(encoded[:188], relationalSearchCursorCRC),
	)
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeRelationalSearchCursor(text string) (relationalSearchCursor, error) {
	encoded, err := base64.RawURLEncoding.DecodeString(text)
	if err != nil || len(encoded) != relationalSearchCursorSize {
		return relationalSearchCursor{}, fmt.Errorf("kitdb SQL: SEARCH cursor is malformed")
	}
	if string(encoded[:8]) != string(relationalSearchCursorMagic[:]) ||
		binary.LittleEndian.Uint32(encoded[8:12]) != 1 ||
		binary.LittleEndian.Uint32(encoded[12:16]) != relationalSearchCursorSize {
		return relationalSearchCursor{}, fmt.Errorf("kitdb SQL: SEARCH cursor version is unsupported")
	}
	if crc32.Checksum(encoded[:188], relationalSearchCursorCRC) != binary.LittleEndian.Uint32(encoded[188:192]) {
		return relationalSearchCursor{}, fmt.Errorf("kitdb SQL: SEARCH cursor checksum mismatch")
	}
	watermark, err := searchprojection.DecodeWatermark(encoded[16:100])
	if err != nil {
		return relationalSearchCursor{}, fmt.Errorf("kitdb SQL: SEARCH cursor watermark: %w", err)
	}
	result := relationalSearchCursor{
		watermark:       watermark,
		indexGeneration: binary.LittleEndian.Uint64(encoded[100:108]),
		after: search.SearchAfter{
			Score:   math.Float64frombits(binary.LittleEndian.Uint64(encoded[172:180])),
			Ordinal: binary.LittleEndian.Uint64(encoded[180:188]),
		},
	}
	copy(result.schemaFingerprint[:], encoded[108:140])
	copy(result.queryFingerprint[:], encoded[140:172])
	if math.IsNaN(result.after.Score) || math.IsInf(result.after.Score, 0) || result.after.Score < 0 {
		return relationalSearchCursor{}, fmt.Errorf("kitdb SQL: SEARCH cursor score is invalid")
	}
	return result, nil
}

func decodeAndValidateRelationalSearchCursor(
	text string,
	watermark searchprojection.Watermark,
	indexGeneration uint64,
	schemaFingerprint [sha256.Size]byte,
	queryFingerprint [sha256.Size]byte,
) (*search.SearchAfter, error) {
	if text == "" {
		return nil, nil
	}
	cursor, err := decodeRelationalSearchCursor(text)
	if err != nil {
		return nil, err
	}
	if cursor.watermark != watermark {
		return nil, fmt.Errorf("kitdb SQL: SEARCH cursor is stale because the source database changed")
	}
	if cursor.indexGeneration != indexGeneration || cursor.schemaFingerprint != schemaFingerprint {
		return nil, fmt.Errorf("kitdb SQL: SEARCH cursor is stale because the projection changed")
	}
	if cursor.queryFingerprint != queryFingerprint {
		return nil, fmt.Errorf("kitdb SQL: SEARCH cursor belongs to a different query")
	}
	return &cursor.after, nil
}
