package postgres

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/kitwork/engine/search"
)

const (
	ShoppingSearchCanaryFormat = "kitdb-postgres-shopping-search-canary/v1"
	defaultShoppingSearchPage  = 2_048
	maximumShoppingSearchPage  = 10_000
	defaultSearchSamples       = 20
	maximumSearchSamples       = 1_000
	defaultSearchSegmentRows   = 250_000
	defaultSearchSegmentBytes  = int64(512 << 20)
	maximumSearchSegmentBytes  = int64(4 << 30)
	defaultSourceConnLifetime  = 15 * time.Minute
	minimumSourceConnLifetime  = time.Second
	maximumSourceConnLifetime  = time.Hour
	shoppingSearchTermBytes    = 256
	localSearchTimingBatch     = 32
	maximumLocalSearchBatch    = 1_024
)

type ShoppingSearchCanaryConfig struct {
	SourceURL                string
	SourceSchema             string
	SourceTable              string
	TargetURL                string
	TargetDatabase           string
	TargetTable              string
	SearchRoot               string
	IndexKey                 string
	MaxRows                  int64
	ExpectedRows             int64
	PageRows                 int
	Samples                  int
	LocalSearchBatch         int
	SegmentRows              int
	SegmentBytes             int64
	ResilientSource          bool
	ReuseIndex               bool
	SourceConnectionLifetime time.Duration
	Progress                 func(ShoppingSearchProgress)
}

type ShoppingSearchProgress struct {
	Rows    int64         `json:"rows"`
	Pages   int64         `json:"pages"`
	Elapsed time.Duration `json:"elapsed"`
}

type ShoppingSearchCanaryReport struct {
	Format                   string                    `json:"format"`
	Source                   string                    `json:"source"`
	Target                   string                    `json:"target"`
	Rows                     int64                     `json:"rows"`
	Pages                    int64                     `json:"pages"`
	BuildDuration            time.Duration             `json:"build_duration"`
	RowsPerSecond            float64                   `json:"rows_per_second"`
	Boundary                 ShoppingSearchBoundary    `json:"boundary"`
	IndexKey                 string                    `json:"index_key"`
	SchemaHash               string                    `json:"schema_fingerprint"`
	SourceConsistency        string                    `json:"source_consistency"`
	SourceConnectionLifetime time.Duration             `json:"source_connection_lifetime"`
	ReusedIndex              bool                      `json:"reused_index"`
	Index                    ShoppingSearchIndexReport `json:"index"`
	Queries                  []ShoppingQueryComparison `json:"queries"`
	PointLookup              []ShoppingLatencyReport   `json:"point_lookup"`
}

type ShoppingSearchBoundary struct {
	Merchant string `json:"merchant"`
	ID       int64  `json:"id,string"`
	Key      string `json:"key"`
}

type ShoppingSearchIndexReport struct {
	Generation       uint64 `json:"generation"`
	Segments         int    `json:"segments"`
	Documents        uint64 `json:"documents"`
	Bytes            int64  `json:"bytes"`
	MaximumDocuments int    `json:"maximum_documents_per_segment"`
	FlushThreshold   int64  `json:"flush_threshold_bytes"`
	MaximumTermBytes int    `json:"maximum_term_bytes"`
	SkipsLongTerms   bool   `json:"skips_long_terms"`
}

type ShoppingQueryComparison struct {
	Query                string                `json:"query"`
	KitDBName            ShoppingLatencyReport `json:"kitdb_search_name"`
	KitDBExpanded        ShoppingLatencyReport `json:"kitdb_search_expanded"`
	PostgreSQL           ShoppingLatencyReport `json:"postgresql_text_search"`
	NamePostgresOverlap  int                   `json:"name_postgres_top20_overlap"`
	ExpandedNameOverlap  int                   `json:"expanded_name_top20_overlap"`
	PostgreSQLScope      string                `json:"postgresql_scope"`
	KitDBProjectionScope string                `json:"kitdb_projection_scope"`
}

type ShoppingLatencyReport struct {
	Engine      string   `json:"engine"`
	Samples     int      `json:"samples"`
	Batch       int      `json:"batch"`
	Hits        int      `json:"hits"`
	P50Micros   int64    `json:"p50_micros"`
	P95Micros   int64    `json:"p95_micros"`
	MaxMicros   int64    `json:"max_micros"`
	TopIDs      []string `json:"top_ids,omitempty"`
	VerifiedKey string   `json:"verified_key,omitempty"`
}

type shoppingSearchRow struct {
	Key         string
	Merchant    string
	ID          int64
	Name        string
	Brand       string
	Description string
	Content     string
}

// RunShoppingSearchCanary streams a bounded, read-only PostgreSQL snapshot into
// a durable search projection, verifies point hydration through the migrated
// KitDB target, then compares both search engines over the same key range. It
// never writes to PostgreSQL.
func RunShoppingSearchCanary(
	ctx context.Context,
	config ShoppingSearchCanaryConfig,
) (_ ShoppingSearchCanaryReport, returnErr error) {
	config, err := normalizeShoppingSearchCanaryConfig(config)
	if err != nil {
		return ShoppingSearchCanaryReport{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	schema, err := ShoppingSearchSchema()
	if err != nil {
		return ShoppingSearchCanaryReport{}, err
	}
	manager, err := search.NewManager(config.SearchRoot, search.ManagerOptions{
		MaxOpenIndexes: 1,
		Writer: search.WriterOptions{Segment: search.BuildOptions{
			MaxDocuments:        uint32(config.SegmentRows),
			FlushThresholdBytes: config.SegmentBytes,
			MaxTermBytes:        shoppingSearchTermBytes,
			SkipLongTerms:       true,
		}},
		ReplacementTimeout: 24 * time.Hour,
		DisableAutoCompact: true,
	})
	if err != nil {
		return ShoppingSearchCanaryReport{}, fmt.Errorf("shopping search canary: open search manager: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, manager.Close()) }()

	targetURL, err := URLWithDatabase(config.TargetURL, config.TargetDatabase)
	if err != nil {
		return ShoppingSearchCanaryReport{}, err
	}
	target, err := openTarget(ctx, targetURL)
	if err != nil {
		return ShoppingSearchCanaryReport{}, err
	}
	defer target.Close()
	source, sourceTx, err := openShoppingSearchSource(ctx, config)
	if err != nil {
		return ShoppingSearchCanaryReport{}, err
	}
	defer source.Close()
	if sourceTx != nil {
		defer sourceTx.Rollback()
	}
	var sourcePages shoppingSearchQueryer = source
	if sourceTx != nil {
		sourcePages = sourceTx
	}

	report := ShoppingSearchCanaryReport{
		Format:                   ShoppingSearchCanaryFormat,
		Source:                   config.SourceSchema + "." + config.SourceTable,
		Target:                   config.TargetDatabase + "." + config.TargetTable,
		IndexKey:                 config.IndexKey,
		SourceConnectionLifetime: config.SourceConnectionLifetime,
	}
	if config.ReuseIndex {
		report.SourceConsistency = "existing-committed-projection"
	} else if config.ResilientSource {
		report.SourceConsistency = "ordered-read-committed-pages"
	} else {
		report.SourceConsistency = "repeatable-read-snapshot"
	}
	fingerprint := schema.Fingerprint()
	report.SchemaHash = hex.EncodeToString(fingerprint[:])
	var first ShoppingSearchBoundary
	var info search.IndexInfo
	if config.ReuseIndex {
		report.ReusedIndex = true
		first, err = readShoppingSearchBoundary(ctx, sourcePages, config, false)
		if err != nil {
			return ShoppingSearchCanaryReport{}, err
		}
		report.Boundary, err = readShoppingSearchBoundary(ctx, sourcePages, config, true)
		if err != nil {
			return ShoppingSearchCanaryReport{}, err
		}
		info, err = manager.Info(ctx, config.IndexKey, schema)
		if err != nil {
			return ShoppingSearchCanaryReport{}, fmt.Errorf("shopping search canary: open reused index: %w", err)
		}
		report.Rows = int64(info.Documents)
	} else {
		buildStarted := time.Now()
		replacement, beginErr := manager.BeginReplacement(ctx, config.IndexKey, schema)
		if beginErr != nil {
			return ShoppingSearchCanaryReport{}, fmt.Errorf(
				"shopping search canary: begin replacement: %w", beginErr,
			)
		}
		defer func() { returnErr = errors.Join(returnErr, replacement.Abort()) }()

		var cursor migrationCursor
		startedCursor := false
		for config.MaxRows == 0 || report.Rows < config.MaxRows {
			limit := config.PageRows
			if remaining := config.MaxRows - report.Rows; config.MaxRows > 0 && remaining < int64(limit) {
				limit = int(remaining)
			}
			page, pageErr := readSourceShoppingSearchPage(
				ctx, sourcePages, config, cursor, startedCursor, limit,
			)
			if pageErr != nil && config.ResilientSource && isTransientMigrationTargetError(pageErr) {
				page, pageErr = retrySourceShoppingSearchPage(
					ctx, source, config, cursor, startedCursor, limit,
				)
			}
			if pageErr != nil {
				return ShoppingSearchCanaryReport{}, pageErr
			}
			if len(page) == 0 {
				break
			}
			for _, row := range page {
				if addErr := replacement.Add(ctx, search.Document{
					ID: row.Key,
					Fields: map[string]string{
						"name": row.Name, "brand": row.Brand,
						"description": row.Description, "content": row.Content,
					},
				}); addErr != nil {
					return ShoppingSearchCanaryReport{}, fmt.Errorf(
						"shopping search canary: index %q: %w", row.Key, addErr,
					)
				}
				boundary := ShoppingSearchBoundary{Merchant: row.Merchant, ID: row.ID, Key: row.Key}
				if report.Rows == 0 {
					first = boundary
				}
				report.Boundary = boundary
				report.Rows++
			}
			report.Pages++
			last := page[len(page)-1]
			cursor = migrationCursor{Merchant: last.Merchant, ID: last.ID}
			startedCursor = true
			if config.Progress != nil && (report.Rows%5_000 < int64(len(page)) || len(page) < limit) {
				config.Progress(ShoppingSearchProgress{
					Rows: report.Rows, Pages: report.Pages, Elapsed: time.Since(buildStarted),
				})
			}
			if len(page) < limit {
				break
			}
		}
		if report.Rows == 0 {
			return ShoppingSearchCanaryReport{}, fmt.Errorf("shopping search canary: target table is empty")
		}
		info, err = replacement.Commit(ctx)
		if err != nil {
			return ShoppingSearchCanaryReport{}, fmt.Errorf("shopping search canary: publish replacement: %w", err)
		}
		report.BuildDuration = time.Since(buildStarted)
		if seconds := report.BuildDuration.Seconds(); seconds > 0 {
			report.RowsPerSecond = float64(report.Rows) / seconds
		}
	}
	report.Index = ShoppingSearchIndexReport{
		Generation: info.Generation, Segments: info.Segments,
		Documents: info.Documents, Bytes: info.Bytes,
		MaximumDocuments: config.SegmentRows, FlushThreshold: config.SegmentBytes,
		MaximumTermBytes: shoppingSearchTermBytes, SkipsLongTerms: true,
	}
	if info.Documents != uint64(report.Rows) {
		return ShoppingSearchCanaryReport{}, fmt.Errorf(
			"shopping search canary: index has %d documents, expected %d", info.Documents, report.Rows,
		)
	}
	if config.ExpectedRows > 0 && report.Rows != config.ExpectedRows {
		return ShoppingSearchCanaryReport{}, fmt.Errorf(
			"shopping search canary: index has %d documents, expected exactly %d",
			report.Rows, config.ExpectedRows,
		)
	}

	var benchmarkSource shoppingSearchPreparer = source
	if sourceTx != nil {
		benchmarkSource = sourceTx
	}
	queries := defaultShoppingSearchQueries()
	for _, query := range queries {
		name, nameIDs, err := benchmarkKitDBSearch(
			ctx, manager, config.IndexKey, schema, query, []string{"name"},
			config.Samples, config.LocalSearchBatch,
		)
		if err != nil {
			return ShoppingSearchCanaryReport{}, err
		}
		expanded, expandedIDs, err := benchmarkKitDBSearch(
			ctx, manager, config.IndexKey, schema, query,
			[]string{"name", "brand", "description", "content"},
			config.Samples, config.LocalSearchBatch,
		)
		if err != nil {
			return ShoppingSearchCanaryReport{}, err
		}
		postgres, postgresIDs, err := benchmarkPostgresSearch(
			ctx, benchmarkSource, config, report.Boundary, query, config.Samples,
		)
		if err != nil {
			return ShoppingSearchCanaryReport{}, err
		}
		report.Queries = append(report.Queries, ShoppingQueryComparison{
			Query: query, KitDBName: name, KitDBExpanded: expanded, PostgreSQL: postgres,
			NamePostgresOverlap: overlapCount(nameIDs, postgresIDs),
			ExpandedNameOverlap: overlapCount(expandedIDs, nameIDs),
			PostgreSQLScope: fmt.Sprintf(
				"source rows through (%s,%d) using text_search @@ plainto_tsquery('simple', query)",
				report.Boundary.Merchant, report.Boundary.ID,
			),
			KitDBProjectionScope: fmt.Sprintf(
				"%d ordered source rows in the migrated canary key range", report.Rows,
			),
		})
	}

	targetPoint, err := benchmarkTargetPointLookup(
		ctx, target, config.TargetTable, first.Key, config.Samples,
	)
	if err != nil {
		return ShoppingSearchCanaryReport{}, err
	}
	sourcePoint, err := benchmarkSourcePointLookup(
		ctx, benchmarkSource, config, first.Merchant, first.ID, config.Samples,
	)
	if err != nil {
		return ShoppingSearchCanaryReport{}, err
	}
	report.PointLookup = []ShoppingLatencyReport{targetPoint, sourcePoint}
	return report, nil
}

// ShoppingSearchSchema keeps legacy-compatible name search available while
// allowing callers to opt into richer cross-field retrieval.
func ShoppingSearchSchema() (search.Schema, error) {
	analyzer := search.VietnameseAnalyzer()
	return search.NewSchema(
		search.Text("name", analyzer, search.Boost(5)),
		search.Text("description", analyzer, search.Boost(2)),
		search.Text("content", analyzer),
		search.Text("brand", analyzer, search.Boost(2)),
	)
}

func normalizeShoppingSearchCanaryConfig(
	config ShoppingSearchCanaryConfig,
) (ShoppingSearchCanaryConfig, error) {
	config.SourceURL = strings.TrimSpace(config.SourceURL)
	config.SourceSchema = strings.TrimSpace(config.SourceSchema)
	config.SourceTable = strings.TrimSpace(config.SourceTable)
	config.TargetURL = strings.TrimSpace(config.TargetURL)
	config.TargetDatabase = strings.TrimSpace(config.TargetDatabase)
	config.TargetTable = strings.TrimSpace(config.TargetTable)
	config.SearchRoot = strings.TrimSpace(config.SearchRoot)
	config.IndexKey = strings.TrimSpace(config.IndexKey)
	if config.SourceSchema == "" {
		config.SourceSchema = defaultSchema
	}
	if config.SourceTable == "" {
		config.SourceTable = "shopping"
	}
	if config.TargetTable == "" {
		config.TargetTable = "shopping"
	}
	if config.IndexKey == "" {
		config.IndexKey = "shopping-canary"
	}
	if config.PageRows == 0 {
		config.PageRows = defaultShoppingSearchPage
	}
	if config.Samples == 0 {
		config.Samples = defaultSearchSamples
	}
	if config.LocalSearchBatch == 0 {
		config.LocalSearchBatch = localSearchTimingBatch
	}
	if config.SegmentRows == 0 {
		config.SegmentRows = defaultSearchSegmentRows
	}
	if config.SegmentBytes == 0 {
		config.SegmentBytes = defaultSearchSegmentBytes
	}
	if config.SourceConnectionLifetime == 0 {
		config.SourceConnectionLifetime = defaultSourceConnLifetime
	}
	if config.SourceURL == "" || config.TargetURL == "" || config.TargetDatabase == "" || config.SearchRoot == "" {
		return ShoppingSearchCanaryConfig{}, fmt.Errorf(
			"shopping search canary: source URL, target URL, target database and search root are required",
		)
	}
	if config.MaxRows < 0 {
		return ShoppingSearchCanaryConfig{}, fmt.Errorf("shopping search canary: max rows cannot be negative")
	}
	if config.ExpectedRows < 0 {
		return ShoppingSearchCanaryConfig{}, fmt.Errorf("shopping search canary: expected rows cannot be negative")
	}
	if config.ReuseIndex && config.MaxRows != 0 {
		return ShoppingSearchCanaryConfig{}, fmt.Errorf(
			"shopping search canary: reusing an index requires max rows zero",
		)
	}
	if config.PageRows < 1 || config.PageRows > maximumShoppingSearchPage {
		return ShoppingSearchCanaryConfig{}, fmt.Errorf(
			"shopping search canary: page rows must be between 1 and %d", maximumShoppingSearchPage,
		)
	}
	if config.Samples < 1 || config.Samples > maximumSearchSamples {
		return ShoppingSearchCanaryConfig{}, fmt.Errorf(
			"shopping search canary: samples must be between 1 and %d", maximumSearchSamples,
		)
	}
	if config.LocalSearchBatch < 1 || config.LocalSearchBatch > maximumLocalSearchBatch {
		return ShoppingSearchCanaryConfig{}, fmt.Errorf(
			"shopping search canary: local search batch must be between 1 and %d",
			maximumLocalSearchBatch,
		)
	}
	if config.SegmentRows < 1 || uint64(config.SegmentRows) > math.MaxUint32 {
		return ShoppingSearchCanaryConfig{}, fmt.Errorf(
			"shopping search canary: segment rows must be between 1 and %d", uint64(math.MaxUint32),
		)
	}
	if config.SegmentBytes < 1 || config.SegmentBytes > maximumSearchSegmentBytes {
		return ShoppingSearchCanaryConfig{}, fmt.Errorf(
			"shopping search canary: segment bytes must be between 1 and %d", maximumSearchSegmentBytes,
		)
	}
	if config.SourceConnectionLifetime < minimumSourceConnLifetime ||
		config.SourceConnectionLifetime > maximumSourceConnLifetime {
		return ShoppingSearchCanaryConfig{}, fmt.Errorf(
			"shopping search canary: source connection lifetime must be between %s and %s",
			minimumSourceConnLifetime, maximumSourceConnLifetime,
		)
	}
	return config, nil
}

type shoppingSearchQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type shoppingSearchPreparer interface {
	PrepareContext(context.Context, string) (*sql.Stmt, error)
}

func readShoppingSearchBoundary(
	ctx context.Context,
	queryer shoppingSearchQueryer,
	config ShoppingSearchCanaryConfig,
	last bool,
) (ShoppingSearchBoundary, error) {
	direction := "ASC"
	if last {
		direction = "DESC"
	}
	query := fmt.Sprintf(
		`SELECT "merchant", "id" FROM %s.%s ORDER BY "merchant" %s, "id" %s LIMIT 1`,
		quoteIdentifier(config.SourceSchema), quoteIdentifier(config.SourceTable), direction, direction,
	)
	rows, err := queryer.QueryContext(ctx, query)
	if err != nil {
		return ShoppingSearchBoundary{}, sourceError(config.SourceURL, "read search projection boundary", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return ShoppingSearchBoundary{}, sourceError(config.SourceURL, "read search projection boundary", err)
		}
		return ShoppingSearchBoundary{}, fmt.Errorf("shopping search canary: source table is empty")
	}
	var boundary ShoppingSearchBoundary
	if err := rows.Scan(&boundary.Merchant, &boundary.ID); err != nil {
		return ShoppingSearchBoundary{}, sourceError(config.SourceURL, "decode search projection boundary", err)
	}
	boundary.Key = syntheticShoppingKey(boundary.Merchant, boundary.ID)
	return boundary, nil
}

func readSourceShoppingSearchPage(
	ctx context.Context,
	queryer shoppingSearchQueryer,
	config ShoppingSearchCanaryConfig,
	cursor migrationCursor,
	started bool,
	limit int,
) ([]shoppingSearchRow, error) {
	query := fmt.Sprintf(
		`SELECT "merchant", "id", "name", "brand", "description", "content" `+
			`FROM %s.%s WHERE NOT $1::boolean OR ("merchant", "id") > ($2::text, $3::bigint) `+
			`ORDER BY "merchant", "id" LIMIT $4`,
		quoteIdentifier(config.SourceSchema), quoteIdentifier(config.SourceTable),
	)
	rows, err := queryer.QueryContext(ctx, query, started, cursor.Merchant, cursor.ID, limit)
	if err != nil {
		return nil, sourceError(config.SourceURL, "read search projection page", err)
	}
	defer rows.Close()
	page := make([]shoppingSearchRow, 0, limit)
	for rows.Next() {
		var row shoppingSearchRow
		if err := rows.Scan(
			&row.Merchant, &row.ID, &row.Name,
			&row.Brand, &row.Description, &row.Content,
		); err != nil {
			return nil, sourceError(config.SourceURL, "decode search projection row", err)
		}
		row.Key = syntheticShoppingKey(row.Merchant, row.ID)
		page = append(page, row)
	}
	if err := rows.Err(); err != nil {
		return nil, sourceError(config.SourceURL, "iterate search projection page", err)
	}
	return page, nil
}

func retrySourceShoppingSearchPage(
	ctx context.Context,
	database *sql.DB,
	config ShoppingSearchCanaryConfig,
	cursor migrationCursor,
	started bool,
	limit int,
) ([]shoppingSearchRow, error) {
	var lastErr error
	for attempt := 1; attempt <= 12; attempt++ {
		if err := waitMigrationRetry(ctx, attempt); err != nil {
			return nil, err
		}
		page, err := readSourceShoppingSearchPage(ctx, database, config, cursor, started, limit)
		if err == nil {
			return page, nil
		}
		lastErr = err
		if !isTransientMigrationTargetError(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("shopping search canary: source page retries exhausted: %w", lastErr)
}

func openShoppingSearchSource(
	ctx context.Context,
	config ShoppingSearchCanaryConfig,
) (*sql.DB, *sql.Tx, error) {
	database, err := sql.Open("postgres", config.SourceURL)
	if err != nil {
		return nil, nil, sourceError(config.SourceURL, "initialize search comparison", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	database.SetConnMaxLifetime(config.SourceConnectionLifetime)
	database.SetConnMaxIdleTime(5 * time.Minute)
	if err := database.PingContext(ctx); err != nil {
		database.Close()
		return nil, nil, sourceError(config.SourceURL, "connect search comparison", err)
	}
	if config.ResilientSource {
		return database, nil, nil
	}
	tx, err := beginShoppingSearchBenchmark(ctx, database, config)
	if err != nil {
		database.Close()
		return nil, nil, err
	}
	return database, tx, nil
}

func beginShoppingSearchBenchmark(
	ctx context.Context,
	database *sql.DB,
	config ShoppingSearchCanaryConfig,
) (*sql.Tx, error) {
	tx, err := database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, sourceError(config.SourceURL, "begin search comparison snapshot", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('application_name', 'kitdbmigrate-search-canary', true)`); err != nil {
		tx.Rollback()
		return nil, sourceError(config.SourceURL, "identify search comparison", err)
	}
	return tx, nil
}

func benchmarkKitDBSearch(
	ctx context.Context,
	manager *search.Manager,
	indexKey string,
	schema search.Schema,
	query string,
	fields []string,
	samples int,
	batch int,
) (ShoppingLatencyReport, []string, error) {
	match := search.MatchQuery{Fields: fields, Text: query}
	operation := func() ([]string, error) {
		hits, err := manager.Search(ctx, indexKey, schema, match, search.SearchOptions{Limit: 20})
		if err != nil {
			return nil, err
		}
		identifiers := make([]string, len(hits))
		for position, hit := range hits {
			identifiers[position] = hit.ID
		}
		return identifiers, nil
	}
	return benchmarkIdentifierOperation(
		"kitdb-search:"+strings.Join(fields, "+"), samples, batch, operation,
	)
}

func benchmarkPostgresSearch(
	ctx context.Context,
	source shoppingSearchPreparer,
	config ShoppingSearchCanaryConfig,
	boundary ShoppingSearchBoundary,
	queryText string,
	samples int,
) (ShoppingLatencyReport, []string, error) {
	query := fmt.Sprintf(`
		WITH query AS (SELECT plainto_tsquery('simple', $3) AS value)
		SELECT source."merchant", source."id"
		FROM %s.%s AS source CROSS JOIN query
		WHERE (source."merchant", source."id") <= ($1::text, $2::bigint)
			AND source."text_search" @@ query.value
		ORDER BY ts_rank_cd(source."text_search", query.value) DESC,
			source."merchant", source."id"
		LIMIT 20
	`, quoteIdentifier(config.SourceSchema), quoteIdentifier(config.SourceTable))
	statement, err := source.PrepareContext(ctx, query)
	if err != nil {
		return ShoppingLatencyReport{}, nil, sourceError(config.SourceURL, "prepare PostgreSQL search", err)
	}
	defer statement.Close()
	operation := func() ([]string, error) {
		rows, err := statement.QueryContext(ctx, boundary.Merchant, boundary.ID, queryText)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		identifiers := make([]string, 0, 20)
		for rows.Next() {
			var merchant string
			var identifier int64
			if err := rows.Scan(&merchant, &identifier); err != nil {
				return nil, err
			}
			identifiers = append(identifiers, syntheticShoppingKey(merchant, identifier))
		}
		return identifiers, rows.Err()
	}
	report, identifiers, err := benchmarkIdentifierOperation("postgresql-gin:text_search", samples, 1, operation)
	if err != nil {
		return ShoppingLatencyReport{}, nil, sourceError(config.SourceURL, "benchmark PostgreSQL search", err)
	}
	return report, identifiers, nil
}

func benchmarkTargetPointLookup(
	ctx context.Context,
	database *sql.DB,
	table, key string,
	samples int,
) (ShoppingLatencyReport, error) {
	statement, err := database.PrepareContext(ctx, fmt.Sprintf(
		`SELECT "_key" FROM %s WHERE "_key" = $1 LIMIT 1`, quoteIdentifier(table),
	))
	if err != nil {
		return ShoppingLatencyReport{}, err
	}
	defer statement.Close()
	operation := func() ([]string, error) {
		var found string
		if err := statement.QueryRowContext(ctx, key).Scan(&found); err != nil {
			return nil, err
		}
		return []string{found}, nil
	}
	report, _, err := benchmarkIdentifierOperation("kitdb-primary", samples, 1, operation)
	if err != nil {
		return ShoppingLatencyReport{}, fmt.Errorf("shopping search canary: benchmark KitDB point lookup: %w", err)
	}
	report.VerifiedKey = key
	return report, nil
}

func benchmarkSourcePointLookup(
	ctx context.Context,
	source shoppingSearchPreparer,
	config ShoppingSearchCanaryConfig,
	merchant string,
	identifier int64,
	samples int,
) (ShoppingLatencyReport, error) {
	statement, err := source.PrepareContext(ctx, fmt.Sprintf(
		`SELECT "merchant", "id" FROM %s.%s WHERE "merchant" = $1 AND "id" = $2`,
		quoteIdentifier(config.SourceSchema), quoteIdentifier(config.SourceTable),
	))
	if err != nil {
		return ShoppingLatencyReport{}, sourceError(config.SourceURL, "prepare PostgreSQL point lookup", err)
	}
	defer statement.Close()
	key := syntheticShoppingKey(merchant, identifier)
	operation := func() ([]string, error) {
		var foundMerchant string
		var foundID int64
		if err := statement.QueryRowContext(ctx, merchant, identifier).Scan(&foundMerchant, &foundID); err != nil {
			return nil, err
		}
		return []string{syntheticShoppingKey(foundMerchant, foundID)}, nil
	}
	report, _, err := benchmarkIdentifierOperation("postgresql-composite-primary", samples, 1, operation)
	if err != nil {
		return ShoppingLatencyReport{}, sourceError(config.SourceURL, "benchmark PostgreSQL point lookup", err)
	}
	report.VerifiedKey = key
	return report, nil
}

func benchmarkIdentifierOperation(
	engine string,
	samples int,
	batch int,
	operation func() ([]string, error),
) (ShoppingLatencyReport, []string, error) {
	if batch < 1 {
		return ShoppingLatencyReport{}, nil, fmt.Errorf("%s has an invalid timing batch", engine)
	}
	identifiers, err := operation()
	if err != nil {
		return ShoppingLatencyReport{}, nil, err
	}
	durations := make([]time.Duration, samples)
	for sample := range samples {
		started := time.Now()
		for range batch {
			current, err := operation()
			if err != nil {
				return ShoppingLatencyReport{}, nil, err
			}
			if !equalIdentifiers(identifiers, current) {
				return ShoppingLatencyReport{}, nil, fmt.Errorf("%s returned unstable ordered identifiers", engine)
			}
		}
		durations[sample] = time.Since(started) / time.Duration(batch)
	}
	sort.Slice(durations, func(left, right int) bool { return durations[left] < durations[right] })
	report := ShoppingLatencyReport{
		Engine: engine, Samples: samples, Batch: batch, Hits: len(identifiers),
		P50Micros: durationMicros(percentileDuration(durations, 50)),
		P95Micros: durationMicros(percentileDuration(durations, 95)),
		MaxMicros: durationMicros(durations[len(durations)-1]),
		TopIDs:    append([]string(nil), identifiers[:min(5, len(identifiers))]...),
	}
	return report, identifiers, nil
}

func percentileDuration(sorted []time.Duration, percentile int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	position := int(math.Ceil(float64(percentile)*float64(len(sorted))/100)) - 1
	return sorted[max(0, min(position, len(sorted)-1))]
}

func durationMicros(duration time.Duration) int64 {
	return duration.Microseconds()
}

func equalIdentifiers(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for position := range left {
		if left[position] != right[position] {
			return false
		}
	}
	return true
}

func overlapCount(left, right []string) int {
	seen := make(map[string]struct{}, len(right))
	for _, identifier := range right {
		seen[identifier] = struct{}{}
	}
	overlap := 0
	for _, identifier := range left {
		if _, exists := seen[identifier]; exists {
			overlap++
		}
	}
	return overlap
}

func defaultShoppingSearchQueries() []string {
	return []string{
		"bàn phím logitech",
		"ban phim logitech",
		"cân sức khỏe",
		"can suc khoe",
		"usb wifi tp link",
		"bếp gas hồng ngoại",
		"iphone 15 pro max",
		"samsung galaxy",
		"máy lọc không khí",
		"ao thun nam",
		"son môi",
		"sữa rửa mặt",
	}
}
