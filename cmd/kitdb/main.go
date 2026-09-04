// Command kitdb provides a small, machine-readable operator surface over the
// verified KitDB kernel primitives. It deliberately contains no storage or
// durability logic of its own.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/node"
	"github.com/kitwork/engine/kitdb/relational"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const commandFormat = "kitdb-cli/v1"

type commandResponse struct {
	Format  string `json:"format"`
	Command string `json:"command"`
	Result  any    `json:"result"`
}

type inspectResult struct {
	Path           string               `json:"path"`
	DatabaseID     string               `json:"database_id"`
	CatalogVersion kitdb.CatalogVersion `json:"catalog_version"`
	Stats          kitdb.Stats          `json:"stats"`
	Pins           []kitdb.HistoryPin   `json:"history_pins,omitempty"`
}

type verifyResult struct {
	Path       string      `json:"path"`
	DatabaseID string      `json:"database_id"`
	Verified   bool        `json:"verified"`
	Stats      kitdb.Stats `json:"stats"`
}

type backupResult struct {
	Anchor                kitdb.BackupAnchor `json:"anchor"`
	Pin                   kitdb.HistoryPin   `json:"pin"`
	CheckpointTransaction uint64             `json:"checkpoint_transaction"`
	Resumed               bool               `json:"resumed"`
}

type queryResult struct {
	Execution  *relational.ExecutionStats `json:"execution,omitempty"`
	Path       string                     `json:"path"`
	Columns    []relational.Column        `json:"columns,omitempty"`
	Rows       [][]any                    `json:"rows,omitempty"`
	Affected   int64                      `json:"affected"`
	CommandTag string                     `json:"command_tag"`
}

type serveResult struct {
	Path     string `json:"path"`
	Database string `json:"database"`
	Listen   string `json:"listen"`
	Stopped  bool   `json:"stopped"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "kitdb: %v\n", err)
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
	command := strings.ToLower(strings.TrimSpace(args[0]))
	var result any
	var err error
	switch command {
	case "version":
		if len(args) != 1 {
			return fmt.Errorf("version accepts no arguments")
		}
		result = kitdb.CurrentCompatibility()
	case "doctor":
		result, err = runDoctor(ctx, args[1:])
	case "inspect":
		result, err = runInspect(args[1:])
	case "catalog":
		result, err = runCatalog(args[1:])
	case "query":
		result, err = runQuery(ctx, args[1:])
	case "refresh-projections":
		result, err = runRefreshProjections(ctx, args[1:])
	case "projections":
		result, err = runProjectionStatus(ctx, args[1:])
	case "serve":
		result, err = runServe(ctx, args[1:])
	case "verify":
		result, err = runVerify(ctx, args[1:])
	case "backup":
		result, err = runBackup(ctx, args[1:])
	case "restore":
		result, err = runRestore(ctx, args[1:])
	case "restore-time":
		result, err = runRestoreTime(ctx, args[1:])
	case "help", "-h", "--help":
		return usageError()
	default:
		return fmt.Errorf("unknown command %q; %w", command, usageError())
	}
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(commandResponse{
		Format: commandFormat, Command: command, Result: result,
	})
}

func runQuery(ctx context.Context, args []string) (queryResult, error) {
	flags := newFlagSet("query")
	projections := flags.Bool("experimental-projections", false, "read experimental .analytics/.search snapshot files; explicit refresh required")
	projectionPolicySource := flags.String("projection-open-policy", "lazy", "projection admission: lazy, validate, or require-ready")
	batch := flags.Bool("batch-aggregates", false, "try bounded typed batches for numeric aggregate scans")
	create := flags.Bool("create", false, "create the database file when it does not exist")
	readOnly := flags.Bool("readonly", false, "accept SELECT only")
	parametersJSON := flags.String("params", "[]", "bound parameters as one JSON array")
	maximumResultRows := flags.Int("max-result-rows", relational.DefaultMaximumResultRows, "maximum result rows")
	maximumMutationRows := flags.Int("max-mutation-rows", relational.DefaultMaximumMutationRows, "maximum mutation rows")
	searchRoot := flags.String("search-root", "", "search projection directory")
	searchNamespace := flags.String("search-namespace", "", "stable search projection namespace")
	maximumSearchResults := flags.Int("max-search-results", 0, "maximum SEARCH result rows; defaults from max-result-rows")
	maximumSearchCandidates := flags.Int("max-search-candidates", relational.DefaultMaximumSearchCandidates, "maximum filtered SEARCH candidates")
	searchForegroundWait := flags.Duration("search-foreground-wait", relational.DefaultSearchForegroundWait, "projection build foreground wait")
	if err := flags.Parse(args); err != nil {
		return queryResult{}, err
	}
	if flags.NArg() < 2 {
		return queryResult{}, fmt.Errorf("query requires DATABASE and SQL")
	}
	projectionPolicy, err := relational.ParseProjectionOpenPolicy(*projectionPolicySource)
	if err != nil {
		return queryResult{}, err
	}
	path, err := databasePath(flags.Arg(0), *create)
	if err != nil {
		return queryResult{}, err
	}
	var parameters []any
	decoder := json.NewDecoder(strings.NewReader(*parametersJSON))
	decoder.UseNumber()
	if err := decoder.Decode(&parameters); err != nil {
		return queryResult{}, fmt.Errorf("decode --params JSON array: %w", err)
	}
	database, err := relational.OpenWithContext(ctx, path, relational.Options{
		ExperimentalProjections: *projections, BatchAggregates: *batch,
		ProjectionOpenPolicy: projectionPolicy,
		MaximumResultRows:    *maximumResultRows, MaximumMutationRows: *maximumMutationRows,
		SearchRoot: *searchRoot, SearchNamespace: *searchNamespace,
		MaximumSearchResults:    *maximumSearchResults,
		MaximumSearchCandidates: *maximumSearchCandidates,
		SearchForegroundWait:    *searchForegroundWait,
	})
	if err != nil {
		return queryResult{}, err
	}
	source := strings.Join(flags.Args()[1:], " ")
	statement, err := relationalStatementReadOnly(source)
	if err != nil {
		_ = database.Close()
		return queryResult{}, err
	}
	if *readOnly && !statement {
		_ = database.Close()
		return queryResult{}, fmt.Errorf("query is read-only")
	}
	result, executeErr := database.Execute(ctx, source, parameters...)
	closeErr := database.Close()
	return queryResult{
		Execution: result.Execution,
		Path:      path, Columns: result.Columns, Rows: result.Rows,
		Affected: result.Affected, CommandTag: result.CommandTag,
	}, errors.Join(executeErr, closeErr)
}

func runRefreshProjections(ctx context.Context, args []string) (relational.ProjectionReport, error) {
	flags := newFlagSet("refresh-projections")
	analyticsOnly := flags.Bool("analytics-only", false, "rebuild only analytics; leave search files and directories untouched")
	if err := flags.Parse(args); err != nil {
		return relational.ProjectionReport{}, err
	}
	if flags.NArg() != 1 {
		return relational.ProjectionReport{}, fmt.Errorf("refresh-projections requires DATABASE")
	}
	path, err := existingDatabasePath(flags.Arg(0))
	if err != nil {
		return relational.ProjectionReport{}, err
	}
	database, err := relational.OpenWithOptions(path, relational.Options{ExperimentalProjections: true})
	if err != nil {
		return relational.ProjectionReport{}, err
	}
	var report relational.ProjectionReport
	var refreshErr error
	if *analyticsOnly {
		report, refreshErr = database.RefreshAnalytics(ctx)
	} else {
		report, refreshErr = database.RefreshProjections(ctx)
	}
	return report, errors.Join(refreshErr, database.Close())
}

func runProjectionStatus(ctx context.Context, args []string) (relational.ProjectionPreflightReport, error) {
	path, err := oneDatabaseArgument("projections", args)
	if err != nil {
		return relational.ProjectionPreflightReport{}, err
	}
	database, err := relational.OpenWithContext(ctx, path, relational.Options{
		ExperimentalProjections: true,
		ProjectionOpenPolicy:    relational.ProjectionOpenLazy,
		Kernel:                  kitdb.OpenOptions{PageCacheBytes: -1},
	})
	if err != nil {
		return relational.ProjectionPreflightReport{}, err
	}
	report, statusErr := database.PreflightProjections(ctx)
	return report, errors.Join(statusErr, database.Close())
}

func relationalStatementReadOnly(source string) (bool, error) {
	envelope, err := kitdbsql.ParseEnvelope(source)
	if err != nil {
		return false, err
	}
	return envelope.Kind == kitdbsql.StatementSelect || envelope.Kind == kitdbsql.StatementExplain, nil
}

func runServe(ctx context.Context, args []string) (serveResult, error) {
	flags := newFlagSet("serve")
	projections := flags.Bool("experimental-projections", false, "read experimental .analytics/.search snapshot files; explicit refresh required")
	projectionPolicySource := flags.String("projection-open-policy", "lazy", "projection admission: lazy, validate, or require-ready")
	batch := flags.Bool("batch-aggregates", false, "try bounded typed batches for numeric aggregate scans")
	create := flags.Bool("create", false, "create the database file when it does not exist")
	databaseName := flags.String("database", "", "logical PostgreSQL database name")
	user := flags.String("user", "kitdb", "PostgreSQL user")
	password := flags.String("password", os.Getenv("KITDB_TOKEN"), "PostgreSQL password; defaults to KITDB_TOKEN")
	listen := flags.String("listen", "127.0.0.1:5433", "loopback listen address")
	readOnly := flags.Bool("readonly", false, "reject SQL writes")
	retainHistory := flags.Bool("retain-history", false, "retain checkpointed WAL history")
	verifyOnOpen := flags.Bool("verify-on-open", false, "verify active storage pages before serving")
	maximumResultRows := flags.Int("max-result-rows", relational.DefaultMaximumResultRows, "maximum result rows")
	maximumMutationRows := flags.Int("max-mutation-rows", relational.DefaultMaximumMutationRows, "maximum mutation rows")
	searchRoot := flags.String("search-root", "", "search projection directory")
	searchNamespace := flags.String("search-namespace", "", "stable search projection namespace")
	maximumSearchResults := flags.Int("max-search-results", 0, "maximum SEARCH result rows; defaults from max-result-rows")
	maximumSearchCandidates := flags.Int("max-search-candidates", relational.DefaultMaximumSearchCandidates, "maximum filtered SEARCH candidates")
	searchForegroundWait := flags.Duration("search-foreground-wait", relational.DefaultSearchForegroundWait, "projection build foreground wait")
	maxConnections := flags.Int("max-connections", 64, "maximum PostgreSQL connections")
	idleTimeout := flags.Duration("idle-timeout", 30*time.Minute, "idle connection timeout")
	queryTimeout := flags.Duration("query-timeout", 30*time.Second, "statement timeout")
	if err := flags.Parse(args); err != nil {
		return serveResult{}, err
	}
	if flags.NArg() != 1 {
		return serveResult{}, fmt.Errorf("serve requires DATABASE")
	}
	if strings.TrimSpace(*password) == "" {
		return serveResult{}, fmt.Errorf("serve requires --password or KITDB_TOKEN")
	}
	projectionPolicy, err := relational.ParseProjectionOpenPolicy(*projectionPolicySource)
	if err != nil {
		return serveResult{}, err
	}
	path, err := databasePath(flags.Arg(0), *create)
	if err != nil {
		return serveResult{}, err
	}
	database, err := relational.OpenWithContext(ctx, path, relational.Options{
		ExperimentalProjections: *projections, BatchAggregates: *batch,
		ProjectionOpenPolicy: projectionPolicy,
		MaximumResultRows:    *maximumResultRows, MaximumMutationRows: *maximumMutationRows,
		SearchRoot: *searchRoot, SearchNamespace: *searchNamespace,
		MaximumSearchResults:    *maximumSearchResults,
		MaximumSearchCandidates: *maximumSearchCandidates,
		SearchForegroundWait:    *searchForegroundWait,
		Kernel:                  kitdb.OpenOptions{RetainHistory: *retainHistory, VerifyOnOpen: *verifyOnOpen},
	})
	if err != nil {
		return serveResult{}, err
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		_ = database.Close()
		return serveResult{}, err
	}
	logicalName := strings.TrimSpace(*databaseName)
	if logicalName == "" {
		logicalName = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	serveErr := database.ServePostgres(ctx, listener, relational.PostgresServerOptions{
		PostgresOptions: relational.PostgresOptions{
			Database: logicalName, User: *user, Password: *password, ReadOnly: *readOnly,
		},
		MaxConnections: *maxConnections, IdleTimeout: *idleTimeout, QueryTimeout: *queryTimeout,
	})
	closeErr := database.Close()
	if serveErr != nil && !errors.Is(serveErr, context.Canceled) {
		return serveResult{}, errors.Join(serveErr, closeErr)
	}
	return serveResult{
		Path: path, Database: logicalName, Listen: listener.Addr().String(), Stopped: true,
	}, closeErr
}

func runCatalog(args []string) (kitdb.CatalogSnapshot, error) {
	path, err := oneDatabaseArgument("catalog", args)
	if err != nil {
		return kitdb.CatalogSnapshot{}, err
	}
	database, err := kitdb.OpenWithOptions(path, kitdb.OpenOptions{PageCacheBytes: -1})
	if err != nil {
		return kitdb.CatalogSnapshot{}, err
	}
	catalog, catalogErr := database.Catalog()
	return catalog, errors.Join(catalogErr, database.Close())
}

func runInspect(args []string) (inspectResult, error) {
	path, err := oneDatabaseArgument("inspect", args)
	if err != nil {
		return inspectResult{}, err
	}
	database, err := kitdb.OpenWithOptions(path, kitdb.OpenOptions{PageCacheBytes: -1})
	if err != nil {
		return inspectResult{}, err
	}
	stats, statsErr := database.Stats()
	catalogVersion, catalogErr := database.CatalogVersion()
	result := inspectResult{
		Path: database.Path(), DatabaseID: database.ID(),
		CatalogVersion: catalogVersion, Stats: stats,
	}
	if statsErr == nil && catalogErr == nil && stats.HistoryEnabled {
		result.Pins, statsErr = database.HistoryPins()
	}
	return result, errors.Join(statsErr, catalogErr, database.Close())
}

func runVerify(ctx context.Context, args []string) (verifyResult, error) {
	path, err := oneDatabaseArgument("verify", args)
	if err != nil {
		return verifyResult{}, err
	}
	database, err := kitdb.OpenWithOptions(path, kitdb.OpenOptions{PageCacheBytes: -1})
	if err != nil {
		return verifyResult{}, err
	}
	verifyErr := database.VerifyContext(ctx)
	stats, statsErr := database.Stats()
	result := verifyResult{
		Path: database.Path(), DatabaseID: database.ID(),
		Verified: verifyErr == nil && statsErr == nil, Stats: stats,
	}
	return result, errors.Join(verifyErr, statsErr, database.Close())
}

func runBackup(ctx context.Context, args []string) (backupResult, error) {
	flags := newFlagSet("backup")
	pinName := flags.String("pin", "backup/cli", "durable source history pin")
	if err := flags.Parse(args); err != nil {
		return backupResult{}, err
	}
	if flags.NArg() != 2 {
		return backupResult{}, fmt.Errorf("backup requires SOURCE and DESTINATION")
	}
	source, err := existingDatabasePath(flags.Arg(0))
	if err != nil {
		return backupResult{}, err
	}
	destination, err := filepath.Abs(flags.Arg(1))
	if err != nil {
		return backupResult{}, fmt.Errorf("resolve backup destination: %w", err)
	}
	manager, err := node.NewManager(node.Limits{})
	if err != nil {
		return backupResult{}, err
	}
	options := kitdb.OpenOptions{
		PageCacheBytes: -1,
		RetainHistory:  true,
	}
	ticket, scheduleErr := manager.ScheduleBackup(ctx, node.BackupRequest{
		Source: source, Destination: destination, Options: options,
		PinName: *pinName, Priority: node.MaintenanceUrgent,
	})
	if scheduleErr != nil {
		return backupResult{}, errors.Join(scheduleErr, manager.Close())
	}
	completed, waitErr := ticket.Wait(ctx)
	closeErr := manager.Close()
	if waitErr != nil || completed.Backup == nil {
		if waitErr == nil {
			waitErr = fmt.Errorf("backup completed without a verified result")
		}
		return backupResult{}, errors.Join(waitErr, closeErr)
	}
	return backupResult{
		Anchor:                completed.Backup.Anchor,
		Pin:                   completed.Backup.Pin,
		CheckpointTransaction: completed.Backup.CheckpointTransaction,
		Resumed:               completed.Backup.Resumed,
	}, closeErr
}

func runRestore(ctx context.Context, args []string) (kitdb.RestoreResult, error) {
	flags := newFlagSet("restore")
	history := flags.String("history", "", "source retained-history directory")
	transactionText := flags.String("transaction", "", "target transaction; defaults to anchor")
	if err := flags.Parse(args); err != nil {
		return kitdb.RestoreResult{}, err
	}
	if flags.NArg() != 2 {
		return kitdb.RestoreResult{}, fmt.Errorf("restore requires ANCHOR and DESTINATION")
	}
	anchorPath, err := existingDatabasePath(flags.Arg(0))
	if err != nil {
		return kitdb.RestoreResult{}, err
	}
	anchor, err := kitdb.VerifyBackupAnchor(ctx, anchorPath)
	if err != nil {
		return kitdb.RestoreResult{}, err
	}
	transaction := anchor.Transaction
	if strings.TrimSpace(*transactionText) != "" {
		transaction, err = strconv.ParseUint(strings.TrimSpace(*transactionText), 10, 64)
		if err != nil {
			return kitdb.RestoreResult{}, fmt.Errorf("invalid restore transaction: %w", err)
		}
	}
	return kitdb.RestoreToTransaction(
		ctx, anchorPath, strings.TrimSpace(*history), flags.Arg(1), transaction,
	)
}

func runRestoreTime(ctx context.Context, args []string) (kitdb.TimeRestoreResult, error) {
	flags := newFlagSet("restore-time")
	at := flags.String("at", "", "RFC3339 point-in-time target")
	if err := flags.Parse(args); err != nil {
		return kitdb.TimeRestoreResult{}, err
	}
	if flags.NArg() != 2 || strings.TrimSpace(*at) == "" {
		return kitdb.TimeRestoreResult{}, fmt.Errorf(
			"restore-time requires --at RFC3339 SOURCE and DESTINATION",
		)
	}
	source, err := existingDatabasePath(flags.Arg(0))
	if err != nil {
		return kitdb.TimeRestoreResult{}, err
	}
	target, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*at))
	if err != nil {
		return kitdb.TimeRestoreResult{}, fmt.Errorf("invalid restore time: %w", err)
	}
	database, err := kitdb.OpenWithOptions(source, kitdb.OpenOptions{
		PageCacheBytes: -1,
		VerifyOnOpen:   true,
		RetainHistory:  true,
	})
	if err != nil {
		return kitdb.TimeRestoreResult{}, err
	}
	result, restoreErr := database.RestoreToTime(ctx, flags.Arg(1), target)
	return result, errors.Join(restoreErr, database.Close())
}

func oneDatabaseArgument(command string, args []string) (string, error) {
	flags := newFlagSet(command)
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if flags.NArg() != 1 {
		return "", fmt.Errorf("%s requires DATABASE", command)
	}
	return existingDatabasePath(flags.Arg(0))
}

func existingDatabasePath(path string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("resolve database path: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect database path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("database path %q is not a regular file", absolute)
	}
	return absolute, nil
}

func databasePath(path string, create bool) (string, error) {
	if !create {
		return existingDatabasePath(path)
	}
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("resolve database path: %w", err)
	}
	if info, err := os.Stat(absolute); err == nil && !info.Mode().IsRegular() {
		return "", fmt.Errorf("database path %q is not a regular file", absolute)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect database path: %w", err)
	}
	return absolute, nil
}

func newFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}

func usageError() error {
	return fmt.Errorf(
		"usage: kitdb <version|query|serve|refresh-projections|projections|doctor|inspect|catalog|verify|backup|restore|restore-time> [options]",
	)
}
