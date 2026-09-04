package pgimport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lib/pq"
)

const (
	DefaultChunkRows   = 1000
	DefaultChunkBytes  = 8 << 20
	MaxChunkRows       = 10_000
	MaxChunkBytes      = 32 << 20
	importRetryLimit   = 5
	importRestartLimit = 8
)

// Config describes one stable source-to-struct import. Reuse ImportID to
// resume; use a fresh ID only when intentionally starting a new import.
type Config struct {
	URL        string
	Database   *sql.DB
	File       string
	Format     string
	Table      string
	Columns    []string
	ImportID   string
	SourceID   string
	Header     bool
	Delimiter  rune
	Null       string
	NullSet    bool
	ChunkRows  int
	ChunkBytes int64
	MaxChunks  int
	Progress   func(Progress)
}

type Status struct {
	ImportID     string   `json:"import_id"`
	Source       string   `json:"source"`
	SourceFormat string   `json:"source_format"`
	Table        string   `json:"table"`
	Columns      []string `json:"columns"`
	SeedChecksum string   `json:"seed_checksum"`
	Checksum     string   `json:"checksum"`
	Chunk        uint64   `json:"chunk"`
	Rows         uint64   `json:"rows"`
	Offset       uint64   `json:"offset"`
	Complete     bool     `json:"complete"`
	Cancelled    bool     `json:"cancelled"`
	Transaction  uint64   `json:"transaction"`
}

type Progress struct {
	Status
	ChunkRows uint64 `json:"chunk_rows"`
}

type Report struct {
	Status
	Resumed         bool   `json:"resumed"`
	CommittedChunks uint64 `json:"committed_chunks"`
}

type Verification struct {
	Status
	Verified bool `json:"verified"`
}

type normalizedConfig struct {
	Config
	filePath   string
	format     string
	chunkRows  int
	chunkBytes uint64
}

type importChunk struct {
	number   uint64
	start    uint64
	end      uint64
	previous [sha256.Size]byte
	checksum [sha256.Size]byte
	records  []sourceRecord
	complete bool
}

type importAdvancedError struct {
	status Status
}

func (err *importAdvancedError) Error() string {
	return fmt.Sprintf("pgimport: import advanced to chunk %d in another process", err.status.Chunk)
}

// Run imports at most one bounded chunk at a time. Every chunk and its KIMP
// watermark commit atomically, so replay after a lost connection is safe.
func Run(ctx context.Context, config Config) (Report, error) {
	return run(ctx, config, 0)
}

// Verify proves that the local source prefix still matches the durable KIMP
// watermark without writing rows or advancing the import.
func Verify(ctx context.Context, config Config) (Verification, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, err := normalizeConfig(config)
	if err != nil {
		return Verification{}, err
	}
	source, err := openImportSource(normalized)
	if err != nil {
		return Verification{}, err
	}
	defer source.Close()
	normalized.Columns = source.Columns()
	if normalized.SourceID == "" {
		normalized.SourceID = stableSourceID(normalized.filePath)
	}
	if normalized.ImportID == "" {
		normalized.ImportID = stableImportID(normalized.SourceID, normalized.Table)
	}
	if err := validateIdentity("import id", normalized.ImportID, 256); err != nil {
		return Verification{}, err
	}
	if err := validateIdentity("source id", normalized.SourceID, 1024); err != nil {
		return Verification{}, err
	}
	database, owned, err := openDatabase(normalized)
	if err != nil {
		return Verification{}, err
	}
	if owned {
		defer database.Close()
	}
	status, found, err := QueryStatus(ctx, database, normalized.ImportID)
	if err != nil {
		return Verification{}, err
	}
	if !found {
		return Verification{}, fmt.Errorf("pgimport: import %q does not exist", normalized.ImportID)
	}
	expectedSeed := importSeed(
		normalized.ImportID, normalized.SourceID, normalized.format, normalized.Table, normalized.Columns,
	)
	if err := validateResumeStatus(normalized, status, expectedSeed); err != nil {
		return Verification{}, err
	}
	if _, _, err := verifyCommittedPrefix(source, status); err != nil {
		return Verification{}, err
	}
	return Verification{Status: status, Verified: true}, nil
}

func run(ctx context.Context, config Config, restarts int) (Report, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, err := normalizeConfig(config)
	if err != nil {
		return Report{}, err
	}
	source, err := openImportSource(normalized)
	if err != nil {
		return Report{}, err
	}
	defer source.Close()
	normalized.Columns = source.Columns()
	if normalized.SourceID == "" {
		normalized.SourceID = stableSourceID(normalized.filePath)
	}
	if normalized.ImportID == "" {
		normalized.ImportID = stableImportID(normalized.SourceID, normalized.Table)
	}
	if err := validateIdentity("import id", normalized.ImportID, 256); err != nil {
		return Report{}, err
	}
	if err := validateIdentity("source id", normalized.SourceID, 1024); err != nil {
		return Report{}, err
	}

	database, owned, err := openDatabase(normalized)
	if err != nil {
		return Report{}, err
	}
	if owned {
		defer database.Close()
	}

	expectedSeed := importSeed(
		normalized.ImportID, normalized.SourceID, normalized.format, normalized.Table, normalized.Columns,
	)
	status, found, err := QueryStatus(ctx, database, normalized.ImportID)
	if err != nil {
		return Report{}, err
	}
	report := Report{Resumed: found}
	checksum := expectedSeed
	committedOffset := source.Offset()
	var committedRows uint64
	var committedChunk uint64
	if found {
		if err := validateResumeStatus(normalized, status, expectedSeed); err != nil {
			return Report{}, err
		}
		if status.Cancelled {
			return Report{}, fmt.Errorf("pgimport: import %q is cancelled and cannot be resumed", normalized.ImportID)
		}
		checksum, committedOffset, err = verifyCommittedPrefix(source, status)
		if err != nil {
			return Report{}, err
		}
		committedRows, committedChunk = status.Rows, status.Chunk
		report.Status = status
		if status.Complete {
			return report, nil
		}
	}

	var pending *sourceRecord
	for normalized.MaxChunks == 0 || int(report.CommittedChunks) < normalized.MaxChunks {
		chunk, nextPending, err := buildImportChunk(
			source, pending, committedChunk+1, committedOffset, checksum,
			normalized.chunkRows, normalized.chunkBytes,
		)
		if err != nil {
			return report, err
		}
		pending = nextPending
		expected := Status{
			ImportID: normalized.ImportID, Source: normalized.SourceID, SourceFormat: normalized.format,
			Table: normalized.Table, Columns: append([]string(nil), normalized.Columns...),
			SeedChecksum: hex.EncodeToString(expectedSeed[:]), Checksum: hex.EncodeToString(chunk.checksum[:]),
			Chunk: chunk.number, Rows: committedRows + uint64(len(chunk.records)), Offset: chunk.end,
			Complete: chunk.complete,
		}
		previous := Status{
			ImportID: normalized.ImportID, Chunk: committedChunk, Rows: committedRows,
			Offset: committedOffset, Checksum: hex.EncodeToString(checksum[:]),
		}
		committed, err := commitImportChunk(ctx, database, normalized, chunk, expected, previous, found || committedChunk != 0)
		if err != nil {
			var advanced *importAdvancedError
			if errors.As(err, &advanced) && restarts < importRestartLimit {
				_ = source.Close()
				if owned {
					_ = database.Close()
				}
				next, retryErr := run(ctx, config, restarts+1)
				next.Resumed = true
				next.CommittedChunks += report.CommittedChunks
				return next, retryErr
			}
			return report, err
		}
		found = true
		report.Status = committed
		report.CommittedChunks++
		committedRows, committedChunk, committedOffset = committed.Rows, committed.Chunk, committed.Offset
		checksum = chunk.checksum
		if normalized.Progress != nil {
			normalized.Progress(Progress{Status: committed, ChunkRows: uint64(len(chunk.records))})
		}
		if committed.Complete {
			return report, nil
		}
	}
	return report, nil
}

func normalizeConfig(config Config) (normalizedConfig, error) {
	normalized := normalizedConfig{Config: config}
	if strings.TrimSpace(config.File) == "" || config.File == "-" {
		return normalized, fmt.Errorf("pgimport: a seekable source file is required")
	}
	absolute, err := filepath.Abs(config.File)
	if err != nil {
		return normalized, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return normalized, err
	}
	if !info.Mode().IsRegular() {
		return normalized, fmt.Errorf("pgimport: source must be a regular file")
	}
	normalized.filePath = filepath.Clean(absolute)
	normalized.format = strings.ToLower(strings.TrimSpace(config.Format))
	if normalized.format == "" {
		switch strings.ToLower(filepath.Ext(absolute)) {
		case ".csv":
			normalized.format = "csv"
		case ".jsonl", ".ndjson":
			normalized.format = "jsonl"
		}
	}
	if normalized.format != "csv" && normalized.format != "jsonl" {
		return normalized, fmt.Errorf("pgimport: format must be csv or jsonl")
	}
	if strings.TrimSpace(config.Table) == "" {
		return normalized, fmt.Errorf("pgimport: table is required")
	}
	normalized.Table = strings.TrimSpace(config.Table)
	normalized.Columns = append([]string(nil), config.Columns...)
	if normalized.format == "jsonl" && config.Header {
		return normalized, fmt.Errorf("pgimport: JSONL does not have a CSV header")
	}
	normalized.chunkRows = config.ChunkRows
	if normalized.chunkRows == 0 {
		normalized.chunkRows = DefaultChunkRows
	}
	if normalized.chunkRows < 1 || normalized.chunkRows > MaxChunkRows {
		return normalized, fmt.Errorf("pgimport: chunk rows must be between 1 and %d", MaxChunkRows)
	}
	chunkBytes := config.ChunkBytes
	if chunkBytes == 0 {
		chunkBytes = DefaultChunkBytes
	}
	if chunkBytes < 1 || chunkBytes > MaxChunkBytes {
		return normalized, fmt.Errorf("pgimport: chunk bytes must be between 1 and %d", MaxChunkBytes)
	}
	normalized.chunkBytes = uint64(chunkBytes)
	if config.MaxChunks < 0 {
		return normalized, fmt.Errorf("pgimport: max chunks cannot be negative")
	}
	return normalized, nil
}

func openImportSource(config normalizedConfig) (importSource, error) {
	if config.format == "csv" {
		return openCSVSource(
			config.filePath, config.Columns, config.Header, config.Delimiter, config.Null, config.NullSet,
		)
	}
	return openJSONLSource(config.filePath, config.Columns)
}

func openDatabase(config normalizedConfig) (*sql.DB, bool, error) {
	if config.Database != nil {
		return config.Database, false, nil
	}
	if strings.TrimSpace(config.URL) == "" {
		return nil, false, fmt.Errorf("pgimport: PostgreSQL URL is required")
	}
	database, err := sql.Open("postgres", config.URL)
	if err != nil {
		return nil, false, err
	}
	database.SetMaxOpenConns(2)
	database.SetMaxIdleConns(1)
	return database, true, nil
}

func validateResumeStatus(config normalizedConfig, status Status, seed [sha256.Size]byte) error {
	if status.ImportID != config.ImportID || status.Source != config.SourceID ||
		status.SourceFormat != config.format || !strings.EqualFold(status.Table, config.Table) ||
		!equalColumnNames(status.Columns, config.Columns) {
		return fmt.Errorf("pgimport: import %q is bound to a different source, table, or column order", config.ImportID)
	}
	if status.SeedChecksum != hex.EncodeToString(seed[:]) {
		return fmt.Errorf("pgimport: import %q seed descriptor changed", config.ImportID)
	}
	return nil
}

func verifyCommittedPrefix(
	source importSource,
	status Status,
) ([sha256.Size]byte, uint64, error) {
	checksum, err := parseImportChecksum(status.SeedChecksum)
	if err != nil {
		return checksum, 0, err
	}
	committedOffset := source.Offset()
	for row := uint64(0); row < status.Rows; row++ {
		record, readErr := source.Next()
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return checksum, committedOffset, fmt.Errorf(
					"pgimport: source ended after %d rows; committed prefix requires %d", row, status.Rows,
				)
			}
			return checksum, committedOffset, readErr
		}
		checksum = advanceImportChecksum(checksum, record.fields)
		committedOffset = record.end
	}
	if status.Complete {
		if _, readErr := source.Next(); !errors.Is(readErr, io.EOF) {
			if readErr == nil {
				return checksum, committedOffset, fmt.Errorf("pgimport: completed source now contains additional records")
			}
			return checksum, committedOffset, readErr
		}
		committedOffset = source.Offset()
	}
	if hex.EncodeToString(checksum[:]) != status.Checksum || committedOffset != status.Offset {
		return checksum, committedOffset, fmt.Errorf(
			"pgimport: source prefix changed before row %d or byte %d; refusing unsafe resume",
			status.Rows, status.Offset,
		)
	}
	return checksum, committedOffset, nil
}

func buildImportChunk(
	source importSource,
	pending *sourceRecord,
	number, start uint64,
	previous [sha256.Size]byte,
	maxRows int,
	maxBytes uint64,
) (importChunk, *sourceRecord, error) {
	chunk := importChunk{number: number, start: start, end: start, previous: previous, checksum: previous}
	next := func() (sourceRecord, error) {
		if pending != nil {
			record := *pending
			pending = nil
			return record, nil
		}
		return source.Next()
	}
	for len(chunk.records) < maxRows {
		record, err := next()
		if errors.Is(err, io.EOF) {
			chunk.complete = true
			chunk.end = source.Offset()
			break
		}
		if err != nil {
			return importChunk{}, nil, err
		}
		if record.end < start || record.end-start > maxBytes {
			if len(chunk.records) == 0 {
				return importChunk{}, nil, fmt.Errorf(
					"pgimport: source record ending at byte %d exceeds the %d-byte chunk bound",
					record.end, maxBytes,
				)
			}
			pending = &record
			break
		}
		chunk.records = append(chunk.records, record)
		chunk.end = record.end
		chunk.checksum = advanceImportChecksum(chunk.checksum, record.fields)
	}
	if !chunk.complete && len(chunk.records) == maxRows {
		record, err := next()
		switch {
		case errors.Is(err, io.EOF):
			chunk.complete = true
			chunk.end = source.Offset()
		case err != nil:
			return importChunk{}, nil, err
		default:
			pending = &record
		}
	}
	return chunk, pending, nil
}

func commitImportChunk(
	ctx context.Context,
	database *sql.DB,
	config normalizedConfig,
	chunk importChunk,
	expected, previous Status,
	hadPrevious bool,
) (Status, error) {
	var lastErr error
	for attempt := 0; attempt < importRetryLimit; attempt++ {
		lastErr = sendImportChunk(ctx, database, config, chunk)
		current, found, statusErr := QueryStatus(ctx, database, config.ImportID)
		if statusErr == nil && found && sameCommittedChunk(current, expected) {
			return current, nil
		}
		if lastErr == nil {
			if statusErr != nil {
				lastErr = fmt.Errorf("pgimport: committed chunk %d but cannot verify its watermark: %w", chunk.number, statusErr)
			} else {
				lastErr = fmt.Errorf("pgimport: committed chunk %d without its KIMP watermark", chunk.number)
			}
		}
		if statusErr == nil && found && !samePreviousStatus(current, previous) {
			return Status{}, &importAdvancedError{status: current}
		}
		if statusErr == nil && !found && hadPrevious {
			return Status{}, fmt.Errorf("pgimport: import %q watermark disappeared", config.ImportID)
		}
		if !retryableImportError(lastErr) || attempt+1 == importRetryLimit {
			return Status{}, fmt.Errorf("pgimport: commit chunk %d: %w", chunk.number, lastErr)
		}
		delay := time.Duration(attempt+1) * 25 * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Status{}, ctx.Err()
		case <-timer.C:
		}
	}
	return Status{}, lastErr
}

func sendImportChunk(ctx context.Context, database *sql.DB, config normalizedConfig, chunk importChunk) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	query := importCopyQuery(config, chunk)
	statement, err := transaction.PrepareContext(ctx, query)
	if err != nil {
		_ = transaction.Rollback()
		return err
	}
	for _, record := range chunk.records {
		if _, err := statement.ExecContext(ctx, record.values...); err != nil {
			_ = statement.Close()
			_ = transaction.Rollback()
			return err
		}
	}
	result, err := statement.ExecContext(ctx)
	if err != nil {
		_ = statement.Close()
		_ = transaction.Rollback()
		return err
	}
	if err := statement.Close(); err != nil {
		_ = transaction.Rollback()
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != int64(len(chunk.records)) {
		_ = transaction.Rollback()
		if err != nil {
			return err
		}
		return fmt.Errorf("COPY reported %d rows; expected %d", affected, len(chunk.records))
	}
	return transaction.Commit()
}

func importCopyQuery(config normalizedConfig, chunk importChunk) string {
	columns := make([]string, len(config.Columns))
	for index, column := range config.Columns {
		columns[index] = quoteIdentifier(column)
	}
	return fmt.Sprintf(
		"COPY %s (%s) FROM STDIN WITH ("+
			"KITDB_IMPORT %s, KITDB_SOURCE %s, KITDB_SOURCE_FORMAT %s, "+
			"KITDB_CHUNK %d, KITDB_START %d, KITDB_END %d, KITDB_ROWS %d, "+
			"KITDB_PREVIOUS %s, KITDB_CHECKSUM %s, KITDB_COMPLETE %t)",
		quoteIdentifier(config.Table), strings.Join(columns, ", "),
		quoteLiteral(config.ImportID), quoteLiteral(config.SourceID), quoteLiteral(config.format),
		chunk.number, chunk.start, chunk.end, len(chunk.records),
		quoteLiteral(hex.EncodeToString(chunk.previous[:])), quoteLiteral(hex.EncodeToString(chunk.checksum[:])),
		chunk.complete,
	)
}

// QueryStatus reads the durable KIMP watermark through the SQL-light profile.
func QueryStatus(ctx context.Context, database *sql.DB, id string) (Status, bool, error) {
	if database == nil {
		return Status{}, false, fmt.Errorf("pgimport: database is unavailable")
	}
	if err := validateIdentity("import id", id, 256); err != nil {
		return Status{}, false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	status, err := scanImportStatus(
		database.QueryRowContext(ctx, "PRAGMA import_status("+quoteLiteral(id)+")"),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Status{}, false, nil
	}
	if err != nil {
		return Status{}, false, err
	}
	return status, true, nil
}

// Cancel marks an incomplete import terminal without removing committed rows.
func Cancel(ctx context.Context, database *sql.DB, id string) (Status, error) {
	if database == nil {
		return Status{}, fmt.Errorf("pgimport: database is unavailable")
	}
	if err := validateIdentity("import id", id, 256); err != nil {
		return Status{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return scanImportStatus(
		database.QueryRowContext(ctx, "PRAGMA import_cancel("+quoteLiteral(id)+")"),
	)
}

// Forget removes a terminal import checkpoint. It never removes imported rows.
func Forget(ctx context.Context, database *sql.DB, id string) (bool, error) {
	if database == nil {
		return false, fmt.Errorf("pgimport: database is unavailable")
	}
	if err := validateIdentity("import id", id, 256); err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var forgotten int64
	if err := database.QueryRowContext(
		ctx, "PRAGMA import_forget("+quoteLiteral(id)+")",
	).Scan(&forgotten); err != nil {
		return false, err
	}
	if forgotten != 0 && forgotten != 1 {
		return false, fmt.Errorf("pgimport: server returned an invalid forget result")
	}
	return forgotten == 1, nil
}

type importStatusScanner interface {
	Scan(destinations ...any) error
}

func scanImportStatus(scanner importStatusScanner) (Status, error) {
	var (
		status      Status
		columnsJSON string
		chunk       int64
		rows        int64
		offset      int64
		complete    int64
		cancelled   int64
		transaction int64
	)
	err := scanner.Scan(
		&status.ImportID, &status.Source, &status.SourceFormat, &status.Table, &columnsJSON,
		&status.SeedChecksum, &status.Checksum, &chunk, &rows, &offset, &complete, &cancelled, &transaction,
	)
	if err != nil {
		return Status{}, err
	}
	if chunk < 0 || rows < 0 || offset < 0 || transaction < 0 ||
		(complete != 0 && complete != 1) || (cancelled != 0 && cancelled != 1) ||
		(complete == 1 && cancelled == 1) {
		return Status{}, fmt.Errorf("pgimport: server returned an invalid import watermark")
	}
	if err := json.Unmarshal([]byte(columnsJSON), &status.Columns); err != nil {
		return Status{}, fmt.Errorf("pgimport: decode status columns: %w", err)
	}
	status.Chunk = uint64(chunk)
	status.Rows = uint64(rows)
	status.Offset = uint64(offset)
	status.Complete = complete == 1
	status.Cancelled = cancelled == 1
	status.Transaction = uint64(transaction)
	return status, nil
}

func sameCommittedChunk(current, expected Status) bool {
	return current.ImportID == expected.ImportID && current.Chunk == expected.Chunk &&
		current.Rows == expected.Rows && current.Offset == expected.Offset &&
		current.Checksum == expected.Checksum && current.Complete == expected.Complete &&
		current.Cancelled == expected.Cancelled
}

func samePreviousStatus(current, previous Status) bool {
	return current.ImportID == previous.ImportID && current.Chunk == previous.Chunk &&
		current.Rows == previous.Rows && current.Offset == previous.Offset &&
		current.Checksum == previous.Checksum && current.Cancelled == previous.Cancelled
}

func retryableImportError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, io.EOF) {
		return true
	}
	var postgresErr *pq.Error
	if errors.As(err, &postgresErr) {
		code := string(postgresErr.Code)
		return code == "40001" || strings.HasPrefix(code, "08") || code == "57P01"
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "connection") || strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "reset by peer") || strings.Contains(lower, "unexpected eof")
}

func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func quoteLiteral(literal string) string {
	return `'` + strings.ReplaceAll(literal, `'`, `''`) + `'`
}

func stableSourceID(path string) string {
	digest := sha256.Sum256([]byte(filepath.Clean(path)))
	return "file-" + hex.EncodeToString(digest[:])
}

func stableImportID(source, table string) string {
	digest := sha256.Sum256([]byte(source + "\x00" + table))
	return "import-" + hex.EncodeToString(digest[:16])
}

func validateIdentity(name, identity string, limit int) error {
	if identity == "" || len(identity) > limit || !utf8.ValidString(identity) || strings.ContainsRune(identity, 0) {
		return fmt.Errorf("pgimport: %s must be valid UTF-8 between 1 and %d bytes", name, limit)
	}
	for _, current := range identity {
		if current < 0x20 || current == 0x7f {
			return fmt.Errorf("pgimport: %s contains a control character", name)
		}
	}
	return nil
}
