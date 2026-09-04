package relational

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/kitwork/engine/internal/snapshotfile"
	"github.com/kitwork/engine/search"
)

// ProjectionOpenPolicy controls whether relational.Open accepts derived
// projection state. Canonical KROW recovery remains owned by the kernel.
type ProjectionOpenPolicy string

const (
	// ProjectionOpenDefault preserves the caller's default policy. Direct
	// Engines normalize it to lazy; a multi-database node may choose a safer
	// default for explicitly warm databases.
	ProjectionOpenDefault ProjectionOpenPolicy = ""
	// ProjectionOpenLazy defers every projection check to the first query.
	ProjectionOpenLazy ProjectionOpenPolicy = "lazy"
	// ProjectionOpenValidate rejects an existing structurally invalid sidecar.
	// Missing and stale projections remain safe because execution can fall back.
	ProjectionOpenValidate ProjectionOpenPolicy = "validate"
	// ProjectionOpenRequireReady requires every supported analytics/search
	// projection to be present, exact-watermark compatible and query-ready.
	ProjectionOpenRequireReady ProjectionOpenPolicy = "require-ready"
)

// ParseProjectionOpenPolicy parses the stable CLI/config spelling.
func ParseProjectionOpenPolicy(source string) (ProjectionOpenPolicy, error) {
	policy := ProjectionOpenPolicy(strings.ToLower(strings.TrimSpace(source)))
	switch policy {
	case ProjectionOpenDefault, ProjectionOpenLazy, ProjectionOpenValidate, ProjectionOpenRequireReady:
		return policy, nil
	default:
		return ProjectionOpenDefault, fmt.Errorf(
			"kitdb: projection open policy must be lazy, validate, or require-ready",
		)
	}
}

func normalizeProjectionOpenPolicy(policy ProjectionOpenPolicy) (ProjectionOpenPolicy, error) {
	parsed, err := ParseProjectionOpenPolicy(string(policy))
	if err != nil {
		return ProjectionOpenDefault, err
	}
	if parsed == ProjectionOpenDefault {
		return ProjectionOpenLazy, nil
	}
	return parsed, nil
}

func stricterProjectionOpenPolicy(left, right ProjectionOpenPolicy) ProjectionOpenPolicy {
	left, _ = normalizeProjectionOpenPolicy(left)
	right, _ = normalizeProjectionOpenPolicy(right)
	rank := func(policy ProjectionOpenPolicy) int {
		switch policy {
		case ProjectionOpenRequireReady:
			return 2
		case ProjectionOpenValidate:
			return 1
		default:
			return 0
		}
	}
	if rank(right) > rank(left) {
		return right
	}
	return left
}

// SearchProjectionStatus reports whether one table can use its immutable
// packed search snapshot. Legacy mutable search directories are deliberately
// outside this exact-watermark projection contract.
type SearchProjectionStatus struct {
	Table               string `json:"table"`
	Status              string `json:"status"`
	Fresh               bool   `json:"fresh"`
	Enabled             bool   `json:"enabled"`
	Supported           bool   `json:"supported"`
	QueryPath           string `json:"query_path"`
	ExpectedLayout      string `json:"expected_layout"`
	SourceTransaction   uint64 `json:"source_transaction"`
	CurrentTransaction  uint64 `json:"current_transaction"`
	Documents           uint64 `json:"documents"`
	Segments            int    `json:"segments"`
	IndexGeneration     uint64 `json:"index_generation"`
	ReaderCapacityBytes int64  `json:"reader_capacity_bytes"`
	SnapshotGeneration  uint64 `json:"snapshot_generation"`
	FileBytes           int64  `json:"file_bytes"`
	LiveBytes           int64  `json:"live_bytes"`
	ObsoleteBytes       int64  `json:"obsolete_bytes"`
	Reason              string `json:"reason,omitempty"`
}

// ProjectionPreflightReport is one fixed-snapshot view of every supported
// derived representation. Valid means no representation is structurally
// invalid. Ready additionally requires every representation to be usable now.
type ProjectionPreflightReport struct {
	DatabaseID      string                      `json:"database_id"`
	Transaction     uint64                      `json:"transaction"`
	CatalogRevision string                      `json:"catalog_revision"`
	Valid           bool                        `json:"valid"`
	Ready           bool                        `json:"ready"`
	Analytics       []AnalyticsProjectionStatus `json:"analytics"`
	Search          []SearchProjectionStatus    `json:"search"`
}

// ProjectionOpenError identifies a path-free projection policy failure.
type ProjectionOpenError struct {
	Policy ProjectionOpenPolicy `json:"policy"`
	Kind   string               `json:"kind"`
	Table  string               `json:"table"`
	Status string               `json:"status"`
	Reason string               `json:"reason,omitempty"`
}

func (problem *ProjectionOpenError) Error() string {
	if problem == nil {
		return "kitdb: projection open policy failed"
	}
	detail := ""
	if problem.Reason != "" {
		detail = ": " + problem.Reason
	}
	return fmt.Sprintf(
		"kitdb: projection open policy %s rejected %s for table %q with status %s%s",
		problem.Policy, problem.Kind, problem.Table, problem.Status, detail,
	)
}

// PreflightProjections checks bounded projection metadata at one canonical
// transaction. It does not refresh, repair or scan KROW records.
func (engine *Engine) PreflightProjections(ctx context.Context) (ProjectionPreflightReport, error) {
	if engine == nil || ctx == nil {
		return ProjectionPreflightReport{}, fmt.Errorf("kitdb: nil projection engine/context")
	}
	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		return ProjectionPreflightReport{}, err
	}
	defer transaction.Rollback()
	return transaction.preflightProjections(ctx)
}

func (transaction *Transaction) preflightProjections(ctx context.Context) (ProjectionPreflightReport, error) {
	if err := transaction.ready(); err != nil {
		return ProjectionPreflightReport{}, err
	}
	if err := ctx.Err(); err != nil {
		return ProjectionPreflightReport{}, err
	}
	report := ProjectionPreflightReport{
		DatabaseID:  transaction.snapshot.HistoryCursor().DatabaseID,
		Transaction: transaction.base, CatalogRevision: transaction.catalog.Revision,
		Valid: true, Ready: true,
		Analytics: make([]AnalyticsProjectionStatus, 0),
		Search:    make([]SearchProjectionStatus, 0),
	}
	for _, table := range transaction.catalog.Structs {
		if err := ctx.Err(); err != nil {
			return ProjectionPreflightReport{}, err
		}
		schema, err := decodeCatalogSchema(table.Definition)
		if err != nil {
			return ProjectionPreflightReport{}, err
		}
		if len(columnarFields(schema)) != 0 {
			status, err := transaction.analyticsStatus(ctx, schema.Name)
			if err != nil {
				return ProjectionPreflightReport{}, err
			}
			report.Analytics = append(report.Analytics, status)
			observeProjectionPreflightStatus(&report, status.Status)
		}
		if len(relationalSearchableColumns(schema)) != 0 {
			status, err := transaction.searchProjectionStatus(ctx, schema.Name)
			if err != nil {
				return ProjectionPreflightReport{}, err
			}
			report.Search = append(report.Search, status)
			observeProjectionPreflightStatus(&report, status.Status)
		}
	}
	sort.Slice(report.Analytics, func(left, right int) bool {
		return report.Analytics[left].Table < report.Analytics[right].Table
	})
	sort.Slice(report.Search, func(left, right int) bool {
		return report.Search[left].Table < report.Search[right].Table
	})
	return report, nil
}

func observeProjectionPreflightStatus(report *ProjectionPreflightReport, status string) {
	if status != "ready" {
		report.Ready = false
	}
	if status == "invalid" {
		report.Valid = false
	}
}

func (engine *Engine) enforceProjectionOpenPolicy(ctx context.Context, requested ProjectionOpenPolicy) error {
	policy, err := normalizeProjectionOpenPolicy(requested)
	if err != nil {
		return err
	}
	if policy == ProjectionOpenLazy {
		return nil
	}
	report, err := engine.PreflightProjections(ctx)
	if err != nil {
		return err
	}
	for _, status := range report.Analytics {
		if projectionStatusRejected(policy, status.Status) {
			return &ProjectionOpenError{
				Policy: policy, Kind: "analytics", Table: status.Table,
				Status: status.Status, Reason: status.Reason,
			}
		}
	}
	for _, status := range report.Search {
		if projectionStatusRejected(policy, status.Status) {
			return &ProjectionOpenError{
				Policy: policy, Kind: "search", Table: status.Table,
				Status: status.Status, Reason: status.Reason,
			}
		}
	}
	return nil
}

func projectionStatusRejected(policy ProjectionOpenPolicy, status string) bool {
	if policy == ProjectionOpenRequireReady {
		return status != "ready"
	}
	return policy == ProjectionOpenValidate && status == "invalid"
}

func (transaction *Transaction) searchProjectionStatus(
	ctx context.Context,
	table string,
) (SearchProjectionStatus, error) {
	if ctx == nil {
		return SearchProjectionStatus{}, fmt.Errorf("kitdb: query context is nil")
	}
	if err := ctx.Err(); err != nil {
		return SearchProjectionStatus{}, err
	}
	if err := transaction.ready(); err != nil {
		return SearchProjectionStatus{}, err
	}
	table = unqualifiedColumn(table)
	schema, err := transaction.schema(table)
	if err != nil {
		return SearchProjectionStatus{}, err
	}
	cursor := transaction.snapshot.HistoryCursor()
	columns := relationalSearchableColumns(schema)
	status := SearchProjectionStatus{
		Table: schema.Name, Status: "missing", Enabled: transaction.engine.experimentalProjections,
		Supported: len(columns) != 0, QueryPath: "unavailable",
		ExpectedLayout: "ksrch-v1", CurrentTransaction: cursor.Transaction,
	}
	if !status.Enabled {
		status.Status = "disabled"
		status.Reason = "experimental search projections are disabled"
		return status, nil
	}
	if !status.Supported {
		status.Status = "unsupported"
		status.Reason = "table has no searchable fields"
		return status, nil
	}
	generation, epoch, err := activeRowLayout(transaction, schema)
	if err != nil {
		return SearchProjectionStatus{}, err
	}

	transaction.engine.projectionMu.RLock()
	defer transaction.engine.projectionMu.RUnlock()
	path := transaction.engine.database.Path() + ".search"
	if info, statErr := os.Stat(path); statErr == nil {
		status.FileBytes = info.Size()
	} else if !errors.Is(statErr, os.ErrNotExist) {
		status.Status = "invalid"
		status.Reason = "search file metadata is unreadable"
		return status, nil
	}
	file, err := snapshotfile.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		status.Reason = "search file does not exist; call RefreshProjections"
		return status, nil
	}
	if err != nil {
		status.Status = "invalid"
		status.Reason = "search file is unreadable"
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
		status.Reason = "search manifest is unreadable"
		return status, nil
	}
	status.SourceTransaction = manifest.Cursor.Transaction
	if manifest.Version != 1 || manifest.Kind != "search" {
		status.Status = "invalid"
		status.Reason = "search manifest format is incompatible"
		return status, nil
	}
	if manifest.Cursor.DatabaseID != cursor.DatabaseID {
		status.Status = "invalid"
		status.Reason = "search source database identity does not match"
		return status, nil
	}
	if manifest.Cursor.Transaction == cursor.Transaction && manifest.Cursor.Checksum != cursor.Checksum {
		status.Status = "invalid"
		status.Reason = "search source checksum does not match"
		return status, nil
	}
	tableStatus, found := manifest.Tables[schema.ID]
	if !found {
		status.Reason = "table is absent from the search generation"
		return status, nil
	}
	status.Documents = tableStatus.Rows
	if manifest.Cursor != cursor {
		status.Status = "stale"
		status.Reason = "search source watermark does not match the query snapshot"
		return status, nil
	}
	if manifest.Catalog != transaction.catalog.Revision || tableStatus.Hash != schema.Hash ||
		tableStatus.Generation != generation || tableStatus.Epoch != epoch {
		status.Status = "stale"
		status.Reason = "search catalog or layout contract does not match"
		return status, nil
	}
	section, err := file.Section(schema.ID)
	if err != nil {
		status.Status = "invalid"
		status.Reason = "search table section is missing"
		return status, nil
	}
	projection, _, err := newRelationalSearchSchema(columns)
	if err != nil {
		return SearchProjectionStatus{}, err
	}
	indexInfo, err := search.InspectSnapshot(ctx, section, projection)
	if err != nil {
		status.Status = "invalid"
		status.Reason = "search snapshot headers are unreadable"
		return status, nil
	}
	status.Documents = indexInfo.Documents
	status.Segments = indexInfo.Segments
	status.IndexGeneration = indexInfo.Generation
	status.ReaderCapacityBytes = indexInfo.ReaderCapacityBytes
	if indexInfo.Documents != tableStatus.Rows {
		status.Status = "invalid"
		status.Reason = "search snapshot row count does not match"
		return status, nil
	}
	status.Status = "ready"
	status.Fresh = true
	status.QueryPath = "search-snapshot"
	return status, nil
}
