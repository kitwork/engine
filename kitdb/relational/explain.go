package relational

import (
	"context"
	"fmt"
	"strings"
	"time"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func explainColumns() []Column {
	return []Column{
		{Name: "id", Kind: "integer"},
		{Name: "operation", Kind: "text"},
		{Name: "detail", Kind: "text"},
	}
}

func (transaction *Transaction) executeExplain(
	ctx context.Context,
	plan *kitdbsql.SelectStatement,
	parameters []any,
) (Result, error) {
	if err := transaction.ready(); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if plan == nil {
		return Result{}, fmt.Errorf("kitdb SQL: invalid EXPLAIN plan")
	}
	if selectUsesMaterialization(plan) {
		return transaction.executeMaterializedExplain(plan)
	}
	if len(plan.SetOperations) != 0 {
		return transaction.executeCompoundExplain(ctx, plan, parameters)
	}
	if plan.Search != nil {
		return Result{}, fmt.Errorf("kitdb SQL: EXPLAIN SEARCH requires the engine autocommit path")
	}
	if plan.Table == "" {
		return Result{
			Columns:    explainColumns(),
			Rows:       [][]any{{int64(0), "constant projection", "estimated_rows=1"}},
			CommandTag: "EXPLAIN",
		}, nil
	}
	schema, err := transaction.schema(plan.Table)
	if err != nil {
		return Result{}, err
	}
	conditions, err := bindConditions(schema, plan.Conditions, parameters)
	if err != nil {
		return Result{}, err
	}
	predicate, err := bindPredicate(schema, plan.Predicate, parameters)
	if err != nil {
		return Result{}, err
	}
	var orders []boundOrder
	if !selectHasAggregates(plan) && !selectHasScalarExpressions(plan) {
		orders, err = bindOrders(schema, plan.Order)
		if err != nil {
			return Result{}, err
		}
	}
	generation, err := activeRowGeneration(transaction, schema)
	if err != nil {
		return Result{}, err
	}
	access, err := transaction.planRowAccess(schema, generation, conditions, orders)
	if err != nil {
		return Result{}, err
	}
	var indexOnlyGroup indexOnlyGroupAccess
	useIndexOnlyGroup := false
	var indexOnlyAggregate indexOnlyAggregateAccess
	useIndexOnlyAggregate := false
	if selectHasAggregates(plan) && len(plan.GroupBy) != 0 {
		if _, err := describeAggregateSelect(schema, plan); err != nil {
			return Result{}, err
		}
		groupFields, bindings, err := bindAggregateShape(schema, plan)
		if err != nil {
			return Result{}, err
		}
		indexOnlyGroup, useIndexOnlyGroup, err = transaction.planIndexOnlyGroup(
			schema, plan, groupFields, bindings,
		)
		if err != nil {
			return Result{}, err
		}
	}
	if selectHasAggregates(plan) && !useIndexOnlyGroup {
		if _, err := describeAggregateSelect(schema, plan); err != nil {
			return Result{}, err
		}
		groupFields, bindings, err := bindAggregateShape(schema, plan)
		if err != nil {
			return Result{}, err
		}
		indexOnlyAggregate, useIndexOnlyAggregate, err = transaction.planIndexOnlyAggregate(
			schema, plan, groupFields, bindings, conditions, predicate,
		)
		if err != nil {
			return Result{}, err
		}
	}
	operation := "sequential scan"
	switch access.kind {
	case rowAccessPrimary:
		operation = "primary lookup"
	case rowAccessUnique:
		operation = "unique lookup"
	case rowAccessSecondary:
		operation = "index scan"
	}
	accessName := access.name
	if useIndexOnlyGroup {
		operation = "index-only group"
		accessName = indexOnlyGroup.index.name
	} else if useIndexOnlyAggregate {
		operation = "index-only aggregate"
		accessName = indexOnlyAggregate.index.name
	}
	estimatedRows, estimatedRowsFound, err := readTableCount(transaction, schema)
	if err != nil {
		return Result{}, err
	}
	catalogCount := estimatedRowsFound && selectUsesCatalogCount(plan)
	if catalogCount {
		operation = "catalog count"
	}
	details := []string{"table=" + schema.Name}
	if accessName != "" && !catalogCount {
		details = append(details, "index="+accessName)
	}
	if estimatedRowsFound {
		details = append(details, fmt.Sprintf("estimated_rows=%d", estimatedRows))
	} else {
		details = append(details, "estimated_rows=unknown")
	}
	if len(conditions) != 0 {
		details = append(details, fmt.Sprintf("filters=%d", len(conditions)))
	}
	if !catalogCount && (access.kind == rowAccessSecondary || useIndexOnlyAggregate) {
		equalityPrefix, rangeField, reverse := access.equalityPrefix, access.rangeField, access.options.Reverse
		if useIndexOnlyAggregate {
			equalityPrefix = indexOnlyAggregate.equalityPrefix
			rangeField = indexOnlyAggregate.rangeField
			reverse = indexOnlyAggregate.options.Reverse
		}
		details = append(details, fmt.Sprintf("equality_prefix=%d", equalityPrefix))
		if rangeField != "" {
			details = append(details, "range="+rangeField)
		}
		direction := "forward"
		if reverse {
			direction = "reverse"
		}
		details = append(details, "direction="+direction)
	}
	if access.orderCovered {
		details = append(details, "order=index")
	} else if len(plan.Order) != 0 {
		details = append(details, "order=sort")
	}
	if selectHasAggregates(plan) {
		details = append(details, fmt.Sprintf("aggregate=true groups=%d", len(plan.GroupBy)))
	}
	if selectHasScalarExpressions(plan) {
		details = append(details, "expressions=true")
	}
	return Result{
		Columns:    explainColumns(),
		Rows:       [][]any{{int64(0), operation, strings.Join(details, " ")}},
		CommandTag: "EXPLAIN",
	}, nil
}

func (transaction *Transaction) executeExplainAnalyze(
	ctx context.Context,
	plan *kitdbsql.SelectStatement,
	parameters []any,
) (Result, error) {
	planningStarted := time.Now()
	explained, err := transaction.executeExplain(ctx, plan, parameters)
	planningElapsed := time.Since(planningStarted)
	if err != nil {
		return Result{}, err
	}
	executionStarted := time.Now()
	actual, err := transaction.executeReadSelectObserved(ctx, plan, parameters)
	executionElapsed := time.Since(executionStarted)
	if err != nil {
		return Result{}, err
	}
	return finishExplainAnalyze(explained, actual, planningElapsed, executionElapsed), nil
}

func finishExplainAnalyze(
	explained Result,
	actual Result,
	planningElapsed time.Duration,
	executionElapsed time.Duration,
) Result {
	rows := make([][]any, 0, len(explained.Rows)+8)
	rows = append(rows, explained.Rows...)
	nextID := int64(len(rows))
	path := explainPlanPath(explained)
	if actual.Execution != nil && actual.Execution.Path != "" {
		path = actual.Execution.Path
	}
	rows = append(rows, []any{
		nextID,
		"actual",
		fmt.Sprintf(
			"path=%s loops=1 result_rows=%d planning_ns=%s execution_ns=%s",
			path, len(actual.Rows), explainDurationNanos(planningElapsed), explainDurationNanos(executionElapsed),
		),
	})
	nextID++
	if stats := actual.Execution; stats != nil {
		if stats.ProjectionCacheHits != 0 || stats.ProjectionCacheMisses != 0 || stats.ProjectionCacheBypasses != 0 {
			rows = append(rows, []any{nextID, "projection_cache", fmt.Sprintf(
				"hits=%d misses=%d bypasses=%d",
				stats.ProjectionCacheHits, stats.ProjectionCacheMisses, stats.ProjectionCacheBypasses,
			)})
			nextID++
		}
		if stats.SearchReaderCacheHits != 0 || stats.SearchReaderCacheMisses != 0 || stats.SearchReaderCacheBypasses != 0 {
			rows = append(rows, []any{nextID, "search_reader_cache", fmt.Sprintf(
				"hits=%d misses=%d bypasses=%d",
				stats.SearchReaderCacheHits, stats.SearchReaderCacheMisses, stats.SearchReaderCacheBypasses,
			)})
			nextID++
		}
		if stats.RowsMaterialized != 0 || stats.MaterializationBytes != 0 || stats.MaterializationPeakBytes != 0 {
			rows = append(rows, []any{nextID, "materialization actual", fmt.Sprintf(
				"rows=%d direct_rows=%d retained_bytes=%d peak_bytes=%d",
				stats.RowsMaterialized, stats.MaterializationDirectRows,
				stats.MaterializationBytes, stats.MaterializationPeakBytes,
			)})
			nextID++
		}
		switch {
		case rowAccessStatsPath(stats.Path):
			rowDetail := fmt.Sprintf(
				"scanned=%d matched=%d", stats.RowsScanned, stats.RowsMatched,
			)
			if stats.Path == "index-only-group" || stats.Path == "index-only-aggregate" {
				rowDetail += fmt.Sprintf(" groups=%d", stats.Groups)
			}
			rows = append(rows,
				[]any{nextID, "index", fmt.Sprintf(
					"entries_scanned=%d point_lookups=%d",
					stats.IndexEntriesScanned, stats.PointLookups,
				)},
				[]any{nextID + 1, "rows", rowDetail},
				[]any{nextID + 2, "pages", fmt.Sprintf(
					"accessed=%d read=%d bytes=%d records_decoded=%d",
					stats.PageAccesses, stats.PagesRead, stats.PageBytesRead, stats.PageRecordsDecoded,
				)},
				[]any{nextID + 3, "cache", fmt.Sprintf(
					"hits=%d misses=%d bypasses=%d",
					stats.PageCacheHits, stats.PageCacheMisses, stats.PageCacheBypasses,
				)},
				[]any{nextID + 4, "physical", fmt.Sprintf(
					"generation_entries=%d overlay_entries=%d",
					stats.GenerationEntriesVisited, stats.OverlayEntriesVisited,
				)},
			)
			nextID += 5
		case stats.Path == "catalog-count":
			rows = append(rows, []any{nextID, "rows", fmt.Sprintf(
				"scanned=0 matched=%d metadata=%d", stats.RowsMatched, stats.RowsFromMetadata,
			)})
			nextID++
		case stats.Path == "materialized-scan" || stats.Path == "materialized-aggregate":
			rows = append(rows, []any{nextID, "rows", fmt.Sprintf(
				"scanned=%d matched=%d groups=%d", stats.RowsScanned, stats.RowsMatched, stats.Groups,
			)})
			nextID++
		case strings.HasSuffix(stats.Path, "-batch"):
			if stats.Path == "kcol-batch" {
				rows = append(rows,
					[]any{nextID, "chunks", fmt.Sprintf(
						"scanned=%d skipped=%d", stats.ChunksScanned, stats.ChunksSkipped,
					)},
					[]any{nextID + 1, "kcol_directory", fmt.Sprintf(
						"blocks_pruned=%d rows_pruned=%d", stats.ColumnarBlocksPruned, stats.ColumnarRowsPruned,
					)},
					[]any{nextID + 2, "kcol_io", fmt.Sprintf(
						"header_reads=%d header_bytes=%d payload_reads=%d payload_bytes=%d total_bytes=%d",
						stats.ColumnarBlockHeadersRead, stats.ColumnarHeaderBytesRead,
						stats.ColumnarPayloadsRead, stats.ColumnarPayloadBytesRead,
						stats.ColumnarHeaderBytesRead+stats.ColumnarPayloadBytesRead,
					)},
				)
				nextID += 3
			}
			rows = append(rows,
				[]any{nextID, "blocks", fmt.Sprintf(
					"scanned=%d skipped=%d metadata=%d",
					stats.Batches, stats.BatchesSkipped, stats.BatchesFromMetadata,
				)},
				[]any{nextID + 1, "rows", fmt.Sprintf(
					"scanned=%d skipped=%d metadata=%d groups=%d",
					stats.RowsScanned, stats.RowsSkipped, stats.RowsFromMetadata, stats.Groups,
				)},
			)
			nextID += 2
			if stats.Path == "krow-batch" {
				rows = append(rows,
					[]any{nextID, "pages", fmt.Sprintf(
						"accessed=%d read=%d bytes=%d records_decoded=%d",
						stats.PageAccesses, stats.PagesRead, stats.PageBytesRead, stats.PageRecordsDecoded,
					)},
					[]any{nextID + 1, "cache", fmt.Sprintf(
						"hits=%d misses=%d bypasses=%d",
						stats.PageCacheHits, stats.PageCacheMisses, stats.PageCacheBypasses,
					)},
					[]any{nextID + 2, "physical", fmt.Sprintf(
						"generation_entries=%d overlay_entries=%d",
						stats.GenerationEntriesVisited, stats.OverlayEntriesVisited,
					)},
				)
				nextID += 3
			}
		}
		if stats.Fallback != "" {
			rows = append(rows, []any{nextID, "fallback", stats.Fallback})
		}
	}
	explained.Rows = rows
	explained.Execution = actual.Execution
	explained.CommandTag = "EXPLAIN"
	return explained
}

func rowAccessStatsPath(path string) bool {
	switch path {
	case "sequential-scan", "primary-lookup", "unique-lookup", "index-scan", "index-only-group", "index-only-aggregate", "nested-loop-join":
		return true
	default:
		return false
	}
}

func explainDurationNanos(elapsed time.Duration) string {
	if elapsed <= 0 {
		return "below-clock-resolution"
	}
	return fmt.Sprintf("%d", elapsed.Nanoseconds())
}

func explainPlanPath(explained Result) string {
	if len(explained.Rows) == 0 || len(explained.Rows[0]) < 2 {
		return "unknown"
	}
	operation, ok := explained.Rows[0][1].(string)
	if !ok || operation == "" {
		return "unknown"
	}
	return strings.ReplaceAll(operation, " ", "-")
}
