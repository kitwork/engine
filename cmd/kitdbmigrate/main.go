// Command kitdbmigrate provides bounded, resumable migration tooling around
// KitDB. The initial preflight command inspects PostgreSQL without reading user
// rows or accepting a source secret on the command line.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	postgresmigrate "github.com/kitwork/engine/kitdb/migrate/postgres"
)

const defaultSourceEnv = "KITDB_MIGRATE_PG_URL"

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "kitdbmigrate: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	if ctx == nil {
		return fmt.Errorf("nil command context")
	}
	if output == nil {
		return fmt.Errorf("nil command output")
	}
	if len(args) == 0 {
		return usageError()
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "preflight":
		return runPreflight(ctx, args[1:], output)
	case "plan":
		return runPlan(ctx, args[1:], output)
	case "fts-profile":
		return runFullTextProfile(ctx, args[1:], output)
	case "search-canary":
		return runSearchCanary(ctx, args[1:], output)
	case "search-identity":
		return runSearchIdentity(args[1:], output)
	case "search-adopt":
		return runSearchAdopt(ctx, args[1:], output)
	case "prepare":
		return runPrepare(ctx, args[1:], output)
	case "migrate":
		return runMigrate(ctx, args[1:], output)
	case "verify":
		return runVerifyTarget(ctx, args[1:], output)
	case "help", "-h", "--help":
		return usageError()
	default:
		return fmt.Errorf("unknown command %q; %w", args[0], usageError())
	}
}

func runSearchAdopt(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("search-adopt", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	sourceRoot := flags.String("source-root", "", "root containing the completed offline search index")
	sourceIndexKey := flags.String("source-index-key", "", "logical key used while building the offline index")
	tenantRoot := flags.String("tenant-root", "", "resolved Kitwork tenant directory")
	storageName := flags.String("storage", "", "offline KitDB storage filename")
	table := flags.String("table", "shopping", "searchable table")
	expectedDocuments := flags.Uint64("expected-documents", 0, "required live document count")
	skipVerify := flags.Bool("skip-verify", false, "skip full physical index verification")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("search-adopt accepts flags only")
	}
	report, err := postgresmigrate.AdoptShoppingSearchProjection(ctx, postgresmigrate.ShoppingSearchAdoptConfig{
		SourceRoot: *sourceRoot, SourceIndexKey: *sourceIndexKey,
		TenantRoot: *tenantRoot, StorageName: *storageName, Table: *table,
		ExpectedDocuments: *expectedDocuments, SkipVerify: *skipVerify,
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func runSearchIdentity(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("search-identity", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	tenantRoot := flags.String("tenant-root", "", "resolved Kitwork tenant directory")
	storageName := flags.String("storage", "", "KitDB storage filename")
	table := flags.String("table", "shopping", "searchable table")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("search-identity accepts flags only")
	}
	identity, err := postgresmigrate.ResolveShoppingSearchProjectionIdentity(
		*tenantRoot, *storageName, *table,
	)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(identity)
}

func runSearchCanary(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("search-canary", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	sourceEnvFile := flags.String("source-env-file", "", "dotenv file containing the PostgreSQL source URL")
	sourceEnv := flags.String("source-env", defaultSourceEnv, "environment variable containing the PostgreSQL source URL")
	sourceDatabase := flags.String("source-database", "", "override the source database name")
	sourceSchema := flags.String("source-schema", "public", "PostgreSQL source schema")
	sourceTable := flags.String("source-table", "shopping", "PostgreSQL source table")
	targetEnvFile := flags.String("target-env-file", "", "dotenv file containing the KitDB target URL")
	targetEnv := flags.String("target-env", "KITDB_MIGRATE_TARGET_URL", "environment variable containing the KitDB target URL")
	targetDatabase := flags.String("target-database", "", "logical KitDB target database")
	targetTable := flags.String("target-table", "shopping", "KitDB target table")
	searchRoot := flags.String("search-root", "", "durable root directory for the search projection")
	indexKey := flags.String("index-key", "shopping-canary", "logical search index key")
	maxRows := flags.Int64("max-rows", 100_000, "maximum migrated target rows to index; zero means all")
	expectedRows := flags.Int64("expected-rows", 0, "optional exact document count required from a reused or newly built index")
	pageRows := flags.Int("page-rows", 2_048, "rows fetched from PostgreSQL per bounded page")
	samples := flags.Int("samples", 20, "timed repetitions per query")
	localSearchBatch := flags.Int("local-search-batch", 32, "search operations averaged inside each local timing sample")
	segmentRows := flags.Int("segment-rows", 250_000, "maximum documents retained by one bounded search segment builder")
	segmentBytes := flags.Int64("segment-bytes", 512<<20, "soft byte accounting threshold for one search segment builder")
	resilientSource := flags.Bool("resilient-source", false, "use reconnectable ordered pages instead of one long source snapshot")
	reuseIndex := flags.Bool("reuse-index", false, "benchmark an already committed full projection without rebuilding it")
	sourceConnectionLifetime := flags.Duration("source-connection-lifetime", 15*time.Minute, "maximum lifetime of one PostgreSQL source connection")
	reportFile := flags.String("report-file", "", "optional JSON report path")
	timeout := flags.Duration("timeout", 20*time.Minute, "whole search canary timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*targetDatabase) == "" || strings.TrimSpace(*searchRoot) == "" {
		return fmt.Errorf("search-canary requires -target-database and -search-root")
	}
	sourceDSN, err := resolveSourceDSN(*sourceEnvFile, strings.TrimSpace(*sourceEnv), *sourceDatabase)
	if err != nil {
		return err
	}
	targetDSN, err := resolveEnvDSN(*targetEnvFile, strings.TrimSpace(*targetEnv))
	if err != nil {
		return err
	}
	canaryContext := ctx
	var cancel context.CancelFunc
	if *timeout > 0 {
		canaryContext, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}
	report, err := postgresmigrate.RunShoppingSearchCanary(
		canaryContext,
		postgresmigrate.ShoppingSearchCanaryConfig{
			SourceURL: sourceDSN, SourceSchema: *sourceSchema, SourceTable: *sourceTable,
			TargetURL: targetDSN, TargetDatabase: *targetDatabase, TargetTable: *targetTable,
			SearchRoot: *searchRoot, IndexKey: *indexKey, MaxRows: *maxRows,
			ExpectedRows: *expectedRows, PageRows: *pageRows, Samples: *samples,
			LocalSearchBatch: *localSearchBatch,
			SegmentRows:      *segmentRows, SegmentBytes: *segmentBytes,
			ResilientSource:          *resilientSource,
			ReuseIndex:               *reuseIndex,
			SourceConnectionLifetime: *sourceConnectionLifetime,
			Progress: func(progress postgresmigrate.ShoppingSearchProgress) {
				fmt.Fprintf(
					os.Stderr, "kitdbmigrate: search rows=%d pages=%d elapsed=%s\n",
					progress.Rows, progress.Pages, progress.Elapsed.Round(time.Millisecond),
				)
			},
		},
	)
	if err != nil {
		return err
	}
	if path := strings.TrimSpace(*reportFile); path != "" {
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("encode search canary report: %w", err)
		}
		encoded = append(encoded, '\n')
		if err := os.WriteFile(path, encoded, 0o600); err != nil {
			return fmt.Errorf("write search canary report: %w", err)
		}
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func runFullTextProfile(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("fts-profile", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	envFile := flags.String("env-file", "", "dotenv file containing the PostgreSQL source URL")
	sourceEnv := flags.String("source-env", defaultSourceEnv, "environment variable containing the PostgreSQL source URL")
	databaseName := flags.String("database", "", "override the database name from the source URL")
	schema := flags.String("schema", "public", "PostgreSQL source schema")
	table := flags.String("table", "shopping", "PostgreSQL shopping table")
	rows := flags.Int("rows", 8, "bounded number of source rows to sample")
	timeout := flags.Duration("timeout", 30*time.Second, "whole profile timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("fts-profile accepts flags only")
	}
	dsn, err := resolveSourceDSN(*envFile, strings.TrimSpace(*sourceEnv), *databaseName)
	if err != nil {
		return err
	}
	profile, err := postgresmigrate.ProfileShoppingFullText(ctx, postgresmigrate.Config{
		URL: dsn, Schema: *schema, Timeout: *timeout,
	}, *table, *rows)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(profile)
}

func runPreflight(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("preflight", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	envFile := flags.String("env-file", "", "dotenv file containing the PostgreSQL source URL")
	sourceEnv := flags.String("source-env", defaultSourceEnv, "environment variable containing the PostgreSQL source URL")
	databaseName := flags.String("database", "", "override the database name from the source URL")
	schema := flags.String("schema", "public", "PostgreSQL schema to inspect")
	timeout := flags.Duration("timeout", 30*time.Second, "whole preflight timeout")
	maxTables := flags.Int("max-tables", 20, "largest tables included in the report")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("preflight accepts flags only")
	}
	key := strings.TrimSpace(*sourceEnv)
	if key == "" {
		return fmt.Errorf("-source-env cannot be empty")
	}
	dsn, err := resolveSourceDSN(*envFile, key, *databaseName)
	if err != nil {
		return err
	}

	report, err := postgresmigrate.Preflight(ctx, postgresmigrate.Config{
		URL: dsn, Schema: *schema, Timeout: *timeout, MaxTables: *maxTables,
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func runPlan(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	envFile := flags.String("env-file", "", "dotenv file containing the PostgreSQL source URL")
	sourceEnv := flags.String("source-env", defaultSourceEnv, "environment variable containing the PostgreSQL source URL")
	databaseName := flags.String("database", "", "override the database name from the source URL")
	schema := flags.String("schema", "public", "PostgreSQL source schema")
	table := flags.String("table", "", "PostgreSQL source table")
	timeout := flags.Duration("timeout", 30*time.Second, "whole schema inspection timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*table) == "" {
		return fmt.Errorf("plan requires -table and accepts flags only")
	}
	dsn, err := resolveSourceDSN(*envFile, strings.TrimSpace(*sourceEnv), *databaseName)
	if err != nil {
		return err
	}
	report, err := postgresmigrate.InspectTable(ctx, postgresmigrate.Config{
		URL: dsn, Schema: *schema, Timeout: *timeout,
	}, *table)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func runPrepare(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("prepare", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	sourceEnvFile := flags.String("source-env-file", "", "dotenv file containing the PostgreSQL source URL")
	sourceEnv := flags.String("source-env", defaultSourceEnv, "environment variable containing the PostgreSQL source URL")
	sourceDatabase := flags.String("source-database", "", "override the source database name")
	sourceSchema := flags.String("source-schema", "public", "PostgreSQL source schema")
	sourceTable := flags.String("source-table", "", "PostgreSQL source table")
	targetEnvFile := flags.String("target-env-file", "", "dotenv file containing the KitDB target URL")
	targetEnv := flags.String("target-env", "KITDB_MIGRATE_TARGET_URL", "environment variable containing the KitDB target URL")
	targetDatabase := flags.String("target-database", "", "logical KitDB target database")
	capability := flags.String("capability", "", "source-declared KitDB capability used to create the target")
	timeout := flags.Duration("timeout", 30*time.Second, "source inspection timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourceTable) == "" ||
		strings.TrimSpace(*targetDatabase) == "" || strings.TrimSpace(*capability) == "" {
		return fmt.Errorf("prepare requires -source-table, -target-database and -capability")
	}
	sourceDSN, err := resolveSourceDSN(*sourceEnvFile, strings.TrimSpace(*sourceEnv), *sourceDatabase)
	if err != nil {
		return err
	}
	targetDSN, err := resolveEnvDSN(*targetEnvFile, strings.TrimSpace(*targetEnv))
	if err != nil {
		return err
	}
	schema, err := postgresmigrate.InspectTable(ctx, postgresmigrate.Config{
		URL: sourceDSN, Schema: *sourceSchema, Timeout: *timeout,
	}, *sourceTable)
	if err != nil {
		return err
	}
	plan, err := postgresmigrate.BuildTargetPlan(schema)
	if err != nil {
		return err
	}
	report, err := postgresmigrate.PrepareTarget(ctx, targetDSN, *targetDatabase, *capability, plan)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func runMigrate(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	sourceEnvFile := flags.String("source-env-file", "", "dotenv file containing the PostgreSQL source URL")
	sourceEnv := flags.String("source-env", defaultSourceEnv, "environment variable containing the PostgreSQL source URL")
	sourceDatabase := flags.String("source-database", "", "override the source database name")
	sourceSchema := flags.String("source-schema", "public", "PostgreSQL source schema")
	sourceTable := flags.String("source-table", "", "PostgreSQL source table")
	targetEnvFile := flags.String("target-env-file", "", "dotenv file containing the KitDB target URL")
	targetEnv := flags.String("target-env", "KITDB_MIGRATE_TARGET_URL", "environment variable containing the KitDB target URL")
	targetDatabase := flags.String("target-database", "", "logical KitDB target database")
	migrationID := flags.String("id", "", "stable migration scope identifier")
	chunkRows := flags.Int("chunk-rows", 512, "maximum rows in one KitDB transaction")
	chunkBytes := flags.Int64("chunk-bytes", 8<<20, "maximum canonical source bytes in one KitDB transaction")
	bulk := flags.Bool("bulk", true, "use the bounded KitDB bulk checkpoint policy")
	maxRows := flags.Int64("max-rows", 0, "bounded canary row limit; zero means the complete source snapshot")
	timeout := flags.Duration("timeout", 30*time.Minute, "whole migration timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourceTable) == "" ||
		strings.TrimSpace(*targetDatabase) == "" || strings.TrimSpace(*migrationID) == "" {
		return fmt.Errorf("migrate requires -source-table, -target-database and -id")
	}
	sourceDSN, err := resolveSourceDSN(*sourceEnvFile, strings.TrimSpace(*sourceEnv), *sourceDatabase)
	if err != nil {
		return err
	}
	targetDSN, err := resolveEnvDSN(*targetEnvFile, strings.TrimSpace(*targetEnv))
	if err != nil {
		return err
	}
	schema, err := postgresmigrate.InspectTable(ctx, postgresmigrate.Config{
		URL: sourceDSN, Schema: *sourceSchema, Timeout: 30 * time.Second,
	}, *sourceTable)
	if err != nil {
		return err
	}
	plan, err := postgresmigrate.BuildTargetPlan(schema)
	if err != nil {
		return err
	}
	migrationContext := ctx
	var cancel context.CancelFunc
	if *timeout > 0 {
		migrationContext, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}
	report, err := postgresmigrate.Migrate(migrationContext, postgresmigrate.MigrationConfig{
		SourceURL: sourceDSN, TargetURL: targetDSN, TargetDB: *targetDatabase,
		ID: *migrationID, Plan: plan, Bulk: *bulk, ChunkRows: *chunkRows,
		ChunkBytes: *chunkBytes, MaxRows: *maxRows,
		Progress: func(progress postgresmigrate.MigrationProgress) {
			fmt.Fprintf(
				os.Stderr,
				"kitdbmigrate: chunk=%d chunk_rows=%d rows=%d bytes=%d elapsed=%s\n",
				progress.Chunk, progress.ChunkRows, progress.Rows, progress.Bytes, progress.Elapsed.Round(time.Millisecond),
			)
		},
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func runVerifyTarget(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	targetEnvFile := flags.String("target-env-file", "", "dotenv file containing the KitDB target URL")
	targetEnv := flags.String("target-env", "KITDB_MIGRATE_TARGET_URL", "environment variable containing the KitDB target URL")
	targetDatabase := flags.String("target-database", "", "logical KitDB target database")
	table := flags.String("table", "", "target KitDB table")
	migrationID := flags.String("id", "", "migration scope identifier")
	timeout := flags.Duration("timeout", 5*time.Minute, "verification timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*targetDatabase) == "" ||
		strings.TrimSpace(*table) == "" || strings.TrimSpace(*migrationID) == "" {
		return fmt.Errorf("verify requires -target-database, -table and -id")
	}
	targetDSN, err := resolveEnvDSN(*targetEnvFile, strings.TrimSpace(*targetEnv))
	if err != nil {
		return err
	}
	verifyContext, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	report, err := postgresmigrate.VerifyTargetCount(
		verifyContext, targetDSN, *targetDatabase, *table, *migrationID,
	)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func resolveEnvDSN(envFile, key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("environment variable name cannot be empty")
	}
	dsn, found := os.LookupEnv(key)
	if !found && strings.TrimSpace(envFile) != "" {
		var err error
		dsn, found, err = readEnvValue(envFile, key)
		if err != nil {
			return "", err
		}
	}
	if !found || strings.TrimSpace(dsn) == "" {
		return "", fmt.Errorf("URL is not configured in environment variable %s", key)
	}
	return dsn, nil
}

func resolveSourceDSN(envFile, key, databaseName string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("-source-env cannot be empty")
	}
	dsn, found := os.LookupEnv(key)
	if !found && strings.TrimSpace(envFile) != "" {
		var err error
		dsn, found, err = readEnvValue(envFile, key)
		if err != nil {
			return "", err
		}
	}
	if !found || strings.TrimSpace(dsn) == "" {
		return "", fmt.Errorf("source URL is not configured in environment variable %s", key)
	}
	if strings.TrimSpace(databaseName) != "" {
		var err error
		dsn, err = overrideURLDatabase(dsn, databaseName)
		if err != nil {
			return "", err
		}
	}
	return dsn, nil
}

func overrideURLDatabase(dsn, database string) (string, error) {
	database = strings.TrimSpace(database)
	if database == "" {
		return "", fmt.Errorf("database override cannot be empty")
	}
	if strings.IndexByte(database, 0) >= 0 {
		return "", fmt.Errorf("database override contains NUL")
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("source URL cannot accept a database override")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "postgres", "postgresql":
	default:
		return "", fmt.Errorf("source URL cannot accept a database override")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("source URL cannot accept a database override")
	}
	parsed.Path = "/" + database
	parsed.RawPath = ""
	return parsed.String(), nil
}

func readEnvValue(path, key string) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", false, fmt.Errorf("open env file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var (
		value string
		found bool
	)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		name, raw, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		if found {
			return "", false, fmt.Errorf("environment variable %s is declared more than once", key)
		}
		decoded, err := decodeEnvValue(strings.TrimSpace(raw))
		if err != nil {
			return "", false, fmt.Errorf("decode environment variable %s: %w", key, err)
		}
		value, found = decoded, true
	}
	if err := scanner.Err(); err != nil {
		return "", false, fmt.Errorf("read env file: %w", err)
	}
	return value, found, nil
}

func decodeEnvValue(value string) (string, error) {
	if len(value) < 2 {
		return value, nil
	}
	switch value[0] {
	case '\'':
		if value[len(value)-1] != '\'' {
			return "", fmt.Errorf("unterminated single-quoted value")
		}
		return value[1 : len(value)-1], nil
	case '"':
		if value[len(value)-1] != '"' {
			return "", fmt.Errorf("unterminated double-quoted value")
		}
		decoded, err := strconv.Unquote(value)
		if err != nil {
			return "", err
		}
		return decoded, nil
	default:
		return value, nil
	}
}

func usageError() error {
	return fmt.Errorf("usage: kitdbmigrate <preflight|plan|fts-profile|search-canary|search-identity|search-adopt|prepare|migrate|verify> [flags]")
}
