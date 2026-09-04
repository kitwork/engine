package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

const (
	ShoppingFullTextProfileFormat = "kitdb-postgres-shopping-fts-profile/v1"
	defaultFullTextProfileRows    = 8
	maximumFullTextProfileRows    = 32
	fullTextVectorPreviewBytes    = 768
)

// ShoppingFullTextProfile is a bounded sample of the source application's
// persisted tsvectors. It is evidence for projection design, not a data export.
type ShoppingFullTextProfile struct {
	Format   string                       `json:"format"`
	Database string                       `json:"database"`
	Schema   string                       `json:"schema"`
	Table    string                       `json:"table"`
	Snapshot string                       `json:"snapshot"`
	Rows     []ShoppingFullTextProfileRow `json:"rows"`
}

type ShoppingFullTextProfileRow struct {
	Merchant         string `json:"merchant"`
	ID               int64  `json:"id,string"`
	Name             string `json:"name"`
	Brand            string `json:"brand"`
	Categories       string `json:"categories"`
	DescriptionBytes int64  `json:"description_bytes"`
	ContentBytes     int64  `json:"content_bytes"`
	TextSearchTerms  int64  `json:"text_search_terms"`
	TextSearchBytes  int64  `json:"text_search_bytes"`
	KeywordTerms     int64  `json:"keyword_terms"`
	KeywordBytes     int64  `json:"keyword_bytes"`
	TextSearch       string `json:"text_search_preview"`
	Keyword          string `json:"keyword_preview"`
}

// ProfileShoppingFullText reads a small key-ordered sample through one
// repeatable-read, read-only PostgreSQL transaction. The fixed field set is
// deliberate: the initial migration profile is specific to shopping.
func ProfileShoppingFullText(
	ctx context.Context,
	config Config,
	table string,
	limit int,
) (ShoppingFullTextProfile, error) {
	config, err := normalizeConfig(config)
	if err != nil {
		return ShoppingFullTextProfile{}, err
	}
	table = strings.TrimSpace(table)
	if table == "" || strings.IndexByte(table, 0) >= 0 {
		return ShoppingFullTextProfile{}, fmt.Errorf("postgres full-text profile: invalid table")
	}
	if limit == 0 {
		limit = defaultFullTextProfileRows
	}
	if limit < 1 || limit > maximumFullTextProfileRows {
		return ShoppingFullTextProfile{}, fmt.Errorf(
			"postgres full-text profile: rows must be between 1 and %d",
			maximumFullTextProfileRows,
		)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	database, err := sql.Open("postgres", config.URL)
	if err != nil {
		return ShoppingFullTextProfile{}, sourceError(config.URL, "initialize full-text profile", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(0)
	database.SetConnMaxLifetime(config.Timeout)
	defer database.Close()
	if err := database.PingContext(ctx); err != nil {
		return ShoppingFullTextProfile{}, sourceError(config.URL, "connect full-text profile", err)
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return ShoppingFullTextProfile{}, sourceError(config.URL, "begin full-text profile snapshot", err)
	}
	defer tx.Rollback()

	statementTimeout := strconv.FormatInt(config.Timeout.Milliseconds(), 10) + "ms"
	if _, err := tx.ExecContext(ctx, `SELECT set_config('statement_timeout', $1, true)`, statementTimeout); err != nil {
		return ShoppingFullTextProfile{}, sourceError(config.URL, "bound full-text profile", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('application_name', 'kitdbmigrate-fts-profile', true)`); err != nil {
		return ShoppingFullTextProfile{}, sourceError(config.URL, "identify full-text profile", err)
	}

	profile := ShoppingFullTextProfile{
		Format: ShoppingFullTextProfileFormat,
		Schema: config.Schema,
		Table:  table,
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT current_database()::text, txid_current_snapshot()::text`,
	).Scan(&profile.Database, &profile.Snapshot); err != nil {
		return ShoppingFullTextProfile{}, sourceError(config.URL, "identify full-text profile snapshot", err)
	}

	query := shoppingFullTextProfileQuery(config.Schema, table)
	rows, err := tx.QueryContext(ctx, query, limit, fullTextVectorPreviewBytes)
	if err != nil {
		return ShoppingFullTextProfile{}, sourceError(config.URL, "sample full-text vectors", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row ShoppingFullTextProfileRow
		if err := rows.Scan(
			&row.Merchant,
			&row.ID,
			&row.Name,
			&row.Brand,
			&row.Categories,
			&row.DescriptionBytes,
			&row.ContentBytes,
			&row.TextSearchTerms,
			&row.TextSearchBytes,
			&row.KeywordTerms,
			&row.KeywordBytes,
			&row.TextSearch,
			&row.Keyword,
		); err != nil {
			return ShoppingFullTextProfile{}, sourceError(config.URL, "decode full-text sample", err)
		}
		profile.Rows = append(profile.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return ShoppingFullTextProfile{}, sourceError(config.URL, "iterate full-text samples", err)
	}
	return profile, nil
}

func shoppingFullTextProfileQuery(schema, table string) string {
	return fmt.Sprintf(`
		SELECT
			"merchant",
			"id",
			left("name", 240),
			left("brand", 120),
			left(COALESCE(array_to_string("categories", ' '), ''), 240),
			octet_length("description"),
			octet_length("content"),
			COALESCE(length("text_search"), 0),
			COALESCE(octet_length("text_search"::text), 0),
			COALESCE(length("keyword"), 0),
			COALESCE(octet_length("keyword"::text), 0),
			left(COALESCE("text_search"::text, ''), $2),
			left(COALESCE("keyword"::text, ''), $2)
		FROM %s.%s
		ORDER BY "merchant", "id"
		LIMIT $1
	`, quoteIdentifier(schema), quoteIdentifier(table))
}
