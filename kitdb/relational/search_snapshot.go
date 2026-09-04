package relational

import (
	"context"
	"fmt"

	"github.com/kitwork/engine/search"
)

// executePackedSearch is called with engine.mu held for lifetime safety. It
// deliberately does not run the mutable directory manager or rebuild on query.
func (engine *Engine) executePackedSearch(
	ctx context.Context,
	plan boundRelationalSearchPlan,
) ([][]any, *ExecutionStats, error) {
	stats := &ExecutionStats{Path: "search-snapshot"}
	select {
	case engine.projectionQueries <- struct{}{}:
	case <-ctx.Done():
		return nil, stats, ctx.Err()
	}
	defer func() { <-engine.projectionQueries }()
	engine.writeMu.Lock()
	catalog, err := engine.database.Catalog()
	if err != nil {
		engine.writeMu.Unlock()
		return nil, stats, err
	}
	snapshot, err := engine.database.Snapshot()
	engine.writeMu.Unlock()
	if err != nil {
		return nil, stats, err
	}
	defer snapshot.Close()
	current, err := schemaFromCatalog(catalog, plan.schema.Name)
	if err != nil {
		return nil, stats, err
	}
	if catalog.Transaction != snapshot.Transaction() || current.Hash != plan.schema.Hash {
		return nil, stats, fmt.Errorf("kitdb: SEARCH catalog changed; retry")
	}
	generation, epoch, err := activeRowLayout(snapshot, plan.schema)
	if err != nil {
		return nil, stats, err
	}
	engine.projectionMu.RLock()
	defer engine.projectionMu.RUnlock()
	lease, err := engine.acquireProjection(
		engine.database.Path()+".search", "search", snapshot.HistoryCursor(),
		catalog.Revision, plan.schema, generation, epoch,
	)
	if err != nil {
		stats.ProjectionCacheMisses++
		return nil, stats, fmt.Errorf("kitdb: search snapshot unavailable; call RefreshProjections: %w", err)
	}
	defer lease.close()
	stats.observeProjectionCache(lease.access)
	section, err := lease.file.Section(plan.schema.ID)
	if err != nil {
		return nil, stats, err
	}
	index, err := search.OpenSnapshot(section, plan.projection)
	if err != nil {
		return nil, stats, err
	}
	defer index.Close()
	if index.Info().Documents != lease.table.Rows {
		return nil, stats, fmt.Errorf("kitdb: search snapshot row count mismatch")
	}
	watermark := relationalSearchWatermark(engine.database.ID(), plan.schema, relationalSearchSource{cursor: snapshot.HistoryCursor(), generation: generation, epoch: epoch})
	after, err := decodeAndValidateRelationalSearchCursor(plan.afterText, watermark, index.Info().Generation, plan.projection.Fingerprint(), plan.queryFingerprint)
	if err != nil {
		return nil, stats, err
	}
	qualified, err := engine.collectRelationalSearchRows(ctx, index.Search, snapshot, plan, watermark, after)
	if err != nil {
		return nil, stats, err
	}
	start := min(plan.offset, len(qualified))
	end := min(start+plan.limit, len(qualified))
	rows := make([][]any, 0, end-start)
	for _, row := range qualified[start:end] {
		projected, err := projectRelationalSearchRow(plan, watermark, index.Info().Generation, row)
		if err != nil {
			return nil, stats, err
		}
		rows = append(rows, projected)
	}
	return rows, stats, nil
}
