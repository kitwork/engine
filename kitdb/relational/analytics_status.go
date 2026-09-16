package relational

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/kitwork/engine/internal/snapshotfile"
	"github.com/kitwork/engine/kitdb/columnar"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

// AnalyticsProjectionStatus reports whether one table can use its derived
// KCOL snapshot at an exact relational snapshot. It is observability, not a
// second freshness authority: execution repeats the same watermark checks.
type AnalyticsProjectionStatus struct {
	Table              string `json:"table"`
	Status             string `json:"status"`
	Fresh              bool   `json:"fresh"`
	Enabled            bool   `json:"enabled"`
	Supported          bool   `json:"supported"`
	QueryPath          string `json:"query_path"`
	ExpectedLayout     string `json:"expected_layout"`
	PartitionStrategy  string `json:"partition_strategy,omitempty"`
	PartitionField     string `json:"partition_field,omitempty"`
	SourceTransaction  uint64 `json:"source_transaction"`
	CurrentTransaction uint64 `json:"current_transaction"`
	Rows               uint64 `json:"rows"`
	Chunks             int    `json:"chunks"`
	ChunkVersion       int    `json:"chunk_version"`
	SnapshotGeneration uint64 `json:"snapshot_generation"`
	FileBytes          int64  `json:"file_bytes"`
	LiveBytes          int64  `json:"live_bytes"`
	ObsoleteBytes      int64  `json:"obsolete_bytes"`
	Reason             string `json:"reason,omitempty"`
}

var analyticsStatusColumns = []Column{
	{Name: "table", Kind: "text"},
	{Name: "status", Kind: "text"},
	{Name: "fresh", Kind: "bool"},
	{Name: "enabled", Kind: "bool"},
	{Name: "supported", Kind: "bool"},
	{Name: "query_path", Kind: "text"},
	{Name: "expected_layout", Kind: "text"},
	{Name: "partition_strategy", Kind: "text"},
	{Name: "partition_field", Kind: "text"},
	{Name: "source_transaction", Kind: "bigint"},
	{Name: "current_transaction", Kind: "bigint"},
	{Name: "rows", Kind: "bigint"},
	{Name: "chunks", Kind: "integer"},
	{Name: "chunk_version", Kind: "integer"},
	{Name: "generation", Kind: "bigint"},
	{Name: "file_bytes", Kind: "bigint"},
	{Name: "live_bytes", Kind: "bigint"},
	{Name: "obsolete_bytes", Kind: "bigint"},
	{Name: "reason", Kind: "text"},
}

// AnalyticsStatus inspects one table through a fresh fixed snapshot. It never
// refreshes, repairs, scans rows or mutates the analytics file.
func (engine *Engine) AnalyticsStatus(ctx context.Context, table string) (AnalyticsProjectionStatus, error) {
	if engine == nil || ctx == nil {
		return AnalyticsProjectionStatus{}, fmt.Errorf("kitdb: nil analytics engine/context")
	}
	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		return AnalyticsProjectionStatus{}, err
	}
	defer transaction.Rollback()
	return transaction.analyticsStatus(ctx, table)
}

func (transaction *Transaction) analyticsStatus(ctx context.Context, table string) (AnalyticsProjectionStatus, error) {
	if ctx == nil {
		return AnalyticsProjectionStatus{}, fmt.Errorf("kitdb: query context is nil")
	}
	if err := ctx.Err(); err != nil {
		return AnalyticsProjectionStatus{}, err
	}
	if err := transaction.ready(); err != nil {
		return AnalyticsProjectionStatus{}, err
	}
	table = unqualifiedColumn(table)
	schema, err := transaction.schema(table)
	if err != nil {
		return AnalyticsProjectionStatus{}, err
	}
	cursor := transaction.snapshot.HistoryCursor()
	status := AnalyticsProjectionStatus{
		Table: schema.Name, Status: "missing", Enabled: transaction.engine.experimentalProjections,
		Supported: len(columnarFields(schema)) != 0, QueryPath: "krow-batch",
		ExpectedLayout: expectedAnalyticsLayout(schema), CurrentTransaction: cursor.Transaction,
	}
	if schema.Partition != nil {
		status.PartitionStrategy = schema.Partition.Strategy
		for _, field := range schema.Fields {
			if field.Tag == schema.Partition.Field {
				status.PartitionField = field.Name
				break
			}
		}
		if status.PartitionField == "" {
			return AnalyticsProjectionStatus{}, fmt.Errorf("kitdb: analytics partition field is missing from schema")
		}
	}
	if !status.Enabled {
		status.Status = "disabled"
		status.Reason = "experimental analytics projections are disabled"
		return status, nil
	}
	if !status.Supported {
		status.Status = "unsupported"
		status.Reason = "table has no analytics-compatible fields"
		return status, nil
	}
	generation, epoch, err := activeRowLayout(transaction, schema)
	if err != nil {
		return AnalyticsProjectionStatus{}, err
	}

	transaction.engine.projectionMu.RLock()
	defer transaction.engine.projectionMu.RUnlock()
	if err := ctx.Err(); err != nil {
		return AnalyticsProjectionStatus{}, err
	}
	path := transaction.engine.database.Path() + ".analytics"
	if info, statErr := os.Stat(path); statErr == nil {
		status.FileBytes = info.Size()
	} else if !errors.Is(statErr, os.ErrNotExist) {
		status.Status = "invalid"
		status.Reason = "analytics file metadata is unreadable"
		return status, nil
	}
	file, err := snapshotfile.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		status.Reason = "analytics file does not exist; call RefreshAnalytics"
		return status, nil
	}
	if err != nil {
		status.Status = "invalid"
		status.Reason = "analytics file is unreadable"
		return status, nil
	}
	defer file.Close()
	info := file.GenerationInfo()
	status.SnapshotGeneration = info.Number
	if info.FileBytes != 0 {
		status.FileBytes = info.FileBytes
		status.LiveBytes = info.LiveBytes
		status.ObsoleteBytes = info.ObsoleteBytes
	} else {
		status.LiveBytes = status.FileBytes
	}

	var manifest projectionManifest
	if err := file.Metadata(&manifest); err != nil {
		status.Status = "invalid"
		status.Reason = "analytics manifest is unreadable"
		return status, nil
	}
	status.SourceTransaction = manifest.Cursor.Transaction
	if manifest.Version != 1 || manifest.Kind != "columnar" {
		status.Status = "invalid"
		status.Reason = "analytics manifest format is incompatible"
		return status, nil
	}
	if manifest.Cursor.DatabaseID != cursor.DatabaseID {
		status.Status = "invalid"
		status.Reason = "analytics source database identity does not match"
		return status, nil
	}
	if manifest.Cursor.Transaction == cursor.Transaction && manifest.Cursor.Checksum != cursor.Checksum {
		status.Status = "invalid"
		status.Reason = "analytics source checksum does not match"
		return status, nil
	}
	tableStatus, found := manifest.Tables[schema.ID]
	if !found {
		status.Reason = "table is absent from the analytics generation"
		return status, nil
	}
	status.Rows = tableStatus.Rows
	status.Chunks = len(tableStatus.Chunks)
	status.ChunkVersion = tableStatus.ChunkVersion
	if manifest.Cursor != cursor {
		status.Status = "stale"
		status.Reason = "analytics source watermark does not match the query snapshot"
		return status, nil
	}
	if manifest.Catalog != transaction.catalog.Revision || tableStatus.Hash != schema.Hash ||
		tableStatus.Generation != generation || tableStatus.Epoch != epoch ||
		tableStatus.ChunkVersion != expectedColumnarChunkVersion(schema) {
		status.Status = "stale"
		status.Reason = "analytics catalog or layout contract does not match"
		return status, nil
	}
	section, err := file.Section(schema.ID)
	if err != nil {
		status.Status = "invalid"
		status.Reason = "analytics table section is missing"
		return status, nil
	}
	reader, err := columnar.Open(section)
	if err != nil {
		status.Status = "invalid"
		status.Reason = "analytics table header is unreadable"
		return status, nil
	}
	prefix, err := rowPrefix(schema, generation)
	if err != nil {
		return AnalyticsProjectionStatus{}, err
	}
	if reader.FormatVersion() != columnarFormatVersion ||
		!validColumnarChunks(tableStatus, prefix, reader.DataOffset(), section.Size(), schema.Partition) {
		status.Status = "invalid"
		status.Reason = "analytics chunk manifest is invalid"
		return status, nil
	}
	status.Status = "ready"
	status.Fresh = true
	status.QueryPath = "kcol-batch"
	return status, nil
}

func expectedAnalyticsLayout(schema kitdbsql.Schema) string {
	if schema.Partition == nil {
		return "kcol-v3+chunk-v5"
	}
	switch schema.Partition.Strategy {
	case "range":
		return "kcol-v3+chunk-v6-range"
	case "hash":
		return "kcol-v3+chunk-v5-hash"
	default:
		return "kcol-unknown"
	}
}

func describePragma(plan *kitdbsql.PragmaStatement) ([]Column, error) {
	if plan == nil {
		return nil, fmt.Errorf("kitdb SQL: invalid PRAGMA plan")
	}
	switch plan.Name {
	case "analytics_status":
		return append([]Column(nil), analyticsStatusColumns...), nil
	case "cache_status":
		if plan.Argument != "" {
			return nil, fmt.Errorf("kitdb SQL: PRAGMA cache_status does not accept an argument")
		}
		return append([]Column(nil), queryCacheStatusColumns...), nil
	default:
		return nil, fmt.Errorf("kitdb SQL: unsupported PRAGMA %q", plan.Name)
	}
}

func (transaction *Transaction) executePragma(ctx context.Context, plan *kitdbsql.PragmaStatement) (Result, error) {
	if _, err := describePragma(plan); err != nil {
		return Result{}, err
	}
	if plan.Name == "cache_status" {
		return transaction.executeQueryCacheStatus(), nil
	}
	status, err := transaction.analyticsStatus(ctx, plan.Argument)
	if err != nil {
		return Result{}, err
	}
	sourceTransaction, err := analyticsStatusInteger(status.SourceTransaction)
	if err != nil {
		return Result{}, err
	}
	currentTransaction, err := analyticsStatusInteger(status.CurrentTransaction)
	if err != nil {
		return Result{}, err
	}
	rows, err := analyticsStatusInteger(status.Rows)
	if err != nil {
		return Result{}, err
	}
	generation, err := analyticsStatusInteger(status.SnapshotGeneration)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Columns: append([]Column(nil), analyticsStatusColumns...),
		Rows: [][]any{{
			status.Table, status.Status, status.Fresh, status.Enabled, status.Supported,
			status.QueryPath, status.ExpectedLayout, nullableAnalyticsText(status.PartitionStrategy),
			nullableAnalyticsText(status.PartitionField), sourceTransaction, currentTransaction,
			rows, int64(status.Chunks), int64(status.ChunkVersion), generation,
			status.FileBytes, status.LiveBytes, status.ObsoleteBytes, nullableAnalyticsText(status.Reason),
		}},
		CommandTag: "SELECT 1",
	}, nil
}

func analyticsStatusInteger(value uint64) (int64, error) {
	if value > math.MaxInt64 {
		return 0, fmt.Errorf("kitdb: analytics status integer exceeds BIGINT")
	}
	return int64(value), nil
}

func nullableAnalyticsText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
