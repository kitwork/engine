package relational

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/internal/snapshotfile"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	"github.com/kitwork/engine/search"
)

// SearchPackReport describes an offline adoption of deletion-free legacy
// search segments. CanonicalRowsScanned stays zero because packing verifies
// and copies an already-current immutable index instead of tokenizing KROW.
type SearchPackReport struct {
	Transaction          uint64
	SearchFile           string
	SearchTables         int
	SearchDocuments      uint64
	SearchSegments       int
	SearchSourceBytes    int64
	SearchFileBytes      int64
	CanonicalRowsScanned uint64
	Publication          string
}

type searchPackTable struct {
	schema     kitdbsql.Schema
	columns    []relationalSearchColumn
	generation uint64
	epoch      uint64
}

// PackSearchProjection adopts current legacy managed indexes into the packed
// read-only .search projection. It never scans KROW and never repairs, catches
// up, or silently rebuilds a legacy index. Every source watermark must match
// the fixed canonical transaction exactly before publication can succeed.
func (engine *Engine) PackSearchProjection(ctx context.Context) (SearchPackReport, error) {
	if engine == nil || ctx == nil {
		return SearchPackReport{}, fmt.Errorf("kitdb: nil projection engine/context")
	}
	if !engine.experimentalProjections {
		return SearchPackReport{}, fmt.Errorf("kitdb: experimental projections are disabled")
	}
	select {
	case engine.projectionBuilds <- struct{}{}:
	case <-ctx.Done():
		return SearchPackReport{}, ctx.Err()
	}
	defer func() { <-engine.projectionBuilds }()

	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		return SearchPackReport{}, err
	}
	defer transaction.Rollback()
	report := SearchPackReport{
		Transaction: transaction.base,
		SearchFile:  engine.database.Path() + ".search",
		Publication: "packed-legacy-index",
	}
	tables := make([]searchPackTable, 0)
	for _, catalogTable := range transaction.catalog.Structs {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		schema, err := kitdbsql.DecodeSchema(catalogTable.Definition)
		if err != nil {
			return report, err
		}
		columns := relationalSearchableColumns(schema)
		if len(columns) == 0 {
			continue
		}
		generation, epoch, err := activeRowLayout(transaction.snapshot, schema)
		if err != nil {
			return report, err
		}
		tables = append(tables, searchPackTable{
			schema: schema, columns: columns, generation: generation, epoch: epoch,
		})
	}
	if len(tables) == 0 {
		return report, fmt.Errorf("kitdb: database has no searchable tables")
	}
	if sameProjectionPath(engine.searchRoot, report.SearchFile) {
		return report, fmt.Errorf(
			"kitdb: legacy search root and packed projection share %q; reopen with a separate SearchRoot before packing",
			report.SearchFile,
		)
	}
	root, err := os.Stat(engine.searchRoot)
	if errors.Is(err, os.ErrNotExist) {
		return report, fmt.Errorf("kitdb: legacy search root %q does not exist", engine.searchRoot)
	}
	if err != nil {
		return report, fmt.Errorf("kitdb: inspect legacy search root: %w", err)
	}
	if !root.IsDir() {
		return report, fmt.Errorf("kitdb: legacy search root %q is not a directory", engine.searchRoot)
	}
	if target, err := os.Lstat(report.SearchFile); err == nil {
		if !target.Mode().IsRegular() {
			return report, fmt.Errorf(
				"kitdb: refusing to replace non-regular projection %q; use a separate search root",
				report.SearchFile,
			)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return report, err
	}

	manager, err := engine.searchManagerFor()
	if err != nil {
		return report, err
	}
	writer, err := snapshotfile.Create(report.SearchFile)
	if err != nil {
		return report, err
	}
	defer writer.Close()
	manifest := projectionManifest{
		Version: 1, Kind: "search", Cursor: transaction.snapshot.HistoryCursor(),
		Catalog: transaction.catalog.Revision, Tables: make(map[string]projectionTable),
	}
	for _, table := range tables {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		projection, _, err := newRelationalSearchSchema(table.columns)
		if err != nil {
			return report, err
		}
		indexKey := engine.relationalSearchIndexKey(table.schema, projection)
		state := engine.relationalSearchState(indexKey)
		state.projectionMu.RLock()
		err = engine.packLegacySearchTable(
			ctx, transaction, manager, writer, manifest, table, projection, indexKey, &report,
		)
		state.projectionMu.RUnlock()
		if err != nil {
			return report, err
		}
	}
	if err := engine.publishProjection(ctx, "search", writer, manifest); err != nil {
		return report, err
	}
	stat, err := os.Stat(report.SearchFile)
	if err != nil {
		return report, err
	}
	report.SearchFileBytes = stat.Size()
	report.SearchTables = len(manifest.Tables)
	return report, nil
}

func (engine *Engine) packLegacySearchTable(
	ctx context.Context,
	transaction *Transaction,
	manager *search.Manager,
	writer *snapshotfile.Writer,
	manifest projectionManifest,
	table searchPackTable,
	projection search.Schema,
	indexKey string,
	report *SearchPackReport,
) error {
	source := relationalSearchSource{
		cursor: transaction.snapshot.HistoryCursor(), generation: table.generation, epoch: table.epoch,
	}
	watermark, found, err := readRelationalSearchWatermark(ctx, manager, indexKey, projection)
	if err != nil {
		return fmt.Errorf("kitdb: read legacy search watermark for table %q: %w", table.schema.Name, err)
	}
	if !found || !relationalSearchWatermarkMatches(
		watermark, engine.database.ID(), table.schema, source,
	) || watermark.Cursor != source.cursor {
		return fmt.Errorf(
			"kitdb: legacy search projection for table %q is not at source transaction %d; run a legacy SEARCH catch-up before packing",
			table.schema.Name, source.cursor.Transaction,
		)
	}
	info, err := manager.Info(ctx, indexKey, projection)
	if err != nil {
		return fmt.Errorf("kitdb: inspect legacy search index for table %q: %w", table.schema.Name, err)
	}
	if info.Generation == 0 {
		return fmt.Errorf("kitdb: legacy search index for table %q has no committed generation", table.schema.Name)
	}
	if info.Deleted != 0 {
		return fmt.Errorf(
			"kitdb: legacy search index for table %q has %d tombstones; rebuild it before packing",
			table.schema.Name, info.Deleted,
		)
	}
	var packed search.IndexInfo
	if err := writer.Add(table.schema.ID, func(output io.Writer) error {
		var writeErr error
		packed, writeErr = manager.WriteSnapshot(ctx, indexKey, projection, output)
		return writeErr
	}); err != nil {
		return fmt.Errorf("kitdb: pack legacy search index for table %q: %w", table.schema.Name, err)
	}
	if packed.Generation != info.Generation || packed.Documents != info.Documents || packed.Bytes != info.Bytes {
		return fmt.Errorf("kitdb: legacy search generation changed while packing table %q", table.schema.Name)
	}
	if report.SearchDocuments > math.MaxUint64-packed.Documents ||
		report.SearchSegments > math.MaxInt-packed.Segments ||
		report.SearchSourceBytes > math.MaxInt64-packed.Bytes {
		return fmt.Errorf("kitdb: packed search report counters overflow")
	}
	report.SearchDocuments += packed.Documents
	report.SearchSegments += packed.Segments
	report.SearchSourceBytes += packed.Bytes
	manifest.Tables[table.schema.ID] = projectionTable{
		Hash: table.schema.Hash, Generation: table.generation, Epoch: table.epoch,
		Rows: packed.Documents,
	}
	return nil
}

func sameProjectionPath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	return left == right || strings.EqualFold(left, right)
}
