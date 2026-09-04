package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	TargetPlanFormat       = "kitdb-postgres-target-plan/v2"
	legacyTargetPlanFormat = "kitdb-postgres-target-plan/v1"
)

const migrationStateTable = "_kitdb_migration"

type TargetPlan struct {
	Format                string   `json:"format"`
	SourceDatabase        string   `json:"source_database"`
	SourceSchema          string   `json:"source_schema"`
	SourceTable           string   `json:"source_table"`
	TargetTable           string   `json:"target_table"`
	SchemaFingerprint     string   `json:"schema_fingerprint"`
	PrimaryStrategy       string   `json:"primary_strategy"`
	SyntheticPrimary      string   `json:"synthetic_primary,omitempty"`
	SourcePrimary         []string `json:"source_primary"`
	SourceColumns         []string `json:"source_columns"`
	TargetColumns         []string `json:"target_columns"`
	DerivedColumns        []string `json:"derived_columns,omitempty"`
	CreateTableSQL        string   `json:"create_table_sql"`
	CreateStateSQL        string   `json:"create_state_sql"`
	DeferredIndexes       []string `json:"deferred_indexes,omitempty"`
	SearchRequiresRebuild bool     `json:"search_requires_rebuild"`
	Warnings              []string `json:"warnings,omitempty"`
}

type TargetPrepareReport struct {
	Database        string     `json:"database"`
	DatabaseCreated bool       `json:"database_created"`
	Statements      int        `json:"statements"`
	Plan            TargetPlan `json:"plan"`
}

func BuildTargetPlan(source SchemaReport) (TargetPlan, error) {
	if source.Format != SchemaReportFormat {
		return TargetPlan{}, fmt.Errorf("postgres target plan: unsupported source schema format %q", source.Format)
	}
	if len(source.Blockers) > 0 {
		return TargetPlan{}, fmt.Errorf("postgres target plan: source schema has unresolved blockers: %s", strings.Join(source.Blockers, "; "))
	}
	if len(source.PrimaryKey) == 0 {
		return TargetPlan{}, fmt.Errorf("postgres target plan: source table has no primary key")
	}

	plan := TargetPlan{
		Format: TargetPlanFormat, SourceDatabase: source.Database,
		SourceSchema: source.Schema, SourceTable: source.Table, TargetTable: source.Table,
		SourcePrimary:         append([]string(nil), source.PrimaryKey...),
		SearchRequiresRebuild: source.FullText.RequiresRebuild,
	}
	if len(source.PrimaryKey) == 1 {
		plan.PrimaryStrategy = "source"
	} else {
		plan.PrimaryStrategy = "native-composite"
	}

	definitions := make([]string, 0, len(source.Columns)+1)
	for _, column := range source.Columns {
		if column.Mapping == "derived" {
			plan.DerivedColumns = append(plan.DerivedColumns, column.Name)
			continue
		}
		if column.Mapping == "unsupported" || column.TargetKind == "" {
			return TargetPlan{}, fmt.Errorf("postgres target plan: column %q has no target mapping", column.Name)
		}
		definition := quoteIdentifier(column.Name) + " " + strings.ToUpper(column.TargetKind)
		if len(source.PrimaryKey) == 1 && column.Name == source.PrimaryKey[0] {
			definition += " PRIMARY KEY"
		} else if containsString(source.PrimaryKey, column.Name) || !column.Nullable {
			definition += " NOT NULL"
		}
		definitions = append(definitions, definition)
		plan.SourceColumns = append(plan.SourceColumns, column.Name)
		plan.TargetColumns = append(plan.TargetColumns, column.Name)
	}
	if len(source.PrimaryKey) > 1 {
		primary := make([]string, len(source.PrimaryKey))
		for position, column := range source.PrimaryKey {
			primary[position] = quoteIdentifier(column)
		}
		definitions = append(definitions, "PRIMARY KEY ("+strings.Join(primary, ", ")+")")
	}
	plan.CreateTableSQL = fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (%s)",
		quoteIdentifier(plan.TargetTable), strings.Join(definitions, ", "),
	)
	plan.CreateStateSQL = `CREATE TABLE IF NOT EXISTS "` + migrationStateTable + `" (` +
		`"id" TEXT PRIMARY KEY, ` +
		`"source" TEXT NOT NULL, ` +
		`"schema_fingerprint" TEXT NOT NULL, ` +
		`"cursor" TEXT NOT NULL, ` +
		`"rows" BIGINT NOT NULL, ` +
		`"checksum" TEXT NOT NULL, ` +
		`"complete" BOOL NOT NULL, ` +
		`"updated_at" DATETIME NOT NULL)`

	for _, index := range source.Indexes {
		if index.Primary || index.FullText || index.Method != "btree" {
			continue
		}
		name := index.Name
		columns := make([]string, len(index.Columns))
		for position, column := range index.Columns {
			columns[position] = quoteIdentifier(column)
		}
		prefix := "CREATE INDEX"
		if index.Unique {
			prefix = "CREATE UNIQUE INDEX"
		}
		plan.DeferredIndexes = append(plan.DeferredIndexes, fmt.Sprintf(
			"%s %s ON %s (%s)", prefix, quoteIdentifier(name),
			quoteIdentifier(source.Table), strings.Join(columns, ", "),
		))
	}

	fingerprintInput := struct {
		Database    string
		Schema      string
		Table       string
		Columns     []ColumnReport
		Primary     []string
		Constraints []ConstraintReport
		Indexes     []IndexReport
	}{
		Database: source.Database, Schema: source.Schema, Table: source.Table,
		Columns: source.Columns, Primary: source.PrimaryKey,
		Constraints: source.Constraints, Indexes: source.Indexes,
	}
	encoded, err := json.Marshal(fingerprintInput)
	if err != nil {
		return TargetPlan{}, fmt.Errorf("postgres target plan: encode schema fingerprint: %w", err)
	}
	digest := sha256.Sum256(encoded)
	plan.SchemaFingerprint = hex.EncodeToString(digest[:])
	return plan, nil
}

func containsString(items []string, requested string) bool {
	for _, item := range items {
		if item == requested {
			return true
		}
	}
	return false
}

func PrepareTarget(
	ctx context.Context,
	targetURL string,
	databaseName string,
	capability string,
	plan TargetPlan,
) (TargetPrepareReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	databaseName = strings.TrimSpace(databaseName)
	capability = strings.TrimSpace(capability)
	if databaseName == "" || capability == "" {
		return TargetPrepareReport{}, fmt.Errorf("postgres target: database and capability are required")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	maintenance, err := openTarget(ctx, targetURL)
	if err != nil {
		return TargetPrepareReport{}, err
	}
	created := false
	exists := false
	rows, err := maintenance.QueryContext(ctx, `SELECT datname FROM pg_database`)
	if err != nil {
		maintenance.Close()
		return TargetPrepareReport{}, fmt.Errorf("postgres target: inspect databases: %w", err)
	}
	for rows.Next() {
		var candidate string
		if err := rows.Scan(&candidate); err != nil {
			rows.Close()
			maintenance.Close()
			return TargetPrepareReport{}, fmt.Errorf("postgres target: inspect database row: %w", err)
		}
		if candidate == databaseName {
			exists = true
		}
	}
	if err := rows.Close(); err != nil {
		maintenance.Close()
		return TargetPrepareReport{}, fmt.Errorf("postgres target: close database catalog: %w", err)
	}
	if err := rows.Err(); err != nil {
		maintenance.Close()
		return TargetPrepareReport{}, fmt.Errorf("postgres target: inspect database catalog: %w", err)
	}
	if !exists {
		statement := "CREATE DATABASE " + quoteIdentifier(databaseName) +
			" WITH CAPABILITY " + quoteIdentifier(capability)
		if _, err := maintenance.ExecContext(ctx, statement); err != nil {
			maintenance.Close()
			return TargetPrepareReport{}, fmt.Errorf("postgres target: create database: %w", err)
		}
		created = true
	}
	if err := maintenance.Close(); err != nil {
		return TargetPrepareReport{}, fmt.Errorf("postgres target: close maintenance connection: %w", err)
	}

	databaseURL, err := URLWithDatabase(targetURL, databaseName)
	if err != nil {
		return TargetPrepareReport{}, err
	}
	target, err := openTarget(ctx, databaseURL)
	if err != nil {
		return TargetPrepareReport{}, err
	}
	defer target.Close()
	statements := []string{plan.CreateTableSQL, plan.CreateStateSQL}
	for _, statement := range statements {
		if _, err := target.ExecContext(ctx, statement); err != nil {
			return TargetPrepareReport{}, fmt.Errorf("postgres target: apply schema: %w", err)
		}
	}
	return TargetPrepareReport{
		Database: databaseName, DatabaseCreated: created,
		Statements: len(statements), Plan: plan,
	}, nil
}

func openTarget(ctx context.Context, targetURL string) (*sql.DB, error) {
	database, err := sql.Open("postgres", targetURL)
	if err != nil {
		return nil, fmt.Errorf("postgres target: initialize driver: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	if err := database.PingContext(ctx); err != nil {
		database.Close()
		return nil, fmt.Errorf("postgres target: connect: %w", err)
	}
	return database, nil
}

func URLWithDatabase(dsn, database string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("postgres target: URL cannot accept a database override")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "postgres", "postgresql":
	default:
		return "", fmt.Errorf("postgres target: URL cannot accept a database override")
	}
	if strings.TrimSpace(database) == "" || strings.IndexByte(database, 0) >= 0 {
		return "", fmt.Errorf("postgres target: invalid database override")
	}
	parsed.Path = "/" + strings.TrimSpace(database)
	parsed.RawPath = ""
	return parsed.String(), nil
}

func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
