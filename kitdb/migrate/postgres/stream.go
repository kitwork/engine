package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

const (
	defaultMigrationChunkRows  = 512
	defaultMigrationChunkBytes = 8 << 20
	maximumMigrationChunkRows  = 10_000
	maximumMigrationChunkBytes = 32 << 20
)

type MigrationConfig struct {
	SourceURL  string
	TargetURL  string
	TargetDB   string
	ID         string
	Plan       TargetPlan
	Bulk       bool
	ChunkRows  int
	ChunkBytes int64
	MaxRows    int64
	Progress   func(MigrationProgress)
}

type MigrationProgress struct {
	ID        string        `json:"id"`
	Chunk     int64         `json:"chunk"`
	Rows      int64         `json:"rows"`
	ChunkRows int           `json:"chunk_rows"`
	Bytes     int64         `json:"chunk_bytes"`
	Cursor    string        `json:"cursor"`
	Checksum  string        `json:"checksum"`
	Elapsed   time.Duration `json:"elapsed"`
}

type MigrationReport struct {
	ID              string        `json:"id"`
	Source          string        `json:"source"`
	TargetDatabase  string        `json:"target_database"`
	TargetTable     string        `json:"target_table"`
	Snapshot        string        `json:"snapshot"`
	StartLSN        string        `json:"start_lsn"`
	Rows            int64         `json:"rows"`
	Chunks          int64         `json:"chunks"`
	Bytes           int64         `json:"bytes"`
	Cursor          string        `json:"cursor"`
	Checksum        string        `json:"checksum"`
	Complete        bool          `json:"complete"`
	SourceExhausted bool          `json:"source_exhausted"`
	ReachedLimit    bool          `json:"reached_limit"`
	Duration        time.Duration `json:"duration"`
	RowsPerSecond   float64       `json:"rows_per_second"`
	Resumed         bool          `json:"resumed"`
	ResumedRows     int64         `json:"resumed_rows,omitempty"`
	ValidatedRows   int64         `json:"validated_rows,omitempty"`
	ValidationTime  time.Duration `json:"validation_time,omitempty"`
}

type migrationState struct {
	Source      string
	Fingerprint string
	Cursor      string
	Rows        int64
	Checksum    string
	Complete    bool
}

type migrationCursor struct {
	Merchant string `json:"merchant"`
	ID       int64  `json:"id,string"`
}

type migrationChunk struct {
	rows      [][]any
	cursor    migrationCursor
	encoded   string
	checksum  [sha256.Size]byte
	bytes     int64
	exhausted bool
}

type scannedValue struct {
	destination any
	read        func() (any, []byte, error)
}

const migrationTargetCommitAttempts = 3

type migrationCommitResolution uint8

const (
	migrationCommitUnknown migrationCommitResolution = iota
	migrationCommitApplied
	migrationCommitNotApplied
)

// Migrate streams one bounded target transaction at a time from a single
// PostgreSQL repeatable-read snapshot. An incomplete run may continue on a new
// snapshot only after the complete committed prefix reproduces its cursor and
// chained checksum. This makes resume fail closed when source history changed.
func Migrate(ctx context.Context, config MigrationConfig) (MigrationReport, error) {
	config, err := normalizeMigrationConfig(config)
	if err != nil {
		return MigrationReport{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	started := time.Now()
	sourceLabel := config.Plan.SourceDatabase + "/" + config.Plan.SourceSchema + "." + config.Plan.SourceTable
	report := MigrationReport{
		ID: config.ID, Source: sourceLabel, TargetDatabase: config.TargetDB,
		TargetTable: config.Plan.TargetTable,
	}

	targetURL, err := URLWithDatabase(config.TargetURL, config.TargetDB)
	if err != nil {
		return MigrationReport{}, err
	}
	target, err := openTarget(ctx, targetURL)
	if err != nil {
		return MigrationReport{}, err
	}
	defer target.Close()
	existing, found, err := loadMigrationState(ctx, target, config.ID)
	if err != nil {
		return MigrationReport{}, err
	}
	if found {
		if existing.Source != sourceLabel || existing.Fingerprint != config.Plan.SchemaFingerprint {
			return MigrationReport{}, fmt.Errorf("postgres migration: id %q belongs to another source or schema", config.ID)
		}
		if existing.Complete {
			report.Rows = existing.Rows
			report.Cursor = existing.Cursor
			report.Checksum = existing.Checksum
			report.Complete = true
			return finishMigrationReport(report, started), nil
		}
	}

	source, err := sql.Open("postgres", config.SourceURL)
	if err != nil {
		return MigrationReport{}, sourceError(config.SourceURL, "initialize migration source", err)
	}
	source.SetMaxOpenConns(1)
	source.SetMaxIdleConns(0)
	defer source.Close()
	if err := source.PingContext(ctx); err != nil {
		return MigrationReport{}, sourceError(config.SourceURL, "connect migration source", err)
	}
	snapshot, err := source.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return MigrationReport{}, sourceError(config.SourceURL, "begin migration snapshot", err)
	}
	defer snapshot.Rollback()
	if err := snapshot.QueryRowContext(ctx,
		`SELECT txid_current_snapshot()::text, pg_current_wal_lsn()::text`,
	).Scan(&report.Snapshot, &report.StartLSN); err != nil {
		return MigrationReport{}, sourceError(config.SourceURL, "identify migration snapshot", err)
	}

	checksum := migrationSeed(config.ID, sourceLabel, config.Plan.SchemaFingerprint, config.Plan.TargetColumns)
	var cursor migrationCursor
	startedCursor := false
	stateFound := found
	if found {
		validationStarted := time.Now()
		cursor, checksum, err = validateMigrationPrefix(ctx, snapshot, config, existing, checksum)
		if err != nil {
			return report, err
		}
		report.Resumed = true
		report.ResumedRows = existing.Rows
		report.ValidatedRows = existing.Rows
		report.ValidationTime = time.Since(validationStarted)
		report.Rows = existing.Rows
		report.Cursor = existing.Cursor
		report.Checksum = existing.Checksum
		startedCursor = existing.Rows > 0
	}
	for {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		remaining := config.MaxRows - report.Rows
		if config.MaxRows > 0 && remaining <= 0 {
			report.ReachedLimit = true
			report.Complete = true
			break
		}
		requested := config.ChunkRows
		if config.MaxRows > 0 && remaining < int64(requested) {
			requested = int(remaining)
		}
		chunk, err := readMigrationChunk(
			ctx, snapshot, config, cursor, startedCursor, requested, checksum,
		)
		if err != nil {
			return report, err
		}
		if len(chunk.rows) == 0 {
			report.SourceExhausted = true
			report.Complete = true
			if err := commitEmptyMigrationState(ctx, target, config, sourceLabel, report, checksum); err != nil {
				return report, err
			}
			break
		}

		nextRows := report.Rows + int64(len(chunk.rows))
		nextChunks := report.Chunks + 1
		reachedLimit := config.MaxRows > 0 && nextRows >= config.MaxRows
		complete := chunk.exhausted || reachedLimit
		previous := migrationState{
			Source: sourceLabel, Fingerprint: config.Plan.SchemaFingerprint,
			Cursor: report.Cursor, Rows: report.Rows, Checksum: report.Checksum,
			Complete: report.Complete,
		}
		if err := commitMigrationChunkResilient(
			ctx, target, config, sourceLabel, chunk, nextRows, complete,
			previous, stateFound,
		); err != nil {
			return report, err
		}
		stateFound = true
		cursor, startedCursor, checksum = chunk.cursor, true, chunk.checksum
		report.Rows = nextRows
		report.Chunks = nextChunks
		report.Bytes += chunk.bytes
		report.Cursor = chunk.encoded
		report.Checksum = hex.EncodeToString(checksum[:])
		report.SourceExhausted = chunk.exhausted
		report.ReachedLimit = reachedLimit
		report.Complete = complete
		if config.Progress != nil {
			config.Progress(MigrationProgress{
				ID: config.ID, Chunk: report.Chunks, Rows: report.Rows,
				ChunkRows: len(chunk.rows), Bytes: chunk.bytes,
				Cursor: chunk.encoded, Checksum: report.Checksum,
				Elapsed: time.Since(started),
			})
		}
		if complete {
			break
		}
	}
	return finishMigrationReport(report, started), nil
}

func validateMigrationPrefix(
	ctx context.Context,
	snapshot *sql.Tx,
	config MigrationConfig,
	existing migrationState,
	seed [sha256.Size]byte,
) (migrationCursor, [sha256.Size]byte, error) {
	if existing.Rows < 0 {
		return migrationCursor{}, seed, fmt.Errorf("postgres migration: target watermark has a negative row count")
	}
	expectedChecksum, err := decodeMigrationChecksum(existing.Checksum)
	if err != nil {
		return migrationCursor{}, seed, err
	}
	if existing.Rows == 0 {
		if existing.Cursor != "" || expectedChecksum != seed {
			return migrationCursor{}, seed, fmt.Errorf("postgres migration: empty target watermark does not match the migration seed")
		}
		return migrationCursor{}, seed, nil
	}

	checksum := seed
	var cursor migrationCursor
	started := false
	validated := int64(0)
	for validated < existing.Rows {
		remaining := existing.Rows - validated
		requested := config.ChunkRows
		if remaining < int64(requested) {
			requested = int(remaining)
		}
		chunk, readErr := readMigrationChunk(ctx, snapshot, config, cursor, started, requested, checksum)
		if readErr != nil {
			return migrationCursor{}, seed, fmt.Errorf("postgres migration: validate committed source prefix: %w", readErr)
		}
		if len(chunk.rows) == 0 {
			return migrationCursor{}, seed, fmt.Errorf(
				"postgres migration: source ended after %d rows while validating %d committed rows",
				validated, existing.Rows,
			)
		}
		validated += int64(len(chunk.rows))
		cursor, checksum, started = chunk.cursor, chunk.checksum, true
	}
	encodedCursor, err := json.Marshal(cursor)
	if err != nil {
		return migrationCursor{}, seed, fmt.Errorf("postgres migration: encode validated cursor: %w", err)
	}
	if string(encodedCursor) != existing.Cursor || checksum != expectedChecksum {
		return migrationCursor{}, seed, fmt.Errorf(
			"postgres migration: committed prefix changed in the new PostgreSQL snapshot; target remains untouched",
		)
	}
	return cursor, checksum, nil
}

func decodeMigrationChecksum(encoded string) ([sha256.Size]byte, error) {
	var checksum [sha256.Size]byte
	decoded, err := hex.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(decoded) != sha256.Size {
		return checksum, fmt.Errorf("postgres migration: target watermark has an invalid checksum")
	}
	copy(checksum[:], decoded)
	return checksum, nil
}

func normalizeMigrationConfig(config MigrationConfig) (MigrationConfig, error) {
	config.SourceURL = strings.TrimSpace(config.SourceURL)
	config.TargetURL = strings.TrimSpace(config.TargetURL)
	config.TargetDB = strings.TrimSpace(config.TargetDB)
	config.ID = strings.TrimSpace(config.ID)
	if config.SourceURL == "" || config.TargetURL == "" || config.TargetDB == "" || config.ID == "" {
		return MigrationConfig{}, fmt.Errorf("postgres migration: source URL, target URL, target database and id are required")
	}
	if (config.Plan.Format != TargetPlanFormat && config.Plan.Format != legacyTargetPlanFormat) ||
		config.Plan.TargetTable == "" {
		return MigrationConfig{}, fmt.Errorf("postgres migration: a valid target plan is required")
	}
	if (config.Plan.PrimaryStrategy != "native-composite" && config.Plan.PrimaryStrategy != "synthetic-composite") ||
		len(config.Plan.SourcePrimary) != 2 ||
		config.Plan.SourcePrimary[0] != "merchant" || config.Plan.SourcePrimary[1] != "id" {
		return MigrationConfig{}, fmt.Errorf("postgres migration: the initial streaming profile requires source primary key (merchant, id)")
	}
	if config.ChunkRows == 0 {
		config.ChunkRows = defaultMigrationChunkRows
	}
	if config.ChunkRows < 1 || config.ChunkRows > maximumMigrationChunkRows {
		return MigrationConfig{}, fmt.Errorf("postgres migration: chunk rows must be between 1 and %d", maximumMigrationChunkRows)
	}
	if config.ChunkBytes == 0 {
		config.ChunkBytes = defaultMigrationChunkBytes
	}
	if config.ChunkBytes < 1<<20 || config.ChunkBytes > maximumMigrationChunkBytes {
		return MigrationConfig{}, fmt.Errorf("postgres migration: chunk bytes must be between 1 MiB and %d", maximumMigrationChunkBytes)
	}
	if config.MaxRows < 0 {
		return MigrationConfig{}, fmt.Errorf("postgres migration: max rows cannot be negative")
	}
	return config, nil
}

func readMigrationChunk(
	ctx context.Context,
	snapshot *sql.Tx,
	config MigrationConfig,
	cursor migrationCursor,
	started bool,
	limit int,
	previous [sha256.Size]byte,
) (migrationChunk, error) {
	quotedColumns := make([]string, len(config.Plan.SourceColumns))
	for position, column := range config.Plan.SourceColumns {
		quotedColumns[position] = quoteIdentifier(column)
	}
	query := fmt.Sprintf(
		"SELECT %s FROM %s.%s WHERE NOT $1::boolean OR (%s, %s) > ($2::text, $3::bigint) "+
			"ORDER BY %s, %s LIMIT $4",
		strings.Join(quotedColumns, ", "),
		quoteIdentifier(config.Plan.SourceSchema), quoteIdentifier(config.Plan.SourceTable),
		quoteIdentifier("merchant"), quoteIdentifier("id"),
		quoteIdentifier("merchant"), quoteIdentifier("id"),
	)
	rows, err := snapshot.QueryContext(ctx, query, started, cursor.Merchant, cursor.ID, limit)
	if err != nil {
		return migrationChunk{}, sourceError(config.SourceURL, "read source chunk", err)
	}
	defer rows.Close()

	chunk := migrationChunk{checksum: previous}
	byteLimited := false
	for rows.Next() {
		cells, err := newScanValues(config.Plan)
		if err != nil {
			return migrationChunk{}, err
		}
		destinations := make([]any, len(cells))
		for position := range cells {
			destinations[position] = cells[position].destination
		}
		if err := rows.Scan(destinations...); err != nil {
			return migrationChunk{}, sourceError(config.SourceURL, "decode source row", err)
		}
		values := make([]any, 0, len(cells)+1)
		canonical := make([][]byte, 0, len(cells)+1)
		var merchant string
		var identifier int64
		for position, cell := range cells {
			value, encoded, err := cell.read()
			if err != nil {
				return migrationChunk{}, fmt.Errorf("postgres migration: column %q: %w", config.Plan.SourceColumns[position], err)
			}
			switch config.Plan.SourceColumns[position] {
			case "merchant":
				merchant, _ = value.(string)
			case "id":
				identifier, _ = value.(int64)
			}
			values = append(values, value)
			canonical = append(canonical, encoded)
		}
		if merchant == "" {
			return migrationChunk{}, fmt.Errorf("postgres migration: source row has an empty merchant key")
		}
		if config.Plan.SyntheticPrimary != "" {
			key := syntheticShoppingKey(merchant, identifier)
			values = append([]any{key}, values...)
			canonical = append([][]byte{[]byte(key)}, canonical...)
		}
		rowBytes := canonicalSize(canonical)
		if len(chunk.rows) > 0 && chunk.bytes+rowBytes > config.ChunkBytes {
			byteLimited = true
			break
		}
		chunk.rows = append(chunk.rows, values)
		chunk.bytes += rowBytes
		chunk.cursor = migrationCursor{Merchant: merchant, ID: identifier}
		chunk.checksum = advanceMigrationChecksum(chunk.checksum, canonical)
	}
	if err := rows.Err(); err != nil {
		return migrationChunk{}, sourceError(config.SourceURL, "iterate source chunk", err)
	}
	chunk.exhausted = len(chunk.rows) < limit && !byteLimited
	if len(chunk.rows) > 0 {
		encoded, err := json.Marshal(chunk.cursor)
		if err != nil {
			return migrationChunk{}, fmt.Errorf("postgres migration: encode cursor: %w", err)
		}
		chunk.encoded = string(encoded)
	}
	return chunk, nil
}

func newScanValues(plan TargetPlan) ([]scannedValue, error) {
	byName := make(map[string]ColumnReport, len(plan.SourceColumns))
	// TargetPlan intentionally stores only names. Its source mappings are
	// reconstructed from the target kinds in the same position.
	for position, sourceName := range plan.SourceColumns {
		targetPosition := position
		if plan.SyntheticPrimary != "" {
			targetPosition++
		}
		if targetPosition >= len(plan.TargetColumns) || plan.TargetColumns[targetPosition] != sourceName {
			return nil, fmt.Errorf("postgres migration: source/target column order drift")
		}
		byName[sourceName] = ColumnReport{Name: sourceName}
	}
	values := make([]scannedValue, 0, len(plan.SourceColumns))
	for _, name := range plan.SourceColumns {
		values = append(values, scannerForShoppingColumn(name))
	}
	return values, nil
}

func scannerForShoppingColumn(name string) scannedValue {
	switch name {
	case "id", "shop", "arrange", "category", "categorization", "review", "sold", "selled",
		"price", "pricing", "discount", "expired", "stock", "like":
		value := &sql.NullInt64{}
		return scannedValue{destination: value, read: func() (any, []byte, error) {
			if !value.Valid {
				return nil, nil, nil
			}
			encoded := strconv.AppendInt(nil, value.Int64, 10)
			return value.Int64, encoded, nil
		}}
	case "official", "is_adult", "is_deleted", "is_detail":
		value := &sql.NullBool{}
		return scannedValue{destination: value, read: func() (any, []byte, error) {
			if !value.Valid {
				return nil, nil, nil
			}
			if value.Bool {
				return true, []byte("true"), nil
			}
			return false, []byte("false"), nil
		}}
	case "rating", "commission":
		value := &sql.NullString{}
		return scannedValue{destination: value, read: func() (any, []byte, error) {
			if !value.Valid {
				return nil, nil, nil
			}
			return value.String, []byte(value.String), nil
		}}
	case "images", "categories":
		value := &pq.StringArray{}
		return scannedValue{destination: value, read: func() (any, []byte, error) {
			encoded, err := json.Marshal([]string(*value))
			if err != nil {
				return nil, nil, err
			}
			return string(encoded), encoded, nil
		}}
	case "attributes":
		value := &sql.NullString{}
		return scannedValue{destination: value, read: func() (any, []byte, error) {
			if !value.Valid {
				return nil, nil, nil
			}
			encoded, err := canonicalJSON([]byte(value.String))
			if err != nil {
				return nil, nil, fmt.Errorf("invalid jsonb: %w", err)
			}
			return string(encoded), encoded, nil
		}}
	default:
		value := &sql.NullString{}
		return scannedValue{destination: value, read: func() (any, []byte, error) {
			if !value.Valid {
				return nil, nil, nil
			}
			return value.String, []byte(value.String), nil
		}}
	}
}

func canonicalJSON(source []byte) ([]byte, error) {
	var decoded any
	if err := json.Unmarshal(source, &decoded); err != nil {
		return nil, err
	}
	return json.Marshal(decoded)
}

type TargetCountVerification struct {
	ID           string `json:"id"`
	Database     string `json:"database"`
	Table        string `json:"table"`
	Rows         int64  `json:"rows"`
	StateRows    int64  `json:"state_rows"`
	Complete     bool   `json:"complete"`
	CountMatches bool   `json:"count_matches"`
	Verified     bool   `json:"verified"`
}

// VerifyTargetCount proves that the committed relational row count and the
// atomically published migration state agree. It is not a logical digest.
func VerifyTargetCount(
	ctx context.Context,
	targetURL, databaseName, table, id string,
) (TargetCountVerification, error) {
	databaseURL, err := URLWithDatabase(targetURL, databaseName)
	if err != nil {
		return TargetCountVerification{}, err
	}
	target, err := openTarget(ctx, databaseURL)
	if err != nil {
		return TargetCountVerification{}, err
	}
	defer target.Close()
	state, found, err := loadMigrationState(ctx, target, id)
	if err != nil {
		return TargetCountVerification{}, err
	}
	if !found {
		return TargetCountVerification{}, fmt.Errorf("postgres migration: target state %q does not exist", id)
	}
	verification := TargetCountVerification{
		ID: id, Database: databaseName, Table: table,
		StateRows: state.Rows, Complete: state.Complete,
	}
	query := "SELECT COUNT(*) FROM " + quoteIdentifier(table)
	if err := target.QueryRowContext(ctx, query).Scan(&verification.Rows); err != nil {
		return TargetCountVerification{}, fmt.Errorf("postgres migration: count target rows: %w", err)
	}
	verification.CountMatches = verification.Rows == verification.StateRows
	verification.Verified = verification.Complete && verification.CountMatches
	return verification, nil
}

func commitMigrationChunk(
	ctx context.Context,
	target *sql.DB,
	config MigrationConfig,
	sourceLabel string,
	chunk migrationChunk,
	totalRows int64,
	complete bool,
) error {
	transaction, err := target.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres migration: begin target chunk: %w", err)
	}
	defer transaction.Rollback()
	copySQL := pq.CopyIn(config.Plan.TargetTable, config.Plan.TargetColumns...)
	if config.Bulk {
		copySQL += " WITH (KITDB_BULK TRUE)"
	}
	statement, err := transaction.PrepareContext(ctx, copySQL)
	if err != nil {
		return fmt.Errorf("postgres migration: prepare target COPY: %w", err)
	}
	for _, row := range chunk.rows {
		if _, err := statement.ExecContext(ctx, row...); err != nil {
			statement.Close()
			return fmt.Errorf("postgres migration: send target row: %w", err)
		}
	}
	if _, err := statement.ExecContext(ctx); err != nil {
		statement.Close()
		return fmt.Errorf("postgres migration: finish target COPY: %w", err)
	}
	if err := statement.Close(); err != nil {
		return fmt.Errorf("postgres migration: close target COPY: %w", err)
	}
	checksum := hex.EncodeToString(chunk.checksum[:])
	if err := replaceMigrationState(
		ctx, transaction, config.ID, sourceLabel, config.Plan.SchemaFingerprint,
		chunk.encoded, totalRows, checksum, complete,
	); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("postgres migration: commit target chunk: %w", err)
	}
	return nil
}

func commitMigrationChunkResilient(
	ctx context.Context,
	target *sql.DB,
	config MigrationConfig,
	sourceLabel string,
	chunk migrationChunk,
	totalRows int64,
	complete bool,
	previous migrationState,
	previousFound bool,
) error {
	expected := migrationState{
		Source: sourceLabel, Fingerprint: config.Plan.SchemaFingerprint,
		Cursor: chunk.encoded, Rows: totalRows,
		Checksum: hex.EncodeToString(chunk.checksum[:]), Complete: complete,
	}
	var lastErr error
	for attempt := 0; attempt < migrationTargetCommitAttempts; attempt++ {
		if attempt > 0 {
			if err := waitMigrationRetry(ctx, attempt); err != nil {
				return err
			}
		}
		err := commitMigrationChunk(ctx, target, config, sourceLabel, chunk, totalRows, complete)
		if err == nil {
			return nil
		}
		if !isTransientMigrationTargetError(err) {
			return err
		}
		lastErr = err
		resolution, resolveErr := resolveMigrationCommit(
			ctx, target, config.ID, expected, previous, previousFound,
		)
		if resolveErr != nil {
			return fmt.Errorf("postgres migration: target commit outcome is ambiguous after %w: %v", err, resolveErr)
		}
		switch resolution {
		case migrationCommitApplied:
			return nil
		case migrationCommitNotApplied:
			continue
		default:
			return fmt.Errorf("postgres migration: target commit outcome is ambiguous after %w", err)
		}
	}
	return fmt.Errorf("postgres migration: target commit failed after %d resolved retries: %w", migrationTargetCommitAttempts, lastErr)
}

func resolveMigrationCommit(
	ctx context.Context,
	target *sql.DB,
	id string,
	expected migrationState,
	previous migrationState,
	previousFound bool,
) (migrationCommitResolution, error) {
	var lastErr error
	for attempt := 0; attempt < migrationTargetCommitAttempts; attempt++ {
		if attempt > 0 {
			if err := waitMigrationRetry(ctx, attempt); err != nil {
				return migrationCommitUnknown, err
			}
		}
		state, found, err := loadMigrationState(ctx, target, id)
		if err != nil {
			if !isTransientMigrationTargetError(err) {
				return migrationCommitUnknown, err
			}
			lastErr = err
			continue
		}
		if found && migrationStatesEqual(state, expected) {
			return migrationCommitApplied, nil
		}
		if found == previousFound && (!found || migrationStatesEqual(state, previous)) {
			return migrationCommitNotApplied, nil
		}
		return migrationCommitUnknown, fmt.Errorf(
			"target watermark diverged: found=%t rows=%d cursor=%q",
			found, state.Rows, state.Cursor,
		)
	}
	return migrationCommitUnknown, fmt.Errorf("cannot read target watermark after transient failure: %w", lastErr)
}

func migrationStatesEqual(left, right migrationState) bool {
	return left.Source == right.Source &&
		left.Fingerprint == right.Fingerprint &&
		left.Cursor == right.Cursor &&
		left.Rows == right.Rows &&
		left.Checksum == right.Checksum &&
		left.Complete == right.Complete
}

func isTransientMigrationTargetError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, io.EOF) {
		return true
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, fragment := range []string{
		"bad connection", "broken pipe", "connection reset", "connection refused",
		"connection aborted", "server closed the connection", "unexpected eof",
		"forcibly closed", "wsarecv",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

func waitMigrationRetry(ctx context.Context, attempt int) error {
	delay := time.Duration(attempt) * 250 * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func commitEmptyMigrationState(
	ctx context.Context,
	target *sql.DB,
	config MigrationConfig,
	sourceLabel string,
	report MigrationReport,
	checksum [sha256.Size]byte,
) error {
	transaction, err := target.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres migration: begin empty target state: %w", err)
	}
	defer transaction.Rollback()
	if err := replaceMigrationState(
		ctx, transaction, config.ID, sourceLabel, config.Plan.SchemaFingerprint,
		report.Cursor, report.Rows, hex.EncodeToString(checksum[:]), true,
	); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("postgres migration: commit empty target state: %w", err)
	}
	return nil
}

func replaceMigrationState(
	ctx context.Context,
	target *sql.Tx,
	id, source, fingerprint, cursor string,
	rows int64,
	checksum string,
	complete bool,
) error {
	if _, err := target.ExecContext(ctx,
		`DELETE FROM "_kitdb_migration" WHERE "id" = $1`, id,
	); err != nil {
		return fmt.Errorf("postgres migration: retire previous target watermark: %w", err)
	}
	columns := []string{
		"id", "source", "schema_fingerprint", "cursor",
		"rows", "checksum", "complete", "updated_at",
	}
	statement, err := target.PrepareContext(ctx, pq.CopyIn(migrationStateTable, columns...))
	if err != nil {
		return fmt.Errorf("postgres migration: prepare target watermark COPY: %w", err)
	}
	if _, err := statement.ExecContext(
		ctx,
		id, source, fingerprint, cursor, rows, checksum, complete,
		time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		statement.Close()
		return fmt.Errorf("postgres migration: send target watermark: %w", err)
	}
	if _, err := statement.ExecContext(ctx); err != nil {
		statement.Close()
		return fmt.Errorf("postgres migration: finish target watermark COPY: %w", err)
	}
	if err := statement.Close(); err != nil {
		return fmt.Errorf("postgres migration: close target watermark COPY: %w", err)
	}
	return nil
}

func loadMigrationState(ctx context.Context, target *sql.DB, id string) (migrationState, bool, error) {
	var state migrationState
	err := target.QueryRowContext(ctx, `
		SELECT "source", "schema_fingerprint", "cursor", "rows", "checksum", "complete"
		FROM "_kitdb_migration" WHERE "id" = $1
	`, id).Scan(
		&state.Source, &state.Fingerprint, &state.Cursor,
		&state.Rows, &state.Checksum, &state.Complete,
	)
	if err == sql.ErrNoRows {
		return migrationState{}, false, nil
	}
	if err != nil {
		return migrationState{}, false, fmt.Errorf("postgres migration: read target watermark: %w", err)
	}
	return state, true, nil
}

func migrationSeed(id, source, fingerprint string, columns []string) [sha256.Size]byte {
	return sha256.Sum256([]byte("kitdb-postgres-migration/v1\x00" + id + "\x00" + source + "\x00" + fingerprint + "\x00" + strings.Join(columns, "\x00")))
}

func advanceMigrationChecksum(previous [sha256.Size]byte, fields [][]byte) [sha256.Size]byte {
	hasher := sha256.New()
	hasher.Write(previous[:])
	for _, field := range fields {
		writeCanonicalField(hasher, field)
	}
	var result [sha256.Size]byte
	copy(result[:], hasher.Sum(nil))
	return result
}

func writeCanonicalField(hasher hash.Hash, field []byte) {
	if field == nil {
		hasher.Write([]byte{0})
		return
	}
	hasher.Write([]byte{1})
	var length [binary.MaxVarintLen64]byte
	size := binary.PutUvarint(length[:], uint64(len(field)))
	hasher.Write(length[:size])
	hasher.Write(field)
}

func canonicalSize(fields [][]byte) int64 {
	var size int64
	for _, field := range fields {
		size += int64(len(field) + binary.MaxVarintLen64 + 1)
	}
	return size
}

func syntheticShoppingKey(merchant string, id int64) string {
	merchantKey := hex.EncodeToString([]byte(merchant))
	orderedID := uint64(id) ^ uint64(1<<63)
	return merchantKey + ":" + fmt.Sprintf("%016x", orderedID)
}

func finishMigrationReport(report MigrationReport, started time.Time) MigrationReport {
	report.Duration = time.Since(started)
	seconds := report.Duration.Seconds()
	if seconds > 0 {
		report.RowsPerSecond = float64(report.Rows) / seconds
	}
	if math.IsNaN(report.RowsPerSecond) || math.IsInf(report.RowsPerSecond, 0) {
		report.RowsPerSecond = 0
	}
	return report
}
