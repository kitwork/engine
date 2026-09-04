package relational

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kitwork/engine/internal/snapshotfile"
	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/columnar"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	"github.com/kitwork/engine/search"
)

type ExecutionStats struct {
	Path                      string
	IndexEntriesScanned       uint64
	PointLookups              uint64
	RowsMatched               uint64
	PageAccesses              uint64
	PageCacheHits             uint64
	PageCacheMisses           uint64
	PageCacheBypasses         uint64
	PagesRead                 uint64
	PageBytesRead             uint64
	PageRecordsDecoded        uint64
	GenerationEntriesVisited  uint64
	OverlayEntriesVisited     uint64
	ChunksScanned             uint64
	ChunksSkipped             uint64
	RowsScanned               uint64
	Batches                   uint64
	RowsSkipped               uint64
	BatchesSkipped            uint64
	RowsFromMetadata          uint64
	BatchesFromMetadata       uint64
	RowsMaterialized          uint64
	MaterializationDirectRows uint64
	MaterializationBytes      uint64
	MaterializationPeakBytes  uint64
	ColumnarBlockHeadersRead  uint64
	ColumnarHeaderBytesRead   uint64
	ColumnarPayloadsRead      uint64
	ColumnarPayloadBytesRead  uint64
	ColumnarBlocksPruned      uint64
	ColumnarRowsPruned        uint64
	ProjectionCacheHits       uint64
	ProjectionCacheMisses     uint64
	ProjectionCacheBypasses   uint64
	SearchReaderCacheHits     uint64
	SearchReaderCacheMisses   uint64
	SearchReaderCacheBypasses uint64
	Groups                    uint64
	Fallback                  string
}

func (stats *ExecutionStats) addCursorStats(cursor kitdbengine.CursorStats) {
	if stats == nil {
		return
	}
	stats.PageAccesses += cursor.PageAccesses
	stats.PageCacheHits += cursor.PageCacheHits
	stats.PageCacheMisses += cursor.PageCacheMisses
	stats.PageCacheBypasses += cursor.PageCacheBypasses
	stats.PagesRead += cursor.PagesRead
	stats.PageBytesRead += cursor.PageBytesRead
	stats.PageRecordsDecoded += cursor.PageRecordsDecoded
	stats.GenerationEntriesVisited += cursor.GenerationEntriesVisited
	stats.OverlayEntriesVisited += cursor.OverlayEntriesVisited
}

type ProjectionReport struct {
	Transaction              uint64
	AnalyticsFile            string
	SearchFile               string
	AnalyticsTables          int
	SearchTables             int
	AnalyticsSourceRows      uint64
	AnalyticsReusedRows      uint64
	AnalyticsBuiltChunks     int
	AnalyticsReusedChunks    int
	AnalyticsCopiedBytes     int64
	AnalyticsReferencedBytes int64
	AnalyticsWrittenBytes    int64
	AnalyticsFileBytes       int64
	AnalyticsObsoleteBytes   int64
	AnalyticsPublication     string
	AnalyticsFallback        string
}

type projectionTable struct {
	Hash                   string
	Generation             uint64
	Epoch                  uint64
	Rows                   uint64
	ChunkVersion           int               `json:",omitempty"`
	Chunks                 []projectionChunk `json:",omitempty"`
	BlockDirectoryVersion  int               `json:",omitempty"`
	BlockDirectory         []byte            `json:",omitempty"`
	blockDirectoryBytes    int
	blockDirectoryDisabled bool
}

type projectionManifest struct {
	Version int
	Kind    string
	Cursor  kitdbengine.HistoryCursor
	Catalog string
	Tables  map[string]projectionTable
}

// RefreshProjections builds both experimental sidecars from one source snapshot.
// Each file is published independently. A crash can leave files at different
// transactions; readers check each watermark rather than assuming a shared age.
// It never replaces a legacy .search directory. No live data is migrated.
func (engine *Engine) RefreshProjections(ctx context.Context) (ProjectionReport, error) {
	return engine.refreshProjections(ctx, true)
}

// RefreshAnalytics rebuilds only the experimental analytics snapshot. It leaves
// search files and legacy search directories untouched. Proven unchanged chunks
// are referenced in place in the generational container. Legacy files and
// compaction use a checked replacement. Search storage is not modified.
// This method does not start a background scheduler.
func (engine *Engine) RefreshAnalytics(ctx context.Context) (ProjectionReport, error) {
	return engine.refreshProjections(ctx, false)
}

func (engine *Engine) refreshProjections(ctx context.Context, includeSearch bool) (ProjectionReport, error) {
	if engine == nil || ctx == nil {
		return ProjectionReport{}, fmt.Errorf("kitdb: nil projection engine/context")
	}
	if !engine.experimentalProjections {
		return ProjectionReport{}, fmt.Errorf("kitdb: experimental projections are disabled")
	}
	select {
	case engine.projectionBuilds <- struct{}{}:
	case <-ctx.Done():
		return ProjectionReport{}, ctx.Err()
	}
	defer func() { <-engine.projectionBuilds }()
	tx, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		return ProjectionReport{}, err
	}
	defer tx.Rollback()
	report := ProjectionReport{Transaction: tx.base, AnalyticsFile: engine.database.Path() + ".analytics"}
	targets := []struct {
		kind string
		path string
	}{{kind: "columnar", path: report.AnalyticsFile}}
	if includeSearch {
		report.SearchFile = engine.database.Path() + ".search"
		targets = append(targets, struct {
			kind string
			path string
		}{kind: "search", path: report.SearchFile})
	}
	for _, target := range targets {
		if stat, err := os.Lstat(target.path); err == nil {
			if !stat.Mode().IsRegular() {
				return report, fmt.Errorf("kitdb: refusing to replace non-regular projection %q; use a separate test database", target.path)
			}
		} else if !os.IsNotExist(err) {
			return report, err
		}
	}
	for _, target := range targets {
		if target.kind == "columnar" {
			if err := engine.refreshColumnarSnapshot(ctx, tx, &report); err != nil {
				return report, err
			}
			continue
		}
		writer, err := snapshotfile.Create(target.path)
		if err != nil {
			return report, err
		}
		err = func() error {
			defer writer.Close()
			manifest := projectionManifest{Version: 1, Kind: target.kind, Cursor: tx.snapshot.HistoryCursor(), Catalog: tx.catalog.Revision, Tables: make(map[string]projectionTable)}
			for _, catalogTable := range tx.catalog.Structs {
				if err := ctx.Err(); err != nil {
					return err
				}
				schema, err := kitdbsql.DecodeSchema(catalogTable.Definition)
				if err != nil {
					return err
				}
				columns := relationalSearchableColumns(schema)
				if len(columns) == 0 {
					continue
				}
				generation, epoch, err := activeRowLayout(tx.snapshot, schema)
				if err != nil {
					return err
				}
				var rows uint64
				err = writer.Add(schema.ID, func(output io.Writer) error {
					var err error
					rows, err = engine.writeSearchSnapshot(ctx, tx.snapshot, schema, generation, columns, output)
					return err
				})
				if err != nil {
					return err
				}
				manifest.Tables[schema.ID] = projectionTable{Hash: schema.Hash, Generation: generation, Epoch: epoch, Rows: rows}
			}
			err := engine.publishProjection(ctx, target.kind, writer, manifest)
			if err != nil {
				return err
			}
			report.SearchTables = len(manifest.Tables)
			return nil
		}()
		if err != nil {
			return report, err
		}
	}
	return report, nil
}

func columnarField(field kitdbsql.Field) (columnar.Field, bool) {
	kind := columnar.Kind(0)
	typeInfo, found := kitdbsql.LookupKind(field.Kind)
	if !found {
		return columnar.Field{}, false
	}
	switch typeInfo.Family {
	case kitdbsql.FamilyInteger, kitdbsql.FamilySystem:
		kind = columnar.Integer
	case kitdbsql.FamilyFloat:
		kind = columnar.Float
	case kitdbsql.FamilyBoolean:
		kind = columnar.Boolean
	case kitdbsql.FamilyText, kitdbsql.FamilyIdentifier, kitdbsql.FamilyChoice:
		if field.Analytics {
			kind = columnar.Text
		}
	}
	return columnar.Field{Tag: field.Tag, Kind: kind}, kind != 0
}

func columnarFields(schema kitdbsql.Schema) []columnar.Field {
	var fields []columnar.Field
	for _, field := range schema.Fields {
		if projected, ok := columnarField(field); ok {
			fields = append(fields, projected)
		}
	}
	return fields
}

func (engine *Engine) writeSearchSnapshot(ctx context.Context, snapshot *kitdbengine.Snapshot, schema kitdbsql.Schema, generation uint64, columns []relationalSearchColumn, output io.Writer) (rows uint64, returnErr error) {
	projection, _, err := newRelationalSearchSchema(columns)
	if err != nil {
		return 0, err
	}
	directory, err := os.MkdirTemp(filepath.Dir(engine.database.Path()), ".search-build-*")
	if err != nil {
		return 0, err
	}
	defer func() { returnErr = errors.Join(returnErr, os.RemoveAll(directory)) }()
	writer, err := search.NewIndexWriter(directory, projection, search.WriterOptions{Segment: search.BuildOptions{FlushThresholdBytes: 8 << 20, MaxDocuments: 25000}})
	if err != nil {
		return 0, err
	}
	defer func() { returnErr = errors.Join(returnErr, writer.Close()) }()
	replacement, err := writer.BeginReplacement()
	if err != nil {
		return 0, err
	}
	defer replacement.Abort()
	err = scanRelationalSearchSnapshot(ctx, snapshot, schema, generation, relationalSearchProjectionTags(columns), func(key []byte, values map[string]any) error {
		document, err := relationalSearchDocument(schema, key, values, columns)
		if err != nil {
			return err
		}
		if err := replacement.Add(ctx, document); err != nil {
			return err
		}
		rows++
		return nil
	})
	if err != nil {
		return 0, err
	}
	if _, err := replacement.Commit(ctx); err != nil {
		return 0, err
	}
	index, err := search.OpenIndex(directory, projection)
	if err != nil {
		return 0, err
	}
	defer func() { returnErr = errors.Join(returnErr, index.Close()) }()
	return rows, index.WriteSnapshot(ctx, output)
}
